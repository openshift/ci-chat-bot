package orgdatacore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// AnonymizingDataSource is a DataSource decorator that anonymizes PII
// in loaded data. When pii_mode is PIIModeAnonymized, it replaces real
// UIDs with random nonces (HUMAN-<8 hex chars>) and strips PII fields,
// while preserving structural relationships.
//
// Nonce tables are ephemeral — rebuilt on every Load() call.
//
// PII fields anonymized:
//   - uid → HUMAN-<hex> nonce
//   - full_name → "[ANONYMIZED]"
//   - email → "[ANONYMIZED]"
//   - slack_uid → SLACK-<hex> nonce
//   - github_id → GITHUB-<hex> nonce
//   - manager_uid → mapped HUMAN nonce (consistent)
//
// Indexes re-mapped:
//   - membership_index keys → HUMAN nonces
//   - resolved_people_uid_list → HUMAN nonces
//   - roles.people → HUMAN nonces
//   - slack_id_mappings: SLACK nonce → HUMAN nonce
//   - github_id_mappings: GITHUB nonce → HUMAN nonce
type AnonymizingDataSource struct {
	source  DataSource
	piiMode PIIMode

	mu               sync.Mutex
	nonceToUID       map[string]string
	uidToNonce       map[string]string
	nameToNonce      map[string]string
	nonceToDisplay   map[string]string
	slackIDToNonce   map[string]string
	githubIDToNonce  map[string]string
}

// NewAnonymizingDataSource wraps a DataSource with PII anonymization.
// Defaults to PIIModeFull (no anonymization) if piiMode is empty.
func NewAnonymizingDataSource(source DataSource, piiMode PIIMode) *AnonymizingDataSource {
	if piiMode == "" {
		piiMode = PIIModeFull
	}
	return &AnonymizingDataSource{
		source:  source,
		piiMode: piiMode,
	}
}

func (a *AnonymizingDataSource) Load(ctx context.Context) (io.ReadCloser, error) {
	reader, err := a.source.Load(ctx)
	if err != nil {
		return nil, err
	}

	if a.piiMode != PIIModeAnonymized {
		return reader, nil
	}

	defer reader.Close()

	var data Data
	if err := json.NewDecoder(reader).Decode(&data); err != nil {
		return nil, fmt.Errorf("anonymizing data source: decode: %w", err)
	}

	a.mu.Lock()
	a.anonymize(&data)
	a.mu.Unlock()

	out, err := json.Marshal(&data)
	if err != nil {
		return nil, fmt.Errorf("anonymizing data source: encode: %w", err)
	}
	return io.NopCloser(bytes.NewReader(out)), nil
}

func (a *AnonymizingDataSource) Watch(ctx context.Context, callback func() error) error {
	return a.source.Watch(ctx, callback)
}

func (a *AnonymizingDataSource) String() string {
	if a.piiMode == PIIModeAnonymized {
		return fmt.Sprintf("%s [PII anonymized]", a.source)
	}
	return a.source.String()
}

func (a *AnonymizingDataSource) Close() error {
	return a.source.Close()
}

func (a *AnonymizingDataSource) Resolve(nonce string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	uid, ok := a.nonceToUID[nonce]
	return uid, ok
}

func (a *AnonymizingDataSource) AnonymizeUID(uid string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	nonce, ok := a.uidToNonce[uid]
	return nonce, ok
}

// LookupByName performs a case-insensitive name lookup returning the nonce.
func (a *AnonymizingDataSource) LookupByName(name string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	nonce, ok := a.nameToNonce[strings.ToLower(name)]
	return nonce, ok
}

// GetDisplayName returns human-readable display for a nonce: "Full Name (uid)".
func (a *AnonymizingDataSource) GetDisplayName(nonce string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	display, ok := a.nonceToDisplay[nonce]
	return display, ok
}

func (a *AnonymizingDataSource) UIDToNonceMap() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	m := make(map[string]string, len(a.uidToNonce))
	for k, v := range a.uidToNonce {
		m[k] = v
	}
	return m
}

func (a *AnonymizingDataSource) NameToNonceMap() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	m := make(map[string]string, len(a.nameToNonce))
	for k, v := range a.nameToNonce {
		m[k] = v
	}
	return m
}

func (a *AnonymizingDataSource) SlackIDToNonceMap() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	m := make(map[string]string, len(a.slackIDToNonce))
	for k, v := range a.slackIDToNonce {
		m[k] = v
	}
	return m
}

func (a *AnonymizingDataSource) GitHubIDToNonceMap() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	m := make(map[string]string, len(a.githubIDToNonce))
	for k, v := range a.githubIDToNonce {
		m[k] = v
	}
	return m
}

// anonymize rewrites org data in-place and rebuilds lookup tables.
// Must be called with a.mu held.
func (a *AnonymizingDataSource) anonymize(data *Data) {
	a.nonceToUID = make(map[string]string)
	a.uidToNonce = make(map[string]string)
	a.nameToNonce = make(map[string]string)
	a.nonceToDisplay = make(map[string]string)
	a.slackIDToNonce = make(map[string]string)
	a.githubIDToNonce = make(map[string]string)

	usedNonces := make(map[string]struct{})

	employees := data.Lookups.Employees

	// Phase 1: Build UID → nonce mapping for all employees
	for uid, emp := range employees {
		nonce := generateNonce("HUMAN-", usedNonces)
		a.uidToNonce[uid] = nonce
		a.nonceToUID[nonce] = uid

		fullName := emp.FullName
		if fullName != "" {
			a.nameToNonce[strings.ToLower(fullName)] = nonce
			a.nonceToDisplay[nonce] = fmt.Sprintf("%s (%s)", fullName, uid)
		} else {
			a.nonceToDisplay[nonce] = fmt.Sprintf("(%s)", uid)
		}
	}

	// Phase 2: Build Slack ID and GitHub ID nonce mappings
	for slackID, uid := range data.Indexes.SlackIDMappings.SlackUIDToUID {
		if _, ok := a.uidToNonce[uid]; ok {
			a.slackIDToNonce[slackID] = generateNonce("SLACK-", usedNonces)
		}
	}
	for githubID, uid := range data.Indexes.GitHubIDMappings.GitHubIDToUID {
		if _, ok := a.uidToNonce[uid]; ok {
			a.githubIDToNonce[githubID] = generateNonce("GITHUB-", usedNonces)
		}
	}

	// Phase 3: Rewrite employee records with nonces for all ID fields
	newEmployees := make(map[string]Employee, len(employees))
	// Build reverse maps: uid → slack nonce, uid → github nonce
	uidToSlackNonce := make(map[string]string)
	for slackID, uid := range data.Indexes.SlackIDMappings.SlackUIDToUID {
		if slackNonce, ok := a.slackIDToNonce[slackID]; ok {
			uidToSlackNonce[uid] = slackNonce
		}
	}
	uidToGitHubNonce := make(map[string]string)
	for githubID, uid := range data.Indexes.GitHubIDMappings.GitHubIDToUID {
		if githubNonce, ok := a.githubIDToNonce[githubID]; ok {
			uidToGitHubNonce[uid] = githubNonce
		}
	}

	for uid, emp := range employees {
		nonce := a.uidToNonce[uid]
		emp.UID = nonce
		emp.FullName = "[ANONYMIZED]"
		emp.Email = "[ANONYMIZED]"
		emp.SlackUID = uidToSlackNonce[uid]
		emp.GitHubID = uidToGitHubNonce[uid]

		if emp.ManagerUID != "" {
			if managerNonce, ok := a.uidToNonce[emp.ManagerUID]; ok {
				emp.ManagerUID = managerNonce
			} else {
				emp.ManagerUID = ""
			}
		}

		newEmployees[nonce] = emp
	}
	data.Lookups.Employees = newEmployees

	// Phase 4: Re-key membership index and remap Slack/GitHub indexes with nonces
	if data.Indexes.Membership.MembershipIndex != nil {
		oldIndex := data.Indexes.Membership.MembershipIndex
		newIndex := make(map[string][]MembershipInfo, len(oldIndex))
		for uid, memberships := range oldIndex {
			if nonce, ok := a.uidToNonce[uid]; ok {
				newIndex[nonce] = memberships
			}
		}
		data.Indexes.Membership.MembershipIndex = newIndex
	}

	// Remap slack index: slackNonce → uidNonce
	newSlackIndex := make(map[string]string, len(a.slackIDToNonce))
	for slackID, slackNonce := range a.slackIDToNonce {
		if uid, ok := data.Indexes.SlackIDMappings.SlackUIDToUID[slackID]; ok {
			if uidNonce, ok := a.uidToNonce[uid]; ok {
				newSlackIndex[slackNonce] = uidNonce
			}
		}
	}
	data.Indexes.SlackIDMappings.SlackUIDToUID = newSlackIndex

	// Remap github index: githubNonce → uidNonce
	newGitHubIndex := make(map[string]string, len(a.githubIDToNonce))
	for githubID, githubNonce := range a.githubIDToNonce {
		if uid, ok := data.Indexes.GitHubIDMappings.GitHubIDToUID[githubID]; ok {
			if uidNonce, ok := a.uidToNonce[uid]; ok {
				newGitHubIndex[githubNonce] = uidNonce
			}
		}
	}
	data.Indexes.GitHubIDMappings.GitHubIDToUID = newGitHubIndex

	// Phase 4: Rewrite group people lists in teams/orgs/pillars/team_groups
	for name, team := range data.Lookups.Teams {
		a.remapGroup(&team.Group)
		data.Lookups.Teams[name] = team
	}
	for name, org := range data.Lookups.Orgs {
		a.remapGroup(&org.Group)
		data.Lookups.Orgs[name] = org
	}
	for name, pillar := range data.Lookups.Pillars {
		a.remapGroup(&pillar.Group)
		data.Lookups.Pillars[name] = pillar
	}
	for name, tg := range data.Lookups.TeamGroups {
		a.remapGroup(&tg.Group)
		data.Lookups.TeamGroups[name] = tg
	}
}

func (a *AnonymizingDataSource) remapGroup(g *Group) {
	for i, uid := range g.ResolvedPeopleUIDList {
		if nonce, ok := a.uidToNonce[uid]; ok {
			g.ResolvedPeopleUIDList[i] = nonce
		}
	}

	for i := range g.Roles {
		for j, uid := range g.Roles[i].People {
			if nonce, ok := a.uidToNonce[uid]; ok {
				g.Roles[i].People[j] = nonce
			}
		}
	}
}

func generateNonce(prefix string, used map[string]struct{}) string {
	for {
		b := make([]byte, 4)
		if _, err := rand.Read(b); err != nil {
			panic(fmt.Sprintf("crypto/rand failed: %v", err))
		}
		nonce := prefix + hex.EncodeToString(b)
		if _, exists := used[nonce]; !exists {
			used[nonce] = struct{}{}
			return nonce
		}
	}
}
