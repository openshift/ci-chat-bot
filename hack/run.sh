#!/usr/bin/env bash

set -euo pipefail

if [[ -z $BOT_TOKEN ]]; then echo "BOT_TOKEN var must be set"; exit 1; fi
if [[ -z $BOT_SIGNING_SECRET ]]; then echo "BOT_SIGNING_SECRET var must be set"; exit 1; fi

tmp_dir=$(mktemp -d)
tmp_kube=$tmp_dir/kubeconfigs
mkdir -p $tmp_kube
tmp_boskos=$tmp_dir/boskos
tmp_subnets=$tmp_dir/subnets
tmp_oidc_config_id=$tmp_dir/rosa_oidc_config_id
tmp_billing_account_id=$tmp_dir/rosa_billing_account_id
trap 'rm -rf $tmp_dir' EXIT

oc --context app.ci -n ci extract secrets/ci-chat-bot-kubeconfigs --to=${tmp_kube} --confirm
oc --context app.ci -n ci get secrets boskos-credentials -ogo-template={{.data.credentials}} | base64 -d > $tmp_boskos
oc --context app.ci -n ci get secrets ci-chat-bot-slack-app --template='{{index .data "rosa-subnet-ids"}}' | base64 -d > $tmp_subnets
oc --context app.ci -n ci get secrets ci-chat-bot-slack-app --template='{{index .data "rosa-oidc-config-id"}}' | base64 -d > $tmp_oidc_config_id
oc --context app.ci -n ci get secrets ci-chat-bot-slack-app --template='{{index .data "rosa-billing-account-id"}}' | base64 -d > $tmp_billing_account_id
oc --context app.ci -n ci get secrets ci-chat-bot-slack-app --template='{{index .data "sa.ci-chat-bot-mce.dpcr.config"}}' | base64 -d > $tmp_kube/sa.ci-chat-bot-mce.dpcr.config
oc --context app.ci -n ci get secrets ci-chat-bot-slack-app --template='{{index .data "sa.ci-chat-bot-mce.dpcr.token.txt"}}' | base64 -d > $tmp_kube/sa.ci-chat-bot-mce.dpcr.token.txt

work_dir=$(readlink -f $(dirname $0)/..)
make
./ci-chat-bot \
  --force-pr-owner=system:serviceaccount:ci:ci-chat-bot \
  --job-config ${work_dir}/../release/ci-operator/jobs/openshift/release/ \
  --prow-config ${work_dir}/../release/core-services/prow/02_config/_config.yaml \
  --workflow-config-path ${work_dir}/../release/core-services/ci-chat-bot/workflows-config.yaml \
  --rosa-subnetlist-path $tmp_subnets \
  --lease-server-credentials-file $tmp_boskos \
  --override-launch-label "ci-chat-bot-${USER}.openshift.io/launch" \
  --override-rosa-secret-name "ci-chat-bot-rosa-clusters-${USER}" \
  --kubeconfig-dir $tmp_kube \
  --kubeconfig-suffix=.config \
  --rosa-oidcConfigId-path=$tmp_oidc_config_id \
  --rosa-billingAccount-path=$tmp_billing_account_id \
  --disable-rosa
  --v=2
