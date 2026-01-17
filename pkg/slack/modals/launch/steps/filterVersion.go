package steps

import (
	"net/http"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/common"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/launch"
	"github.com/slack-go/slack"
)

func RegisterFilterVersion(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(launch.IdentifierFilterVersionView, launch.FilterVersionView(nil, jobmanager, modals.CallbackData{}, httpclient, nil, false)).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: common.MakeFilterVersionHandler(
			string(launch.IdentifierFilterVersionView),
			launch.ModalTitle,
			string(launch.IdentifierSelectVersion),
			common.ViewFuncs{
				FilterVersionView: launch.FilterVersionView,
				PRInputView:       launch.PRInputView,
				ThirdStepView:     launch.ThirdStepView,
				SelectVersionView: launch.SelectVersionView,
			},
		)(client, jobmanager, httpclient),
	})
}
