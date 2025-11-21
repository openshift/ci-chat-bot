package orgdatacore

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// FakeDataSource implements DataSource for testing with controllable data
type FakeDataSource struct {
	Data        string
	LoadError   error
	WatchError  error
	Description string
	WatchCalled bool
}

// NewFakeDataSource creates a fake data source for testing
func NewFakeDataSource(data string) *FakeDataSource {
	return &FakeDataSource{
		Data:        data,
		Description: "fake-data-source",
	}
}

// Load returns the test data
func (f *FakeDataSource) Load(ctx context.Context) (io.ReadCloser, error) {
	if f.LoadError != nil {
		return nil, f.LoadError
	}
	return io.NopCloser(strings.NewReader(f.Data)), nil
}

// Watch tracks that it was called but doesn't actually watch
func (f *FakeDataSource) Watch(ctx context.Context, callback func() error) error {
	f.WatchCalled = true
	if f.WatchError != nil {
		return f.WatchError
	}
	return nil
}

// String returns the description
func (f *FakeDataSource) String() string {
	return f.Description
}

// CreateTestData creates minimal valid test data for testing
func CreateTestData() *Data {
	return &Data{
		Metadata: Metadata{
			GeneratedAt:    "2024-01-01T00:00:00Z",
			DataVersion:    "test-v1.0",
			TotalEmployees: 2,
			TotalOrgs:      1,
			TotalTeams:     1,
		},
		Lookups: Lookups{
			Employees: map[string]Employee{
				"testuser1": {
					UID:      "testuser1",
					FullName: "Test User One",
					Email:    "testuser1@example.com",
					JobTitle: "Test Engineer",
					SlackUID: "U111111",
				},
				"testuser2": {
					UID:      "testuser2",
					FullName: "Test User Two",
					Email:    "testuser2@example.com",
					JobTitle: "Test Manager",
					SlackUID: "U222222",
				},
			},
			Teams: map[string]Team{
				"test-squad": {
					UID:  "team1",
					Name: "test-squad",
					Type: "team",
					Group: Group{
						Type: GroupType{
							Name: "team",
						},
						ResolvedPeopleUIDList: []string{"testuser1", "testuser2"},
					},
				},
			},
			Orgs: map[string]Org{
				"test-division": {
					UID:  "org1",
					Name: "test-division",
					Type: "organization",
					Group: Group{
						Type: GroupType{
							Name: "organization",
						},
						ResolvedPeopleUIDList: []string{"testuser1", "testuser2"},
					},
				},
			},
		},
		Indexes: Indexes{
			Membership: MembershipIndex{
				MembershipIndex: map[string][]MembershipInfo{
					"testuser1": {
						{Name: "test-squad", Type: "team"},
						{Name: "test-division", Type: "org"},
					},
					"testuser2": {
						{Name: "test-squad", Type: "team"},
						{Name: "test-division", Type: "org"},
					},
				},
				RelationshipIndex: map[string]map[string]RelationshipInfo{
					"teams": {
						"test-squad": {
							Ancestry: struct {
								Orgs       []string `json:"orgs"`
								Teams      []string `json:"teams"`
								Pillars    []string `json:"pillars"`
								TeamGroups []string `json:"team_groups"`
							}{
								Orgs:       []string{"test-division"},
								Teams:      []string{},
								Pillars:    []string{},
								TeamGroups: []string{},
							},
						},
					},
				},
			},
			SlackIDMappings: SlackIDMappings{
				SlackUIDToUID: map[string]string{
					"U111111": "testuser1",
					"U222222": "testuser2",
				},
			},
			GitHubIDMappings: GitHubIDMappings{
				GitHubIDToUID: map[string]string{
					"ghuser1": "testuser1",
					"ghuser2": "testuser2",
				},
			},
		},
	}
}

// CreateTestDataJSON creates test data as JSON string
func CreateTestDataJSON() string {
	data := CreateTestData()
	jsonData, _ := json.Marshal(data)
	return string(jsonData)
}

// CreateEmptyTestData creates test data with no employees/teams/orgs
func CreateEmptyTestData() string {
	data := &Data{
		Metadata: Metadata{
			GeneratedAt:    "2024-01-01T00:00:00Z",
			DataVersion:    "empty-v1.0",
			TotalEmployees: 0,
			TotalOrgs:      0,
			TotalTeams:     0,
		},
		Lookups: Lookups{
			Employees: map[string]Employee{},
			Teams:     map[string]Team{},
			Orgs:      map[string]Org{},
		},
		Indexes: Indexes{
			Membership: MembershipIndex{
				MembershipIndex:   map[string][]MembershipInfo{},
				RelationshipIndex: map[string]map[string]RelationshipInfo{},
			},
			SlackIDMappings: SlackIDMappings{
				SlackUIDToUID: map[string]string{},
			},
			GitHubIDMappings: GitHubIDMappings{
				GitHubIDToUID: map[string]string{},
			},
		},
	}

	jsonData, _ := json.Marshal(data)
	return string(jsonData)
}

// AssertEmployeeEqual compares two employees for testing
func AssertEmployeeEqual(t *testing.T, actual, expected *Employee, context string) {
	t.Helper()

	if actual == nil && expected == nil {
		return
	}

	if actual == nil || expected == nil {
		t.Errorf("%s: got %+v, expected %+v", context, actual, expected)
		return
	}

	if actual.UID != expected.UID {
		t.Errorf("%s: UID got %q, expected %q", context, actual.UID, expected.UID)
	}
	if actual.FullName != expected.FullName {
		t.Errorf("%s: FullName got %q, expected %q", context, actual.FullName, expected.FullName)
	}
	if actual.Email != expected.Email {
		t.Errorf("%s: Email got %q, expected %q", context, actual.Email, expected.Email)
	}
	if actual.JobTitle != expected.JobTitle {
		t.Errorf("%s: JobTitle got %q, expected %q", context, actual.JobTitle, expected.JobTitle)
	}
	if actual.SlackUID != expected.SlackUID {
		t.Errorf("%s: SlackUID got %q, expected %q", context, actual.SlackUID, expected.SlackUID)
	}
}
