package auth

import (
	"net/http"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

const identifier = "mce_lookup"
const title = "Lookup MCE Versions"

func Register(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(identifier, View()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: process(client, jobmanager, httpclient),
	})
}

func process(updater *slack.Client, jobManager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(identifier, func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		go func() {
			modals.OverwriteView(updater, modals.SubmissionView(title, jobManager.ListMceVersions()), callback, logger)
		}()
		return modals.SubmitPrepare(title, identifier, logger)
	})
}
