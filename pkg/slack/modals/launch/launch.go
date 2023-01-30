package launch

import (
	"encoding/json"
	"fmt"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"net/http"
	"strings"
	"sync"
)

// Identifier is the view identifier for this modal
const Identifier modals.Identifier = "launch"
const Identifier2ndStep modals.Identifier = "launch2ndStep"
const Identifier3rdStep modals.Identifier = "launch3ddStep"

type callbackData struct {
	input             map[string]string
	multipleSelection map[string][]string
	context           map[string]string
}

func RegisterFirstStep(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(Identifier, FirstStepView()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextForFirstStep(client, jobmanager, httpclient),
	})
}

func processNextForFirstStep(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc("launch2", func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		go func() {
			callbackData := callbackData{
				input: modals.CallBackInputAll(callback),
			}
			overwriteView := func(view slack.ModalViewRequest) {
				// don't pass a hash, so we overwrite the View always
				response, err := updater.UpdateView(view, "", "", callback.View.ID)
				if err != nil {
					logger.WithError(err).Warn("Failed to update a modal View.")
				}
				logger.WithField("response", response).Trace("Got a modal response.")
			}
			overwriteView(SecondStepView(callback, jobmanager, httpclient, callbackData))
		}()
		response, err := json.Marshal(&slack.ViewSubmissionResponse{
			ResponseAction: slack.RAUpdate,
			View:           PrepareNextStepView(),
		})
		if err != nil {
			logger.WithError(err).Error("Failed to marshal FirstStepView update submission response.")
			return nil, err
		}
		return response, nil
	})
}

func RegisterSecondStep(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(Identifier2ndStep, ThirdStepView(nil, jobmanager, httpclient, callbackData{})).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextForSecondStep(client, jobmanager, httpclient),
	})
}

func processNextForSecondStep(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc("launch3", func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		submissionData := callbackData{
			input:   modals.CallBackInputAll(callback),
			context: callbackContext(callback),
		}
		response := validateSecondStepSubmission(submissionData, jobmanager)
		if response != nil {
			return response, nil
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
			overwriteView(ThirdStepView(callback, jobmanager, httpclient, submissionData))
		}()
		response, err = json.Marshal(&slack.ViewSubmissionResponse{
			ResponseAction: slack.RAUpdate,
			View:           PrepareNextStepView(),
		})
		if err != nil {
			logger.WithError(err).Error("Failed to marshal FirstStepView update submission response.")
			return nil, err
		}
		return response, nil
	})
}

func checkPR(wg *sync.WaitGroup, pr string, jobmanager manager.JobManager) error {
	defer wg.Done()
	_, err := jobmanager.ResolveAsPullRequest(pr)
	return err
}

func validateSecondStepSubmission(submissionData callbackData, jobmanager manager.JobManager) []byte {
	var found []string
	errors := make(map[string]string, 0)
	checkInput := []string{launchFromReleaseController, launchFromLatestBuild, launchFromStream, launchFromMajorMinor, launchFromCustom}
	for _, versionType := range checkInput {
		_, exists := submissionData.input[versionType]
		if exists {
			found = append(found, versionType)
		}
	}
	prs, ok := submissionData.input[launchFromPR]
	var prErrors []string
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

		if len(prErrors) > 0 {
			errors[launchFromPR] = strings.Join(prErrors, "; ")
		}
	}
	if len(found) > 1 {
		for _, v := range found {
			errors[v] = "No more than one version type can be selected at the same time!"
		}
	}
	if len(errors) == 0 {
		return nil
	}
	response, err := modals.ValidationError(errors)
	if err == nil {
		return response
	}
	return nil
}

func RegisterThirdStep(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(Identifier3rdStep, SubmissionView("")).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextForThirdStep(client, jobmanager, httpclient),
	})
}

func processNextForThirdStep(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(string(Identifier3rdStep), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		var launchInputs []string
		data := callbackData{
			input:             modals.CallBackInputAll(callback),
			multipleSelection: modals.CallbackMultipleSelect(callback),
			context:           callbackContext(callback),
		}
		platform := data.context[launchPlatform]
		architecture := data.context[launchArchitecture]
		version := data.context[launchVersion]
		launchInputs = append(launchInputs, version)
		prs, ok := data.context[launchFromPR]
		if ok && prs != "None" {
			prSlice := strings.Split(prs, ",")
			for _, pr := range prSlice {
				launchInputs = append(launchInputs, strings.TrimSpace(pr))
			}
		}
		parameters := data.multipleSelection[launchParameters]
		parametersMap := make(map[string]string)
		for _, parameter := range parameters {
			parametersMap[parameter] = ""
		}
		launch := fmt.Sprintf("launch %s %s,%s,%s ", version, architecture, platform, strings.Join(parameters[:], ","))
		job := &manager.JobRequest{
			OriginalMessage: launch,
			User:            callback.User.ID,
			UserName:        callback.User.Name,
			Inputs:          [][]string{launchInputs},
			Type:            manager.JobTypeInstall,
			Platform:        platform,
			Channel:         callback.User.ID,
			JobParams:       parametersMap,
			Architecture:    architecture,
		}
		errorResponse := validateThirdStepSubmission(jobmanager, job)
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
				}
				logger.WithField("response", response).Trace("Got a modal response.")
			}
			if err != nil {
				overwriteView(SubmissionView(err.Error()))
			} else {
				overwriteView(SubmissionView(msg))
			}
		}()
		response, err := json.Marshal(&slack.ViewSubmissionResponse{
			ResponseAction: slack.RAUpdate,
			View:           PrepareNextStepView(),
		})
		if err != nil {
			logger.WithError(err).Error("Failed to marshal FirstStepView update submission response.")
			return nil, err
		}
		return response, nil
	})
}

func validateThirdStepSubmission(jobManager manager.JobManager, job *manager.JobRequest) []byte {
	err := jobManager.CheckValidJobConfiguration(job)
	errors := make(map[string]string, 0)
	if err != nil {
		errors[launchParameters] = err.Error()
	} else {
		return nil
	}
	response, err := modals.ValidationError(errors)
	if err == nil {
		return response
	}
	return nil
}
func fetchReleases(client *http.Client, architecture string) (map[string][]string, error) {
	url := fmt.Sprintf("https://%s.ocp.releases.ci.openshift.org/api/v1/releasestreams/accepted", architecture)
	acceptedReleases := make(map[string][]string, 0)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&acceptedReleases); err != nil {
		return nil, err
	}
	return acceptedReleases, nil
}

func callbackContext(callback *slack.InteractionCallback) map[string]string {
	contextMap := make(map[string]string)
	var context string
	for _, value := range callback.View.Blocks.BlockSet {
		if value.BlockType() == slack.MBTContext {
			metadata, ok := value.(*slack.ContextBlock)
			if ok {
				text, ok := metadata.ContextElements.Elements[0].(*slack.TextBlockObject)
				if ok {
					context = text.Text
				}
			}
		}
	}
	mSplit := strings.Split(context, ";")
	for _, v := range mSplit {
		switch strings.ToLower(strings.TrimSpace(strings.Split(v, ":")[0])) {
		case launchArchitecture:
			contextMap[launchArchitecture] = strings.TrimSpace(strings.Split(v, ":")[1])
		case launchPlatform:
			contextMap[launchPlatform] = strings.TrimSpace(strings.Split(v, ":")[1])
		case launchVersion:
			contextMap[launchVersion] = strings.TrimSpace(strings.Split(v, ":")[1])
		case launchFromPR:
			contextMap[launchFromPR] = strings.TrimSpace(strings.Split(v, ":")[1])
		}
	}
	return contextMap
}
