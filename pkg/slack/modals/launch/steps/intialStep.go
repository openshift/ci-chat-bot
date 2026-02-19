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

func RegisterFirstStep(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(launch.IdentifierInitialView, launch.FirstStepView()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: common.MakeFirstStepHandler(
			string(launch.IdentifierInitialView),
			launch.ModalTitle,
			launch.SelectModeView,
			common.FirstStepConfig{
				DefaultPlatform:     launch.DefaultPlatform,
				DefaultArchitecture: launch.DefaultArchitecture,
				NeedsArchitecture:   true,
			},
		)(client, jobmanager, httpclient),
	})
}
