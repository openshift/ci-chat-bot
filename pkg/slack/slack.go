package slack

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	clustermgmtv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/parser"
	"github.com/openshift/ci-chat-bot/pkg/utils"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/slack-go/slack"
	"k8s.io/klog"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	prowapiv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
)

type Bot struct {
	BotToken         string
	BotSigningSecret string
	GracePeriod      time.Duration
	Port             int
	userID           string
}

func (b *Bot) JobResponder(s *slack.Client) func(manager.Job) {
	return func(job manager.Job) {
		if len(job.RequestedChannel) == 0 || len(job.RequestedBy) == 0 {
			klog.Infof("job %q has no requested channel or user, can't notify", job.Name)
			return
		}
		switch job.Mode {
		case manager.JobTypeLaunch, manager.JobTypeWorkflowLaunch:
			if len(job.Credentials) == 0 && len(job.Failure) == 0 {
				klog.Infof("no credentials or failure, still pending")
				return
			}
		default:
			if len(job.URL) == 0 && len(job.Failure) == 0 {
				klog.Infof("no URL or failure, still pending")
				return
			}
		}
		NotifyJob(s, &job, true)
	}
}

func (b *Bot) RosaResponder(s *slack.Client) func(*clustermgmtv1.Cluster, string) {
	return func(cluster *clustermgmtv1.Cluster, password string) {
		tags := cluster.AWS().Tags()
		if len(tags) == 0 {
			klog.Errorf("Cluster has no tags, cannot notify")
			return
		}
		if len(tags[utils.UserTag]) == 0 || len(tags[utils.ChannelTag]) == 0 {
			klog.Infof("rosa cluster %s has no requested channel or user, can't notify", cluster.ID())
			return
		}
		if password == "" && cluster.State() != clustermgmtv1.ClusterStateError {
			klog.Infof("no credentials or failure, still pending")
			return
		}
		NotifyRosa(s, cluster, password)
	}
}

func (b *Bot) MceResponder(s *slack.Client) func(*clusterv1.ManagedCluster, *hivev1.ClusterDeployment, *hivev1.ClusterProvision, string, string, error) {
	return func(cluster *clusterv1.ManagedCluster, clusterDeployment *hivev1.ClusterDeployment, clusterProvision *hivev1.ClusterProvision, kubeconfig, password string, errMsg error) {
		if len(cluster.Annotations[utils.UserTag]) == 0 || len(cluster.Annotations[utils.ChannelTag]) == 0 {
			klog.Infof("mce cluster %s has no requested channel or user, can't notify", cluster.Name)
			return
		}
		var failedProvisionCondition bool
		if clusterDeployment != nil {
			for _, provisionCondition := range clusterDeployment.Status.Conditions {
				if provisionCondition.Type == hivev1.ProvisionFailedCondition {
					if provisionCondition.Status == "True" {
						failedProvisionCondition = true
					}
					break
				}
			}
		}
		if !failedProvisionCondition && errMsg == nil {
			if clusterProvision != nil {
				if clusterProvision.Spec.Stage != hivev1.ClusterProvisionStageComplete && clusterProvision.Spec.Stage != hivev1.ClusterProvisionStageFailed {
					klog.Infof("no credentials or failure, still pending")
					return
				}
			} else {
				klog.Infof("no credentials or failure, still pending")
				return
			}
		}
		NotifyMce(s, cluster, clusterDeployment, clusterProvision, kubeconfig, password, true, errMsg)
	}
}

func NewBot(botToken, botSigningSecret string, graceperiod time.Duration, port int, workflowConfig *manager.WorkflowConfig) *Bot {
	return &Bot{
		BotToken:         botToken,
		BotSigningSecret: botSigningSecret,
		GracePeriod:      graceperiod,
		Port:             port,
		userID:           "unknown",
	}
}

func (b *Bot) SupportedCommands() []parser.BotCommand {
	return []parser.BotCommand{
		parser.NewBotCommand("launch <image_or_version_or_prs> <options>", &parser.CommandDefinition{
			Description: fmt.Sprintf("Launch an OpenShift cluster using a known image, version, or PR(s). The <image_or_version_or_prs> must contain an Openshift version. You may omit the <options> argument. Arguments can be specified as any number of comma-delimited values. Use `nightly` for the latest OCP build, `ci` for the the latest CI build, provide a version directly from any listed on https://amd64.ocp.releases.ci.openshift.org, a stream name (4.19.0-0.ci, 4.19.0-0.nightly, etc), a major/minor `X.Y` to load the \"next stable\" version, from nightly, for that version (`4.19`), `<org>/<repo>#<pr>` to launch from any combination of PRs, or an image for the first argument. Options is a comma-delimited list of variations including platform (%s), architecture (%s), and variant (%s).",
				strings.Join(CodeSlice(manager.SupportedPlatforms), ", "),
				strings.Join(CodeSlice(manager.SupportedArchitectures), ", "),
				strings.Join(CodeSlice(manager.SupportedParameters), ", ")),
			Example: "launch 4.19,openshift/installer#7160,openshift/machine-config-operator#3688 gcp,techpreview",
			Handler: LaunchCluster,
		}, false),
		parser.NewBotCommand("rosa create <version> <duration>", &parser.CommandDefinition{
			Description: "Launch an cluster in ROSA. Only GA Openshift versions are supported at the moment.",
			Example:     "rosa create 4.19 3h",
			Handler:     RosaCreate,
		}, false),
		parser.NewBotCommand("rosa lookup <version>", &parser.CommandDefinition{
			Description: "Find openshift version(s) with provided prefix that is supported in ROSA.",
			Example:     "rosa lookup 4.19",
			Handler:     RosaLookup,
		}, false),
		parser.NewBotCommand("rosa describe <cluster>", &parser.CommandDefinition{
			Description: "Display the details of the specified ROSA cluster.",
			Example:     "rosa describe s9h9g-9b6nj-x94",
			Handler:     RosaDescribe,
		}, false),
		parser.NewBotCommand("list", &parser.CommandDefinition{
			Description: "See who is hogging all the clusters.",
			Handler:     List,
		}, false),
		parser.NewBotCommand("done", &parser.CommandDefinition{
			Description: "Terminate the running cluster",
			Handler:     Done,
		}, false),
		parser.NewBotCommand("refresh", &parser.CommandDefinition{
			Description: "If the cluster is currently marked as failed, retry fetching its credentials in case of an error.",
			Handler:     Refresh,
		}, false),
		parser.NewBotCommand("auth", &parser.CommandDefinition{
			Description: "Send the credentials for the cluster you most recently requested",
			Handler:     Auth,
		}, false),
		parser.NewBotCommand("test upgrade <from> <to> <options>", &parser.CommandDefinition{
			Description: fmt.Sprintf("Run the upgrade tests between two release images. The arguments may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org. You may change the upgrade test by passing `test=NAME` in options with one of %s", strings.Join(CodeSlice(manager.SupportedUpgradeTests), ", ")),
			Example:     "test upgrade 4.17 4.19 aws",
			Handler:     TestUpgrade,
		}, false),
		parser.NewBotCommand("test <name> <image_or_version_or_prs> <options>", &parser.CommandDefinition{
			Description: fmt.Sprintf("Run the requested test suite from an image or release or built PRs. Supported test suites are %s. The from argument may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org. ", strings.Join(CodeSlice(manager.SupportedTests), ", ")),
			Example:     "test e2e 4.19 vsphere",
			Handler:     Test,
		}, false),
		parser.NewBotCommand("build <pullrequest>", &parser.CommandDefinition{
			Description: "Create a new release image from one or more pull requests. The successful build location will be sent to you when it completes and then preserved for 12 hours.  To obtain a pull secret use `oc registry login --to /path/to/pull-secret` after using `oc login` to login to the relevant CI cluster.",
			Example:     "build openshift/operator-framework-olm#68,operator-framework/operator-marketplace#396",
			Handler:     Build,
		}, false),
		parser.NewBotCommand("workflow-launch <name> <image_or_version_or_prs> <parameters>", &parser.CommandDefinition{
			Description: "Launch a cluster using the requested workflow from an image or release or built PRs. The from argument may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org.",
			Example:     "workflow-launch openshift-e2e-gcp-windows-node 4.19 gcp",
			Handler:     WorkflowLaunch,
		}, false),
		parser.NewBotCommand("workflow-test <name> <image_or_version_or_prs> <parameters>", &parser.CommandDefinition{
			Description: "Start the test using the requested workflow from an image or release or built PRs. The from argument may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org.",
			Example:     "workflow-test openshift-e2e-gcp 4.19",
			Handler:     WorkflowTest,
		}, false),
		parser.NewBotCommand("workflow-upgrade <name> <from_image_or_version_or_prs> <to_image_or_version_or_prs> <parameters>", &parser.CommandDefinition{
			Description: "Run a custom upgrade using the requested workflow from an image or release or built PRs to a specified version/image/pr from https://amd64.ocp.releases.ci.openshift.org. ",
			Example:     "workflow-upgrade openshift-upgrade-azure-ovn 4.17 4.19 azure",
			Handler:     WorkflowUpgrade,
		}, false),
		parser.NewBotCommand("version", &parser.CommandDefinition{
			Description: "Report the version of the bot",
			Handler:     Version,
		}, false),
		parser.NewBotCommand("lookup <image_or_version_or_prs> <architecture>", &parser.CommandDefinition{
			Description: "Get info about a version.",
			Example:     "lookup 4.19 arm64",
			Handler:     Lookup,
		}, false),
		parser.NewBotCommand("catalog build <pullrequest> <bundle_name>", &parser.CommandDefinition{
			Description: "Create an operator, bundle, and catalog from a pull request. The successful build location will be sent to you when it completes and then preserved for 12 hours.  To obtain a pull secret use `oc registry login --to /path/to/pull-secret` after using `oc login` to login to the relevant CI cluster.",
			Example:     "catalog build openshift/aws-efs-csi-driver-operator#75 aws-efs-csi-driver-operator-bundle",
			Handler:     CatalogBuild,
		}, false),
		parser.NewBotCommand("mce create <image_or_version_or_prs> <duration> <platform>", &parser.CommandDefinition{
			Description: "Create a new cluster using Hive and MCE.",
			Example:     "mce create 4.16.7 6h aws",
			Handler:     MceCreate,
		}, false),
		parser.NewBotCommand("mce auth <name>", &parser.CommandDefinition{
			Description: "Print kubeconfig and kubeadmin password for specified MCE cluster.",
			Example:     "mce auth mycluster",
			Handler:     MceAuth,
		}, false),
		parser.NewBotCommand("mce delete <cluster_name>", &parser.CommandDefinition{
			Description: "Delete a previously created MCE cluster.",
			Example:     "mce delete mycluster",
			Handler:     MceDelete,
		}, false),
		parser.NewBotCommand("mce list <all>", &parser.CommandDefinition{
			Description: "List active MCE clusters. Append `all` to list clusters for all users.",
			Handler:     MceList,
			Example:     "mce list all",
		}, false),
		parser.NewBotCommand("mce lookup", &parser.CommandDefinition{
			Description: "List available versions for MCE clusters.",
			Handler:     MceImageSets,
		}, false),
		parser.NewBotCommand("request <resource?> <justification?>", &parser.CommandDefinition{
			Description: "Request access to workspace. Access is granted for 7 days. Must be member of Hybrid Platforms organization.",
			Example:     "request gcp-access \"Need to debug CI infrastructure issues\"",
			Handler:     Request,
		}, false),
		parser.NewBotCommand("revoke <resource?>", &parser.CommandDefinition{
			Description: "Revoke your workspace access before expiration.",
			Example:     "revoke gcp-access",
			Handler:     Revoke,
		}, false),
	}
}

func GetUserName(client parser.SlackClient, userID string) string {
	user, err := client.GetUserInfo(userID)
	if err != nil {
		klog.Warningf("Failed to get the User Info for UserID: %s, %v", userID, err)
		return ""
	}
	if before, ok := strings.CutSuffix(user.Profile.Email, "@redhat.com"); ok {
		return before
	}
	klog.Warningf("Failed to get the User details for UserID: %s", userID)
	return ""
}

func VerifiedBody(request *http.Request, signingSecret string) ([]byte, bool) {
	verifier, err := slack.NewSecretsVerifier(request.Header, signingSecret)
	if err != nil {
		klog.Errorf("Failed to create a secrets verifier. %v", err)
		return nil, false
	}

	body, err := io.ReadAll(request.Body)
	if err != nil {
		klog.Errorf("Failed to read an event payload. %v", err)
		return nil, false
	}

	// need to use body again when unmarshalling
	request.Body = io.NopCloser(bytes.NewBuffer(body))

	if _, err := verifier.Write(body); err != nil {
		klog.Errorf("Failed to hash an event payload. %v", err)
		return nil, false
	}

	if err = verifier.Ensure(); err != nil {
		klog.Errorf("Failed to verify an event payload. %v", err)
		return nil, false
	}

	return body, true
}

func GetPlatformArchFromWorkflowConfig(workflowConfig *manager.WorkflowConfig, name string) (string, string, error) {
	platform := ""
	architecture := "amd64"
	workflowConfig.Mutex.RLock()
	defer workflowConfig.Mutex.RUnlock()
	if workflow, ok := workflowConfig.Workflows[name]; !ok {
		workflows := make([]string, 0, len(workflowConfig.Workflows))
		for w := range workflowConfig.Workflows {
			workflows = append(workflows, w)
		}
		sort.Strings(workflows)
		return "", "", fmt.Errorf("workflow %s not in workflow list ( https://github.com/openshift/release/blob/master/core-services/ci-chat-bot/workflows-config.yaml ). Please add %s to the workflows list before retrying this command, or use a workflow from: %s", name, name, strings.Join(workflows, ", "))
	} else {
		platform = workflow.Platform
		if workflow.Architecture != "" {
			if slices.Contains(manager.SupportedArchitectures, workflow.Architecture) {
				architecture = workflow.Architecture
			} else {
				return "", "", fmt.Errorf("architecture %s not supported by cluster-bot", workflow.Architecture)
			}
		}
	}
	if platform == "hypershift-hosted" {
		architecture = "multi"
	}
	return platform, architecture, nil
}

func BuildJobParams(params string) (map[string]string, error) {
	var splitParams []string
	if len(params) > 0 {
		params = strings.ReplaceAll(strings.ReplaceAll(params, "“", "\""), "”", "\"")
		if !strings.Contains(params, "\"") {
			return nil, fmt.Errorf("unable to parse `%s` for parameters. Please ensure that you're using double quotes to enclose variables", params)
		}
		splitParams = strings.Split(params, "\",\"")
		// first item will have a double quote at the beginning
		splitParams[0] = strings.TrimPrefix(splitParams[0], "\"")
		// last item will have a double quote at the end
		splitParams[len(splitParams)-1] = strings.TrimSuffix(splitParams[len(splitParams)-1], "\"")
	}
	jobParams := make(map[string]string)
	for _, combinedParam := range splitParams {
		split := strings.Split(combinedParam, "=")
		if len(split) > 2 {
			// We detected nested parameters so process them.
			multiParams := strings.Join(split[1:], "=")
			multiSplit := strings.Split(multiParams, ";")
			var value strings.Builder
			value.WriteString(multiSplit[0])
			for _, param := range multiSplit[1:] {
				variable := strings.Split(param, "=")
				if len(variable) != 2 {
					return nil, fmt.Errorf("unable to interpret parameter in `%s`. Each nested parameter must be in the form of KEY=VALUE", param)
				}
				value.WriteString(fmt.Sprintf("\n%s=%s", variable[0], variable[1]))
			}
			jobParams[split[0]] = value.String()
		} else if len(split) == 2 {
			jobParams[split[0]] = parseParameterValue(split[1])
		} else {
			return nil, fmt.Errorf("unable to interpret `%s` as a parameter. Please ensure that all parameters are in the form of KEY=VALUE", combinedParam)
		}
	}
	return jobParams, nil
}

const (
	markdownLink = `^<(.*)\|(.*)>$`
)

func parseParameterValue(value string) string {
	re, _ := regexp.Compile(markdownLink)
	matches := re.FindStringSubmatch(value)
	if len(matches) == 3 {
		return matches[2]
	}
	return value
}

func NotifyJob(client parser.SlackClient, job *manager.Job, postMessage bool) (string, string) {
	var msg, kubeconfig string
	switch job.Mode {
	case manager.JobTypeLaunch, manager.JobTypeWorkflowLaunch:
		switch {
		case len(job.Failure) > 0 && len(job.URL) > 0:
			msg = fmt.Sprintf("your cluster failed to launch: %s (<%s|logs>)", job.Failure, job.URL)
			if postMessage {
				_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(msg, false))
				if err != nil {
					klog.Warningf("Failed to post the msg: %s\nto the channel: %s.", msg, job.RequestedChannel)
				}
			}
		case len(job.Failure) > 0:
			msg = fmt.Sprintf("your cluster failed to launch: %s", job.Failure)
			if postMessage {
				_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(msg, false))
				if err != nil {
					klog.Warningf("Failed to post the msg: %s\nto the channel: %s.", msg, job.RequestedChannel)
				}
			}
		case len(job.Credentials) == 0 && len(job.URL) > 0:
			msg = fmt.Sprintf("cluster is still starting (launched %d minutes ago, <%s|logs>)", time.Since(job.RequestedAt)/time.Minute, job.URL)
			if postMessage {
				_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(msg, false))
				if err != nil {
					klog.Warningf("Failed to post the msg: %s\nto the channel: %s.", msg, job.RequestedChannel)
				}
			}
		case len(job.Credentials) == 0:
			msg = fmt.Sprintf("cluster is still starting (launched %d minutes ago)", time.Since(job.RequestedAt)/time.Minute)
			if postMessage {
				_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(msg, false))
				if err != nil {
					klog.Warningf("Failed to post the msg: %s\nto the channel: %s.", msg, job.RequestedChannel)
				}
			}
		default:
			msg = fmt.Sprintf(
				"Your cluster is ready, it will be shut down automatically in ~%d minutes.",
				time.Until(job.ExpiresAt)/time.Minute,
			)
			if len(job.PasswordSnippet) > 0 {
				msg += "\n" + job.PasswordSnippet
			}
			kubeconfig = job.Credentials
			if postMessage {
				SendKubeConfig(client, job.RequestedChannel, kubeconfig, msg, job.RequestedAt.Format("2006-01-02-150405"))
			}
		}
		return msg, kubeconfig
	}

	// Catalog builds incomplete after the job completes; assume complete unless catalog build
	incomplete := false
	if job.Mode == manager.JobTypeCatalog {
		incomplete = !job.CatalogComplete
	}

	var failure, success bool
	switch job.State {
	case prowapiv1.FailureState, prowapiv1.AbortedState, prowapiv1.ErrorState:
		failure = true
	case prowapiv1.SuccessState:
		if job.CatalogError {
			failure = true
		}
		success = true
	}

	if !incomplete {
		if len(job.URL) > 0 {
			if failure {
				msg = fmt.Sprintf("job <%s | %s> failed", job.URL, job.OriginalMessage)
				if postMessage {
					_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(msg, false))
					if err != nil {
						klog.Warningf("Failed to post the msg: %s\nto the channel: %s.", msg, job.RequestedChannel)
					}
				}
				return msg, kubeconfig
			}
			if success {
				msg = fmt.Sprintf("job <%s | %s> succeeded", job.URL, job.OriginalMessage)
				if postMessage {
					_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(msg, false))
					if err != nil {
						klog.Warningf("Failed to post the msg: %s\nto the channel: %s.", msg, job.RequestedChannel)
					}
				}
				return msg, kubeconfig
			}
		} else {
			if failure {
				msg = fmt.Sprintf("job %s failed, but no details could be retrieved", job.OriginalMessage)
				if postMessage {
					_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(msg, false))
					if err != nil {
						klog.Warningf("Failed to post the msg: %s\nto the channel: %s.", msg, job.RequestedChannel)
					}
				}
				return msg, kubeconfig
			}
			if success {
				msg = fmt.Sprintf("job %s succeded, but no details could be retrieved", job.OriginalMessage)
				if postMessage {
					_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(msg, false))
					if err != nil {
						klog.Warningf("Failed to post the msg: %s\nto the channel: %s.", msg, job.RequestedChannel)
					}
				}
				return msg, kubeconfig
			}
		}
	}

	switch {
	case len(job.Credentials) == 0 && len(job.URL) > 0:
		if len(job.OriginalMessage) > 0 {
			msg = fmt.Sprintf("job <%s|%s> is running", job.URL, job.OriginalMessage)
			if postMessage {
				_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(msg, false))
				if err != nil {
					klog.Warningf("Failed to post the msg: %s\nto the channel: %s.", msg, job.RequestedChannel)
				}
			}
		} else {
			msg = fmt.Sprintf("job is running, see %s for details", job.URL)
			if postMessage {
				_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(msg, false))
				if err != nil {
					klog.Warningf("Failed to post the msg: %s\nto the channel: %s.", msg, job.RequestedChannel)
				}
			}
		}
	case len(job.Credentials) == 0:
		msg = fmt.Sprintf("job is running (launched %d minutes ago)", time.Since(job.RequestedAt)/time.Minute)
		if postMessage {
			_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(msg, false))
			if err != nil {
				klog.Warningf("Failed to post the msg: %s\nto the channel: %s.", msg, job.RequestedChannel)
			}
		}
	default:
		msg = "Your job has started a cluster, it will be shut down when the test ends."
		if len(job.URL) > 0 {
			msg += fmt.Sprintf(" See %s for details.", job.URL)
		}
		if len(job.PasswordSnippet) > 0 {
			msg += "\n" + job.PasswordSnippet
		}
		kubeconfig = job.Credentials
		if postMessage {
			SendKubeConfig(client, job.RequestedChannel, kubeconfig, msg, job.RequestedAt.Format("2006-01-02-150405"))
		}
	}
	return msg, kubeconfig
}

func SendKubeConfig(client parser.SlackClient, channel, contents, comment, identifier string) string {
	params := slack.UploadFileV2Parameters{
		Content:        contents,
		FileSize:       len(contents),
		Channel:        channel,
		Filename:       fmt.Sprintf("cluster-bot-%s.kubeconfig", identifier),
		InitialComment: comment,
	}
	summary, err := client.UploadFileV2(params)
	if err != nil {
		klog.Errorf("error: unable to send attachment with message: %v", err)
		return ""
	}
	klog.Infof("successfully uploaded file to %s", channel)
	return summary.ID
}

func SendGCPServiceAccountKey(client parser.SlackClient, channel, keyJSON, email string) error {
	sanitized := strings.ReplaceAll(strings.ReplaceAll(email, "@", "-"), ".", "-")
	params := slack.UploadFileV2Parameters{
		Content:        keyJSON,
		FileSize:       len(keyJSON),
		Channel:        channel,
		Filename:       fmt.Sprintf("gcp-access-%s.json", sanitized),
		InitialComment: "⚠️  Service Account Key - Keep secure and do not share!",
	}
	_, err := client.UploadFileV2(params)
	if err != nil {
		klog.Errorf("error: unable to upload GCP service account key: %v", err)
		return err
	}
	klog.Infof("successfully uploaded GCP key to %s", channel)
	return nil
}

func CodeSlice(items []string) []string {
	code := make([]string, 0, len(items))
	for _, item := range items {
		code = append(code, fmt.Sprintf("`%s`", item))
	}
	return code
}

func ParseImageInput(input string) ([]string, error) {
	input = strings.TrimSpace(input)
	if len(input) == 0 {
		return nil, nil
	}
	input = utils.StripLinks(input)
	parts := strings.Split(input, ",")
	var validParts []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) > 0 {
			validParts = append(validParts, part)
		}
	}
	if len(validParts) == 0 {
		return nil, fmt.Errorf("no valid inputs found. Please provide at least one version, image, or PR")
	}
	return validParts, nil
}

// findClosestMatch finds the closest match to input from a list of valid options
func findClosestMatch(input string, validOptions []string) string {
	input = strings.ToLower(input)
	var bestMatch string
	bestScore := 0

	for _, option := range validOptions {
		optionLower := strings.ToLower(option)
		score := 0

		// Exact match
		if input == optionLower {
			return option
		}

		// Prefix match gets high score
		if strings.HasPrefix(optionLower, input) {
			score = len(input) * 2
		}

		// Contains match gets medium score
		if strings.Contains(optionLower, input) {
			score = len(input)
		}

		// Simple character overlap
		for _, char := range input {
			if strings.ContainsRune(optionLower, char) {
				score++
			}
		}

		if score > bestScore {
			bestScore = score
			bestMatch = option
		}
	}

	// Only suggest if we have a reasonable match
	// Require at least 2/3 of the characters to match, minimum score of 2
	minScore := max(2, (len(input)*2)/3)
	if bestScore >= minScore {
		return bestMatch
	}
	return ""
}

func ParseOptions(options string, inputs [][]string, jobType manager.JobType) (string, string, map[string]string, error) {
	params, err := utils.ParamsFromAnnotation(options)
	if err != nil {
		return "", "", nil, fmt.Errorf("options could not be parsed: %w", err)
	}
	var platform, architecture string
	for opt := range params {
		switch {
		case slices.Contains(manager.SupportedPlatforms, opt):
			if len(platform) > 0 {
				return "", "", nil, fmt.Errorf("you may only specify one platform in options")
			}
			platform = opt
			delete(params, opt)
		case slices.Contains(manager.SupportedArchitectures, opt):
			if len(architecture) > 0 {
				return "", "", nil, fmt.Errorf("you may only specify one architecture in options")
			}
			architecture = opt
			delete(params, opt)
		case opt == "":
			delete(params, opt)
		case slices.Contains(manager.SupportedParameters, opt):
			// do nothing
		default:
			// Try to find a close match
			allOptions := make([]string, 0, len(manager.SupportedPlatforms)+len(manager.SupportedArchitectures)+len(manager.SupportedParameters))
			allOptions = append(allOptions, manager.SupportedPlatforms...)
			allOptions = append(allOptions, manager.SupportedArchitectures...)
			allOptions = append(allOptions, manager.SupportedParameters...)

			suggestion := findClosestMatch(opt, allOptions)
			if suggestion != "" {
				return "", "", nil, fmt.Errorf("unrecognized option '%s'. Did you mean '%s'?\n\nValid options:\n- Platforms: %s\n- Architectures: %s\n- Parameters: %s",
					opt, suggestion,
					strings.Join(manager.SupportedPlatforms, ", "),
					strings.Join(manager.SupportedArchitectures, ", "),
					strings.Join(manager.SupportedParameters, ", "))
			}
			return "", "", nil, fmt.Errorf("unrecognized option '%s'.\n\nValid options:\n- Platforms: %s\n- Architectures: %s\n- Parameters: %s",
				opt,
				strings.Join(manager.SupportedPlatforms, ", "),
				strings.Join(manager.SupportedArchitectures, ", "),
				strings.Join(manager.SupportedParameters, ", "))
		}
	}
	if len(platform) == 0 {
		switch architecture {
		case "", "multi":
			// for hypershift, only support normal launches
			if jobType == manager.JobTypeInstall || jobType == manager.JobTypeLaunch {
				// only use hypershift for supported versions
				manager.HypershiftSupportedVersions.Mu.RLock()
				defer manager.HypershiftSupportedVersions.Mu.RUnlock()
				var validVersion bool
				if len(inputs) == 1 {
					for version := range manager.HypershiftSupportedVersions.Versions {
						if strings.HasPrefix(inputs[0][0], version) {
							validVersion = true
							break
						}
					}
				}
				if validVersion {
					platform = "hypershift-hosted"
				} else if manager.HypershiftSupportedVersions.Versions.Has(fmt.Sprintf("%d.%d", manager.CurrentRelease.Major, manager.CurrentRelease.Minor)) &&
					(len(inputs) == 0 || inputs[0][0] == "nightly" || inputs[0][0] == "ci" || inputs[0][0] == "prerelease") {
					platform = "hypershift-hosted"
				} else {
					platform = "aws"
				}
			} else {
				platform = "aws"
			}
		case "amd64", "arm64":
			platform = "aws"
		default:
			return "", "", nil, fmt.Errorf("unknown architecture: %s", architecture)
		}
	}
	if architecture == "" {
		if platform == "hypershift-hosted" {
			architecture = "multi"
		} else {
			architecture = "amd64"
		}
	}
	if architecture != "multi" && platform == "hypershift-hosted" {
		return "", "", nil, fmt.Errorf("The hypershift-hosted platform requires a multiarch image. See: https://docs.ci.openshift.org/docs/architecture/ci-operator/#testing-with-a-cluster-from-hypershift") //nolint:staticcheck
	}
	return platform, architecture, params, nil
}

func NotifyMce(client parser.SlackClient, cluster *clusterv1.ManagedCluster, clusterDeployment *hivev1.ClusterDeployment, clusterProvision *hivev1.ClusterProvision, kubeconfig, password string, postMessage bool, errMsg error) (string, string) {
	channel := cluster.Annotations[utils.ChannelTag]
	if errMsg != nil {
		msg := fmt.Sprintf("Creation of your cluster has failed with the following message: ```%s```\nExisting cluster resources will be deleted.", errMsg.Error())
		if postMessage {
			if _, _, err := client.PostMessage(channel, slack.MsgOptionText(msg, false)); err != nil {
				klog.Warningf("Failed to post the message to the channel: %s.", channel)
			}
		}
		return msg, ""
	}
	var availability string
	for _, condition := range cluster.Status.Conditions {
		if condition.Type == "ManagedClusterConditionAvailable" {
			availability = string(condition.Status)
		}
	}
	if clusterProvision != nil && clusterProvision.Spec.Stage == hivev1.ClusterProvisionStageFailed {
		failedCondition := hivev1.ClusterProvisionCondition{}
		for _, condition := range clusterProvision.Status.Conditions {
			if condition.ConditionType() == hivev1.ClusterProvisionFailedCondition {
				failedCondition = condition
			}
		}
		message := fmt.Sprintf("your cluster (name: `%s`) has failed to provision. Reason for failure is: `%s`. Error message is:\n```%s```", cluster.GetName(), failedCondition.Reason, failedCondition.Message)
		if clusterProvision.Spec.InstallLog == nil {
			if postMessage {
				if _, _, err := client.PostMessage(channel, slack.MsgOptionText(message, false)); err != nil {
					klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, channel)
				}
			}
			return message, ""
		}
		params := slack.UploadFileV2Parameters{
			Content:        *clusterProvision.Spec.InstallLog,
			FileSize:       len(*clusterProvision.Spec.InstallLog),
			Channel:        channel,
			Filename:       fmt.Sprintf("%s-error.txt", cluster.Name),
			InitialComment: message,
		}
		_, err := client.UploadFileV2(params)
		if err != nil {
			klog.Errorf("error: unable to send attachment with message: %v", err)
			return message, ""
		}
		return message, ""
	}
	// in some cases, an early provisioning fail may result in a ClusterProvision not being created
	if clusterDeployment != nil {
		for _, provisionCondition := range clusterDeployment.Status.Conditions {
			if provisionCondition.Type == hivev1.ProvisionFailedCondition {
				if provisionCondition.Status == "True" {
					message := fmt.Sprintf("your cluster (name: `%s`) has failed to provision. Reason for failure is: `%s`.  Error message is:\n```%s```", cluster.GetName(), provisionCondition.Reason, provisionCondition.Message)
					if postMessage {
						if _, _, err := client.PostMessage(channel, slack.MsgOptionText(message, false)); err != nil {
							klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, channel)
						}
					}
					return message, ""
				}
				break
			}
		}
	}
	if availability == "True" {
		message := fmt.Sprintf("your cluster (name: `%s`) is ready", cluster.GetName())
		expiryTime := cluster.Annotations[utils.ExpiryTimeTag]
		if parsedExpiryTime, err := time.Parse(time.RFC3339, string(expiryTime)); err != nil {
			klog.Errorf("Failed to parse expiry time: %v", err)
			message += "."
		} else {
			message += fmt.Sprintf(", it will be shut down automatically in ~%d minutes.", time.Until(parsedExpiryTime)/time.Minute)
		}
		requestTime := cluster.Annotations[utils.RequestTimeTag]
		parsedRequestTime, err := time.Parse(time.RFC3339, string(requestTime))
		if err != nil {
			// fall back to current time if parse fails
			parsedRequestTime = time.Now()
			klog.Errorf("Failed to parse request time: %v", err)
		}
		message += "\n" + clusterDeployment.Status.WebConsoleURL
		ocLoginCommand := fmt.Sprintf("oc login %s --username kubeadmin --password %s", clusterDeployment.Status.APIURL, password)
		message += "\n\nLog in to the console with user `kubeadmin` and password `" + password + "`.\nTo use the `oc` command, log in by running `" + ocLoginCommand + "`."
		if postMessage {
			SendKubeConfig(client, channel, kubeconfig, message, parsedRequestTime.Format("2006-01-02-150405"))
		}
		return message, kubeconfig
	}
	if postMessage {
		if _, _, err := client.PostMessage(channel, slack.MsgOptionText(fmt.Sprintf("Cluster %s is not yet available", cluster.GetName()), false)); err != nil {
			klog.Warningf("Failed to post the message to the channel: %s.", channel)
		}
	}
	return fmt.Sprintf("Cluster %s is not yet available", cluster.GetName()), ""
}

func NotifyRosa(client parser.SlackClient, cluster *clustermgmtv1.Cluster, password string) {
	channel := cluster.AWS().Tags()[utils.ChannelTag]
	switch {
	case cluster.State() == clustermgmtv1.ClusterStateError:
		message := fmt.Sprintf("your cluster (name: `%s`, id: `%s`) has encountered an error; please contact the CRT team in #forum-ocp-crt", cluster.Name(), cluster.ID())
		_, _, err := client.PostMessage(channel, slack.MsgOptionText(message, false))
		if err != nil {
			klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, channel)
		}
	case cluster.State() == clustermgmtv1.ClusterStateInstalling:
		message := fmt.Sprintf("cluster is still starting (launched %d minutes ago)", time.Since(cluster.CreationTimestamp())/time.Minute)
		_, _, err := client.PostMessage(channel, slack.MsgOptionText(message, false))
		if err != nil {
			klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, channel)
		}
	default:
		message := "Your cluster is ready"
		expiryTime, err := base64.RawStdEncoding.DecodeString(cluster.AWS().Tags()[utils.ExpiryTimeTag])
		if err != nil {
			klog.Errorf("Failed to base64 decode expiry time tag: %v", err)
			message += "."
		} else if parsedExpiryTime, err := time.Parse(time.RFC3339, string(expiryTime)); err != nil {
			klog.Errorf("Failed to parse time: %v", err)
			message += "."
		} else {
			message += fmt.Sprintf(", it will be shut down automatically in ~%d minutes.", time.Until(parsedExpiryTime)/time.Minute)
		}
		if console, ok := cluster.GetConsole(); ok {
			message += "\n" + console.URL()
		} else {
			message += "\nYour cluster's console is not currently available. We will send you another message when the console becomes ready. To manually check if the console is ready, use the `auth` command."
		}
		ocLoginCommand := fmt.Sprintf("oc login %s --username cluster-admin --password %s", cluster.API().URL(), password)
		message += "\n\nLog in to the console with user `cluster-admin` and password `" + password + "`.\nTo use the `oc` command, log in by running `" + ocLoginCommand + "`."
		if _, _, err := client.PostMessage(channel, slack.MsgOptionText(message, false)); err != nil {
			klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, channel)
		}
	}
}
