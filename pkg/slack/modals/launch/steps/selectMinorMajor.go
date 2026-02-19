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

func RegisterSelectMinorMajor(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(launch.IdentifierSelectMinorMajor, launch.SelectMinorMajor(nil, httpclient, modals.CallbackData{}, "")).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: common.MakeSelectMinorMajorHandler(
			string(launch.IdentifierSelectMinorMajor),
			launch.ModalTitle,
			launch.SelectVersionView,
		)(client, jobmanager, httpclient),
	})
}
