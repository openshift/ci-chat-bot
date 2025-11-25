package create

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

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
	return FirstStepViewWithData(modals.CallbackData{})
}

func FirstStepViewWithData(data modals.CallbackData) slackClient.ModalViewRequest {
	platformOptions := modals.BuildOptions(manager.MCEPlatforms.UnsortedList(), nil)
	durations := []string{}
	for i := 2; i <= int(manager.MaxMCEDuration/time.Hour); i++ {
		durations = append(durations, fmt.Sprintf("%dh", i))
	}
	durationOptions := modals.BuildOptions(durations, nil)

	// Get initial selections from callback data
	var platformInitial, durationInitial *slackClient.OptionBlockObject
	if platform, ok := data.Input[modals.LaunchPlatform]; ok && platform != "" {
		platformInitial = &slackClient.OptionBlockObject{
			Value: platform,
			Text:  &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: platform},
		}
	}
	if duration, ok := data.Input[CreateDuration]; ok && duration != "" {
		durationInitial = &slackClient.OptionBlockObject{
			Value: duration,
			Text:  &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: duration},
		}
	}

	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(data, string(IdentifierInitialView)),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch an MCE Cluster"},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Next"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			&slackClient.HeaderBlock{
				Type: slackClient.MBTHeader,
				Text: &slackClient.TextBlockObject{
					Type:     "plain_text",
					Text:     "Select the Launch Platform and Duration",
					Emoji:    false,
					Verbatim: false,
				},
			},
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  modals.LaunchPlatform,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: fmt.Sprintf("Platform (Default - %s)", defaultPlatform)},
				Element: &slackClient.SelectBlockElement{
					Type:          slackClient.OptTypeStatic,
					Placeholder:   &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: defaultPlatform},
					Options:       platformOptions,
					InitialOption: platformInitial,
				},
			},
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  CreateDuration,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: fmt.Sprintf("Duration (Default - %s)", defaultDuration)},
				Element: &slackClient.SelectBlockElement{
					Type:          slackClient.OptTypeStatic,
					Placeholder:   &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: defaultDuration},
					Options:       durationOptions,
					InitialOption: durationInitial,
				},
			},
		}},
	}
}

func ThirdStepView(callback *slackClient.InteractionCallback, jobmanager manager.JobManager, httpclient *http.Client, data modals.CallbackData, previousStep string) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}
	platform := data.Input[modals.LaunchPlatform]
	duration := data.Input[CreateDuration]
	prs, ok := data.Input[modals.LaunchFromPR]
	if !ok {
		prs = "None"
	}
	version := modals.GetVersion(data, jobmanager)
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
	context := fmt.Sprintf("Duration: %s;Platform: %s;Version: %s;PR: %s", duration, platform, version, prs)
	// Set the previous step for back navigation
	data = modals.SetPreviousStep(data, previousStep)
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(data, string(Identifier3rdStep)),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch a Cluster"},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Submit"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			modals.BackButtonBlock(),
			&slackClient.ContextBlock{
				Type:    slackClient.MBTContext,
				BlockID: modals.LaunchStepContext,
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

func SelectModeView(callback *slackClient.InteractionCallback, jobmanager manager.JobManager, data modals.CallbackData) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}
	klog.Infof("Callback Data: %+v", data)
	platform, ok := data.Input[modals.LaunchPlatform]
	if !ok {
		platform = defaultPlatform
	}
	duration, ok := data.Input[CreateDuration]
	if !ok {
		duration = defaultDuration
	}
	metadata := fmt.Sprintf("Platform: %s; Duration: %s", platform, duration)
	options := modals.BuildOptions([]string{modals.LaunchModePRKey, modals.LaunchModeVersionKey}, nil)

	// Build initial options from saved selections
	var initialOptions []*slackClient.OptionBlockObject
	if modes, ok := data.MultipleSelection[modals.LaunchMode]; ok {
		for _, mode := range modes {
			initialOptions = append(initialOptions, &slackClient.OptionBlockObject{
				Value: mode,
				Text:  &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: mode},
			})
		}
	}

	// Set the previous step for back navigation
	data = modals.SetPreviousStep(data, string(IdentifierInitialView))
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(data, string(IdentifierSelectModeView)),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch an MCE Cluster"},
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
	klog.Infof("Callback Data: %+v", data)
	platform := data.Input[modals.LaunchPlatform]
	duration := data.Input[CreateDuration]
	latestBuildOptions := []*slackClient.OptionBlockObject{}
	_, nightly, _, err := jobmanager.ResolveImageOrVersion("nightly", "", "amd64")
	if err == nil {
		latestBuildOptions = append(latestBuildOptions, &slackClient.OptionBlockObject{Value: "nightly", Text: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: nightly}})
	}
	_, ci, _, err := jobmanager.ResolveImageOrVersion("ci", "", "amd64")
	if err == nil {
		latestBuildOptions = append(latestBuildOptions, &slackClient.OptionBlockObject{Value: "ci", Text: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: ci}})
	}
	releases, err := FetchReleases(httpclient, "amd64")
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
	metadata := fmt.Sprintf("Duration: %s;Platform: %s;%s: %s", duration, platform, modals.LaunchModeContext, strings.Join(sets.List(mode), ","))
	// Set the previous step for back navigation
	data = modals.SetPreviousStep(data, string(IdentifierSelectModeView))
	view := slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(data, string(IdentifierFilterVersionView)),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch a Cluster"},
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
				BlockID:  modals.LaunchFromLatestBuild,
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
				BlockID:  modals.LaunchFromCustom,
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
				BlockID: modals.LaunchStepContext,
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

func PRInputView(callback *slackClient.InteractionCallback, data modals.CallbackData, previousStep string) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}
	platform := data.Input[modals.LaunchPlatform]
	duration := data.Input[CreateDuration]
	mode := data.MultipleSelection[modals.LaunchMode]
	launchWithVersion := false
	for _, key := range mode {
		if strings.TrimSpace(key) == modals.LaunchModeVersionKey {
			launchWithVersion = true
		}
	}
	metadata := fmt.Sprintf("Duration: %s; Platform: %s;%s: %s", duration, platform, modals.LaunchModeContext, mode)
	if launchWithVersion {
		version := data.Input[modals.LaunchVersion]
		if version == "" {
			version = data.Input[modals.LaunchFromLatestBuild]
		}
		if version == "" {
			version = data.Input[modals.LaunchFromCustom]
		}
		metadata = fmt.Sprintf("%s;Version: %s", metadata, version)
	}
	// Set the previous step for back navigation
	data = modals.SetPreviousStep(data, previousStep)
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(data, string(IdentifierPRInputView)),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch a Cluster"},
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
						Text:     metadata,
						Emoji:    false,
						Verbatim: false,
					},
				}},
			},
		}},
	}
}

func SelectVersionView(callback *slackClient.InteractionCallback, jobmanager manager.JobManager, httpclient *http.Client, data modals.CallbackData, previousStep string) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}

	platform := data.Input[modals.LaunchPlatform]
	duration := data.Input[CreateDuration]
	mode := data.MultipleSelection[modals.LaunchMode]
	selectedStream := data.Input[modals.LaunchFromStream]
	selectedMajorMinor := data.Input[modals.LaunchFromMajorMinor]
	metadata := fmt.Sprintf("Duration: %s; Platform: %s; %s: %s", duration, platform, modals.LaunchModeContext, mode)
	releases, err := FetchReleases(httpclient, "amd64")
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
		return SelectMinorMajor(callback, httpclient, data, previousStep)
	}
	//sort.Strings(allTags)
	allTagsOptions := modals.BuildOptions(allTags, nil)
	// Set the previous step for back navigation
	data = modals.SetPreviousStep(data, previousStep)
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(data, string(IdentifierSelectVersion)),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch a Cluster"},
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
						Text:     metadata,
						Emoji:    false,
						Verbatim: false,
					},
				}},
			},
		}},
	}
}

func SelectMinorMajor(callback *slackClient.InteractionCallback, httpclient *http.Client, data modals.CallbackData, previousStep string) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}

	platform := data.Input[modals.LaunchPlatform]
	duration := data.Input[CreateDuration]
	mode := data.MultipleSelection[modals.LaunchMode]
	selectedStream := data.Input[modals.LaunchFromStream]
	metadata := fmt.Sprintf("Duration: %s; Platform: %s; %s: %s; %s: %s", duration, platform, modals.LaunchModeContext, mode, modals.LaunchFromStream, selectedStream)
	releases, err := FetchReleases(httpclient, "amd64")
	if err != nil {
		klog.Warningf("failed to fetch the data from release controller: %s", err)
		return modals.ErrorView("retrive valid releases from the release-controller", err)
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
	// Set the previous step for back navigation
	data = modals.SetPreviousStep(data, previousStep)
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(data, string(IdentifierSelectMinorMajor)),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch a Cluster"},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Next"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			modals.BackButtonBlock(),
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
						Text:     metadata,
						Emoji:    false,
						Verbatim: false,
					},
				}},
			},
		}},
	}
}
