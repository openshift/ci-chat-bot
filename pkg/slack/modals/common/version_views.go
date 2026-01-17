package common

import (
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	slackClient "github.com/slack-go/slack"
	"golang.org/x/mod/semver"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog"
)

const (
	// SlackUIMaxOptions is the maximum number of options that can be displayed in a Slack select menu
	SlackUIMaxOptions = 99
)

// BaseViewConfig holds fields common to all version-related view builders
type BaseViewConfig struct {
	// Callback is the interaction callback from Slack
	Callback *slackClient.InteractionCallback
	// Data contains the accumulated user input from previous steps
	Data modals.CallbackData
	// ModalIdentifier is the identifier for this modal step
	ModalIdentifier string
	// Title is the modal title
	Title string
	// PreviousStep is the identifier of the previous step (for back navigation)
	PreviousStep string
	// ContextMetadata is additional context to display to the user
	ContextMetadata string
}

// ReleaseViewConfig extends BaseViewConfig with fields needed for release fetching
type ReleaseViewConfig struct {
	BaseViewConfig
	// HTTPClient is used to fetch releases
	HTTPClient *http.Client
	// Architecture is the target architecture for release fetching
	Architecture string
}

// FilterVersionViewConfig holds configuration for BuildFilterVersionView
type FilterVersionViewConfig struct {
	ReleaseViewConfig
	// JobManager is used to resolve nightly/ci versions
	JobManager manager.JobManager
	// NoneSelected indicates if validation failed due to no selection
	NoneSelected bool
}

// SelectVersionViewConfig holds configuration for BuildSelectVersionView
type SelectVersionViewConfig struct {
	ReleaseViewConfig
	// SelectMinorMajorIdentifier is the identifier for the minor/major selection modal,
	// used when BuildSelectVersionView needs to redirect due to too many results
	SelectMinorMajorIdentifier string
}

// BuildFilterVersionView creates a view for selecting versions from streams, builds, or custom specs
func BuildFilterVersionView(config FilterVersionViewConfig) slackClient.ModalViewRequest {
	if config.Callback == nil {
		return slackClient.ModalViewRequest{}
	}

	latestBuildOptions := []*slackClient.OptionBlockObject{}
	_, nightly, _, err := config.JobManager.ResolveImageOrVersion("nightly", "", config.Architecture)
	if err == nil {
		latestBuildOptions = append(latestBuildOptions, &slackClient.OptionBlockObject{
			Value: "nightly",
			Text:  &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: nightly},
		})
	}
	_, ci, _, err := config.JobManager.ResolveImageOrVersion("ci", "", config.Architecture)
	if err == nil {
		latestBuildOptions = append(latestBuildOptions, &slackClient.OptionBlockObject{
			Value: "ci",
			Text:  &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: ci},
		})
	}

	releases, err := FetchReleases(config.HTTPClient, config.Architecture)
	if err != nil {
		klog.Warningf("failed to fetch the data from release controller: %s", err)
		return modals.ErrorView("retrieve valid releases from the release-controller", err)
	}

	streams := filterStreams(releases, config.Data.Input[modals.LaunchPlatform])
	sort.Strings(streams)
	streamsOptions := modals.BuildOptions(streams, nil)

	// Preserve previous selections
	var streamInitial, latestBuildInitial *slackClient.OptionBlockObject
	if stream, ok := config.Data.Input[modals.LaunchFromStream]; ok && stream != "" {
		streamInitial = &slackClient.OptionBlockObject{
			Value: stream,
			Text:  &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: stream},
		}
	}
	if build, ok := config.Data.Input[modals.LaunchFromLatestBuild]; ok && build != "" {
		for _, opt := range latestBuildOptions {
			if opt.Value == build {
				latestBuildInitial = opt
				break
			}
		}
	}
	customValue := config.Data.Input[modals.LaunchFromCustom]

	// Set the previous step for back navigation
	config.Data = modals.SetPreviousStep(config.Data, config.PreviousStep)

	view := slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(config.Data, config.ModalIdentifier),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: config.Title},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Next"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			modals.BackButtonBlock(),
			&slackClient.HeaderBlock{
				Type: slackClient.MBTHeader,
				Text: &slackClient.TextBlockObject{
					Type:     slackClient.PlainTextType,
					Text:     "Version Specifications",
					Emoji:    false,
					Verbatim: false,
				},
			},
			&slackClient.SectionBlock{
				Type: slackClient.MBTSection,
				Text: &slackClient.TextBlockObject{
					Type: slackClient.MarkdownType,
					Text: "*Specify the _stream_ to get a list of versions to select from*",
				},
			},
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  modals.LaunchFromStream,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Specify the Stream:"},
				Element: &slackClient.SelectBlockElement{
					Type:          slackClient.OptTypeStatic,
					Placeholder:   &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select an entry..."},
					Options:       streamsOptions,
					InitialOption: streamInitial,
				},
			},
			&slackClient.DividerBlock{
				Type:    slackClient.MBTDivider,
				BlockID: "divider_section",
			},
			&slackClient.SectionBlock{
				Type: slackClient.MBTSection,
				Text: &slackClient.TextBlockObject{
					Type: slackClient.MarkdownType,
					Text: "\n*Alternatively:*\n*Launch using the latest Nightly or CI build*",
				},
			},
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  modals.LaunchFromLatestBuild,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "The latest build (nightly) or CI build:"},
				Element: &slackClient.SelectBlockElement{
					Type:          slackClient.OptTypeStatic,
					Placeholder:   &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select an entry..."},
					Options:       latestBuildOptions,
					InitialOption: latestBuildInitial,
				},
			},
			&slackClient.DividerBlock{
				Type:    slackClient.MBTDivider,
				BlockID: "divider_2nd_section",
			},
			&slackClient.SectionBlock{
				Type: slackClient.MBTSection,
				Text: &slackClient.TextBlockObject{
					Type: slackClient.MarkdownType,
					Text: "\n*Alternatively:*\n*Launch using a _Custom_ Pull Spec*",
				},
			},
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  modals.LaunchFromCustom,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Enter a Custom Pull Spec:"},
				Element: &slackClient.PlainTextInputBlockElement{
					Type:         slackClient.METPlainTextInput,
					Placeholder:  &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Enter a custom pull spec..."},
					InitialValue: customValue,
				},
			},
			&slackClient.DividerBlock{
				Type:    slackClient.MBTDivider,
				BlockID: "divider",
			},
			&slackClient.ContextBlock{
				Type:    slackClient.MBTContext,
				BlockID: modals.LaunchStepContext,
				ContextElements: slackClient.ContextElements{Elements: []slackClient.MixedElement{
					&slackClient.TextBlockObject{
						Type:     slackClient.PlainTextType,
						Text:     config.ContextMetadata,
						Emoji:    false,
						Verbatim: false,
					},
				}},
			},
		}},
	}

	if config.NoneSelected {
		view.Blocks.BlockSet = append([]slackClient.Block{
			slackClient.NewHeaderBlock(slackClient.NewTextBlockObject(
				slackClient.PlainTextType,
				":warning: Error: At least one option must be selected :warning:",
				true,
				false,
			)),
		}, view.Blocks.BlockSet...)
	}

	return view
}

// BuildSelectVersionView creates a view for selecting a specific version from a stream
func BuildSelectVersionView(config SelectVersionViewConfig) slackClient.ModalViewRequest {
	if config.Callback == nil {
		return slackClient.ModalViewRequest{}
	}

	selectedStream := config.Data.Input[modals.LaunchFromStream]
	selectedMajorMinor := config.Data.Input[modals.LaunchFromMajorMinor]

	releases, err := FetchReleases(config.HTTPClient, config.Architecture)
	if err != nil {
		klog.Warningf("failed to fetch the data from release controller: %s", err)
		return modals.ErrorView("retrieve valid releases from the release-controller", err)
	}

	var allTags []string
	for stream, tags := range releases {
		if stream == selectedStream {
			for _, tag := range tags {
				if strings.HasPrefix(tag, selectedMajorMinor) {
					allTags = append(allTags, tag)
				}
			}
		}
	}

	// If too many results, redirect to major/minor selection
	if len(allTags) > SlackUIMaxOptions {
		releaseConfig := config.ReleaseViewConfig
		if config.SelectMinorMajorIdentifier != "" {
			releaseConfig.ModalIdentifier = config.SelectMinorMajorIdentifier
		}
		return BuildSelectMinorMajorView(releaseConfig)
	}

	allTagsOptions := modals.BuildOptions(allTags, nil)

	// Set the previous step for back navigation
	config.Data = modals.SetPreviousStep(config.Data, config.PreviousStep)

	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(config.Data, config.ModalIdentifier),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: config.Title},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Next"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			modals.BackButtonBlock(),
			&slackClient.HeaderBlock{
				Type: slackClient.MBTHeader,
				Text: &slackClient.TextBlockObject{
					Type:     slackClient.PlainTextType,
					Text:     "Select a Version",
					Emoji:    false,
					Verbatim: false,
				},
			},
			&slackClient.DividerBlock{
				Type:    slackClient.MBTDivider,
				BlockID: "divider",
			},
			&slackClient.InputBlock{
				Type:    slackClient.MBTInput,
				BlockID: modals.LaunchVersion,
				Label:   &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select a version:"},
				Element: &slackClient.SelectBlockElement{
					Type:        slackClient.OptTypeStatic,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select an entry..."},
					Options:     allTagsOptions,
				},
			},
			&slackClient.DividerBlock{
				Type:    slackClient.MBTDivider,
				BlockID: "context_divider",
			},
			&slackClient.ContextBlock{
				Type:    slackClient.MBTContext,
				BlockID: modals.LaunchStepContext,
				ContextElements: slackClient.ContextElements{Elements: []slackClient.MixedElement{
					&slackClient.TextBlockObject{
						Type:     slackClient.PlainTextType,
						Text:     config.ContextMetadata,
						Emoji:    false,
						Verbatim: false,
					},
				}},
			},
		}},
	}
}

// BuildSelectMinorMajorView creates a view for selecting a major.minor version to filter results
func BuildSelectMinorMajorView(config ReleaseViewConfig) slackClient.ModalViewRequest {
	if config.Callback == nil {
		return slackClient.ModalViewRequest{}
	}

	selectedStream := config.Data.Input[modals.LaunchFromStream]
	platform := config.Data.Input[modals.LaunchPlatform]

	releases, err := FetchReleases(config.HTTPClient, config.Architecture)
	if err != nil {
		klog.Warningf("failed to fetch the data from release controller: %s", err)
		return modals.ErrorView("retrieve valid releases from the release-controller", err)
	}

	majorMinor := make(map[string]bool, 0)
	for stream, tags := range releases {
		if stream != selectedStream {
			continue
		}
		if strings.HasPrefix(stream, modals.StableReleasesPrefix) {
			for _, tag := range tags {
				splitTag := strings.Split(tag, ".")
				if len(splitTag) >= 2 {
					majorMinor[fmt.Sprintf("%s.%s", splitTag[0], splitTag[1])] = true
				}
			}
		}
	}

	var majorMinorReleases []string
	for key := range majorMinor {
		if manager.HypershiftSupportedVersions.Versions.Has(key) || platform != "hypershift-hosted" {
			majorMinorReleases = append(majorMinorReleases, key)
		}
	}

	// Use semver for proper version sorting
	for index, version := range majorMinorReleases {
		majorMinorReleases[index] = "v" + version
	}
	semver.Sort(majorMinorReleases)
	for index, version := range majorMinorReleases {
		majorMinorReleases[index] = strings.TrimPrefix(version, "v")
	}
	slices.Reverse(majorMinorReleases)

	majorMinorOptions := modals.BuildOptions(majorMinorReleases, nil)

	// Set the previous step for back navigation
	config.Data = modals.SetPreviousStep(config.Data, config.PreviousStep)

	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(config.Data, config.ModalIdentifier),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: config.Title},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Next"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			modals.BackButtonBlock(),
			&slackClient.HeaderBlock{
				Type: slackClient.MBTHeader,
				Text: &slackClient.TextBlockObject{
					Type:     slackClient.PlainTextType,
					Text:     "There are too many results from the selected Stream. Select a Minor.Major as well",
					Emoji:    false,
					Verbatim: false,
				},
			},
			&slackClient.DividerBlock{
				Type:    slackClient.MBTDivider,
				BlockID: "divider",
			},
			&slackClient.InputBlock{
				Type:    slackClient.MBTInput,
				BlockID: modals.LaunchFromMajorMinor,
				Label:   &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Specify the Major.Minor:"},
				Element: &slackClient.SelectBlockElement{
					Type:        slackClient.OptTypeStatic,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select an entry..."},
					Options:     majorMinorOptions,
				},
			},
			&slackClient.DividerBlock{
				Type:    slackClient.MBTDivider,
				BlockID: "context_divider",
			},
			&slackClient.ContextBlock{
				Type:    slackClient.MBTContext,
				BlockID: modals.LaunchStepContext,
				ContextElements: slackClient.ContextElements{Elements: []slackClient.MixedElement{
					&slackClient.TextBlockObject{
						Type:     slackClient.PlainTextType,
						Text:     config.ContextMetadata,
						Emoji:    false,
						Verbatim: false,
					},
				}},
			},
		}},
	}
}

// BuildPRInputView creates a view for entering PR numbers
func BuildPRInputView(config BaseViewConfig) slackClient.ModalViewRequest {
	if config.Callback == nil {
		return slackClient.ModalViewRequest{}
	}

	// Set the previous step for back navigation
	config.Data = modals.SetPreviousStep(config.Data, config.PreviousStep)

	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(config.Data, config.ModalIdentifier),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: config.Title},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Next"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			modals.BackButtonBlock(),
			&slackClient.HeaderBlock{
				Type: slackClient.MBTHeader,
				Text: &slackClient.TextBlockObject{
					Type:     slackClient.PlainTextType,
					Text:     "Enter A PR",
					Emoji:    false,
					Verbatim: false,
				},
			},
			&slackClient.InputBlock{
				Type:    slackClient.MBTInput,
				BlockID: modals.LaunchFromPR,
				Label:   &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Enter one or more PRs, separated by comma:"},
				Element: &slackClient.PlainTextInputBlockElement{
					Type:        slackClient.METPlainTextInput,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Enter one or more PRs..."},
				},
			},
			&slackClient.DividerBlock{
				Type:    slackClient.MBTDivider,
				BlockID: "divider",
			},
			&slackClient.ContextBlock{
				Type:    slackClient.MBTContext,
				BlockID: modals.LaunchStepContext,
				ContextElements: slackClient.ContextElements{Elements: []slackClient.MixedElement{
					&slackClient.TextBlockObject{
						Type:     slackClient.PlainTextType,
						Text:     config.ContextMetadata,
						Emoji:    false,
						Verbatim: false,
					},
				}},
			},
		}},
	}
}

// filterStreams filters release streams based on platform support
func filterStreams(releases map[string][]string, platform string) []string {
	var streams []string
	for stream := range releases {
		if platform == "hypershift-hosted" {
			for _, v := range sets.List(manager.HypershiftSupportedVersions.Versions) {
				parts := strings.SplitN(stream, "-", 2)
				if strings.HasPrefix(stream, v) || (len(parts) > 1 && (parts[1] == "dev" || parts[1] == "stable")) {
					streams = append(streams, stream)
					break
				}
			}
		} else {
			streams = append(streams, stream)
		}
	}
	return streams
}

// SelectModeViewConfig holds configuration for building the select mode view
type SelectModeViewConfig struct {
	Callback        *slackClient.InteractionCallback
	Data            modals.CallbackData
	ModalIdentifier string
	Title           string
	PreviousStep    string
	ContextMetadata string
}

// BuildSelectModeView creates a modal view for selecting launch mode (PR/Version)
func BuildSelectModeView(config SelectModeViewConfig) slackClient.ModalViewRequest {
	if config.Callback == nil {
		return slackClient.ModalViewRequest{}
	}

	options := modals.BuildOptions([]string{modals.LaunchModePRKey, modals.LaunchModeVersionKey}, nil)

	// Build initial options from saved selections
	var initialOptions []*slackClient.OptionBlockObject
	if modes, ok := config.Data.MultipleSelection[modals.LaunchMode]; ok {
		for _, mode := range modes {
			initialOptions = append(initialOptions, &slackClient.OptionBlockObject{
				Value: mode,
				Text:  &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: mode},
			})
		}
	}

	// Set the previous step for back navigation
	data := modals.SetPreviousStep(config.Data, config.PreviousStep)

	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(data, config.ModalIdentifier),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: config.Title},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Next"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			modals.BackButtonBlock(),
			&slackClient.HeaderBlock{
				Type: slackClient.MBTHeader,
				Text: &slackClient.TextBlockObject{
					Type:     slackClient.PlainTextType,
					Text:     "Select the launch mode",
					Emoji:    false,
					Verbatim: false,
				},
			},
			&slackClient.InputBlock{
				Type:    slackClient.MBTInput,
				BlockID: modals.LaunchMode,
				Label:   &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch the Cluster using:"},
				Element: &slackClient.CheckboxGroupsBlockElement{
					Type:           slackClient.METCheckboxGroups,
					Options:        options,
					InitialOptions: initialOptions,
				},
			},
			&slackClient.DividerBlock{
				Type:    slackClient.MBTDivider,
				BlockID: "divider",
			},
			&slackClient.ContextBlock{
				Type:    slackClient.MBTContext,
				BlockID: modals.LaunchStepContext,
				ContextElements: slackClient.ContextElements{Elements: []slackClient.MixedElement{
					&slackClient.TextBlockObject{
						Type:     slackClient.PlainTextType,
						Text:     config.ContextMetadata,
						Emoji:    false,
						Verbatim: false,
					},
				}},
			},
		}},
	}
}

// BuildPRInputMetadata builds metadata string for PR input view
func BuildPRInputMetadata(data modals.CallbackData, baseMetadata string) string {
	mode := data.MultipleSelection[modals.LaunchMode]
	launchWithVersion := false
	for _, key := range mode {
		if strings.TrimSpace(key) == modals.LaunchModeVersionKey {
			launchWithVersion = true
			break
		}
	}

	if !launchWithVersion {
		return baseMetadata
	}

	version := data.Input[modals.LaunchVersion]
	if version == "" {
		version = data.Input[modals.LaunchFromLatestBuild]
	}
	if version == "" {
		version = data.Input[modals.LaunchFromCustom]
	}

	return fmt.Sprintf("%s; Version: %s", baseMetadata, version)
}
