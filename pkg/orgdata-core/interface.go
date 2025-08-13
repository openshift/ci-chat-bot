package orgdatacore

import "context"

// ServiceInterface defines the core interface for organizational data services
type ServiceInterface interface {

	// Core data access methods

	GetEmployeeByUID(uid string) *Employee
	GetEmployeeBySlackID(slackID string) *Employee
	GetTeamByName(teamName string) *Team
	GetOrgByName(orgName string) *Org

	// Membership queries

	GetTeamsForUID(uid string) []string
	GetTeamsForSlackID(slackID string) []string
	GetTeamMembers(teamName string) []Employee
	IsEmployeeInTeam(uid string, teamName string) bool
	IsSlackUserInTeam(slackID string, teamName string) bool

	// Organization queries

	IsEmployeeInOrg(uid string, orgName string) bool
	IsSlackUserInOrg(slackID string, orgName string) bool
	GetUserOrganizations(slackUserID string) []OrgInfo

	// Data management

	GetVersion() DataVersion
	LoadFromFiles(filePaths []string) error
	StartConfigMapWatcher(ctx context.Context, configMapPaths []string)
}

// OrgInfo represents organization information for a user
type OrgInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
