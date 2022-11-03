package bug

import (
	"encoding/json"
	"text/template"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	"github.com/openshift/ci-chat-bot/pkg/jira"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/helpdesk"
)

// Identifier is the view identifier for this modal
const Identifier modals.Identifier = "bug"

const (
	blockIdQuestion     = "question"
	blockIdCategory     = "category"
	blockIdOptional     = "optional"
	blockIdSymptom      = "symptom"
	blockIdExpected     = "expected"
	blockIdImpact       = "impact"
	blockIdReproduction = "reproduction"

	optionClusterBot        = "Cluster Bot"
	optionSearch            = "CI Search"
	optionReleaseController = "Release Controller"
	optionOther             = "Other"
)

// View is the modal view for submitting a new bug to Jira
func View() slack.ModalViewRequest {
	return slack.ModalViewRequest{
		Type:            slack.VTModal,
		PrivateMetadata: string(Identifier),
		Title:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "File a Bug"},
		Close:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Cancel"},
		Submit:          &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Submit"},
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			&slack.SectionBlock{
				Type: slack.MBTSection,
				Text: &slack.TextBlockObject{
					Type: slack.PlainTextType,
					Text: "Use this form to report a bug for the CRT projects.",
				},
			},
			&slack.SectionBlock{
				Type: slack.MBTSection,
				Text: &slack.TextBlockObject{
					Type: slack.PlainTextType,
					Text: "Please be certain that what you are reporting is a bug in the system. If it's not clear, please use the 'Ask a Question' workflow",
				},
				Accessory: &slack.Accessory{
					ButtonElement: &slack.ButtonBlockElement{
						Type:  slack.METButton,
						Text:  &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Ask a Question"},
						Value: blockIdQuestion,
					},
				},
			},
			&slack.DividerBlock{
				Type: slack.MBTDivider,
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: modals.BlockIdTitle,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Provide a title for this bug:"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdCategory,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "What CRT project/component is affected?"},
				Element: &slack.SelectBlockElement{
					Type:        slack.OptTypeStatic,
					Placeholder: &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Select a category..."},
					Options: []*slack.OptionBlockObject{
						{Value: optionClusterBot, Text: &slack.TextBlockObject{Type: slack.PlainTextType, Text: optionClusterBot}},
						{Value: optionSearch, Text: &slack.TextBlockObject{Type: slack.PlainTextType, Text: optionSearch}},
						{Value: optionReleaseController, Text: &slack.TextBlockObject{Type: slack.PlainTextType, Text: optionReleaseController}},
						{Value: optionOther, Text: &slack.TextBlockObject{Type: slack.PlainTextType, Text: optionOther}},
					},
				},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  blockIdOptional,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "If other, what best describes the bugged component?"},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.DividerBlock{
				Type: slack.MBTDivider,
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdSymptom,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "What incorrect behavior did you notice?"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput, Multiline: true},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdExpected,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "What behavior did you expect instead?"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput, Multiline: true},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdImpact,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "What is the impact of this bug?"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput, Multiline: true},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdReproduction,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Is this bug reproducible? If so, how?"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput, Multiline: true},
			},
		}},
	}
}

// helpdeskButtonHandler redirects the user to the helpdesk flow
// if they request it via button push
func helpdeskButtonHandler(updater modals.ViewUpdater) interactions.Handler {
	return interactions.HandlerFromPartial(modals.UpdateViewForButtonPress(string(Identifier)+".question", blockIdQuestion, updater, helpdesk.View()))
}

// validateSubmissionHandler validates user input
func validateSubmissionHandler() interactions.PartialHandler {
	return interactions.PartialHandlerFunc(string(Identifier)+".validate", func(callback *slack.InteractionCallback, logger *logrus.Entry) (bool, []byte, error) {
		// if someone selected the "other" component, make sure they fill in the field
		otherSelected := false
		for _, action := range callback.View.State.Values[blockIdCategory] {
			if action.SelectedOption.Value == optionOther {
				otherSelected = true
			}
		}
		if otherSelected {
			for _, action := range callback.View.State.Values[blockIdOptional] {
				if action.Value == "" {
					logger.Debug("Detected invalid submission.")
					response, err := json.Marshal(&slack.ViewSubmissionResponse{
						ResponseAction: slack.RAErrors,
						Errors: map[string]string{
							blockIdOptional: "Provide a description of the other component.",
						},
					})
					if err != nil {
						logger.WithError(err).Error("Failed to marshal view submission response.")
						return true, nil, err
					}
					return true, response, nil
				}
			}
		}
		return false, nil, nil
	})
}

func issueParameters() modals.JiraIssueParameters {
	return modals.JiraIssueParameters{
		Id:        Identifier,
		IssueType: jira.IssueTypeBug,
		Template: template.Must(template.New(string(Identifier)).Parse(`h3. Symptomatic Behavior
{{ .` + blockIdSymptom + ` }}

h3. Expected Behavior
{{ .` + blockIdExpected + ` }}

h3. Impact
{{ .` + blockIdImpact + ` }}

h3. Category
{{ if eq .` + blockIdCategory + `_static_select "Other" }}Other: {{ .` + blockIdOptional + ` }}{{ else }}{{ .` + blockIdCategory + `_static_select }}{{ end }}

h3. How to Reproduce
{{ .` + blockIdReproduction + ` }}`)),
		Fields: []string{modals.BlockIdTitle, blockIdCategory, blockIdOptional, blockIdSymptom, blockIdExpected, blockIdImpact, blockIdReproduction},
	}
}

// processSubmissionHandler files a Jira issue for this form
func processSubmissionHandler(filer jira.IssueFiler, updater modals.ViewUpdater) interactions.PartialHandler {
	return interactions.PartialFromHandler(modals.ToJiraIssue(issueParameters(), filer, updater))
}

// Register creates a registration entry for the bug form
func Register(filer jira.IssueFiler, client *slack.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(Identifier, View()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeBlockActions: helpdeskButtonHandler(client),
		slack.InteractionTypeViewSubmission: interactions.MultiHandler(
			validateSubmissionHandler(),
			processSubmissionHandler(filer, client),
		),
	})
}
