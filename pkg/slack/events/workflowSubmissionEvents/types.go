package workflowSubmissionEvents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"text/template"
)

type workflowSubmit interface {
	WorkflowStepCompleted(workflowStepExecuteID string, outputs map[string]string) error
	WorkflowStepFailed(workflowStepExecuteID string, errorMessage string) error
}

type Identifier string

// JiraIssueParameters holds the metadata used to create a Jira issue
type JiraIssueParameters struct {
	Id        Identifier
	IssueType string
	Template  *template.Template
	Fields    []string
}

// workflowStepExecuteEvent mirrors the deprecated slackevents.WorkflowStepExecuteEvent
// which was removed from slack-go/slack v0.23.0+.
type workflowStepExecuteEvent struct {
	Type           string            `json:"type"`
	CallbackID     string            `json:"callback_id"`
	WorkflowStep   eventWorkflowStep `json:"workflow_step"`
	EventTimestamp string            `json:"event_ts"`
}

type eventWorkflowStep struct {
	WorkflowStepExecuteID string                `json:"workflow_step_execute_id"`
	WorkflowID            string                `json:"workflow_id"`
	WorkflowInstanceID    string                `json:"workflow_instance_id"`
	StepID                string                `json:"step_id"`
	Inputs                *WorkflowStepInputs   `json:"inputs,omitempty"`
	Outputs               *[]WorkflowStepOutput `json:"outputs,omitempty"`
}

type WorkflowStepInputElement struct {
	Value                   string `json:"value"`
	SkipVariableReplacement bool   `json:"skip_variable_replacement"`
}

type WorkflowStepInputs map[string]WorkflowStepInputElement

type WorkflowStepOutput struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Label string `json:"label"`
}

// SlackWorkflowClient wraps a tokenized HTTP client to call the deprecated
// Slack workflow step API methods (workflows.stepCompleted, workflows.stepFailed,
// workflows.updateStep).
type SlackWorkflowClient struct {
	token    string
	endpoint string
}

func NewSlackWorkflowClient(token string) *SlackWorkflowClient {
	return &SlackWorkflowClient{
		token:    token,
		endpoint: "https://slack.com/api/",
	}
}

type workflowUpdateStepRequest struct {
	WorkflowStepEditID string                `json:"workflow_step_edit_id"`
	Inputs             *WorkflowStepInputs   `json:"inputs,omitempty"`
	Outputs            *[]WorkflowStepOutput `json:"outputs,omitempty"`
}

type workflowStepCompletedRequest struct {
	WorkflowStepExecuteID string            `json:"workflow_step_execute_id"`
	Outputs               map[string]string `json:"outputs,omitempty"`
}

type workflowStepFailedRequest struct {
	WorkflowStepExecuteID string `json:"workflow_step_execute_id"`
	Error                 struct {
		Message string `json:"message"`
	} `json:"error"`
}

type slackResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (c *SlackWorkflowClient) WorkflowStepCompleted(workflowStepExecuteID string, outputs map[string]string) error {
	r := &workflowStepCompletedRequest{
		WorkflowStepExecuteID: workflowStepExecuteID,
		Outputs:               outputs,
	}
	return c.postJSON("workflows.stepCompleted", r)
}

func (c *SlackWorkflowClient) WorkflowStepFailed(workflowStepExecuteID string, errorMessage string) error {
	r := &workflowStepFailedRequest{
		WorkflowStepExecuteID: workflowStepExecuteID,
	}
	r.Error.Message = errorMessage
	return c.postJSON("workflows.stepFailed", r)
}

func (c *SlackWorkflowClient) SaveWorkflowStepConfiguration(workflowStepEditID string, inputs *WorkflowStepInputs, outputs *[]WorkflowStepOutput) error {
	r := &workflowUpdateStepRequest{
		WorkflowStepEditID: workflowStepEditID,
		Inputs:             inputs,
		Outputs:            outputs,
	}
	return c.postJSON("workflows.updateStep", r)
}

func (c *SlackWorkflowClient) postJSON(method string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(context.Background(), "POST", c.endpoint+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var sr slackResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return err
	}
	if !sr.OK {
		return fmt.Errorf("slack API %s: %s", method, sr.Error)
	}
	return nil
}
