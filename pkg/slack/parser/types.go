package parser

import (
	"regexp"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// SlackClient defines the interface for Slack operations used by action handlers.
// This interface enables testing by allowing mock implementations.
// The concrete *slack.Client type already implements all these methods.
type SlackClient interface {
	// GetUserInfo retrieves user profile information by user ID
	GetUserInfo(userID string) (*slack.User, error)

	// PostMessage posts a message to a Slack channel
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)

	// UploadFile uploads a file to Slack
	UploadFile(params slack.UploadFileParameters) (*slack.FileSummary, error)
}

type Command struct {
	tokens      []*Token
	expressions []*regexp.Regexp
}

type Token struct {
	Word string
	Type int
}

// CommandDefinition structure contains definition of the bot command
type CommandDefinition struct {
	Description       string
	Example           string
	Handler           func(client SlackClient, manager manager.JobManager, event *slackevents.MessageEvent, properties *Properties) string
	ContextualHandler func(client SlackClient, manager manager.JobManager, event *slackevents.MessageEvent, properties *Properties, context *CommandExecutionContext) string
}

// GCPAccessOutcomeRecorder records the outcome of a GCP access request. It is
// kept as a small interface so command parsing does not depend on a concrete
// metrics implementation.
type GCPAccessOutcomeRecorder interface {
	RecordGCPAccessOutcome(slackUserID, membership, outcome string)
}

// CommandExecutionContext carries optional dependencies for command handlers
// that need instrumentation in addition to the central command recorder.
type CommandExecutionContext struct {
	Membership               string
	GCPAccessOutcomeRecorder GCPAccessOutcomeRecorder
}

// BotCommand interface
type BotCommand interface {
	Usage() string
	Definition() *CommandDefinition
	Match(text string) (*Properties, bool)
	Tokenize() []*Token
	Execute(client SlackClient, manager manager.JobManager, event *slackevents.MessageEvent, properties *Properties) string
	IsPrivate() bool
}

// ContextualBotCommand is implemented by commands that can receive optional
// execution dependencies while retaining the standard BotCommand API.
type ContextualBotCommand interface {
	BotCommand
	ExecuteWithContext(client SlackClient, manager manager.JobManager, event *slackevents.MessageEvent, properties *Properties, context *CommandExecutionContext) string
}

// botCommand structure Contains the bots' command, description and handler
type botCommand struct {
	usage      string
	definition *CommandDefinition
	command    *Command
	private    bool
}
