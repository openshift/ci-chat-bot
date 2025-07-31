package orgdata

import (
	"time"
)

// Employee represents a resolved person in the organizational data
// Only includes essential fields as requested
type Employee struct {
	UID         string `json:"uid"`
	SlackUID    string `json:"slack_uid"`
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email,omitempty"`
	JobTitle    string `json:"job_title,omitempty"`
}

// Role represents a role with associated people and role types
type Role struct {
	People []Employee `json:"people"`
	Types  []string   `json:"types"`
}

// Group contains the team/org metadata and resolved data
type Group struct {
	Type           GroupType  `json:"type"`
	Parent         string     `json:"parent,omitempty"`
	ResolvedPeople []Employee `json:"resolved_people,omitempty"`
	ResolvedRoles  []Role     `json:"resolved_roles,omitempty"`
	// Add other group fields as needed for future extensibility
}

// GroupType defines the type of organizational unit
type GroupType struct {
	Name      string   `json:"name"`
	Visualize bool     `json:"visualize,omitempty"`
	Groups    []string `json:"visualize_group,omitempty"`
}

// OrgUnit represents any organizational unit (Org, Team Group, Team)
type OrgUnit struct {
	Name        string     `json:"name"`
	Group       Group      `json:"group"`
	Children    []*OrgUnit `json:"children,omitempty"`
	TabName     string     `json:"tab_name,omitempty"`
	Description string     `json:"description,omitempty"`

	// Computed fields for fast access
	AllEmployees []Employee `json:"-"` // Flattened list including from children
	OrgPath      []string   `json:"-"` // Path from root org to this unit
}

// TeamMembership represents an employee's membership in teams/orgs
type TeamMembership struct {
	Employee Employee
	Teams    []string // List of team names
	Orgs     []string // List of org names (including parent orgs)
	Roles    []string // List of role types across all teams
}

// DataVersion tracks the version of loaded data for hot reload
type DataVersion struct {
	LoadTime      time.Time
	ConfigMaps    map[string]string // ConfigMap name -> checksum/version
	OrgCount      int
	EmployeeCount int
}

// QueryCondition represents a query filter condition
type QueryCondition struct {
	Field    string
	Value    interface{}
	Operator string // "eq", "in", "contains", etc.
}

// QueryResult represents the result of a complex query
type QueryResult struct {
	Teams     []*OrgUnit
	Employees []Employee
	Orgs      []*OrgUnit
	Count     int
}

// QueryInterface defines the extensible query system
type QueryInterface interface {
	// Current use cases
	GetTeamsForUID(uid string) []string
	GetTeamsForSlackID(slackID string) []string
	IsEmployeeInOrg(uid string, orgName string) bool
	GetTeamMembers(teamName string) []Employee

	// Extensible query system for future use cases
	Query(conditions []QueryCondition) (*QueryResult, error)
	QueryEmployees(conditions []QueryCondition) ([]Employee, error)
	QueryTeams(conditions []QueryCondition) ([]*OrgUnit, error)
}
