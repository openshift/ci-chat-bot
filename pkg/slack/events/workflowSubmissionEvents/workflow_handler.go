package workflowSubmissionEvents

import (
	"bytes"
	"fmt"
	jiraClient "github.com/andygrunwald/go-jira"
	"github.com/openshift/ci-chat-bot/pkg/jira"
	"github.com/openshift/ci-chat-bot/pkg/slack/events"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

const (
	// BlockIdTitle is the block identifier to use for inputs
	// that should be used as the title of a Jira issue
	BlockIdTitle = "title"
)

const (
	TicketTitle = "ticket_title"
	UserDetails = "user_details"
)

func Handler(client workflowSubmit, filer jira.IssueFiler) events.PartialHandler {
	return events.PartialHandlerFunc("workflow-execution-event", func(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (handled bool, err error) {
		if callback.Type != slackevents.CallbackEvent {
			return false, nil
		}
		event, ok := callback.InnerEvent.Data.(*slackevents.WorkflowStepExecuteEvent)
		if !ok {
			return false, nil
		}
		switch event.CallbackID {
		case "jira_ticket":
			err := handleJiraStep(client, event, filer)
			if err != nil {
				error := client.WorkflowStepFailed(event.WorkflowStep.WorkflowStepExecuteID, err.Error())
				if error != nil {
					return false, error
				}
				return false, err
			}
		}
		return true, err
	})
}

func checkTicketType(event *slackevents.WorkflowStepExecuteEvent) (ticketType string, supported bool) {
	for key, output := range *event.WorkflowStep.Inputs {
		if key == "ticket_type" {
			switch output.Value {
			case string(BugIdentifier):
				return string(BugIdentifier), true
			case string(ConsultationIdentifier):
				return string(ConsultationIdentifier), true
			case string(EnhancementIdentifier):
				return string(EnhancementIdentifier), true
			default:
				return output.Value, false
			}
		}
	}
	return "unsupported_ticket_type", false
}

func handleJiraStep(client workflowSubmit, event *slackevents.WorkflowStepExecuteEvent, filer jira.IssueFiler) error {
	ticketType, isSupported := checkTicketType(event)

	if !isSupported {
		return fmt.Errorf("unsuported ticket type %s", ticketType)
	}
	var issue *jiraClient.Issue
	var err error

	switch ticketType {
	case string(BugIdentifier):
		issue, err = fileTicket(event, BugParameters(), filer)
		if err != nil {
			return err
		}
	case string(EnhancementIdentifier):
		issue, err = fileTicket(event, enhancementParameters(), filer)
		if err != nil {
			return err
		}
	case string(ConsultationIdentifier):
		issue, err = fileTicket(event, consultationParameters(), filer)
		if err != nil {
			return err
		}
	}
	outgoingOutputs := make(map[string]string)
	for _, incomingOutputs := range *event.WorkflowStep.Outputs {
		switch incomingOutputs.Name {
		case "issue.key":
			outgoingOutputs[incomingOutputs.Name] = issue.Key
		case "issue.link":
			outgoingOutputs[incomingOutputs.Name] = issue.Self
		case "issue.summary":
			outgoingOutputs[incomingOutputs.Name] = issue.Fields.Summary
		}
	}
	options := slack.WorkflowStepCompletedRequestOptionOutput(outgoingOutputs)
	err = client.WorkflowStepCompleted(event.WorkflowStep.WorkflowStepExecuteID, options)
	if err != nil {
		return err
	}
	return nil
}

func fileTicket(event *slackevents.WorkflowStepExecuteEvent, parameters JiraIssueParameters, filer jira.IssueFiler) (*jiraClient.Issue, error) {
	title := "not_defined"
	reporter := "not_defined"
	data := make(map[string]string)
	for key, inputs := range *event.WorkflowStep.Inputs {
		if key == TicketTitle {
			title = inputs.Value
			continue
		}
		if key == UserDetails {
			reporter = inputs.Value
			continue
		}
		data[key] = inputs.Value
	}
	body := &bytes.Buffer{}
	if err := parameters.Template.Execute(body, data); err != nil {
		return nil, fmt.Errorf("failed to render %s template: %w", parameters.Id, err)
	}
	logger := logrus.WithField("api", "events")
	issue, err := filer.FileIssue(jira.IssueTypeBug, title, body.String(), reporter, logger)
	if err != nil {
		logger.WithError(err).Errorf("Failed to create %s Jira.", parameters.Id)
		return nil, err
	}
	return issue, nil
}
