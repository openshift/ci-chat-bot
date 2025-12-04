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
	"k8s.io/apimachinery/pkg/util/sets"
)

func RegisterFilterVersion(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(launch.IdentifierFilterVersionView, launch.FilterVersionView(nil, jobmanager, modals.CallbackData{}, httpclient, nil, false)).WithFollowUps(map[slack.InteractionType]interactions.Handler{
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
		nightlyOrCi := submissionData.Input[modals.LaunchFromLatestBuild]
		customBuild := submissionData.Input[modals.LaunchFromCustom]
		stream := submissionData.Input[modals.LaunchFromStream]
		mode := submissionData.MultipleSelection[modals.LaunchMode]
		launchWithPr := false
		for _, key := range mode {
			if strings.TrimSpace(key) == modals.LaunchModePRKey {
				launchWithPr = true
			}
		}
		go func() {
			if (nightlyOrCi == "") && customBuild == "" && !launchWithPr && stream == "" {
				modals.OverwriteView(updater, launch.FilterVersionView(callback, jobmanager, submissionData, httpclient, sets.New(mode...), true), callback, logger)
			} else if (nightlyOrCi != "" || customBuild != "") && launchWithPr {
				modals.OverwriteView(updater, launch.PRInputView(callback, submissionData, string(launch.IdentifierFilterVersionView)), callback, logger)
			} else if (nightlyOrCi != "" || customBuild != "") && !launchWithPr {
				modals.OverwriteView(updater, launch.ThirdStepView(callback, jobmanager, httpclient, submissionData, string(launch.IdentifierFilterVersionView)), callback, logger)
			} else {
				modals.OverwriteView(updater, launch.SelectVersionView(callback, jobmanager, httpclient, submissionData, string(launch.IdentifierFilterVersionView)), callback, logger)
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
	nightlyOrCi := submissionData.Input[modals.LaunchFromLatestBuild]
	if nightlyOrCi != "" {
		errs[modals.LaunchFromLatestBuild] = "Select only one parameter!"
	}
	customBuild := submissionData.Input[modals.LaunchFromCustom]
	if customBuild != "" {
		errs[modals.LaunchFromCustom] = "Select only one parameter!"
	}
	selectedStream := submissionData.Input[modals.LaunchFromStream]
	if selectedStream != "" {
		errs[modals.LaunchFromStream] = "Select only one parameter!"
	}
	if !checkVariables(nightlyOrCi, customBuild, selectedStream) {
		response, err := modals.ValidationError(errs)
		if err == nil {
			return response
		}
	}
	return nil
}
