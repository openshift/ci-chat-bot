# Development Scripts Configuration

## Environment Variables

### Required Variables
These must be set before running any hack scripts:

```bash
export BOT_TOKEN="your-slack-bot-token"
export BOT_SIGNING_SECRET="your-slack-signing-secret"
```

### Optional Configuration

#### Organizational Data Backend

**Option 1: Use GCS Backend (Production)**
```bash
export USE_GCS_ORGDATA=true
export GCS_BUCKET="resolved-org"                    # Default: resolved-org
export GCS_OBJECT_PATH="orgdata/comprehensive_index_dump.json"  # Default path
export GCS_PROJECT_ID="openshift-crt-mce"           # Default project
export GCS_CHECK_INTERVAL="5m"                      # Default: 5 minutes
export GCS_CREDENTIALS_JSON='{"type":"service_account",...}'  # Optional: explicit creds
```

**Option 2: Use Local Files (Development)**
```bash
export ORGDATA_PATHS="/path/to/your/comprehensive_index_dump.json"
# Default: test-data/comprehensive_index_dump.json (relative to ci-chat-bot)
# You can generate this file using the Python orglib indexing system
```

#### Authorization Configuration
```bash
export AUTH_CONFIG="/path/to/your/authorization.yaml"
# Default: ./test-authorization.yaml (relative to ci-chat-bot root)
```

## GCS Authentication Setup

### Using Application Default Credentials (Recommended)
```bash
# Authenticate with gcloud
gcloud auth login
gcloud config set project openshift-crt-mce
```

### Using Service Account (Production)
```bash
# Set credentials via environment variable
export GCS_CREDENTIALS_JSON='{"type":"service_account",...}'

# OR via file
export GOOGLE_APPLICATION_CREDENTIALS="/path/to/service-account.json"
```

### GCS Bucket Security
The GCS bucket should be configured with:
- ✅ **Public access prevention**: Enforced
- ✅ **Uniform bucket-level access**: Enabled  
- ✅ **IAM-based access control**: Project members only
- ✅ **Bucket-level encryption**: Enabled

## Directory Structure Assumptions

The scripts assume this directory layout (relative to ci-chat-bot):
```
workspace/
├── ci-chat-bot/          # This repository
│   ├── hack/
│   │   ├── run.sh        # Main development script
│   │   └── run-with-gcs.sh  # GCS convenience script
│   └── test-authorization.yaml  # Default auth config
├── test-data/           # Test data and examples
│   ├── comprehensive_index_dump.json  # Sample orgdata file
│   └── orgdata.json                   # Legacy test data
└── release/              # OpenShift release repository (required)
    ├── ci-operator/
    └── core-services/
```

## Usage Examples

### Quick Start with GCS
```bash
# Set required tokens
export BOT_TOKEN="xoxb-your-token"
export BOT_SIGNING_SECRET="your-secret"

# Use GCS backend
./hack/run-with-gcs.sh
```

### Development with Local Files
```bash
# Set required tokens
export BOT_TOKEN="xoxb-your-token"
export BOT_SIGNING_SECRET="your-secret"

# Point to your local orgdata file
export ORGDATA_PATHS="/your/path/to/comprehensive_index_dump.json"

# Run with local file backend
./hack/run.sh
```

### Custom Configuration
```bash
# Required tokens
export BOT_TOKEN="xoxb-your-token"
export BOT_SIGNING_SECRET="your-secret"

# Custom GCS configuration
export USE_GCS_ORGDATA=true
export GCS_BUCKET="my-org-bucket"
export GCS_PROJECT_ID="my-project"
export GCS_CREDENTIALS_JSON="$(cat /path/to/service-account.json)"

# Custom auth config
export AUTH_CONFIG="/path/to/my-auth-config.yaml"

./hack/run.sh
```

## Script Behavior

1. **`hack/run.sh`** - Main development script
   - Detects GCS vs local file mode via `USE_GCS_ORGDATA`
   - Uses sensible defaults for file paths relative to project
   - Extracts secrets from OpenShift CI clusters
   - Builds and runs ci-chat-bot with appropriate flags

2. **`hack/run-with-gcs.sh`** - Convenience wrapper
   - Sets `USE_GCS_ORGDATA=true` 
   - Uses production GCS defaults
   - Calls `hack/run.sh`

## Troubleshooting

### File Not Found Errors
If you see errors about missing files:
1. Check that `ORGDATA_PATHS` points to a valid file
2. Generate or obtain a `comprehensive_index_dump.json` file from your orgdata system
3. Verify the `../release` directory exists (OpenShift release repo)

To generate organizational data:
- Use the Python `orglib` indexing system to create `comprehensive_index_dump.json`
- Or obtain the file from your organization's data pipeline
- See the cyborg/org_tools project for data generation examples

### GCS Authentication Errors
If GCS fails to authenticate:
1. **Check authentication**: `gcloud auth list`
2. **Test access**: `gcloud storage ls gs://resolved-org/orgdata/`
3. **Verify permissions**: Check bucket IAM settings
4. **Try service account**: Set `GCS_CREDENTIALS_JSON` if ADC fails

Common GCS errors:
- **"Authentication failed"**: Run `gcloud auth login`
- **"Access denied"**: Check bucket IAM permissions
- **"Object not found"**: Verify `GCS_BUCKET` and `GCS_OBJECT_PATH`

### Authorization Issues
If authorization is too restrictive:
1. Check `AUTH_CONFIG` points to a valid YAML file
2. Review the authorization rules in that file
3. Set `AUTH_CONFIG=""` to disable authorization for testing

### Migration from File-based to GCS
1. **Upload your existing data**:
   ```bash
   gcloud storage cp comprehensive_index_dump.json gs://resolved-org/orgdata/
   ```
2. **Test GCS access**: `./hack/run-with-gcs.sh`
3. **Update your workflow**: Set `USE_GCS_ORGDATA=true` in your environment
