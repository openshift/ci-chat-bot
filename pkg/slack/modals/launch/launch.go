package launch

import (
	"encoding/json"
	"fmt"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"k8s.io/apimachinery/pkg/util/sets"
	"net/http"
	"strings"
)

// Identifier is the view identifier for this modal
const Identifier modals.Identifier = "launch"
const Identifier2ndStep modals.Identifier = "launch2ndStep"
const Identifier3rdStep modals.Identifier = "launch3ddStep"

type callbackData struct {
	input             map[string]string
	selection         map[string]string
	multipleSelection map[string][]string
	context           map[string]string
}

func RegisterFirstStep(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(Identifier, FirstStepView()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextForFirstStep(client, jobmanager, httpclient),
	})
}

func processNextForFirstStep(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc("launch2", func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		go func() {
			callbackData := callbackData{
				input:     callbackInput(callback),
				selection: callbackSelection(callback),
			}
			overwriteView := func(view slack.ModalViewRequest) {
				// don't pass a hash, so we overwrite the View always
				response, err := updater.UpdateView(view, "", "", callback.View.ID)
				if err != nil {
					logger.WithError(err).Warn("Failed to update a modal View.")
				}
				logger.WithField("response", response).Trace("Got a modal response.")
			}
			overwriteView(SecondStepView(callback, jobmanager, httpclient, callbackData))
		}()
		response, err := json.Marshal(&slack.ViewSubmissionResponse{
			ResponseAction: slack.RAUpdate,
			View:           PrepareNextStepView(),
		})
		if err != nil {
			logger.WithError(err).Error("Failed to marshal FirstStepView update submission response.")
			return nil, err
		}
		return response, nil
	})
}

func RegisterSecondStep(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(Identifier2ndStep, ThirdStepView(nil, jobmanager, httpclient, callbackData{})).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextForSecondStep(client, jobmanager, httpclient),
	})
}

func processNextForSecondStep(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc("launch3", func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		go func() {
			callbackData := callbackData{
				input:     callbackInput(callback),
				selection: callbackSelection(callback),
				context:   callbackContext(callback),
			}
			overwriteView := func(view slack.ModalViewRequest) {
				// don't pass a hash, so we overwrite the View always
				response, err := updater.UpdateView(view, "", "", callback.View.ID)
				if err != nil {
					logger.WithError(err).Warn("Failed to update a modal View.")
				}
				logger.WithField("response", response).Trace("Got a modal response.")
			}
			overwriteView(ThirdStepView(callback, jobmanager, httpclient, callbackData))
		}()
		response, err := json.Marshal(&slack.ViewSubmissionResponse{
			ResponseAction: slack.RAUpdate,
			View:           PrepareNextStepView(),
		})
		if err != nil {
			logger.WithError(err).Error("Failed to marshal FirstStepView update submission response.")
			return nil, err
		}
		return response, nil
	})
}

func RegisterThirdStep(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(Identifier3rdStep, SubmissionView("")).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextForThirdStep(client, jobmanager, httpclient),
	})
}

func processNextForThirdStep(updater modals.ViewUpdater, jobmanager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(string(Identifier3rdStep), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		go func() {
			var launchInputs []string
			data := callbackData{
				input:             callbackInput(callback),
				selection:         callbackSelection(callback),
				multipleSelection: callbackMultipleSelect(callback),
				context:           callbackContext(callback),
			}
			platform, _ := data.context[launchPlatform]
			architecture, _ := data.context[launchArchitecture]
			version, ok := data.context[launchVersion]
			if !ok || version == defaultLaunchVersion {
				_, version, _, _ = jobmanager.ResolveImageOrVersion("nightly", "", architecture)
			}
			launchInputs = append(launchInputs, version)
			prs, ok := data.context[launchFromPR]
			if ok && prs != "None" {
				prSlice := strings.Split(prs, ",")
				for _, pr := range prSlice {
					launchInputs = append(launchInputs, strings.TrimSpace(pr))
				}
			}
			parameters, _ := data.multipleSelection[launchParameters]
			parametersMap := make(map[string]string)
			for _, parameter := range parameters {
				parametersMap[parameter] = ""
			}
			launch := fmt.Sprintf("launch %s %s,%s,%s ", version, architecture, platform, strings.Join(parameters[:], ","))
			msg, err := jobmanager.LaunchJobForUser(&manager.JobRequest{
				OriginalMessage: launch,
				User:            callback.User.ID,
				UserName:        callback.User.Name,
				Inputs:          [][]string{launchInputs},
				Type:            manager.JobTypeInstall,
				Platform:        platform,
				Channel:         callback.User.ID,
				JobParams:       parametersMap,
				Architecture:    architecture,
			})
			overwriteView := func(view slack.ModalViewRequest) {
				// don't pass a hash, so we overwrite the View always
				response, err := updater.UpdateView(view, "", "", callback.View.ID)
				if err != nil {
					logger.WithError(err).Warn("Failed to update a modal View.")
				}
				logger.WithField("response", response).Trace("Got a modal response.")
			}
			if err != nil {
				// TODO if error, update the view with the error message, and load back the last step
				overwriteView(SubmissionView(err.Error()))
			} else {
				overwriteView(SubmissionView(msg))
			}

		}()
		response, err := json.Marshal(&slack.ViewSubmissionResponse{
			ResponseAction: slack.RAUpdate,
			View:           PrepareNextStepView(),
		})
		if err != nil {
			logger.WithError(err).Error("Failed to marshal FirstStepView update submission response.")
			return nil, err
		}
		return response, nil
	})
}

func buildOptions(options []string, blacklist sets.String) []*slack.OptionBlockObject {
	slackOptions := make([]*slack.OptionBlockObject, 0)
	for _, parameter := range options {
		if !blacklist.Has(parameter) {
			slackOptions = append(slackOptions, &slack.OptionBlockObject{
				Value: parameter,
				Text: &slack.TextBlockObject{
					Type: slack.PlainTextType,
					Text: parameter,
				},
			})
		}
	}
	return slackOptions
}

func fetchReleases(client *http.Client, architecture string) (map[string][]string, error) {
	url := fmt.Sprintf("https://%s.ocp.releases.ci.openshift.org/api/v1/releasestreams/accepted", architecture)
	acceptedReleases := make(map[string][]string, 0)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&acceptedReleases); err != nil {
		return nil, err
	}
	return acceptedReleases, nil
}

func callbackSelection(callback *slack.InteractionCallback) map[string]string {
	selectionValues := make(map[string]string)
	for key, value := range callback.View.State.Values {
		for _, v := range value {
			if v.SelectedOption.Value != "" {
				selectionValues[key] = v.SelectedOption.Text.Text
			}
		}
	}
	return selectionValues
}

func callbackInput(callback *slack.InteractionCallback) map[string]string {
	selectionValues := make(map[string]string)
	for key, value := range callback.View.State.Values {
		for _, v := range value {
			if v.Value != "" {
				selectionValues[key] = v.Value
			}
		}
	}
	return selectionValues
}

func callbackMultipleSelect(callback *slack.InteractionCallback) map[string][]string {
	selectedValues := make(map[string][]string)
	for key, value := range callback.View.State.Values {
		var selections []string
		for _, v := range value {
			if len(v.SelectedOptions) > 0 {
				for _, selection := range v.SelectedOptions {
					selections = append(selections, selection.Value)
				}
				selectedValues[key] = selections
			}
		}
	}
	return selectedValues
}

func callbackContext(callback *slack.InteractionCallback) map[string]string {
	contextMap := make(map[string]string)
	var context string
	for _, value := range callback.View.Blocks.BlockSet {
		if value.BlockType() == slack.MBTContext {
			metadata, ok := value.(*slack.ContextBlock)
			if ok {
				text, ok := metadata.ContextElements.Elements[0].(*slack.TextBlockObject)
				if ok {
					context = text.Text
				}
			}
		}
	}
	mSplit := strings.Split(context, ";")
	for _, v := range mSplit {
		switch strings.ToLower(strings.TrimSpace(strings.Split(v, ":")[0])) {
		case launchArchitecture:
			contextMap[launchArchitecture] = strings.TrimSpace(strings.Split(v, ":")[1])
		case launchPlatform:
			contextMap[launchPlatform] = strings.TrimSpace(strings.Split(v, ":")[1])
		case launchVersion:
			contextMap[launchVersion] = strings.TrimSpace(strings.Split(v, ":")[1])
		case launchFromPR:
			contextMap[launchFromPR] = strings.TrimSpace(strings.Split(v, ":")[1])
		}
	}
	return contextMap
}
