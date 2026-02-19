package steps

import (
	"net/http"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/common"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/mce/create"
	"github.com/slack-go/slack"
)

func RegisterFilterVersion(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(create.IdentifierFilterVersionView, create.FilterVersionView(nil, jobmanager, modals.CallbackData{}, httpclient, nil, false)).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: common.MakeFilterVersionHandler(
			string(create.IdentifierFilterVersionView),
			create.ModalTitle,
			string(create.IdentifierSelectVersion),
			common.ViewFuncs{
				FilterVersionView: create.FilterVersionView,
				PRInputView:       create.PRInputView,
				ThirdStepView:     create.ThirdStepView,
				SelectVersionView: create.SelectVersionView,
			},
		)(client, jobmanager, httpclient),
	})
}
