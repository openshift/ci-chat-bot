package steps

import (
	"net/http"
	"strings"
	"time"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/mce/create"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"k8s.io/klog"
)

func RegisterCreateConfirmStep(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(create.Identifier3rdStep, modals.SubmissionView(create.ModalTitle, "")).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processLaunchOptionsStep(client, jobmanager, httpclient),
	})
}

func processLaunchOptionsStep(updater *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(string(create.Identifier3rdStep), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		klog.Infof("Private Metadata: %s", callback.View.PrivateMetadata)
		var createInputs []string
		data := modals.MergeCallbackData(callback)
		platform := data.Input[modals.LaunchPlatform]
		duration := data.Input[create.CreateDuration]
		parsedDuration, _ := time.ParseDuration(duration)
		version := modals.GetVersion(data, jobmanager)
		createInputs = append(createInputs, version)
		prs, ok := data.Input[modals.LaunchFromPR]
		if ok && prs != "none" {
			prSlice := strings.Split(prs, ",")
			for _, pr := range prSlice {
				createInputs = append(createInputs, strings.TrimSpace(pr))
			}
		}
		go func() {
			// the channel ID is empty for app home messages; identify the user's IM channel
			conversation, _, _, err := updater.OpenConversation(&slack.OpenConversationParameters{Users: []string{callback.User.ID}})
			if err != nil {
				logger.Errorf("Failed to get user message channel: %v", err)
			}
			var channel string
			if conversation != nil {
				channel = conversation.ID
			}
			msg, err := jobmanager.CreateMceCluster(callback.User.ID, channel, platform, [][]string{createInputs}, parsedDuration)
			if err != nil {
				modals.OverwriteView(updater, modals.SubmissionView(create.ModalTitle, err.Error()), callback, logger)
			} else {
				modals.OverwriteView(updater, modals.SubmissionView(create.ModalTitle, msg), callback, logger)
			}
		}()
		return modals.SubmitPrepare(create.ModalTitle, string(create.Identifier3rdStep), logger)
	})
}
