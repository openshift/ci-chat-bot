# Go Implementation Guide

## Quick Reference

```bash
cd go
make test           # Run all tests
make test-with-gcs  # Test with GCS support
make lint           # Lint code
go test -run TestEmployee  # Run specific tests
```

## Extending the API

### Adding a New Query Method

Follow this exact pattern for every new method:

#### Step 1: Add to Interface (`interface.go`)

```go
type ServiceInterface interface {
    // ... existing methods ...

    // NewMethod returns X for the given Y.
    NewMethod(param string) *ResultType
}
```

#### Step 2: Implement in Service (`service.go`)

```go
func (s *Service) NewMethod(param string) *ResultType {
    s.mu.RLock()
    defer s.mu.RUnlock()

    // ALWAYS check for nil data first
    if s.data == nil || s.data.Lookups.SomeMap == nil {
        return nil
    }

    // Use O(1) index lookup - NEVER traverse
    result, exists := s.data.Lookups.SomeMap[param]
    if !exists {
        return nil
    }

    return &result
}
```

#### Step 3: Add Tests (`*_test.go`)

```go
func TestNewMethod(t *testing.T) {
    service := setupTestService(t)

    tests := []struct {
        name     string
        param    string
        expected *ResultType
    }{
        {
            name:     "existing value",
            param:    "valid",
            expected: &ResultType{...},
        },
        {
            name:     "nonexistent value",
            param:    "invalid",
            expected: nil,
        },
        {
            name:     "empty param",
            param:    "",
            expected: nil,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := service.NewMethod(tt.param)
            if !reflect.DeepEqual(result, tt.expected) {
                t.Errorf("NewMethod(%q) = %+v, expected %+v",
                    tt.param, result, tt.expected)
            }
        })
    }
}
```

### Design Rules

1. **Thread Safety**: Always use `s.mu.RLock()` for reads, `s.mu.Lock()` for writes
2. **Nil Checks**: Check `s.data` and the specific map/index before accessing
3. **Return Pointers**: Return `*Type` for entities (allows nil for "not found")
4. **Return Slices**: Return `[]Type` for collections (empty slice, not nil)
5. **Defer Unlock**: Always `defer s.mu.RUnlock()` immediately after lock

### Performance Guidelines

**Hot path methods** (called frequently, latency-sensitive):
- MUST use pre-computed indexes for O(1) lookup
- Examples: `GetEmployeeBySlackID`, `IsEmployeeInTeam`, `GetTeamsForUID`

**Cold path methods** (infrequent, admin/debug use):
- MAY traverse data if index cost outweighs benefit
- Document the O(n) complexity in method comment
- Examples: `GetEmployeeByEmail`, `GetAllEmployeeUIDs`

When adding a new method, consider:
- How often will this be called?
- Is there an existing index that covers this case?
- Would a new index significantly grow the data file?

If traversal is acceptable, document it:
```go
// GetEmployeeByEmail finds an employee by email address.
// Note: O(n) scan - use GetEmployeeByUID for hot paths.
func (s *Service) GetEmployeeByEmail(email string) *Employee {
```

### Method Categories

| Category | Return Type | Nil Check Pattern |
|----------|-------------|-------------------|
| Single entity lookup | `*Employee` | Return `nil` if not found |
| Collection lookup | `[]string` | Return empty slice `[]string{}` |
| Boolean check | `bool` | Return `false` if data unavailable |
| Version/metadata | `DataVersion` | Return zero value |

### Index Lookup Patterns

**Direct map lookup** (e.g., GetEmployeeByUID):
```go
if emp, exists := s.data.Lookups.Employees[uid]; exists {
    return &emp
}
return nil
```

**Two-step lookup** (e.g., GetEmployeeBySlackID):
```go
uid := s.data.Indexes.SlackIDMappings.SlackUIDToUID[slackID]
if uid == "" {
    return nil
}
if emp, exists := s.data.Lookups.Employees[uid]; exists {
    return &emp
}
return nil
```

**Membership check** (e.g., GetTeamsForUID):
```go
var teams []string
for _, m := range s.data.Indexes.Membership.MembershipIndex[uid] {
    if m.Type == string(MembershipTeam) {
        teams = append(teams, m.Name)
    }
}
return teams
```

## File Organization

| File | Purpose |
|------|---------|
| `interface.go` | `ServiceInterface` + `DataSource` interface |
| `service.go` | `Service` implementation |
| `types.go` | Data structures (Employee, Team, etc.) |
| `options.go` | Functional options (`WithLogger`, etc.) |
| `errors.go` | Sentinel errors |
| `enums.go` | Type-safe enums |

### Test File Mapping

| Entity | Test File |
|--------|-----------|
| Employee queries | `employee_test.go` |
| Team queries | `team_test.go` |
| Org queries | `organization_test.go` |
| Pillar queries | `pillar_test.go` |
| TeamGroup queries | `team_group_test.go` |
| Service lifecycle | `service_test.go` |
| Edge cases | `service_edge_cases_test.go` |

## Adding a New Entity Type

1. Add struct to `types.go`:
```go
type NewEntity struct {
    UID         string `json:"uid"`
    Name        string `json:"name"`
    // ... fields match JSON structure
}
```

2. Add to `Lookups` struct in `types.go`:
```go
type Lookups struct {
    // ... existing fields ...
    NewEntities map[string]NewEntity `json:"new_entities,omitempty"`
}
```

3. Add query method following the pattern above

4. Add tests and update `testdata/test_org_data.json`

## Adding a New Index Mapping

For new external ID → UID mappings:

1. Add mapping struct to `types.go`:
```go
type NewIDMappings struct {
    NewIDToUID map[string]string `json:"new_id_to_uid"`
}
```

2. Add to `Indexes` struct:
```go
type Indexes struct {
    // ... existing fields ...
    NewIDMappings NewIDMappings `json:"new_id_mappings,omitempty"`
}
```

3. Add lookup method following the two-step pattern

## Build Tags

- Default build: No external dependencies
- `-tags gcs`: Includes GCS SDK

When adding GCS-specific code, use build constraints:
```go
//go:build gcs

package orgdatacore
// GCS-specific implementation
```

## Logging

Use structured logging with `slog`:
```go
s.logger.Info("action completed", "key", value, "count", n)
s.logger.Error("action failed", "error", err)
```

## Functional Options Pattern

For configurable constructors:
```go
type ServiceOption func(*serviceConfig)

func WithLogger(logger *slog.Logger) ServiceOption {
    return func(cfg *serviceConfig) {
        cfg.logger = logger
    }
}
```

## Remember

After implementing in Go:
1. **Implement the same method in Python** (see `python/CLAUDE.md`)
2. Run `make test` from repository root to verify parity
