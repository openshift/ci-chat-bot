package auth

import (
	"net/http"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/common"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

const identifier = "mce_list"
const title = "List Managed Clusters"

func Register(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(identifier, View()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: process(client, jobmanager, httpclient),
	})
}

func process(updater *slack.Client, jobManager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(identifier, func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		go func() {
			_, beginning, elements := jobManager.ListManagedClusters("")
			submission := common.BuildListResultModal(title, beginning, elements)
			modals.OverwriteView(updater, submission, callback, logger)
		}()
		return modals.SubmitPrepare(title, identifier, logger)
	})
}
