package orgdatacore

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

type FakeDataSource struct {
	Data        string
	LoadError   error
	WatchError  error
	Description string
	WatchCalled bool
	CloseCalled bool
}

func NewFakeDataSource(data string) *FakeDataSource {
	return &FakeDataSource{Data: data, Description: "fake-data-source"}
}

func (f *FakeDataSource) Load(ctx context.Context) (io.ReadCloser, error) {
	if f.LoadError != nil {
		return nil, f.LoadError
	}
	return io.NopCloser(strings.NewReader(f.Data)), nil
}

func (f *FakeDataSource) Watch(ctx context.Context, callback func() error) error {
	f.WatchCalled = true
	return f.WatchError
}

func (f *FakeDataSource) String() string { return f.Description }

func (f *FakeDataSource) Close() error {
	f.CloseCalled = true
	return nil
}

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
				"testuser1": {UID: "testuser1", FullName: "Test User One", Email: "testuser1@example.com", JobTitle: "Test Engineer", SlackUID: "U111111"},
				"testuser2": {UID: "testuser2", FullName: "Test User Two", Email: "testuser2@example.com", JobTitle: "Test Manager", SlackUID: "U222222"},
			},
			Teams: map[string]Team{
				"test-squad": {UID: "team1", Name: "test-squad", Type: "team", Parent: &ParentInfo{Name: "test-division", Type: "org"}, Group: Group{Type: GroupType{Name: "team"}, ResolvedPeopleUIDList: []string{"testuser1", "testuser2"}}},
			},
			Orgs: map[string]Org{
				"test-division": {UID: "org1", Name: "test-division", Type: "organization", Group: Group{Type: GroupType{Name: "organization"}, ResolvedPeopleUIDList: []string{"testuser1", "testuser2"}}},
			},
		},
		Indexes: Indexes{
			Membership: MembershipIndex{
				MembershipIndex: map[string][]MembershipInfo{
					"testuser1": {{Name: "test-squad", Type: "team"}, {Name: "test-division", Type: "org"}},
					"testuser2": {{Name: "test-squad", Type: "team"}, {Name: "test-division", Type: "org"}},
				},
			},
			SlackIDMappings:  SlackIDMappings{SlackUIDToUID: map[string]string{"U111111": "testuser1", "U222222": "testuser2"}},
			GitHubIDMappings: GitHubIDMappings{GitHubIDToUID: map[string]string{"ghuser1": "testuser1", "ghuser2": "testuser2"}},
		},
	}
}

func CreateTestDataJSON() string {
	data := CreateTestData()
	jsonData, _ := json.Marshal(data)
	return string(jsonData)
}

func CreateEmptyTestData() string {
	data := &Data{
		Metadata: Metadata{GeneratedAt: "2024-01-01T00:00:00Z", DataVersion: "empty-v1.0"},
		Lookups:  Lookups{Employees: map[string]Employee{}, Teams: map[string]Team{}, Orgs: map[string]Org{}},
		Indexes: Indexes{
			Membership:       MembershipIndex{MembershipIndex: map[string][]MembershipInfo{}},
			SlackIDMappings:  SlackIDMappings{SlackUIDToUID: map[string]string{}},
			GitHubIDMappings: GitHubIDMappings{GitHubIDToUID: map[string]string{}},
		},
	}
	jsonData, _ := json.Marshal(data)
	return string(jsonData)
}

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
}
