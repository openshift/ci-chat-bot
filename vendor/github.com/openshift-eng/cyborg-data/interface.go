package orgdatacore

import (
	"context"
	"io"
	"time"
)

// DataSource represents a source of organizational data
type DataSource interface {
	// Load returns a reader for the organizational data JSON
	Load(ctx context.Context) (io.ReadCloser, error)

	// Watch monitors for changes and calls the callback when data is updated
	Watch(ctx context.Context, callback func() error) error

	// String returns a description of this data source
	String() string
}

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
	LoadFromDataSource(ctx context.Context, source DataSource) error
	StartDataSourceWatcher(ctx context.Context, source DataSource) error
}

// OrgInfo represents organization information for a user
type OrgInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// GCSConfig represents Google Cloud Storage configuration for data loading
type GCSConfig struct {
	Bucket          string        `json:"bucket"`
	ObjectPath      string        `json:"object_path"`
	ProjectID       string        `json:"project_id"`
	CredentialsJSON string        `json:"credentials_json"` // Optional: service account JSON
	CheckInterval   time.Duration `json:"check_interval"`
}
