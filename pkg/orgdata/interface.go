package orgdata

import "context"

// OrgDataServiceInterface defines the interface that both OrgDataService and IndexedOrgDataService implement
type OrgDataServiceInterface interface {
	// Core methods used by AuthorizationService
	IsSlackUserUID(slackID string, uid string) bool
	IsSlackUserInTeam(slackID string, teamName string) bool
	IsSlackUserInOrg(slackID string, orgName string) bool
	GetTeamsForSlackID(slackID string) []string
	GetEmployeeBySlackID(slackID string) *Employee
	GetVersion() DataVersion

	// Additional methods for compatibility
	GetTeamsForUID(uid string) []string
	IsEmployeeInOrg(uid string, orgName string) bool
	GetTeamMembers(teamName string) []Employee
	StartConfigMapWatcher(ctx context.Context, configMapPaths []string)

	// Methods for building organization information
	GetUserOrganizations(slackUserID string) []OrgInfo

	// Data loading method
	LoadFromFiles(filePaths []string) error
}
