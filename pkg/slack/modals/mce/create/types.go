package create

import "github.com/openshift/ci-chat-bot/pkg/slack/modals"

const (
	IdentifierInitialView       modals.Identifier = "mce_create"
	IdentifierSelectModeView    modals.Identifier = "mce_mode"
	Identifier3rdStep           modals.Identifier = "mce3rdStep"
	IdentifierPRInputView       modals.Identifier = "mce_pr_input_view"
	IdentifierFilterVersionView modals.Identifier = "mce_filter_version_view"
	IdentifierSelectVersion     modals.Identifier = "mce_select_version"
	IdentifierSelectMinorMajor  modals.Identifier = "mce_select_minor_major"
)

const (
	defaultPlatform = "aws"
	defaultDuration = "6h"
	CreateDuration  = "duration"
	ModalTitle      = "Launch an MCE Cluster"
)
