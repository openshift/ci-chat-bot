package workflowSubmissionEvents

import (
	"github.com/openshift/ci-chat-bot/pkg/jira"
	"text/template"
)

const BugIdentifier Identifier = "bug"

const (
	AffectedComponent  = "affected_component"
	OtherComponent     = "other_component_not_listed"
	IncorrectBehaviour = "incorrect_behaviour"
	ExpectedBehaviour  = "expected_behaviour"
	Impact             = "impact"
	IsReproducible     = "is_reproducible"
)

func BugParameters() JiraIssueParameters {
	return JiraIssueParameters{
		Id:        BugIdentifier,
		IssueType: jira.IssueTypeBug,
		Template: template.Must(template.New(string(BugIdentifier)).Parse(`h3. Symptomatic Behavior
{{ .` + IncorrectBehaviour + ` }}

h3. Expected Behavior
{{ .` + ExpectedBehaviour + ` }}

h3. Impact
{{ .` + Impact + ` }}

h3. Category
{{if eq .` + AffectedComponent + ` "Other"}}{{ .` + OtherComponent + ` }}{{else}}{{ .` + AffectedComponent + ` }}{{end}}

h3. How to Reproduce
{{ .` + IsReproducible + ` }}`)),
		Fields: []string{BlockIdTitle, AffectedComponent, OtherComponent, IncorrectBehaviour, ExpectedBehaviour, Impact, IsReproducible},
	}
}
