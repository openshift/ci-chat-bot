package steps

import (
	"net/http"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/mce/create"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

func RegisterFirstStep(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(create.IdentifierInitialView, create.FirstStepView()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextRegisterFirstStep(client, jobmanager, httpclient),
	})
}

func processNextRegisterFirstStep(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(string(create.IdentifierInitialView), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		go func() {
			callbackData := modals.CallbackData{
				Input: modals.CallBackInputAll(callback),
			}
			modals.OverwriteView(updater, create.SelectModeView(callback, jobmanager, callbackData), callback, logger)
		}()
		return modals.SubmitPrepare(create.ModalTitle, string(create.IdentifierInitialView), logger)
	})
}
