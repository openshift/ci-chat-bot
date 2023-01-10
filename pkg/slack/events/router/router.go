package router

import (
	"github.com/openshift/ci-chat-bot/pkg/jira"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/events/workflowSubmissionEvents"
	"github.com/openshift/ci-chat-bot/pkg/slack/mention"
	slackCommandParser "github.com/openshift/ci-chat-bot/pkg/slack/parser"
	"github.com/slack-go/slack"

	"github.com/openshift/ci-chat-bot/pkg/slack/events"
	"github.com/openshift/ci-chat-bot/pkg/slack/events/messages"
)

// ForEvents returns a Handler that appropriately routes
// event callbacks for the handlers we know about
func ForEvents(client *slack.Client, manager manager.JobManager, botCommands []slackCommandParser.BotCommand, filer jira.IssueFiler) events.Handler {
	return events.MultiHandler(
		messages.Handle(client, manager, botCommands),
		mention.Handler(client),
		workflowSubmissionEvents.Handler(client, filer),
	)
}
