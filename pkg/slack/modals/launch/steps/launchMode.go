package steps

import (
	"net/http"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/launch"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"k8s.io/apimachinery/pkg/util/sets"
)

func RegisterLaunchModeStep(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(launch.IdentifierRegisterLaunchMode, launch.ThirdStepView(nil, jobmanager, httpclient, modals.CallbackData{})).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextLaunchModeStep(client, jobmanager, httpclient),
	})
}

func processNextLaunchModeStep(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(string(launch.IdentifierRegisterLaunchMode), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		submissionData := modals.MergeCallbackData(callback)
		mode := sets.New[string]()
		for _, selection := range submissionData.MultipleSelection[launch.LaunchMode] {
			switch selection {
			case launch.LaunchModePRKey:
				mode.Insert(launch.LaunchModePR)
			case launch.LaunchModeVersionKey:
				mode.Insert(launch.LaunchModeVersion)
			}
		}
		go func() {
			if mode.Has(launch.LaunchModeVersion) {
				modals.OverwriteView(updater, launch.FilterVersionView(callback, jobmanager, submissionData, httpclient, mode), callback, logger)
			} else {
				modals.OverwriteView(updater, launch.PRInputView(callback, submissionData), callback, logger)
			}

		}()
		return modals.SubmitPrepare(launch.ModalTitle, string(launch.IdentifierRegisterLaunchMode), logger)
	})
}
