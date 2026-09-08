package slack

import (
	"testing"

	orgdatacore "github.com/openshift-eng/cyborg-data/go"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	chatmetrics "github.com/openshift/ci-chat-bot/pkg/metrics"
)

type membershipOrgDataService struct {
	employeeBySlackID *orgdatacore.Employee
	employeeByEmail   *orgdatacore.Employee
	employeeInOrg     bool
	emailLookups      int
	employeeLookups   int
}

func (m *membershipOrgDataService) GetEmployeeBySlackID(string) *orgdatacore.Employee {
	m.employeeLookups++
	return m.employeeBySlackID
}

func (m *membershipOrgDataService) IsSlackUserInOrg(string, string) bool {
	return false
}

func (m *membershipOrgDataService) GetEmployeeByEmail(string) *orgdatacore.Employee {
	m.emailLookups++
	return m.employeeByEmail
}

func (m *membershipOrgDataService) IsEmployeeInOrg(string, string) bool {
	return m.employeeInOrg
}

var _ manager.OrgDataService = (*membershipOrgDataService)(nil)

func TestClassifyUserMembership(t *testing.T) {
	tests := []struct {
		name               string
		service            manager.OrgDataService
		expected           chatmetrics.Membership
		serviceUnavailable bool
		employeeBySlackID  *orgdatacore.Employee
		employeeByEmail    *orgdatacore.Employee
		employeeInOrg      bool
	}{
		{
			name:              "Slack ID resolves member",
			employeeBySlackID: &orgdatacore.Employee{UID: "member", Email: "member@example.com"},
			employeeInOrg:     true,
			expected:          chatmetrics.MembershipMember,
		},
		{
			name:              "Slack ID resolves confirmed non-member",
			employeeBySlackID: &orgdatacore.Employee{UID: "non-member", Email: "person@example.com"},
			employeeInOrg:     false,
			expected:          chatmetrics.MembershipNonMember,
		},
		{
			name:            "email fallback resolves member",
			employeeByEmail: &orgdatacore.Employee{UID: "member", Email: "member@example.com"},
			employeeInOrg:   true,
			expected:        chatmetrics.MembershipMember,
		},
		{
			name:            "email fallback resolves non-member",
			employeeByEmail: &orgdatacore.Employee{UID: "non-member", Email: "person@example.com"},
			employeeInOrg:   false,
			expected:        chatmetrics.MembershipNonMember,
		},
		{
			name:     "employee absent is unknown",
			expected: chatmetrics.MembershipUnknown,
		},
		{
			name:               "service unavailable is unknown",
			serviceUnavailable: true,
			expected:           chatmetrics.MembershipUnknown,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := test.service
			if service == nil && !test.serviceUnavailable {
				service = &membershipOrgDataService{
					employeeBySlackID: test.employeeBySlackID,
					employeeByEmail:   test.employeeByEmail,
					employeeInOrg:     test.employeeInOrg,
				}
			}

			if got := ClassifyUserMembership(service, "U12345", "person@example.com", HybridPlatformsOrganization); got != test.expected {
				t.Fatalf("ClassifyUserMembership() = %q, want %q", got, test.expected)
			}
		})
	}
}

func TestClassifyUserMembershipPrefersSlackID(t *testing.T) {
	service := &membershipOrgDataService{
		employeeBySlackID: &orgdatacore.Employee{UID: "slack-id-employee"},
		employeeByEmail:   &orgdatacore.Employee{UID: "email-employee"},
		employeeInOrg:     true,
	}

	if got := ClassifyUserMembership(service, "U12345", "person@example.com", HybridPlatformsOrganization); got != chatmetrics.MembershipMember {
		t.Fatalf("ClassifyUserMembership() = %q, want %q", got, chatmetrics.MembershipMember)
	}
	if service.employeeLookups != 1 {
		t.Fatalf("Slack ID lookup count = %d, want 1", service.employeeLookups)
	}
	if service.emailLookups != 0 {
		t.Fatalf("email lookup count = %d, want 0", service.emailLookups)
	}
}
