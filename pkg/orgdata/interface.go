package orgdata

import (
	"context"

	orgdatacore "github.com/openshift-eng/cyborg-data"
)

// OrgDataServiceInterface extends the core service interface with Slack-specific methods
type OrgDataServiceInterface interface {
	// Core data access methods
	GetEmployeeByUID(uid string) *orgdatacore.Employee
	GetEmployeeBySlackID(slackID string) *orgdatacore.Employee
	GetTeamByName(teamName string) *orgdatacore.Team
	GetOrgByName(orgName string) *orgdatacore.Org

	// Membership queries
	GetTeamsForUID(uid string) []string
	GetTeamsForSlackID(slackID string) []string
	GetTeamMembers(teamName string) []orgdatacore.Employee
	IsEmployeeInTeam(uid string, teamName string) bool
	IsSlackUserInTeam(slackID string, teamName string) bool

	// Organization queries
	IsEmployeeInOrg(uid string, orgName string) bool
	IsSlackUserInOrg(slackID string, orgName string) bool
	GetUserOrganizations(slackUserID string) []orgdatacore.OrgInfo

	// Slack-specific methods
	IsSlackUserUID(slackID string, uid string) bool

	// Data management
	GetVersion() orgdatacore.DataVersion
	LoadFromDataSource(ctx context.Context, source orgdatacore.DataSource) error
	StartDataSourceWatcher(ctx context.Context, source orgdatacore.DataSource) error

	// Core service access for DataSource operations
	GetCore() orgdatacore.ServiceInterface
}

// NewIndexedOrgDataService creates a new indexed service using the core package
func NewIndexedOrgDataService() OrgDataServiceInterface {
	coreService := orgdatacore.NewService()
	return &slackOrgDataService{core: coreService}
}

// slackOrgDataService wraps the core service to provide Slack-specific functionality
type slackOrgDataService struct {
	core orgdatacore.ServiceInterface
}

// IsSlackUserUID checks if a Slack ID corresponds to a specific UID
func (s *slackOrgDataService) IsSlackUserUID(slackID string, uid string) bool {
	emp := s.core.GetEmployeeBySlackID(slackID)
	if emp == nil {
		return false
	}
	return emp.UID == uid

}

// Delegate all other methods to the core service
func (s *slackOrgDataService) GetEmployeeByUID(uid string) *orgdatacore.Employee {
	return s.core.GetEmployeeByUID(uid)
}

func (s *slackOrgDataService) GetEmployeeBySlackID(slackID string) *orgdatacore.Employee {
	return s.core.GetEmployeeBySlackID(slackID)
}

func (s *slackOrgDataService) GetTeamByName(teamName string) *orgdatacore.Team {
	return s.core.GetTeamByName(teamName)
}

func (s *slackOrgDataService) GetOrgByName(orgName string) *orgdatacore.Org {
	return s.core.GetOrgByName(orgName)
}

func (s *slackOrgDataService) GetTeamsForUID(uid string) []string {
	return s.core.GetTeamsForUID(uid)
}

func (s *slackOrgDataService) GetTeamsForSlackID(slackID string) []string {
	return s.core.GetTeamsForSlackID(slackID)
}

func (s *slackOrgDataService) GetTeamMembers(teamName string) []orgdatacore.Employee {
	return s.core.GetTeamMembers(teamName)
}

func (s *slackOrgDataService) IsEmployeeInTeam(uid string, teamName string) bool {
	return s.core.IsEmployeeInTeam(uid, teamName)
}

func (s *slackOrgDataService) IsSlackUserInTeam(slackID string, teamName string) bool {
	return s.core.IsSlackUserInTeam(slackID, teamName)
}

func (s *slackOrgDataService) IsEmployeeInOrg(uid string, orgName string) bool {
	return s.core.IsEmployeeInOrg(uid, orgName)
}

func (s *slackOrgDataService) IsSlackUserInOrg(slackID string, orgName string) bool {
	return s.core.IsSlackUserInOrg(slackID, orgName)
}

func (s *slackOrgDataService) GetUserOrganizations(slackUserID string) []orgdatacore.OrgInfo {
	return s.core.GetUserOrganizations(slackUserID)
}

func (s *slackOrgDataService) GetVersion() orgdatacore.DataVersion {
	return s.core.GetVersion()
}

func (s *slackOrgDataService) LoadFromDataSource(ctx context.Context, source orgdatacore.DataSource) error {
	return s.core.LoadFromDataSource(ctx, source)
}

func (s *slackOrgDataService) StartDataSourceWatcher(ctx context.Context, source orgdatacore.DataSource) error {
	return s.core.StartDataSourceWatcher(ctx, source)
}

func (s *slackOrgDataService) GetCore() orgdatacore.ServiceInterface {
	return s.core
}
