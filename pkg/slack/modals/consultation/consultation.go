package consultation

import (
	"text/template"

	"github.com/slack-go/slack"

	"github.com/openshift/ci-chat-bot/pkg/jira"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/helpdesk"
)

// Identifier is the view identifier for this modal
const Identifier modals.Identifier = "consultation"

const (
	blockIdQuestion           = "question"
	blockIdRequirement        = "requirement"
	blockIdPrevious           = "previous"
	blockIdAcceptanceCriteria = "acceptance_criteria"
	blockIdAdditional         = "additional"
)

// View is the modal view for submitting a new consultation request to Jira
func View() slack.ModalViewRequest {
	return slack.ModalViewRequest{
		Type:            slack.VTModal,
		PrivateMetadata: string(Identifier),
		Title:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Request a Consultation"},
		Close:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Cancel"},
		Submit:          &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Submit"},
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			&slack.SectionBlock{
				Type: slack.MBTSection,
				Text: &slack.TextBlockObject{
					Type: slack.PlainTextType,
					Text: "When a team has complex or involved requirements from the Continues Release Team, we can provide an engineer's time to consult on how to best achieve those goals. Use this form to request a consultation.",
				},
			},
			&slack.DividerBlock{
				Type: slack.MBTDivider,
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: modals.BlockIdTitle,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Provide a one-line summary for this consultation:"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdRequirement,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Explain your goals (what are you trying to achieve?):"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput, Multiline: true},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdPrevious,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Explain what you've tried already and list any documents that were helpful or insufficient:"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput, Multiline: true},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdAcceptanceCriteria,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Provide acceptance criteria (one per line) for this consultation, focusing on what is to be achieved, not how:"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput, Multiline: true},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  blockIdAdditional,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Provide any additional information:"},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput, Multiline: true},
			},
		}},
	}
}

// helpdeskButtonHandler redirects the user to the helpdesk flow
// if they request it via button push
func helpdeskButtonHandler(updater modals.ViewUpdater) interactions.Handler {
	return interactions.HandlerFromPartial(modals.UpdateViewForButtonPress(string(Identifier)+".question", blockIdQuestion, updater, helpdesk.View()))
}

func issueParameters() modals.JiraIssueParameters {
	return modals.JiraIssueParameters{
		Id:        Identifier,
		IssueType: jira.IssueTypeStory,
		Template: template.Must(template.New(string(Identifier)).Funcs(modals.BulletListFunc()).Parse(`h3. Requirement
{{ .` + blockIdRequirement + ` }}

h3. Previous Efforts
{{ .` + blockIdPrevious + ` }}

h3. Acceptance Criteria
{{ toBulletList .` + blockIdAcceptanceCriteria + ` }}

{{- if .` + blockIdAdditional + ` }}

h3. Additional Details
{{ .` + blockIdAdditional + ` }}
{{- end }}`)),
		Fields: []string{modals.BlockIdTitle, blockIdRequirement, blockIdPrevious, blockIdAcceptanceCriteria, blockIdAdditional},
	}
}

// processSubmissionHandler files a Jira issue for this form
func processSubmissionHandler(filer jira.IssueFiler, updater modals.ViewUpdater) interactions.Handler {
	return modals.ToJiraIssue(issueParameters(), filer, updater)
}

// Register creates a registration entry for the consultation form
func Register(filer jira.IssueFiler, client *slack.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(Identifier, View()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeBlockActions:   helpdeskButtonHandler(client),
		slack.InteractionTypeViewSubmission: processSubmissionHandler(filer, client),
	})
}
