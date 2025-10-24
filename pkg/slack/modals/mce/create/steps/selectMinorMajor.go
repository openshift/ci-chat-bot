package steps

import (
	"net/http"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/mce/create"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"k8s.io/klog"
)

func RegisterSelectMinorMajor(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(create.IdentifierSelectMinorMajor, create.SelectMinorMajor(nil, httpclient, modals.CallbackData{})).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextSelectMinorMajor(client, jobmanager, httpclient),
	})
}

func processNextSelectMinorMajor(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(string(create.IdentifierSelectMinorMajor), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		klog.Infof("Private Metadata: %s", callback.View.PrivateMetadata)
		submissionData := modals.MergeCallbackData(callback)
		go modals.OverwriteView(updater, create.SelectVersionView(callback, jobmanager, httpclient, submissionData), callback, logger)
		return modals.SubmitPrepare(create.ModalTitle, string(create.IdentifierSelectMinorMajor), logger)
	})
}
