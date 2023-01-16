package launch

import (
	"fmt"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	slackClient "github.com/slack-go/slack"
	"k8s.io/apimachinery/pkg/util/sets"
	"net/http"
	"strings"
)

const (
	stableReleasesPrefix        = "4-stable"
	launchFromPR                = "prs"
	launchFromMajorMinor        = "major_minor"
	launchFromStream            = "stream"
	launchFromLatestBuild       = "latest_build"
	launchFromReleaseController = "release_controller"
	launchFromCustom            = "custom"
	launchPlatform              = "platform"
	launchArchitecture          = "architecture"
	launchParameters            = "parameters"
	launchVersion               = "version"
	launchStepContext           = "context"
	defaultPlatform             = "aws"
	defaultArchitecture         = "amd64"
	defaultLaunchVersion        = "Latest Nightly"
)

// FirstStepView is the modal view for submitting a new enhancement card to Jira
func FirstStepView() slackClient.ModalViewRequest {
	platformOptions := buildOptions(manager.SupportedPlatforms, nil)
	architectureOptions := buildOptions(manager.SupportedArchitectures, nil)
	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: string(Identifier),
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

func SecondStepView(callback *slackClient.InteractionCallback, jobmanager manager.JobManager, httpclient *http.Client, data callbackData) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}
	platform, ok := data.selection[launchPlatform]
	if !ok {
		platform = defaultPlatform
	}
	architecture, ok := data.selection[launchArchitecture]
	if !ok {
		architecture = defaultArchitecture
	}
	metadata := fmt.Sprintf("Architecture: %s; Platform: %s", architecture, platform)
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
	streamsOptions := buildOptions(streams, nil)
	majorMinorOptions := buildOptions(majorMinorReleases, nil)

	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: string(Identifier2ndStep),
		Title:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Launch a Cluster"},
		Close:           &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Cancel"},
		Submit:          &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Next"},
		Blocks: slackClient.Blocks{BlockSet: []slackClient.Block{
			&slackClient.HeaderBlock{
				Type: slackClient.MBTHeader,
				Text: &slackClient.TextBlockObject{
					Type:     slackClient.PlainTextType,
					Text:     "Select a Version or enter a PR",
					Emoji:    false,
					Verbatim: false,
				},
			},
			&slackClient.SectionBlock{
				Type: slackClient.MBTSection,
				Text: &slackClient.TextBlockObject{
					Type: slackClient.MarkdownType,
					Text: "You can select a version from one of the drop-downs, and/or one or multiple PRs. To specify more that one PR, use a coma separator ",
				},
			},
			&slackClient.DividerBlock{
				Type:    slackClient.MBTDivider,
				BlockID: "divider",
			},
			&slackClient.SectionBlock{
				Type: slackClient.MBTSection,
				Text: &slackClient.TextBlockObject{
					Type: slackClient.MarkdownType,
					Text: "Select *only one of the following:* a Version, Stream name, Major/Minor or a Custom Build.",
				},
			},
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  launchFromReleaseController,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "A version directly from Release-Controller:"},
				Element: &slackClient.PlainTextInputBlockElement{
					Type:        slackClient.METPlainTextInput,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Enter a version...."},
				},
			},
			&slackClient.SectionBlock{
				Type:    slackClient.MBTSection,
				Text:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select a Version from Release Controller"},
				BlockID: "release_controller_link",
				Accessory: &slackClient.Accessory{
					ButtonElement: &slackClient.ButtonBlockElement{
						Type:     slackClient.METButton,
						Text:     &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Link"},
						ActionID: "button-action",
						URL:      fmt.Sprintf("https://%s.ocp.releases.ci.openshift.org", architecture),
						Value:    "click_release_controller_link",
						Style:    slackClient.StylePrimary,
					},
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
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  launchFromStream,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "A Stream Name:"},
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
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Major/Minor"},
				Element: &slackClient.SelectBlockElement{
					Type:        slackClient.OptTypeStatic,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Select an entry..."},
					Options:     majorMinorOptions,
				},
			},
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  launchFromCustom,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Custom build:"},
				Element: &slackClient.PlainTextInputBlockElement{
					Type:        slackClient.METPlainTextInput,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Enter a custom build...."},
				},
			},
			&slackClient.DividerBlock{
				Type:    slackClient.MBTDivider,
				BlockID: "2nd_divider",
			},
			&slackClient.SectionBlock{
				Type: slackClient.MBTSection,
				Text: &slackClient.TextBlockObject{
					Type: slackClient.MarkdownType,
					Text: "Enter one or more PRs, separated by coma",
				},
			},
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  launchFromPR,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Enter one or more PRs:"},
				Element: &slackClient.PlainTextInputBlockElement{
					Type:        slackClient.METPlainTextInput,
					Placeholder: &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: "Enter one or more PRs..."},
				},
			},
			&slackClient.DividerBlock{
				Type:    slackClient.MBTDivider,
				BlockID: "3rd_divider",
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

func ThirdStepView(callback *slackClient.InteractionCallback, jobmanager manager.JobManager, httpclient *http.Client, data callbackData) slackClient.ModalViewRequest {
	if callback == nil {
		return slackClient.ModalViewRequest{}
	}
	platform, _ := data.context["platform"]
	prs, ok := data.input[launchFromPR]
	if !ok {
		prs = "None"
	}
	version, ok := data.selection[launchFromLatestBuild]
	if !ok {
		version, ok = data.selection[launchFromMajorMinor]
		if !ok {
			version, ok = data.selection[launchFromStream]
			if !ok {
				version, ok = data.input[launchFromReleaseController]
			}
			if !ok {
				version = defaultLaunchVersion
			}
		}
	}
	// get a list of unsuported parameters and filter them from the options, do not modify the slice!!!
	architecture, _ := data.context[launchArchitecture]
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
	options := buildOptions(manager.SupportedParameters, blacklist)
	context := fmt.Sprintf("Architecture: %s;Platform: %s;Version: %s;PRs: %s", architecture, platform, version, prs)

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
