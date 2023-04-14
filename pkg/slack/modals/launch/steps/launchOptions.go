package steps

import (
	"encoding/json"
	"fmt"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/launch"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"net/http"
	"strings"
)

func RegisterLaunchOptionsStep(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(launch.Identifier3rdStep, launch.SubmissionView("")).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processLaunchOptionsStep(client, jobmanager, httpclient),
	})
}

func processLaunchOptionsStep(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(string(launch.Identifier3rdStep), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		var launchInputs []string
		data := launch.CallbackData{
			Input:             modals.CallBackInputAll(callback),
			MultipleSelection: modals.CallbackMultipleSelect(callback),
			Context:           callbackContext(callback),
		}
		platform := data.Context[launch.LaunchPlatform]
		architecture := data.Context[launch.LaunchArchitecture]
		version := data.Context[launch.LaunchVersion]
		launchInputs = append(launchInputs, version)
		prs, ok := data.Context[launch.LaunchFromPR]
		if ok && prs != "none" {
			prSlice := strings.Split(prs, ",")
			for _, pr := range prSlice {
				launchInputs = append(launchInputs, strings.TrimSpace(pr))
			}
		}
		parameters := data.MultipleSelection[launch.LaunchParameters]
		parametersMap := make(map[string]string)
		for _, parameter := range parameters {
			parametersMap[parameter] = ""
		}
		launchCommand := fmt.Sprintf("launch %s %s,%s,%s ", version, architecture, platform, strings.Join(parameters[:], ","))
		job := &manager.JobRequest{
			OriginalMessage: launchCommand,
			User:            callback.User.ID,
			UserName:        callback.User.Name,
			Inputs:          [][]string{launchInputs},
			Type:            manager.JobTypeInstall,
			Platform:        platform,
			Channel:         callback.User.ID,
			JobParams:       parametersMap,
			Architecture:    architecture,
		}
		errorResponse := validateLaunchOptionsStepSubmission(jobmanager, job)
		if errorResponse != nil {
			return errorResponse, nil
		}
		go func() {
			msg, err := jobmanager.LaunchJobForUser(job)
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
			if err != nil {
				overwriteView(launch.SubmissionView(err.Error()))
			} else {
				overwriteView(launch.SubmissionView(msg))
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

func validateLaunchOptionsStepSubmission(jobManager manager.JobManager, job *manager.JobRequest) []byte {
	err := jobManager.CheckValidJobConfiguration(job)
	errors := make(map[string]string, 0)
	if err != nil {
		errors[launch.LaunchParameters] = err.Error()
	} else {
		return nil
	}
	response, err := modals.ValidationError(errors)
	if err == nil {
		return response
	}
	return nil
}
