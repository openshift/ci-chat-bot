package steps

import (
	"encoding/json"
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
	return modals.ForView(launch.IdentifierSelectVersion, launch.SelectVersionView(nil, jobmanager, httpclient, launch.CallbackData{})).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextSelectVersion(client, jobmanager, httpclient),
	})
}

func processNextSelectVersion(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc("launch3", func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		submissionData := launch.CallbackData{
			Input:             modals.CallBackInputAll(callback),
			Context:           callbackContext(callback),
			MultipleSelection: modals.CallbackMultipleSelect(callback),
		}
		m := submissionData.Context[launch.LaunchMode]
		launchModeSplit := strings.Split(m, ",")
		launchWithPR := false
		for _, key := range launchModeSplit {
			if strings.TrimSpace(key) == launch.LaunchModePR {
				launchWithPR = true
			}
		}
		go func() {
			overwriteView := func(view slack.ModalViewRequest) {
				// don't pass a hash, so we overwrite the View always
				response, err := updater.UpdateView(view, "", "", callback.View.ID)
				if err != nil {
					logger.WithError(err).Warn("Failed to update a modal View.")
					_, err := updater.UpdateView(launch.ErrorView(err.Error()), "", "", callback.View.ID)
					if err != nil {
						logger.WithError(err).Warn("Failed to update a modal View.")
					}
				}
				logger.WithField("response", response).Trace("Got a modal response.")
			}
			if launchWithPR {
				overwriteView(launch.PRInputView(callback, submissionData))
			} else {
				overwriteView(launch.ThirdStepView(callback, jobmanager, httpclient, submissionData))
			}
		}()
		response, err := json.Marshal(&slack.ViewSubmissionResponse{
			ResponseAction: slack.RAUpdate,
			View:           launch.PrepareNextStepView(),
		})
		if err != nil {
			logger.WithError(err).Error("Failed to marshal FirstStepView update submission response.")
			return nil, err
		}
		return response, nil
	})
}
