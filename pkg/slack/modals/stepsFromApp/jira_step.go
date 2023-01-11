package stepsFromApp

import (
	"github.com/openshift/ci-chat-bot/pkg/slack/events/workflowSubmissionEvents"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/slack-go/slack"
)

const Identifier modals.Identifier = "workflow_step_edit"

const (
	TicketTitle = "ticket_title"
	ticketType  = "ticket_type"
	UserDetails = "user_details"
)

func WorkflowStepEditView(callback *slack.InteractionCallback) slack.ModalViewRequest {
	return slack.ModalViewRequest{
		Type:            slack.VTWorkflowStep,
		PrivateMetadata: string(Identifier),
		CallbackID:      callback.CallbackID,
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			// using a multi-select does not seem to work here (the selected option is always empty in the input, for
			// some reason). Using a text-block instead, with a hint on currently supported options. If the input is not
			// supported, it is detected and handled when handling the workflow_step_execute event
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: ticketType,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Supported ticket type (currently supported: bug, enhancement, consultation)"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: UserDetails,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Include the user details (the one who initiates the workflow)"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: TicketTitle,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Ticket Title (one-line summary)"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.DividerBlock{
				Type:    slack.MBTDivider,
				BlockID: "bug_divider",
			},
			&slack.HeaderBlock{
				Type:    slack.MBTHeader,
				BlockID: "bug_divider_header",
				Text:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "These are bug specific configurations:"},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  workflowSubmissionEvents.AffectedComponent,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Bugs: Affected component"},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  workflowSubmissionEvents.OtherComponent,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Bugs: Other component if not listed"},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  workflowSubmissionEvents.IncorrectBehaviour,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Bugs: Incorrect behaviour"},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  workflowSubmissionEvents.ExpectedBehaviour,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Bugs: Expected behaviour"},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  workflowSubmissionEvents.Impact,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Bugs: Impact"},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  workflowSubmissionEvents.IsReproducible,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Bugs: Is it reproducible"},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.DividerBlock{
				Type:    slack.MBTDivider,
				BlockID: "consultation_divider",
			},
			&slack.HeaderBlock{
				Type:    slack.MBTHeader,
				BlockID: "consultation_divider_header",
				Text:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "These are consultation specific configurations:"},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  workflowSubmissionEvents.BlockIdRequirement,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Consultation: Requirements"},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  workflowSubmissionEvents.BlockIdPrevious,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Consultation: Previous"},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  workflowSubmissionEvents.BlockIdAcceptanceCriteria,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Consultation: Acceptance Criteria"},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  workflowSubmissionEvents.BlockIdAdditional,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Consultation: Additional"},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.DividerBlock{
				Type:    slack.MBTDivider,
				BlockID: "enhancement_divider",
			},
			&slack.HeaderBlock{
				Type:    slack.MBTHeader,
				BlockID: "enhancement_divider_header",
				Text:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "These are enhancement specific configurations:"},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  workflowSubmissionEvents.BlockIdAsA,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Enhancement: As as..."},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  workflowSubmissionEvents.BlockIdIWant,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Enhancement: I want..."},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  workflowSubmissionEvents.BlockIdSoThat,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Enhancement: So that..."},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  workflowSubmissionEvents.BlockIdSummary,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Enhancement: Summary"},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  workflowSubmissionEvents.BlockIdImplementation,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Enhancement: Implementation"},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
		},
		},
	}
}
