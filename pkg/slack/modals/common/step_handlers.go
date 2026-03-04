package common

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"k8s.io/klog"
)

// HasPRMode checks if PR mode is enabled in the callback data
func HasPRMode(data modals.CallbackData) bool {
	return data.Input[modals.LaunchMode] == modals.LaunchFromPRYes
}

// ValidatePRInput validates PR input by resolving each PR concurrently
func ValidatePRInput(submissionData modals.CallbackData, jobmanager manager.JobManager) []byte {
	prs, ok := submissionData.Input[modals.LaunchFromPR]
	if !ok {
		return nil
	}

	var wg sync.WaitGroup
	errCh := make(chan error)

	prSlice := strings.SplitSeq(prs, ",")
	for pr := range prSlice {
		wg.Add(1)
		go func(pr string) {
			defer wg.Done()
			prParts, err := jobmanager.ResolveAsPullRequest(pr)
			if prParts == nil {
				errCh <- fmt.Errorf("invalid PR(s)")
			} else if err != nil {
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

// ValidateFilterVersion validates that only one version source is selected
func ValidateFilterVersion(submissionData modals.CallbackData) []byte {
	customBuild := submissionData.Input[modals.LaunchFromCustom]
	selectedStream := submissionData.Input[modals.LaunchFromStream]
	if customBuild != "" && selectedStream != "" {
		errs := map[string]string{
			modals.LaunchFromCustom: "Select only one parameter!",
			modals.LaunchFromStream: "Select only one parameter!",
		}
		response, err := modals.ValidationError(errs)
		if err == nil {
			return response
		}
	}
	return nil
}

// ViewFuncs groups all view builder functions for a flow
type ViewFuncs struct {
	FilterVersionView func(*slack.InteractionCallback, manager.JobManager, modals.CallbackData, *http.Client, bool) slack.ModalViewRequest
	PRInputView       func(*slack.InteractionCallback, modals.CallbackData, string) slack.ModalViewRequest
	ThirdStepView     func(*slack.InteractionCallback, manager.JobManager, *http.Client, modals.CallbackData, string) slack.ModalViewRequest
	SelectVersionView func(*slack.InteractionCallback, manager.JobManager, *http.Client, modals.CallbackData, string) slack.ModalViewRequest
}

// MakeModeStepHandler creates a mode selection step handler
func MakeModeStepHandler(
	identifier string,
	modalTitle string,
	filterVersionView func(*slack.InteractionCallback, manager.JobManager, modals.CallbackData, *http.Client, bool) slack.ModalViewRequest,
) func(modals.ViewUpdater, manager.JobManager, *http.Client) interactions.Handler {
	return func(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
		return interactions.HandlerFunc(identifier, func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
			submissionData := modals.MergeCallbackData(callback)
			go func() {
				modals.OverwriteView(updater, filterVersionView(callback, jobmanager, submissionData, httpclient, false), callback, logger)
			}()
			return modals.SubmitPrepare(modalTitle, identifier, logger)
		})
	}
}

// MakePRInputHandler creates a PR input step handler
func MakePRInputHandler(
	identifier string,
	modalTitle string,
	thirdStepView func(*slack.InteractionCallback, manager.JobManager, *http.Client, modals.CallbackData, string) slack.ModalViewRequest,
) func(modals.ViewUpdater, manager.JobManager, *http.Client) interactions.Handler {
	return func(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
		return interactions.HandlerFunc(identifier, func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
			submissionData := modals.MergeCallbackData(callback)
			errorsResponse := ValidatePRInput(submissionData, jobmanager)
			if errorsResponse != nil {
				return errorsResponse, nil
			}
			go modals.OverwriteView(updater, thirdStepView(callback, jobmanager, httpclient, submissionData, identifier), callback, logger)
			return modals.SubmitPrepare(modalTitle, identifier, logger)
		})
	}
}

// MakeFilterVersionHandler creates a filter version step handler
func MakeFilterVersionHandler(
	identifier string,
	modalTitle string,
	returnIdentifier string,
	views ViewFuncs,
) func(modals.ViewUpdater, manager.JobManager, *http.Client) interactions.Handler {
	return func(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
		return interactions.HandlerFunc(identifier, func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
			submissionData := modals.MergeCallbackData(callback)
			errorResponse := ValidateFilterVersion(submissionData)
			if errorResponse != nil {
				return errorResponse, nil
			}
			customBuild := submissionData.Input[modals.LaunchFromCustom]
			stream := submissionData.Input[modals.LaunchFromStream]
			hasPR := HasPRMode(submissionData)
			go func() {
				if customBuild == "" && stream == "" {
					modals.OverwriteView(updater, views.FilterVersionView(callback, jobmanager, submissionData, httpclient, true), callback, logger)
				} else if customBuild != "" && hasPR {
					modals.OverwriteView(updater, views.PRInputView(callback, submissionData, identifier), callback, logger)
				} else if customBuild != "" && !hasPR {
					modals.OverwriteView(updater, views.ThirdStepView(callback, jobmanager, httpclient, submissionData, identifier), callback, logger)
				} else {
					modals.OverwriteView(updater, views.SelectVersionView(callback, jobmanager, httpclient, submissionData, identifier), callback, logger)
				}
			}()
			return modals.SubmitPrepare(modalTitle, returnIdentifier, logger)
		})
	}
}

// MakeSelectVersionHandler creates a select version step handler
func MakeSelectVersionHandler(
	identifier string,
	modalTitle string,
	prInputView func(*slack.InteractionCallback, modals.CallbackData, string) slack.ModalViewRequest,
	thirdStepView func(*slack.InteractionCallback, manager.JobManager, *http.Client, modals.CallbackData, string) slack.ModalViewRequest,
) func(modals.ViewUpdater, manager.JobManager, *http.Client) interactions.Handler {
	return func(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
		return interactions.HandlerFunc(identifier, func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
			submissionData := modals.MergeCallbackData(callback)
			hasPR := HasPRMode(submissionData)
			go func() {
				if hasPR {
					modals.OverwriteView(updater, prInputView(callback, submissionData, identifier), callback, logger)
				} else {
					modals.OverwriteView(updater, thirdStepView(callback, jobmanager, httpclient, submissionData, identifier), callback, logger)
				}
			}()
			return modals.SubmitPrepare(modalTitle, identifier, logger)
		})
	}
}

// MakeSelectMinorMajorHandler creates a select minor/major version step handler
func MakeSelectMinorMajorHandler(
	identifier string,
	modalTitle string,
	selectVersionView func(*slack.InteractionCallback, manager.JobManager, *http.Client, modals.CallbackData, string) slack.ModalViewRequest,
) func(modals.ViewUpdater, manager.JobManager, *http.Client) interactions.Handler {
	return func(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
		return interactions.HandlerFunc(identifier, func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
			submissionData := modals.MergeCallbackData(callback)
			go modals.OverwriteView(updater, selectVersionView(callback, jobmanager, httpclient, submissionData, identifier), callback, logger)
			return modals.SubmitPrepare(modalTitle, identifier, logger)
		})
	}
}

// FirstStepConfig holds configuration for the first step handler
type FirstStepConfig struct {
	DefaultPlatform     string
	DefaultArchitecture string
	// NeedsArchitecture indicates if architecture selection is required
	NeedsArchitecture bool
}

// MakeFirstStepHandler creates a first step handler with optional platform/architecture defaults
func MakeFirstStepHandler(
	identifier string,
	modalTitle string,
	selectModeView func(*slack.InteractionCallback, manager.JobManager, modals.CallbackData) slack.ModalViewRequest,
	config FirstStepConfig,
) func(modals.ViewUpdater, manager.JobManager, *http.Client) interactions.Handler {
	return func(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
		return interactions.HandlerFunc(identifier, func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
			callbackData := modals.CallbackData{
				Input: modals.CallBackInputAll(callback),
			}

			// Apply platform defaults if configured
			if config.NeedsArchitecture {
				if callbackData.Input[modals.LaunchPlatform] == "" {
					callbackData.Input[modals.LaunchPlatform] = config.DefaultPlatform
				}
				if callbackData.Input[modals.LaunchArchitecture] == "" {
					// Handle multi-arch for hypershift-hosted
					if callbackData.Input[modals.LaunchPlatform] == "hypershift-hosted" {
						callbackData.Input[modals.LaunchArchitecture] = "multi"
					} else {
						callbackData.Input[modals.LaunchArchitecture] = config.DefaultArchitecture
					}
				}

				// Validate architecture for hypershift-hosted
				if callbackData.Input[modals.LaunchPlatform] == "hypershift-hosted" && callbackData.Input[modals.LaunchArchitecture] != "multi" {
					errs := map[string]string{
						modals.LaunchArchitecture: `hypershift-hosted only supports the "multi" architecture`,
					}
					response, err := modals.ValidationError(errs)
					if err == nil {
						return response, nil
					}
				}
			}

			go func() {
				modals.OverwriteView(updater, selectModeView(callback, jobmanager, callbackData), callback, logger)
			}()
			return modals.SubmitPrepare(modalTitle, identifier, logger)
		})
	}
}

// BackNavigationViews holds all view functions needed for back navigation
type BackNavigationViews struct {
	FirstStepViewWithData func(modals.CallbackData) slack.ModalViewRequest
	SelectModeView        func(*slack.InteractionCallback, manager.JobManager, modals.CallbackData) slack.ModalViewRequest
	FilterVersionView     func(*slack.InteractionCallback, manager.JobManager, modals.CallbackData, *http.Client, bool) slack.ModalViewRequest
	SelectMinorMajor      func(*slack.InteractionCallback, *http.Client, modals.CallbackData, string) slack.ModalViewRequest
	SelectVersionView     func(*slack.InteractionCallback, manager.JobManager, *http.Client, modals.CallbackData, string) slack.ModalViewRequest
	PRInputView           func(*slack.InteractionCallback, modals.CallbackData, string) slack.ModalViewRequest
}

// BackNavigationIdentifiers holds all step identifiers for a flow
type BackNavigationIdentifiers struct {
	InitialView       string
	SelectModeView    string
	FilterVersionView string
	SelectMinorMajor  string
	SelectVersion     string
	PRInputView       string
	ThirdStep         string
}

// clearForwardSelections removes Input and MultipleSelection keys belonging to
// the target step and all subsequent steps. This prevents stale selections from
// leaking forward when the user navigates back through the modal flow.
func clearForwardSelections(data *modals.CallbackData, fromStep string, identifiers BackNavigationIdentifiers) {
	// Ordered list of steps and their associated keys.
	type stepKeys struct {
		step       string
		input      []string
		multiInput []string
	}
	steps := []stepKeys{
		{step: identifiers.InitialView, input: []string{modals.LaunchPlatform, modals.LaunchArchitecture}},
		{step: identifiers.SelectModeView, input: []string{modals.LaunchMode}},
		{step: identifiers.FilterVersionView, input: []string{modals.LaunchFromStream, modals.LaunchFromCustom}},
		{step: identifiers.SelectMinorMajor, input: []string{modals.LaunchFromMajorMinor}},
		{step: identifiers.SelectVersion, input: []string{modals.LaunchVersion}},
		{step: identifiers.PRInputView, input: []string{modals.LaunchFromPR}},
		{step: identifiers.ThirdStep, multiInput: []string{modals.LaunchParameters}},
	}

	clearing := false
	for _, s := range steps {
		if s.step == fromStep {
			clearing = true
			continue
		}
		if !clearing {
			continue
		}
		for _, k := range s.input {
			delete(data.Input, k)
		}
		for _, k := range s.multiInput {
			delete(data.MultipleSelection, k)
		}
	}
}

// NavigateBack resolves the previous step view and overwrites the current modal.
// The caller is responsible for extracting the callback data and previous step.
func NavigateBack(
	updater modals.ViewUpdater,
	jobmanager manager.JobManager,
	httpclient *http.Client,
	callback *slack.InteractionCallback,
	logger *logrus.Entry,
	data modals.CallbackData,
	previousStep string,
	views BackNavigationViews,
	identifiers BackNavigationIdentifiers,
) {
	clearForwardSelections(&data, previousStep, identifiers)

	var previousView slack.ModalViewRequest
	switch previousStep {
	case identifiers.InitialView:
		previousView = views.FirstStepViewWithData(data)
	case identifiers.SelectModeView:
		previousView = views.SelectModeView(callback, jobmanager, data)
	case identifiers.FilterVersionView:
		previousView = views.FilterVersionView(callback, jobmanager, data, httpclient, false)
	case identifiers.SelectMinorMajor:
		previousView = views.SelectMinorMajor(callback, httpclient, data, identifiers.FilterVersionView)
	case identifiers.SelectVersion:
		previousView = views.SelectVersionView(callback, jobmanager, httpclient, data, identifiers.FilterVersionView)
	case identifiers.PRInputView:
		prPreviousStep := identifiers.SelectModeView
		if data.Input[modals.LaunchVersion] != "" || data.Input[modals.LaunchFromCustom] != "" {
			prPreviousStep = identifiers.FilterVersionView
		}
		previousView = views.PRInputView(callback, data, prPreviousStep)
	default:
		logger.Warnf("Unknown previous step: %s, defaulting to first step", previousStep)
		previousView = views.FirstStepViewWithData(data)
	}

	modals.OverwriteView(updater, previousView, callback, logger)
}
