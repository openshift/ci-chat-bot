package steps

import (
	"net/http"
	"strings"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/mce/create"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"k8s.io/klog"
)

func RegisterSelectVersion(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(create.IdentifierSelectVersion, create.SelectVersionView(nil, jobmanager, httpclient, modals.CallbackData{})).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextSelectVersion(client, jobmanager, httpclient),
	})
}

func processNextSelectVersion(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(string(create.IdentifierSelectVersion), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		klog.Infof("Private Metadata: %s", callback.View.PrivateMetadata)
		submissionData := modals.MergeCallbackData(callback)
		mode := submissionData.MultipleSelection[create.LaunchMode]
		createWithPR := false
		for _, key := range mode {
			if strings.TrimSpace(key) == create.LaunchModePRKey {
				createWithPR = true
			}
		}
		go func() {
			if createWithPR {
				modals.OverwriteView(updater, create.PRInputView(callback, submissionData), callback, logger)
			} else {
				modals.OverwriteView(updater, create.ThirdStepView(callback, jobmanager, httpclient, submissionData), callback, logger)
			}
		}()
		return modals.SubmitPrepare(create.ModalTitle, string(create.IdentifierSelectVersion), logger)
	})
}
