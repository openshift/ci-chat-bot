package steps

import (
	"net/http"
	"strings"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/mce/create"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog"
)

func RegisterFilterVersion(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(create.IdentifierFilterVersionView, create.FilterVersionView(nil, nil, modals.CallbackData{}, nil, nil, false)).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextFilterVersion(client, jobmanager, httpclient),
	})
}

func processNextFilterVersion(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(string(create.IdentifierFilterVersionView), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		klog.Infof("Private Metadata: %s", callback.View.PrivateMetadata)
		submissionData := modals.MergeCallbackData(callback)
		errorResponse := validateFilterVersion(submissionData)
		if errorResponse != nil {
			return errorResponse, nil
		}
		nightlyOrCi := submissionData.Input[create.LaunchFromLatestBuild]
		customBuild := submissionData.Input[create.LaunchFromCustom]
		stream := submissionData.Input[create.LaunchFromStream]
		mode := submissionData.MultipleSelection[create.LaunchMode]
		createWithPr := false
		for _, key := range mode {
			if strings.TrimSpace(key) == create.LaunchModePRKey {
				createWithPr = true
			}
		}
		go func() {
			if (nightlyOrCi == "") && customBuild == "" && !createWithPr && stream == "" {
				modals.OverwriteView(updater, create.FilterVersionView(callback, jobmanager, submissionData, httpclient, sets.New(mode...), true), callback, logger)
			} else if (nightlyOrCi != "" || customBuild != "") && createWithPr {
				modals.OverwriteView(updater, create.PRInputView(callback, submissionData), callback, logger)
			} else if (nightlyOrCi != "" || customBuild != "") && !createWithPr {
				modals.OverwriteView(updater, create.ThirdStepView(callback, jobmanager, httpclient, submissionData), callback, logger)
			} else {
				modals.OverwriteView(updater, create.SelectVersionView(callback, jobmanager, httpclient, submissionData), callback, logger)
			}

		}()
		return modals.SubmitPrepare(create.ModalTitle, string(create.IdentifierFilterVersionView), logger)
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
	nightlyOrCi := submissionData.Input[create.LaunchFromLatestBuild]
	if nightlyOrCi != "" {
		errs[create.LaunchFromLatestBuild] = "Select only one parameter!"
	}
	customBuild := submissionData.Input[create.LaunchFromCustom]
	if customBuild != "" {
		errs[create.LaunchFromCustom] = "Select only one parameter!"
	}
	selectedStream := submissionData.Input[create.LaunchFromStream]
	if selectedStream != "" {
		errs[create.LaunchFromStream] = "Select only one parameter!"
	}
	if !checkVariables(nightlyOrCi, customBuild, selectedStream) {
		response, err := modals.ValidationError(errs)
		if err == nil {
			return response
		}
	}
	return nil
}
