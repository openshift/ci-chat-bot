package refresh

import (
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/common"
	slackClient "github.com/slack-go/slack"
)

func View() slackClient.ModalViewRequest {
	return common.BuildSimpleView(
		identifier,
		title,
		"If the cluster is currently marked as failed, retry fetching its credentials in case of an error",
	)
}
