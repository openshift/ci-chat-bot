package auth

import (
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	slackClient "github.com/slack-go/slack"
)

func View() slackClient.ModalViewRequest {
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(modals.CallbackData{}, identifier),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: title},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Submit"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			&slackClient.SectionBlock{
				Type: slackClient.MBTSection,
				Text: &slackClient.TextBlockObject{
					Type: slackClient.MarkdownType,
					Text: "Click submit to retrieve the credentials for your cluster",
				},
			},
		}},
	}
}
