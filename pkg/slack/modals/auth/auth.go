package auth

import (
	"github.com/openshift/ci-chat-bot/pkg/manager"
	localslack "github.com/openshift/ci-chat-bot/pkg/slack"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	slackClient "github.com/slack-go/slack"
)

const identifier = "auth"
const title = "Authentication"

func Register(client *slack.Client, jobmanager manager.JobManager) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(identifier, View()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: process(client, jobmanager),
	})
}

func process(updater *slack.Client, jobManager manager.JobManager) interactions.Handler {
	return interactions.HandlerFunc(identifier, func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		go func() {
			job, err := jobManager.GetLaunchJob(callback.User.ID)
			if err != nil {
				modals.OverwriteView(updater, modals.ErrorView("getting launch job", err), callback, logger)
				return
			}
			msg, kubeconfig := localslack.NotifyJob(updater, job, false)
			submission := modals.SubmissionView(title, msg)
			// add kubeconfig block if exists
			if kubeconfig != "" {
				submission.Blocks.BlockSet = append(submission.Blocks.BlockSet,
					slackClient.NewDividerBlock(),
					slackClient.NewHeaderBlock(slackClient.NewTextBlockObject(slackClient.PlainTextType, "KubeConfig File (to download the kubeconfig as a file, send `auth` in the message tab):", true, false)),
					slackClient.NewRichTextBlock("submission", &slackClient.RichTextPreformatted{
						RichTextSection: slackClient.RichTextSection{
							Type: slackClient.RTEPreformatted,
							Elements: []slackClient.RichTextSectionElement{
								slackClient.NewRichTextSectionTextElement(kubeconfig, &slackClient.RichTextSectionTextStyle{Code: false}),
							},
						},
					}))
			}
			modals.OverwriteView(updater, submission, callback, logger)
		}()
		return modals.SubmitPrepare(title, identifier, logger)
	})
}
