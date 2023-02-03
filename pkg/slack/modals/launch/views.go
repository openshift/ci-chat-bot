package launch

import (
	"fmt"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	slackClient "github.com/slack-go/slack"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog"
	"net/http"
	"sort"
	"strings"
)

func FirstStepView() slackClient.ModalViewRequest {
	platformOptions := modals.BuildOptions(manager.SupportedPlatforms, nil)
	architectureOptions := modals.BuildOptions(manager.SupportedArchitectures, nil)
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: string(IdentifierInitialView),
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
				BlockID:  launchPlatform,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Platform (Default - aws)"},
				Element: &slackClient.SelectBlockElement{
					Type:        slackClient.OptTypeStatic,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "AWS"},
					Options:     platformOptions,
				},
			},
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  launchArchitecture,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Architecture (Default - amd64)"},
				Element: &slackClient.SelectBlockElement{
					Type:        slackClient.OptTypeStatic,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "amd64"},
					Options:     architectureOptions,
				},
			},
		}},
	}
}

func ThirdStepView(callback *slackClient.InteractionCallback, jobmanager manager.JobManager, httpclient *http.Client, data callbackData) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}
	platform := data.context["platform"]
	architecture := data.context[launchArchitecture]
	prs, ok := data.input[launchFromPR]
	if !ok {
		prs = "None"
	}
	version, ok := data.input[launchFromLatestBuild]
	if !ok {
		version, ok = data.input[launchFromMajorMinor]
		if !ok {
			version, ok = data.input[launchFromStream]
			if !ok {
				version, ok = data.input[launchFromReleaseController]
				if !ok {
					version, ok = data.input[launchFromCustom]
					if !ok {
						_, version, _, _ = jobmanager.ResolveImageOrVersion("nightly", "", architecture)
					}
				}
			}
		}
	}
	blacklist := sets.String{}
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
		PrivateMetadata: string(Identifier3rdStep),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch a Cluster"},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Submit"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  launchParameters,
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

func PrepareNextStepView() *slackClient.ModalViewRequest {
	return &slackClient.ModalViewRequest{
		Type:  slackClient.VTModal,
		Title: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch a Cluster"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			&slackClient.SectionBlock{
				Type: slackClient.MBTSection,
				Text: &slackClient.TextBlockObject{
					Type: slackClient.MarkdownType,
					Text: "Processing the next step, do not close this window...",
				},
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

func SelectModeView(callback *slackClient.InteractionCallback, jobmanager manager.JobManager, data callbackData) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}
	platform, ok := data.input[launchPlatform]
	if !ok {
		platform = defaultPlatform
	}
	architecture, ok := data.input[launchArchitecture]
	if !ok {
		architecture = defaultArchitecture
	}
	metadata := fmt.Sprintf("Architecture: %s; Platform: %s", architecture, platform)
	options := modals.BuildOptions([]string{launchModePRKey, launchModeVersionKey}, nil)
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: string(IdentifierRegisterLaunchMode),
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
				BlockID: launchMode,
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

func FilterVersionView(callback *slackClient.InteractionCallback, jobmanager manager.JobManager, data callbackData, httpclient *http.Client, mode sets.String) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}
	platform := data.context[launchPlatform]
	architecture := data.context[launchArchitecture]
	//launchMode := data.multipleSelection[launchMode]
	_, nightly, _, err := jobmanager.ResolveImageOrVersion("nightly", "", architecture)
	if err != nil {
		nightly = fmt.Sprintf("unable to find a release matching `nightly` for %s", architecture)
	}
	_, ci, _, err := jobmanager.ResolveImageOrVersion("ci", "", architecture)
	if err != nil {
		ci = fmt.Sprintf("unable to find a release matching \"ci\" for %s", architecture)
	}
	releases, err := fetchReleases(httpclient, architecture)
	if err != nil {
		// TODO - return an error view, with a try again
		klog.Warningf("failed to fetch the data from release controller: %s", err)
	}
	var streams []string
	majorMinor := make(map[string]bool, 0)
	for stream, tags := range releases {
		if strings.HasPrefix(stream, stableReleasesPrefix) {
			for _, tag := range tags {
				splitTag := strings.Split(tag, ".")
				if len(splitTag) >= 2 {
					majorMinor[fmt.Sprintf("%s.%s", splitTag[0], splitTag[1])] = true
				}
			}

		}
		streams = append(streams, stream)
	}

	var majorMinorReleases []string
	for key := range majorMinor {
		majorMinorReleases = append(majorMinorReleases, key)
	}
	sort.Strings(streams)
	sort.Strings(majorMinorReleases)
	streamsOptions := modals.BuildOptions(streams, nil)
	majorMinorOptions := modals.BuildOptions(majorMinorReleases, nil)
	metadata := fmt.Sprintf("Architecture: %s;Platform: %s;%s: %s", architecture, platform, launchModeContext, strings.Join(mode.List(), ","))
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: string(IdentifierFilterVersionView),
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
					Text: "*To get a list of versions to select from, specify the _stream_ and/or _major.minor_*\nIf the *_stable_* stream is selected, please specify the *_major.minor_* as well.\nFor any other *_streams_*, you may omit the major.minor",
				},
			},
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  launchFromStream,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Specify the Stream:"},
				Element: &slackClient.SelectBlockElement{
					Type:        slackClient.OptTypeStatic,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select an entry..."},
					Options:     streamsOptions,
				},
			},
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  launchFromMajorMinor,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Specify the Major.Minor:"},
				Element: &slackClient.SelectBlockElement{
					Type:        slackClient.OptTypeStatic,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select an entry..."},
					Options:     majorMinorOptions,
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
				BlockID:  launchFromLatestBuild,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "The latest build (nightly) or CI build:"},
				Element: &slackClient.SelectBlockElement{
					Type:        slackClient.OptTypeStatic,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select an entry..."},
					Options: []*slackClient.OptionBlockObject{
						{Value: "nightly", Text: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: nightly}},
						{Value: "ci", Text: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: ci}},
					},
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
				BlockID:  launchFromCustom,
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
}

func PRInputView(callback *slackClient.InteractionCallback, data callbackData) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}
	platform := data.context[launchPlatform]
	architecture := data.context[launchArchitecture]
	mode := data.context[launchMode]
	launchModeSplit := strings.Split(mode, ",")
	launchWithVersion := false
	for _, key := range launchModeSplit {
		if strings.TrimSpace(key) == launchVersion {
			launchWithVersion = true
		}
	}
	metadata := fmt.Sprintf("Architecture: %s; Platform: %s;%s: %s", architecture, platform, launchModeContext, mode)
	if launchWithVersion {
		version := data.input[launchVersion]
		if version == "" {
			version = data.input[launchFromLatestBuild]
		}
		if version == "" {
			version = data.input[launchFromCustom]
		}
		metadata = fmt.Sprintf("%s;Version: %s", metadata, version)
	}
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: string(IdentifierPRInputView),
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
				BlockID: launchFromPR,
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

func SelectVersionView(callback *slackClient.InteractionCallback, jobmanager manager.JobManager, httpclient *http.Client, data callbackData) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}

	platform := data.context[launchPlatform]
	architecture := data.context[launchArchitecture]
	mode := data.context[launchMode]
	selectedStream := data.input[launchFromStream]
	selectedMajonMinor := data.input[launchFromMajorMinor]
	metadata := fmt.Sprintf("Architecture: %s; Platform: %s; %s: %s", architecture, platform, launchModeContext, mode)
	releases, err := fetchReleases(httpclient, architecture)
	if err != nil {
		// TODO - return an error view, with a try again
		klog.Warningf("failed to fetch the data from release controller: %s", err)
	}
	var allTags []string
	for stream, tags := range releases {
		if stream == selectedStream {
			for _, tag := range tags {
				if strings.HasPrefix(tag, selectedMajonMinor) {
					allTags = append(allTags, tag)
				}
			}

		}
	}
	//sort.Strings(allTags)
	allTagsOptions := modals.BuildOptions(allTags, nil)
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: string(IdentifierSelectVersion),
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
				Type:     slackClient.MBTInput,
				BlockID:  launchVersion,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select a version:"},
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
