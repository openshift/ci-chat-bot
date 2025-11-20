# Request Command Implementation

This document describes the implementation of the `request` command for the ci-chat-bot, which allows users to request access to a GCP workspace via IAM policy bindings.

## Overview

The `request` command creates temporary service accounts with IAM roles for users requesting GCP project access. Instead of granting IAM permissions directly to user accounts, a service account is created for each user and granted the necessary permissions. Users receive a service account key JSON file via Slack (similar to kubeconfig files) which they use to authenticate. Access is granted for 7 days and automatically expires via IAM policy conditions. Users cannot extend their access; they must either wait for expiration or use the `revoke` command to revoke their current access before requesting new access.

## Command Syntax

**request**: `request <resource> "<justification>"`
- Parameters:
  - `<resource>`: Resource identifier (currently only "gcp-access" supported) - lazy parameter
  - `<justification>`: Business justification - MUST be enclosed in quotes - lazy parameter
  - Note: Both parameters use lazy matching. Quotes are required for justification and are automatically stripped by the parser before storage.
- Example: `request gcp-access "Need to debug CI infrastructure issues"`
- Parsed justification: `Need to debug CI infrastructure issues` (quotes removed)

## Parameter Parsing and Quote Handling

The `request` command uses **lazy parameters** for both `resource` and `justification`:
- Lazy parameters (`<param?>`) capture quoted strings WITH quotes preserved during initial parsing
- The `StringParam()` method automatically strips surrounding quotes before use
- This means users MUST use quotes around justification, but they won't appear in storage

**Example Flow**:
1. User types: `request gcp-access "Need to debug CI infrastructure issues"`
2. Parser captures: `justification = "\"Need to debug CI infrastructure issues\""`
3. StringParam() strips quotes: `justification = "Need to debug CI infrastructure issues"`
4. Stored in BigQuery/IAM: `Need to debug CI infrastructure issues` (no quotes)

**Why Quotes Are Required**:
- Allows justification to contain spaces
- Ensures entire justification is captured as a single parameter
- Consistent with other multi-word parameters in the bot

## Features

- ✅ Parameter-based command interface: `request <resource> "<justification>"`
- ✅ Self-service revocation: `revoke <resource>`
- ✅ Automatic 7-day expiration via IAM conditions
- ✅ Automatic cleanup of expired users (runs hourly)
- ✅ Runtime caching with GCP IAM as source of truth
- ✅ Group-based access validation using cyborg-data (Hybrid Platforms membership required)
- ✅ Business justification tracking in IAM condition descriptions
- ✅ Resource validation (currently supports: gcp-access resource only)
- ✅ Integration with Google Cloud Resource Manager API for IAM policy management
- ✅ **BigQuery audit logging** - All successful credential grants are logged to BigQuery for audit purposes
- ✅ **Reversible email sanitization** - Secure user identification via immutable service account emails (no dependency on mutable DisplayName)

## Files Created/Modified

### New Files

1. **`pkg/manager/gcp_access.go`**
   - `GCPAccessManager` struct with `iamService *iam.Service` field for service account management, runtime cache, and BigQuery client
   - Google Cloud Resource Manager API integration using `googleapi.Error` for proper error handling
   - IAM service integration (`google.golang.org/api/iam/v1`) for service account operations
   - BigQuery integration for audit logging
   - GCP IAM policy as the source of truth (no ConfigMap needed)
   - Service account management methods: `createServiceAccount()`, `createServiceAccountKey()`, `deleteServiceAccount()`
   - Methods: `GrantAccess()` (returns service account key JSON), `RevokeAccess()` (deletes service account), `CleanupExpiredAccess()` (deletes expired service accounts), `GetUserGrant()`, `logAccessGrant()` (includes service account email)
   - IAM policy management with time-based conditions for automatic expiration
   - Helper functions: `parseExpirationFromCondition()`, `extractServiceAccountEmail()`, `extractUserEmailFromServiceAccount()`, `sanitizeUsernameReversible()`, `unsanitizeUsername()`, `needsDisplayNameFallback()`, `isNotFoundError()`
   - Error checking functions using HTTP status codes instead of string matching
   - Each user gets a service account with a unique IAM binding with an expiration condition
   - `UserAccessGrant` struct includes `ServiceAccountEmail` field
   - `AccessGrantLogEntry` struct for BigQuery audit log schema includes `ServiceAccountEmail` field
   - Service account naming convention: `{sanitized-email}-{unix-timestamp}` (6-30 characters)
   - Key regeneration: Re-requesting generates new key for existing service account (handles upload failures)
   - **GCP Eventual Consistency Handling**: `createServiceAccountKey()` implements exponential backoff retry (up to 10 attempts, max 31 seconds) to handle GCP's eventual consistency when a service account is created but not yet available for key generation
   - **IAM Policy Version 3**: All `GetIamPolicy` calls request version 3 and all `SetIamPolicy` calls set `policy.Version = 3` to support conditional bindings (required for time-based expiration)
   - **Custom Role Path**: Uses full resource path format `projects/{project-id}/roles/{role-name}` for custom project-level IAM roles instead of `roles/{role-name}` format (which is for predefined roles)

### Modified Files

1. **`pkg/manager/types.go`**
   - Added `gcpAccessManager` field to `jobManager` struct
   - Added `orgDataService` field to `jobManager` struct for organizational data validation
   - Added `OrgDataService` interface for group membership validation
   - Added interface methods: `GrantGCPAccess()`, `RevokeGCPAccess()`, `GetGCPAccessManager()`, `GetOrgDataService()`

2. **`pkg/manager/manager.go`**
   - Updated `NewJobManager()` to accept `GCPAccessManager` and `OrgDataService` parameters
   - Added `GrantGCPAccess()` implementation - returns service account key JSON bytes along with success message
   - Added `RevokeGCPAccess()` implementation
   - Added `GetOrgDataService()` implementation
   - Added `gcpAccessCleanup()` background job (runs hourly)
   - Updated `Start()` to run cleanup goroutine

3. **`pkg/slack/actions.go`**
   - Added `Request()` command handler for granting access - uploads service account key JSON file to Slack
   - Added `Revoke()` command handler for revoking access
   - Validates user membership in Hybrid Platforms organization using cyborg-data
   - Provides user-friendly response messages with instructions for using service account key
   - Handles upload failures gracefully (user can re-request to get key)

4. **`pkg/slack/slack.go`**
   - Registered `request <resource?> <justification?>` command in `SupportedCommands()`
   - Registered `revoke <resource?>` command in `SupportedCommands()`
   - Added `SendGCPServiceAccountKey()` helper function for uploading service account key JSON files to Slack

5. **`pkg/slack/events/messages/message_handler.go`**
   - Added `request` command to help overview
   - Added detailed help in `GenerateManageHelpMessage()`

6. **`cmd/ci-chat-bot/main.go`**
   - Initialize cyborg-data service for organizational data from GCS
   - Initialize GCP access manager from environment variables
   - Pass both services to `NewJobManager()`

## Configuration

### Environment Variables

The following environment variables must be set for the `request` command to work:

1. **`ORG_DATA_BUCKET`** (required)
   - GCS bucket name containing organizational data
   - Example: `my-org-data-bucket`
   - Used to load employee and team membership information from cyborg-data

2. **`ORG_DATA_OBJECT_PATH`** (optional)
   - Path to the org data JSON file within the bucket
   - Defaults to `orgdata/comprehensive_index_dump.json` if not specified
   - Example: `production/org_data.json`

3. **`GCP_SERVICE_ACCOUNT_JSON`** (required)
   - JSON content of the GCP service account key
   - This credential is shared between two features:
     - **GCP Access Manager**: Creates/manages service accounts and modifies IAM policies for temporary access grants
     - **Organizational Data GCS**: Reads user/group membership data from GCS bucket
   - Service account must have permissions to:
     - Create, delete, and manage service accounts (`roles/iam.serviceAccountAdmin` or similar)
     - Generate service account keys (`roles/iam.serviceAccountKeyAdmin` or similar)
     - Modify IAM policies on the target project (`roles/resourcemanager.projectIamAdmin` or similar)
     - Read objects from the GCS bucket specified in `ORG_DATA_BUCKET` (`roles/storage.objectViewer` or similar)
     - Insert rows into BigQuery for audit logging (`roles/bigquery.dataEditor` on the dataset)
   - Required scopes:
     - `https://www.googleapis.com/auth/cloud-platform` (for IAM service account management)
     - `https://www.googleapis.com/auth/cloud-resource` (for IAM policy management)
     - `https://www.googleapis.com/auth/bigquery` (for audit logging)
     - `https://www.googleapis.com/auth/devstorage.read_only` (for GCS access, included in cloud-platform scope)

4. **`GCP_ACCESS_DRY_RUN`** (optional)
   - Set to `"true"` to enable dry-run mode
   - When enabled, the bot will skip all IAM policy changes but still log to BigQuery
   - Useful for testing without affecting production IAM policies
   - Default: `false` (disabled)

### GCP Project Configuration

The following constants are configured in `pkg/manager/gcp_access.go`:

```go
const (
    // GCP project ID where users need IAM access
    GCPProjectID = "openshift-crt-ephemeral-access"

    // IAM role to grant to users for GCP workspace access
    // This is a custom project-level role, so it must use the full resource path
    GCPIAMRole = "projects/openshift-crt-ephemeral-access/roles/chat_bot_user"

    // Duration for which access is valid (7 days)
    GCPAccessDuration = 7 * 24 * time.Hour

    // Duration before automatic resource cleanup by external GCP process (7 days)
    // Note: This constant is informational only - cleanup is handled by an external
    // process, not the ci-chat-bot. Service accounts are cleaned up after 7-day
    // access expiration via the bot's hourly cleanup job.
    GCPResourceCleanupDuration = 7 * 24 * time.Hour

    // BigQuery dataset and table for audit logging
    BigQueryDataset = "ci_chat_bot"
    BigQueryTable   = "access_grants"
)
```

**Configuration:**
- **GCPProjectID**: Set to `"openshift-crt-ephemeral-access"` - the project where users will be granted temporary access
- **GCPIAMRole**: Set to `"projects/openshift-crt-ephemeral-access/roles/chat_bot_user"` - a custom project-level role. Note: Custom roles must use the full resource path format `projects/{project-id}/roles/{role-name}`, not the `roles/{role-name}` format used for predefined roles
- **GCPAccessDuration**: Access duration of 7 days (enforced via IAM conditions)
- **GCPResourceCleanupDuration**: Resources created by users in the GCP project will be automatically deleted after 7 days by an external GCP cleanup process (this is NOT handled by the ci-chat-bot)
- **BigQueryDataset**: Dataset name for audit logs (`"ci_chat_bot"`)
- **BigQueryTable**: Table name for audit logs (`"access_grants"`)

**Service Account vs Resource Cleanup:**
- **Service Accounts**: Managed by ci-chat-bot, deleted after 7-day access expiration via hourly cleanup job
- **GCP Resources** (VMs, disks, etc.): Managed by external process, deleted after 7 days
- Both have the same 7-day lifecycle to align with access expiration

### Data Storage

The implementation uses **GCP IAM policy** as the single source of truth:

- IAM conditional bindings store all grant metadata (expiration timestamp in condition expression)
- Runtime cache improves performance by avoiding repeated API calls
- Cache is automatically populated from IAM policy when queried
- No Kubernetes ConfigMap needed

### BigQuery Audit Logging

All successful credential grants are logged to BigQuery for audit and compliance purposes:

**Table Schema (`ci_chat_bot.access_grants`):**
```sql
CREATE TABLE `openshift-crt-ephemeral-access.ci_chat_bot.access_grants` (
  timestamp TIMESTAMP,
  command STRING,
  user_email STRING,
  slack_user_id STRING,
  justification STRING,
  resource STRING,
  project_id STRING,
  expires_at TIMESTAMP,
  service_account_email STRING
);
```

**Log Entry Fields:**
- `timestamp`: When the credential was granted (UTC)
- `command`: Always "request"
- `user_email`: Email address of the user granted access
- `slack_user_id`: Slack user ID who made the request
- `justification`: Business justification provided by the user
- `resource`: Resource name (e.g., "gcp-access")
- `project_id`: GCP project ID where access was granted
- `expires_at`: When the access will expire (UTC)
- `service_account_email`: Email of the service account created for the user

**Behavior:**
- Logging is **non-blocking** - failures to log will not prevent credential grants
- If BigQuery client fails to initialize, credential grants still work but won't be logged
- Errors are logged to klog for monitoring

## Setting Up GCP Service Account

### 1. Create Service Account

```bash
gcloud iam service-accounts create ci-chat-bot-gcp-access-creds \
    --display-name="CI Chat Bot GCP Credentials Manager" \
    --project=YOUR_PROJECT_ID
```

### 2. Grant IAM Permissions

Grant the service account permission to create/manage service accounts and modify IAM policies on your target project:

```bash
# Grant service account admin permissions
gcloud projects add-iam-policy-binding YOUR_TARGET_PROJECT_ID \
    --member="serviceAccount:ci-chat-bot-gcp-access-creds@YOUR_PROJECT_ID.iam.gserviceaccount.com" \
    --role="roles/iam.serviceAccountAdmin"

# Grant service account key admin permissions
gcloud projects add-iam-policy-binding YOUR_TARGET_PROJECT_ID \
    --member="serviceAccount:ci-chat-bot-gcp-access-creds@YOUR_PROJECT_ID.iam.gserviceaccount.com" \
    --role="roles/iam.serviceAccountKeyAdmin"

# Grant IAM policy admin permissions
gcloud projects add-iam-policy-binding YOUR_TARGET_PROJECT_ID \
    --member="serviceAccount:ci-chat-bot-gcp-access-creds@YOUR_PROJECT_ID.iam.gserviceaccount.com" \
    --role="roles/resourcemanager.projectIamAdmin"
```

**Note**: These roles allow the service account to create service accounts, generate keys, and manage IAM policies. For production use, consider creating a custom role with minimal permissions:
- `iam.serviceAccounts.create`
- `iam.serviceAccounts.delete`
- `iam.serviceAccounts.get`
- `iam.serviceAccountKeys.create`
- `iam.serviceAccountKeys.delete`
- `resourcemanager.projects.getIamPolicy`
- `resourcemanager.projects.setIamPolicy`

### 2b. Grant BigQuery Permissions (for Audit Logging)

Grant the service account permission to insert data into BigQuery:

```bash
gcloud projects add-iam-policy-binding YOUR_TARGET_PROJECT_ID \
    --member="serviceAccount:ci-chat-bot-gcp-access-creds@YOUR_PROJECT_ID.iam.gserviceaccount.com" \
    --role="roles/bigquery.dataEditor"
```

**Note**: `roles/bigquery.dataEditor` allows the service account to insert rows into BigQuery tables. This is required for audit logging.

### 3. Create BigQuery Dataset and Table

Create the BigQuery dataset and table for audit logging:

```bash
# Create dataset
bq mk --dataset --location=US YOUR_TARGET_PROJECT_ID:ci_chat_bot

# Create table with schema
bq mk --table \
  YOUR_TARGET_PROJECT_ID:ci_chat_bot.access_grants \
  timestamp:TIMESTAMP,command:STRING,user_email:STRING,slack_user_id:STRING,justification:STRING,resource:STRING,project_id:STRING,expires_at:TIMESTAMP,service_account_email:STRING
```

### 4. Create and Download Key

```bash
gcloud iam service-accounts keys create ~/ci-chat-bot-sa-key.json \
    --iam-account=ci-chat-bot-gcp-access-creds@YOUR_PROJECT_ID.iam.gserviceaccount.com
```

### 5. Set Environment Variables in Deployment

Add to your Kubernetes deployment or secret:

```yaml
env:
  - name: GCP_SERVICE_ACCOUNT_JSON
    value: |
      {
        "type": "service_account",
        "project_id": "your-project",
        "private_key_id": "...",
        "private_key": "-----BEGIN PRIVATE KEY-----\n...",
        "client_email": "ci-chat-bot-gcp-access-creds@your-project.iam.gserviceaccount.com",
        "client_id": "...",
        "auth_uri": "https://accounts.google.com/o/oauth2/auth",
        "token_uri": "https://oauth2.googleapis.com/token",
        "auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
        "client_x509_cert_url": "..."
      }
```

Or use a Kubernetes Secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: gcp-access-credentials-config
  namespace: ci
type: Opaque
stringData:
  service-account.json: |
    {...}
```

Then reference in deployment:

```yaml
env:
  - name: GCP_SERVICE_ACCOUNT_JSON
    valueFrom:
      secretKeyRef:
        name: gcp-access-credentials-config
        key: service-account.json
```

## Usage

### For End Users

1. **Request access:**
   ```
   request gcp-access "Need to debug CI infrastructure issues"
   ```

   **Command format:** `request <resource> "<business justification>"`
   - `<resource>`: Resource name (currently only `gcp-access` is supported)
   - `"<business justification>"`: Brief explanation of why access is needed (must be enclosed in quotes)

   **Access Requirements:** You must be a member of the Hybrid Platforms organization to receive access. The bot automatically validates your membership before granting access.

2. **Response (first time):**
   ```
   Confirmed you are a member of the Hybrid Platforms organization. You have been granted access to project "openshift-crt-ephemeral-access".

   You will have access to the project for the next 7 days (until 2025-11-24 15:30 MST, 7 days from now).

   You are responsible for deleting resources you create within this account, however, resources older than 7 days will be deleted automatically.

   To use the service account key:
   1. Download the JSON file from Slack
   2. Set environment variable: export GOOGLE_APPLICATION_CREDENTIALS=/path/to/key.json
   3. Access will expire in 7 days

   Justification: Need to debug CI infrastructure issues
   ```

   The bot will also upload a JSON key file to Slack with filename `gcp-access-{sanitized-email}.json` and a security warning.

3. **Response (already has access - re-requesting):**
   ```
   Confirmed you are a member of the Hybrid Platforms organization. You have been granted access to project "openshift-crt-ephemeral-access".

   You will have access to the project for the next 7 days (until 2025-11-24 15:30 MST, 5 days from now).

   You are responsible for deleting resources you create within this account, however, resources older than 7 days will be deleted automatically.

   To use the service account key:
   1. Download the JSON file from Slack
   2. Set environment variable: export GOOGLE_APPLICATION_CREDENTIALS=/path/to/key.json
   3. Access will expire in 7 days

   Justification: Need to debug CI infrastructure issues
   ```

   A new key file will be uploaded (previous keys remain valid until the service account is deleted).

4. **Revoke access early:**
   ```
   revoke gcp-access
   ```

5. **Response (revoke successful):**
   ```
   Your GCP workspace access has been revoked successfully.
   ```

   The service account and all associated keys are deleted from GCP.

6. **Error responses:**
   - Missing parameters: "Invalid command format. Usage: request <resource> \"<business justification>\""
   - Wrong resource: "Currently, access is only available for the 'gcp-access' resource."
   - Not in Hybrid Platforms (Request command): "You are not a member of the 'Hybrid Platforms' organization. Access can only be granted to Hybrid Platforms members."
   - Not in Hybrid Platforms (Revoke command): "GCP workspace access is only available to members of the Hybrid Platforms organization."
   - Upload failure: Returns success message followed by "\n\n⚠️  Failed to upload key file. Please run the command again to retrieve your credentials."

7. **Response (no active access):**
   ```
   You do not have any active GCP workspace access to revoke.
   ```

### Getting Help

```
help manage
```

Shows the `request` command in the management section.

## Architecture

### Data Flow

1. User sends `request <resource> "<justification>"` command to bot via Slack DM
2. Bot validates command parameters:
   - Checks that resource and justification are provided
   - Validates resource is "gcp-access" (only supported value currently)
3. Bot retrieves user's Slack ID and email from Slack profile
4. Bot validates user membership in Hybrid Platforms using cyborg-data:
   - If organizational data service is not available, returns error message
   - If user is not in Hybrid Platforms, returns error
5. Bot calls `GrantGCPAccess()` on JobManager with user's email, Slack ID, and justification
6. JobManager calls `GrantAccess()` on GCP Access Manager
7. GCP Access Manager:
   - Checks runtime cache for existing grants
   - **If exists and not expired**: Regenerates service account key for existing service account, returns key JSON
   - **If new or expired**:
     - Creates service account with naming format: `{sanitized-email}-{unix-timestamp}`
     - Generates service account key (JSON format)
     - Creates IAM binding for the service account (not the user) with time-based condition
     - Condition includes title "Temp Access", expiration description with justification, and CEL expression
     - Expression format: `request.time < timestamp('2026-01-19T12:34:56Z')`
     - Description format: "Access expires on 2026-01-19T12:34:56Z. Justification: <user's justification>"
     - Member format: `serviceAccount:{service-account-email}` (not `user:{email}`)
     - Updates runtime cache with grant details including service account email and justification
     - Logs grant to BigQuery with service account email
8. JobManager returns success message and service account key JSON to Slack handler
9. Slack handler uploads key file to Slack channel with filename `gcp-access-{sanitized-email}.json`
10. If upload fails, user receives error message and can re-run the command to retrieve the key

### Background Cleanup

- Runs every hour via goroutine in `jobManager.Start()`
- Queries GCP IAM policy for all conditional bindings
- Parses condition expressions to extract expiration timestamps
- Identifies bindings with expiration times before current time
- Extracts service account emails from expired bindings (format: `serviceAccount:{email}`)
- Deletes expired service accounts from GCP (also deletes all associated keys)
- Removes expired conditional bindings from IAM policy
- Updates runtime cache to remove expired entries
- **Note**: IAM conditions automatically deny access after expiration, but cleanup removes the bindings and service accounts for hygiene

### Storage Schema

IAM policy binding structure (example for one user):

```json
{
  "role": "projects/openshift-crt-ephemeral-access/roles/chat_bot_user",
  "members": [
    "serviceAccount:user1-redhat-com-1737504000@openshift-crt-ephemeral-access.iam.gserviceaccount.com"
  ],
  "condition": {
    "title": "Temp Access",
    "description": "Access expires on 2026-01-19T18:30:00Z. Justification: Need to debug CI infrastructure issues",
    "expression": "request.time < timestamp('2026-01-19T18:30:00Z')"
  }
}
```

**Key Differences from User-Based Bindings:**
- Member is `serviceAccount:{email}` instead of `user:{email}`
- Service account naming: `{sanitized-email}-{unix-timestamp}@{project}.iam.gserviceaccount.com`
- Service account display name: User's email address
- Service account description: Includes justification

### Reversible Email Sanitization

**Security Enhancement**: The implementation uses reversible email encoding in service account names to enable secure user identification without relying on mutable fields.

**Encoding Scheme** (since all users have @redhat.com emails):
1. Extract username from email: `user.name@redhat.com` → `user.name`
2. Encode special characters for GCP compliance:
   - `.` → `-dot-`
   - `-` → `-dash-`
3. Append timestamp for uniqueness

**Examples:**
- `jsmith@redhat.com` → `jsmith-1234567890`
- `jane.doe@redhat.com` → `jane-dot-doe-1234567890`
- `john-smith@redhat.com` → `john-dash-smith-1234567890`
- `user.name-test@redhat.com` → `user-dot-name-dash-test-1234567890`

**Benefits:**
- ✅ **Secure**: Based on immutable service account email, not mutable DisplayName field
- ✅ **Efficient**: No API call needed to fetch service account details during revocation (saves 1 API call)
- ✅ **Reliable**: Works even if DisplayName is modified via GCP Console
- ✅ **Reversible**: Unambiguous decoding to recover original email
- ✅ **Space-efficient**: Only stores username (saves ~12 characters vs full email)

**Implementation Functions:**
- `sanitizeUsernameReversible()`: Encodes email username for service account names
- `unsanitizeUsername()`: Reverses encoding and appends @redhat.com domain
- `extractUserEmailFromServiceAccount()`: Parses service account email to extract user email
- `needsDisplayNameFallback()`: Determines if an email requires DisplayName fallback (sanitized username > 19 chars)

**Fallback Strategy:**
- Primary: Parse service account email to extract user email (fast, secure, immutable)
- Fallback: Query service account DisplayName only when necessary:
  - Only queries API if the sanitized username exceeds 19 characters (max allowed - timestamp length)
  - Uses `needsDisplayNameFallback()` to check email length upfront
  - For most users with short emails, no API call is needed
  - For long emails (>19 chars after sanitization), falls back to DisplayName lookup via API
- This optimization significantly reduces API calls during user grant lookups

Runtime cache structure (in-memory):

```go
map[string]*UserAccessGrant{
  "user1@redhat.com": {
    Email:                "user1@redhat.com",
    GrantedAt:            time.Parse(...),  // Calculated from ExpiresAt - 7 days
    ExpiresAt:            time.Parse(...),  // Parsed from condition expression
    RequestedBy:          "U12345678",      // Slack user ID (only available at grant time, not from IAM)
    Justification:        "Need to debug CI infrastructure issues",  // Business justification
    ServiceAccountEmail:  "user1-redhat-com-1737504000@openshift-crt-ephemeral-access.iam.gserviceaccount.com",  // Service account email
  },
}
```

## Error Handling

The implementation handles several error scenarios using proper error type checking:

1. **Missing or invalid parameters**: Returns usage message with example command
2. **Invalid resource**: Returns error message indicating only "gcp-access" is currently supported
3. **Organizational data service not available**: Command returns error message asking user to contact administrator
4. **User not in Hybrid Platforms organization**: Returns error message: "You are not a member of the 'Hybrid Platforms' organization. Access can only be granted to Hybrid Platforms members."
5. **User not in specified organization**: Returns error message explaining user must be a member of the organization they specified
6. **Service account not configured**: Command returns helpful error message
7. **User email not found**: Returns error asking user to configure Slack profile
8. **Google API errors**: Uses `googleapi.Error` type with HTTP status codes for proper error detection
   - `isNotFoundError()`: Checks for HTTP 404 Not Found (instead of string matching)
9. **IAM policy modification errors**: Properly handles concurrent modifications and retries
10. **User already has access**: Regenerates service account key and uploads new key file (handles upload failures)
11. **Service account creation errors**: Returns error to user, no cleanup needed
12. **Service account key generation errors**: Deletes service account and returns error
13. **IAM binding errors**: Deletes service account (and all keys) and returns error
14. **Slack upload errors**: Keeps service account active, user can retry to get key

## Testing

### Dry-Run Mode (Recommended for Testing)

To safely test the `request` command without modifying IAM policies, use dry-run mode:

1. **Enable dry-run mode:**
   ```bash
   # Set environment variable in your deployment
   export GCP_ACCESS_DRY_RUN=true

   # Or add to Kubernetes deployment:
   env:
     - name: GCP_ACCESS_DRY_RUN
       value: "true"
   ```

2. **Test the full flow:**
   ```
   request gcp-access "Testing the command"
   ```

3. **Verify dry-run behavior:**
   - Check pod logs for: `DRY-RUN: Would grant GCP IAM access to user...`
   - Confirm BigQuery audit logs are still being written
   - Verify IAM policy remains unchanged in GCP Console
   - User still receives success message

4. **Benefits of dry-run mode:**
   - Safe to test even if you're already a project owner
   - Full command flow is exercised (parsing, validation, org checks)
   - BigQuery logging works normally
   - No risk of modifying production IAM policies
   - Easy to enable/disable with environment variable

5. **When to use dry-run:**
   - Initial deployment testing
   - Testing with your own account
   - Verifying BigQuery integration
   - Testing command parsing and validation
   - Debugging without IAM side effects

### Dry-Run Mode Implementation Details

**Service Account Email Format in Dry-Run**:
- Format: `dry-run-{sanitized-email}@{projectID}.iam.gserviceaccount.com`
- Example: `dry-run-user-at-redhat-com@openshift-crt-ephemeral-access.iam.gserviceaccount.com`
- Note: This differs from production format which includes timestamp:
  - Production: `{sanitized-username}-{unix-timestamp}@{projectID}.iam.gserviceaccount.com`
  - Dry-run: `dry-run-{sanitized-email}@{projectID}.iam.gserviceaccount.com`

**Mock Service Account Key Format**:
```json
{
  "type": "service_account",
  "project_id": "dry-run"
}
```

### Manual Testing

1. **Test grant:**
   ```
   request gcp-access "Need to test new feature"
   ```
   Verify:
   - Service account created in GCP Console (IAM & Admin → Service Accounts)
   - Service account has correct naming format: `{sanitized-email}-{timestamp}`
   - Service account display name is user's email
   - Service account description includes justification
   - IAM binding created with `serviceAccount:` member (not `user:`)
   - Binding description includes justification
   - JSON key file uploaded to Slack
   - Cache updated with service account email and justification

2. **Test already has access (key regeneration):**
   Run command again within 7 days
   Verify:
   - User receives message showing current expiration date and original justification
   - New key file uploaded to Slack
   - No duplicate service account created (same service account in GCP Console)
   - Both old and new keys work until service account is deleted

3. **Test missing parameters:**
   ```
   request
   ```
   Verify: User receives usage error with example


5. **Test invalid resource:**
   ```
   request "Hybrid Platforms" aws "Testing"
   ```
   Verify: User receives error "Currently, access is only available for the 'gcp-access' resource."

6. **Test revoke:**
   ```
   revoke gcp-access
   ```
   Verify:
   - Service account deleted from GCP Console
   - All service account keys deleted (no longer work)
   - IAM binding removed from project
   - Cache cleared

7. **Test revoke without access:**
   Run `revoke gcp-access` without having active access
   Verify: User receives message "You do not have any active GCP workspace access to revoke."

8. **Test cleanup:**
   - Manually create a service account and IAM binding with an expired condition timestamp
   - Wait up to 1 hour for cleanup job
   - Verify:
     - Service account deleted from GCP Console
     - IAM binding removed from project
     - Cache cleared

9. **Test error cases:**
   - User not in Hybrid Platforms organization (should be rejected immediately)
   - User validation only checks Hybrid Platforms membership
   - User without email in Slack profile
   - Invalid service account credentials
   - Organizational data service not available (ORG_DATA_BUCKET not set)

### Unit Tests

To add unit tests, create `pkg/manager/gcp_access_test.go`:

```go
package manager_test

import (
    "testing"
    "time"

    "github.com/openshift/ci-chat-bot/pkg/manager"
)

func TestGrantAccess(t *testing.T) {
    // Test implementation
}

func TestCleanupExpiredAccess(t *testing.T) {
    // Test implementation
}
```

## Security Considerations

1. **Service Account Key Protection**: Bot's service account key stored in Kubernetes Secret, not in code
2. **User Key Security**: Service account key files uploaded to Slack with security warnings
3. **Domain Validation**: Only @redhat.com emails allowed
4. **Automatic Expiration**: All access expires after 7 days via IAM condition expressions
5. **Condition-Based Access Control**: IAM conditions automatically deny access after expiration timestamp
6. **Key Lifecycle**: All keys deleted when service account is deleted (on revoke or expiry)
7. **Audit Trail**: GCP IAM policy, Cloud Audit Logs, and BigQuery audit logs provide full audit trail
8. **Least Privilege**: Bot's service account only has IAM policy management and service account creation permissions on target project
9. **Unique Service Accounts**: Each user gets a separate service account for isolation
10. **No Persistent Key Storage**: Keys not stored in cache or database - only uploaded to Slack
11. **Automatic Cleanup**: Expired service accounts and keys automatically deleted

## Monitoring

### Logs to Monitor

```bash
# Service account creation
grep "Created service account" logs

# Service account key generation
grep "Created key for service account" logs

# Successful grants
grep "Granted GCP IAM access to service account" logs

# Key regeneration
grep "already has active access, regenerating key" logs

# Cleanup operations
grep "Running GCP.*cleanup" logs
grep "Found expired service account" logs
grep "Deleted service account" logs

# Errors
grep "Failed to create service account" logs
grep "Failed to generate service account key" logs
grep "Failed to grant GCP" logs
grep "Failed to upload service account key" logs
grep "Failed to delete service account" logs
```

### Metrics to Add (Future Enhancement)

Consider adding Prometheus metrics:
- `ci_chat_bot_gcp_access_grants_total` - Total grants (service accounts created)
- `ci_chat_bot_gcp_access_active_service_accounts` - Current active service accounts
- `ci_chat_bot_gcp_access_key_regenerations_total` - Total key regenerations
- `ci_chat_bot_gcp_access_cleanup_runs_total` - Cleanup job runs
- `ci_chat_bot_gcp_access_service_accounts_deleted_total` - Service accounts deleted
- `ci_chat_bot_gcp_access_errors_total` - Errors by type (creation, key_gen, upload, etc.)

## Troubleshooting

### Command Returns "Organizational data service is not available"

**Cause**: Cyborg-data not loaded or ORG_DATA_BUCKET not configured

**Fix**:
1. Check `ORG_DATA_BUCKET` environment variable is set
2. Check `ORG_DATA_OBJECT_PATH` if using custom path
3. Check pod logs for cyborg-data initialization errors
4. Verify GCS bucket exists and is accessible
5. Verify service account has Storage Object Viewer permissions on the bucket

### Command Returns "GCP access manager is not enabled"

**Cause**: Environment variables not set or service account configuration failed

**Fix**:
1. Check `GCP_SERVICE_ACCOUNT_JSON` is set
2. Check pod logs for initialization errors
3. Verify service account JSON is valid

### "Failed to set IAM policy: 403" or "Failed to create service account: 403"

**Cause**: Service account lacks permissions to create service accounts or modify IAM policies

**Fix**:
1. Verify service account has the following roles on the target project:
   - `roles/iam.serviceAccountAdmin` (or custom role with `iam.serviceAccounts.*` permissions)
   - `roles/iam.serviceAccountKeyAdmin` (or custom role with `iam.serviceAccountKeys.*` permissions)
   - `roles/resourcemanager.projectIamAdmin` (or custom role with IAM policy permissions)
2. Check that the service account credentials are correctly configured
3. Ensure the project ID is correct in the constants
4. Verify the service account JSON includes the `https://www.googleapis.com/auth/cloud-platform` scope

### Service accounts not being cleaned up after 7 days

**Cause**: Cleanup job not running or encountering errors

**Fix**:
1. Check pod logs for cleanup errors
2. Verify service account has the following permissions:
   - `iam.serviceAccounts.delete` (for deleting service accounts)
   - `resourcemanager.projects.setIamPolicy` (for removing IAM bindings)
3. Check Cloud Resource Manager API and IAM API quota/rate limits
4. Check for errors like "Failed to delete service account" in logs
5. Note: Users are automatically denied access via IAM conditions, cleanup is just for hygiene and removing unused service accounts

## Future Enhancements

1. **List command**: Show current access status and expiration
2. **Admin commands**: List all active grants, force revoke
3. **Notification**: Slack DM reminder 1 day before expiration
4. **Multiple roles**: Support different IAM roles for different purposes
5. **Audit logging**: Export grant/revoke events to external system
6. **Metrics dashboard**: Grafana dashboard for usage patterns
7. **Custom durations**: Allow admins to specify different expiration periods

## Dependencies

The implementation uses the following Go packages:

- `github.com/openshift-eng/cyborg-data/go` - Organizational data service for group membership validation
- `cloud.google.com/go/storage` - Google Cloud Storage client (for GCS data source with `-tags gcs`)
- `cloud.google.com/go/bigquery` - BigQuery client for audit logging
- `golang.org/x/oauth2` - OAuth2 authentication
- `google.golang.org/api/cloudresourcemanager/v1` - Google Cloud Resource Manager API for IAM policy management
- `google.golang.org/api/iam/v1` - IAM API for service account management (create, delete, key generation)
- `google.golang.org/api/googleapi` - Google API error types
- `github.com/slack-go/slack` - Slack SDK for file uploads (service account key JSON)
- `k8s.io/client-go` - Kubernetes client for ConfigMap access
- `k8s.io/apimachinery/pkg/api/errors` - Kubernetes error utilities (aliased as `apierrors`)
- `k8s.io/klog` - Logging

### Build Tags

The application must be built with the `-tags gcs` build tag to enable GCS support for loading organizational data.

The Makefile has been updated to automatically include the `gcs` build tag:

```bash
make build
```

Or manually:

```bash
go build -tags gcs ./cmd/ci-chat-bot
```

Without this tag, the GCS data source will be a stub that returns an error.

## Organization Membership Validation

### Overview

The `request` command requires users to be members of the **Hybrid Platforms** organization. This is the only organization membership requirement - users do not need to specify an organization in the command.

### How It Works

1. **Automatic Hybrid Platforms Check**:
   - Every user requesting access MUST be a member of the "Hybrid Platforms" organization
   - The check happens automatically before granting access
   - If the user is not in Hybrid Platforms, they are rejected immediately with: "You are not a member of the 'Hybrid Platforms' organization. Access can only be granted to Hybrid Platforms members."
   - No organization parameter is required in the command - the validation always checks Hybrid Platforms membership

### Example Scenarios

**Scenario 1: User in Hybrid Platforms**
- Command: `request gcp-access "Need access"`
- User is in: Hybrid Platforms
- Result: ✅ Approved - Access granted

**Scenario 2: User not in Hybrid Platforms**
- Command: `request gcp-access "Need access"`
- User is NOT in: Hybrid Platforms
- Result: ❌ Rejected - "You are not a member of the 'Hybrid Platforms' organization..."

### Implementation Details

The membership check is implemented in [pkg/slack/actions.go](../../../pkg/slack/actions.go) in the `Request()` function:

```go
// Verify user is a member of Hybrid Platforms (required for all access)
if !isUserInOrg(orgDataService, event.User, email, "Hybrid Platforms") {
    return "You are not a member of the 'Hybrid Platforms' organization. Access can only be granted to Hybrid Platforms members."
}
```

The `isUserInOrg()` helper function (in the same file) performs the actual validation using cyborg-data:
- First attempts Slack ID lookup: `orgDataService.IsSlackUserInOrg(slackID, org)`
- Falls back to email-based lookup if Slack ID fails: `GetEmployeeByEmail()` + `IsEmployeeInOrg()`

### Testing

Unit tests in [pkg/slack/actions_request_test.go](../../../pkg/slack/actions_request_test.go) verify:
- Users not in Hybrid Platforms are rejected
- Users in Hybrid Platforms are approved
- Both lookup methods work correctly (Slack ID + email fallback)

## Deployment Checklist

- [ ] Set up organizational data in GCS bucket
- [ ] Configure `ORG_DATA_BUCKET` and optionally `ORG_DATA_OBJECT_PATH` environment variables
- [ ] Ensure GCS bucket has proper permissions for the service account
- [ ] Verify `GCPProjectID` constant is set to `"openshift-crt-ephemeral-access"` in `pkg/manager/gcp_access.go`
- [ ] Verify `GCPIAMRole` constant uses full resource path for custom role: `projects/openshift-crt-ephemeral-access/roles/chat_bot_user` in `pkg/manager/gcp_access.go`
- [ ] Verify `BigQueryDataset` and `BigQueryTable` constants are set correctly in `pkg/manager/gcp_access.go`
- [ ] Create GCP service account for the bot
- [ ] Grant service account `roles/iam.serviceAccountAdmin` on `openshift-crt-ephemeral-access` project (for creating/deleting service accounts)
- [ ] Grant service account `roles/iam.serviceAccountKeyAdmin` on `openshift-crt-ephemeral-access` project (for generating service account keys)
- [ ] Grant service account `roles/resourcemanager.projectIamAdmin` on `openshift-crt-ephemeral-access` project (for managing IAM policies)
- [ ] Grant service account `roles/bigquery.dataEditor` on `openshift-crt-ephemeral-access` project (for audit logging)
- [ ] Create BigQuery dataset `ci_chat_bot` in `openshift-crt-ephemeral-access` project
- [ ] Create BigQuery table `access_grants` with the correct schema (including `service_account_email` field)
- [ ] Create Kubernetes Secret with service account JSON
- [ ] Update deployment to include `GCP_SERVICE_ACCOUNT_JSON` environment variable
- [ ] Set up automatic resource cleanup for resources older than 7 days in the GCP project
- [ ] Build ci-chat-bot using `make build` (automatically includes `-tags gcs`)
- [ ] Deploy updated ci-chat-bot
- [ ] Test with a real user account (in Hybrid Platforms org)
- [ ] Test with a user not in Hybrid Platforms org (should be denied)
- [ ] Verify service accounts are created in GCP Console (IAM & Admin → Service Accounts)
- [ ] Verify service account naming follows convention: `{sanitized-email}-{timestamp}`
- [ ] Verify IAM conditional bindings use `serviceAccount:` members in GCP Console
- [ ] Verify JSON key files are uploaded to Slack with security warnings
- [ ] Download key file and test authentication with `export GOOGLE_APPLICATION_CREDENTIALS=...`
- [ ] Test key regeneration by re-requesting access
- [ ] Test service account deletion on revoke
- [ ] Verify BigQuery audit logs include `service_account_email` field
- [ ] Verify success message includes service account key usage instructions
- [ ] Monitor logs for service account creation/deletion
- [ ] Monitor logs for errors
- [ ] Document process for team

## Support

For issues or questions:
- Check pod logs: `kubectl logs -n ci deployment/ci-chat-bot`
- Review service accounts in Google Cloud Console: IAM & Admin → Service Accounts
- Review IAM policy in Google Cloud Console: IAM & Admin → IAM → View by Principals
- Filter for conditional bindings with title "Temp Access" and `serviceAccount:` members
- Check Cloud Audit Logs for service account creation/deletion and IAM policy changes
- Query BigQuery audit logs:
  ```sql
  SELECT user_email, service_account_email, timestamp, expires_at, justification
  FROM `openshift-crt-ephemeral-access.ci_chat_bot.access_grants`
  WHERE DATE(timestamp) = CURRENT_DATE()
  ORDER BY timestamp DESC;
  ```
- File an issue at https://github.com/openshift/ci-chat-bot/issues
