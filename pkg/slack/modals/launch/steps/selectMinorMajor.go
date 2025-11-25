package steps

import (
	"net/http"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/launch"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

func RegisterSelectMinorMajor(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(launch.IdentifierSelectMinorMajor, launch.SelectMinorMajor(nil, httpclient, modals.CallbackData{}, "")).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextSelectMinorMajor(client, jobmanager, httpclient),
	})
}

func processNextSelectMinorMajor(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(string(launch.IdentifierSelectMinorMajor), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		submissionData := modals.MergeCallbackData(callback)
		go modals.OverwriteView(updater, launch.SelectVersionView(callback, jobmanager, httpclient, submissionData, string(launch.IdentifierSelectMinorMajor)), callback, logger)
		return modals.SubmitPrepare(launch.ModalTitle, string(launch.IdentifierSelectMinorMajor), logger)
	})
}
