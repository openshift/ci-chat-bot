package slack

import (
	"fmt"
	"strings"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/orgdata"
	"github.com/openshift/ci-chat-bot/pkg/slack/parser"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// CommandHandler is the signature for slack command handlers
type CommandHandler func(*slack.Client, manager.JobManager, *slackevents.MessageEvent, *parser.Properties) string

// AuthorizedCommandHandler wraps a command handler with authorization checking
func AuthorizedCommandHandler(command string, authService *orgdata.AuthorizationService, handler CommandHandler) CommandHandler {
	return func(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
		if authService == nil {
			// No authorization service configured, allow all access
			return handler(client, jobManager, event, properties)
		}

		// Check if user is authorized
		authorized, denyMessage := authService.CheckAuthorization(event.User, command)
		if !authorized {
			return denyMessage
		}

		// User is authorized, proceed with the original handler
		return handler(client, jobManager, event, properties)
	}
}

// GetUserInfo returns comprehensive user information for debugging/troubleshooting access
func GetUserInfo(client *slack.Client, authService *orgdata.AuthorizationService, event *slackevents.MessageEvent, properties *parser.Properties) string {
	if authService == nil {
		return "❌ Authorization service not configured - all commands are unrestricted"
	}

	userInfo := authService.GetUserInfo(event.User)
	userName := GetUserName(client, event.User)

	var response strings.Builder
	response.WriteString("🔍 *Your Account Information*\n\n")

	// Basic Slack information
	response.WriteString("*Slack Details:*\n")
	response.WriteString(fmt.Sprintf("• *Name*: %s\n", userName))
	response.WriteString(fmt.Sprintf("• *Slack ID*: `%s`\n\n", event.User))

	if !userInfo.HasOrgData {
		response.WriteString("⚠️ *No organizational data found*\n")
		response.WriteString("You may not be in the organizational directory, or the org data service may not be loaded.\n")
		response.WriteString("This means you'll only have access to commands with `allow_all: true` in the authorization config.\n")
		return response.String()
	}

	// Employee information from org data
	emp := userInfo.Employee
	response.WriteString("*Employee Information:*\n")
	response.WriteString(fmt.Sprintf("• *Employee UID*: `%s`\n", emp.UID))
	if emp.DisplayName != "" {
		response.WriteString(fmt.Sprintf("• *Display Name*: %s\n", emp.DisplayName))
	}
	if emp.Email != "" {
		response.WriteString(fmt.Sprintf("• *Email*: %s\n", emp.Email))
	}
	if emp.JobTitle != "" {
		response.WriteString(fmt.Sprintf("• *Job Title*: %s\n", emp.JobTitle))
	}
	response.WriteString("\n")

	// Organizational memberships (consolidated teams and orgs with types)
	response.WriteString("*Organizational Memberships:*\n")
	if len(userInfo.Organizations) > 0 {
		for _, org := range userInfo.Organizations {
			response.WriteString(fmt.Sprintf("• `%s` (%s)\n", org.Name, org.Type))
		}
	} else {
		response.WriteString("• None found\n")
	}
	response.WriteString("\n")

	// Authorization troubleshooting info
	response.WriteString("*Commands You Can Execute:*\n")

	userCommands := authService.GetUserCommands(event.User)
	totalCommands := 0

	// Count total commands for summary
	for _, commands := range userCommands {
		totalCommands += len(commands)
	}

	if totalCommands == 0 {
		response.WriteString("⚠️ No specific commands configured - you have access to all unrestricted commands\n\n")
	} else {
		// Display commands by access type
		if len(userCommands["allow_all"]) > 0 {
			response.WriteString(fmt.Sprintf("🌐 *Available to Everyone:* %s\n", formatCommandList(userCommands["allow_all"])))
		}

		if len(userCommands["by_uid"]) > 0 {
			response.WriteString(fmt.Sprintf("👤 *Your Personal Access:* %s\n", formatCommandList(userCommands["by_uid"])))
		}

		if len(userCommands["by_team"]) > 0 {
			response.WriteString(fmt.Sprintf("👥 *Via Team Membership:* %s\n", formatCommandList(userCommands["by_team"])))
		}

		if len(userCommands["by_org"]) > 0 {
			response.WriteString(fmt.Sprintf("🏢 *Via Organization:* %s\n", formatCommandList(userCommands["by_org"])))
		}

		response.WriteString("\n")
	}

	// Add troubleshooting info
	response.WriteString("*Authorization Logic:*\n")
	response.WriteString("Access is granted if you match any of:\n")
	response.WriteString("• Commands with `allow_all: true` (everyone)\n")
	if emp.UID != "" {
		response.WriteString(fmt.Sprintf("• Commands with your UID `%s` in `allowed_uids`\n", emp.UID))
	}

	// Check if user has teams (for allowed_teams) or orgs (for allowed_orgs)
	hasTeams := len(userInfo.Teams) > 0
	hasOrgs := len(userInfo.Organizations) > 0

	if hasTeams {
		response.WriteString("• Commands with your teams in `allowed_teams`\n")
	}
	if hasOrgs {
		response.WriteString("• Commands with your organizations in `allowed_orgs`\n")
	}

	return response.String()
}

// formatCommandList formats a slice of command names for display
func formatCommandList(commands []string) string {
	if len(commands) == 0 {
		return "None"
	}

	result := ""
	for i, cmd := range commands {
		if i > 0 {
			result += ", "
		}
		result += "`" + cmd + "`"
	}
	return result
}
