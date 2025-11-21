package router

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/orgdata"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/auth"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/done"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/launch/steps"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/list"
	mceauth "github.com/openshift/ci-chat-bot/pkg/slack/modals/mce/auth"
	mcecreate "github.com/openshift/ci-chat-bot/pkg/slack/modals/mce/create/steps"
	mcedelete "github.com/openshift/ci-chat-bot/pkg/slack/modals/mce/delete"
	mcelist "github.com/openshift/ci-chat-bot/pkg/slack/modals/mce/list"
	mcelookup "github.com/openshift/ci-chat-bot/pkg/slack/modals/mce/lookup"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/refresh"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/stepsFromApp"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

// ForModals returns a Handler that appropriately routes
// interaction callbacks for the modals we know about
func ForModals(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client, authService *orgdata.AuthorizationService) interactions.Handler {
	router := &modalRouter{
		slackClient:         client,
		authService:         authService,
		viewsByID:           map[modals.Identifier]slack.ModalViewRequest{},
		handlersByIDAndType: map[modals.Identifier]map[slack.InteractionType]interactions.Handler{},
	}

	toRegister := []*modals.FlowWithViewAndFollowUps{
		steps.RegisterFirstStep(client, jobmanager, httpclient),
		steps.RegisterLaunchModeStep(client, jobmanager, httpclient),
		steps.RegisterLaunchOptionsStep(client, jobmanager, httpclient),
		steps.RegisterSelectVersion(client, jobmanager, httpclient),
		steps.RegisterFilterVersion(client, jobmanager, httpclient),
		steps.RegisterPRInput(client, jobmanager, httpclient),
		steps.RegisterSelectMinorMajor(client, jobmanager, httpclient),
		list.Register(client, jobmanager),
		auth.Register(client, jobmanager),
		done.Register(client, jobmanager),
		refresh.Register(client, jobmanager),
		mcecreate.RegisterFirstStep(client, jobmanager, httpclient),
		mcecreate.RegisterLaunchModeStep(client, jobmanager, httpclient),
		mcecreate.RegisterSelectVersion(client, jobmanager, httpclient),
		mcecreate.RegisterFilterVersion(client, jobmanager, httpclient),
		mcecreate.RegisterPRInput(client, jobmanager, httpclient),
		mcecreate.RegisterSelectMinorMajor(client, jobmanager, httpclient),
		mcecreate.RegisterCreateConfirmStep(client, jobmanager, httpclient),
		mceauth.Register(client, jobmanager, httpclient),
		mcelist.Register(client, jobmanager, httpclient),
		mcedelete.Register(client, jobmanager),
		mcelookup.Register(client, jobmanager, httpclient),
	}

	for _, entry := range toRegister {
		router.viewsByID[entry.Identifier] = entry.View
		router.handlersByIDAndType[entry.Identifier] = entry.FollowUps
	}

	return router
}

type modalRouter struct {
	slackClient slackClient
	authService *orgdata.AuthorizationService

	// viewsById maps callback IDs to modal flows, for triggering
	// modals as a response to short-cut interaction events
	viewsByID map[modals.Identifier]slack.ModalViewRequest
	// handlersByIdAndType holds handlers for different types of
	// interaction payloads, further mapping to identifiers we
	// store in private metadata for routing
	handlersByIDAndType map[modals.Identifier]map[slack.InteractionType]interactions.Handler
}

// Handle routes the interaction callback to the appropriate handler
func (r *modalRouter) Handle(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
	switch callback.Type {
	case slack.InteractionTypeWorkflowStepEdit:
		return nil, r.viewForApplicationStep(callback, logger)
	case slack.InteractionTypeShortcut:
		return nil, r.viewForShortcut(callback, logger)
	case slack.InteractionTypeBlockActions:
		if isMessageButtonPress(callback) {
			return nil, r.viewForButton(callback, logger)
		}
		return r.delegate(callback, logger)
	case slack.InteractionTypeViewSubmission:
		if metadataToIdentifier(callback.View.PrivateMetadata, logger) == string(slack.InteractionTypeWorkflowStepEdit) {
			input, output := stepsFromApp.StepFromAppSubmit(callback)
			return nil, r.slackClient.SaveWorkflowStepConfiguration(callback.WorkflowStep.WorkflowStepEditID, &input, &output)
		}
		return r.delegate(callback, logger)
	default:
		return r.delegate(callback, logger)
	}
}

// isMessageButtonPress determines if an interaction callback is for a button press in a message
func isMessageButtonPress(callback *slack.InteractionCallback) bool {
	return len(callback.ActionCallback.BlockActions) > 0 && callback.ActionCallback.BlockActions[0].Type == "button"
}

type slackClient interface {
	OpenView(triggerID string, view slack.ModalViewRequest) (*slack.ViewResponse, error)
	SaveWorkflowStepConfiguration(workflowStepEditID string, inputs *slack.WorkflowStepInputs, outputs *[]slack.WorkflowStepOutput) error
}

// viewForShortcut reacts to the original shortcut action from the user
// to open the first modal view for them
func (r *modalRouter) viewForShortcut(callback *slack.InteractionCallback, logger *logrus.Entry) error {
	id := modals.Identifier(callback.CallbackID)
	return r.openModal(id, callback.TriggerID, logger)
}

func (r *modalRouter) viewForApplicationStep(callback *slack.InteractionCallback, logger *logrus.Entry) error {
	response, err := r.slackClient.OpenView(callback.TriggerID, stepsFromApp.WorkflowStepEditView(callback))
	if err != nil {
		logger.WithError(err).Warn("Failed to open the workflow_step_edit view.")
	}
	logger.WithField("response", response).Trace("Received a workflow_step_edit request")
	return err
}

// viewForButton reacts to the a user pressing a button in a bot message
// to open the a modal view for them
func (r *modalRouter) viewForButton(callback *slack.InteractionCallback, logger *logrus.Entry) error {
	id := modals.Identifier(callback.ActionCallback.BlockActions[0].Value)

	// Check authorization for protected commands
	if authorized, denyMessage := r.checkModalAuthorization(string(id), callback.User.ID); !authorized {
		logger.Infof("User %s denied access to modal %s", callback.User.ID, id)
		return r.showUnauthorizedModal(callback.TriggerID, string(id), denyMessage, logger)
	}

	return r.openModal(id, callback.TriggerID, logger)
}

func (r *modalRouter) openModal(id modals.Identifier, triggerID string, logger *logrus.Entry) error {
	logger = logger.WithField("view_id", id)
	logger.Infof("Opening modal view %s.", id)
	view, exists := r.viewsByID[id]
	if id != "" && !exists {
		logger.Debug("Unknown callback ID.")
		return nil
	}

	response, err := r.slackClient.OpenView(triggerID, view)
	if err != nil {
		logger.WithError(err).WithField("messages", response.ResponseMetadata.Messages).Warn("Failed to open a modal flow.")
	}
	logger.WithField("response", response).Trace("Got a modal response.")
	return err
}

// delegate routes the interaction callback to the appropriate handler
func (r *modalRouter) delegate(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
	id := modals.Identifier(metadataToIdentifier(callback.View.PrivateMetadata, logger))
	logger = logger.WithField("view_id", id)
	handlersForId, registered := r.handlersByIDAndType[id]
	if !registered {
		handlersForId, registered = r.handlersByIDAndType[modals.Identifier(callback.ActionCallback.BlockActions[0].ActionID)]
		if !registered {
			logger.Debugf("Received a callback ID (%s) for which no handlers were registered.", id)
			return nil, nil
		}
	}
	handler, exists := handlersForId[callback.Type]
	if !exists {
		logger.Debugf("Received a callback ID (%s) and type (%s) for which no handlers were registered.", callback.Type, id)
		return nil, nil
	}
	return handler.Handle(callback, logger)
}

func (r *modalRouter) Identifier() string {
	return "modal"
}

func metadataToIdentifier(privateMetadata string, logger *logrus.Entry) string {
	dataAndIdentifier := CallbackDataAndIdentifier{}
	if err := json.Unmarshal([]byte(privateMetadata), &dataAndIdentifier); err != nil {
		logger.Errorf("Failed to unmarshal private metadata: %v", err)
	}
	return dataAndIdentifier.Identifier
}

type CallbackDataAndIdentifier struct {
	Input             map[string]string
	MultipleSelection map[string][]string
	Context           map[string]string
	Identifier        string
}

// commandNameForModal maps modal identifiers to their corresponding command names
// for authorization checking. This ensures modals use the same authorization rules
// as their text-based command equivalents.
var commandNameForModal = map[string]string{
	"launch":     "launch",
	"done":       "done",
	"mce_create": "mce_create",
	"mce_delete": "mce_delete",
	// Read-only commands that don't require authorization
	// "list", "auth", "refresh", "mce_auth", "mce_list", "mce_lookup"
}

// checkModalAuthorization checks if a user is authorized to use a modal
func (r *modalRouter) checkModalAuthorization(modalID string, userID string) (bool, string) {
	// If no authorization service, allow all access
	if r.authService == nil {
		return true, ""
	}

	// Get the command name for this modal
	commandName, requiresAuth := commandNameForModal[modalID]
	if !requiresAuth {
		// Modal doesn't require authorization (read-only command)
		return true, ""
	}

	// Check authorization using the command name
	return r.authService.CheckAuthorization(userID, commandName)
}

// showUnauthorizedModal displays an error modal when a user is not authorized
func (r *modalRouter) showUnauthorizedModal(triggerID string, modalID string, denyMessage string, logger *logrus.Entry) error {
	if denyMessage == "" {
		denyMessage = fmt.Sprintf("You are not authorized to use the `%s` command. Please contact your administrator for access.", modalID)
	}

	unauthorizedView := slack.ModalViewRequest{
		Type:  slack.VTModal,
		Title: &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Access Denied"},
		Close: &slack.TextBlockObject{Type: slack.PlainTextType, Text: "OK"},
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			&slack.SectionBlock{
				Type: slack.MBTSection,
				Text: &slack.TextBlockObject{
					Type: slack.MarkdownType,
					Text: fmt.Sprintf("🚫 *Authorization Required*\n\n%s", denyMessage),
				},
			},
		}},
	}

	response, err := r.slackClient.OpenView(triggerID, unauthorizedView)
	if err != nil {
		logger.WithError(err).WithField("messages", response.ResponseMetadata.Messages).Warn("Failed to open unauthorized modal.")
	}
	logger.WithField("response", response).Trace("Showed unauthorized modal.")
	return err
}
