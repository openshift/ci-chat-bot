package parser

import (
	"regexp"
	"strings"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

const (
	escapeCharacter      = "\\"
	ignoreCase           = "(?iU)"
	parameterPattern     = "<\\S+>"
	lazyParameterPattern = "<\\S+\\?>"
	spacePattern         = "\\s+"
	inputPattern         = "(.+)"
	lazyInputPattern     = "(.+?)"
	preCommandPattern    = "(^)"
	postCommandPattern   = "$"
)

const (
	notParameter = iota
	greedyParameter
	lazyParameter
)

var (
	regexCharacters = []string{"\\", "(", ")", "{", "}", "[", "]", "?", ".", "+", "|", "^", "$"}
)

func (c *botCommand) Execute(client *slack.Client, manager manager.JobManager, event *slackevents.MessageEvent, properties *Properties) string {
	if c.definition == nil || c.definition.Handler == nil {
		return "Failed to execute the command!"
	}
	return c.definition.Handler(client, manager, event, properties)
}

// Match determines whether the bot should respond based on the text received
func (c *botCommand) Match(text string) (*Properties, bool) {
	return c.command.Match(text)
}

// Tokenize returns Command info as tokens
func (c *Command) Tokenize() []*Token {
	return c.tokens
}

// Tokenize returns the command format's tokens
func (c *botCommand) Tokenize() []*Token {
	return c.command.Tokenize()
}

// NewBotCommand creates a new bot command object
func NewBotCommand(usage string, definition *CommandDefinition, isPrivate bool) BotCommand {
	command := NewCommand(usage)
	return &botCommand{
		usage:      usage,
		definition: definition,
		command:    command,
		private:    isPrivate,
	}
}

// Usage returns the command usage
func (c *botCommand) Usage() string {
	return c.usage
}

// Definition Description returns the command description
func (c *botCommand) Definition() *CommandDefinition {
	return c.definition
}

// IsPrivate returns whether the command is a private command
func (c *botCommand) IsPrivate() bool {
	return c.private
}

func NewCommand(format string) *Command {
	tokens := tokenize(format)
	expressions := generate(tokens)
	return &Command{tokens: tokens, expressions: expressions}
}

func tokenize(format string) []*Token {
	parameterRegex := regexp.MustCompile(parameterPattern)
	lazyParameterRegex := regexp.MustCompile(lazyParameterPattern)
	words := strings.Fields(format)
	tokens := make([]*Token, len(words))
	for i, word := range words {
		switch {
		case lazyParameterRegex.MatchString(word):
			tokens[i] = &Token{Word: word[1 : len(word)-2], Type: lazyParameter}
		case parameterRegex.MatchString(word):
			tokens[i] = &Token{Word: word[1 : len(word)-1], Type: greedyParameter}
		default:
			tokens[i] = &Token{Word: word, Type: notParameter}
		}
	}
	return tokens
}

func generate(tokens []*Token) []*regexp.Regexp {
	var regexps []*regexp.Regexp
	if len(tokens) == 0 {
		return regexps
	}

	for index := len(tokens) - 1; index >= -1; index-- {
		regex := compile(create(tokens, index))
		regexps = append(regexps, regex)
	}

	return regexps
}

func (t Token) IsParameter() bool {
	return t.Type != notParameter
}

func create(tokens []*Token, boundary int) []*Token {
	var newTokens []*Token
	for i := 0; i < len(tokens); i++ {
		if !tokens[i].IsParameter() || i <= boundary {
			newTokens = append(newTokens, tokens[i])
		}
	}
	return newTokens
}

func compile(tokens []*Token) *regexp.Regexp {
	if len(tokens) == 0 {
		return nil
	}

	pattern := preCommandPattern + getInputPattern(tokens[0])
	for index := 1; index < len(tokens); index++ {
		currentToken := tokens[index]
		pattern += spacePattern + getInputPattern(currentToken)
	}
	pattern += postCommandPattern

	return regexp.MustCompile(ignoreCase + pattern)
}

func getInputPattern(token *Token) string {
	switch token.Type {
	case lazyParameter:
		return lazyInputPattern
	case greedyParameter:
		return inputPattern
	default:
		return escape(token.Word)
	}
}

func escape(text string) string {
	for _, character := range regexCharacters {
		text = strings.ReplaceAll(text, character, escapeCharacter+character)
	}
	return text
}

// Match takes in the command and the text received, attempts to find the pattern and extract the parameters
func (c *Command) Match(text string) (*Properties, bool) {
	if len(c.expressions) == 0 {
		return nil, false
	}

	for _, expression := range c.expressions {
		matches := expression.FindStringSubmatch(text)
		if len(matches) == 0 {
			continue
		}

		values := matches[2:]

		valueIndex := 0
		parameters := make(map[string]string)
		for i := 0; i < len(c.tokens) && valueIndex < len(values); i++ {
			token := c.tokens[i]
			if !token.IsParameter() {
				continue
			}

			parameters[token.Word] = values[valueIndex]
			valueIndex++
		}
		return NewProperties(parameters), true
	}
	return nil, false
}

// Properties is a string map decorator
type Properties struct {
	PropertyMap map[string]string
}

// NewProperties creates a new Properties object
func NewProperties(m map[string]string) *Properties {
	return &Properties{PropertyMap: m}
}

// StringParam attempts to look up a string value by key. If not found, return the default string value
func (p *Properties) StringParam(key string, defaultValue string) string {
	value, ok := p.PropertyMap[key]
	if !ok {
		return defaultValue
	}
	return value
}
