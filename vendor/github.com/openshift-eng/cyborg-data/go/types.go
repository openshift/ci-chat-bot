package orgdatacore

import (
	"time"
)

// Employee represents an employee in the organizational data
type Employee struct {
	UID             string `json:"uid"`
	FullName        string `json:"full_name"`
	Email           string `json:"email"`
	JobTitle        string `json:"job_title"`
	SlackUID        string `json:"slack_uid,omitempty"`
	GitHubID        string `json:"github_id,omitempty"`
	RhatGeo         string `json:"rhat_geo,omitempty"`
	CostCenter      int    `json:"cost_center,omitempty"`
	ManagerUID      string `json:"manager_uid,omitempty"`
	IsPeopleManager bool   `json:"is_people_manager,omitempty"`
}

// SlackConfig contains Slack channel and alias configuration
type SlackConfig struct {
	Channels []ChannelInfo `json:"channels,omitempty"`
	Aliases  []AliasInfo   `json:"aliases,omitempty"`
}

// ChannelInfo represents a Slack channel configuration
type ChannelInfo struct {
	Channel     string   `json:"channel"`
	ChannelID   string   `json:"channel_id,omitempty"`
	Description string   `json:"description,omitempty"`
	Types       []string `json:"types,omitempty"`
}

// AliasInfo represents a Slack alias configuration
type AliasInfo struct {
	Alias       string `json:"alias"`
	Description string `json:"description,omitempty"`
}

// RoleInfo represents a role assignment with associated people
type RoleInfo struct {
	People []string `json:"people"`
	Types  []string `json:"types"`
}

// JiraInfo represents Jira project/component configuration
type JiraInfo struct {
	Project     string   `json:"project,omitempty"`
	Component   string   `json:"component,omitempty"`
	Description string   `json:"description,omitempty"`
	View        string   `json:"view,omitempty"`
	Types       []string `json:"types,omitempty"`
}

// RepoInfo represents GitHub repository configuration
type RepoInfo struct {
	Repo        string   `json:"repo,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Path        string   `json:"path,omitempty"`
	Roles       []string `json:"roles,omitempty"`
	Branch      string   `json:"branch,omitempty"`
	Types       []string `json:"types,omitempty"`
}

// EmailInfo represents an email configuration
type EmailInfo struct {
	Address     string `json:"address"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// ResourceInfo represents a resource/documentation link
type ResourceInfo struct {
	Name        string `json:"name"`
	URL         string `json:"url,omitempty"`
	Description string `json:"description,omitempty"`
}

// ComponentRoleInfo represents component ownership information
type ComponentRoleInfo struct {
	Component string   `json:"component"`
	Types     []string `json:"types"`
}

// ParentInfo represents parent reference for hierarchy traversal
type ParentInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Team represents a team in the organizational data
type Team struct {
	UID         string      `json:"uid"`
	Name        string      `json:"name"`
	TabName     string      `json:"tab_name,omitempty"`
	Description string      `json:"description,omitempty"`
	Type        string      `json:"type"`
	Parent      *ParentInfo `json:"parent,omitempty"`
	Group       Group       `json:"group"`
}

// Group contains group metadata and configuration
type Group struct {
	Type                  GroupType           `json:"type"`
	ResolvedPeopleUIDList []string            `json:"resolved_people_uid_list"`
	Slack                 *SlackConfig        `json:"slack,omitempty"`
	Roles                 []RoleInfo          `json:"roles,omitempty"`
	Jiras                 []JiraInfo          `json:"jiras,omitempty"`
	Repos                 []RepoInfo          `json:"repos,omitempty"`
	Keywords              []string            `json:"keywords,omitempty"`
	Emails                []EmailInfo         `json:"emails,omitempty"`
	Resources             []ResourceInfo      `json:"resources,omitempty"`
	ComponentRoles        []ComponentRoleInfo `json:"component_roles,omitempty"`
}

// GroupType contains group type information
type GroupType struct {
	Name string `json:"name"`
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
	Employees  map[string]Employee  `json:"employees"`
	Teams      map[string]Team      `json:"teams"`
	Orgs       map[string]Org       `json:"orgs"`
	Pillars    map[string]Pillar    `json:"pillars,omitempty"`
	TeamGroups map[string]TeamGroup `json:"team_groups,omitempty"`
	Components map[string]Component `json:"components,omitempty"`
}

// Org represents an organization in the organizational data
type Org struct {
	UID         string      `json:"uid"`
	Name        string      `json:"name"`
	TabName     string      `json:"tab_name,omitempty"`
	Description string      `json:"description,omitempty"`
	Type        string      `json:"type"`
	Parent      *ParentInfo `json:"parent,omitempty"`
	Group       Group       `json:"group"`
}

// Pillar represents a pillar in the organizational hierarchy
type Pillar struct {
	UID         string      `json:"uid"`
	Name        string      `json:"name"`
	TabName     string      `json:"tab_name,omitempty"`
	Description string      `json:"description,omitempty"`
	Type        string      `json:"type"`
	Parent      *ParentInfo `json:"parent,omitempty"`
	Group       Group       `json:"group"`
}

// TeamGroup represents a team group in the organizational hierarchy
type TeamGroup struct {
	UID         string      `json:"uid"`
	Name        string      `json:"name"`
	TabName     string      `json:"tab_name,omitempty"`
	Description string      `json:"description,omitempty"`
	Type        string      `json:"type"`
	Parent      *ParentInfo `json:"parent,omitempty"`
	Group       Group       `json:"group"`
}

// Component represents a component in the organizational data
type Component struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Description string      `json:"description,omitempty"`
	Parent      *ParentInfo `json:"parent,omitempty"`
	ParentPath  string      `json:"parent_path,omitempty"`
	Repos       []RepoInfo  `json:"repos,omitempty"`
	Jiras       []JiraInfo  `json:"jiras,omitempty"`
	ReposList   []string    `json:"repos_list,omitempty"`
}

// Indexes contains pre-computed lookup tables
type Indexes struct {
	Membership       MembershipIndex  `json:"membership"`
	SlackIDMappings  SlackIDMappings  `json:"slack_id_mappings"`
	GitHubIDMappings GitHubIDMappings `json:"github_id_mappings,omitempty"`
	Jira             JiraIndex        `json:"jira,omitempty"`
}

// SlackIDMappings contains Slack ID to UID mappings
type SlackIDMappings struct {
	SlackUIDToUID map[string]string `json:"slack_uid_to_uid"`
}

// GitHubIDMappings contains GitHub ID to UID mappings
type GitHubIDMappings struct {
	GitHubIDToUID map[string]string `json:"github_id_to_uid"`
}

// MembershipIndex represents the membership index structure
type MembershipIndex struct {
	MembershipIndex map[string][]MembershipInfo `json:"membership_index"`
}

// MembershipInfo represents a membership entry with name and type
type MembershipInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// HierarchyPathEntry represents a single entry in a hierarchy path
type HierarchyPathEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// HierarchyNode represents a node in the descendants tree with nested children
type HierarchyNode struct {
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Children []HierarchyNode `json:"children"`
}

// JiraOwnerInfo represents an entity that owns a Jira project/component
type JiraOwnerInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// JiraIndex contains Jira project/component to team mappings
// Structure: project -> component -> list of owner entities
// Special key "_project_level" indicates project-level ownership
// Note: In JSON, projects are directly under indexes.jira (no wrapper object)
type JiraIndex map[string]map[string][]JiraOwnerInfo

// JiraOwnership represents a project/component ownership entry
type JiraOwnership struct {
	Project   string `json:"project"`
	Component string `json:"component"`
}

// DataVersion tracks the version of loaded data for hot reload
type DataVersion struct {
	LoadTime      time.Time
	ConfigMaps    map[string]string // ConfigMap name -> checksum/version
	OrgCount      int
	EmployeeCount int
}
