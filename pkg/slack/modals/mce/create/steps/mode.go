package steps

import (
	"net/http"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/mce/create"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog"
)

func RegisterLaunchModeStep(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(create.IdentifierSelectModeView, create.SelectModeView(nil, jobmanager, modals.CallbackData{})).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextLaunchModeStep(client, jobmanager, httpclient),
	})
}

func processNextLaunchModeStep(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(string(create.IdentifierSelectModeView), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		klog.Infof("Private Metadata: %s", callback.View.PrivateMetadata)
		submissionData := modals.MergeCallbackData(callback)
		mode := sets.New[string]()
		for _, selection := range submissionData.MultipleSelection[modals.LaunchMode] {
			switch selection {
			case modals.LaunchModePRKey:
				mode.Insert(modals.LaunchModePR)
			case modals.LaunchModeVersionKey:
				mode.Insert(modals.LaunchModeVersion)
			}
		}
		go func() {
			if mode.Has(modals.LaunchModeVersion) {
				modals.OverwriteView(updater, create.FilterVersionView(callback, jobmanager, submissionData, httpclient, mode, false), callback, logger)
			} else {
				modals.OverwriteView(updater, create.PRInputView(callback, submissionData, string(create.IdentifierSelectModeView)), callback, logger)
			}

		}()
		return modals.SubmitPrepare(create.ModalTitle, string(create.IdentifierSelectModeView), logger)
	})
}
