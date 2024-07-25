#!/usr/bin/env python

import argparse
import hashlib
import json
import logging
import subprocess
import sys

import boto3

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
            if tag['Key'].startswith('kubernetes.io/cluster/'):
                found = False
                for cluster in clusters:
                    if tag["Key"].endswith(cluster['id']):
                        found = True
                        break

                if not found:
                    tags.append(ec2.Tag(subnet["SubnetId"], tag['Key'], tag['Value']))

        for tag in tags:
            logger.info(f' Deleting tag: {tag.key}:{tag.value}')
            tag.delete(DryRun=False)


def chunks(lst, n):
    """Yield successive n-sized chunks from lst."""
    for i in range(0, len(lst), n):
        yield lst[i:i + n]


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
        for tag in lb['Tags']:
            if tag['Key'] == 'api.openshift.com/id':
                found = False
                for cluster in clusters:
                    if tag["Value"] == cluster['id']:
                        found = True
                        break

                if not found:
                    arns.append(lb["ResourceArn"])

    for arn in arns:
        logger.info(f' Deleting load balancer: {arn}')
        client.delete_load_balancer(LoadBalancerArn=arn)


def get_iam_resources():
    client = boto3.client('iam')

    return {
        'roles': get_aws_paginated_resources(client, 'list_roles', 'Roles'),
        'policies': get_aws_paginated_resources(client, 'list_policies', 'Policies'),
        'identityProviders': get_iam_identity_providers(client),
    }


# We run into issues when we hit the IAM Roles limit (1000)...
def prune_iam_roles(clusters, resources):
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
            _ = client.detach_role_policy(
                RoleName=role,
                PolicyArn=policy['PolicyArn']
            )

        _ = client.delete_role(
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

    logger.info(f'Retrieved: {len(resources)} {response_key} resources')
    return resources


def get_ec2_instances(client):
    instances = []
    reservations = get_aws_paginated_resources(client, 'describe_instances', 'Reservations')

    for reservation in reservations:
        if 'Instances' in reservation:
            instances.extend(reservation['Instances'])

    logger.info(f'Retrieved: {len(instances)} EC2 Instances')
    return instances


def get_iam_identity_providers(client):
    providers = {}
    response = client.list_open_id_connect_providers()

    if 'OpenIDConnectProviderList' in response:
        for item in response['OpenIDConnectProviderList']:
            if 'Arn' in item:
                providers.update({item['Arn']: get_aws_paginated_resources(client, 'list_open_id_connect_provider_tags', 'Tags', OpenIDConnectProviderArn=item['Arn'])})

    logger.info(f'Retrieved: {len(providers)} IAM Identity Providers')
    return providers


def get_s3_buckets(client):
    buckets = []
    response = client.list_buckets()

    if 'Buckets' in response:
        for item in response['Buckets']:
            if 'Name' in item:
                buckets.append(item['Name'])

    logger.info(f'Retrieved: {len(buckets)} S3 Buckets')
    return buckets


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
    prune_iam_roles(rosa_clusters, get_iam_resources())

    # Subnets
    prune_ec2_subnet_tags(rosa_clusters, get_ec2_subnets())

    # Load Balancers
    prune_load_balancers(rosa_clusters, get_load_balancer_tags())
