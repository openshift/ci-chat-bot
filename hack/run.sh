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

# Determine orgdata configuration
orgdata_flags=""

# Check if GCS is enabled via environment variable
if [[ "${USE_GCS_ORGDATA:-}" == "true" ]]; then
  echo "Using GCS backend for organizational data..."

  # GCS configuration with defaults
  GCS_BUCKET="${GCS_BUCKET:-resolved-org}"
  GCS_OBJECT_PATH="${GCS_OBJECT_PATH:-orgdata/comprehensive_index_dump.json}"
  GCS_PROJECT_ID="${GCS_PROJECT_ID:-openshift-crt}"
  GCS_CHECK_INTERVAL="${GCS_CHECK_INTERVAL:-5m}"

  echo "GCS Config: gs://${GCS_BUCKET}/${GCS_OBJECT_PATH}"

  orgdata_flags="--gcs-enabled=true \
  --gcs-bucket=${GCS_BUCKET} \
  --gcs-object-path=${GCS_OBJECT_PATH} \
  --gcs-project-id=${GCS_PROJECT_ID} \
  --gcs-check-interval=${GCS_CHECK_INTERVAL}"

  # Add GCS credentials if provided
  if [[ -n "${GCS_CREDENTIALS_JSON:-}" ]]; then
    orgdata_flags="${orgdata_flags} --gcs-credentials-json=${GCS_CREDENTIALS_JSON}"
  fi
else
  echo "⚠️  No organizational data source configured - running in PERMIT ALL mode"
  echo ""
  echo "   All users will have access to all commands (no authorization enforcement)"
  echo ""
  echo "   To enable authorization with GCS:"
  echo "   export USE_GCS_ORGDATA=true"
  echo "   export GCS_BUCKET=your-bucket"
  echo "   export GCS_OBJECT_PATH=orgdata/comprehensive_index_dump.json"
  echo ""
  echo "   Note: Production deployments should use GCS with proper authentication."
  echo ""
  # No flags - will run without orgdata (permit all mode)
fi

# Authorization config (optional - if not set, permit all mode)
AUTH_CONFIG="${AUTH_CONFIG:-}"
if [[ -n "$AUTH_CONFIG" && -f "$AUTH_CONFIG" ]]; then
  echo "Using authorization config: ${AUTH_CONFIG}"
  auth_flags="--authorization-config=${AUTH_CONFIG}"
elif [[ -f "${work_dir}/test-authorization.yaml" ]]; then
  echo "Using default authorization config: ${work_dir}/test-authorization.yaml"
  auth_flags="--authorization-config=${work_dir}/test-authorization.yaml"
else
  echo "No authorization config found - running in PERMIT ALL mode"
  auth_flags=""
fi

echo ""
echo "Building ci-chat-bot..."
make

echo ""
echo "Starting ci-chat-bot..."
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
  --disable-rosa \
  --v=2 \
  $orgdata_flags \
  $auth_flags
