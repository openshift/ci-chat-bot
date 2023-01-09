package router

import (
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/stepsFromApp"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

// ForModals returns a Handler that appropriately routes
// interaction callbacks for the modals we know about
func ForModals(client *slack.Client) interactions.Handler {
	router := &modalRouter{
		slackClient:         client,
		viewsByID:           map[modals.Identifier]slack.ModalViewRequest{},
		handlersByIDAndType: map[modals.Identifier]map[slack.InteractionType]interactions.Handler{},
	}

	toRegister := []*modals.FlowWithViewAndFollowUps{
		// sample entry
		//bug.Register(filer, client),
	}

	for _, entry := range toRegister {
		router.viewsByID[entry.Identifier] = entry.View
		router.handlersByIDAndType[entry.Identifier] = entry.FollowUps
	}

	return router
}

type modalRouter struct {
	slackClient slackClient

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
		if callback.View.PrivateMetadata == string(slack.InteractionTypeWorkflowStepEdit) {
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
	return callback.View.ID == "" && callback.Message.Text != "" && len(callback.ActionCallback.BlockActions) > 0 && callback.ActionCallback.BlockActions[0].Type == "button"
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
		logger.WithError(err).Warn("Failed to open a modal flow.")
	}
	logger.WithField("response", response).Trace("Got a modal response.")
	return err
}

// delegate routes the interaction callback to the appropriate handler
func (r *modalRouter) delegate(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
	id := modals.Identifier(callback.View.PrivateMetadata)
	logger = logger.WithField("view_id", id)
	handlersForId, registered := r.handlersByIDAndType[id]
	if !registered {
		logger.Debugf("Received a callback ID (%s) for which no handlers were registered.", id)
		return nil, nil
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
