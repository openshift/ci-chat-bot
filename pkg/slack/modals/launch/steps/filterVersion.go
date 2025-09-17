package steps

import (
	"net/http"
	"strings"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/launch"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

func RegisterFilterVersion(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(launch.IdentifierFilterVersionView, launch.FilterVersionView(nil, nil, modals.CallbackData{}, nil, nil)).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextFilterVersion(client, jobmanager, httpclient),
	})
}

func processNextFilterVersion(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(string(launch.IdentifierFilterVersionView), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		submissionData := modals.MergeCallbackData(callback)
		errorResponse := validateFilterVersion(submissionData)
		if errorResponse != nil {
			return errorResponse, nil
		}
		nightlyOrCi := submissionData.Input[launch.LaunchFromLatestBuild]
		customBuild := submissionData.Input[launch.LaunchFromCustom]
		mode := submissionData.MultipleSelection[launch.LaunchMode]
		launchWithPr := false
		for _, key := range mode {
			if strings.TrimSpace(key) == launch.LaunchModePR {
				launchWithPr = true
			}
		}
		go func() {
			if (nightlyOrCi != "" || customBuild != "") && launchWithPr {
				modals.OverwriteView(updater, launch.PRInputView(callback, submissionData), callback, logger)
			} else if (nightlyOrCi != "" || customBuild != "") && !launchWithPr {
				modals.OverwriteView(updater, launch.ThirdStepView(callback, jobmanager, httpclient, submissionData), callback, logger)
			} else {
				modals.OverwriteView(updater, launch.SelectVersionView(callback, jobmanager, httpclient, submissionData), callback, logger)
			}

		}()
		return modals.SubmitPrepare(launch.ModalTitle, string(launch.IdentifierSelectVersion), logger)
	})
}

func checkVariables(vars ...string) bool {
	count := 0
	for _, v := range vars {
		if v != "" {
			count++
		}
	}
	return count <= 1
}

func validateFilterVersion(submissionData modals.CallbackData) []byte {
	errs := make(map[string]string, 0)
	nightlyOrCi := submissionData.Input[launch.LaunchFromLatestBuild]
	if nightlyOrCi != "" {
		errs[launch.LaunchFromLatestBuild] = "Select only one parameter!"
	}
	customBuild := submissionData.Input[launch.LaunchFromCustom]
	if customBuild != "" {
		errs[launch.LaunchFromCustom] = "Select only one parameter!"
	}
	selectedStream := submissionData.Input[launch.LaunchFromStream]
	if selectedStream != "" {
		errs[launch.LaunchFromStream] = "Select only one parameter!"
	}
	if !checkVariables(nightlyOrCi, customBuild, selectedStream) {
		response, err := modals.ValidationError(errs)
		if err == nil {
			return response
		}
	}
	return nil
}
