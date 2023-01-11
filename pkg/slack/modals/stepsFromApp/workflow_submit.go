package stepsFromApp

import (
	"github.com/slack-go/slack"
)

func StepFromAppSubmit(callback *slack.InteractionCallback) (slack.WorkflowStepInputs, []slack.WorkflowStepOutput) {
	inputMap := make(slack.WorkflowStepInputs)
	outputMap := []slack.WorkflowStepOutput{
		{
			Name:  "issue.key",
			Type:  "text",
			Label: "Issue Key",
		},
		{
			Name:  "issue.link",
			Type:  "text",
			Label: "Issue Link",
		},
		{
			Name:  "issue.summary",
			Type:  "text",
			Label: "Issue Summary Field",
		},
	}
	for key, text := range callback.View.State.Values {
		var input slack.WorkflowStepInputElement
		for _, subInput := range text {
			// we expect a single value here. If the array has multiple arrays, the last element proceeds.
			input.Value = subInput.Value
			input.SkipVariableReplacement = false
		}
		inputMap[key] = input
	}
	return inputMap, outputMap
}
