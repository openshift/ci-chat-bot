package router

import (
	"github.com/openshift/ci-chat-bot/pkg/manager"
	chatmetrics "github.com/openshift/ci-chat-bot/pkg/metrics"
	"github.com/openshift/ci-chat-bot/pkg/slack/events/apphome"
	"github.com/openshift/ci-chat-bot/pkg/slack/mention"
	slackCommandParser "github.com/openshift/ci-chat-bot/pkg/slack/parser"
	"github.com/slack-go/slack"

	"github.com/openshift/ci-chat-bot/pkg/slack/events"
	"github.com/openshift/ci-chat-bot/pkg/slack/events/messages"
)

// ForEvents returns a Handler that appropriately routes
// event callbacks for the handlers we know about
func ForEvents(client *slack.Client, manager manager.JobManager, botCommands []slackCommandParser.BotCommand, recorders ...chatmetrics.CommandRecorder) events.Handler {
	return events.MultiHandler(
		messages.Handle(client, manager, botCommands, recorders...),
		mention.Handler(client),
		apphome.Handler(client, manager),
	)
}
