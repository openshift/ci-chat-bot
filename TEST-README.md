# Testing the Authorization System

This guide shows how to test the integrated orgdata authorization system with your existing test data.

## 🗂️ Your Data Structure

Your `test-data/orgdata.json` contains a hierarchical organization structure:
- **Top-level org**: "Multi-product and Engineering Experience" 
- **Teams**: Nested units with `"type": {"name": "team"}`
- **Employees**: Found in `resolved_roles[].people[]` throughout the hierarchy

The orgdata service automatically flattens this hierarchy to create indexed lookups.

## 🧪 Quick Test (Direct Authorization Testing)

Test the authorization logic directly without Slack:

```bash
# 1. Update test-auth.go with actual Slack IDs if needed (already configured)
# 2. Run the test
go run test-auth.go
```

**Expected Output:**
```
🔍 Testing User #1: Slack ID UFF9BL596
==================================================
Has org data: true
Employee UID: benl
Display Name: Ben
Email: benl@redhat.com
Job Title: Senior Director, Engineering
Teams: [...]
Organizations: [Multi-product and Engineering Experience]

📋 Command Authorization Tests:
  version             : ✅ ALLOWED
  whoami              : ✅ ALLOWED  
  test_uid_command    : ✅ ALLOWED
  launch              : ✅ ALLOWED
  ...
```

## 🔍 Inspect Your Data

See what's available for authorization rules:

```bash
chmod +x inspect-json.sh
./inspect-json.sh test-data/orgdata.json
```

## 🤖 Full Bot Testing

**Note:** For production, use GCS instead of local files. This test example is for development/testing only.

Run the complete ci-chat-bot with authorization using GCS:

```bash
# Production approach (recommended)
go run ./cmd/ci-chat-bot \
  --gcs-enabled=true \
  --gcs-bucket="your-test-bucket" \
  --gcs-object-path="test-data/orgdata.json" \
  --authorization-config="./test-authorization.yaml" \
  --slack-token="your-slack-bot-token" \
  --slack-signing-secret="your-signing-secret" \
  --dry-run=true
```

Then test in Slack:
```
@your-bot whoami
# Should show comprehensive info including:
# *Organizational Memberships:*
# • ACS UI (Team)
# • Multi-product and Engineering Experience (Organization)
# • Platform+ Engineering (Team Group)
#
# *Commands You Can Execute:*
# 🌐 *Available to Everyone:* version, whoami
# 👤 *Your Personal Access:* launch (if you're benl)
# 👥 *Via Team Membership:* test_team_command (if you're in ACS UI)
# 🏢 *Via Organization:* test_org_command (if you're in MPEX)

@your-bot version
@your-bot test_uid_command
@your-bot launch
```

## 📋 Current Test Configuration

The `test-authorization.yaml` includes:

### ✅ **Allowed for Everyone:**
- `version`, `whoami` (allow_all: true)

### 🔒 **Restricted Commands:**
- `test_uid_command` - Only for UIDs: benl, eparis, linsong
- `test_team_command` - Only for team: "ACS UI"
- `test_org_command` - Only for org: "Multi-product and Engineering Experience"
- `launch` - Only for UID: benl (Senior Director)

### 👥 **Test Users:**
- **UFF9BL596** (benl) - Senior Director ✅ Access to everything
- **U01PLAWUU8N** (linsong) - Associate Manager ✅ Limited access
- **UNKNOWN_USER** - Not in org data ❌ Only allow_all commands

## 🛠️ Customizing Authorization

Edit `test-authorization.yaml` to add your own rules:

```yaml
# Allow specific users
- command: "your_command"
  allowed_uids:
    - "user_uid_from_data"

# Allow specific teams  
- command: "team_command"
  allowed_teams:
    - "Team Name From Data"

# Allow entire organization
- command: "org_command"
  allowed_orgs:
    - "Multi-product and Engineering Experience"

# Mixed authorization (OR logic)
- command: "flexible_command"
  allowed_uids: ["admin_uid"]
  allowed_teams: ["Admin Team"]  
  allowed_orgs: ["Admin Org"]
```

## 🎯 Authorization Priority

1. **`allow_all: true`** → Everyone has access
2. **`allowed_uids`** → Specific Employee.UID values
3. **`allowed_teams`** → Team membership  
4. **`allowed_orgs`** → Organization membership

If any check passes, access is granted. If all fail, access is denied with a helpful message.

## 📊 Understanding Your Data

The hierarchical structure is automatically processed:
- **Teams** are identified by `"type": {"name": "team"}`
- **Employees** are collected from all `resolved_roles[].people[]` 
- **Organizations** are derived from the org path hierarchy
- **All data** is flattened and indexed for fast O(1) lookups

Perfect for testing authorization rules with your real organizational structure! 🚀