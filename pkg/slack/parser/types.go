package parser

import (
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"regexp"
)

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
	Handler     func(client *slack.Client, manager manager.JobManager, event *slackevents.MessageEvent, properties *Properties) string
}

// BotCommand interface
type BotCommand interface {
	Usage() string
	Definition() *CommandDefinition
	Match(text string) (*Properties, bool)
	Tokenize() []*Token
	Execute(client *slack.Client, manager manager.JobManager, event *slackevents.MessageEvent, properties *Properties) string
}

// botCommand structure Contains the bots' command, description and handler
type botCommand struct {
	usage      string
	definition *CommandDefinition
	command    *Command
}
