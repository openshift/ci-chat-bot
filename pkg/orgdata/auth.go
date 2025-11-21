package orgdata

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	orgdatacore "github.com/openshift-eng/cyborg-data"
	"gopkg.in/yaml.v2"
	"k8s.io/klog/v2"
)

// AuthConfigSource defines the interface for loading authorization configuration from different sources
type AuthConfigSource interface {
	// Load loads the authorization configuration
	Load(ctx context.Context) (*AuthorizationConfig, error)

	// Watch starts watching for configuration changes and calls the callback when changes are detected
	Watch(ctx context.Context, callback func(*AuthorizationConfig)) error

	// String returns a description of the configuration source
	String() string
}

// AuthorizationConfig defines the authorization rules for different commands
type AuthorizationConfig struct {
	Rules []AuthRule `yaml:"rules"`
}

// AuthRule defines a single authorization rule
type AuthRule struct {
	Command      string   `yaml:"command"`                 // Command pattern (e.g., "launch", "rosa_create", "*")
	AllowedOrgs  []string `yaml:"allowed_orgs,omitempty"`  // Organizations that can execute this command
	AllowedTeams []string `yaml:"allowed_teams,omitempty"` // Teams that can execute this command
	AllowedUIDs  []string `yaml:"allowed_uids,omitempty"`  // Specific user UIDs that can execute this command
	AllowAll     bool     `yaml:"allow_all,omitempty"`     // If true, allows all users regardless of organization
	DenyMessage  string   `yaml:"deny_message,omitempty"`  // Custom message when access is denied
}

// FileAuthConfigSource implements AuthConfigSource for file-based configuration
type FileAuthConfigSource struct {
	filePath string
	lastMod  time.Time
}

// NewFileAuthConfigSource creates a new file-based auth config source
func NewFileAuthConfigSource(filePath string) AuthConfigSource {
	return &FileAuthConfigSource{
		filePath: filePath,
	}
}

// Load loads the authorization configuration from file
func (f *FileAuthConfigSource) Load(ctx context.Context) (*AuthorizationConfig, error) {
	// Handle empty path case (no auth config)
	if f.filePath == "" {
		return &AuthorizationConfig{Rules: []AuthRule{}}, nil
	}

	data, err := os.ReadFile(f.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read authorization config: %w", err)
	}

	var config AuthorizationConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse authorization config: %w", err)
	}

	// Update last modified time for watching
	if stat, err := os.Stat(f.filePath); err == nil {
		f.lastMod = stat.ModTime()
	}

	return &config, nil
}

// Watch starts watching for file changes with polling
func (f *FileAuthConfigSource) Watch(ctx context.Context, callback func(*AuthorizationConfig)) error {
	if f.filePath == "" {
		// No file to watch, just wait for context cancellation
		<-ctx.Done()
		return ctx.Err()
	}

	ticker := time.NewTicker(60 * time.Second) // Check every 60 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			stat, err := os.Stat(f.filePath)
			if err != nil {
				klog.Errorf("Failed to check auth config file %s: %v", f.filePath, err)
				continue
			}

			if stat.ModTime().After(f.lastMod) {
				config, err := f.Load(ctx)
				if err != nil {
					klog.Errorf("Failed to reload auth config %s: %v", f.filePath, err)
					continue
				}
				callback(config)
			}
		}
	}
}

// String returns a description of the auth config source
func (f *FileAuthConfigSource) String() string {
	if f.filePath == "" {
		return "no auth config (allow all)"
	}
	return fmt.Sprintf("file: %s", f.filePath)
}

// AuthorizationService handles authorization checking
type AuthorizationService struct {
	config         *AuthorizationConfig
	orgDataService orgdatacore.ServiceInterface
	configSource   AuthConfigSource
	mu             sync.RWMutex
}

// NewAuthorizationService creates a new authorization service with file-based config
func NewAuthorizationService(orgDataService orgdatacore.ServiceInterface, configPath string) *AuthorizationService {
	configSource := NewFileAuthConfigSource(configPath)
	return &AuthorizationService{
		orgDataService: orgDataService,
		configSource:   configSource,
	}
}

// NewAuthorizationServiceWithSource creates a new authorization service with a custom config source
func NewAuthorizationServiceWithSource(orgDataService orgdatacore.ServiceInterface, configSource AuthConfigSource) *AuthorizationService {
	return &AuthorizationService{
		orgDataService: orgDataService,
		configSource:   configSource,
	}
}

// LoadConfig loads the authorization configuration from the configured source
func (a *AuthorizationService) LoadConfig(ctx context.Context) error {
	config, err := a.configSource.Load(ctx)
	if err != nil {
		return fmt.Errorf("failed to load authorization config from %s: %w", a.configSource.String(), err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.config = config

	return nil
}

// CheckAuthorization checks if a Slack user is authorized to execute a command
func (a *AuthorizationService) CheckAuthorization(slackUserID, command string) (bool, string) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.config == nil {
		// No config loaded, allow all access
		return true, ""
	}

	// If no org data service available, fall back to permit all mode
	if a.orgDataService == nil {
		klog.V(2).Info("No organizational data available, running in permit all mode")
		return true, ""
	}

	// Find matching rule for the command
	var matchingRule *AuthRule
	for _, rule := range a.config.Rules {
		if rule.Command == command || rule.Command == "*" {
			matchingRule = &rule
			break
		}
	}

	if matchingRule == nil {
		// No rule found, allow access
		return true, ""
	}

	// Check if this rule allows all users
	if matchingRule.AllowAll {
		return true, ""
	}

	// Check if the user is in the allowed UIDs (the highest specificity)
	employee := a.orgDataService.GetEmployeeBySlackID(slackUserID)
	for _, allowedUID := range matchingRule.AllowedUIDs {
		if employee != nil && employee.UID == allowedUID {
			return true, ""
		}
	}

	// Check if a user is in any of the allowed teams
	for _, allowedTeam := range matchingRule.AllowedTeams {
		if a.orgDataService.IsSlackUserInTeam(slackUserID, allowedTeam) {
			return true, ""
		}
	}

	// Check if the user is in any of the allowed orgs (the lowest specificity)
	for _, allowedOrg := range matchingRule.AllowedOrgs {
		if a.orgDataService.IsSlackUserInOrg(slackUserID, allowedOrg) {
			return true, ""
		}
	}

	// User is not authorized
	denyMessage := matchingRule.DenyMessage
	if denyMessage == "" {
		var requirements []string

		if len(matchingRule.AllowedUIDs) > 0 {
			requirements = append(requirements, fmt.Sprintf("specific users: %v", matchingRule.AllowedUIDs))
		}
		if len(matchingRule.AllowedTeams) > 0 {
			requirements = append(requirements, fmt.Sprintf("team membership: %v", matchingRule.AllowedTeams))
		}
		if len(matchingRule.AllowedOrgs) > 0 {
			requirements = append(requirements, fmt.Sprintf("organization membership: %v", matchingRule.AllowedOrgs))
		}

		if len(requirements) > 0 {
			denyMessage = fmt.Sprintf("You are not authorized to execute the '%s' command. Required: %s",
				command, strings.Join(requirements, " OR "))
		} else {
			denyMessage = fmt.Sprintf("You are not authorized to execute the '%s' command.", command)
		}
	}

	return false, denyMessage
}

// GetUserOrganizations returns all organizations a Slack user belongs to (names only, for backward compatibility)
func (a *AuthorizationService) GetUserOrganizations(slackUserID string) []string {
	orgInfos := a.GetUserOrganizationsWithType(slackUserID)
	var orgs []string
	for _, orgInfo := range orgInfos {
		orgs = append(orgs, orgInfo.Name)
	}
	return orgs
}

// GetUserOrganizationsWithType returns all organizations a Slack user belongs to with their types
func (a *AuthorizationService) GetUserOrganizationsWithType(slackUserID string) []orgdatacore.OrgInfo {
	if a.orgDataService == nil {
		return []orgdatacore.OrgInfo{}
	}
	return a.orgDataService.GetUserOrganizations(slackUserID)
}

// GetUserTeams returns all teams a Slack user belongs to
func (a *AuthorizationService) GetUserTeams(slackUserID string) []string {
	if a.orgDataService == nil {
		return []string{}
	}
	return a.orgDataService.GetTeamsForSlackID(slackUserID)
}

// UserInfo represents comprehensive user information for display
type UserInfo struct {
	Employee      *orgdatacore.Employee
	Organizations []orgdatacore.OrgInfo
	Teams         []string
	SlackID       string
	HasOrgData    bool
}

// GetUserInfo returns comprehensive user information for a Slack user
func (a *AuthorizationService) GetUserInfo(slackUserID string) *UserInfo {
	if a.orgDataService == nil {
		return &UserInfo{
			SlackID:    slackUserID,
			HasOrgData: false,
		}
	}

	employee := a.orgDataService.GetEmployeeBySlackID(slackUserID)
	orgs := a.GetUserOrganizationsWithType(slackUserID)
	teams := a.orgDataService.GetTeamsForSlackID(slackUserID)

	return &UserInfo{
		Employee:      employee,
		Organizations: orgs,
		Teams:         teams,
		SlackID:       slackUserID,
		HasOrgData:    employee != nil,
	}
}

// GetUserCommands returns all commands the user has access to, categorized by access type
func (a *AuthorizationService) GetUserCommands(slackUserID string) map[string][]string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := map[string][]string{
		"allow_all":    {},
		"by_uid":       {},
		"by_team":      {},
		"by_org":       {},
		"unrestricted": {}, // Commands with no rules (default allow)
	}

	if a.config == nil {
		// No config means all commands are unrestricted
		return result
	}

	if a.orgDataService == nil {
		// No org data means we can't evaluate org/team based rules, return empty
		return result
	}

	// Track which commands have rules
	commandsWithRules := make(map[string]bool)

	employee := a.orgDataService.GetEmployeeBySlackID(slackUserID)
	for _, rule := range a.config.Rules {
		commandsWithRules[rule.Command] = true

		// Skip wildcard rules for display
		if rule.Command == "*" {
			continue
		}

		// Check if user has access via allow_all
		if rule.AllowAll {
			result["allow_all"] = append(result["allow_all"], rule.Command)
			continue
		}

		// Check if user has access via UID
		hasUIDAccess := false
		for _, allowedUID := range rule.AllowedUIDs {
			if employee != nil && employee.UID == allowedUID {
				result["by_uid"] = append(result["by_uid"], rule.Command)
				hasUIDAccess = true
				break
			}
		}
		if hasUIDAccess {
			continue
		}

		// Check if user has access via team
		hasTeamAccess := false
		for _, allowedTeam := range rule.AllowedTeams {
			if a.orgDataService.IsSlackUserInTeam(slackUserID, allowedTeam) {
				result["by_team"] = append(result["by_team"], rule.Command)
				hasTeamAccess = true
				break
			}
		}
		if hasTeamAccess {
			continue
		}

		// Check if user has access via organization
		for _, allowedOrg := range rule.AllowedOrgs {
			if a.orgDataService.IsSlackUserInOrg(slackUserID, allowedOrg) {
				result["by_org"] = append(result["by_org"], rule.Command)
				break
			}
		}
	}

	return result
}

// StartConfigWatcher starts watching for configuration changes
func (a *AuthorizationService) StartConfigWatcher(ctx context.Context) error {
	callback := func(config *AuthorizationConfig) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.config = config
		klog.Infof("Authorization config reloaded from %s", a.configSource.String())
	}

	return a.configSource.Watch(ctx, callback)
}
