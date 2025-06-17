#!/usr/bin/env python

import argparse
import hashlib
import json
import logging
import subprocess
import sys

import boto3
from botocore.exceptions import ClientError

logging.basicConfig(format='%(asctime)s - %(levelname)s - %(message)s')
logger = logging.getLogger('rosaCleanup')
logger.setLevel(logging.INFO)

DEFAULT_AWS_ACCOUNT_ID_DIGEST = "ea883d7c898c2bf7cfe007260194c999ba88d570182bcbccac610177b10d7d19"
DEFAULT_OCM_ORG_EXTERNAL_ID_DIGEST = "c12416a2086ae609a81785b339d967ccabd444fefc7d180fdad998b7e4442314"


def get_rosa_clusters():
    result = subprocess.run(['rosa', 'list', 'clusters', '-o', 'json'], capture_output=True)
    result.check_returncode()
    clusters = json.loads(result.stdout)
    logger.info(f'Found {len(clusters)} ROSA Clusters')
    return clusters


def get_ec2_subnets():
    client = boto3.client('ec2')
    return get_aws_paginated_resources(client, 'describe_subnets', 'Subnets')


# We run into issues when we hit the maximum number of user tags (50) on Subnets...
def prune_ec2_subnet_tags(clusters, subnets):
    ec2 = boto3.resource('ec2')

    for subnet in subnets:
        tags = []
        logger.info(f'Processing subnet: {subnet["SubnetId"]}')
        for tag in subnet['Tags']:
            found = False
            if tag['Key'].startswith('kubernetes.io/cluster/'):
                for cluster in clusters:
                    if tag["Key"].endswith(cluster['id']):
                        found = True
                        break
                if not found:
                    tags.append(ec2.Tag(subnet["SubnetId"], tag['Key'], tag['Value']))

        for tag in tags:
            logger.info(f' Deleting tag: {tag.key}:{tag.value}')
            tag.delete()


def get_load_balancer_tags():
    client = boto3.client('elbv2')

    tags = []
    load_balancers = get_aws_paginated_resources(client, 'describe_load_balancers', 'LoadBalancers')

    for chunk in chunks(load_balancers, 20):
        arns = []
        for elb in chunk:
            arns.append(elb['LoadBalancerArn'])

        response = client.describe_tags(ResourceArns=arns)
        tags.extend(response['TagDescriptions'])

    return {
        'loadBalancerTags': tags,
    }


def prune_load_balancers(clusters, resources):
    client = boto3.client('elbv2')
    arns = []
    for lb in resources['loadBalancerTags']:
        if not associated_with_active_cluster(lb['Tags'], clusters):
            arns.append(lb["ResourceArn"])

    for arn in arns:
        logger.info(f' Deleting load balancer: {arn}')
        client.delete_load_balancer(LoadBalancerArn=arn)


def get_elb_target_groups():
    client = boto3.client('elbv2')

    tags = []
    target_groups = get_aws_paginated_resources(client, 'describe_target_groups', 'TargetGroups')

    for chunk in chunks(target_groups, 20):
        arns = []
        for tg in chunk:
            arns.append(tg['TargetGroupArn'])

        response = client.describe_tags(ResourceArns=arns)
        tags.extend(response['TagDescriptions'])

    return {
        'targetGroupTags': tags,
    }


def prune_elb_target_groups(clusters, resources):
    client = boto3.client('elbv2')
    arns = []
    for tg in resources['targetGroupTags']:
        if not associated_with_active_cluster(tg['Tags'], clusters):
            arns.append(tg["ResourceArn"])

    for arn in arns:
        logger.info(f' Deleting target group: {arn}')
        client.delete_target_group(TargetGroupArn=arn)


def get_iam_roles():
    client = boto3.client('iam')
    return get_aws_paginated_resources(client, 'list_roles', 'Roles')


# We run into issues when we hit the IAM Roles limit (1000)...
def prune_iam_roles(clusters, resources):
    client = boto3.client('iam')
    roles = []
    for role in resources:
        if "-kube-system-" in role["RoleName"] or "-openshift-" in role["RoleName"]:
            found = False
            for cluster in clusters:
                if role["RoleName"].startswith(cluster['name']):
                    found = True
                    break

            if not found:
                roles.append(role["RoleName"])
        else:
            logger.info(f'Skipping: {role["RoleName"]}')

    for role in roles:
        attached_policies = get_aws_paginated_resources(client, 'list_attached_role_policies', 'AttachedPolicies', RoleName=role)
        logger.info(f'Found: {len(attached_policies)} Attached Policies')

        for policy in attached_policies:
            _ = client.detach_role_policy(
                RoleName=role,
                PolicyArn=policy['PolicyArn']
            )

        _ = client.delete_role(
            RoleName=role
        )


def get_vpc_endpoints():
    client = boto3.client('ec2')
    return get_aws_paginated_resources(client, 'describe_vpc_endpoints', 'VpcEndpoints')


def prune_vpc_endpoints(clusters, resources):
    ec2 = boto3.client('ec2')

    endpoints = []
    for endpoint in resources:
        if endpoint['State'] != 'rejected':
            continue
        if not associated_with_active_cluster(endpoint['Tags'], clusters):
            endpoints.append(endpoint["VpcEndpointId"])

    if len(endpoints) > 0:
        logger.info(f' Deleting {len(endpoints)} VPC Endpoints: {",".join(endpoints)}')
        response = ec2.delete_vpc_endpoints(VpcEndpointIds=endpoints)

        if 'Unsuccessful' in response and len(response['Unsuccessful']) > 0:
            for resource in response['Unsuccessful']:
                logger.warning(f'  - unable to delete vpc endpoint {resource["ResourceId"]}: {resource["Error"]["Message"]}')


def get_ec2_volumes():
    client = boto3.client('ec2')
    return get_aws_paginated_resources(client, 'describe_volumes', 'Volumes')


def prune_ec2_volumes(clusters, resources):
    ec2 = boto3.resource('ec2')

    volumes = []
    for volume in resources:
        if volume['State'] != 'available':
            continue
        if not associated_with_active_cluster(volume['Tags'], clusters):
            volumes.append(ec2.Volume(volume["VolumeId"]))

    for volume in volumes:
        logger.info(f' Deleting volume: {volume.id}')
        volume.delete()


def get_ec2_snapshots():
    client = boto3.client('ec2')
    filters = [
        {
            'Name': 'tag-key',
            'Values': [
                'ci-chat-bot/launch'
            ]
        }
    ]
    return get_aws_paginated_resources(client, 'describe_snapshots', 'Snapshots', Filters=filters)


def prune_ec2_snapshots(clusters, resources):
    ec2 = boto3.resource('ec2')

    snapshots = []
    for snapshot in resources:
        if not associated_with_active_cluster(snapshot['Tags'], clusters):
            snapshots.append(ec2.Snapshot(snapshot["SnapshotId"]))

    for snapshot in snapshots:
        logger.info(f' Deleting snapshot: {snapshot.id}')
        snapshot.delete()


def get_ec2_security_groups():
    client = boto3.client('ec2')
    return get_aws_paginated_resources(client, 'describe_security_groups', 'SecurityGroups')


def get_ec2_network_interfaces_for_security_group(security_group_id):
    client = boto3.client('ec2')
    filters = [
        {
            'Name': 'group-id',
            'Values': [
                security_group_id
            ]
        }
    ]
    return get_aws_paginated_resources(client, 'describe_network_interfaces', 'NetworkInterfaces', Filters=filters)


def get_ec2_reservations(instance_id):
    client = boto3.client('ec2')
    filters = [
        {
            'Name': 'instance-id',
            'Values': [
                instance_id
            ]
        }
    ]
    return get_aws_paginated_resources(client, 'describe_instances', 'Reservations', Filters=filters)


def prune_ec2_security_groups(clusters, resources):
    ec2 = boto3.resource('ec2')
    client = boto3.client('ec2')
    waiter = client.get_waiter('instance_terminated')

    security_groups = []
    for security_group in resources:
        if 'Tags' in security_group:
            if not associated_with_active_cluster(security_group['Tags'], clusters):
                security_groups.append(ec2.SecurityGroup(security_group["GroupId"]))

    for security_group in security_groups:
        network_interfaces = get_ec2_network_interfaces_for_security_group(security_group.id)
        for network_interface in network_interfaces:
            interface = ec2.NetworkInterface(network_interface['NetworkInterfaceId'])

            if interface.attachment is not None and 'InstanceId' in interface.attachment:
                reservations = get_ec2_reservations(interface.attachment['InstanceId'])

                instances = []
                for reservation in reservations:
                    for instance in reservation['Instances']:
                        logger.info(f' Terminating instance: {instance["InstanceId"]}')
                        ec2.Instance(instance["InstanceId"]).terminate()
                        instances.append(instance["InstanceId"])

                if len(instances) > 0:
                    filters = [
                        {
                            'Name': 'instance-id',
                            'Values': instances
                        }
                    ]
                    logger.info(f' Waiting for {len(instances)} instances to terminate')
                    waiter.wait(InstanceIds=instances, Filters=filters)

            try:
                logger.info(f' Deleting network interface: {interface.id}')
                interface.delete()
            except ClientError as e:
                if e.response['Error']['Code'] != 'InvalidNetworkInterfaceID.NotFound':
                    raise e

        logger.info(f' Deleting security group: {security_group.id}')
        security_group.delete()


def get_s3_buckets():
    client = boto3.client('s3')
    buckets = {}

    for bucket in client.list_buckets()['Buckets']:
        response = client.get_bucket_tagging(Bucket=bucket['Name'])
        buckets.update({bucket['Name']: response['TagSet']})

    logger.info(f'Retrieved: {len(buckets)} Buckets')
    return buckets


def prune_s3_buckets(clusters, resources):
    s3 = boto3.resource('s3')
    buckets = []
    for bucket, tags in resources.items():
        if not associated_with_active_cluster(tags, clusters):
            buckets.append(bucket)

    for bucket in buckets:
        b = s3.Bucket(bucket)
        response = b.objects.delete()
        logger.info(f'Removed: {len(response)} items from bucket: {b.name}')
        b.delete()
        logger.info(f'Deleted bucket: {b.name}')


def get_route53_hosted_zones():
    client = boto3.client('route53')
    return get_aws_paginated_resources(client, 'list_hosted_zones', 'HostedZones')


def prune_route53_hosted_zones(clusters, resources):
    route53 = boto3.client('route53')
    zones = []

    for zone in resources:
        zone_id = zone['Id'].replace('/hostedzone/', '')
        response = route53.list_tags_for_resource(ResourceType='hostedzone', ResourceId=zone_id)

        # Special care needs to be taken for Hosted Zones because deleting the wrong one would be disastrous to the
        # entire environment (be afraid, be very afraid)...
        zone_tags = response['ResourceTagSet']['Tags']
        tags = []
        for tag in zone_tags:
            if tag['Key'].startswith('kubernetes.io/cluster/') or tag['Key'] == 'api.openshift.com/id':
                tags.extend(zone_tags)

        if len(tags) > 0 and not associated_with_active_cluster(tags, clusters):
            zones.append((zone_id, zone['Name']))

    logger.info(f'Found: {len(zones)} orphaned hosted zones')
    for zone in zones:
        response = route53.list_resource_record_sets(HostedZoneId=zone[0])

        changes = []
        for record_set in response['ResourceRecordSets']:
            if record_set['Type'] == 'NS' or record_set['Type'] == 'SOA':
                continue

            changes.append({
                'Action': 'DELETE',
                'ResourceRecordSet': record_set,
            })

        if len(changes) > 0:
            logger.info(f' Deleting: {len(changes)} ResourceRecordSets in zone: {zone[0]}')
            route53.change_resource_record_sets(HostedZoneId=zone[0], ChangeBatch={
                'Comment': 'Pruning orphaned resources',
                'Changes': changes,
            })

        logger.info(f' Deleting hosted zone: {zone[0]} ({zone[1]})')
        route53.delete_hosted_zone(Id=zone[0])


def chunks(lst, n):
    """Yield successive n-sized chunks from lst."""
    for i in range(0, len(lst), n):
        yield lst[i:i + n]


def associated_with_active_cluster(tags, clusters):
    for tag in tags:
        if tag['Key'].startswith('kubernetes.io/cluster/') or tag['Key'] == 'api.openshift.com/id':
            for cluster in clusters:
                if tag["Key"].endswith(cluster['id']) or tag['Value'] == cluster['id']:
                    return True
    return False


def get_aws_paginated_resources(client, operation_name, response_key, **paginator_args):
    resources = []

    paginator = client.get_paginator(operation_name)
    page_iterator = paginator.paginate(**paginator_args)

    for page in page_iterator:
        if response_key in page:
            resources.extend(page[response_key])

    logger.info(f'Retrieved: {len(resources)} {response_key} resources')
    return resources


def validate_cloud_account(account_name, parameters, name, command, digest):
    # If user specifies their own Account override, then they are responsible for their own actions...
    if name in parameters and parameters[name] is not None and len(parameters[name]) > 0:
        logger.warning(f'Using {account_name}: {parameters[name]}')
    else:
        # Verify our "default" Account is correct
        external_id = subprocess.check_output(command, shell=True, text=True).strip()
        external_id_sha256 = hashlib.sha256(external_id.encode("utf-8")).hexdigest()
        if external_id_sha256 != digest:
            logger.error(f'{account_name} mismatch detected!  Please verify your environment and try again!')
            sys.exit(-1)


def pre_flight_check(params):
    validate_cloud_account('OCM Organization External ID',
                           params,
                           'external_id',
                           "rosa whoami --output json | jq -r '.[\"OCM Organization External ID\"]'",
                           DEFAULT_OCM_ORG_EXTERNAL_ID_DIGEST)

    validate_cloud_account('AWS Account ID',
                           params,
                           'account_id',
                           'aws sts get-caller-identity | jq -r .Account',
                           DEFAULT_AWS_ACCOUNT_ID_DIGEST)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description='ROSA Cleanup')

    config_group = parser.add_argument_group('Configuration Options')
    parser.add_argument('--execute', help='Specify to persist changes on the cluster.', action='store_true')
    config_group.add_argument('-v', '--verbose', help='Enable verbose output.', action='store_true')

    aws_group = parser.add_argument_group('AWS Configuration Options')
    aws_group.add_argument('-a', '--account-id', help='Override default AWS Account ID.', default=None)
    aws_group.add_argument('-r', '--region', help='Region to run in (default is "us-east-1").', default='us-east-1')
    aws_group.add_argument('-p', '--profile', help='AWS Profile configuration to run (default is "default").', default='default')

    ocm_group = parser.add_argument_group('OCM Configuration Options')
    ocm_group.add_argument('-e', '--external-id', help='Override default OCM Organization External ID.', default=None)

    args = vars(parser.parse_args())

    if args['verbose']:
        logger.setLevel(logging.DEBUG)

    # logger.info(json.dumps(args, indent=4, default=str))

    # Validate our cloud accounts
    pre_flight_check(args)

    logger.debug('Setting AWS profile to: %s', args['profile'])
    logger.debug('Setting region to: %s', args['region'])
    boto3.setup_default_session(profile_name=args['profile'], region_name=args['region'])

    # Gather the current list of ROSA clusters
    rosa_clusters = get_rosa_clusters()

    # IAM Cleanup
    prune_iam_roles(rosa_clusters, get_iam_roles())

    # Subnets
    prune_ec2_subnet_tags(rosa_clusters, get_ec2_subnets())

    # Load Balancers
    prune_load_balancers(rosa_clusters, get_load_balancer_tags())

    # Target Groups
    prune_elb_target_groups(rosa_clusters, get_elb_target_groups())

    # EC2 Volumes
    prune_ec2_volumes(rosa_clusters, get_ec2_volumes())

    # EC2 Snapshots
    prune_ec2_snapshots(rosa_clusters, get_ec2_snapshots())

    # EC2 Security Groups
    prune_ec2_security_groups(rosa_clusters, get_ec2_security_groups())

    # EC2 VPC Endpoints
    prune_vpc_endpoints(rosa_clusters, get_vpc_endpoints())

    # S3 Buckets
    prune_s3_buckets(rosa_clusters, get_s3_buckets())

    # Route53 Hosted Zones
    prune_route53_hosted_zones(rosa_clusters, get_route53_hosted_zones())
