package launch

import "github.com/openshift/ci-chat-bot/pkg/slack/modals"

const (
	IdentifierInitialView        modals.Identifier = "launch"
	Identifier3rdStep            modals.Identifier = "launch3rdStep"
	IdentifierPRInputView        modals.Identifier = "pr_input_view"
	IdentifierFilterVersionView  modals.Identifier = "filter_version_view"
	IdentifierRegisterLaunchMode modals.Identifier = "launch_mode_view"
	IdentifierSelectVersion      modals.Identifier = "select_version"
	IdentifierSelectMinorMajor   modals.Identifier = "select_minor_major"
)

const (
	DefaultPlatform     = "hypershift-hosted"
	DefaultArchitecture = "amd64"
	ModalTitle          = "Launch a Cluster"
)
