package orgdatacore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Service implements the core organizational data service
type Service struct {
	mu      sync.RWMutex
	data    *Data
	version DataVersion
}

// NewService creates a new organizational data service
func NewService() *Service {
	return &Service{}
}

// LoadFromFiles loads organizational data from JSON files
// Deprecated: Use LoadFromDataSource with FileDataSource for better architecture
func (s *Service) LoadFromFiles(filePaths []string) error {
	// Use FileDataSource to maintain consistency with DataSource pattern
	fileSource := NewFileDataSource(filePaths...)
	return s.LoadFromDataSource(context.Background(), fileSource)
}

// LoadFromDataSource loads organizational data from a data source
func (s *Service) LoadFromDataSource(ctx context.Context, source DataSource) error {
	reader, err := source.Load(ctx)
	if err != nil {
		return fmt.Errorf("failed to load from data source %s: %w", source.String(), err)
	}
	defer reader.Close()

	// Read all data
	data, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("failed to read data from source %s: %w", source.String(), err)
	}

	// Parse JSON
	var orgData Data
	if err := json.Unmarshal(data, &orgData); err != nil {
		return fmt.Errorf("failed to parse JSON from source %s: %w", source.String(), err)
	}

	// Update service data
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = &orgData
	s.version = DataVersion{
		LoadTime:      time.Now(),
		OrgCount:      len(orgData.Lookups.Orgs),
		EmployeeCount: len(orgData.Lookups.Employees),
	}

	return nil
}

// StartDataSourceWatcher starts watching a data source for changes
func (s *Service) StartDataSourceWatcher(ctx context.Context, source DataSource) error {
	callback := func() error {
		return s.LoadFromDataSource(ctx, source)
	}

	return source.Watch(ctx, callback)
}

// GetVersion returns the current data version
func (s *Service) GetVersion() DataVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// GetEmployeeByUID returns an employee by UID
func (s *Service) GetEmployeeByUID(uid string) *Employee {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Employees == nil {
		return nil
	}

	if emp, exists := s.data.Lookups.Employees[uid]; exists {
		return &emp
	}
	return nil
}

// GetEmployeeBySlackID returns an employee by Slack ID
func (s *Service) GetEmployeeBySlackID(slackID string) *Employee {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Indexes.SlackIDMappings.SlackUIDToUID == nil {
		return nil
	}

	uid := s.data.Indexes.SlackIDMappings.SlackUIDToUID[slackID]
	if uid == "" {
		return nil
	}

	return s.GetEmployeeByUID(uid)
}

// GetTeamByName returns a team by name
func (s *Service) GetTeamByName(teamName string) *Team {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Teams == nil {
		return nil
	}

	// Find team by name (linear search for now)
	for _, team := range s.data.Lookups.Teams {
		if team.Name == teamName {
			return &team
		}
	}
	return nil
}

// GetOrgByName returns an organization by name
func (s *Service) GetOrgByName(orgName string) *Org {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Orgs == nil {
		return nil
	}

	// Get org directly from orgs lookup
	org, exists := s.data.Lookups.Orgs[orgName]
	if !exists {
		return nil
	}
	return &org
}

// GetTeamsForUID returns all teams a UID is a member of
func (s *Service) GetTeamsForUID(uid string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Indexes.Membership.MembershipIndex == nil {
		return []string{}
	}

	memberships := s.data.Indexes.Membership.MembershipIndex[uid]
	var teams []string
	for _, membership := range memberships {
		if membership.Type == "team" {
			teams = append(teams, membership.Name)
		}
	}
	return teams
}

// GetTeamsForSlackID returns all teams a Slack user is a member of
func (s *Service) GetTeamsForSlackID(slackID string) []string {
	uid := s.getUIDFromSlackID(slackID)
	if uid == "" {
		return []string{}
	}
	return s.GetTeamsForUID(uid)
}

// GetTeamMembers returns all members of a team
func (s *Service) GetTeamMembers(teamName string) []Employee {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Teams == nil {
		return []Employee{}
	}

	// Get team directly from teams lookup
	team, exists := s.data.Lookups.Teams[teamName]
	if !exists {
		return []Employee{}
	}

	// Get employee objects for each UID
	var members []Employee
	for _, uid := range team.Group.ResolvedPeopleUIDList {
		if emp := s.GetEmployeeByUID(uid); emp != nil {
			members = append(members, *emp)
		}
	}

	return members
}

// IsEmployeeInTeam checks if an employee is in a specific team
func (s *Service) IsEmployeeInTeam(uid string, teamName string) bool {
	teams := s.GetTeamsForUID(uid)
	for _, team := range teams {
		if team == teamName {
			return true
		}
	}
	return false
}

// IsSlackUserInTeam checks if a Slack user is in a specific team
func (s *Service) IsSlackUserInTeam(slackID string, teamName string) bool {
	uid := s.getUIDFromSlackID(slackID)
	if uid == "" {
		return false
	}
	return s.IsEmployeeInTeam(uid, teamName)
}

// IsEmployeeInOrg checks if an employee is in a specific organization
func (s *Service) IsEmployeeInOrg(uid string, orgName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Indexes.Membership.MembershipIndex == nil {
		return false
	}

	memberships := s.data.Indexes.Membership.MembershipIndex[uid]

	// Get relationship index once
	relationshipIndex := s.data.Indexes.Membership.RelationshipIndex
	teamsIndex := relationshipIndex["teams"]

	for _, membership := range memberships {
		if membership.Type == "org" && membership.Name == orgName {
			return true
		} else if membership.Type == "team" {
			// Check if team belongs to the specified org through relationship index
			if teamRelationships, exists := teamsIndex[membership.Name]; exists {
				for _, org := range teamRelationships.Ancestry.Orgs {
					if org == orgName {
						return true
					}
				}
			}
		}
	}
	return false
}

// IsSlackUserInOrg checks if a Slack user is in a specific organization
func (s *Service) IsSlackUserInOrg(slackID string, orgName string) bool {
	uid := s.getUIDFromSlackID(slackID)
	if uid == "" {
		return false
	}
	return s.IsEmployeeInOrg(uid, orgName)
}

// GetUserOrganizations returns the complete organizational hierarchy a Slack user belongs to
func (s *Service) GetUserOrganizations(slackUserID string) []OrgInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Indexes.Membership.MembershipIndex == nil {
		return []OrgInfo{}
	}

	uid := s.getUIDFromSlackID(slackUserID)
	if uid == "" {
		return []OrgInfo{}
	}

	memberships := s.data.Indexes.Membership.MembershipIndex[uid]
	var orgs []OrgInfo
	seenItems := make(map[string]bool) // Track seen items to avoid duplicates

	// Get relationship index once
	relationshipIndex := s.data.Indexes.Membership.RelationshipIndex
	teamsIndex := relationshipIndex["teams"]

	for _, membership := range memberships {
		if membership.Type == "org" {
			// Direct organization membership
			if !seenItems[membership.Name] {
				orgs = append(orgs, OrgInfo{
					Name: membership.Name,
					Type: "Organization",
				})
				seenItems[membership.Name] = true
			}
		} else if membership.Type == "team" {
			// Add the team membership itself
			if !seenItems[membership.Name] {
				orgs = append(orgs, OrgInfo{
					Name: membership.Name,
					Type: "Team",
				})
				seenItems[membership.Name] = true
			}

			// Get team's hierarchy directly from relationship index
			if teamRelationships, exists := teamsIndex[membership.Name]; exists {
				// Add all ancestry items
				addAncestryItems(&orgs, &seenItems, teamRelationships.Ancestry)
			}
		}
	}

	return orgs
}

// addAncestryItems adds all ancestry items to the orgs slice, avoiding duplicates
func addAncestryItems(orgs *[]OrgInfo, seenItems *map[string]bool, ancestry struct {
	Orgs       []string `json:"orgs"`
	Teams      []string `json:"teams"`
	Pillars    []string `json:"pillars"`
	TeamGroups []string `json:"team_groups"`
}) {
	// Add organizations
	for _, orgName := range ancestry.Orgs {
		if !(*seenItems)[orgName] {
			*orgs = append(*orgs, OrgInfo{
				Name: orgName,
				Type: "Organization",
			})
			(*seenItems)[orgName] = true
		}
	}

	// Add pillars
	for _, pillarName := range ancestry.Pillars {
		if !(*seenItems)[pillarName] {
			*orgs = append(*orgs, OrgInfo{
				Name: pillarName,
				Type: "Pillar",
			})
			(*seenItems)[pillarName] = true
		}
	}

	// Add team groups
	for _, teamGroupName := range ancestry.TeamGroups {
		if !(*seenItems)[teamGroupName] {
			*orgs = append(*orgs, OrgInfo{
				Name: teamGroupName,
				Type: "Team Group",
			})
			(*seenItems)[teamGroupName] = true
		}
	}

	// Add parent teams
	for _, parentTeamName := range ancestry.Teams {
		if !(*seenItems)[parentTeamName] {
			*orgs = append(*orgs, OrgInfo{
				Name: parentTeamName,
				Type: "Parent Team",
			})
			(*seenItems)[parentTeamName] = true
		}
	}
}

// getUIDFromSlackID returns the UID for a given Slack ID
func (s *Service) getUIDFromSlackID(slackID string) string {
	if s.data == nil || s.data.Indexes.SlackIDMappings.SlackUIDToUID == nil {
		return ""
	}
	return s.data.Indexes.SlackIDMappings.SlackUIDToUID[slackID]
}
