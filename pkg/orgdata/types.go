package orgdata

import (
	"time"
)

// Employee represents an employee in the organizational data
type Employee struct {
	UID      string `json:"uid"`
	FullName string `json:"full_name"`
	Email    string `json:"email"`
	JobTitle string `json:"job_title"`
	SlackUID string `json:"slack_uid"`
}

// Team represents a team in the organizational data
type Team struct {
	UID                   string   `json:"uid"`
	Name                  string   `json:"name"`
	Type                  string   `json:"type"`
	Group                 Group    `json:"group"`
	ResolvedPeopleUIDList []string `json:"resolved_people_uid_list"`
}

// Org represents an organization in the organizational data
type Org struct {
	UID   string `json:"uid"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Group struct {
		ResolvedPeopleUIDList []string `json:"resolved_people_uid_list"`
	} `json:"group"`
}

// Group contains group metadata
type Group struct {
	Type                  GroupType `json:"type"`
	ResolvedPeopleUIDList []string  `json:"resolved_people_uid_list"`
}

// GroupType contains group type information
type GroupType struct {
	Name string `json:"name"`
}

// MembershipInfo represents a membership entry with name and type
type MembershipInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// RelationshipInfo represents relationship information with ancestry
type RelationshipInfo struct {
	Ancestry struct {
		Orgs       []string `json:"orgs"`
		Teams      []string `json:"teams"`
		Pillars    []string `json:"pillars"`
		TeamGroups []string `json:"team_groups"`
	} `json:"ancestry"`
}

// Data represents the comprehensive organizational data structure
type Data struct {
	Metadata Metadata `json:"metadata"`
	Lookups  Lookups  `json:"lookups"`
	Indexes  Indexes  `json:"indexes"`
}

// Metadata contains summary information about the data
type Metadata struct {
	GeneratedAt    string `json:"generated_at"`
	DataVersion    string `json:"data_version"`
	TotalEmployees int    `json:"total_employees"`
	TotalOrgs      int    `json:"total_orgs"`
	TotalTeams     int    `json:"total_teams"`
}

// Lookups contains the main data objects
type Lookups struct {
	Employees  map[string]Employee `json:"employees"`
	Teams      map[string]Team     `json:"teams"`
	Orgs       map[string]Org      `json:"orgs"`
	Pillars    map[string]Org      `json:"pillars"`
	TeamGroups map[string]Org      `json:"team_groups"`
}

// Indexes contains pre-computed lookup tables
type Indexes struct {
	Membership      MembershipIndex `json:"membership"`
	SlackIDMappings SlackIDMappings `json:"slack_id_mappings"`
}

// SlackIDMappings contains Slack ID to UID mappings
type SlackIDMappings struct {
	SlackUIDToUID map[string]string `json:"slack_uid_to_uid"`
}

// MembershipIndex represents the membership index structure
type MembershipIndex struct {
	MembershipIndex   map[string][]MembershipInfo            `json:"membership_index"`
	RelationshipIndex map[string]map[string]RelationshipInfo `json:"relationship_index"`
}

// OrgUnit represents any organizational unit (Org, Team Group, Team) - for API compatibility
type OrgUnit struct {
	Name  string `json:"name"`
	Group struct {
		Type struct {
			Name string `json:"name"`
		} `json:"type"`
	} `json:"group"`
	OrgPath []string `json:"-"` // Path from root org to this unit
}

// DataVersion tracks the version of loaded data for hot reload
type DataVersion struct {
	LoadTime      time.Time
	ConfigMaps    map[string]string // ConfigMap name -> checksum/version
	OrgCount      int
	EmployeeCount int
}
