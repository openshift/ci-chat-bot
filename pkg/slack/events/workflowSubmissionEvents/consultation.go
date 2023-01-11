package workflowSubmissionEvents

import (
	"github.com/openshift/ci-chat-bot/pkg/jira"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"text/template"
)

const ConsultationIdentifier Identifier = "consultation"

const (
	BlockIdRequirement        = "consultation_requirement"
	BlockIdPrevious           = "consultation_previous"
	BlockIdAcceptanceCriteria = "consultation_acceptance_criteria"
	BlockIdAdditional         = "consultation_additional"
)

func consultationParameters() JiraIssueParameters {
	return JiraIssueParameters{
		Id:        ConsultationIdentifier,
		IssueType: jira.IssueTypeStory,
		Template: template.Must(template.New(string(ConsultationIdentifier)).Funcs(modals.BulletListFunc()).Parse(`h3. Requirement
{{ .` + BlockIdRequirement + ` }}

h3. Previous Efforts
{{ .` + BlockIdPrevious + ` }}

h3. Acceptance Criteria
{{ toBulletList .` + BlockIdAcceptanceCriteria + ` }}

{{- if .` + BlockIdAdditional + ` }}

h3. Additional Details
{{ .` + BlockIdAdditional + ` }}
{{- end }}`)),
		Fields: []string{modals.BlockIdTitle, BlockIdRequirement, BlockIdPrevious, BlockIdAcceptanceCriteria, BlockIdAdditional},
	}
}
