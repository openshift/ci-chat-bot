package modals

import (
	"github.com/slack-go/slack"

	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
)

// Identifier identifies a modal View, either in an interaction
// callback identifier from Slack or in private metadata on the View
// as set by this bot when we publish the View. We need this mechanism
// as there's no other way to associate interaction payloads for a modal
// with the code that created the modal in the first place
type Identifier string

// ForView begins a registration process for a modal View
func ForView(id Identifier, view slack.ModalViewRequest) *FlowWithView {
	return &FlowWithView{Identifier: id, View: view}
}

// FlowWithView holds the data for the first step in registration
type FlowWithView struct {
	// Identifier is how we identify callbacks for this modal
	Identifier Identifier
	// View is the modal we create for a user
	View slack.ModalViewRequest
}

// WithFollowUps adds follow-up handlers for a modal View
func (f *FlowWithView) WithFollowUps(followUps map[slack.InteractionType]interactions.Handler) *FlowWithViewAndFollowUps {
	return &FlowWithViewAndFollowUps{
		FlowWithView: f,
		FollowUps:    followUps,
	}
}

// FlowWithViewAndFollowUps holds the data for the second step in registration
type FlowWithViewAndFollowUps struct {
	*FlowWithView
	// FollowUps are what we do when the user interacts
	// with and submits this modal
	FollowUps map[slack.InteractionType]interactions.Handler
}
