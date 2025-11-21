#!/usr/bin/env bash

set -euo pipefail

echo "Running ci-chat-bot with GCS organizational data backend"
echo "📖 For detailed setup instructions, see: hack/DEVELOPMENT.md"
echo ""

# Set required environment variables for GCS
export USE_GCS_ORGDATA=true

# GCS Configuration (with your existing values)
export GCS_BUCKET="resolved-org"
export GCS_OBJECT_PATH="orgdata/comprehensive_index.json"
export GCS_PROJECT_ID="openshift-crt"
export GCS_CHECK_INTERVAL="5m"

# Optional: Set custom authorization config
# export AUTH_CONFIG="/path/to/your/auth-config.yaml"

# Optional: Set custom orgdata paths (for local file fallback)
# export ORGDATA_PATHS="/path/to/your/comprehensive_index.json"

# Optional: Use service account credentials instead of ADC
# export GCS_CREDENTIALS_JSON='{"type":"service_account",...}'

echo "GCS Configuration:"
echo "   Bucket: gs://${GCS_BUCKET}/${GCS_OBJECT_PATH}"
echo "   Project: ${GCS_PROJECT_ID}"
echo "   Check Interval: ${GCS_CHECK_INTERVAL}"
echo ""

echo "Required Environment Variables:"
echo "   BOT_TOKEN: ${BOT_TOKEN:-(not set)}"
echo "   BOT_SIGNING_SECRET: ${BOT_SIGNING_SECRET:-(not set)}"
echo ""

if [[ -z "${BOT_TOKEN:-}" ]]; then
  echo "BOT_TOKEN must be set"
  exit 1
fi

if [[ -z "${BOT_SIGNING_SECRET:-}" ]]; then
  echo "BOT_SIGNING_SECRET must be set"
  exit 1
fi

echo "All required variables set. Starting ci-chat-bot with GCS backend..."
echo ""

# Run the main script which will detect USE_GCS_ORGDATA=true and use GCS
exec $(dirname $0)/run.sh
