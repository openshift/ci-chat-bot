package list

import (
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	slackClient "github.com/slack-go/slack"
)

func View() slackClient.ModalViewRequest {
	platformOptions := modals.BuildOptions(manager.SupportedPlatforms, nil)
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(modals.CallbackData{}, identifier),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "List Running Clusters"},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Submit"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			&slackClient.HeaderBlock{
				Type: slackClient.MBTHeader,
				Text: &slackClient.TextBlockObject{
					Type:     "plain_text",
					Text:     "See who is hogging all the clusters",
					Emoji:    false,
					Verbatim: false,
				},
			},
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  filterByPlatform,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Filter By platform:"},
				Element: &slackClient.SelectBlockElement{
					Type:        slackClient.OptTypeStatic,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select a platform"},
					Options:     platformOptions,
				},
			},
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  filterByVersion,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Filter by version"},
				Element: &slackClient.PlainTextInputBlockElement{
					Type:        slackClient.METPlainTextInput,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Enter a version..."},
				},
			},
			&slackClient.SectionBlock{
				Type:    slackClient.MBTSection,
				Text:    &slackClient.TextBlockObject{Type: slackClient.MarkdownType, Text: "*Filter by User*"},
				BlockID: filterByUser,
				Accessory: &slackClient.Accessory{
					SelectElement: &slackClient.SelectBlockElement{
						Type:        "users_select",
						Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select a User"},
						ActionID:    "users_select-action",
					},
				},
			},
		}},
	}
}
