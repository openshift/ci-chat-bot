package steps

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/launch"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

func RegisterLaunchOptionsStep(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(launch.Identifier3rdStep, launch.SubmissionView("")).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processLaunchOptionsStep(client, jobmanager, httpclient),
	})
}

func processLaunchOptionsStep(updater *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(string(launch.Identifier3rdStep), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		var launchInputs []string
		data := modals.MergeCallbackData(callback)
		platform := data.Input[launch.LaunchPlatform]
		architecture := data.Input[launch.LaunchArchitecture]
		version, ok := data.Input[launch.LaunchFromLatestBuild]
		if !ok {
			version, ok = data.Input[launch.LaunchFromMajorMinor]
			if !ok {
				version, ok = data.Input[launch.LaunchFromStream]
				if !ok {
					version, ok = data.Input[launch.LaunchFromReleaseController]
					if !ok {
						version, ok = data.Input[launch.LaunchFromCustom]
						if !ok {
							_, version, _, _ = jobmanager.ResolveImageOrVersion("nightly", "", architecture)
						}
					}
				}
			}
		}
		launchInputs = append(launchInputs, version)
		prs, ok := data.Input[launch.LaunchFromPR]
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
		conversation, _, _, err := updater.OpenConversation(&slack.OpenConversationParameters{Users: []string{callback.User.ID}})
		if err != nil {
			logger.Errorf("Failed to get user message channel: %v", err)
		}
		if conversation != nil {
			job.Channel = conversation.ID
		}
		errorResponse := validateLaunchOptionsStepSubmission(jobmanager, job)
		if errorResponse != nil {
			return errorResponse, nil
		}
		go func() {
			msg, err := jobmanager.LaunchJobForUser(job)
			if err != nil {
				modals.OverwriteView(updater, launch.SubmissionView(err.Error()), callback, logger)
			} else {
				modals.OverwriteView(updater, launch.SubmissionView(msg), callback, logger)
			}
		}()
		return modals.SubmitPrepare(launch.ModalTitle, string(launch.Identifier3rdStep), logger)
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
