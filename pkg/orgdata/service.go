package orgdata

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// OrgDataService provides thread-safe access to organizational data with hot reload capability
type OrgDataService struct {
	// Thread-safe access to current data
	mu          sync.RWMutex
	currentData *OrgDataSet
	version     DataVersion

	// Indexes for fast lookups
	uidToTeams    map[string][]string
	slackToTeams  map[string][]string
	uidToOrgs     map[string][]string
	slackToOrgs   map[string][]string
	teamToMembers map[string][]Employee
	orgToTeams    map[string][]*OrgUnit
	nameToOrgUnit map[string]*OrgUnit

	// Hot reload configuration
	reloadInterval time.Duration
}

// OrgDataSet represents a complete set of organizational data
type OrgDataSet struct {
	Orgs         []*OrgUnit
	AllTeams     []*OrgUnit
	AllEmployees []Employee
	LoadedAt     time.Time
}

// NewOrgDataService creates a new service instance
func NewOrgDataService() *OrgDataService {
	return &OrgDataService{
		reloadInterval: 30 * time.Second, // Check for updates every 30 seconds
		uidToTeams:     make(map[string][]string),
		slackToTeams:   make(map[string][]string),
		uidToOrgs:      make(map[string][]string),
		slackToOrgs:    make(map[string][]string),
		teamToMembers:  make(map[string][]Employee),
		orgToTeams:     make(map[string][]*OrgUnit),
		nameToOrgUnit:  make(map[string]*OrgUnit),
	}
}

// LoadFromFiles loads organizational data from JSON files (ConfigMaps in the real implementation)
func (s *OrgDataService) LoadFromFiles(filePaths []string) error {
	var allOrgs []*OrgUnit

	for _, filePath := range filePaths {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", filePath, err)
		}

		var org OrgUnit
		if err := json.Unmarshal(data, &org); err != nil {
			return fmt.Errorf("failed to parse JSON from %s: %w", filePath, err)
		}

		allOrgs = append(allOrgs, &org)
	}

	return s.loadData(allOrgs)
}

// loadData processes the org data and builds indexes
func (s *OrgDataService) loadData(orgs []*OrgUnit) error {
	// Build new data set
	dataSet := &OrgDataSet{
		Orgs:     orgs,
		LoadedAt: time.Now(),
	}

	// Flatten the hierarchy and compute derived data
	s.flattenHierarchy(dataSet)

	// Build new indexes
	newIndexes := s.buildIndexes(dataSet)

	// Atomically swap the data (this is the "hot reload" mechanism)
	s.mu.Lock()
	defer s.mu.Unlock()

	s.currentData = dataSet
	s.uidToTeams = newIndexes.uidToTeams
	s.slackToTeams = newIndexes.slackToTeams
	s.uidToOrgs = newIndexes.uidToOrgs
	s.slackToOrgs = newIndexes.slackToOrgs
	s.teamToMembers = newIndexes.teamToMembers
	s.orgToTeams = newIndexes.orgToTeams
	s.nameToOrgUnit = newIndexes.nameToOrgUnit

	s.version = DataVersion{
		LoadTime:      time.Now(),
		OrgCount:      len(dataSet.Orgs),
		EmployeeCount: len(dataSet.AllEmployees),
	}

	log.Printf("Loaded organizational data: %d orgs, %d teams, %d employees",
		len(dataSet.Orgs), len(dataSet.AllTeams), len(dataSet.AllEmployees))

	return nil
}

// indexSet holds all the indexes for atomic swapping
type indexSet struct {
	uidToTeams    map[string][]string
	slackToTeams  map[string][]string
	uidToOrgs     map[string][]string
	slackToOrgs   map[string][]string
	teamToMembers map[string][]Employee
	orgToTeams    map[string][]*OrgUnit
	nameToOrgUnit map[string]*OrgUnit
}

// flattenHierarchy walks the org tree and flattens teams/employees
func (s *OrgDataService) flattenHierarchy(dataSet *OrgDataSet) {
	var allTeams []*OrgUnit
	var allEmployees []Employee
	employeeSet := make(map[string]Employee) // Dedup by UID

	for _, org := range dataSet.Orgs {
		s.walkOrgTree(org, []string{org.Name}, &allTeams, employeeSet)
	}

	// Convert employee set to slice
	for _, emp := range employeeSet {
		allEmployees = append(allEmployees, emp)
	}

	dataSet.AllTeams = allTeams
	dataSet.AllEmployees = allEmployees
}

// walkOrgTree recursively walks the org tree
func (s *OrgDataService) walkOrgTree(unit *OrgUnit, orgPath []string, teams *[]*OrgUnit, employees map[string]Employee) {
	// Set the org path for this unit
	unit.OrgPath = make([]string, len(orgPath))
	copy(unit.OrgPath, orgPath)

	// Collect employees from this unit
	var unitEmployees []Employee
	for _, emp := range unit.Group.ResolvedPeople {
		employees[emp.UID] = emp
		unitEmployees = append(unitEmployees, emp)
	}

	// If this is a team (leaf node or has specific team type), add to teams list
	if s.isTeam(unit) {
		*teams = append(*teams, unit)
	}

	// Recursively process children
	for _, child := range unit.Children {
		childOrgPath := append(orgPath, child.Name)
		s.walkOrgTree(child, childOrgPath, teams, employees)

		// Bubble up employees from children for easier access
		unitEmployees = append(unitEmployees, child.AllEmployees...)
	}

	unit.AllEmployees = unitEmployees
}

// isTeam determines if an org unit should be considered a "team"
func (s *OrgDataService) isTeam(unit *OrgUnit) bool {
	return unit.Group.Type.Name == "team" || len(unit.Children) == 0
}

// buildIndexes creates all the lookup indexes
func (s *OrgDataService) buildIndexes(dataSet *OrgDataSet) *indexSet {
	indexes := &indexSet{
		uidToTeams:    make(map[string][]string),
		slackToTeams:  make(map[string][]string),
		uidToOrgs:     make(map[string][]string),
		slackToOrgs:   make(map[string][]string),
		teamToMembers: make(map[string][]Employee),
		orgToTeams:    make(map[string][]*OrgUnit),
		nameToOrgUnit: make(map[string]*OrgUnit),
	}

	// Build team and org lookups
	for _, team := range dataSet.AllTeams {
		indexes.nameToOrgUnit[team.Name] = team
		indexes.teamToMembers[team.Name] = team.AllEmployees

		// Map each employee to this team and parent orgs
		for _, emp := range team.AllEmployees {
			// Add to team mappings
			indexes.uidToTeams[emp.UID] = appendUnique(indexes.uidToTeams[emp.UID], team.Name)
			if emp.SlackUID != "" {
				indexes.slackToTeams[emp.SlackUID] = appendUnique(indexes.slackToTeams[emp.SlackUID], team.Name)
			}

			// Add to org mappings (include all orgs in the path)
			for _, orgName := range team.OrgPath {
				indexes.uidToOrgs[emp.UID] = appendUnique(indexes.uidToOrgs[emp.UID], orgName)
				if emp.SlackUID != "" {
					indexes.slackToOrgs[emp.SlackUID] = appendUnique(indexes.slackToOrgs[emp.SlackUID], orgName)
				}
			}
		}
	}

	// Build org name to unit mappings and org to teams mappings
	for _, org := range dataSet.Orgs {
		s.buildOrgIndex(org, indexes)
	}

	// Process employees who are directly in orgs (not just teams)
	for _, org := range dataSet.Orgs {
		s.processOrgEmployees(org, indexes)
	}

	return indexes
}

// buildOrgIndex recursively builds org-related indexes
func (s *OrgDataService) buildOrgIndex(unit *OrgUnit, indexes *indexSet) {
	indexes.nameToOrgUnit[unit.Name] = unit

	// If this unit has teams under it, map them
	for _, team := range indexes.nameToOrgUnit {
		for _, orgName := range team.OrgPath {
			if orgName == unit.Name {
				indexes.orgToTeams[unit.Name] = append(indexes.orgToTeams[unit.Name], team)
				break
			}
		}
	}

	for _, child := range unit.Children {
		s.buildOrgIndex(child, indexes)
	}
}

// processOrgEmployees recursively processes employees who are directly in orgs
func (s *OrgDataService) processOrgEmployees(unit *OrgUnit, indexes *indexSet) {
	// Process employees directly in this org
	for _, emp := range unit.Group.ResolvedPeople {
		// Add to org mappings (include this org and all parent orgs in the path)
		for _, orgName := range unit.OrgPath {
			indexes.uidToOrgs[emp.UID] = appendUnique(indexes.uidToOrgs[emp.UID], orgName)
			if emp.SlackUID != "" {
				indexes.slackToOrgs[emp.SlackUID] = appendUnique(indexes.slackToOrgs[emp.SlackUID], orgName)
			}
		}
	}

	// Recursively process children
	for _, child := range unit.Children {
		s.processOrgEmployees(child, indexes)
	}
}

// appendUnique adds a string to a slice if it's not already present
func appendUnique(slice []string, item string) []string {
	for _, existing := range slice {
		if existing == item {
			return slice
		}
	}
	return append(slice, item)
}

// Query interface implementations

// GetTeamsForUID returns all teams that contain the specified UID
func (s *OrgDataService) GetTeamsForUID(uid string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	teams := s.uidToTeams[uid]
	result := make([]string, len(teams))
	copy(result, teams)
	return result
}

// GetTeamsForSlackID returns all teams that contain the specified Slack ID
func (s *OrgDataService) GetTeamsForSlackID(slackID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	teams := s.slackToTeams[slackID]
	result := make([]string, len(teams))
	copy(result, teams)
	return result
}

// IsEmployeeInOrg checks if an employee (by UID) is part of the specified org (including children)
func (s *OrgDataService) IsEmployeeInOrg(uid string, orgName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	orgs := s.uidToOrgs[uid]
	for _, org := range orgs {
		if org == orgName {
			return true
		}
	}
	return false
}

// IsSlackUserInOrg checks if a Slack user is part of the specified org (including children)
func (s *OrgDataService) IsSlackUserInOrg(slackID string, orgName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	orgs := s.slackToOrgs[slackID]
	for _, org := range orgs {
		if org == orgName {
			return true
		}
	}
	return false
}

// GetTeamMembers returns all employees in the specified team
func (s *OrgDataService) GetTeamMembers(teamName string) []Employee {
	s.mu.RLock()
	defer s.mu.RUnlock()

	members := s.teamToMembers[teamName]
	result := make([]Employee, len(members))
	copy(result, members)
	return result
}

// Query provides an extensible query interface for future complex queries
func (s *OrgDataService) Query(conditions []QueryCondition) (*QueryResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// This is a placeholder for the extensible query system
	// Implementation would depend on specific query requirements
	return &QueryResult{}, fmt.Errorf("complex queries not yet implemented")
}

// QueryEmployees filters employees based on conditions
func (s *OrgDataService) QueryEmployees(conditions []QueryCondition) ([]Employee, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Employee
	for _, emp := range s.currentData.AllEmployees {
		if s.matchesConditions(emp, conditions) {
			result = append(result, emp)
		}
	}
	return result, nil
}

// QueryTeams filters teams based on conditions
func (s *OrgDataService) QueryTeams(conditions []QueryCondition) ([]*OrgUnit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*OrgUnit
	for _, team := range s.currentData.AllTeams {
		if s.matchesTeamConditions(team, conditions) {
			result = append(result, team)
		}
	}
	return result, nil
}

// matchesConditions checks if an employee matches the query conditions
func (s *OrgDataService) matchesConditions(emp Employee, conditions []QueryCondition) bool {
	for _, cond := range conditions {
		if !s.matchesEmployeeCondition(emp, cond) {
			return false
		}
	}
	return true
}

// matchesEmployeeCondition checks a single condition against an employee
func (s *OrgDataService) matchesEmployeeCondition(emp Employee, cond QueryCondition) bool {
	var fieldValue string
	switch cond.Field {
	case "uid":
		fieldValue = emp.UID
	case "slack_uid":
		fieldValue = emp.SlackUID
	case "email":
		fieldValue = emp.Email
	case "display_name":
		fieldValue = emp.DisplayName
	case "job_title":
		fieldValue = emp.JobTitle
	default:
		return false
	}

	switch cond.Operator {
	case "eq", "":
		return fieldValue == fmt.Sprintf("%v", cond.Value)
	case "contains":
		return strings.Contains(strings.ToLower(fieldValue), strings.ToLower(fmt.Sprintf("%v", cond.Value)))
	default:
		return false
	}
}

// matchesTeamConditions checks if a team matches the query conditions
func (s *OrgDataService) matchesTeamConditions(team *OrgUnit, conditions []QueryCondition) bool {
	for _, cond := range conditions {
		if !s.matchesTeamCondition(team, cond) {
			return false
		}
	}
	return true
}

// matchesTeamCondition checks a single condition against a team
func (s *OrgDataService) matchesTeamCondition(team *OrgUnit, cond QueryCondition) bool {
	switch cond.Field {
	case "name":
		return team.Name == fmt.Sprintf("%v", cond.Value)
	case "org_path":
		for _, org := range team.OrgPath {
			if org == fmt.Sprintf("%v", cond.Value) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// StartConfigMapWatcher starts watching for ConfigMap changes (placeholder for Kubernetes integration)
func (s *OrgDataService) StartConfigMapWatcher(ctx context.Context, configMapPaths []string) {
	ticker := time.NewTicker(s.reloadInterval)
	defer ticker.Stop()

	log.Println("Starting ConfigMap watcher...")

	for {
		select {
		case <-ctx.Done():
			log.Println("ConfigMap watcher stopped")
			return
		case <-ticker.C:
			// In a real implementation, this would:
			// 1. Watch Kubernetes ConfigMap resources
			// 2. Detect changes via resourceVersion or checksums
			// 3. Reload data in background
			// 4. Atomically swap when ready

			// For now, just reload from files periodically
			if err := s.LoadFromFiles(configMapPaths); err != nil {
				log.Printf("Failed to reload orgdata: %v", err)
			}
		}
	}
}

// IsSlackUserUID checks if a Slack user (by SlackUID) corresponds to a specific Employee.UID
// This is used for allowed_uids authorization: given a Slack ID from a message,
// check if that Slack user has the specified Employee.UID in the org data
func (s *OrgDataService) IsSlackUserUID(slackID string, uid string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Check all employees to find matching Slack ID and UID
	if s.currentData == nil {
		return false
	}

	for _, employee := range s.currentData.AllEmployees {
		if employee.SlackUID == slackID && employee.UID == uid {
			return true
		}
	}
	return false
}

// IsSlackUserInTeam checks if a Slack user is part of the specified team
func (s *OrgDataService) IsSlackUserInTeam(slackID string, teamName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	teams := s.slackToTeams[slackID]
	for _, team := range teams {
		if team == teamName {
			return true
		}
	}
	return false
}

// GetEmployeeBySlackID returns the Employee record for a given Slack ID
func (s *OrgDataService) GetEmployeeBySlackID(slackID string) *Employee {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.currentData == nil {
		return nil
	}

	for _, employee := range s.currentData.AllEmployees {
		if employee.SlackUID == slackID {
			return &employee
		}
	}
	return nil
}

// GetVersion returns the current data version info
func (s *OrgDataService) GetVersion() DataVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}
