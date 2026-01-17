package delete

import (
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/common"
	slackClient "github.com/slack-go/slack"
)

func View() slackClient.ModalViewRequest {
	return common.BuildSimpleView(
		identifier,
		title,
		"Click submit to terminate your running cluster",
	)
}
