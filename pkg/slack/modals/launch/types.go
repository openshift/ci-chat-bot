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
	stableReleasesPrefix        = "4-stable"
	LaunchFromPR                = "pr"
	LaunchFromMajorMinor        = "major_minor"
	LaunchFromStream            = "stream"
	LaunchFromLatestBuild       = "latest_build"
	launchFromReleaseController = "release_controller_version"
	LaunchFromCustom            = "custom"
	LaunchPlatform              = "platform"
	LaunchArchitecture          = "architecture"
	LaunchParameters            = "parameters"
	LaunchVersion               = "version"
	launchStepContext           = "context"
	DefaultPlatform             = "hypershift-hosted"
	DefaultArchitecture         = "amd64"
	LaunchMode                  = "launch_mode"
	LaunchModeVersion           = "version"
	LaunchModePR                = "pr"
	LaunchModePRKey             = "One or multiple PRs"
	LaunchModeVersionKey        = "A Version"
	LaunchModeContext           = "Launch Mode"
	ModalTitle                  = "Launch a Cluster"
)
