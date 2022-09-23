package main

import (
	"fmt"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/pkg/version"
	"k8s.io/klog"
	"strings"
)

func HandleEvent(client *slack.Client, callback *slackevents.EventsAPIEvent, manager JobManager, botCommands []BotCommand) error {
	if callback.Type != slackevents.CallbackEvent {
		return nil
	}
	event, ok := callback.InnerEvent.Data.(*slackevents.MessageEvent)
	if !ok {
		return fmt.Errorf("failed to parse the slack event")
	}
	if strings.TrimSpace(event.Text) == "help" {
		help(client, event, botCommands)
		return nil
	}
	// do not respond to bots
	if event.BotID != "" {
		return nil
	}
	// do not respond to indirect messages
	if !strings.HasPrefix(event.Channel, "D") {
		_, _, err := client.PostMessage(event.Channel, slack.MsgOptionText("this command is only accepted via direct message)", false))
		if err != nil {
			return err
		}
		return nil
	}
	// do not respond if the event SubType is message_changed or file_share( in cases a link is posted and a preview is
	// added afterwards and when an attachment is included)
	if event.SubType == "message_changed" || event.SubType == "file_share" {
		return nil
	}
	for _, command := range botCommands {
		properties, match := command.Match(event.Text)
		if match {
			response := command.Execute(client, manager, event, properties)
			_, responseTimestamp, err := client.PostMessage(event.Channel, slack.MsgOptionText(response, false))
			if err != nil {
				klog.Errorf("Failed to post response: %s; to UserID: %s; (event: `%s`) at %s", response, event.User, event.Text, responseTimestamp)
				return fmt.Errorf("failed to post the response to the requested action: %s", event.Text)
			}
			klog.Infof("Posted response to UserID: %s (event: `%s`) at %s", event.User, event.Text, responseTimestamp)
			return nil
		}
	}
	_, _, err := client.PostMessage(event.Channel, slack.MsgOptionText("unrecognized command, msg me `help` for a list of all commands", false))
	if err != nil {
		klog.Errorf("Failed to post response to %s: %v", event.Text, err)
		return fmt.Errorf("failed to post the response to the requested action: %s", event.Text)
	}
	return nil
}

func getUserName(client *slack.Client, userID string) string {
	user, err := client.GetUserInfo(userID)
	if err != nil {
		klog.Warningf("Failed to get the User details for UserID: %s", userID)
	}
	if strings.HasSuffix(user.Profile.Email, "@redhat.com") {
		return strings.TrimSuffix(user.Profile.Email, "@redhat.com")
	}
	klog.Warningf("Failed to get the User details for UserID: %s", userID)
	return ""
}

func LaunchCluster(client *slack.Client, manager JobManager, event *slackevents.MessageEvent, properties *Properties) string {
	userName := getUserName(client, event.User)
	from, err := parseImageInput(properties.StringParam("image_or_version_or_pr", ""))
	if err != nil {
		return err.Error()
	}
	var inputs [][]string
	if len(from) > 0 {
		inputs = [][]string{from}
	}

	platform, architecture, params, err := parseOptions(properties.StringParam("options", ""))
	if err != nil {
		return err.Error()
	}
	if len(params["test"]) > 0 {
		return "TestUpgrade arguments may not be passed from the launch command"
	}

	msg, err := manager.LaunchJobForUser(&JobRequest{
		OriginalMessage: event.Text,
		User:            event.User,
		UserName:        userName,
		Inputs:          inputs,
		Type:            JobTypeInstall,
		Channel:         event.Channel,
		Platform:        platform,
		JobParams:       params,
		Architecture:    architecture,
	})
	if err != nil {
		return err.Error()
	}
	return msg
}

func Lookup(client *slack.Client, manager JobManager, event *slackevents.MessageEvent, properties *Properties) string {
	from, err := parseImageInput(properties.StringParam("image_or_version_or_pr", ""))
	if err != nil {
		return err.Error()
	}
	architectureRaw, err := parseImageInput(properties.StringParam("architecture", ""))
	if err != nil {
		return err.Error()
	} else if len(architectureRaw) > 1 {
		return "Error: cannot specify more than one architecture for this command"
	}
	architecture := "amd64" // default arch
	if len(architectureRaw) == 1 {
		architecture = architectureRaw[0]
	}
	if !sets.NewString(supportedArchitectures...).Has(architecture) {
		return fmt.Sprintf("Error: %s is an invalid architecture. Supported architectures: %v", architecture, supportedArchitectures)
	}
	msg, err := manager.LookupInputs(from, architecture)
	if err != nil {
		return err.Error()
	}
	return msg
}

func List(client *slack.Client, manager JobManager, event *slackevents.MessageEvent, properties *Properties) string {
	return manager.ListJobs(event.User)
}

func Done(client *slack.Client, manager JobManager, event *slackevents.MessageEvent, properties *Properties) string {
	msg, err := manager.TerminateJobForUser(event.User)
	if err != nil {
		return err.Error()
	}
	return msg
}

func Refresh(client *slack.Client, manager JobManager, event *slackevents.MessageEvent, properties *Properties) string {
	msg, err := manager.SyncJobForUser(event.User)
	if err != nil {
		return err.Error()
	}
	return msg
}

func Auth(client *slack.Client, manager JobManager, event *slackevents.MessageEvent, properties *Properties) string {
	job, err := manager.GetLaunchJob(event.User)
	if err != nil {
		return err.Error()
	}
	job.RequestedChannel = event.Channel
	NotifyJob(client, job)
	return " "
}

func TestUpgrade(client *slack.Client, manager JobManager, event *slackevents.MessageEvent, properties *Properties) string {
	userName := getUserName(client, event.User)
	from, err := parseImageInput(properties.StringParam("from", ""))
	if err != nil {
		return err.Error()
	}
	if len(from) == 0 {
		return "you must specify an image to upgrade from and to"
	}
	to, err := parseImageInput(properties.StringParam("to", ""))
	if err != nil {
		return err.Error()
	}
	// default to to from
	if len(to) == 0 {
		to = from
	}
	platform, architecture, params, err := parseOptions(properties.StringParam("options", ""))
	if err != nil {
		return err.Error()
	}
	if v := params["test"]; len(v) == 0 {
		params["test"] = "e2e-upgrade"
	}
	if !strings.Contains(params["test"], "-upgrade") {
		return "Only upgrade type tests may be run from this command"
	}
	msg, err := manager.LaunchJobForUser(&JobRequest{
		OriginalMessage: event.Text,
		User:            event.User,
		UserName:        userName,
		Inputs:          [][]string{from, to},
		Type:            JobTypeUpgrade,
		Channel:         event.Channel,
		Platform:        platform,
		JobParams:       params,
		Architecture:    architecture,
	})
	if err != nil {
		return err.Error()
	}
	return msg
}

func Test(client *slack.Client, manager JobManager, event *slackevents.MessageEvent, properties *Properties) string {
	userName := getUserName(client, event.User)
	from, err := parseImageInput(properties.StringParam("image_or_version_or_pr", ""))
	if err != nil {
		return err.Error()
	}
	if len(from) == 0 {
		return "you must specify what will be tested"
	}

	test := properties.StringParam("name", "")
	if len(test) == 0 {
		return fmt.Sprintf("you must specify the name of a test: %s", strings.Join(codeSlice(supportedTests), ", "))
	}
	switch {
	case contains(supportedTests, test):
	default:
		return fmt.Sprintf("warning: You are using a custom test name, may not be supported for all platforms: %s", strings.Join(codeSlice(supportedTests), ", "))
	}

	platform, architecture, params, err := parseOptions(properties.StringParam("options", ""))
	if err != nil {
		return err.Error()
	}

	params["test"] = test
	if strings.Contains(params["test"], "-upgrade") {
		return "Upgrade type tests require the 'test upgrade' command"
	}

	msg, err := manager.LaunchJobForUser(&JobRequest{
		OriginalMessage: event.Text,
		User:            event.User,
		UserName:        userName,
		Inputs:          [][]string{from},
		Type:            JobTypeTest,
		Channel:         event.Channel,
		Platform:        platform,
		JobParams:       params,
		Architecture:    architecture,
	})
	if err != nil {
		return err.Error()
	}
	return msg
}

func Build(client *slack.Client, manager JobManager, event *slackevents.MessageEvent, properties *Properties) string {
	userName := getUserName(client, event.User)
	from, err := parseImageInput(properties.StringParam("pullrequest", ""))
	if err != nil {
		return err.Error()
	}
	if len(from) == 0 {
		return "you must specify at least one pull request to build a release image"
	}

	platform, architecture, params, err := parseOptions(properties.StringParam("options", ""))
	if err != nil {
		return err.Error()
	}

	msg, err := manager.LaunchJobForUser(&JobRequest{
		OriginalMessage: event.Text,
		User:            event.User,
		UserName:        userName,
		Inputs:          [][]string{from},
		Type:            JobTypeBuild,
		Channel:         event.Channel,
		Platform:        platform,
		JobParams:       params,
		Architecture:    architecture,
	})
	if err != nil {
		return err.Error()
	}
	return msg
}

func (b *Bot) WorkflowLaunch(client *slack.Client, manager JobManager, event *slackevents.MessageEvent, properties *Properties) string {
	userName := getUserName(client, event.User)
	from, err := parseImageInput(properties.StringParam("image_or_version_or_pr", ""))
	if err != nil {
		return err.Error()
	}
	if len(from) == 0 {
		return "you must specify what will be tested"
	}

	name := properties.StringParam("name", "")
	if len(name) == 0 {
		return fmt.Sprintf("you must specify the name of a workflow: %s", strings.Join(codeSlice(supportedTests), ", "))
	}
	platform, architecture, err := getPlatformArchFromWorkflowConfig(b.workflowConfig, name)
	if err != nil {
		return err.Error()
	}

	params := properties.StringParam("parameters", "")
	jobParams, err := buildJobParams(params)
	if err != nil {
		return err.Error()
	}

	msg, err := manager.LaunchJobForUser(&JobRequest{
		OriginalMessage: event.Text,
		User:            event.User,
		UserName:        userName,
		Inputs:          [][]string{from},
		Type:            JobTypeWorkflowLaunch,
		Channel:         event.Channel,
		Platform:        platform,
		JobParams:       jobParams,
		Architecture:    architecture,
		WorkflowName:    name,
	})
	if err != nil {
		return err.Error()
	}
	return msg
}

func (b *Bot) WorkflowUpgrade(client *slack.Client, manager JobManager, event *slackevents.MessageEvent, properties *Properties) string {
	userName := getUserName(client, event.User)
	from, err := parseImageInput(properties.StringParam("from_image_or_version_or_pr", ""))
	if err != nil {
		return err.Error()
	}
	if len(from) == 0 {
		return "you must specify initial release"
	}

	to, err := parseImageInput(properties.StringParam("to_image_or_version_or_pr", ""))
	if err != nil {
		return err.Error()
	}
	if len(to) == 0 {
		return "you must specify the target release"
	}

	name := properties.StringParam("name", "")
	if len(name) == 0 {
		return fmt.Sprintf("you must specify the name of a workflow: %s", strings.Join(codeSlice(supportedTests), ", "))
	}
	platform, architecture, err := getPlatformArchFromWorkflowConfig(b.workflowConfig, name)
	if err != nil {
		return err.Error()
	}

	params := properties.StringParam("parameters", "")
	jobParams, err := buildJobParams(params)
	if err != nil {
		return err.Error()
	}

	msg, err := manager.LaunchJobForUser(&JobRequest{
		OriginalMessage: event.Text,
		User:            event.User,
		UserName:        userName,
		Inputs:          [][]string{from, to},
		Type:            JobTypeWorkflowUpgrade,
		Channel:         event.Channel,
		Platform:        platform,
		JobParams:       jobParams,
		Architecture:    architecture,
		WorkflowName:    name,
	})
	if err != nil {
		return err.Error()
	}
	return msg
}

func Version(client *slack.Client, manager JobManager, event *slackevents.MessageEvent, properties *Properties) string {
	return fmt.Sprintf("Running `%s` from https://github.com/openshift/ci-chat-bot", version.Get().String())
}

func help(client *slack.Client, event *slackevents.MessageEvent, botCommands []BotCommand) {
	helpMessage := " "
	helpMessage += "help" + " - " + fmt.Sprintf("_%s_", "help") + "\n"
	for _, command := range botCommands {
		tokens := command.Tokenize()
		for _, token := range tokens {
			if token.IsParameter() {
				helpMessage += fmt.Sprintf("`%s`", token.Word) + " "
			} else {
				helpMessage += fmt.Sprintf("`%s`", token.Word) + " "
			}
		}
		if len(command.Definition().Description) > 0 {
			helpMessage += "-" + " " + fmt.Sprintf("_%s_", command.Definition().Description)
		}
		helpMessage += "\n"
		if len(command.Definition().Example) > 0 {
			helpMessage += fmt.Sprintf(">_*Example:* %s_", command.Definition().Example) + "\n"
		}
	}
	_, _, err := client.PostMessage(event.Channel, slack.MsgOptionText(helpMessage, false))
	if err != nil {
		klog.Warningf("Failed to post the help message")
	}
}
