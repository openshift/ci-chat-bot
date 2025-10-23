package launch

import (
	"encoding/json"
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

func FetchReleases(client *http.Client, architecture string) (map[string][]string, error) {
	url := fmt.Sprintf("https://%s.ocp.releases.ci.openshift.org/api/v1/releasestreams/accepted", architecture)
	acceptedReleases := make(map[string][]string, 0)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			klog.Errorf("Failed to close response for Resolve: %v", closeErr)
		}
	}()
	if err := json.NewDecoder(resp.Body).Decode(&acceptedReleases); err != nil {
		return nil, err
	}
	return acceptedReleases, nil
}

func FirstStepView() slackClient.ModalViewRequest {
	platformOptions := modals.BuildOptions(manager.SupportedPlatforms, nil)
	architectureOptions := modals.BuildOptions(manager.SupportedArchitectures, nil)
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(modals.CallbackData{}, string(IdentifierInitialView)),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch a Cluster"},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Next"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			&slackClient.HeaderBlock{
				Type: slackClient.MBTHeader,
				Text: &slackClient.TextBlockObject{
					Type:     "plain_text",
					Text:     "Select the Launch Platform and Architecture",
					Emoji:    false,
					Verbatim: false,
				},
			},
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  LaunchPlatform,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: fmt.Sprintf("Platform (Default - %s)", DefaultPlatform)},
				Element: &slackClient.SelectBlockElement{
					Type:        slackClient.OptTypeStatic,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: DefaultPlatform},
					Options:     platformOptions,
				},
			},
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  LaunchArchitecture,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: fmt.Sprintf("Architecture (Default - %s)", DefaultArchitecture)},
				Element: &slackClient.SelectBlockElement{
					Type:        slackClient.OptTypeStatic,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: DefaultArchitecture},
					Options:     architectureOptions,
				},
			},
		}},
	}
}

func ThirdStepView(callback *slackClient.InteractionCallback, jobmanager manager.JobManager, httpclient *http.Client, data modals.CallbackData) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}
	platform := data.Input[LaunchPlatform]
	architecture := data.Input[LaunchArchitecture]
	prs, ok := data.Input[LaunchFromPR]
	if !ok {
		prs = "None"
	}
	version, ok := data.Input[LaunchFromLatestBuild]
	if !ok {
		version, ok = data.Input[LaunchFromMajorMinor]
		if !ok {
			version, ok = data.Input[LaunchFromStream]
			if !ok {
				version, ok = data.Input[LaunchFromReleaseController]
				if !ok {
					version, ok = data.Input[LaunchFromCustom]
					if !ok {
						_, version, _, _ = jobmanager.ResolveImageOrVersion("nightly", "", architecture)
					}
				}
			}
		}
	}
	blacklist := sets.Set[string]{}
	for _, parameter := range manager.SupportedParameters {
		for k, envs := range manager.MultistageParameters {
			if k == parameter {
				if !envs.Platforms.Has(platform) {
					blacklist.Insert(parameter)
				}
			}

		}
	}
	options := modals.BuildOptions(manager.SupportedParameters, blacklist)
	context := fmt.Sprintf("Architecture: %s;Platform: %s;Version: %s;PR: %s", architecture, platform, version, prs)
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(data, string(Identifier3rdStep)),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch a Cluster"},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Submit"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  LaunchParameters,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select one or more parameters for your cluster:"},
				Optional: true,
				Element: &slackClient.SelectBlockElement{
					Type:        slackClient.MultiOptTypeStatic,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select one or more parameters..."},
					Options:     options,
				},
			},
			&slackClient.DividerBlock{
				Type:    slackClient.MBTDivider,
				BlockID: "1rs_divider",
			},
			&slackClient.ContextBlock{
				Type:    slackClient.MBTContext,
				BlockID: launchStepContext,
				ContextElements: slackClient.ContextElements{Elements: []slackClient.MixedElement{
					&slackClient.TextBlockObject{
						Type:     slackClient.PlainTextType,
						Text:     context,
						Emoji:    false,
						Verbatim: false,
					},
				}},
			},
		}},
	}
}

func SubmissionView(msg string) slackClient.ModalViewRequest {
	return slackClient.ModalViewRequest{
		Type:  slackClient.VTModal,
		Title: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch a Cluster"},
		Close: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Close"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			&slackClient.SectionBlock{
				Type: slackClient.MBTSection,
				Text: &slackClient.TextBlockObject{
					Type: slackClient.MarkdownType,
					Text: msg,
				},
			},
		}},
	}
}

func SelectModeView(callback *slackClient.InteractionCallback, jobmanager manager.JobManager, data modals.CallbackData) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}
	platform, ok := data.Input[LaunchPlatform]
	if !ok {
		platform = DefaultPlatform
	}
	architecture, ok := data.Input[LaunchArchitecture]
	if !ok {
		architecture = DefaultArchitecture
	}
	metadata := fmt.Sprintf("Architecture: %s; Platform: %s", architecture, platform)
	options := modals.BuildOptions([]string{LaunchModePRKey, LaunchModeVersionKey}, nil)
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(data, string(IdentifierRegisterLaunchMode)),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch a Cluster"},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Next"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
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
				BlockID: LaunchMode,
				Label:   &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch the Cluster using:"},
				Element: &slackClient.CheckboxGroupsBlockElement{
					Type:    slackClient.METCheckboxGroups,
					Options: options,
				},
			},
			&slackClient.DividerBlock{
				Type:    slackClient.MBTDivider,
				BlockID: "divider",
			},
			&slackClient.ContextBlock{
				Type:    slackClient.MBTContext,
				BlockID: launchStepContext,
				ContextElements: slackClient.ContextElements{Elements: []slackClient.MixedElement{
					&slackClient.TextBlockObject{
						Type:     slackClient.PlainTextType,
						Text:     metadata,
						Emoji:    false,
						Verbatim: false,
					},
				}},
			},
		}},
	}
}

func FilterVersionView(callback *slackClient.InteractionCallback, jobmanager manager.JobManager, data modals.CallbackData, httpclient *http.Client, mode sets.Set[string], noneSelected bool) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}
	platform := data.Input[LaunchPlatform]
	architecture := data.Input[LaunchArchitecture]
	latestBuildOptions := []*slackClient.OptionBlockObject{}
	_, nightly, _, err := jobmanager.ResolveImageOrVersion("nightly", "", architecture)
	if err == nil {
		latestBuildOptions = append(latestBuildOptions, &slackClient.OptionBlockObject{Value: "nightly", Text: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: nightly}})
	}
	_, ci, _, err := jobmanager.ResolveImageOrVersion("ci", "", architecture)
	if err == nil {
		latestBuildOptions = append(latestBuildOptions, &slackClient.OptionBlockObject{Value: "ci", Text: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: ci}})
	}
	releases, err := FetchReleases(httpclient, architecture)
	if err != nil {
		klog.Warningf("failed to fetch the data from release controller: %s", err)
		return modals.ErrorView("retrive valid releases from the release-controller", err)
	}
	var streams []string
	for stream := range releases {
		if platform == "hypershift-hosted" {
			for _, v := range sets.List(manager.HypershiftSupportedVersions.Versions) {
				if strings.HasPrefix(stream, v) || strings.Split(stream, "-")[1] == "dev" || strings.Split(stream, "-")[1] == "stable" {
					streams = append(streams, stream)
					break
				}
			}
		} else {
			streams = append(streams, stream)
		}

	}

	sort.Strings(streams)
	streamsOptions := modals.BuildOptions(streams, nil)
	metadata := fmt.Sprintf("Architecture: %s;Platform: %s;%s: %s", architecture, platform, LaunchModeContext, strings.Join(sets.List(mode), ","))
	view := slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(data, string(IdentifierFilterVersionView)),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch a Cluster"},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Next"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
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
				BlockID:  LaunchFromStream,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Specify the Stream:"},
				Element: &slackClient.SelectBlockElement{
					Type:        slackClient.OptTypeStatic,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select an entry..."},
					Options:     streamsOptions,
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
				BlockID:  LaunchFromLatestBuild,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "The latest build (nightly) or CI build:"},
				Element: &slackClient.SelectBlockElement{
					Type:        slackClient.OptTypeStatic,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select an entry..."},
					Options:     latestBuildOptions,
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
				BlockID:  LaunchFromCustom,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Enter a Custom Pull Spec:"},
				Element: &slackClient.PlainTextInputBlockElement{
					Type:        slackClient.METPlainTextInput,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Enter a custom pull spec..."},
				},
			},
			&slackClient.DividerBlock{
				Type:    slackClient.MBTDivider,
				BlockID: "divider",
			},
			&slackClient.ContextBlock{
				Type:    slackClient.MBTContext,
				BlockID: launchStepContext,
				ContextElements: slackClient.ContextElements{Elements: []slackClient.MixedElement{
					&slackClient.TextBlockObject{
						Type:     slackClient.PlainTextType,
						Text:     metadata,
						Emoji:    false,
						Verbatim: false,
					},
				}},
			},
		}},
	}
	if noneSelected {
		view.Blocks.BlockSet = append([]slackClient.Block{slackClient.NewHeaderBlock(slackClient.NewTextBlockObject(slackClient.PlainTextType, ":warning: Error: At least one option must be selected :warning:", true, false))}, view.Blocks.BlockSet...)
	}
	return view
}

func PRInputView(callback *slackClient.InteractionCallback, data modals.CallbackData) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}
	platform := data.Input[LaunchPlatform]
	architecture := data.Input[LaunchArchitecture]
	mode := data.MultipleSelection[LaunchMode]
	launchWithVersion := false
	for _, key := range mode {
		if strings.TrimSpace(key) == LaunchModeVersionKey {
			launchWithVersion = true
		}
	}
	metadata := fmt.Sprintf("Architecture: %s; Platform: %s;%s: %s", architecture, platform, LaunchModeContext, mode)
	if launchWithVersion {
		version := data.Input[LaunchVersion]
		if version == "" {
			version = data.Input[LaunchFromLatestBuild]
		}
		if version == "" {
			version = data.Input[LaunchFromCustom]
		}
		metadata = fmt.Sprintf("%s;Version: %s", metadata, version)
	}
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(data, string(IdentifierPRInputView)),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch a Cluster"},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Next"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
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
				BlockID: LaunchFromPR,
				Label:   &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Enter one or more PRs, separated by coma:"},
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
				BlockID: launchStepContext,
				ContextElements: slackClient.ContextElements{Elements: []slackClient.MixedElement{
					&slackClient.TextBlockObject{
						Type:     slackClient.PlainTextType,
						Text:     metadata,
						Emoji:    false,
						Verbatim: false,
					},
				}},
			},
		}},
	}
}

func SelectVersionView(callback *slackClient.InteractionCallback, jobmanager manager.JobManager, httpclient *http.Client, data modals.CallbackData) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}

	platform := data.Input[LaunchPlatform]
	architecture := data.Input[LaunchArchitecture]
	mode := data.MultipleSelection[LaunchMode]
	selectedStream := data.Input[LaunchFromStream]
	if selectedStream == "" {
		selectedStream = data.Input[LaunchFromStream]
	}
	selectedMajorMinor := data.Input[LaunchFromMajorMinor]
	metadata := fmt.Sprintf("Architecture: %s; Platform: %s; %s: %s", architecture, platform, LaunchModeContext, mode)
	releases, err := FetchReleases(httpclient, architecture)
	if err != nil {
		klog.Warningf("failed to fetch the data from release controller: %s", err)
		return modals.ErrorView("retrive valid releases from the release-controller", err)
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
	if len(allTags) > 99 {
		return SelectMinorMajor(callback, httpclient, data)
	}
	//sort.Strings(allTags)
	allTagsOptions := modals.BuildOptions(allTags, nil)
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(data, string(IdentifierSelectVersion)),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch a Cluster"},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Next"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
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
				BlockID: LaunchVersion,
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
				BlockID: launchStepContext,
				ContextElements: slackClient.ContextElements{Elements: []slackClient.MixedElement{
					&slackClient.TextBlockObject{
						Type:     slackClient.PlainTextType,
						Text:     metadata,
						Emoji:    false,
						Verbatim: false,
					},
				}},
			},
		}},
	}
}

func SelectMinorMajor(callback *slackClient.InteractionCallback, httpclient *http.Client, data modals.CallbackData) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}

	platform := data.Input[LaunchPlatform]
	architecture := data.Input[LaunchArchitecture]
	mode := data.MultipleSelection[LaunchMode]
	selectedStream := data.Input[LaunchFromStream]
	metadata := fmt.Sprintf("Architecture: %s; Platform: %s; %s: %s; %s: %s", architecture, platform, LaunchModeContext, mode, LaunchFromStream, selectedStream)
	releases, err := FetchReleases(httpclient, architecture)
	if err != nil {
		klog.Warningf("failed to fetch the data from release controller: %s", err)
		return modals.ErrorView("retrive valid releases from the release-controller", err)
	}

	majorMinor := make(map[string]bool, 0)
	for stream, tags := range releases {
		if stream != selectedStream {
			continue
		}
		if strings.HasPrefix(stream, stableReleasesPrefix) {
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
	// the x/mod/semver requires a `v` prefix for a version to be considered valid
	for index, version := range majorMinorReleases {
		majorMinorReleases[index] = "v" + version
	}
	semver.Sort(majorMinorReleases)
	for index, version := range majorMinorReleases {
		majorMinorReleases[index] = strings.TrimPrefix(version, "v")
	}
	slices.Reverse(majorMinorReleases)
	majorMinorOptions := modals.BuildOptions(majorMinorReleases, nil)
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(data, string(IdentifierSelectMinorMajor)),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch a Cluster"},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Next"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			&slackClient.HeaderBlock{
				Type: slackClient.MBTHeader,
				Text: &slackClient.TextBlockObject{
					Type:     slackClient.PlainTextType,
					Text:     "There are to many results from the selected Stream. Select a Minor.Major as well",
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
				BlockID: LaunchFromMajorMinor,
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
				BlockID: launchStepContext,
				ContextElements: slackClient.ContextElements{Elements: []slackClient.MixedElement{
					&slackClient.TextBlockObject{
						Type:     slackClient.PlainTextType,
						Text:     metadata,
						Emoji:    false,
						Verbatim: false,
					},
				}},
			},
		}},
	}
}
