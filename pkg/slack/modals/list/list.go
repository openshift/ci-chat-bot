package list

import (
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

const (
	identifier       = "list"
	filterByVersion  = "filter_by_version"
	filterByPlatform = "filter_by_platform"
	filterByUser     = "filter_by_user"
	title            = "List Running Clusters"
)

func Register(client *slack.Client, jobmanager manager.JobManager) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(identifier, View()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: process(client, jobmanager),
	})
}

func process(updater *slack.Client, jobmanager manager.JobManager) interactions.Handler {
	return interactions.HandlerFunc(identifier, func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		go func() {
			inputs := modals.CallBackInputAll(callback)
			var filters manager.ListFilters
			for key, input := range inputs {
				switch key {
				case filterByPlatform:
					filters.Platform = input
				case filterByVersion:
					filters.Version = input
				case filterByUser:
					filters.Requestor = input
				}
			}
			_, beginning, elements := jobmanager.ListJobs(callback.User.ID, filters)

			submission := slack.ModalViewRequest{
				Type:  slack.VTModal,
				Title: &slack.TextBlockObject{Type: slack.PlainTextType, Text: title},
				Close: &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Close"},
				Blocks: slack.Blocks{BlockSet: []slack.Block{
					slack.NewRichTextBlock("beginning", slack.NewRichTextSection(slack.NewRichTextSectionTextElement(beginning, &slack.RichTextSectionTextStyle{}))),
				}},
			}
			for _, element := range elements {
				submission.Blocks.BlockSet = append(submission.Blocks.BlockSet, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, element, false, false), nil, nil))
			}
			modals.OverwriteView(updater, submission, callback, logger)
		}()
		return modals.SubmitPrepare(title, identifier, logger)
	})
}
