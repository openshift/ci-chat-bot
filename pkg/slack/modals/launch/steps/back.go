package steps

import (
	"net/http"
	"strings"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/launch"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/mce/create"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"k8s.io/apimachinery/pkg/util/sets"
)

// RegisterBackButton registers the back button handler for both launch and MCE modal flows
func RegisterBackButton(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(modals.Identifier(modals.BackButtonActionID), slack.ModalViewRequest{}).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeBlockActions: handleBackButton(client, jobmanager, httpclient),
	})
}

func handleBackButton(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(modals.BackButtonActionID, func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		// Check if this is a back button action
		if len(callback.ActionCallback.BlockActions) == 0 {
			return nil, nil
		}
		action := callback.ActionCallback.BlockActions[0]
		if action.ActionID != modals.BackButtonActionID {
			return nil, nil
		}

		// Get the callback data which contains the previous step
		data := modals.MergeCallbackData(callback)
		previousStep := data.PreviousStep

		logger.Infof("Back button pressed, navigating to previous step: %s", previousStep)

		// Determine if this is an MCE modal based on the previous step identifier
		isMCE := strings.HasPrefix(previousStep, "mce_")

		var previousView slack.ModalViewRequest
		if isMCE {
			previousView = handleMCEBackNavigation(callback, jobmanager, httpclient, data, previousStep, logger)
		} else {
			previousView = handleLaunchBackNavigation(callback, jobmanager, httpclient, data, previousStep, logger)
		}

		modals.OverwriteView(updater, previousView, callback, logger)
		return nil, nil
	})
}

func handleLaunchBackNavigation(callback *slack.InteractionCallback, jobmanager manager.JobManager, httpclient *http.Client, data modals.CallbackData, previousStep string, logger *logrus.Entry) slack.ModalViewRequest {
	switch previousStep {
	case string(launch.IdentifierInitialView):
		return launch.FirstStepViewWithData(data)
	case string(launch.IdentifierRegisterLaunchMode):
		return launch.SelectModeView(callback, jobmanager, data)
	case string(launch.IdentifierFilterVersionView):
		mode := sets.New(data.MultipleSelection[modals.LaunchMode]...)
		return launch.FilterVersionView(callback, jobmanager, data, httpclient, mode, false)
	case string(launch.IdentifierSelectMinorMajor):
		return launch.SelectMinorMajor(callback, httpclient, data, string(launch.IdentifierFilterVersionView))
	case string(launch.IdentifierSelectVersion):
		return launch.SelectVersionView(callback, jobmanager, httpclient, data, string(launch.IdentifierFilterVersionView))
	case string(launch.IdentifierPRInputView):
		prPreviousStep := string(launch.IdentifierRegisterLaunchMode)
		if data.Input[modals.LaunchVersion] != "" || data.Input[modals.LaunchFromLatestBuild] != "" || data.Input[modals.LaunchFromCustom] != "" {
			prPreviousStep = string(launch.IdentifierFilterVersionView)
		}
		return launch.PRInputView(callback, data, prPreviousStep)
	default:
		logger.Warnf("Unknown launch previous step: %s, defaulting to first step", previousStep)
		return launch.FirstStepViewWithData(data)
	}
}

func handleMCEBackNavigation(callback *slack.InteractionCallback, jobmanager manager.JobManager, httpclient *http.Client, data modals.CallbackData, previousStep string, logger *logrus.Entry) slack.ModalViewRequest {
	switch previousStep {
	case string(create.IdentifierInitialView):
		return create.FirstStepViewWithData(data)
	case string(create.IdentifierSelectModeView):
		return create.SelectModeView(callback, jobmanager, data)
	case string(create.IdentifierFilterVersionView):
		mode := sets.New(data.MultipleSelection[modals.LaunchMode]...)
		return create.FilterVersionView(callback, jobmanager, data, httpclient, mode, false)
	case string(create.IdentifierSelectMinorMajor):
		return create.SelectMinorMajor(callback, httpclient, data, string(create.IdentifierFilterVersionView))
	case string(create.IdentifierSelectVersion):
		return create.SelectVersionView(callback, jobmanager, httpclient, data, string(create.IdentifierFilterVersionView))
	case string(create.IdentifierPRInputView):
		prPreviousStep := string(create.IdentifierSelectModeView)
		if data.Input[modals.LaunchVersion] != "" || data.Input[modals.LaunchFromLatestBuild] != "" || data.Input[modals.LaunchFromCustom] != "" {
			prPreviousStep = string(create.IdentifierFilterVersionView)
		}
		return create.PRInputView(callback, data, prPreviousStep)
	default:
		logger.Warnf("Unknown MCE previous step: %s, defaulting to first step", previousStep)
		return create.FirstStepViewWithData(data)
	}
}
