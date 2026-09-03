package slack

import (
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

	employee := orgDataService.GetEmployeeBySlackID(slackID)
	if employee == nil && email != "" {
		employee = orgDataService.GetEmployeeByEmail(email)
	}
	if employee == nil || employee.UID == "" {
		return chatmetrics.MembershipUnknown
	}

	if orgDataService.IsEmployeeInOrg(employee.UID, org) {
		return chatmetrics.MembershipMember
	}
	return chatmetrics.MembershipNonMember
}
