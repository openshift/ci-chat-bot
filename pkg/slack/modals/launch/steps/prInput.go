package steps

import (
	"encoding/json"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/launch"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"k8s.io/klog/v2"
	"net/http"
	"strings"
	"sync"
)

func RegisterPRInput(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(launch.IdentifierPRInputView, launch.PRInputView(nil, launch.CallbackData{})).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextPRInput(client, jobmanager, httpclient),
	})
}

func processNextPRInput(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc("launch3", func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		submissionData := launch.CallbackData{
			Input:             modals.CallBackInputAll(callback),
			Context:           callbackContext(callback),
			MultipleSelection: modals.CallbackMultipleSelect(callback),
		}
		errorsResponse := validatePRInputView(submissionData, jobmanager)
		if errorsResponse != nil {
			return errorsResponse, nil
		}
		go func() {
			overwriteView := func(view slack.ModalViewRequest) {
				// don't pass a hash, so we overwrite the View always
				response, err := updater.UpdateView(view, "", "", callback.View.ID)
				if err != nil {
					logger.WithError(err).Warn("Failed to update a modal View.")
				}
				logger.WithField("response", response).Trace("Got a modal response.")
			}

			overwriteView(launch.ThirdStepView(callback, jobmanager, httpclient, submissionData))

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

func validatePRInputView(submissionData launch.CallbackData, jobmanager manager.JobManager) []byte {
	// TODO - there must be a better way to validate here. Slack will wait for 3 seconds before timing out the view
	// in case of a 404, this might take longer (I assume due to some retries). Maybe check with a 404 before validating the PR
	prs, ok := submissionData.Input[launch.LaunchFromPR]
	var prErrors []string
	errors := make(map[string]string, 0)
	if ok {
		var wg sync.WaitGroup
		prSlice := strings.Split(prs, ",")
		wg.Add(len(prSlice))
		for _, pr := range prSlice {
			tmpPr := pr
			go func() {
				err := checkPR(&wg, tmpPr, jobmanager)
				if err != nil {
					prErrors = append(prErrors, err.Error())
				}
			}()
		}
		wg.Wait()
		if len(prErrors) == 0 {
			return nil
		}
	}
	errors[launch.LaunchFromPR] = strings.Join(prErrors, "; ")
	response, err := modals.ValidationError(errors)
	if err != nil {
		klog.Warningf("failed to build validation error: %v", err)
		return nil
	}
	return response
}

func checkPR(wg *sync.WaitGroup, pr string, jobmanager manager.JobManager) error {
	defer wg.Done()
	_, err := jobmanager.ResolveAsPullRequest(pr)
	return err
}
