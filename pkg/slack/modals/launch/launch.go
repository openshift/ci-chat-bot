package launch

import (
	"encoding/json"
	"fmt"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog"
	"net/http"
	"strings"
	"sync"
)

const (
	IdentifierInitialView        modals.Identifier = "launch"
	Identifier3rdStep            modals.Identifier = "launch3rdStep"
	IdentifierPRInputView        modals.Identifier = "pr_input_view"
	IdentifierFilterVersionView  modals.Identifier = "filter_version_view"
	IdentifierRegisterLaunchMode modals.Identifier = "launch_mode_view"
	IdentifierSelectVersion                        = "select_version"
)

const (
	stableReleasesPrefix        = "4-stable"
	launchFromPR                = "pr"
	launchFromMajorMinor        = "major_minor"
	launchFromStream            = "stream"
	launchFromLatestBuild       = "latest_build"
	launchFromReleaseController = "release_controller_version"
	launchFromCustom            = "custom"
	launchPlatform              = "platform"
	launchArchitecture          = "architecture"
	launchParameters            = "parameters"
	launchVersion               = "version"
	launchStepContext           = "context"
	defaultPlatform             = "hypershift-hosted"
	defaultArchitecture         = "amd64"
	launchMode                  = "launch_mode"
	launchModeVersion           = "version"
	launchModePR                = "pr"
	launchModePRKey             = "One or multiple PRs"
	launchModeVersionKey        = "A Version"
	launchModeContext           = "Launch Mode"
)

type callbackData struct {
	input             map[string]string
	multipleSelection map[string][]string
	context           map[string]string
}

func RegisterFirstStep(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(IdentifierInitialView, FirstStepView()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextRegisterFirstStep(client, jobmanager, httpclient),
	})
}

func processNextRegisterFirstStep(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
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
			overwriteView(SelectModeView(callback, jobmanager, callbackData))
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

func RegisterLaunchModeStep(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(IdentifierRegisterLaunchMode, ThirdStepView(nil, jobmanager, httpclient, callbackData{})).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextLaunchModeStep(client, jobmanager, httpclient),
	})
}

func processNextLaunchModeStep(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc("launch3", func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		submissionData := callbackData{
			input:             modals.CallBackInputAll(callback),
			context:           callbackContext(callback),
			multipleSelection: modals.CallbackMultipleSelect(callback),
		}
		mode := make(sets.String)
		for _, selection := range submissionData.multipleSelection[launchMode] {
			switch selection {
			case launchModePRKey:
				mode.Insert(launchModePR)
			case launchModeVersionKey:
				mode.Insert(launchModeVersion)
			}
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
			if mode.Has(launchModeVersion) {
				overwriteView(FilterVersionView(callback, jobmanager, submissionData, httpclient, mode))
			} else {
				overwriteView(PRInputView(callback, submissionData))
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

func RegisterFilterVersion(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(IdentifierFilterVersionView, FilterVersionView(nil, nil, callbackData{}, nil, nil)).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextFilterVersion(client, jobmanager, httpclient),
	})
}

func processNextFilterVersion(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	// TODO - validate the combination. Check the version from the release controller, and if it is empty, retrun an
	// error, like: this combination this and that does not seem right, no version found.

	// TODO - if a stream that has multiple major.minor versions is selected, enforce the selection of major.minor
	// if a major.minor is selected without a stream name, enforce the stream name selection.
	return interactions.HandlerFunc("launch3", func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		submissionData := callbackData{
			input:             modals.CallBackInputAll(callback),
			context:           callbackContext(callback),
			multipleSelection: modals.CallbackMultipleSelect(callback),
		}
		errorResponse := validateFilterVersion(submissionData, httpclient)
		if errorResponse != nil {
			return errorResponse, nil
		}
		nightlyOrCi := submissionData.input[launchFromLatestBuild]
		customBuild := submissionData.input[launchFromCustom]
		mode := submissionData.context[launchMode]
		launchModeSplit := strings.Split(mode, ",")
		launchWithPr := false
		for _, key := range launchModeSplit {
			if strings.TrimSpace(key) == launchModePR {
				launchWithPr = true
			}
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
			if (nightlyOrCi != "" || customBuild != "") && launchWithPr {
				overwriteView(PRInputView(callback, submissionData))
			} else if (nightlyOrCi != "" || customBuild != "") && !launchWithPr {
				overwriteView(ThirdStepView(callback, jobmanager, httpclient, submissionData))
			} else {
				overwriteView(SelectVersionView(callback, jobmanager, httpclient, submissionData))
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

func RegisterSelectVersion(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(IdentifierSelectVersion, SelectVersionView(nil, jobmanager, httpclient, callbackData{})).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextSelectVersion(client, jobmanager, httpclient),
	})
}

func processNextSelectVersion(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc("launch3", func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		submissionData := callbackData{
			input:             modals.CallBackInputAll(callback),
			context:           callbackContext(callback),
			multipleSelection: modals.CallbackMultipleSelect(callback),
		}
		m := submissionData.context[launchMode]
		launchModeSplit := strings.Split(m, ",")
		launchWithPR := false
		for _, key := range launchModeSplit {
			if strings.TrimSpace(key) == launchModePR {
				launchWithPR = true
			}
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
			if launchWithPR {
				overwriteView(PRInputView(callback, submissionData))
			} else {
				overwriteView(ThirdStepView(callback, jobmanager, httpclient, submissionData))
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

func RegisterPRInput(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(IdentifierPRInputView, PRInputView(nil, callbackData{})).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextPRInput(client, jobmanager, httpclient),
	})
}

func processNextPRInput(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc("launch3", func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		submissionData := callbackData{
			input:             modals.CallBackInputAll(callback),
			context:           callbackContext(callback),
			multipleSelection: modals.CallbackMultipleSelect(callback),
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

			overwriteView(ThirdStepView(callback, jobmanager, httpclient, submissionData))

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

func validatePRInputView(submissionData callbackData, jobmanager manager.JobManager) []byte {
	// TODO - there must be a better way to validate here. Slack will wait for 3 seconds before timing out the view
	// in case of a 404, this might take longer (I assume due to some retries). Maybe check with a 404 before validating the PR
	prs, ok := submissionData.input[launchFromPR]
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
	errors[launchFromPR] = strings.Join(prErrors, "; ")
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
		if ok && prs != "none" {
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

func validateFilterVersion(submissionData callbackData, httpclient *http.Client) []byte {
	errors := make(map[string]string, 0)
	nightlyOrCi := submissionData.input[launchFromLatestBuild]
	customBuild := submissionData.input[launchFromCustom]
	if nightlyOrCi != "" && customBuild != "" {
		errors[launchFromCustom] = "Chose either one or the other parameter!"
		errors[launchFromLatestBuild] = "Chose either one or the other parameter!"
		response, err := modals.ValidationError(errors)
		if err == nil {
			return response
		}
	}
	if nightlyOrCi != "" || customBuild != "" {
		return nil
	}
	architecture := submissionData.context[launchArchitecture]
	selectedStream := submissionData.input[launchFromStream]
	selectedMajorMinor := submissionData.input[launchFromMajorMinor]
	releases, _ := fetchReleases(httpclient, architecture)
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
	errors[launchFromMajorMinor] = message
	errors[launchFromStream] = message
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
		key := strings.ToLower(strings.TrimSpace(strings.Split(v, ":")[0]))
		value := strings.ToLower(strings.TrimSpace(strings.Split(v, ":")[1]))
		switch key {
		case launchArchitecture:
			contextMap[launchArchitecture] = value
		case launchPlatform:
			contextMap[launchPlatform] = value
		case launchVersion:
			contextMap[launchVersion] = value
		case launchFromPR:
			contextMap[launchFromPR] = value
		case strings.ToLower(launchModeContext):
			contextMap[launchMode] = value
		}
	}
	return contextMap
}
