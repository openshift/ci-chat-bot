#!/usr/bin/env python

import argparse
import json
import logging
import subprocess

import boto3

logging.basicConfig(format='%(asctime)s - %(levelname)s - %(message)s')
logger = logging.getLogger('rosaCleanup')
logger.setLevel(logging.INFO)


def get_rosa_clusters():
    result = subprocess.run(['rosa', 'list', 'clusters', '-o', 'json'], capture_output=True)
    result.check_returncode()
    clusters = json.loads(result.stdout)
    logger.debug(f'Found {len(clusters)} ROSA Clusters')
    return clusters


def get_ec2_resources():
    client = boto3.client('ec2')

    return {
        'instances': get_ec2_instances(client),
        'securityGroups': get_aws_paginated_resources(client, 'describe_security_groups', 'SecurityGroups'),
        'volumes': get_aws_paginated_resources(client, 'describe_volumes', 'Volumes'),
        # 'snapshots': get_aws_paginated_resources(client, 'describe_snapshots', 'Snapshots'),
        'networkInterfaces': get_aws_paginated_resources(client, 'describe_network_interfaces', 'NetworkInterfaces')
    }


def get_elb_resources():
    client = boto3.client('elbv2')

    return {
        'loadBalancers': get_aws_paginated_resources(client, 'describe_load_balancers', 'LoadBalancers'),
        'targetGroups': get_aws_paginated_resources(client, 'describe_target_groups', 'TargetGroups'),
    }


def get_iam_resources():
    client = boto3.client('iam')

    return {
        'roles': get_aws_paginated_resources(client, 'list_roles', 'Roles'),
        'policies': get_aws_paginated_resources(client, 'list_policies', 'Policies'),
        'identityProviders': get_iam_identity_providers(client),
    }


def delete_iam_roles(clusters, resources):
    client = boto3.client('iam')
    roles = []
    for role in resources['roles']:
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
            response = client.detach_role_policy(
                RoleName=role,
                PolicyArn=policy['PolicyArn']
            )

        response = client.delete_role(
            RoleName=role
        )


def get_s3_resources():
    client = boto3.client('s3')

    s3_data = {}

    buckets = get_s3_buckets(client)

    for bucket in buckets:
        logger.info(f'Bucket: {bucket}')
        s3_data.update({bucket: get_aws_paginated_resources(client, 'list_objects_v2', 'Contents', Bucket=bucket)})

    return s3_data


def get_aws_paginated_resources(client, operation_name, response_key, **paginator_args):
    resources = []

    paginator = client.get_paginator(operation_name)
    page_iterator = paginator.paginate(**paginator_args)

    for page in page_iterator:
        if response_key in page:
            resources.extend(page[response_key])

    logger.debug(f'Retrieved: {len(resources)} {response_key} resources')
    return resources


def get_ec2_instances(client):
    instances = []
    reservations = get_aws_paginated_resources(client, 'describe_instances', 'Reservations')

    for reservation in reservations:
        if 'Instances' in reservation:
            instances.extend(reservation['Instances'])

    logger.debug(f'Retrieved: {len(instances)} EC2 Instances')
    return instances


def get_iam_identity_providers(client):
    providers = {}
    response = client.list_open_id_connect_providers()

    if 'OpenIDConnectProviderList' in response:
        for item in response['OpenIDConnectProviderList']:
            if 'Arn' in item:
                providers.update({item['Arn']: get_aws_paginated_resources(client, 'list_open_id_connect_provider_tags', 'Tags', OpenIDConnectProviderArn=item['Arn'])})

    logger.debug(f'Retrieved: {len(providers)} IAM Identity Providers')
    return providers


def get_s3_buckets(client):
    buckets = []
    response = client.list_buckets()

    if 'Buckets' in response:
        for item in response['Buckets']:
            if 'Name' in item:
                buckets.append(item['Name'])

    logger.debug(f'Retrieved: {len(buckets)} S3 Buckets')
    return buckets


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description='ROSA Cleanup')

    config_group = parser.add_argument_group('Configuration Options')
    parser.add_argument('--execute', help='Specify to persist changes on the cluster', action='store_true')
    config_group.add_argument('-v', '--verbose', help='Enable verbose output.', action='store_true')

    aws_group = parser.add_argument_group('AWS Configuration Options')
    aws_group.add_argument('-r', '--region', help='Region to run in (default is "us-east-1").', default='us-east-1')
    aws_group.add_argument('-p', '--profile', help='AWS Profile configuration to run (default is "default").', default='default')

    args = vars(parser.parse_args())

    if args['verbose']:
        logger.setLevel(logging.DEBUG)

    # logger.info(json.dumps(args, indent=4, default=str))

    logger.debug('Setting AWS profile to: %s', args['profile'])
    logger.debug('Setting region to: %s', args['region'])
    boto3.setup_default_session(profile_name=args['profile'], region_name=args['region'])

    # Gather the current list of ROSA clusters
    rosa_clusters = get_rosa_clusters()

    # We're currently running into issues with hitting the IAM Roles limit (1000)...
    iam_resources = get_iam_resources()
    delete_iam_roles(rosa_clusters, iam_resources)

    # Other resources to check for...
    # ec2_resources = get_ec2_resources()
    # elb_resources = get_elb_resources()
    # s3_resources = get_s3_resources()
