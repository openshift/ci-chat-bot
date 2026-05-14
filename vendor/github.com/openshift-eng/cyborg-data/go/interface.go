package orgdatacore

import (
	"context"
	"io"
	"time"
)

// DataSource provides organizational data from external storage.
// The caller is responsible for calling Close() when done with the data source.
type DataSource interface {
	// Load returns a reader with the organizational data JSON.
	// The caller must close the returned ReadCloser when done.
	Load(ctx context.Context) (io.ReadCloser, error)

	// Watch monitors the data source for changes and calls callback on updates.
	// Blocks until context is cancelled or an error occurs.
	Watch(ctx context.Context, callback func() error) error

	// String returns a human-readable description of this data source.
	String() string

	// Close releases any resources held by this data source.
	io.Closer
}

type ServiceInterface interface {
	GetEmployeeByUID(uid string) *Employee
	GetEmployeeBySlackID(slackID string) *Employee
	GetEmployeeByGitHubID(githubID string) *Employee
	GetEmployeeByEmail(email string) *Employee
	GetManagerForEmployee(uid string) *Employee
	GetTeamByName(teamName string) *Team
	GetTeamsBySlackChannel(channel string) []Team
	GetOrgByName(orgName string) *Org
	GetPillarByName(pillarName string) *Pillar
	GetTeamGroupByName(teamGroupName string) *TeamGroup

	GetUserMemberships(uid string) []MembershipInfo
	GetUserTeams(uid string) []string
	GetTeamsForUID(uid string) []string
	GetTeamsForSlackID(slackID string) []string
	GetTeamMembers(teamName string) []Employee
	GetOrgMembers(orgName string) []Employee
	IsEmployeeInTeam(uid string, teamName string) bool
	IsSlackUserInTeam(slackID string, teamName string) bool

	IsEmployeeInOrg(uid string, orgName string) bool
	IsSlackUserInOrg(slackID string, orgName string) bool
	GetUserOrganizations(slackUserID string) []OrgInfo

	GetTeamEscalation(teamName string) []EscalationContactInfo

	GetVersion() DataVersion
	GetDataAge() time.Duration
	IsDataStale(maxAge time.Duration) bool
	LoadFromDataSource(ctx context.Context, source DataSource) error
	StartDataSourceWatcher(ctx context.Context, source DataSource) error
	StopWatcher()

	GetAllEmployeeUIDs() []string
	GetAllEmployees() []Employee
	GetAllTeamNames() []string
	GetAllTeams() []Team
	GetAllOrgNames() []string
	GetAllOrgs() []Org
	GetAllPillarNames() []string
	GetAllPillars() []Pillar
	GetAllTeamGroupNames() []string
	GetAllTeamGroups() []TeamGroup

	// Hierarchy queries
	GetHierarchyPath(entityName string, entityType string) []HierarchyPathEntry
	GetDescendantsTree(entityName string) *HierarchyNode

	// Component queries
	GetComponentByName(name string) *Component
	GetAllComponents() []Component
	GetAllComponentNames() []string
	GetTeamsForComponent(componentName string) []ComponentOwnerInfo
	GetComponentsForTeam(teamName string) []ComponentOwnership

	// Jira queries
	GetJiraProjects() []string
	GetJiraComponents(project string) []string
	GetTeamsByJiraProject(project string) []JiraOwnerInfo
	GetTeamsByJiraComponent(project, component string) []JiraOwnerInfo
	GetJiraOwnershipForTeam(teamName string) []JiraOwnership

	// Context queries
	GetContextForTeam(teamName string) []ContextItemInfo
	GetContextForEntity(entityName string, entityType string) []ContextItemInfo
	GetContextByType(entityName string, contextType string, entityType string) []ContextItemInfo
	GetAllContextTypesForEntity(entityName string, entityType string) []string
	GetContextTypeDescriptions() map[string]string
}

type OrgInfo struct {
	Name string      `json:"name"`
	Type OrgInfoType `json:"type"`
}

type GCSConfig struct {
	Bucket          string        `json:"bucket"`
	ObjectPath      string        `json:"object_path"`
	ProjectID       string        `json:"project_id"`
	CredentialsJSON string        `json:"credentials_json"`
	CheckInterval   time.Duration `json:"check_interval"`
}
