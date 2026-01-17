package steps

import (
	"net/http"
	"strings"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/common"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/launch"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/mce/create"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
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

		var views common.BackNavigationViews
		var identifiers common.BackNavigationIdentifiers
		if isMCE {
			views = common.BackNavigationViews{
				FirstStepViewWithData: create.FirstStepViewWithData,
				SelectModeView:        create.SelectModeView,
				FilterVersionView:     create.FilterVersionView,
				SelectMinorMajor:      create.SelectMinorMajor,
				SelectVersionView:     create.SelectVersionView,
				PRInputView:           create.PRInputView,
			}
			identifiers = common.BackNavigationIdentifiers{
				InitialView:       string(create.IdentifierInitialView),
				SelectModeView:    string(create.IdentifierSelectModeView),
				FilterVersionView: string(create.IdentifierFilterVersionView),
				SelectMinorMajor:  string(create.IdentifierSelectMinorMajor),
				SelectVersion:     string(create.IdentifierSelectVersion),
				PRInputView:       string(create.IdentifierPRInputView),
				ThirdStep:         string(create.Identifier3rdStep),
			}
		} else {
			views = common.BackNavigationViews{
				FirstStepViewWithData: launch.FirstStepViewWithData,
				SelectModeView:        launch.SelectModeView,
				FilterVersionView:     launch.FilterVersionView,
				SelectMinorMajor:      launch.SelectMinorMajor,
				SelectVersionView:     launch.SelectVersionView,
				PRInputView:           launch.PRInputView,
			}
			identifiers = common.BackNavigationIdentifiers{
				InitialView:       string(launch.IdentifierInitialView),
				SelectModeView:    string(launch.IdentifierRegisterLaunchMode),
				FilterVersionView: string(launch.IdentifierFilterVersionView),
				SelectMinorMajor:  string(launch.IdentifierSelectMinorMajor),
				SelectVersion:     string(launch.IdentifierSelectVersion),
				PRInputView:       string(launch.IdentifierPRInputView),
				ThirdStep:         string(launch.Identifier3rdStep),
			}
		}

		common.NavigateBack(updater, jobmanager, httpclient, callback, logger, data, previousStep, views, identifiers)
		return nil, nil
	})
}
