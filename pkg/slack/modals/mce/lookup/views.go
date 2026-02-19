package auth

import (
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/common"
	slackClient "github.com/slack-go/slack"
)

func View() slackClient.ModalViewRequest {
	return common.BuildSimpleView(
		identifier,
		title,
		"Click submit to view all Openshift versions available for MCE.\nNote: CI versions may also be used, but will take longer to launch.",
	)
}
