//go:build go1.23

package orgdatacore

import "iter"

// AllEmployeeUIDs returns an iterator over all employee UIDs.
// The iterator uses a snapshot approach - data is collected while briefly
// holding a read lock, then iteration proceeds without holding any lock.
// This is safe for concurrent use and allows slow consumer operations.
func (s *Service) AllEmployeeUIDs() iter.Seq[string] {
	// Snapshot while holding lock
	s.mu.RLock()
	var uids []string
	if s.data != nil && s.data.Lookups.Employees != nil {
		uids = make([]string, 0, len(s.data.Lookups.Employees))
		for uid := range s.data.Lookups.Employees {
			uids = append(uids, uid)
		}
	}
	s.mu.RUnlock()

	// Iterate without lock
	return func(yield func(string) bool) {
		for _, uid := range uids {
			if !yield(uid) {
				return
			}
		}
	}
}

// AllEmployees returns an iterator over all employees.
// The iterator uses a snapshot approach - data is collected while briefly
// holding a read lock, then iteration proceeds without holding any lock.
func (s *Service) AllEmployees() iter.Seq[*Employee] {
	// Snapshot while holding lock
	s.mu.RLock()
	var employees []*Employee
	if s.data != nil && s.data.Lookups.Employees != nil {
		employees = make([]*Employee, 0, len(s.data.Lookups.Employees))
		for _, emp := range s.data.Lookups.Employees {
			e := emp // Copy to avoid reference issues
			employees = append(employees, &e)
		}
	}
	s.mu.RUnlock()

	// Iterate without lock
	return func(yield func(*Employee) bool) {
		for _, emp := range employees {
			if !yield(emp) {
				return
			}
		}
	}
}

// AllTeamNames returns an iterator over all team names.
// The iterator uses a snapshot approach for safe concurrent use.
func (s *Service) AllTeamNames() iter.Seq[string] {
	s.mu.RLock()
	var names []string
	if s.data != nil && s.data.Lookups.Teams != nil {
		names = make([]string, 0, len(s.data.Lookups.Teams))
		for name := range s.data.Lookups.Teams {
			names = append(names, name)
		}
	}
	s.mu.RUnlock()

	return func(yield func(string) bool) {
		for _, name := range names {
			if !yield(name) {
				return
			}
		}
	}
}

// AllTeams returns an iterator over all teams with their names.
// The iterator uses a snapshot approach for safe concurrent use.
func (s *Service) AllTeams() iter.Seq2[string, *Team] {
	type entry struct {
		name string
		team *Team
	}

	s.mu.RLock()
	var entries []entry
	if s.data != nil && s.data.Lookups.Teams != nil {
		entries = make([]entry, 0, len(s.data.Lookups.Teams))
		for name, team := range s.data.Lookups.Teams {
			t := team // Copy to avoid reference issues
			entries = append(entries, entry{name: name, team: &t})
		}
	}
	s.mu.RUnlock()

	return func(yield func(string, *Team) bool) {
		for _, e := range entries {
			if !yield(e.name, e.team) {
				return
			}
		}
	}
}

// AllOrgNames returns an iterator over all organization names.
// The iterator uses a snapshot approach for safe concurrent use.
func (s *Service) AllOrgNames() iter.Seq[string] {
	s.mu.RLock()
	var names []string
	if s.data != nil && s.data.Lookups.Orgs != nil {
		names = make([]string, 0, len(s.data.Lookups.Orgs))
		for name := range s.data.Lookups.Orgs {
			names = append(names, name)
		}
	}
	s.mu.RUnlock()

	return func(yield func(string) bool) {
		for _, name := range names {
			if !yield(name) {
				return
			}
		}
	}
}

// AllOrgs returns an iterator over all organizations with their names.
// The iterator uses a snapshot approach for safe concurrent use.
func (s *Service) AllOrgs() iter.Seq2[string, *Org] {
	type entry struct {
		name string
		org  *Org
	}

	s.mu.RLock()
	var entries []entry
	if s.data != nil && s.data.Lookups.Orgs != nil {
		entries = make([]entry, 0, len(s.data.Lookups.Orgs))
		for name, org := range s.data.Lookups.Orgs {
			o := org
			entries = append(entries, entry{name: name, org: &o})
		}
	}
	s.mu.RUnlock()

	return func(yield func(string, *Org) bool) {
		for _, e := range entries {
			if !yield(e.name, e.org) {
				return
			}
		}
	}
}

// AllPillarNames returns an iterator over all pillar names.
// The iterator uses a snapshot approach for safe concurrent use.
func (s *Service) AllPillarNames() iter.Seq[string] {
	s.mu.RLock()
	var names []string
	if s.data != nil && s.data.Lookups.Pillars != nil {
		names = make([]string, 0, len(s.data.Lookups.Pillars))
		for name := range s.data.Lookups.Pillars {
			names = append(names, name)
		}
	}
	s.mu.RUnlock()

	return func(yield func(string) bool) {
		for _, name := range names {
			if !yield(name) {
				return
			}
		}
	}
}

// AllPillars returns an iterator over all pillars with their names.
// The iterator uses a snapshot approach for safe concurrent use.
func (s *Service) AllPillars() iter.Seq2[string, *Pillar] {
	type entry struct {
		name   string
		pillar *Pillar
	}

	s.mu.RLock()
	var entries []entry
	if s.data != nil && s.data.Lookups.Pillars != nil {
		entries = make([]entry, 0, len(s.data.Lookups.Pillars))
		for name, pillar := range s.data.Lookups.Pillars {
			p := pillar
			entries = append(entries, entry{name: name, pillar: &p})
		}
	}
	s.mu.RUnlock()

	return func(yield func(string, *Pillar) bool) {
		for _, e := range entries {
			if !yield(e.name, e.pillar) {
				return
			}
		}
	}
}

// AllTeamGroupNames returns an iterator over all team group names.
// The iterator uses a snapshot approach for safe concurrent use.
func (s *Service) AllTeamGroupNames() iter.Seq[string] {
	s.mu.RLock()
	var names []string
	if s.data != nil && s.data.Lookups.TeamGroups != nil {
		names = make([]string, 0, len(s.data.Lookups.TeamGroups))
		for name := range s.data.Lookups.TeamGroups {
			names = append(names, name)
		}
	}
	s.mu.RUnlock()

	return func(yield func(string) bool) {
		for _, name := range names {
			if !yield(name) {
				return
			}
		}
	}
}

// AllTeamGroups returns an iterator over all team groups with their names.
// The iterator uses a snapshot approach for safe concurrent use.
func (s *Service) AllTeamGroups() iter.Seq2[string, *TeamGroup] {
	type entry struct {
		name      string
		teamGroup *TeamGroup
	}

	s.mu.RLock()
	var entries []entry
	if s.data != nil && s.data.Lookups.TeamGroups != nil {
		entries = make([]entry, 0, len(s.data.Lookups.TeamGroups))
		for name, tg := range s.data.Lookups.TeamGroups {
			teamGroup := tg
			entries = append(entries, entry{name: name, teamGroup: &teamGroup})
		}
	}
	s.mu.RUnlock()

	return func(yield func(string, *TeamGroup) bool) {
		for _, e := range entries {
			if !yield(e.name, e.teamGroup) {
				return
			}
		}
	}
}
