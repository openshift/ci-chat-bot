package steps

import (
	"net/http"
	"strings"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/launch"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

func RegisterSelectVersion(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(launch.IdentifierSelectVersion, launch.SelectVersionView(nil, jobmanager, httpclient, modals.CallbackData{})).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextSelectVersion(client, jobmanager, httpclient),
	})
}

func processNextSelectVersion(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(string(launch.IdentifierSelectVersion), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		submissionData := modals.MergeCallbackData(callback)
		mode := submissionData.MultipleSelection[modals.LaunchMode]
		launchWithPR := false
		for _, key := range mode {
			if strings.TrimSpace(key) == modals.LaunchModePRKey {
				launchWithPR = true
			}
		}
		go func() {
			if launchWithPR {
				modals.OverwriteView(updater, launch.PRInputView(callback, submissionData), callback, logger)
			} else {
				modals.OverwriteView(updater, launch.ThirdStepView(callback, jobmanager, httpclient, submissionData), callback, logger)
			}
		}()
		return modals.SubmitPrepare(launch.ModalTitle, string(launch.IdentifierSelectVersion), logger)
	})
}
