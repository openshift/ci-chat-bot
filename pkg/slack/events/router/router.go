package router

import (
	"github.com/openshift/ci-chat-bot/pkg/manager"
	slackCommandParser "github.com/openshift/ci-chat-bot/pkg/slack"
	"github.com/slack-go/slack"

	"github.com/openshift/ci-chat-bot/pkg/slack/events"
	"github.com/openshift/ci-chat-bot/pkg/slack/events/messages"
)

// ForEvents returns a Handler that appropriately routes
// event callbacks for the handlers we know about
func ForEvents(client *slack.Client, manager manager.JobManager, botCommands []slackCommandParser.BotCommand) events.Handler {
	return events.MultiHandler(
		messages.Handle(client, manager, botCommands),
	)
}
