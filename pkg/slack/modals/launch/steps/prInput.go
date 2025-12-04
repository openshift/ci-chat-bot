package steps

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/launch"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"k8s.io/klog"
)

func RegisterPRInput(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(launch.IdentifierPRInputView, launch.PRInputView(nil, modals.CallbackData{}, "")).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextPRInput(client, jobmanager, httpclient),
	})
}

func processNextPRInput(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(string(launch.IdentifierPRInputView), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		submissionData := modals.MergeCallbackData(callback)
		errorsResponse := validatePRInputView(submissionData, jobmanager)
		if errorsResponse != nil {
			return errorsResponse, nil
		}
		go modals.OverwriteView(updater, launch.ThirdStepView(callback, jobmanager, httpclient, submissionData, string(launch.IdentifierPRInputView)), callback, logger)
		return modals.SubmitPrepare(launch.ModalTitle, string(launch.IdentifierPRInputView), logger)
	})
}

func validatePRInputView(submissionData modals.CallbackData, jobmanager manager.JobManager) []byte {
	prs, ok := submissionData.Input[modals.LaunchFromPR]
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

	errors[modals.LaunchFromPR] = strings.Join(prErrors, "; ")
	response, err := modals.ValidationError(errors)
	if err != nil {
		klog.Warningf("failed to build validation error: %v", err)
		return nil
	}

	return response
}
