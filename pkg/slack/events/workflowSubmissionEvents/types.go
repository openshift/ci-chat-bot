package workflowSubmissionEvents

import (
	"text/template"

	"github.com/slack-go/slack"
)

type workflowSubmit interface {
	WorkflowStepCompleted(workflowStepExecuteID string, options ...slack.WorkflowStepCompletedRequestOption) error
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
