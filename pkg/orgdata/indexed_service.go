package orgdata

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// IndexedOrgDataService provides thread-safe access to indexed organizational data with hot reload capability
type IndexedOrgDataService struct {
	// Thread-safe access to current data
	mu      sync.RWMutex
	data    *Data
	version DataVersion

	// Hot reload configuration
	reloadInterval time.Duration
}

// NewIndexedOrgDataService creates a new indexed service instance
func NewIndexedOrgDataService() *IndexedOrgDataService {
	return &IndexedOrgDataService{
		reloadInterval: 30 * time.Second, // Check for updates every 30 seconds
	}
}

// LoadFromFiles loads indexed organizational data from JSON files
func (s *IndexedOrgDataService) LoadFromFiles(filePaths []string) error {
	var allData []*Data

	for _, filePath := range filePaths {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", filePath, err)
		}

		var indexedData Data
		if err := json.Unmarshal(data, &indexedData); err != nil {
			return fmt.Errorf("failed to parse indexed JSON from %s: %w", filePath, err)
		}

		allData = append(allData, &indexedData)
	}

	// For now, we'll use the first file (can be extended to merge multiple files later)
	if len(allData) == 0 {
		return fmt.Errorf("no valid indexed data files found")
	}

	return s.loadData(allData[0])
}

// loadData stores the indexed data directly
func (s *IndexedOrgDataService) loadData(indexedData *Data) error {
	// Atomically swap the data
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = indexedData

	// Update version
	s.version = DataVersion{
		LoadTime:      time.Now(),
		ConfigMaps:    map[string]string{"indexed_data": indexedData.Metadata.DataVersion},
		OrgCount:      indexedData.Metadata.TotalOrgs,
		EmployeeCount: indexedData.Metadata.TotalEmployees,
	}

	log.Printf("Loaded indexed organizational data: %d orgs, %d employees",
		indexedData.Metadata.TotalOrgs, indexedData.Metadata.TotalEmployees)

	return nil
}

// GetTeamsForUID returns all teams a UID is a member of
func (s *IndexedOrgDataService) GetTeamsForUID(uid string) []string {
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

// GetTeamsForSlackID returns all teams a Slack ID is a member of
func (s *IndexedOrgDataService) GetTeamsForSlackID(slackID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Indexes.SlackIDMappings.SlackUIDToUID == nil {
		return []string{}
	}

	// Multi-step lookup: Slack ID -> UID -> Teams
	uid := s.data.Indexes.SlackIDMappings.SlackUIDToUID[slackID]
	if uid == "" {
		return []string{}
	}

	return s.GetTeamsForUID(uid)
}

// IsEmployeeInOrg checks if an employee is in a specific organization
func (s *IndexedOrgDataService) IsEmployeeInOrg(uid string, orgName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Indexes.Membership.MembershipIndex == nil {
		return false
	}

	// Check direct membership
	memberships := s.data.Indexes.Membership.MembershipIndex[uid]
	for _, membership := range memberships {
		if (membership.Type == "org" || membership.Type == "pillar" || membership.Type == "team_group") &&
			membership.Name == orgName {
			return true
		}
	}

	// Check if employee is in any team that belongs to this org
	teams := s.GetTeamsForUID(uid)
	for _, teamName := range teams {
		if s.isTeamInOrg(teamName, orgName) {
			return true
		}
	}

	return false
}

// isTeamInOrg checks if a team belongs to a specific organization
func (s *IndexedOrgDataService) isTeamInOrg(teamName, orgName string) bool {
	if s.data == nil || s.data.Indexes.Membership.RelationshipIndex == nil {
		return false
	}

	// Check if team is directly in this org
	if teamRelationships, exists := s.data.Indexes.Membership.RelationshipIndex["teams"]; exists {
		if teamData, exists := teamRelationships[teamName]; exists {
			ancestry := teamData.Ancestry
			for _, org := range ancestry.Orgs {
				if org == orgName {
					return true
				}
			}
		}
	}

	return false
}

// IsSlackUserInOrg checks if a Slack user is in a specific organization
func (s *IndexedOrgDataService) IsSlackUserInOrg(slackID string, orgName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Multi-step lookup: Slack ID -> UID -> Check org membership
	uid := s.getUIDFromSlackID(slackID)
	if uid == "" {
		return false
	}

	return s.IsEmployeeInOrg(uid, orgName)
}

// getUIDFromSlackID returns the UID for a given Slack ID
func (s *IndexedOrgDataService) getUIDFromSlackID(slackID string) string {
	if s.data == nil || s.data.Indexes.SlackIDMappings.SlackUIDToUID == nil {
		return ""
	}
	return s.data.Indexes.SlackIDMappings.SlackUIDToUID[slackID]
}

// GetTeamMembers returns all members of a team
func (s *IndexedOrgDataService) GetTeamMembers(teamName string) []Employee {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Teams == nil {
		return []Employee{}
	}

	team, exists := s.data.Lookups.Teams[teamName]
	if !exists {
		return []Employee{}
	}

	var members []Employee
	for _, memberUID := range team.Group.ResolvedPeopleUIDList {
		if employee, exists := s.data.Lookups.Employees[memberUID]; exists {
			legacyEmployee := Employee{
				UID:      employee.UID,
				SlackUID: employee.SlackUID,
				FullName: employee.FullName,
				Email:    employee.Email,
				JobTitle: employee.JobTitle,
			}
			members = append(members, legacyEmployee)
		}
	}
	return members
}

// IsSlackUserUID checks if a Slack user ID matches a specific UID
func (s *IndexedOrgDataService) IsSlackUserUID(slackID string, uid string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Employees == nil {
		return false
	}

	if employee, exists := s.data.Lookups.Employees[uid]; exists {
		return employee.SlackUID == slackID
	}
	return false
}

// IsSlackUserInTeam checks if a Slack user is in a specific team
func (s *IndexedOrgDataService) IsSlackUserInTeam(slackID string, teamName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Multi-step lookup: Slack ID -> UID -> Check team membership
	uid := s.getUIDFromSlackID(slackID)
	if uid == "" {
		return false
	}

	teams := s.GetTeamsForUID(uid)
	for _, team := range teams {
		if team == teamName {
			return true
		}
	}
	return false
}

// GetEmployeeBySlackID returns an employee by Slack ID
func (s *IndexedOrgDataService) GetEmployeeBySlackID(slackID string) *Employee {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Employees == nil {
		return nil
	}

	// Use the Slack ID mapping for O(1) lookup
	uid := s.getUIDFromSlackID(slackID)
	if uid == "" {
		return nil
	}

	if employee, exists := s.data.Lookups.Employees[uid]; exists {
		return &Employee{
			UID:      employee.UID,
			SlackUID: employee.SlackUID,
			FullName: employee.FullName,
			Email:    employee.Email,
			JobTitle: employee.JobTitle,
		}
	}
	return nil
}

// GetVersion returns the current data version
func (s *IndexedOrgDataService) GetVersion() DataVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// StartConfigMapWatcher starts watching for config map changes (placeholder for compatibility)
func (s *IndexedOrgDataService) StartConfigMapWatcher(ctx context.Context, configMapPaths []string) {
	// For indexed data, we can implement file watching if needed
	// For now, this is a placeholder to maintain compatibility
	go func() {
		ticker := time.NewTicker(s.reloadInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Check if files have changed and reload if necessary
				// This could be implemented with file modification time checking
			}
		}
	}()
}

// GetNameToOrgUnit returns the name to org unit mapping
func (s *IndexedOrgDataService) GetNameToOrgUnit() map[string]*OrgUnit {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil {
		return make(map[string]*OrgUnit)
	}

	result := make(map[string]*OrgUnit)

	// Add teams
	for teamName, team := range s.data.Lookups.Teams {
		result[teamName] = &OrgUnit{
			Name: teamName,
			Group: struct {
				Type struct {
					Name string `json:"name"`
				} `json:"type"`
			}{
				Type: struct {
					Name string `json:"name"`
				}{
					Name: team.Type,
				},
			},
		}
	}

	// Add orgs
	for orgName, org := range s.data.Lookups.Orgs {
		result[orgName] = &OrgUnit{
			Name: orgName,
			Group: struct {
				Type struct {
					Name string `json:"name"`
				} `json:"type"`
			}{
				Type: struct {
					Name string `json:"name"`
				}{
					Name: org.Type,
				},
			},
		}
	}

	return result
}

// GetOrgPath returns the org path for a team (placeholder for compatibility)
func (s *IndexedOrgDataService) GetOrgPath(teamName string) []string {
	// For indexed data, we can compute this from the relationship index
	// For now, return empty slice to maintain compatibility
	return []string{}
}

// GetUserOrganizations returns all organizations a Slack user belongs to with their types
func (s *IndexedOrgDataService) GetUserOrganizations(slackUserID string) []OrgInfo {
	teams := s.GetTeamsForSlackID(slackUserID)
	orgMap := make(map[string]OrgInfo) // Use map to deduplicate

	// Get all orgs from teams using the indexed data
	for _, teamName := range teams {
		// Add the team itself first
		orgMap[teamName] = OrgInfo{
			Name: teamName,
			Type: "Team",
		}

		// Add parent organizations from the relationship index
		if s.data != nil && s.data.Indexes.Membership.RelationshipIndex != nil {
			if teamRelationships, exists := s.data.Indexes.Membership.RelationshipIndex["teams"]; exists {
				if teamData, exists := teamRelationships[teamName]; exists {
					ancestry := teamData.Ancestry
					// Add orgs from ancestry
					for _, orgName := range ancestry.Orgs {
						orgMap[orgName] = OrgInfo{
							Name: orgName,
							Type: "Organization",
						}
					}
					// Add pillars from ancestry
					for _, pillarName := range ancestry.Pillars {
						orgMap[pillarName] = OrgInfo{
							Name: pillarName,
							Type: "Pillar",
						}
					}
					// Add team groups from ancestry
					for _, teamGroupName := range ancestry.TeamGroups {
						orgMap[teamGroupName] = OrgInfo{
							Name: teamGroupName,
							Type: "Team Group",
						}
					}
				}
			}
		}
	}

	// Convert map to slice
	var orgs []OrgInfo
	for _, orgInfo := range orgMap {
		orgs = append(orgs, orgInfo)
	}

	return orgs
}
