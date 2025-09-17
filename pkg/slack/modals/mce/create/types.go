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
	CreatePlatform              = "platform"
	CreateDuration              = "duration"
	defaultPlatform             = "aws"
	defaultDuration             = "6h"
	stableReleasesPrefix        = "4-stable"
	LaunchFromPR                = "pr"
	LaunchFromMajorMinor        = "major_minor"
	LaunchFromStream            = "stream"
	LaunchFromLatestBuild       = "latest_build"
	launchFromReleaseController = "release_controller_version"
	LaunchFromCustom            = "custom"
	LaunchVersion               = "version"
	launchStepContext           = "context"
	LaunchMode                  = "launch_mode"
	LaunchModeVersion           = "version"
	LaunchModePR                = "pr"
	LaunchModePRKey             = "One or multiple PRs"
	LaunchModeVersionKey        = "A Version"
	LaunchModeContext           = "Launch Mode"
	ModalTitle                  = "Launch an MCE Cluster"
)
