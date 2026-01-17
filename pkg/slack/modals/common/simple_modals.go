package common

import (
	"strings"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

// SimpleModalConfig holds configuration for simple modal registration
type SimpleModalConfig struct {
	Identifier string
	Title      string
	ViewFunc   func() slack.ModalViewRequest
}

// RegisterSimpleModal creates a standard modal registration with view submission handler
func RegisterSimpleModal(
	config SimpleModalConfig,
	processFunc func(*slack.Client, manager.JobManager) interactions.Handler,
) func(*slack.Client, manager.JobManager) *modals.FlowWithViewAndFollowUps {
	return func(client *slack.Client, jobmanager manager.JobManager) *modals.FlowWithViewAndFollowUps {
		return modals.ForView(modals.Identifier(config.Identifier), config.ViewFunc()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
			slack.InteractionTypeViewSubmission: processFunc(client, jobmanager),
		})
	}
}

// ProcessHandlerFunc is a function that performs the modal's main action
type ProcessHandlerFunc func(manager.JobManager, *slack.InteractionCallback) (string, error)

// MakeSimpleProcessHandler creates a standard process handler that:
// 1. Runs the action asynchronously
// 2. Handles errors with ErrorView
// 3. Shows success with SubmissionView
// 4. Returns SubmitPrepare immediately
func MakeSimpleProcessHandler(
	identifier string,
	title string,
	actionFunc ProcessHandlerFunc,
	errorContext string,
) func(*slack.Client, manager.JobManager) interactions.Handler {
	return func(updater *slack.Client, jobManager manager.JobManager) interactions.Handler {
		return interactions.HandlerFunc(identifier, func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
			go func() {
				msg, err := actionFunc(jobManager, callback)
				if err != nil {
					modals.OverwriteView(updater, modals.ErrorView(errorContext, err), callback, logger)
					return
				}
				modals.OverwriteView(updater, modals.SubmissionView(title, msg), callback, logger)
			}()
			return modals.SubmitPrepare(title, identifier, logger)
		})
	}
}

// BuildSimpleView creates a simple modal view with a single section message
func BuildSimpleView(identifier, title, message string) slack.ModalViewRequest {
	return slack.ModalViewRequest{
		Type:            slack.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(modals.CallbackData{}, identifier),
		Title:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: title},
		Close:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Cancel"},
		Submit:          &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Submit"},
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			&slack.SectionBlock{
				Type: slack.MBTSection,
				Text: &slack.TextBlockObject{
					Type: slack.MarkdownType,
					Text: message,
				},
			},
		}},
	}
}

// AppendKubeconfigBlock appends a kubeconfig display block to a modal view
func AppendKubeconfigBlock(view *slack.ModalViewRequest, kubeconfig, headerText string) {
	if kubeconfig == "" {
		return
	}
	view.Blocks.BlockSet = append(view.Blocks.BlockSet,
		slack.NewDividerBlock(),
		slack.NewHeaderBlock(slack.NewTextBlockObject(slack.PlainTextType, headerText, true, false)),
		slack.NewRichTextBlock("kubeconfig", &slack.RichTextPreformatted{
			RichTextSection: slack.RichTextSection{
				Type: slack.RTEPreformatted,
				Elements: []slack.RichTextSectionElement{
					slack.NewRichTextSectionTextElement(kubeconfig, &slack.RichTextSectionTextStyle{Code: false}),
				},
			},
		}))
}

// BuildListResultModal creates a modal view for displaying list results
func BuildListResultModal(title, beginning string, elements []string) slack.ModalViewRequest {
	submission := slack.ModalViewRequest{
		Type:  slack.VTModal,
		Title: &slack.TextBlockObject{Type: slack.PlainTextType, Text: title},
		Close: &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Close"},
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			slack.NewRichTextBlock("beginning", slack.NewRichTextSection(slack.NewRichTextSectionTextElement(beginning, &slack.RichTextSectionTextStyle{}))),
		}},
	}
	for _, element := range elements {
		submission.Blocks.BlockSet = append(submission.Blocks.BlockSet, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, element, false, false), nil, nil))
	}
	return submission
}

// MetadataBuilder helps construct context metadata strings
type MetadataBuilder struct {
	parts []string
}

// NewMetadataBuilder creates a new metadata builder
func NewMetadataBuilder() *MetadataBuilder {
	return &MetadataBuilder{parts: nil}
}

// Add adds a key-value pair to the metadata
func (mb *MetadataBuilder) Add(key, value string) *MetadataBuilder {
	if value != "" {
		mb.parts = append(mb.parts, key+": "+value)
	}
	return mb
}

// Build returns the formatted metadata string
func (mb *MetadataBuilder) Build() string {
	return strings.Join(mb.parts, "; ")
}
