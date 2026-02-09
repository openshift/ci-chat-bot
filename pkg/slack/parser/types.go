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

	// UploadFileV2 uploads a file to Slack
	UploadFileV2(params slack.UploadFileV2Parameters) (*slack.FileSummary, error)
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
	Description string
	Example     string
	Handler     func(client SlackClient, manager manager.JobManager, event *slackevents.MessageEvent, properties *Properties) string
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

// botCommand structure Contains the bots' command, description and handler
type botCommand struct {
	usage      string
	definition *CommandDefinition
	command    *Command
	private    bool
}
