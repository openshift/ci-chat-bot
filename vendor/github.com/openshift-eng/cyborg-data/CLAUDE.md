# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`cyborg-data` (package name: `orgdatacore`) is a Go library for high-performance organizational data access. It provides O(1) lookups for employee, team, organization, pillar, and team group queries through pre-computed indexes. The library is designed to be consumed by multiple services (Slack bots, REST APIs, CLI tools) and supports hot-reload from pluggable data sources.

**Key Architecture Principle**: All organizational relationships are pre-computed during indexing. No expensive tree traversals occur at query time.

## Build Commands

### Basic Development
```bash
# Run all tests
make test

# Run tests with GCS support (requires build tag)
make test-with-gcs

# Run benchmarks
make bench

# Build all examples
make examples

# Lint code
make lint

# Lint with GCS build tags
make lint-with-gcs

# Update dependencies
make vendor

# Clean built binaries
make clean
```

### Single Test Execution
```bash
# Run a specific test
go test -run TestEmployeeQueries

# Run with verbose output
go test -v -run TestEmployeeQueries

# Run tests in a specific file (by pattern)
go test -run TestService

# Run with GCS tags
go test -tags gcs -run TestGCS

# Run specific entity tests
go test -run TestEmployee  # Employee tests
go test -run TestTeam      # Team tests
go test -run TestPillar    # Pillar tests
go test -run TestTeamGroup # Team group tests
```

### Building Examples
```bash
# Comprehensive example (shows GCS usage)
cd example/comprehensive && go build -o ./comprehensive .

# GCS example with full SDK support (requires build tag)
cd example/with-gcs && go build -tags gcs -o ./with-gcs .

# GCS example in stub mode (demonstrates API without SDK)
cd example/with-gcs && go build -o ./with-gcs-stub .
```

## Build Tags System

This codebase uses Go build tags to optionally include GCS support:

- **Default (no tags)**: GCS stub only (errors on use), suitable for testing/development
- **With `-tags gcs`**: Includes full GCS SDK (~50+ packages), enables GCSDataSourceImpl

### Files Affected by Build Tags
- `gcs_datasource.go` - Only compiled with `//go:build gcs` tag
- `datasources.go` - Contains stub GCSDataSource that always errors without the tag
- `internal/testing/filesource.go` - Internal test-only FileDataSource (not public API)

**Production builds must use `-tags gcs`** to enable the real GCS implementation.

When adding GCS functionality, use the `gcs` build tag to avoid forcing heavy dependencies on all consumers.

## Core Architecture

### Data Flow
1. **Data Source** (GCS) → **Service.LoadFromDataSource()** → **In-memory indexes**
2. Queries use pre-computed indexes (no traversal needed)
3. Hot reload via Watch() updates indexes atomically with RWMutex

Production deployments use GCS as the data source.

### Key Types

**Service** (`service.go`):
- Main entry point with RWMutex for thread-safe access
- Implements `ServiceInterface`
- Methods: `LoadFromDataSource()`, `StartDataSourceWatcher()`, `GetEmployeeByUID()`, `GetEmployeeBySlackID()`, `GetEmployeeByGitHubID()`, etc.

**Data Structure** (`types.go`):
- `Data`: Top-level containing Metadata, Lookups, Indexes
- `Lookups`: Direct O(1) maps (Employees, Teams, Orgs, Pillars, TeamGroups)
- `Indexes`: Pre-computed relationships (Membership, SlackIDMappings, GitHubIDMappings)

**DataSource Interface** (`interface.go`):
- `Load(ctx)`: Returns io.ReadCloser with JSON data
- `Watch(ctx, callback)`: Monitors for changes
- **Production Implementation**: GCSDataSourceImpl (requires `-tags gcs`)
- **Test-only**: FileDataSource (in `internal/testing` package, not public API)

### Index Types

**MembershipIndex**:
- `MembershipIndex[uid]` → list of teams/orgs/pillars/team_groups user belongs to
- `RelationshipIndex["teams"][teamName]` → team's ancestry (orgs, pillars, team_groups)
- `RelationshipIndex["orgs"][orgName]` → org's ancestry
- `RelationshipIndex["pillars"][pillarName]` → pillar's ancestry
- `RelationshipIndex["team_groups"][teamGroupName]` → team group's ancestry

**SlackIDMappings**:
- `SlackUIDToUID[slackID]` → employee UID

**GitHubIDMappings**:
- `GitHubIDToUID[githubID]` → employee UID

All lookups are O(1) - relationships are flattened at load time.

## Testing Strategy

### Test Data Location
- `testdata/test_org_data.json` - Minimal test dataset
- Tests use `setupTestService(t)` helper to load test data
- **Internal FileDataSource**: Tests use `internal/testing.NewFileDataSource()` (not part of public API)

### Test Files Organization
- `service_test.go` - Core service functionality
- `employee_test.go` - Employee query tests (including new fields: RhatGeo, CostCenter, etc.)
- `team_test.go` - Team membership tests and Group extended fields
- `organization_test.go` - Organization hierarchy tests
- `pillar_test.go` - Pillar query tests
- `team_group_test.go` - Team group query tests
- `service_edge_cases_test.go` - Edge cases and error scenarios
- `datasource_test.go` - DataSource implementations
- `benchmark_test.go` - Performance benchmarks

### Running Specific Tests
When modifying employee lookups, run: `go test -run TestEmployee`
When modifying team queries, run: `go test -run TestTeam`
When modifying pillar queries, run: `go test -run TestPillar`
When modifying team group queries, run: `go test -run TestTeamGroup`
When modifying GCS code, run: `go test -tags gcs -run TestGCS`

## Logging

The package uses structured logging via `logr` interface (compatible with OpenShift/Kubernetes):
- Default: `stdr` (stdlib wrapper)
- Set custom logger: `orgdatacore.SetLogger(klogr.New())`
- Helper functions in `logger.go`: `logInfo()`, `logError()`

When adding logging, use structured key-value pairs:
```go
logInfo("Data reloaded", "source", source.String(), "employee_count", count)
logError(err, "Failed to load data", "source", source.String())
```

## Common Development Patterns

### Adding a New Query Method
1. Add method to `ServiceInterface` in `interface.go`
2. Implement in `service.go` with `s.mu.RLock()` for thread safety
3. Check if `s.data` is nil before accessing
4. Use existing indexes for O(1) performance
5. Add tests in appropriate `*_test.go` file (follow the pattern: entity tests go in entity_test.go)

### Adding a New DataSource
1. Implement `DataSource` interface (Load, Watch, String)
2. If it requires external dependencies, consider using build tags
3. Add example in `example/` directory
4. Update README.md with usage instructions

### Modifying Indexes
The indexes are **loaded from JSON** (generated by Python `orglib` in the cyborg project). This package does **not** compute indexes - it only consumes them. If you need to change index structure:
1. Modify the upstream Python indexing system
2. Update `types.go` to match new JSON structure
3. Update query methods in `service.go` to use new indexes

## Data Format

Expected JSON structure from `comprehensive_index_dump.json`:
```json
{
  "metadata": { "generated_at": "...", "total_employees": 100, ... },
  "lookups": {
    "employees": {
      "uid": {
        "uid": "...",
        "full_name": "...",
        "email": "...",
        "job_title": "...",
        "slack_uid": "...",
        "github_id": "...",
        "rhat_geo": "NA",
        "cost_center": 12345,
        "manager_uid": "...",
        "is_people_manager": false
      }
    },
    "teams": {
      "team_name": {
        "name": "...",
        "tab_name": "...",
        "description": "...",
        "type": "team",
        "group": {
          "type": { "name": "team" },
          "resolved_people_uid_list": [...],
          "slack": {
            "channels": [{"channel": "...", "channel_id": "...", "types": [...]}],
            "aliases": [{"alias": "...", "description": "..."}]
          },
          "roles": [{"people": [...], "types": ["manager", "qe", ...]}],
          "jiras": [{"project": "...", "component": "...", "types": ["main"]}],
          "repos": [{"repo": "...", "types": ["source"]}],
          "keywords": [...],
          "emails": [{"address": "...", "name": "..."}],
          "resources": [{"name": "...", "url": "..."}],
          "component_roles": [{"component": "...", "types": ["owner"]}]
        }
      }
    },
    "orgs": { "org_name": { ... } },
    "pillars": { "pillar_name": { ... } },
    "team_groups": { "team_group_name": { ... } }
  },
  "indexes": {
    "membership": {
      "membership_index": { "uid": [{"name": "team", "type": "team"}] },
      "relationship_index": {
        "teams": { "team_name": { "ancestry": {"orgs": [...], "pillars": [...], "team_groups": [...]} } },
        "orgs": { "org_name": { "ancestry": {...} } },
        "pillars": { "pillar_name": { "ancestry": {...} } },
        "team_groups": { "team_group_name": { "ancestry": {...} } }
      }
    },
    "slack_id_mappings": { "slack_uid_to_uid": { "U123": "uid" } },
    "github_id_mappings": { "github_id_to_uid": { "octocat": "uid" } }
  }
}
```

### Employee Fields
The Employee struct includes:
- **Core Identity**: `UID`, `FullName`, `Email`, `JobTitle`
- **External IDs**: `SlackUID`, `GitHubID`
- **Organization Data**: `RhatGeo`, `CostCenter`, `ManagerUID`, `IsPeopleManager`

### Organizational Types
All organizational entities (Team, Org, Pillar, TeamGroup) share the same structure:
- **Basic Fields**: `UID`, `Name`, `TabName`, `Description`, `Type`
- **Group Configuration**: Contains people, Slack channels, roles, Jira mappings, repos, etc.

### Group Configuration
The `Group` struct contains rich metadata about teams/orgs:
- **Slack Integration**: Channels with IDs, types, and aliases
- **Roles**: People assignments to roles (manager, qe, team_lead, etc.)
- **Jira Projects**: Project/component mappings with types
- **Repositories**: GitHub repos with ownership roles
- **Communication**: Email addresses and keywords
- **Resources**: Documentation links and other resources
- **Component Ownership**: Component role mappings

## Service Interface Methods

All methods available on `ServiceInterface`:

### Employee Queries
- `GetEmployeeByUID(uid string) *Employee` - Lookup by UID
- `GetEmployeeBySlackID(slackID string) *Employee` - Lookup by Slack ID
- `GetEmployeeByGitHubID(githubID string) *Employee` - Lookup by GitHub username
- `GetManagerForEmployee(uid string) *Employee` - Get employee's manager

### Entity Queries
- `GetTeamByName(teamName string) *Team` - Get team details
- `GetOrgByName(orgName string) *Org` - Get organization details
- `GetPillarByName(pillarName string) *Pillar` - Get pillar details
- `GetTeamGroupByName(teamGroupName string) *TeamGroup` - Get team group details

### Membership Queries
- `GetTeamsForUID(uid string) []string` - Get all teams for a user
- `GetTeamsForSlackID(slackID string) []string` - Get all teams for a Slack user
- `GetTeamMembers(teamName string) []Employee` - Get all members of a team
- `IsEmployeeInTeam(uid string, teamName string) bool` - Check team membership
- `IsSlackUserInTeam(slackID string, teamName string) bool` - Check team membership by Slack ID

### Organization Queries
- `IsEmployeeInOrg(uid string, orgName string) bool` - Check org membership
- `IsSlackUserInOrg(slackID string, orgName string) bool` - Check org membership by Slack ID
- `GetUserOrganizations(slackUserID string) []OrgInfo` - Get all orgs for a user

### Enumeration Methods
- `GetAllEmployeeUIDs() []string` - Get all employee UIDs
- `GetAllTeamNames() []string` - Get all team names
- `GetAllOrgNames() []string` - Get all organization names
- `GetAllPillarNames() []string` - Get all pillar names
- `GetAllTeamGroupNames() []string` - Get all team group names

### Data Management
- `GetVersion() DataVersion` - Get current data version
- `LoadFromDataSource(ctx context.Context, source DataSource) error` - Load data from a source
- `StartDataSourceWatcher(ctx context.Context, source DataSource) error` - Start watching for changes

## Performance Characteristics

All queries are O(1) via pre-computed indexes:
- `GetEmployeeByUID`: Direct map lookup
- `GetEmployeeBySlackID`: Index lookup + map lookup
- `GetEmployeeByGitHubID`: Index lookup + map lookup
- `GetManagerForEmployee`: Two direct map lookups (employee + manager)
- `GetTeamByName`, `GetOrgByName`, `GetPillarByName`, `GetTeamGroupByName`: Direct map lookups
- `GetTeamsForUID`: Index lookup (no traversal)
- `IsEmployeeInTeam`: Index scan (pre-computed memberships only)
- `GetUserOrganizations`: Index lookup with flattened ancestry
- `GetAllEmployeeUIDs`, `GetAllTeamNames`, `GetAllOrgNames`, `GetAllPillarNames`, `GetAllTeamGroupNames`: Map key iteration

When adding new queries, ensure they use existing indexes rather than traversing data structures.

## Dependencies

- Go 1.23.0+
- Standard library only (default build)
- Optional with `-tags gcs`:
  - `cloud.google.com/go/storage` - GCS client
  - `github.com/go-logr/logr` - Logging interface
  - `github.com/go-logr/stdr` - stdlib logger adapter

Avoid adding new required dependencies. Optional dependencies should use build tags.
