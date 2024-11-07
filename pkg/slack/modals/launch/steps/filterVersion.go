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

func RegisterFilterVersion(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(launch.IdentifierFilterVersionView, launch.FilterVersionView(nil, nil, launch.CallbackData{}, nil, nil)).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextFilterVersion(client, jobmanager, httpclient),
	})
}

func processNextFilterVersion(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc("launch3", func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		submissionData := launch.CallbackData{
			Input:             modals.CallBackInputAll(callback),
			Context:           callbackContext(callback),
			MultipleSelection: modals.CallbackMultipleSelect(callback),
		}
		errorResponse := validateFilterVersion(submissionData)
		if errorResponse != nil {
			return errorResponse, nil
		}
		nightlyOrCi := submissionData.Input[launch.LaunchFromLatestBuild]
		customBuild := submissionData.Input[launch.LaunchFromCustom]
		mode := submissionData.Context[launch.LaunchMode]
		launchModeSplit := strings.Split(mode, ",")
		launchWithPr := false
		for _, key := range launchModeSplit {
			if strings.TrimSpace(key) == launch.LaunchModePR {
				launchWithPr = true
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
			if (nightlyOrCi != "" || customBuild != "") && launchWithPr {
				overwriteView(launch.PRInputView(callback, submissionData))
			} else if (nightlyOrCi != "" || customBuild != "") && !launchWithPr {
				overwriteView(launch.ThirdStepView(callback, jobmanager, httpclient, submissionData))
			} else {
				overwriteView(launch.SelectVersionView(callback, jobmanager, httpclient, submissionData))
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

func checkVariables(vars ...string) bool {
	count := 0
	for _, v := range vars {
		if v != "" {
			count++
		}
	}
	return count <= 1
}

func validateFilterVersion(submissionData launch.CallbackData) []byte {
	errs := make(map[string]string, 0)
	nightlyOrCi := submissionData.Input[launch.LaunchFromLatestBuild]
	if nightlyOrCi != "" {
		errs[launch.LaunchFromLatestBuild] = "Select only one parameter!"
	}
	customBuild := submissionData.Input[launch.LaunchFromCustom]
	if customBuild != "" {
		errs[launch.LaunchFromCustom] = "Select only one parameter!"
	}
	selectedStream := submissionData.Input[launch.LaunchFromStream]
	if selectedStream != "" {
		errs[launch.LaunchFromStream] = "Select only one parameter!"
	}
	if !checkVariables(nightlyOrCi, customBuild, selectedStream) {
		response, err := modals.ValidationError(errs)
		if err == nil {
			return response
		}
	}
	return nil
}
