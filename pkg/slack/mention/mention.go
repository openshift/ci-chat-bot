package mention

import (
	"fmt"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/helpdesk"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/openshift/ci-chat-bot/pkg/slack/events"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/bug"
)

type messagePoster interface {
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
}

// Handler returns a handler that knows how to respond to
// new messages that mention the robot by showing users
// which interactive workflows they might be interested in,
// based on the phrasing that they used to mention the bot.
func Handler(client messagePoster) events.PartialHandler {
	return events.PartialHandlerFunc("mention", func(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (handled bool, err error) {
		if callback.Type != slackevents.CallbackEvent {
			return false, nil
		}
		event, ok := callback.InnerEvent.Data.(*slackevents.AppMentionEvent)
		if !ok {
			return false, nil
		}
		logger.Info("Handling app mention...")
		timestamp := event.TimeStamp
		if event.ThreadTimeStamp != "" {
			timestamp = event.ThreadTimeStamp
		}
		responseChannel, responseTimestamp, err := client.PostMessage(event.Channel, slack.MsgOptionBlocks(responseFor(event.Text)...), slack.MsgOptionTS(timestamp))
		if err != nil {
			logger.WithError(err).Warn("Failed to post response to app mention")
		} else {
			logger.Infof("Posted response to app mention in channel %s at %s", responseChannel, responseTimestamp)
		}
		return true, err
	})
}

func responseFor(message string) []slack.Block {
	type interaction struct {
		identifier              modals.Identifier
		description, buttonText string
	}
	interactions := []interaction{
		{
			identifier:  bug.Identifier,
			description: "Record a defect on one of the CRT projects, providing a reproducer where possible.",
			buttonText:  "File a Bug",
		},
		{
			identifier:  helpdesk.Identifier,
			description: "Request clarification on a subject managed by CRT",
			buttonText:  "Ask a Question",
		},
	}

	block := func(identifier, description, buttonText string) slack.Block {
		return &slack.SectionBlock{
			Type: slack.MBTSection,
			Text: &slack.TextBlockObject{
				Type: slack.MarkdownType,
				Text: fmt.Sprintf("*%s*\n%s", buttonText, description),
			},
			Accessory: &slack.Accessory{
				ButtonElement: &slack.ButtonBlockElement{
					Type:  slack.METButton,
					Text:  &slack.TextBlockObject{Type: slack.PlainTextType, Text: buttonText},
					Value: identifier,
				},
			},
		}
	}

	var blocks []slack.Block
	for _, interaction := range interactions {
		if strings.Contains(message, string(interaction.identifier)) {
			blocks = append(blocks, &slack.DividerBlock{
				Type: slack.MBTDivider,
			})
			blocks = append(blocks, block(string(interaction.identifier), interaction.description, interaction.buttonText))
		}
	}

	if len(blocks) == 0 {
		blocks = append(blocks, &slack.SectionBlock{
			Type: slack.MBTSection,
			Text: &slack.TextBlockObject{
				Type: slack.PlainTextType,
				Text: "Sorry, I don't know how to help with that. Here are all the things I know how to do:",
			},
		})
		for _, interaction := range interactions {
			blocks = append(blocks, &slack.DividerBlock{
				Type: slack.MBTDivider,
			})
			blocks = append(blocks, block(string(interaction.identifier), interaction.description, interaction.buttonText))
		}
	} else {
		blocks = append([]slack.Block{&slack.SectionBlock{
			Type: slack.MBTSection,
			Text: &slack.TextBlockObject{
				Type: slack.PlainTextType,
				Text: "It looks like you're trying to do one of the following:",
			},
		}}, blocks...)
	}

	return blocks
}
