package modals

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	"github.com/openshift/ci-chat-bot/pkg/jira"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
)

const (
	// BlockIdTitle is the block identifier to use for inputs
	// that should be used as the title of a Jira issue
	BlockIdTitle = "title"
)

// ViewUpdater is a subset of the Slack client
type ViewUpdater interface {
	UpdateView(view slack.ModalViewRequest, externalID, hash, viewID string) (*slack.ViewResponse, error)
}

// UpdateViewForButtonPress updates the given View if the interaction
// being handled was the identified button being pushed
func UpdateViewForButtonPress(identifier, buttonID string, updater ViewUpdater, view slack.ModalViewRequest) interactions.PartialHandler {
	return interactions.PartialHandlerFunc(identifier, func(callback *slack.InteractionCallback, logger *logrus.Entry) (bool, []byte, error) {
		// if someone pushed the identified button, show them that form
		if len(callback.ActionCallback.BlockActions) > 0 {
			action := callback.ActionCallback.BlockActions[0]
			if action.Type == "button" && action.Value == buttonID {
				logger.Debugf("The %s button was pressed, updating the View for handler %s", buttonID, identifier)
				response, err := updater.UpdateView(view, "", callback.View.Hash, callback.View.ID)
				if err != nil {
					logger.WithError(err).Warn("Failed to update a modal View.")
				}
				logger.WithField("response", response).Trace("Got a modal response.")
				return true, nil, err
			}
		}
		return false, nil, nil
	})
}

// JiraIssueParameters holds the metadata used to create a Jira issue
type JiraIssueParameters struct {
	Id        Identifier
	IssueType string
	Template  *template.Template
	Fields    []string
}

// Process processes the interaction callback data to render the Jira issue title and body
func (p *JiraIssueParameters) Process(callback *slack.InteractionCallback) (string, string, error) {
	data := valuesFor(callback, p.Fields...)
	body := &bytes.Buffer{}
	if err := p.Template.Execute(body, data); err != nil {
		return "", "", fmt.Errorf("failed to render %s template: %w", p.Id, err)
	}
	return data[BlockIdTitle], body.String(), nil
}

// ToJiraIssue responds to the user with a confirmation screen and files
// a Jira issue behind the scenes, updating the View once the operation
// has finished. We need this asynchronous response mechanism as the API
// calls needed to file the issue often take longer than the 3sec TTL on
// responding to the interaction payload we have.
func ToJiraIssue(parameters JiraIssueParameters, filer jira.IssueFiler, updater ViewUpdater) interactions.Handler {
	return interactions.HandlerFunc(string(parameters.Id)+".jira", func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		logger.Infof("Submitting new %s to Jira.", parameters.Id)
		if filer == nil {
			// respond to the HTTP payload from Slack with a submission response
			response, err := json.Marshal(&slack.ViewSubmissionResponse{
				ResponseAction: slack.RAUpdate,
				View:           NotEnabledView(),
			})
			if err != nil {
				logger.WithError(err).Error("Failed to marshal View update submission response.")
				return nil, err
			}
			return response, nil
		}
		go func() {
			overwriteView := func(view slack.ModalViewRequest) {
				// don't pass a hash so we overwrite the View always
				response, err := updater.UpdateView(view, "", "", callback.View.ID)
				if err != nil {
					logger.WithError(err).Warn("Failed to update a modal View.")
				}
				logger.WithField("response", response).Trace("Got a modal response.")
			}
			title, body, err := parameters.Process(callback)
			if err != nil {
				logger.WithError(err).Warnf("Failed to render %s template.", parameters.Id)
				overwriteView(ErrorView(fmt.Sprintf("render %s template", parameters.Id), err))
				return
			}

			issue, err := filer.FileIssue(parameters.IssueType, title, body, callback.User.ID, logger)
			if err != nil {
				logger.WithError(err).Errorf("Failed to create %s Jira.", parameters.Id)
				overwriteView(ErrorView(fmt.Sprintf("create %s Jira issue", parameters.Id), err))
				return
			}
			overwriteView(JiraView(issue.Key))
		}()

		// respond to the HTTP payload from Slack with a submission response
		response, err := json.Marshal(&slack.ViewSubmissionResponse{
			ResponseAction: slack.RAUpdate,
			View:           PendingJiraView(),
		})
		if err != nil {
			logger.WithError(err).Error("Failed to marshal View update submission response.")
			return nil, err
		}
		return response, nil
	})
}

const (
	IdentifierJira        Identifier = "jira"
	IdentifierJiraPending Identifier = "jira_pending"
	IdentifierError       Identifier = "error"
)

// PendingJiraView is a placeholder modal View for the user
// to know we are working on publishing a Jira issue
func PendingJiraView() *slack.ModalViewRequest {
	return &slack.ModalViewRequest{
		Type:            slack.VTModal,
		PrivateMetadata: string(IdentifierJiraPending),
		Title:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Creating Jira Issue..."},
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			&slack.SectionBlock{
				Type: slack.MBTSection,
				Text: &slack.TextBlockObject{
					Type: slack.MarkdownType,
					Text: "A Jira issue is being filed, please do not close this window...",
				},
			},
		}},
	}
}

// NotEnabledView is a placeholder modal View when a jira filer is not defined (disabled)
func NotEnabledView() *slack.ModalViewRequest {
	return &slack.ModalViewRequest{
		Type:            slack.VTModal,
		PrivateMetadata: string(IdentifierJiraPending),
		Title:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Creating Jira Issue..."},
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			&slack.SectionBlock{
				Type: slack.MBTSection,
				Text: &slack.TextBlockObject{
					Type: slack.MarkdownType,
					Text: "This feature is not implemented. Please contact #forum-crt for more details",
				},
			},
		}},
	}
}

// JiraView is a modal View to show the user the
// Jira issue we just created for them
func JiraView(key string) slack.ModalViewRequest {
	return slack.ModalViewRequest{
		Type:            slack.VTModal,
		PrivateMetadata: string(IdentifierJira),
		Title:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Jira Issue Created"},
		Close:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "OK"},
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			&slack.SectionBlock{
				Type: slack.MBTSection,
				Text: &slack.TextBlockObject{
					Type: slack.MarkdownType,
					Text: fmt.Sprintf("A Jira issue was filed: <https://issues.redhat.com/browse/%s|%s>", key, key),
				},
			},
		}},
	}
}

// ErrorView is a modal View to show the user an error
func ErrorView(action string, err error) slack.ModalViewRequest {
	return slack.ModalViewRequest{
		Type:            slack.VTModal,
		PrivateMetadata: string(IdentifierError),
		Title:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Error Occurred"},
		Close:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "OK"},
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			&slack.SectionBlock{
				Type: slack.MBTSection,
				Text: &slack.TextBlockObject{
					Type: slack.MarkdownType,
					Text: fmt.Sprintf("We encountered an error trying to %s:\n>%v", action, err),
				},
			},
		}},
	}
}

// valuesFor extracts values identified by the block IDs and exposes them
// in a map by their IDs. We bias towards plain text inputs where the block
// Identifier that we set when creating the modal View can fully identify the data,
// and concatenate the block identifier with the input type in other scenarios
func valuesFor(callback *slack.InteractionCallback, blockIds ...string) map[string]string {
	values := map[string]string{}
	for _, id := range blockIds {
		for _, action := range callback.View.State.Values[id] {
			switch string(action.Type) {
			case string(slack.METPlainTextInput):
				// the most common input type is just a plain text, for which
				// there will only ever be one action per block and for which
				// we can identify this data with the block Identifier
				values[id] = action.Value
			// in all other cases - like selectors, etc - we can have more
			// than one input block per block and need to identify each of
			// them with a concatenation of the block Identifier and the type
			case slack.OptTypeChannels:
				values[fmt.Sprintf("%s_%s", id, slack.OptTypeChannels)] = action.SelectedChannel
			case slack.OptTypeConversations:
				values[fmt.Sprintf("%s_%s", id, slack.OptTypeConversations)] = action.SelectedConversation
			case slack.OptTypeUser:
				values[fmt.Sprintf("%s_%s", id, slack.OptTypeUser)] = action.SelectedUser
			case slack.OptTypeStatic:
				values[fmt.Sprintf("%s_%s", id, slack.OptTypeStatic)] = action.SelectedOption.Value
			}
		}
	}
	return values
}

// BulletListFunc exposes a function to turn lines into a bullet list
func BulletListFunc() template.FuncMap {
	return template.FuncMap{
		"toBulletList": func(input string) string {
			var output []string
			for _, line := range strings.Split(input, "\n") {
				if trimmed := strings.TrimSpace(line); trimmed != "" {
					output = append(output, fmt.Sprintf("* %s", trimmed))
				}
			}
			return strings.Join(output, "\n")
		},
	}
}
