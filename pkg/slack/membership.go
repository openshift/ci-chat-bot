package slack

import (
	orgdatacore "github.com/openshift-eng/cyborg-data/go"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	chatmetrics "github.com/openshift/ci-chat-bot/pkg/metrics"
)

// HybridPlatformsOrganization is the organization used by GCP access
// authorization and usage classification.
const HybridPlatformsOrganization = "Hybrid Platforms"

// ClassifyUserMembership resolves a Slack user to an employee before
// checking organization membership. A missing employee is unknown, not a
// confirmed non-member.
func ClassifyUserMembership(orgDataService manager.OrgDataService, slackID, email, org string) chatmetrics.Membership {
	if orgDataService == nil {
		return chatmetrics.MembershipUnknown
	}

	employee := employeeForSlackUser(orgDataService, slackID, email)
	if employee == nil || employee.UID == "" {
		return chatmetrics.MembershipUnknown
	}

	if orgDataService.IsEmployeeInOrg(employee.UID, org) {
		return chatmetrics.MembershipMember
	}
	return chatmetrics.MembershipNonMember
}

// employeeForSlackUser resolves a Slack user to organizational data, preferring
// the Slack ID and falling back to the email from the Slack profile.
func employeeForSlackUser(orgDataService manager.OrgDataService, slackID, email string) *orgdatacore.Employee {
	if orgDataService == nil {
		return nil
	}

	employee := orgDataService.GetEmployeeBySlackID(slackID)
	if employee == nil && email != "" {
		employee = orgDataService.GetEmployeeByEmail(email)
	}
	return employee
}
