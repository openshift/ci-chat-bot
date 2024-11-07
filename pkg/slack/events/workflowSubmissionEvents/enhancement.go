package workflowSubmissionEvents

import (
	"text/template"

	"github.com/openshift/ci-chat-bot/pkg/jira"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
)

const EnhancementIdentifier Identifier = "enhancement"

const (
	BlockIdAsA            = "as_a"
	BlockIdIWant          = "i_want"
	BlockIdSoThat         = "so_that"
	BlockIdSummary        = "enhancement_summary"
	BlockIdImplementation = "enhancement_implementation"
)

func enhancementParameters() JiraIssueParameters {
	return JiraIssueParameters{
		Id:        EnhancementIdentifier,
		IssueType: jira.IssueTypeStory,
		Template: template.Must(template.New(string(EnhancementIdentifier)).Funcs(modals.BulletListFunc()).Parse(`h3. Overview
As a {{ .` + BlockIdAsA + ` }}
I want {{ .` + BlockIdIWant + ` }}
So that {{ .` + BlockIdSoThat + ` }}

h3. Summary
{{ .` + BlockIdSummary + ` }}

{{- if .` + BlockIdImplementation + ` }}

h3. Implementation Details
{{ .` + BlockIdImplementation + ` }}
{{- end }}`)),
		Fields: []string{modals.BlockIdTitle, BlockIdAsA, BlockIdIWant, BlockIdSoThat, BlockIdSummary, BlockIdImplementation},
	}
}
