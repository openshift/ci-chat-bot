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

# Check if GCS is enabled
if [[ "${USE_GCS_ORGDATA:-false}" == "true" ]]; then
  echo "Using GCS backend..."

  # GCS configuration with defaults
  GCS_BUCKET="${GCS_BUCKET:-resolved-org}"
  GCS_OBJECT_PATH="${GCS_OBJECT_PATH:-orgdata/comprehensive_index.json}"
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
  echo "Using local file-based orgdata..."
  # Default orgdata path (can be overridden with ORGDATA_PATHS env var)
  # Users should set ORGDATA_PATHS to point to their comprehensive_index.json file
  default_orgdata="${work_dir}/test-data/comprehensive_index.json"
  ORGDATA_PATHS="${ORGDATA_PATHS:-${default_orgdata}}"
  
  if [[ ! -f "$ORGDATA_PATHS" ]]; then
    echo "⚠️  Warning: Orgdata file not found at: $ORGDATA_PATHS"
    echo "   Set ORGDATA_PATHS environment variable to your comprehensive_index.json file"
    echo "   Example: export ORGDATA_PATHS=\"/path/to/comprehensive_index.json\""
  fi
  orgdata_flags="--orgdata-paths=${ORGDATA_PATHS}"
  echo "Orgdata file: ${ORGDATA_PATHS}"
fi

# Authorization config (relative to project root)
AUTH_CONFIG="${AUTH_CONFIG:-${work_dir}/test-authorization.yaml}"

echo "Building ci-chat-bot..."
make

echo "Starting ci-chat-bot with $(echo $orgdata_flags | cut -d' ' -f1) backend..."
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
  --authorization-config="${AUTH_CONFIG}"
