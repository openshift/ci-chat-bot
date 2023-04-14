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

func RegisterFilterVersion(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(launch.IdentifierFilterVersionView, launch.FilterVersionView(nil, nil, launch.CallbackData{}, nil, nil)).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextFilterVersion(client, jobmanager, httpclient),
	})
}

func processNextFilterVersion(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	// TODO - validate the combination. Check the version from the release controller, and if it is empty, retrun an
	// error, like: this combination this and that does not seem right, no version found.

	// TODO - if a stream that has multiple major.minor versions is selected, enforce the selection of major.minor
	// if a major.minor is selected without a stream name, enforce the stream name selection.
	return interactions.HandlerFunc("launch3", func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		submissionData := launch.CallbackData{
			Input:             modals.CallBackInputAll(callback),
			Context:           callbackContext(callback),
			MultipleSelection: modals.CallbackMultipleSelect(callback),
		}
		errorResponse := validateFilterVersion(submissionData, httpclient)
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

func validateFilterVersion(submissionData launch.CallbackData, httpclient *http.Client) []byte {
	errors := make(map[string]string, 0)
	nightlyOrCi := submissionData.Input[launch.LaunchFromLatestBuild]
	customBuild := submissionData.Input[launch.LaunchFromCustom]
	if nightlyOrCi != "" && customBuild != "" {
		errors[launch.LaunchFromCustom] = "Chose either one or the other parameter!"
		errors[launch.LaunchFromLatestBuild] = "Chose either one or the other parameter!"
		response, err := modals.ValidationError(errors)
		if err == nil {
			return response
		}
	}
	if nightlyOrCi != "" || customBuild != "" {
		return nil
	}
	architecture := submissionData.Context[launch.LaunchArchitecture]
	selectedStream := submissionData.Input[launch.LaunchFromStream]
	selectedMajorMinor := submissionData.Input[launch.LaunchFromMajorMinor]
	releases, _ := launch.FetchReleases(httpclient, architecture)
	for stream, tags := range releases {
		if stream == selectedStream {
			for _, tag := range tags {
				if strings.HasPrefix(tag, selectedMajorMinor) {
					return nil
				}
			}
		}
	}
	message := fmt.Sprintf("Can't find any tags with this combination (Stream: %s, Major.Minor: %s)!", selectedStream, selectedMajorMinor)
	errors[launch.LaunchFromMajorMinor] = message
	errors[launch.LaunchFromStream] = message
	response, err := modals.ValidationError(errors)
	if err == nil {
		return response
	}
	return nil
}
