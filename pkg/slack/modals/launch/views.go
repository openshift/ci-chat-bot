package launch

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/common"
	slackClient "github.com/slack-go/slack"
	"k8s.io/apimachinery/pkg/util/sets"
)

func FirstStepView() slackClient.ModalViewRequest {
	return FirstStepViewWithData(modals.CallbackData{})
}

func FirstStepViewWithData(data modals.CallbackData) slackClient.ModalViewRequest {
	platformOptions := modals.BuildOptions(manager.SupportedPlatforms, nil)
	architectureOptions := modals.BuildOptions(manager.SupportedArchitectures, nil)

	// Get initial selections from callback data
	var platformInitial, architectureInitial *slackClient.OptionBlockObject
	if platform, ok := data.Input[modals.LaunchPlatform]; ok && platform != "" {
		platformInitial = &slackClient.OptionBlockObject{
			Value: platform,
			Text:  &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: platform},
		}
	}
	if arch, ok := data.Input[modals.LaunchArchitecture]; ok && arch != "" {
		architectureInitial = &slackClient.OptionBlockObject{
			Value: arch,
			Text:  &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: arch},
		}
	}

	return slackClient.ModalViewRequest{
		Type:            slackClient.VTModal,
		PrivateMetadata: modals.CallbackDataToMetadata(data, string(IdentifierInitialView)),
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
				BlockID:  modals.LaunchPlatform,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: fmt.Sprintf("Platform (Default - %s)", DefaultPlatform)},
				Element: &slackClient.SelectBlockElement{
					Type:          slackClient.OptTypeStatic,
					Placeholder:   &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: DefaultPlatform},
					Options:       platformOptions,
					InitialOption: platformInitial,
				},
			},
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  modals.LaunchArchitecture,
				Optional: true,
				Label:    &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: fmt.Sprintf("Architecture (Default - %s)", DefaultArchitecture)},
				Element: &slackClient.SelectBlockElement{
					Type:          slackClient.OptTypeStatic,
					Placeholder:   &slackClient.TextBlockObject{Type: slackClient.PlainTextType, Text: DefaultArchitecture},
					Options:       architectureOptions,
					InitialOption: architectureInitial,
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
	architecture := data.Input[modals.LaunchArchitecture]
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
	options := modals.BuildOptions(manager.SupportedParameters, blacklist)
	context := common.NewMetadataBuilder().
		Add("Architecture", architecture).
		Add("Platform", platform).
		Add("Version", version).
		Add("PR", prs).
		Build()
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
			&slackClient.InputBlock{
				Type:     slackClient.MBTInput,
				BlockID:  modals.LaunchParameters,
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
	platform, ok := data.Input[modals.LaunchPlatform]
	if !ok {
		platform = DefaultPlatform
	}
	architecture, ok := data.Input[modals.LaunchArchitecture]
	if !ok {
		architecture = DefaultArchitecture
	}
	metadata := common.NewMetadataBuilder().
		Add("Architecture", architecture).
		Add("Platform", platform).
		Build()

	return common.BuildSelectModeView(common.SelectModeViewConfig{
		Callback:        callback,
		Data:            data,
		ModalIdentifier: string(IdentifierRegisterLaunchMode),
		Title:           ModalTitle,
		PreviousStep:    string(IdentifierInitialView),
		ContextMetadata: metadata,
	})
}

func FilterVersionView(callback *slackClient.InteractionCallback, jobmanager manager.JobManager, data modals.CallbackData, httpclient *http.Client, mode sets.Set[string], noneSelected bool) slackClient.ModalViewRequest {
	platform := data.Input[modals.LaunchPlatform]
	architecture := data.Input[modals.LaunchArchitecture]
	metadata := common.NewMetadataBuilder().
		Add("Architecture", architecture).
		Add("Platform", platform).
		Add(modals.LaunchModeContext, strings.Join(sets.List(mode), ",")).
		Build()

	return common.BuildFilterVersionView(common.FilterVersionViewConfig{
		ReleaseViewConfig: common.ReleaseViewConfig{
			BaseViewConfig: common.BaseViewConfig{
				Callback:        callback,
				Data:            data,
				ModalIdentifier: string(IdentifierFilterVersionView),
				Title:           ModalTitle,
				PreviousStep:    string(IdentifierRegisterLaunchMode),
				ContextMetadata: metadata,
			},
			HTTPClient:   httpclient,
			Architecture: architecture,
		},
		JobManager:   jobmanager,
		NoneSelected: noneSelected,
	})
}

func PRInputView(callback *slackClient.InteractionCallback, data modals.CallbackData, previousStep string) slackClient.ModalViewRequest {
	platform := data.Input[modals.LaunchPlatform]
	architecture := data.Input[modals.LaunchArchitecture]
	mode := data.MultipleSelection[modals.LaunchMode]
	baseMetadata := common.NewMetadataBuilder().
		Add("Architecture", architecture).
		Add("Platform", platform).
		Add(modals.LaunchModeContext, strings.Join(mode, ",")).
		Build()
	metadata := common.BuildPRInputMetadata(data, baseMetadata)

	return common.BuildPRInputView(common.BaseViewConfig{
		Callback:        callback,
		Data:            data,
		ModalIdentifier: string(IdentifierPRInputView),
		Title:           ModalTitle,
		PreviousStep:    previousStep,
		ContextMetadata: metadata,
	})
}

func SelectVersionView(callback *slackClient.InteractionCallback, jobmanager manager.JobManager, httpclient *http.Client, data modals.CallbackData, previousStep string) slackClient.ModalViewRequest {
	platform := data.Input[modals.LaunchPlatform]
	architecture := data.Input[modals.LaunchArchitecture]
	mode := data.MultipleSelection[modals.LaunchMode]
	metadata := common.NewMetadataBuilder().
		Add("Architecture", architecture).
		Add("Platform", platform).
		Add(modals.LaunchModeContext, strings.Join(mode, ",")).
		Build()

	return common.BuildSelectVersionView(common.SelectVersionViewConfig{
		ReleaseViewConfig: common.ReleaseViewConfig{
			BaseViewConfig: common.BaseViewConfig{
				Callback:        callback,
				Data:            data,
				ModalIdentifier: string(IdentifierSelectVersion),
				Title:           ModalTitle,
				PreviousStep:    previousStep,
				ContextMetadata: metadata,
			},
			HTTPClient:   httpclient,
			Architecture: architecture,
		},
		SelectMinorMajorIdentifier: string(IdentifierSelectMinorMajor),
	})
}

func SelectMinorMajor(callback *slackClient.InteractionCallback, httpclient *http.Client, data modals.CallbackData, previousStep string) slackClient.ModalViewRequest {
	platform := data.Input[modals.LaunchPlatform]
	architecture := data.Input[modals.LaunchArchitecture]
	mode := data.MultipleSelection[modals.LaunchMode]
	selectedStream := data.Input[modals.LaunchFromStream]
	metadata := common.NewMetadataBuilder().
		Add("Architecture", architecture).
		Add("Platform", platform).
		Add(modals.LaunchModeContext, strings.Join(mode, ",")).
		Add(modals.LaunchFromStream, selectedStream).
		Build()

	return common.BuildSelectMinorMajorView(common.ReleaseViewConfig{
		BaseViewConfig: common.BaseViewConfig{
			Callback:        callback,
			Data:            data,
			ModalIdentifier: string(IdentifierSelectMinorMajor),
			Title:           ModalTitle,
			PreviousStep:    previousStep,
			ContextMetadata: metadata,
		},
		HTTPClient:   httpclient,
		Architecture: architecture,
	})
}
