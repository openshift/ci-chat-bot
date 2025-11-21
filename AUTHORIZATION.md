# CI Chat Bot Authorization System

This document describes the organizational data-based authorization system integrated into the CI Chat Bot.

## Overview

The authorization system provides fine-grained access control for bot commands based on organizational data. It now uses a modern, indexed data structure that provides fast lookups and comprehensive organizational hierarchy information. The system supports multiple authorization levels including user-specific, team-based, and organization-based permissions.

## Key Features

- **Indexed Organization Data**: Uses pre-computed indexes for O(1) lookups and fast authorization checks
- **Complete Organizational Hierarchy**: Shows full ancestry chain including teams, organizations, pillars, and team groups
- **Multiple Authorization Levels**: Support for user UID, team membership, and organization-based permissions
- **Hot Reload**: Automatic updates when organizational data or authorization config changes (supports local files and GCS)
- **Enhanced Troubleshooting**: Comprehensive `whoami` command showing complete organizational context
- **Fallback Safety**: Graceful degradation when authorization service is unavailable
- **Modular Architecture**: Clean separation between core data service and Slack-specific authorization logic

## Architecture

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│ Indexed Org     │    │ Core Data        │    │ Slack-Specific  │
│ Data (JSON)     │───▶│ Service          │───▶│ Authorization   │
│ (Python orglib) │    │ (orgdata-core)   │    │ Service         │
└─────────────────┘    └──────────────────┘    └─────────────────┘
        │                       │                       │
        │                       │                       │
        ▼                       ▼                       ▼
┌─────────────────┐    ┌──────────────────┐    ┌──────────────────┐
│ Data Sources    │    │ Fast Indexed     │    │ Auth Config      │
│ • Local Files   │    │ Lookups          │    │ (YAML Rules)     │
│ • GCS Bucket    │    │ (O(1) queries)   │    │ • Hot Reload     │
│ • Hot Reload    │    │ • DataSource API │    │ • File Watching  │
└─────────────────┘    └──────────────────┘    └──────────────────┘
```

### DataSource Architecture

The system now uses a pluggable DataSource architecture:

- **FileDataSource**: Local JSON files with file watching
- **GCSDataSource**: Google Cloud Storage with polling for updates  
- **Extensible**: Easy to add new sources (S3, HTTP, databases, etc.)

### Authorization Config Sources

The authorization system also uses pluggable config sources for maximum deployment flexibility:

```go
type AuthConfigSource interface {
    Load(ctx context.Context) (*AuthorizationConfig, error)
    Watch(ctx context.Context, callback func(*AuthorizationConfig)) error
    String() string
}
```

**Current Implementation:**
- **FileAuthConfigSource**: YAML files with polling-based hot reload (60s interval)

**Future Extensions:**
- **ConfigMapAuthConfigSource**: Kubernetes ConfigMaps for zero-downtime policy updates
- **GCSAuthConfigSource**: Centralized policy storage in Google Cloud Storage
- **HTTPAuthConfigSource**: External policy services (OPA, custom APIs)

**Production Benefits:**
- **No container rebuilds** for authorization policy changes
- **Hot reload** without service restarts
- **Centralized policy management** across environments
- **GitOps-friendly** policy deployment pipelines

### Package Structure

- **`github.com/openshift-eng/cyborg-data`**: External reusable core package for organizational data access
  - Supports multiple data sources (files, GCS)
  - Build with `-tags gcs` for cloud storage support
  - Hot reload with configurable check intervals
- **`pkg/orgdata/`**: Slack-specific wrapper around core package  
- **`pkg/slack/`**: Slack command handlers with authorization middleware

## Setup

### 1. Configuration Options

#### Option A: Local Files (Development)

```bash
./ci-chat-bot \
  --orgdata-paths="/path/to/comprehensive_index.json" \
  --authorization-config="/path/to/authorization.yaml" \
  [other flags...]
```

#### Option B: Google Cloud Storage (Production)

```bash
# Build with GCS support
make BUILD_FLAGS="-tags gcs" build

# Run with GCS backend
./ci-chat-bot \
  --gcs-enabled=true \
  --gcs-bucket="resolved-org" \
  --gcs-object-path="orgdata/comprehensive_index.json" \
  --gcs-project-id="openshift-crt" \
  --gcs-check-interval="5m" \
  --authorization-config="/path/to/authorization.yaml" \
  [other flags...]
```

#### Option C: Using Development Scripts

```bash
# Quick start with GCS (recommended)
./hack/run-with-gcs.sh

# Local file development
export ORGDATA_PATHS="/path/to/comprehensive_index.json"
./hack/run.sh

# See all options
make help-ci-chat-bot
```

📖 **For detailed setup instructions, see: `hack/DEVELOPMENT.md`**

### 2. Organizational Data Format

The system now uses indexed JSON data generated by the Python `orglib` indexing system. The data structure includes pre-computed indexes for fast lookups:

```json
{
  "metadata": {
    "generated_at": "2025-08-13T15:00:00Z",
    "data_version": "v1.0",
    "total_employees": 150,
    "total_orgs": 25,
    "total_teams": 45
  },
  "lookups": {
    "employees": {
      "employee_uid": {
        "uid": "employee_uid",
        "full_name": "John Doe",
        "email": "john@company.com",
        "job_title": "Senior Engineer",
        "slack_uid": "U123ABC456"
      }
    },
    "teams": {
      "team_name": {
        "uid": "team_uid",
        "name": "Team Name",
        "type": "team",
        "group": {
          "resolved_people_uid_list": ["employee_uid1", "employee_uid2"]
        }
      }
    },
    "orgs": {
      "org_name": {
        "uid": "org_uid",
        "name": "Organization Name",
        "type": "org",
        "group": {
          "resolved_people_uid_list": ["employee_uid1", "employee_uid2"]
        }
      }
    }
  },
  "indexes": {
    "membership": {
      "membership_index": {
        "employee_uid": [
          {"name": "team_name", "type": "team"},
          {"name": "org_name", "type": "org"}
        ]
      },
      "relationship_index": {
        "teams": {
          "team_name": {
            "ancestry": {
              "orgs": ["parent_org1", "parent_org2"],
              "pillars": ["pillar_name"],
              "team_groups": ["team_group_name"]
            }
          }
        }
      }
    },
    "slack_id_mappings": {
      "slack_uid_to_uid": {
        "U123ABC456": "employee_uid"
      }
    }
  }
}
```

### 3. Authorization Configuration

Create an `authorization.yaml` file with access rules:

```yaml
rules:
  # Allow everyone
  - command: "version"
    allow_all: true

  # Specific users only
  - command: "admin_reset"
    allowed_uids:
      - "admin_user_123"
      - "backup_admin_456"
    deny_message: "Admin access required"

  # Team-based access
  - command: "deploy_staging"
    allowed_teams:
      - "Platform Engineering"
      - "SRE Team"

  # Organization-based access
  - command: "view_metrics"
    allowed_orgs:
      - "Engineering Division"

  # Mixed authorization (OR logic)
  - command: "emergency_access"
    allowed_uids: ["oncall_admin"]
    allowed_teams: ["SRE Team"]
    allowed_orgs: ["Platform Engineering"]
```

## Authorization Levels

The system checks permissions in this order (first match grants access):

1. **`allow_all: true`** - Command available to everyone
2. **`allowed_uids`** - Specific Employee.UID values
3. **`allowed_teams`** - Team membership
4. **`allowed_orgs`** - Organization membership

If no rule matches, the command is allowed by default (fail-open for safety).

## Configuration Examples

### Basic Setup
```yaml
rules:
  - command: "version"
    allow_all: true
  - command: "launch" 
    allowed_orgs: ["Platform Engineering"]
```

### Advanced Multi-Level
```yaml
rules:
  - command: "cluster_create"
    allowed_uids: ["senior_admin"]      # Emergency admin access
    allowed_teams: ["SRE", "Platform"]  # Team access
    allowed_orgs: ["Engineering"]       # Broad org access
    deny_message: "Cluster creation requires SRE, Platform team, or senior admin access"
```

## Using the System

### For End Users

**Check Your Permissions:**
```
@bot whoami
```
This shows:
- Your employee information (UID, name, email, job title)
- Complete organizational hierarchy including:
  - Direct team memberships
  - Organizations you belong to through team membership
  - Pillars and team groups in your ancestry chain
  - Full organizational context
- Specific commands you can execute
- How you get access to each command

### For Administrators

**Update Authorization Rules:**
1. Edit the `authorization.yaml` file
2. The system automatically reloads within 60 seconds
3. Test changes with `@bot whoami`

**Update Organizational Data:**
1. **Local Files**: Edit JSON file, automatic reload via file watching
2. **GCS**: Upload new file to bucket, automatic reload via polling
   ```bash
   gcloud storage cp comprehensive_index.json gs://resolved-org/orgdata/
   ```
3. **Check interval**: Configurable via `--gcs-check-interval` (default: 5 minutes)

**Add New Commands:**
1. Add authorization rule to config file
2. Wrap command handler with authorization middleware

## Package Structure

### Core Package (`pkg/orgdata-core/`)

The core package provides reusable organizational data functionality:

- **`types.go`**: Core data structures (Employee, Team, Org, etc.)
- **`interface.go`**: Service interface for data access
- **`service.go`**: Implementation with fast indexed lookups
- **`README.md`**: Comprehensive documentation

### Slack Package (`pkg/orgdata/`)

Slack-specific wrapper around the core package:

- **`types.go`**: Type re-exports for backward compatibility
- **`interface.go`**: Slack service wrapper with `NewIndexedOrgDataService()`
- **`auth.go`**: Authorization service using the core package

### Usage

```go
// Create the service (same as before)
service := orgdata.NewIndexedOrgDataService()

// Option 1: Load from local files  
err := service.LoadFromFiles([]string{"comprehensive_index.json"})

// Option 2: Load from GCS (requires -tags gcs)
gcsConfig := orgdata.GCSConfig{
    Bucket:        "resolved-org",
    ObjectPath:    "orgdata/comprehensive_index.json", 
    ProjectID:     "openshift-crt",
    CheckInterval: 5 * time.Minute,
}
err := service.LoadFromGCS(ctx, gcsConfig)

// Start hot reload watcher
err = service.StartGCSWatcher(ctx, gcsConfig)

// Use the service (same API regardless of data source)
teams := service.GetTeamsForSlackID("U123ABC456")
orgs := service.GetUserOrganizations("U123ABC456")
```

## Integration Points

### Adding Authorization to New Commands

```go
// In pkg/slack/slack.go
parser.NewBotCommand("your_command <args>", &parser.CommandDefinition{
    Description: "Your command description",
    Handler:     AuthorizedCommandHandler("your_command", b.authService, YourCommandHandler),
}, false),
```

### Authorization Middleware

The `AuthorizedCommandHandler` wrapper:
- Checks user permissions before executing commands
- Returns helpful denial messages
- Logs authorization decisions
- Falls back to allowing access if authorization service is unavailable

## Troubleshooting

### Common Issues

**"Authorization service not configured"**
- **Local files**: Check `--orgdata-paths` and `--authorization-config` flags  
- **GCS**: Verify `--gcs-enabled=true` and GCS configuration flags
- **Build**: Ensure binary built with `-tags gcs` for GCS support
- **Authentication**: Run `gcloud auth login` or set service account credentials
- **Logs**: Check for data loading errors, GCS authentication failures

**User not found in org data**
- Verify user's Slack UID is in the organizational data
- Check data format matches expected structure
- User gets access to `allow_all: true` commands only

**Commands not working as expected**
- Use `@bot whoami` to see user's permissions
- Check authorization config syntax
- Verify team/org names match exactly (case-sensitive)

## Data Sources

The system can load organizational data from multiple sources:

### Local Files
```bash
--orgdata-paths="/path/to/comprehensive_index.json"
```
- **Best for**: Development, testing
- **Hot Reload**: File watching (automatic)
- **Dependencies**: None

### Google Cloud Storage  
```bash
--gcs-enabled=true \
--gcs-bucket="resolved-org" \
--gcs-object-path="orgdata/comprehensive_index.json" \
--gcs-project-id="openshift-crt"
```
- **Best for**: Production, cross-cluster deployments
- **Hot Reload**: Configurable polling (default: 5 minutes)
- **Dependencies**: Requires `-tags gcs` build flag
- **Authentication**: Application Default Credentials or service account JSON

### Development Scripts
```bash
# GCS backend with sensible defaults
./hack/run-with-gcs.sh

# Local file backend 
export ORGDATA_PATHS="/path/to/file.json"
./hack/run.sh

# See all configuration options
make help-ci-chat-bot
cat hack/DEVELOPMENT.md
```

## Recent Refactoring

### What Changed

The authorization system has been refactored to use a modern, indexed data structure:

- **Before**: Used hierarchical JSON with runtime traversal
- **After**: Uses pre-computed indexes for instant lookups
- **Data Source**: Python `orglib` indexing system generates `comprehensive_index.json`
- **Architecture**: Clean separation between core data service and Slack-specific logic

### Benefits

- **Faster**: O(1) lookups instead of O(n) traversals
- **Complete**: Shows full organizational hierarchy (teams → orgs → pillars → team groups)
- **Maintainable**: Core package can be reused by other services
- **Scalable**: Better performance with large organizational datasets

### Migration

- **No breaking changes**: All existing functionality preserved
- **Same API**: `whoami` command works exactly as before
- **Enhanced output**: Now shows complete organizational context
- **Better performance**: Faster authorization checks

## Security Considerations

- **Fail-open design**: Commands work if authorization service fails
- **Hot reload**: Config changes apply without restart
- **Audit trail**: All authorization decisions are logged
- **Principle of least privilege**: Start restrictive, gradually open access

## Performance

- **O(1) lookups**: Pre-computed indexes enable instant authorization checks
- **Memory efficient**: Indexed data structure optimized for fast access
- **Minimal latency**: Authorization check adds <1ms to command execution
- **Scalable**: Handles thousands of employees and teams efficiently
- **Hot reload**: Data updates without service restart (files + GCS)
- **Multiple data sources**: Local files, GCS, extensible to S3/HTTP/databases
- **Modular design**: Core package can be reused by other services (REST APIs, CLI tools, etc.)
- **Production ready**: Secure GCS backend with Application Default Credentials

