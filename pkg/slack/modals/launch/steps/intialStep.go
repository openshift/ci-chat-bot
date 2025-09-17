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

func RegisterFirstStep(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(launch.IdentifierInitialView, launch.FirstStepView()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextRegisterFirstStep(client, jobmanager, httpclient),
	})
}

func processNextRegisterFirstStep(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(string(launch.IdentifierInitialView), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		go func() {
			callbackData := modals.CallbackData{
				Input: modals.CallBackInputAll(callback),
			}
			if callbackData.Input[launch.LaunchPlatform] == "" {
				callbackData.Input[launch.LaunchPlatform] = launch.DefaultPlatform
			}
			if callbackData.Input[launch.LaunchArchitecture] == "" {
				// TODO: handle more inteligently in the future or maybe default to multi
				if callbackData.Input[launch.LaunchPlatform] == "hypershift-hosted" {
					callbackData.Input[launch.LaunchArchitecture] = "multi"
				} else {
					callbackData.Input[launch.LaunchArchitecture] = launch.DefaultArchitecture
				}
			}
			modals.OverwriteView(updater, launch.SelectModeView(callback, jobmanager, callbackData), callback, logger)
		}()
		return modals.SubmitPrepare(launch.ModalTitle, string(launch.IdentifierInitialView), logger)
	})
}
