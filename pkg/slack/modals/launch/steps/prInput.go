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
	"k8s.io/klog"
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
	prs, ok := submissionData.Input[launch.LaunchFromPR]
	if !ok {
		return nil
	}

	var wg sync.WaitGroup
	errCh := make(chan error)

	prSlice := strings.Split(prs, ",")
	for _, pr := range prSlice {
		wg.Add(1)
		go func(pr string) {
			defer wg.Done()
			prParts, err := jobmanager.ResolveAsPullRequest(pr)
			if prParts == nil {
				errCh <- fmt.Errorf("invalid PR(s)")
			}
			if err != nil {
				errCh <- err
			}
		}(pr)
	}

	go func() {
		wg.Wait()
		close(errCh)
	}()

	errors := make(map[string]string)
	var prErrors []string

	for err := range errCh {
		prErrors = append(prErrors, err.Error())
	}

	if len(prErrors) == 0 {
		return nil
	}

	errors[launch.LaunchFromPR] = strings.Join(prErrors, "; ")
	response, err := modals.ValidationError(errors)
	if err != nil {
		klog.Warningf("failed to build validation error: %v", err)
		return nil
	}

	return response
}
