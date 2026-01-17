package create

import (
	"fmt"
	"net/http"
	"strings"
	"time"

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
	context := common.NewMetadataBuilder().
		Add("Duration", duration).
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
		platform = defaultPlatform
	}
	duration, ok := data.Input[CreateDuration]
	if !ok {
		duration = defaultDuration
	}
	metadata := common.NewMetadataBuilder().
		Add("Platform", platform).
		Add("Duration", duration).
		Build()

	return common.BuildSelectModeView(common.SelectModeViewConfig{
		Callback:        callback,
		Data:            data,
		ModalIdentifier: string(IdentifierSelectModeView),
		Title:           ModalTitle,
		PreviousStep:    string(IdentifierInitialView),
		ContextMetadata: metadata,
	})
}

func FilterVersionView(callback *slackClient.InteractionCallback, jobmanager manager.JobManager, data modals.CallbackData, httpclient *http.Client, mode sets.Set[string], noneSelected bool) slackClient.ModalViewRequest {
	platform := data.Input[modals.LaunchPlatform]
	duration := data.Input[CreateDuration]
	metadata := common.NewMetadataBuilder().
		Add("Duration", duration).
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
				PreviousStep:    string(IdentifierSelectModeView),
				ContextMetadata: metadata,
			},
			HTTPClient:   httpclient,
			Architecture: "amd64", // MCE clusters use amd64
		},
		JobManager:   jobmanager,
		NoneSelected: noneSelected,
	})
}

func PRInputView(callback *slackClient.InteractionCallback, data modals.CallbackData, previousStep string) slackClient.ModalViewRequest {
	platform := data.Input[modals.LaunchPlatform]
	duration := data.Input[CreateDuration]
	mode := data.MultipleSelection[modals.LaunchMode]
	baseMetadata := common.NewMetadataBuilder().
		Add("Duration", duration).
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
	duration := data.Input[CreateDuration]
	mode := data.MultipleSelection[modals.LaunchMode]
	metadata := common.NewMetadataBuilder().
		Add("Duration", duration).
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
			Architecture: "amd64", // MCE clusters use amd64
		},
		SelectMinorMajorIdentifier: string(IdentifierSelectMinorMajor),
	})
}

func SelectMinorMajor(callback *slackClient.InteractionCallback, httpclient *http.Client, data modals.CallbackData, previousStep string) slackClient.ModalViewRequest {
	platform := data.Input[modals.LaunchPlatform]
	duration := data.Input[CreateDuration]
	mode := data.MultipleSelection[modals.LaunchMode]
	selectedStream := data.Input[modals.LaunchFromStream]
	metadata := common.NewMetadataBuilder().
		Add("Duration", duration).
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
		Architecture: "amd64", // MCE clusters use amd64
	})
}
