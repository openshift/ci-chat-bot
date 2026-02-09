package orgdatacore

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type Service struct {
	mu             sync.RWMutex
	data           *Data
	version        DataVersion
	logger         *slog.Logger
	watcherRunning bool
	watcherCancel  context.CancelFunc
}

func NewService(opts ...ServiceOption) *Service {
	cfg := defaultServiceConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	return &Service{logger: cfg.logger}
}

func (s *Service) LoadFromDataSource(ctx context.Context, source DataSource) error {
	reader, err := source.Load(ctx)
	if err != nil {
		return NewLoadError(source.String(), err)
	}
	defer func() {
		if closeErr := reader.Close(); closeErr != nil {
			s.logger.Warn("failed to close reader", "source", source.String(), "error", closeErr)
		}
	}()

	var orgData Data
	if err := json.NewDecoder(reader).Decode(&orgData); err != nil {
		return NewLoadError(source.String(), fmt.Errorf("failed to parse JSON: %w", err))
	}

	if err := validateData(&orgData); err != nil {
		return NewLoadError(source.String(), err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = &orgData
	s.version = DataVersion{
		LoadTime:      time.Now(),
		OrgCount:      len(orgData.Lookups.Orgs),
		EmployeeCount: len(orgData.Lookups.Employees),
	}

	s.logger.Info("data loaded", "source", source.String(), "employees", s.version.EmployeeCount, "orgs", s.version.OrgCount)
	return nil
}

func (s *Service) StartDataSourceWatcher(ctx context.Context, source DataSource) error {
	s.mu.Lock()
	if s.watcherRunning {
		s.mu.Unlock()
		return ErrWatcherAlreadyRunning
	}
	s.watcherRunning = true

	// Create a cancellable context so StopWatcher can terminate the watcher
	watchCtx, cancel := context.WithCancel(ctx)
	s.watcherCancel = cancel
	s.mu.Unlock()

	if err := s.LoadFromDataSource(watchCtx, source); err != nil {
		s.mu.Lock()
		s.watcherRunning = false
		s.watcherCancel = nil
		s.mu.Unlock()
		cancel() // Clean up the context
		return err
	}

	err := source.Watch(watchCtx, func() error {
		if err := s.LoadFromDataSource(watchCtx, source); err != nil {
			s.logger.Error("failed to reload data", "source", source.String(), "error", err)
			return err
		}
		return nil
	})

	// Clear watcher state when Watch exits (context cancelled, error, etc.)
	s.mu.Lock()
	s.watcherRunning = false
	s.watcherCancel = nil
	s.mu.Unlock()

	return err
}

// StopWatcher stops the running watcher by cancelling its context.
// This signals the DataSource.Watch method to exit. The method is safe to call
// even if no watcher is running.
func (s *Service) StopWatcher() {
	s.mu.Lock()
	cancel := s.watcherCancel
	s.watcherCancel = nil
	s.watcherRunning = false
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

func (s *Service) GetVersion() DataVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// GetDataAge returns the duration since data was last loaded.
// Returns 0 if no data has been loaded.
func (s *Service) GetDataAge() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.version.LoadTime.IsZero() {
		return 0
	}
	return time.Since(s.version.LoadTime)
}

// IsDataStale returns true if data is older than maxAge, or if no data is loaded.
// Use this in health checks to detect stale data from failed reloads.
func (s *Service) IsDataStale(maxAge time.Duration) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.version.LoadTime.IsZero() {
		return true
	}
	return time.Since(s.version.LoadTime) > maxAge
}

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

func (s *Service) GetEmployeeBySlackID(slackID string) *Employee {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Indexes.SlackIDMappings.SlackUIDToUID == nil || s.data.Lookups.Employees == nil {
		return nil
	}
	uid := s.data.Indexes.SlackIDMappings.SlackUIDToUID[slackID]
	if uid == "" {
		return nil
	}
	if emp, exists := s.data.Lookups.Employees[uid]; exists {
		return &emp
	}
	return nil
}

func (s *Service) GetEmployeeByGitHubID(githubID string) *Employee {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Indexes.GitHubIDMappings.GitHubIDToUID == nil || s.data.Lookups.Employees == nil {
		return nil
	}
	uid := s.data.Indexes.GitHubIDMappings.GitHubIDToUID[githubID]
	if uid == "" {
		return nil
	}
	if emp, exists := s.data.Lookups.Employees[uid]; exists {
		return &emp
	}
	return nil
}

func (s *Service) GetEmployeeByEmail(email string) *Employee {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Employees == nil {
		return nil
	}
	emailLower := strings.ToLower(email)
	for _, emp := range s.data.Lookups.Employees {
		if strings.ToLower(emp.Email) == emailLower {
			e := emp
			return &e
		}
	}
	return nil
}

func (s *Service) GetManagerForEmployee(uid string) *Employee {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Employees == nil {
		return nil
	}
	emp, exists := s.data.Lookups.Employees[uid]
	if !exists || emp.ManagerUID == "" {
		return nil
	}
	if manager, exists := s.data.Lookups.Employees[emp.ManagerUID]; exists {
		return &manager
	}
	return nil
}

func (s *Service) GetTeamByName(teamName string) *Team {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Teams == nil {
		return nil
	}
	if team, exists := s.data.Lookups.Teams[teamName]; exists {
		return &team
	}
	return nil
}

func (s *Service) GetOrgByName(orgName string) *Org {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Orgs == nil {
		return nil
	}
	if org, exists := s.data.Lookups.Orgs[orgName]; exists {
		return &org
	}
	return nil
}

func (s *Service) GetPillarByName(pillarName string) *Pillar {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Pillars == nil {
		return nil
	}
	if pillar, exists := s.data.Lookups.Pillars[pillarName]; exists {
		return &pillar
	}
	return nil
}

func (s *Service) GetTeamGroupByName(teamGroupName string) *TeamGroup {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.TeamGroups == nil {
		return nil
	}
	if tg, exists := s.data.Lookups.TeamGroups[teamGroupName]; exists {
		return &tg
	}
	return nil
}

func (s *Service) GetTeamsForUID(uid string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.getTeamsForUID(uid)
}

// getTeamsForUID is the internal version that assumes the lock is held.
func (s *Service) getTeamsForUID(uid string) []string {
	if s.data == nil || s.data.Indexes.Membership.MembershipIndex == nil {
		return []string{}
	}

	var teams []string
	for _, m := range s.data.Indexes.Membership.MembershipIndex[uid] {
		if m.Type == string(MembershipTeam) {
			teams = append(teams, m.Name)
		}
	}
	return teams
}

func (s *Service) GetTeamsForSlackID(slackID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	uid := s.getUIDFromSlackID(slackID)
	if uid == "" {
		return []string{}
	}
	return s.getTeamsForUID(uid)
}

func (s *Service) GetTeamMembers(teamName string) []Employee {
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
	for _, uid := range team.Group.ResolvedPeopleUIDList {
		if emp, exists := s.data.Lookups.Employees[uid]; exists {
			members = append(members, emp)
		}
	}
	return members
}

func (s *Service) IsEmployeeInTeam(uid string, teamName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.isEmployeeInTeam(uid, teamName)
}

// isEmployeeInTeam is the internal version that assumes the lock is held.
func (s *Service) isEmployeeInTeam(uid string, teamName string) bool {
	for _, team := range s.getTeamsForUID(uid) {
		if team == teamName {
			return true
		}
	}
	return false
}

func (s *Service) IsSlackUserInTeam(slackID string, teamName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	uid := s.getUIDFromSlackID(slackID)
	if uid == "" {
		return false
	}
	return s.isEmployeeInTeam(uid, teamName)
}

func (s *Service) IsEmployeeInOrg(uid string, orgName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.isEmployeeInOrg(uid, orgName)
}

// isEmployeeInOrg is the internal version that assumes the lock is held.
func (s *Service) isEmployeeInOrg(uid string, orgName string) bool {
	if s.data == nil || s.data.Indexes.Membership.MembershipIndex == nil {
		return false
	}

	for _, m := range s.data.Indexes.Membership.MembershipIndex[uid] {
		if m.Type == string(MembershipOrg) && m.Name == orgName {
			return true
		}
		if m.Type == string(MembershipTeam) {
			hierarchyPath := s.computeHierarchyPath(m.Name, "team")
			for _, entry := range hierarchyPath {
				if strings.ToLower(entry.Type) == "org" && entry.Name == orgName {
					return true
				}
			}
		}
	}
	return false
}

func (s *Service) IsSlackUserInOrg(slackID string, orgName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	uid := s.getUIDFromSlackID(slackID)
	if uid == "" {
		return false
	}
	return s.isEmployeeInOrg(uid, orgName)
}

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

	var orgs []OrgInfo
	seen := make(map[string]bool)

	for _, m := range s.data.Indexes.Membership.MembershipIndex[uid] {
		switch m.Type {
		case string(MembershipOrg):
			if !seen[m.Name] {
				orgs = append(orgs, OrgInfo{Name: m.Name, Type: OrgTypeOrganization})
				seen[m.Name] = true
			}
		case string(MembershipTeam):
			if !seen[m.Name] {
				orgs = append(orgs, OrgInfo{Name: m.Name, Type: OrgTypeTeam})
				seen[m.Name] = true
			}
			hierarchyPath := s.computeHierarchyPath(m.Name, "team")
			addHierarchyPathItems(&orgs, &seen, hierarchyPath)
		}
	}
	return orgs
}

func addHierarchyPathItems(orgs *[]OrgInfo, seen *map[string]bool, hierarchyPath []HierarchyPathEntry) {
	typeToOrgInfoType := map[string]OrgInfoType{
		"org":        OrgTypeOrganization,
		"pillar":     OrgTypePillar,
		"team_group": OrgTypeTeamGroup,
		"team":       OrgTypeParentTeam,
	}

	for i, entry := range hierarchyPath {
		if i == 0 {
			continue
		}
		if !(*seen)[entry.Name] {
			orgType, ok := typeToOrgInfoType[strings.ToLower(entry.Type)]
			if !ok {
				orgType = OrgTypeOrganization
			}
			*orgs = append(*orgs, OrgInfo{Name: entry.Name, Type: orgType})
			(*seen)[entry.Name] = true
		}
	}
}

func (s *Service) getUIDFromSlackID(slackID string) string {
	if s.data == nil || s.data.Indexes.SlackIDMappings.SlackUIDToUID == nil {
		return ""
	}
	return s.data.Indexes.SlackIDMappings.SlackUIDToUID[slackID]
}

// getEntityParent returns the parent info for an entity by name and type.
// Must be called with s.mu held.
func (s *Service) getEntityParent(entityName, entityType string) *ParentInfo {
	if s.data == nil {
		return nil
	}

	switch strings.ToLower(entityType) {
	case "team":
		if team, ok := s.data.Lookups.Teams[entityName]; ok {
			return team.Parent
		}
	case "org":
		if org, ok := s.data.Lookups.Orgs[entityName]; ok {
			return org.Parent
		}
	case "pillar":
		if pillar, ok := s.data.Lookups.Pillars[entityName]; ok {
			return pillar.Parent
		}
	case "team_group":
		if tg, ok := s.data.Lookups.TeamGroups[entityName]; ok {
			return tg.Parent
		}
	}
	return nil
}

// getEntityType looks up the type for an entity by scanning all lookups.
// Must be called with s.mu held.
func (s *Service) getEntityType(entityName string) string {
	if s.data == nil {
		return ""
	}
	if _, ok := s.data.Lookups.Teams[entityName]; ok {
		return "team"
	}
	if _, ok := s.data.Lookups.Orgs[entityName]; ok {
		return "org"
	}
	if _, ok := s.data.Lookups.Pillars[entityName]; ok {
		return "pillar"
	}
	if _, ok := s.data.Lookups.TeamGroups[entityName]; ok {
		return "team_group"
	}
	return ""
}

// computeHierarchyPath builds the hierarchy path by walking parent references.
// Must be called with s.mu held.
func (s *Service) computeHierarchyPath(entityName, entityType string) []HierarchyPathEntry {
	if s.data == nil {
		return []HierarchyPathEntry{}
	}

	// Check entity exists - either infer type or validate provided type
	if entityType == "" {
		entityType = s.getEntityType(entityName)
		if entityType == "" {
			return []HierarchyPathEntry{}
		}
	} else {
		// Validate entity exists with given type
		actualType := s.getEntityType(entityName)
		if actualType == "" || !strings.EqualFold(actualType, entityType) {
			return []HierarchyPathEntry{}
		}
		entityType = actualType
	}

	path := []HierarchyPathEntry{{Name: entityName, Type: entityType}}
	visited := make(map[string]bool)
	visited[entityName] = true

	currentName := entityName
	currentType := entityType

	for {
		parent := s.getEntityParent(currentName, currentType)
		if parent == nil {
			break
		}
		if visited[parent.Name] {
			break
		}
		visited[parent.Name] = true
		path = append(path, HierarchyPathEntry{Name: parent.Name, Type: parent.Type})
		currentName = parent.Name
		currentType = parent.Type
	}

	return path
}

func (s *Service) GetAllEmployeeUIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Employees == nil {
		return []string{}
	}
	uids := make([]string, 0, len(s.data.Lookups.Employees))
	for uid := range s.data.Lookups.Employees {
		uids = append(uids, uid)
	}
	return uids
}

func (s *Service) GetAllTeamNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Teams == nil {
		return []string{}
	}
	names := make([]string, 0, len(s.data.Lookups.Teams))
	for name := range s.data.Lookups.Teams {
		names = append(names, name)
	}
	return names
}

func (s *Service) GetAllOrgNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Orgs == nil {
		return []string{}
	}
	names := make([]string, 0, len(s.data.Lookups.Orgs))
	for name := range s.data.Lookups.Orgs {
		names = append(names, name)
	}
	return names
}

func (s *Service) GetAllPillarNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Pillars == nil {
		return []string{}
	}
	names := make([]string, 0, len(s.data.Lookups.Pillars))
	for name := range s.data.Lookups.Pillars {
		names = append(names, name)
	}
	return names
}

func (s *Service) GetAllTeamGroupNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.TeamGroups == nil {
		return []string{}
	}
	names := make([]string, 0, len(s.data.Lookups.TeamGroups))
	for name := range s.data.Lookups.TeamGroups {
		names = append(names, name)
	}
	return names
}

// GetHierarchyPath returns the ordered hierarchy path from entity to root.
func (s *Service) GetHierarchyPath(entityName string, entityType string) []HierarchyPathEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.computeHierarchyPath(entityName, entityType)
}

// GetDescendantsTree returns all descendants of an entity as a nested tree.
func (s *Service) GetDescendantsTree(entityName string) *HierarchyNode {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil {
		return nil
	}

	entityType := s.getEntityType(entityName)
	if entityType == "" {
		return nil
	}

	// Build children map by scanning all entities
	childrenMap := make(map[string][]struct{ name, typ string })

	for name, team := range s.data.Lookups.Teams {
		if team.Parent != nil {
			childrenMap[team.Parent.Name] = append(childrenMap[team.Parent.Name], struct{ name, typ string }{name, "team"})
		}
	}
	for name, org := range s.data.Lookups.Orgs {
		if org.Parent != nil {
			childrenMap[org.Parent.Name] = append(childrenMap[org.Parent.Name], struct{ name, typ string }{name, "org"})
		}
	}
	for name, pillar := range s.data.Lookups.Pillars {
		if pillar.Parent != nil {
			childrenMap[pillar.Parent.Name] = append(childrenMap[pillar.Parent.Name], struct{ name, typ string }{name, "pillar"})
		}
	}
	for name, tg := range s.data.Lookups.TeamGroups {
		if tg.Parent != nil {
			childrenMap[tg.Parent.Name] = append(childrenMap[tg.Parent.Name], struct{ name, typ string }{name, "team_group"})
		}
	}

	// Build tree recursively
	var buildNode func(name, typ string, visited map[string]bool) HierarchyNode
	buildNode = func(name, typ string, visited map[string]bool) HierarchyNode {
		if visited[name] {
			return HierarchyNode{Name: name, Type: typ, Children: []HierarchyNode{}}
		}
		visited[name] = true

		children := childrenMap[name]
		childNodes := make([]HierarchyNode, 0, len(children))
		for _, c := range children {
			childNodes = append(childNodes, buildNode(c.name, c.typ, visited))
		}

		return HierarchyNode{Name: name, Type: typ, Children: childNodes}
	}

	node := buildNode(entityName, entityType, make(map[string]bool))
	return &node
}

// GetComponentByName returns a component by name.
func (s *Service) GetComponentByName(name string) *Component {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Components == nil {
		return nil
	}
	if component, exists := s.data.Lookups.Components[name]; exists {
		return &component
	}
	return nil
}

// GetAllComponents returns all components.
func (s *Service) GetAllComponents() []Component {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Components == nil {
		return []Component{}
	}
	components := make([]Component, 0, len(s.data.Lookups.Components))
	for _, component := range s.data.Lookups.Components {
		components = append(components, component)
	}
	return components
}

// GetAllComponentNames returns all component names.
func (s *Service) GetAllComponentNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Lookups.Components == nil {
		return []string{}
	}
	names := make([]string, 0, len(s.data.Lookups.Components))
	for name := range s.data.Lookups.Components {
		names = append(names, name)
	}
	return names
}

// GetJiraProjects returns all Jira project keys.
func (s *Service) GetJiraProjects() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Indexes.Jira == nil {
		return []string{}
	}
	projects := make([]string, 0, len(s.data.Indexes.Jira))
	for project := range s.data.Indexes.Jira {
		projects = append(projects, project)
	}
	return projects
}

// GetJiraComponents returns all components for a Jira project.
func (s *Service) GetJiraComponents(project string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Indexes.Jira == nil {
		return []string{}
	}
	components, exists := s.data.Indexes.Jira[project]
	if !exists {
		return []string{}
	}
	result := make([]string, 0, len(components))
	for component := range components {
		result = append(result, component)
	}
	return result
}

// GetTeamsByJiraProject returns all teams/entities that own any component in a Jira project.
func (s *Service) GetTeamsByJiraProject(project string) []JiraOwnerInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Indexes.Jira == nil {
		return []JiraOwnerInfo{}
	}
	components, exists := s.data.Indexes.Jira[project]
	if !exists {
		return []JiraOwnerInfo{}
	}

	seen := make(map[string]bool)
	var result []JiraOwnerInfo
	for _, owners := range components {
		for _, owner := range owners {
			if !seen[owner.Name] {
				seen[owner.Name] = true
				result = append(result, owner)
			}
		}
	}
	return result
}

// GetTeamsByJiraComponent returns teams/entities that own a specific Jira component.
func (s *Service) GetTeamsByJiraComponent(project, component string) []JiraOwnerInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Indexes.Jira == nil {
		return []JiraOwnerInfo{}
	}
	components, exists := s.data.Indexes.Jira[project]
	if !exists {
		return []JiraOwnerInfo{}
	}
	owners, exists := components[component]
	if !exists {
		return []JiraOwnerInfo{}
	}
	// Return a copy to avoid external modification
	result := make([]JiraOwnerInfo, len(owners))
	copy(result, owners)
	return result
}

// GetJiraOwnershipForTeam returns all Jira projects and components owned by a team.
func (s *Service) GetJiraOwnershipForTeam(teamName string) []JiraOwnership {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || s.data.Indexes.Jira == nil {
		return []JiraOwnership{}
	}

	var result []JiraOwnership
	for project, components := range s.data.Indexes.Jira {
		for component, owners := range components {
			for _, owner := range owners {
				if owner.Name == teamName {
					result = append(result, JiraOwnership{Project: project, Component: component})
					break
				}
			}
		}
	}
	return result
}

// validateData checks that required data structures are present.
func validateData(data *Data) error {
	if len(data.Lookups.Employees) == 0 {
		return fmt.Errorf("%w: missing lookups.employees", ErrInvalidData)
	}
	if len(data.Indexes.Membership.MembershipIndex) == 0 {
		return fmt.Errorf("%w: missing indexes.membership.membership_index", ErrInvalidData)
	}
	return nil
}
