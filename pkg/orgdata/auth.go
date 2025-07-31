package orgdata

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v2"
)

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

// AuthorizationService handles authorization checking
type AuthorizationService struct {
	config         *AuthorizationConfig
	orgDataService *OrgDataService
	configPath     string
	mu             sync.RWMutex
	reloadInterval time.Duration
}

// NewAuthorizationService creates a new authorization service
func NewAuthorizationService(orgDataService *OrgDataService, configPath string) *AuthorizationService {
	return &AuthorizationService{
		orgDataService: orgDataService,
		configPath:     configPath,
		reloadInterval: 60 * time.Second, // Check for config updates every minute
	}
}

// LoadConfig loads the authorization configuration from file
func (a *AuthorizationService) LoadConfig() error {
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		return fmt.Errorf("failed to read authorization config: %w", err)
	}

	var config AuthorizationConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse authorization config: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.config = &config

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

	// Check if user is in the allowed UIDs (highest specificity)
	for _, allowedUID := range matchingRule.AllowedUIDs {
		if a.orgDataService.IsSlackUserUID(slackUserID, allowedUID) {
			return true, ""
		}
	}

	// Check if user is in any of the allowed teams
	for _, allowedTeam := range matchingRule.AllowedTeams {
		if a.orgDataService.IsSlackUserInTeam(slackUserID, allowedTeam) {
			return true, ""
		}
	}

	// Check if user is in any of the allowed orgs (lowest specificity)
	for _, allowedOrg := range matchingRule.AllowedOrgs {
		if a.orgDataService.IsSlackUserInOrg(slackUserID, allowedOrg) {
			return true, ""
		}
	}

	// User not authorized
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

// GetUserOrganizations returns all organizations a Slack user belongs to
func (a *AuthorizationService) GetUserOrganizations(slackUserID string) []string {
	teams := a.orgDataService.GetTeamsForSlackID(slackUserID)
	orgSet := make(map[string]bool)

	// Get all orgs from teams
	for _, teamName := range teams {
		a.orgDataService.mu.RLock()
		if team, exists := a.orgDataService.nameToOrgUnit[teamName]; exists {
			for _, orgName := range team.OrgPath {
				orgSet[orgName] = true
			}
		}
		a.orgDataService.mu.RUnlock()
	}

	// Convert set to slice
	var orgs []string
	for orgName := range orgSet {
		orgs = append(orgs, orgName)
	}

	return orgs
}

// GetUserTeams returns all teams a Slack user belongs to
func (a *AuthorizationService) GetUserTeams(slackUserID string) []string {
	return a.orgDataService.GetTeamsForSlackID(slackUserID)
}

// UserInfo represents comprehensive user information for display
type UserInfo struct {
	Employee      *Employee
	Organizations []string
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
	orgs := a.GetUserOrganizations(slackUserID)
	teams := a.orgDataService.GetTeamsForSlackID(slackUserID)

	return &UserInfo{
		Employee:      employee,
		Organizations: orgs,
		Teams:         teams,
		SlackID:       slackUserID,
		HasOrgData:    employee != nil,
	}
}

// StartConfigWatcher starts watching for configuration file changes
func (a *AuthorizationService) StartConfigWatcher() {
	ticker := time.NewTicker(a.reloadInterval)
	defer ticker.Stop()

	for range ticker.C {
		if err := a.LoadConfig(); err != nil {
			fmt.Printf("Failed to reload authorization config: %v\n", err)
		}
	}
}
