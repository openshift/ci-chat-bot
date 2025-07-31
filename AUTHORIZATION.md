# CI Chat Bot Authorization System

This document describes the organizational data-based authorization system integrated into the CI Chat Bot.

## Overview

The authorization system provides fine-grained access control for bot commands based on organizational data. It integrates with hierarchical organizational structures and supports multiple authorization levels including user-specific, team-based, and organization-based permissions.

## Key Features

- **Hierarchical Organization Data**: Processes nested organizational structures with teams and employees
- **Multiple Authorization Levels**: Support for user UID, team membership, and organization-based permissions
- **Hot Reload**: Automatic updates when organizational data or authorization config changes
- **Enhanced Troubleshooting**: Comprehensive `whoami` command showing user permissions and available commands
- **Fallback Safety**: Graceful degradation when authorization service is unavailable

## Architecture

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│ Organizational  │    │ Authorization    │    │ Slack Commands  │
│ Data (JSON)     │───▶│ Service          │───▶│ & Handlers      │
└─────────────────┘    └──────────────────┘    └─────────────────┘
        │                       │
        │                       │
        ▼                       ▼
┌─────────────────┐    ┌──────────────────┐
│ OrgDataService  │    │ Auth Config      │
│ (Indexing &     │    │ (YAML Rules)     │
│  Fast Lookups)  │    │                  │
└─────────────────┘    └──────────────────┘
```

## Setup

### 1. Command Line Flags

Add these flags when running the CI Chat Bot:

```bash
./ci-chat-bot \
  --orgdata-paths="/path/to/orgdata.json" \
  --authorization-config="/path/to/authorization.yaml" \
  [other flags...]
```

### 2. Organizational Data Format

The system expects hierarchical JSON data with this structure:

```json
{
  "name": "Top Level Organization",
  "group": {
    "type": {"name": "org"},
    "resolved_roles": [
      {
        "people": [
          {
            "uid": "employee_id",
            "slack_uid": "U123ABC456",
            "display_name": "John Doe",
            "email": "john@company.com",
            "job_title": "Senior Engineer"
          }
        ]
      }
    ]
  },
  "children": [
    {
      "name": "Team Name",
      "group": {
        "type": {"name": "team"},
        "resolved_roles": [...]
      }
    }
  ]
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
- Your employee information
- Team and organization memberships  
- Specific commands you can execute
- How you get access to each command

### For Administrators

**Update Authorization Rules:**
1. Edit the `authorization.yaml` file
2. The system automatically reloads within 60 seconds
3. Test changes with `@bot whoami`

**Add New Commands:**
1. Add authorization rule to config file
2. Wrap command handler with authorization middleware

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
- Check `--orgdata-paths` and `--authorization-config` flags
- Verify file paths exist and are readable
- Check logs for data loading errors

**User not found in org data**
- Verify user's Slack UID is in the organizational data
- Check data format matches expected structure
- User gets access to `allow_all: true` commands only

**Commands not working as expected**
- Use `@bot whoami` to see user's permissions
- Check authorization config syntax
- Verify team/org names match exactly (case-sensitive)

## Data Sources

The system can load organizational data from:
- Local JSON files (`--orgdata-paths`)
- Kubernetes ConfigMaps (in production)
- Multiple files (automatically merged)

## Security Considerations

- **Fail-open design**: Commands work if authorization service fails
- **Hot reload**: Config changes apply without restart
- **Audit trail**: All authorization decisions are logged
- **Principle of least privilege**: Start restrictive, gradually open access

## Performance

- **O(1) lookups**: Pre-built indexes for fast authorization checks
- **Memory efficient**: Hierarchical data flattened into optimized structures
- **Minimal latency**: Authorization check adds <1ms to command execution
- **Scalable**: Handles thousands of employees and teams efficiently
