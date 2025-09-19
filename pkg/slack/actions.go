package slack

import (
	"fmt"
	"slices"
	"strings"
	"time"

	botversion "github.com/openshift/ci-chat-bot/pkg/version"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/parser"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"k8s.io/apimachinery/pkg/util/sets"
)

func LaunchCluster(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	userName := GetUserName(client, event.User)
	from, err := ParseImageInput(properties.StringParam("image_or_version_or_prs", ""))
	if err != nil {
		return err.Error()
	}
	var inputs [][]string
	if len(from) > 0 {
		inputs = [][]string{from}
	}

	platform, architecture, params, err := ParseOptions(properties.StringParam("options", ""), inputs, manager.JobTypeInstall)
	if err != nil {
		return err.Error()
	}
	if len(params["test"]) > 0 {
		return "TestUpgrade arguments may not be passed from the launch command"
	}

	msg, err := jobManager.LaunchJobForUser(&manager.JobRequest{
		OriginalMessage: event.Text,
		User:            event.User,
		UserName:        userName,
		Inputs:          inputs,
		Type:            manager.JobTypeInstall,
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

func ValidateCommand(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	command := strings.TrimSpace(properties.StringParam("command", ""))
	if command == "" {
		return "Error: Please specify a command to validate. Example: `validate launch 4.19 aws,compact`"
	}

	// Parse the command to determine its type
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "Error: Invalid command format"
	}

	commandType := strings.ToLower(parts[0])

	switch commandType {
	case "launch":
		if len(parts) < 2 {
			return "Error: launch command requires at least an image/version. Example: `validate launch 4.19 aws`"
		}
		return validateLaunchCommand(jobManager, command, parts[1:], event.User)
	case "test":
		if len(parts) < 3 {
			return "Error: test command requires test name and image/version. Example: `validate test e2e 4.19 aws`"
		}
		return validateTestCommand(jobManager, command, parts[1:], event.User)
	case "build":
		if len(parts) < 2 {
			return "Error: build command requires PR. Example: `validate build openshift/installer#123`"
		}
		return validateBuildCommand(jobManager, command, parts[1:], event.User)
	default:
		return fmt.Sprintf("Error: Validation is not yet supported for '%s' command. Currently supported: launch, test, build", commandType)
	}
}

func validateLaunchCommand(jobManager manager.JobManager, originalCommand string, args []string, userID string) string {
	// Parse the launch command similar to LaunchCluster
	from, err := ParseImageInput(args[0])
	if err != nil {
		return fmt.Sprintf("❌ Invalid input: %v", err)
	}

	var inputs [][]string
	if len(from) > 0 {
		inputs = [][]string{from}
	}

	options := ""
	if len(args) > 1 {
		options = strings.Join(args[1:], " ")
	}

	platform, architecture, params, err := ParseOptions(options, inputs, manager.JobTypeInstall)
	if err != nil {
		return fmt.Sprintf("❌ Invalid options: %v", err)
	}

	// Create a job request for validation
	jobRequest := &manager.JobRequest{
		OriginalMessage: originalCommand,
		User:            userID,
		UserName:        "validation-user",
		Inputs:          inputs,
		Type:            manager.JobTypeInstall,
		Platform:        platform,
		JobParams:       params,
		Architecture:    architecture,
	}

	// Validate the configuration
	err = jobManager.CheckValidJobConfiguration(jobRequest)
	if err != nil {
		return fmt.Sprintf("❌ Configuration error: %v", err)
	}

	// Success message with details
	msg := "✅ **Valid launch configuration**\n\n"
	msg += fmt.Sprintf("**Would launch:** OpenShift cluster\n")
	msg += fmt.Sprintf("**Input:** %s\n", strings.Join(from, ", "))
	msg += fmt.Sprintf("**Platform:** %s\n", platform)
	msg += fmt.Sprintf("**Architecture:** %s\n", architecture)
	if len(params) > 0 {
		var paramsList []string
		for key, value := range params {
			if value == "true" {
				paramsList = append(paramsList, key)
			} else {
				paramsList = append(paramsList, fmt.Sprintf("%s=%s", key, value))
			}
		}
		msg += fmt.Sprintf("**Parameters:** %s\n", strings.Join(paramsList, ", "))
	}
	msg += "\n*This command would create a cluster with the above configuration.*"

	return msg
}

func validateTestCommand(jobManager manager.JobManager, originalCommand string, args []string, userID string) string {
	if strings.ToLower(args[0]) == "upgrade" {
		// Handle test upgrade
		if len(args) < 3 {
			return "❌ test upgrade requires from and to versions. Example: `validate test upgrade 4.18 4.19 aws`"
		}
		return validateTestUpgradeCommand(jobManager, originalCommand, args[1:], userID)
	}

	// Regular test command
	testName := args[0]
	from, err := ParseImageInput(args[1])
	if err != nil {
		return fmt.Sprintf("❌ Invalid input: %v", err)
	}

	options := ""
	if len(args) > 2 {
		options = strings.Join(args[2:], " ")
	}

	platform, architecture, params, err := ParseOptions(options, [][]string{from}, manager.JobTypeTest)
	if err != nil {
		return fmt.Sprintf("❌ Invalid options: %v", err)
	}

	params["test"] = testName
	if strings.Contains(params["test"], "-upgrade") {
		return "❌ Upgrade type tests require the 'test upgrade' command"
	}

	jobRequest := &manager.JobRequest{
		OriginalMessage: originalCommand,
		User:            userID,
		UserName:        "validation-user",
		Inputs:          [][]string{from},
		Type:            manager.JobTypeTest,
		Platform:        platform,
		JobParams:       params,
		Architecture:    architecture,
	}

	err = jobManager.CheckValidJobConfiguration(jobRequest)
	if err != nil {
		return fmt.Sprintf("❌ Configuration error: %v", err)
	}

	msg := "✅ **Valid test configuration**\n\n"
	msg += fmt.Sprintf("**Would run:** %s test suite\n", testName)
	msg += fmt.Sprintf("**Input:** %s\n", strings.Join(from, ", "))
	msg += fmt.Sprintf("**Platform:** %s (%s)\n", platform, architecture)
	msg += "\n*This command would run the specified test suite.*"

	return msg
}

func validateTestUpgradeCommand(jobManager manager.JobManager, originalCommand string, args []string, userID string) string {
	fromInput, err := ParseImageInput(args[0])
	if err != nil {
		return fmt.Sprintf("❌ Invalid from version: %v", err)
	}

	toInput, err := ParseImageInput(args[1])
	if err != nil {
		return fmt.Sprintf("❌ Invalid to version: %v", err)
	}

	options := ""
	if len(args) > 2 {
		options = strings.Join(args[2:], " ")
	}

	platform, architecture, params, err := ParseOptions(options, [][]string{fromInput, toInput}, manager.JobTypeUpgrade)
	if err != nil {
		return fmt.Sprintf("❌ Invalid options: %v", err)
	}

	if len(params["test"]) == 0 {
		params["test"] = "e2e-upgrade"
	}
	if !strings.Contains(params["test"], "-upgrade") {
		return "❌ Only upgrade type tests may be run from this command"
	}

	jobRequest := &manager.JobRequest{
		OriginalMessage: originalCommand,
		User:            userID,
		UserName:        "validation-user",
		Inputs:          [][]string{fromInput, toInput},
		Type:            manager.JobTypeUpgrade,
		Platform:        platform,
		JobParams:       params,
		Architecture:    architecture,
	}

	err = jobManager.CheckValidJobConfiguration(jobRequest)
	if err != nil {
		return fmt.Sprintf("❌ Configuration error: %v", err)
	}

	msg := "✅ **Valid upgrade test configuration**\n\n"
	msg += fmt.Sprintf("**Would test:** Upgrade from %s to %s\n", strings.Join(fromInput, ", "), strings.Join(toInput, ", "))
	msg += fmt.Sprintf("**Test type:** %s\n", params["test"])
	msg += fmt.Sprintf("**Platform:** %s (%s)\n", platform, architecture)
	msg += "\n*This command would test the specified upgrade path.*"

	return msg
}

func validateBuildCommand(jobManager manager.JobManager, originalCommand string, args []string, userID string) string {
	from, err := ParseImageInput(args[0])
	if err != nil {
		return fmt.Sprintf("❌ Invalid PR input: %v", err)
	}

	options := ""
	if len(args) > 1 {
		options = strings.Join(args[1:], " ")
	}

	platform, architecture, params, err := ParseOptions(options, [][]string{from}, manager.JobTypeBuild)
	if err != nil {
		return fmt.Sprintf("❌ Invalid options: %v", err)
	}

	jobRequest := &manager.JobRequest{
		OriginalMessage: originalCommand,
		User:            userID,
		UserName:        "validation-user",
		Inputs:          [][]string{from},
		Type:            manager.JobTypeBuild,
		Platform:        platform,
		JobParams:       params,
		Architecture:    architecture,
	}

	err = jobManager.CheckValidJobConfiguration(jobRequest)
	if err != nil {
		return fmt.Sprintf("❌ Configuration error: %v", err)
	}

	msg := "✅ **Valid build configuration**\n\n"
	msg += fmt.Sprintf("**Would build:** Release image from %s\n", strings.Join(from, ", "))
	msg += fmt.Sprintf("**Platform:** %s (%s)\n", platform, architecture)
	msg += "\n*This command would create a custom release image from the specified PR(s).*"

	return msg
}

func Lookup(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	from, err := ParseImageInput(properties.StringParam("image_or_version_or_prs", ""))
	if err != nil {
		return err.Error()
	}
	architectureRaw, err := ParseImageInput(properties.StringParam("architecture", ""))
	if err != nil {
		return err.Error()
	} else if len(architectureRaw) > 1 {
		return "Error: cannot specify more than one architecture for this command"
	}
	architecture := "amd64" // default arch
	if len(architectureRaw) == 1 {
		architecture = architectureRaw[0]
	}
	if !sets.NewString(manager.SupportedArchitectures...).Has(architecture) {
		return fmt.Sprintf("Error: %s is an invalid architecture. Supported architectures: %v", architecture, manager.SupportedArchitectures)
	}
	msg, err := jobManager.LookupInputs(from, architecture)
	if err != nil {
		return err.Error()
	}
	return msg
}

func List(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	return jobManager.ListJobs(event.User, manager.ListFilters{})
}

func Done(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	msg, err := jobManager.TerminateJobForUser(event.User)
	if err != nil {
		return err.Error()
	}
	return msg
}

func Refresh(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	msg, err := jobManager.SyncJobForUser(event.User)
	if err != nil {
		return err.Error()
	}
	return msg
}

func MceAuth(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	nameInput, err := ParseImageInput(properties.StringParam("name", ""))
	if err != nil {
		return err.Error()
	}
	var name string
	if len(nameInput) == 1 {
		name = nameInput[0]
	} else if len(nameInput) > 1 {
		return "mce auth take only 0 or 1 argument (cluster name)"
	}
	managed, deployments, provisions, kubeconfigs, passwords := jobManager.GetManagedClustersForUser(event.User)
	if name == "" {
		if len(managed) == 0 {
			return "You have no running MCE clusters."
		} else if len(managed) == 1 {
			// we need to get the key of the 1 cluster the user has
			for clusterName := range managed {
				name = clusterName
			}
		} else {
			return "You user has multiple running clusters. Please specify the name of the cluster your are requested credentials for."
		}
	} else if _, ok := managed[name]; !ok {
		return fmt.Sprintf("No cluster called `%s` for your user found", name)
	}
	NotifyMce(client, managed[name], deployments[name], provisions[name], kubeconfigs[name], passwords[name], nil)
	return ""
}

func Auth(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	cluster, password := jobManager.GetROSACluster(event.User)
	if cluster != nil {
		NotifyRosa(client, cluster, password)
		return ""
	}
	job, err := jobManager.GetLaunchJob(event.User)
	if err != nil {
		return err.Error()
	}
	job.RequestedChannel = event.Channel
	NotifyJob(client, job)
	return " "
}

func TestUpgrade(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	userName := GetUserName(client, event.User)
	from, err := ParseImageInput(properties.StringParam("from", ""))
	if err != nil {
		return err.Error()
	}
	if len(from) == 0 {
		return "you must specify an image to upgrade from and to"
	}
	to, err := ParseImageInput(properties.StringParam("to", ""))
	if err != nil {
		return err.Error()
	}
	// default to to from
	if len(to) == 0 {
		to = from
	}
	platform, architecture, params, err := ParseOptions(properties.StringParam("options", ""), [][]string{from, to}, manager.JobTypeUpgrade)
	if err != nil {
		return err.Error()
	}
	if v := params["test"]; len(v) == 0 {
		params["test"] = "e2e-upgrade"
	}
	if !strings.Contains(params["test"], "-upgrade") {
		return "Only upgrade type tests may be run from this command"
	}
	msg, err := jobManager.LaunchJobForUser(&manager.JobRequest{
		OriginalMessage: event.Text,
		User:            event.User,
		UserName:        userName,
		Inputs:          [][]string{from, to},
		Type:            manager.JobTypeUpgrade,
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

func Test(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	userName := GetUserName(client, event.User)
	from, err := ParseImageInput(properties.StringParam("image_or_version_or_prs", ""))
	if err != nil {
		return err.Error()
	}
	if len(from) == 0 {
		return "you must specify what will be tested"
	}

	test := properties.StringParam("name", "")
	if len(test) == 0 {
		return fmt.Sprintf("you must specify the name of a test: %s", strings.Join(CodeSlice(manager.SupportedTests), ", "))
	}
	switch {
	case slices.Contains(manager.SupportedTests, test):
	default:
		return fmt.Sprintf("warning: You are using a custom test name, may not be supported for all platforms: %s", strings.Join(CodeSlice(manager.SupportedTests), ", "))
	}

	platform, architecture, params, err := ParseOptions(properties.StringParam("options", ""), [][]string{from}, manager.JobTypeTest)
	if err != nil {
		return err.Error()
	}

	params["test"] = test
	if strings.Contains(params["test"], "-upgrade") {
		return "Upgrade type tests require the 'test upgrade' command"
	}

	msg, err := jobManager.LaunchJobForUser(&manager.JobRequest{
		OriginalMessage: event.Text,
		User:            event.User,
		UserName:        userName,
		Inputs:          [][]string{from},
		Type:            manager.JobTypeTest,
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

func CatalogBuild(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	userName := GetUserName(client, event.User)
	from, err := ParseImageInput(properties.StringParam("pullrequest", ""))
	if err != nil {
		return err.Error()
	}
	if len(from) == 0 {
		return "you must specify at least one pull request to build an operator catalog"
	}

	bundleName, err := ParseImageInput(properties.StringParam("bundle_name", ""))
	if err != nil {
		return err.Error()
	}
	if len(bundleName) == 0 {
		return "you must specify the bundle name for the operator bundle you wish to build"
	}

	// this allows us to default platform and arch in the same location as other commands
	platform, architecture, _, err := ParseOptions(properties.StringParam("options", ""), [][]string{from}, manager.JobTypeCatalog)
	if err != nil {
		return err.Error()
	}

	msg, err := jobManager.LaunchJobForUser(&manager.JobRequest{
		OriginalMessage: event.Text,
		User:            event.User,
		UserName:        userName,
		Inputs:          [][]string{from},
		Type:            manager.JobTypeCatalog,
		Channel:         event.Channel,
		JobParams:       map[string]string{"bundle": bundleName[0]},
		Architecture:    architecture,
		Platform:        platform,
	})
	if err != nil {
		return err.Error()
	}
	return msg
}

func Build(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	userName := GetUserName(client, event.User)
	from, err := ParseImageInput(properties.StringParam("pullrequest", ""))
	if err != nil {
		return err.Error()
	}
	if len(from) == 0 {
		return "you must specify at least one pull request to build a release image"
	}

	platform, architecture, params, err := ParseOptions(properties.StringParam("options", ""), [][]string{from}, manager.JobTypeBuild)
	if err != nil {
		return err.Error()
	}

	msg, err := jobManager.LaunchJobForUser(&manager.JobRequest{
		OriginalMessage: event.Text,
		User:            event.User,
		UserName:        userName,
		Inputs:          [][]string{from},
		Type:            manager.JobTypeBuild,
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

func Version(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	return fmt.Sprintf("Running `%s` from https://github.com/openshift/ci-chat-bot", botversion.Get().String())
}

func WorkflowLaunch(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	workflowConfig := jobManager.GetWorkflowConfig()
	userName := GetUserName(client, event.User)
	from, err := ParseImageInput(properties.StringParam("image_or_version_or_prs", ""))
	if err != nil {
		return err.Error()
	}
	if len(from) == 0 {
		return "you must specify what will be tested"
	}

	name := properties.StringParam("name", "")
	if len(name) == 0 {
		return fmt.Sprintf("you must specify the name of a workflow: %s", strings.Join(CodeSlice(manager.SupportedTests), ", "))
	}
	platform, architecture, err := GetPlatformArchFromWorkflowConfig(workflowConfig, name)
	if err != nil {
		return err.Error()
	}

	params := properties.StringParam("parameters", "")
	jobParams, err := BuildJobParams(params)
	if err != nil {
		return err.Error()
	}

	msg, err := jobManager.LaunchJobForUser(&manager.JobRequest{
		OriginalMessage: event.Text,
		User:            event.User,
		UserName:        userName,
		Inputs:          [][]string{from},
		Type:            manager.JobTypeWorkflowLaunch,
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

func WorkflowTest(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	workflowConfig := jobManager.GetWorkflowConfig()
	userName := GetUserName(client, event.User)
	from, err := ParseImageInput(properties.StringParam("image_or_version_or_prs", ""))
	if err != nil {
		return err.Error()
	}
	if len(from) == 0 {
		return "you must specify what will be tested"
	}

	name := properties.StringParam("name", "")
	if len(name) == 0 {
		return fmt.Sprintf("you must specify the name of a workflow: %s", strings.Join(CodeSlice(manager.SupportedTests), ", "))
	}
	platform, architecture, err := GetPlatformArchFromWorkflowConfig(workflowConfig, name)
	if err != nil {
		return err.Error()
	}

	params := properties.StringParam("parameters", "")
	jobParams, err := BuildJobParams(params)
	if err != nil {
		return err.Error()
	}

	msg, err := jobManager.LaunchJobForUser(&manager.JobRequest{
		OriginalMessage: event.Text,
		User:            event.User,
		UserName:        userName,
		Inputs:          [][]string{from},
		Type:            manager.JobTypeWorkflowTest,
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

func WorkflowUpgrade(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	workflowConfig := jobManager.GetWorkflowConfig()
	userName := GetUserName(client, event.User)
	from, err := ParseImageInput(properties.StringParam("from_image_or_version_or_prs", ""))
	if err != nil {
		return err.Error()
	}
	if len(from) == 0 {
		return "you must specify initial release"
	}

	to, err := ParseImageInput(properties.StringParam("to_image_or_version_or_prs", ""))
	if err != nil {
		return err.Error()
	}
	if len(to) == 0 {
		return "you must specify the target release"
	}

	name := properties.StringParam("name", "")
	if len(name) == 0 {
		return fmt.Sprintf("you must specify the name of a workflow: %s", strings.Join(CodeSlice(manager.SupportedTests), ", "))
	}
	platform, architecture, err := GetPlatformArchFromWorkflowConfig(workflowConfig, name)
	if err != nil {
		return err.Error()
	}

	params := properties.StringParam("parameters", "")
	jobParams, err := BuildJobParams(params)
	if err != nil {
		return err.Error()
	}

	msg, err := jobManager.LaunchJobForUser(&manager.JobRequest{
		OriginalMessage: event.Text,
		User:            event.User,
		UserName:        userName,
		Inputs:          [][]string{from, to},
		Type:            manager.JobTypeWorkflowUpgrade,
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

func RosaCreate(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	from, err := ParseImageInput(properties.StringParam("version", ""))
	if err != nil {
		return err.Error()
	}
	providedVersion := ""
	if len(from) > 1 {
		return "rosa create only takes one version"
	}
	if len(from) == 1 {
		providedVersion = from[0]
	}
	rawDuration, err := ParseImageInput(properties.StringParam("duration", ""))
	if err != nil {
		return err.Error()
	}
	var duration time.Duration
	if len(rawDuration) != 0 {
		duration, err = time.ParseDuration(rawDuration[0])
		if err != nil {
			return fmt.Sprintf("Failed to parse provided duration: %v", err)
		}
	}

	msg, err := jobManager.CreateRosaCluster(event.User, event.Channel, providedVersion, duration)
	if err != nil {
		return err.Error()
	}
	return msg
}

func RosaLookup(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	from, err := ParseImageInput(properties.StringParam("version", ""))
	if err != nil {
		return err.Error()
	}

	providedVersion := ""
	if len(from) > 1 {
		return "rosa create only takes one version"
	}
	if len(from) == 1 {
		providedVersion = from[0]
	}
	msg, err := jobManager.LookupRosaInputs(providedVersion)
	if err != nil {
		return err.Error()
	}
	return msg
}

func RosaDescribe(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	from, err := ParseImageInput(properties.StringParam("cluster", ""))
	if err != nil {
		return err.Error()
	}
	cluster := ""
	if len(from) > 1 {
		return "rosa describe only takes one cluster"
	}
	if len(from) == 1 {
		cluster = from[0]
	}
	msg, err := jobManager.DescribeROSACluster(cluster)
	if err != nil {
		return err.Error()
	}
	return msg
}

func MceCreate(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	from, err := ParseImageInput(properties.StringParam("image_or_version_or_prs", ""))
	if err != nil {
		return err.Error()
	}
	var inputs [][]string
	if len(from) > 0 {
		inputs = [][]string{from}
	}

	platformInput, err := ParseImageInput(properties.StringParam("platform", ""))
	if err != nil {
		return err.Error()
	}
	platform := ""
	if len(platform) > 1 {
		return "platform only takes 1 input"
	}

	if len(platformInput) == 1 {
		platform = platformInput[0]
	}
	rawDuration, err := ParseImageInput(properties.StringParam("duration", ""))
	if err != nil {
		return err.Error()
	}
	var duration time.Duration
	if len(rawDuration) != 0 {
		duration, err = time.ParseDuration(rawDuration[0])
		if err != nil {
			return fmt.Sprintf("Failed to parse provided duration: %v", err)
		}
	}

	msg, err := jobManager.CreateMceCluster(event.User, event.Channel, platform, inputs, duration)
	if err != nil {
		return err.Error()
	}
	return msg
}

func MceDelete(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	nameInput, err := ParseImageInput(properties.StringParam("cluster_name", ""))
	if err != nil {
		return err.Error()
	}
	var name string
	if len(nameInput) == 1 {
		name = nameInput[0]
	} else if len(nameInput) > 1 {
		return "mce delete take only 0 or 1 argument (cluster name)"
	}
	if name == "" {
		managed, _, _, _, _ := jobManager.GetManagedClustersForUser(event.User)
		if len(managed) == 1 {
			// we need to get the key of the 1 cluster the user has
			for clusterName := range managed {
				name = clusterName
			}
		} else {
			return "You user has multiple running clusters. Please specify the name of the cluster your are requesting to delete."
		}
	}
	// DeleteMceCluster function checks that the user who is requesting deletion matches user who requested its creation, so a check here isn't necessary
	msg, err := jobManager.DeleteMceCluster(event.User, name)
	if err != nil {
		return err.Error()
	}
	return msg
}

func MceImageSets(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	return jobManager.ListMceVersions()
}

func MceList(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	all, err := ParseImageInput(properties.StringParam("all", ""))
	if err != nil {
		return err.Error()
	}
	if len(all) > 0 && all[0] == "all" {
		return jobManager.ListManagedClusters("")
	}
	return jobManager.ListManagedClusters(event.User)
}
