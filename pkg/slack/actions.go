package slack

import (
	"fmt"
	"slices"
	"strings"
	"time"

	botversion "github.com/openshift/ci-chat-bot/pkg/version"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/parser"
	"github.com/slack-go/slack/slackevents"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog"
)

func LaunchCluster(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
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

func Lookup(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
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

func List(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	list, _, _ := jobManager.ListJobs(event.User, manager.ListFilters{})
	return list
}

func Done(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	msg, err := jobManager.TerminateJobForUser(event.User)
	if err != nil {
		return err.Error()
	}
	return msg
}

func Refresh(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	msg, err := jobManager.SyncJobForUser(event.User)
	if err != nil {
		return err.Error()
	}
	return msg
}

func MceAuth(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
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
	NotifyMce(client, managed[name], deployments[name], provisions[name], kubeconfigs[name], passwords[name], true, nil)
	return ""
}

func Auth(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
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
	NotifyJob(client, job, true)
	return " "
}

func TestUpgrade(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
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

func Test(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
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

func CatalogBuild(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
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

func Build(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
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

func Version(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	return fmt.Sprintf("Running `%s` from https://github.com/openshift/ci-chat-bot", botversion.Get().String())
}

func Request(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	// Extract command parameters
	resource := properties.StringParam("resource", "")
	justification := properties.StringParam("justification", "")

	// Validate parameters
	if resource == "" || justification == "" {
		return "Invalid command format. Usage: request <resource> \"<business justification>\"\nExample: request gcp-access \"Need to debug CI infrastructure issues\""
	}

	klog.Infof("Resource: \"%s\"", resource)

	// For now, only allow "gcp-access" resource
	if resource != "gcp-access" {
		return "Currently, access is only available for the 'gcp-access' resource."
	}

	// Get user's email
	user, err := client.GetUserInfo(event.User)
	if err != nil {
		klog.Errorf("Failed to get user info for %s: %v", event.User, err)
		return "Failed to retrieve your user information. Please try again or contact an administrator."
	}

	email := user.Profile.Email
	if email == "" {
		return "Could not determine your email address. Please ensure your Slack profile has an email configured."
	}

	// Validate using organizational data - check if user is in Hybrid Platforms
	orgDataService := jobManager.GetOrgDataService()
	if orgDataService == nil {
		return "Organizational data service is not available. Please contact an administrator."
	}

	// Verify user is a member of Hybrid Platforms (required for all access)
	if !isUserInOrg(orgDataService, event.User, email, "Hybrid Platforms") {
		return "You are not a member of the 'Hybrid Platforms' organization. Access can only be granted to Hybrid Platforms members."
	}

	// Grant access with business justification (creates service account and returns key)
	msg, keyJSON, err := jobManager.GrantGCPAccess(email, event.User, justification, resource)
	if err != nil {
		klog.Errorf("Failed to grant GCP access for %s: %v", email, err)
		return fmt.Sprintf("Failed to grant access: %v", err)
	}

	// Upload service account key file to Slack
	if err := SendGCPServiceAccountKey(client, event.Channel, string(keyJSON), email); err != nil {
		klog.Errorf("Failed to upload service account key for %s: %v", email, err)
		// Don't rollback - user can re-run command to get key
		return msg + "\n\n⚠️  Failed to upload key file. Please run the command again to retrieve your credentials."
	}

	return msg
}

func Revoke(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	// Extract command parameters
	resource := properties.StringParam("resource", "")

	// Validate parameters
	if resource == "" {
		return "Invalid command format. Usage: revoke <resource>\nExample: revoke gcp-access"
	}

	// For now, only allow "gcp-access" resource
	if resource != "gcp-access" {
		return "Currently, access is only available for the 'gcp-access' resource."
	}

	// Get user's email
	user, err := client.GetUserInfo(event.User)
	if err != nil {
		klog.Errorf("Failed to get user info for %s: %v", event.User, err)
		return "Failed to retrieve your user information. Please try again or contact an administrator."
	}

	email := user.Profile.Email
	if email == "" {
		return "Could not determine your email address. Please ensure your Slack profile has an email configured."
	}

	// Validate using organizational data - check if user is in the openshift org
	orgDataService := jobManager.GetOrgDataService()
	if orgDataService == nil {
		return "Organizational data service is not available. Please contact an administrator."
	}

	if !isUserInOrg(orgDataService, event.User, email, "Hybrid Platforms") {
		return "GCP workspace access is only available to members of the Hybrid Platforms organization."
	}

	// Revoke access
	msg, err := jobManager.RevokeGCPAccess(email, event.User)
	if err != nil {
		klog.Errorf("Failed to revoke GCP access for %s: %v", email, err)
		return fmt.Sprintf("Failed to revoke access: %v", err)
	}

	return msg
}

func WorkflowLaunch(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
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

func WorkflowTest(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
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

func WorkflowUpgrade(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
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

func RosaCreate(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
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

func RosaLookup(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
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

func RosaDescribe(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
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

func MceCreate(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
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

func MceDelete(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
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

func MceImageSets(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	return jobManager.ListMceVersions()
}

func MceList(client parser.SlackClient, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	all, err := ParseImageInput(properties.StringParam("all", ""))
	if err != nil {
		return err.Error()
	}
	if len(all) > 0 && all[0] == "all" {
		list, _, _ := jobManager.ListManagedClusters("")
		return list
	}
	list, _, _ := jobManager.ListManagedClusters(event.User)
	return list
}

// isUserInOrg checks if a user is in the specified organization.
// It first tries to look up by Slack ID, and if that fails (e.g., in staging environments),
// it falls back to looking up by email address and checking the employee's UID.
func isUserInOrg(orgDataService manager.OrgDataService, slackID, email, org string) bool {
	// First try Slack ID lookup (works in production)
	if orgDataService.IsSlackUserInOrg(slackID, org) {
		klog.V(2).Infof("User %s validated by Slack ID for org %s", slackID, org)
		return true
	}

	// Fallback to email lookup (useful for staging/testing environments where Slack IDs differ)
	klog.V(2).Infof("Slack ID %s not found in org data, trying email lookup for %s", slackID, email)
	employee := orgDataService.GetEmployeeByEmail(email)
	if employee == nil {
		klog.V(2).Infof("User with email %s not found in organizational data", email)
		return false
	}

	// Check if the employee (by UID) is in the specified organization
	if orgDataService.IsEmployeeInOrg(employee.UID, org) {
		klog.V(2).Infof("User %s validated by email (%s -> UID %s) for org %s", slackID, email, employee.UID, org)
		return true
	}

	klog.V(2).Infof("User %s (email: %s, UID: %s) is not a member of org %s", slackID, email, employee.UID, org)
	return false
}
