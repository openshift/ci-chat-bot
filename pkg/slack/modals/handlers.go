package modals

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	"maps"

	"github.com/openshift/ci-chat-bot/pkg/jira"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
)

const (
	// BlockIdTitle is the block identifier to use for inputs
	// that should be used as the title of a Jira issue
	BlockIdTitle                     = "title"
	IdentifierJira        Identifier = "jira"
	IdentifierJiraPending Identifier = "jira_pending"
	IdentifierError       Identifier = "error"
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
					Text: "This feature is not implemented. Please contact #forum-ocp-crt for more details",
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

func SubmitPrepare(title, modalName string, logger *logrus.Entry) ([]byte, error) {
	response, err := json.Marshal(&slack.ViewSubmissionResponse{
		ResponseAction: slack.RAUpdate,
		View:           PrepareNextStepView(title),
	})
	if err != nil {
		logger.WithError(err).Errorf("Failed to marshal %s update submission response.", modalName)
		return nil, err
	}
	return response, err
}

func PrepareNextStepView(title string) *slack.ModalViewRequest {
	return &slack.ModalViewRequest{
		Type:  slack.VTModal,
		Title: &slack.TextBlockObject{Type: slack.PlainTextType, Text: title},
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			&slack.SectionBlock{
				Type: slack.MBTSection,
				Text: &slack.TextBlockObject{
					Type: slack.MarkdownType,
					Text: "Processing the next step, do not close this window...",
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

func SubmissionView(title, msg string) slack.ModalViewRequest {
	return slack.ModalViewRequest{
		Type:  slack.VTModal,
		Title: &slack.TextBlockObject{Type: slack.PlainTextType, Text: title},
		Close: &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Close"},
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			&slack.SectionBlock{
				Type: slack.MBTSection,
				Text: &slack.TextBlockObject{
					Type: slack.MarkdownType,
					Text: msg,
				},
			},
		}},
	}
}

func OverwriteView(client ViewUpdater, view slack.ModalViewRequest, callback *slack.InteractionCallback, logger *logrus.Entry) {
	// don't pass a hash, so we overwrite the View always
	response, err := client.UpdateView(view, "", "", callback.View.ID)
	if err != nil {
		logger.WithError(err).WithField("messages", response.ResponseMetadata.Messages).Warn("Failed to update a modal View.")
		_, err := client.UpdateView(ErrorView("updating the modal view", err), "", "", callback.View.ID)
		if err != nil {
			logger.WithError(err).Warn("failed to update a modal View.")
		}
	}
	logger.WithField("response", response).Trace("Got a modal response.")
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

func CallbackSelection(callback *slack.InteractionCallback) map[string]string {
	selectionValues := make(map[string]string)
	for key, value := range callback.View.State.Values {
		for _, v := range value {
			if v.SelectedOption.Value != "" {
				selectionValues[key] = v.SelectedOption.Text.Text
			}
			if v.SelectedUser != "" {
				selectionValues[key] = v.SelectedUser
			}
		}
	}
	return selectionValues
}

func CallbackInput(callback *slack.InteractionCallback) map[string]string {
	inputValues := make(map[string]string)
	for key, value := range callback.View.State.Values {
		for _, v := range value {
			if v.Value != "" {
				inputValues[key] = v.Value
			}
		}
	}
	return inputValues
}

func CallbackMultipleSelect(callback *slack.InteractionCallback) map[string][]string {
	selectedValues := make(map[string][]string)
	for key, value := range callback.View.State.Values {
		var selections []string
		for _, v := range value {
			if len(v.SelectedOptions) > 0 {
				for _, selection := range v.SelectedOptions {
					selections = append(selections, selection.Value)
				}
				selectedValues[key] = selections
			}
		}
	}
	return selectedValues
}

func CallBackInputAll(callback *slack.InteractionCallback) map[string]string {
	merged := make(map[string]string, 0)
	selectionValues := CallbackSelection(callback)
	inputValues := CallbackInput(callback)
	maps.Copy(merged, selectionValues)
	maps.Copy(merged, inputValues)
	return merged
}

func ValidationError(errors map[string]string) ([]byte, error) {
	response, err := json.Marshal(&slack.ViewSubmissionResponse{
		ResponseAction: slack.RAErrors,
		Errors:         errors,
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func BuildOptions(options []string, blacklist sets.Set[string]) []*slack.OptionBlockObject {
	slackOptions := make([]*slack.OptionBlockObject, 0)
	for _, parameter := range options {
		if !blacklist.Has(parameter) {
			slackOptions = append(slackOptions, &slack.OptionBlockObject{
				Value: parameter,
				Text: &slack.TextBlockObject{
					Type: slack.PlainTextType,
					Text: parameter,
				},
			})
		}
	}
	return slackOptions
}

func MergeCallbackData(callback *slack.InteractionCallback) CallbackData {
	merged := CallbackData{}
	if err := json.Unmarshal([]byte(callback.View.PrivateMetadata), &merged); err != nil {
		klog.Errorf("Failed to unmarshal private metadata: %v", err)
	}
	if merged.Input == nil {
		merged.Input = make(map[string]string)
	}
	maps.Copy(merged.Input, CallBackInputAll(callback))
	if merged.MultipleSelection == nil {
		merged.MultipleSelection = make(map[string][]string)
	}
	maps.Copy(merged.MultipleSelection, CallbackMultipleSelect(callback))
	return merged
}

func CallbackDataToMetadata(data CallbackData, identifier string) string {
	dataWithIdentifier := CallbackDataAndIdentifier{
		data,
		identifier,
	}
	privateMetadata, err := json.Marshal(dataWithIdentifier)
	if err != nil {
		klog.Errorf("Failed to marshal callback data: %v", err)
	}
	return string(privateMetadata)
}

// CallbackData contains only the user-generated input portion of slack.InteractionCallback
type CallbackData struct {
	Input             map[string]string
	MultipleSelection map[string][]string
}
type CallbackDataAndIdentifier struct {
	CallbackData
	Identifier string
}
