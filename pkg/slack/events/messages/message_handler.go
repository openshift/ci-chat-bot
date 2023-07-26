package messages

import (
	"fmt"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/events"
	"github.com/openshift/ci-chat-bot/pkg/slack/parser"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
	"strings"
	"time"
)

func Handle(client *slack.Client, manager manager.JobManager, botCommands []parser.BotCommand) events.PartialHandler {
	return events.PartialHandlerFunc("direct-message",
		func(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (handled bool, err error) {
			if callback.Type != slackevents.CallbackEvent {
				return true, nil
			}
			event, ok := callback.InnerEvent.Data.(*slackevents.MessageEvent)
			if !ok {
				return false, fmt.Errorf("failed to parse the slack event")
			}
			if strings.TrimSpace(event.Text) == "help" {
				help(client, event, botCommands)
				return true, nil
			}
			// do not respond to bots
			if event.BotID != "" {
				return true, nil
			}
			// do not respond to indirect messages
			if !strings.HasPrefix(event.Channel, "D") {
				_, _, err := client.PostMessage(event.Channel, slack.MsgOptionText("this command is only accepted via direct message)", false))
				if err != nil {
					return false, err
				}
				return true, nil
			}
			// do not respond if the event SubType is message_changed or file_share( in cases a link is posted and a preview is
			// added afterwards and when an attachment is included)
			if event.SubType == "message_changed" || event.SubType == "file_share" {
				return true, nil
			}
			for _, command := range botCommands {
				properties, match := command.Match(event.Text)
				if match {
					response := command.Execute(client, manager, event, properties)
					if err := postResponse(client, event, response); err != nil {
						return false, fmt.Errorf("failed all attempts to post the response to the requested action: %s", event.Text)
					}
					return true, nil
				}
			}
			if err := postResponse(client, event, "unrecognized command, msg me `help` for a list of all commands"); err != nil {
				return false, fmt.Errorf("failed all attempts to post the response to the requested action: %s", event.Text)
			}
			return true, nil
		})
}

func postResponse(client *slack.Client, event *slackevents.MessageEvent, response string) error {
	var lastErr error
	err := wait.PollImmediate(5*time.Second, 20*time.Second, func() (bool, error) {
		_, responseTimestamp, err := client.PostMessage(event.Channel, slack.MsgOptionText(response, false))
		if err != nil {
			lastErr = err
			return false, nil
		}
		klog.Infof("Posted response to UserID: %s (event: `%s`) at %s", event.User, event.Text, responseTimestamp)
		return true, nil
	})
	if err != nil {
		klog.Errorf("Failed to post response to UserID: %s; (event: `%s`) at %d; %v", event.User, event.Text, (time.Now()).Unix(), err)
		return lastErr
	}
	return nil
}

func help(client *slack.Client, event *slackevents.MessageEvent, botCommands []parser.BotCommand) {
	helpMessage := " "
	helpMessage += "help" + " - " + fmt.Sprintf("_%s_", "help") + "\n"
	for _, command := range botCommands {
		tokens := command.Tokenize()
		for _, token := range tokens {
			if token.IsParameter() {
				helpMessage += fmt.Sprintf("`%s`", token.Word) + " "
			} else {
				helpMessage += fmt.Sprintf("`%s`", token.Word) + " "
			}
		}
		if len(command.Definition().Description) > 0 {
			helpMessage += "\n\t" + fmt.Sprintf("_%s_", command.Definition().Description)
		}
		helpMessage += "\n"
		if len(command.Definition().Example) > 0 {
			helpMessage += fmt.Sprintf(">_*Example:* %s_", command.Definition().Example) + "\n"
		}
	}
	// Adding pointer to our FAQ...
	helpMessage += "Please check out our <https://github.com/openshift/ci-chat-bot/blob/master/docs/FAQ.md|Frequently Asked Questions> for more information.\n"
	helpMessage += "You can also reach out to us in <https://redhat-internal.slack.com/archives/CNHC2DK2M|#forum-ocp-crt> for more information.\n"
	_, _, err := client.PostMessage(event.Channel, slack.MsgOptionText(helpMessage, false))
	if err != nil {
		klog.Warningf("Failed to post the help message")
	}
}
