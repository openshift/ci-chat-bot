package slack

import (
	"fmt"
	"strings"
	"testing"

	orgdatacore "github.com/openshift-eng/cyborg-data/go"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/parser"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

type aroHcpTestJobManager struct {
	mockJobManager
	launchJob func(*manager.JobRequest)
}

func (m *aroHcpTestJobManager) LaunchJobForUser(req *manager.JobRequest) (string, error) {
	if m.launchJob != nil {
		m.launchJob(req)
	}
	return "launched", nil
}

func TestAroHcpCreateAuthorization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		employeeBySlackID    *orgdatacore.Employee
		employeeByEmail      *orgdatacore.Employee
		profileEmail         string
		memberships          []orgdatacore.MembershipInfo
		getUserInfoError     error
		expectLaunch         bool
		expectUserName       string
		expectUserEmail      string
		expectResponsePhrase string
	}{
		{
			name:              "ARO team membership is allowed",
			employeeBySlackID: &orgdatacore.Employee{UID: "aro-user", Email: "aro@example.com"},
			memberships:       []orgdatacore.MembershipInfo{{Name: "ARO", Type: "team"}},
			expectLaunch:      true,
			expectUserName:    "aro-user",
			expectUserEmail:   "aro@example.com",
		},
		{
			name:              "CRT team group membership is allowed",
			employeeBySlackID: &orgdatacore.Employee{UID: "crt-group-user", Email: "crt-group@example.com"},
			memberships:       []orgdatacore.MembershipInfo{{Name: "Continuous Release Tooling (CRT)", Type: "team_group"}},
			expectLaunch:      true,
			expectUserName:    "crt-group-user",
			expectUserEmail:   "crt-group@example.com",
		},
		{
			name:              "ARO organization membership is allowed",
			employeeBySlackID: &orgdatacore.Employee{UID: "aro-org-user", Email: "aro-org@example.com"},
			memberships:       []orgdatacore.MembershipInfo{{Name: "ARO", Type: "org"}},
			expectLaunch:      true,
			expectUserName:    "aro-org-user",
			expectUserEmail:   "aro-org@example.com",
		},
		{
			name:            "Slack email fallback is allowed",
			employeeByEmail: &orgdatacore.Employee{UID: "email-user", Email: "email-user@example.com"},
			profileEmail:    "email-user@example.com",
			memberships:     []orgdatacore.MembershipInfo{{Name: "Continuous Release Tooling (CRT)", Type: "org"}},
			expectLaunch:    true,
			expectUserName:  "email-user",
			expectUserEmail: "email-user@example.com",
		},
		{
			name:              "Slack ID takes precedence over email fallback",
			employeeBySlackID: &orgdatacore.Employee{UID: "slack-user", Email: "slack-user@example.com"},
			employeeByEmail:   &orgdatacore.Employee{UID: "email-user", Email: "email-user@example.com"},
			profileEmail:      "email-user@example.com",
			memberships:       []orgdatacore.MembershipInfo{{Name: "ARO", Type: "team"}},
			expectLaunch:      true,
			expectUserName:    "slack-user",
			expectUserEmail:   "slack-user@example.com",
		},
		{
			name:                 "similar membership name is rejected",
			employeeBySlackID:    &orgdatacore.Employee{UID: "similar-name-user", Email: "similar@example.com"},
			memberships:          []orgdatacore.MembershipInfo{{Name: "ARO Engineering", Type: "team"}},
			expectResponsePhrase: "not authorized",
		},
		{
			name:                 "unrelated membership is rejected",
			employeeBySlackID:    &orgdatacore.Employee{UID: "unrelated-user", Email: "unrelated@example.com"},
			memberships:          []orgdatacore.MembershipInfo{{Name: "Other Organization", Type: "org"}},
			expectResponsePhrase: "not authorized",
		},
		{
			name:                 "employee with no UID is rejected",
			employeeBySlackID:    &orgdatacore.Employee{Email: "missing-uid@example.com"},
			expectResponsePhrase: "valid employee identity",
		},
		{
			name:                 "employee with no email is rejected",
			employeeBySlackID:    &orgdatacore.Employee{UID: "missing-email"},
			expectResponsePhrase: "valid employee identity",
		},
		{
			name:                 "employee with invalid email is rejected",
			employeeBySlackID:    &orgdatacore.Employee{UID: "invalid-email", Email: "not-an-email"},
			expectResponsePhrase: "valid employee identity",
		},
		{
			name:                 "unresolved employee is rejected",
			profileEmail:         "unknown@example.com",
			expectResponsePhrase: "valid employee identity",
		},
		{
			name:                 "Slack profile lookup failure cannot provide fallback",
			getUserInfoError:     fmt.Errorf("Slack unavailable"),
			expectResponsePhrase: "valid employee identity",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var launched *manager.JobRequest
			orgData := &mockOrgDataService{
				getEmployeeBySlackIDFunc: func(string) *orgdatacore.Employee {
					return test.employeeBySlackID
				},
				getEmployeeByEmailFunc: func(string) *orgdatacore.Employee {
					return test.employeeByEmail
				},
				getUserMembershipsFunc: func(uid string) []orgdatacore.MembershipInfo {
					if test.employeeBySlackID != nil && uid != test.employeeBySlackID.UID && test.employeeByEmail != nil && uid != test.employeeByEmail.UID {
						t.Errorf("GetUserMemberships UID = %q", uid)
					}
					return test.memberships
				},
			}
			jobManager := &aroHcpTestJobManager{
				mockJobManager: mockJobManager{
					getOrgDataServiceFunc: func() manager.OrgDataService { return orgData },
				},
				launchJob: func(req *manager.JobRequest) { launched = req },
			}
			client := &mockSlackClient{
				getUserInfoFunc: func(userID string) (*slack.User, error) {
					if test.getUserInfoError != nil {
						return nil, test.getUserInfoError
					}
					return &slack.User{ID: userID, Profile: slack.UserProfile{Email: test.profileEmail}}, nil
				},
			}

			result := AroHcpCreate(client, jobManager, &slackevents.MessageEvent{
				User:    "U123",
				Channel: "C123",
				Text:    "aro-hcp create Azure/ARO-HCP#123",
			}, parser.NewProperties(map[string]string{
				"image_or_version_or_prs": "Azure/ARO-HCP#123",
			}))

			if (launched != nil) != test.expectLaunch {
				t.Fatalf("launch request present = %t, want %t (response: %s)", launched != nil, test.expectLaunch, result)
			}
			if launched != nil {
				if launched.UserName != test.expectUserName || launched.UserEmail != test.expectUserEmail {
					t.Fatalf("request identity = (%q, %q), want (%q, %q)", launched.UserName, launched.UserEmail, test.expectUserName, test.expectUserEmail)
				}
				if _, ok := launched.JobParams["REQUESTER_EMAIL"]; ok {
					t.Fatal("REQUESTER_EMAIL must not be added to JobParams")
				}
			} else if test.expectResponsePhrase != "" && !strings.Contains(result, test.expectResponsePhrase) {
				t.Fatalf("response = %q, want it to contain %q", result, test.expectResponsePhrase)
			}
		})
	}
}

func TestAroHcpCreateOrgDataUnavailableDoesNotLaunch(t *testing.T) {
	var launched bool
	jobManager := &aroHcpTestJobManager{
		mockJobManager: mockJobManager{
			getOrgDataServiceFunc: func() manager.OrgDataService { return nil },
		},
		launchJob: func(*manager.JobRequest) { launched = true },
	}
	client := &mockSlackClient{getUserInfoFunc: func(string) (*slack.User, error) {
		return &slack.User{Profile: slack.UserProfile{Email: "user@example.com"}}, nil
	}}

	result := AroHcpCreate(client, jobManager, &slackevents.MessageEvent{User: "U123"}, parser.NewProperties(map[string]string{
		"image_or_version_or_prs": "Azure/ARO-HCP#123",
	}))
	if launched {
		t.Fatal("ARO-HCP request launched without organizational data")
	}
	if !strings.Contains(result, "organizational data") {
		t.Fatalf("response = %q, want organizational-data error", result)
	}
}
