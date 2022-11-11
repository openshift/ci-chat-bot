package slack

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/parser"
	"github.com/openshift/ci-chat-bot/pkg/utils"
	"github.com/slack-go/slack"
	"k8s.io/klog"
	prowapiv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
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
		NotifyJob(s, &job)
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
		parser.NewBotCommand("launch <image_or_version_or_pr> <options>", &parser.CommandDefinition{
			Description: fmt.Sprintf("Launch an OpenShift cluster using a known image, version, or PR. You may omit both arguments. Use `nightly` for the latest OCP build, `ci` for the the latest CI build, provide a version directly from any listed on https://amd64.ocp.releases.ci.openshift.org, a stream name (4.1.0-0.ci, 4.1.0-0.nightly, etc), a major/minor `X.Y` to load the \"next stable\" version, from nightly, for that version (`4.1`), `<org>/<repo>#<pr>` to launch from a PR, or an image for the first argument. Options is a comma-delimited list of variations including platform (%s) and variant (%s).",
				strings.Join(CodeSlice(manager.SupportedPlatforms), ", "),
				strings.Join(CodeSlice(manager.SupportedParameters), ", ")),
			Example: "launch openshift/origin#49563 gcp",
			Handler: LaunchCluster,
		}),
		parser.NewBotCommand("list", &parser.CommandDefinition{
			Description: "See who is hogging all the clusters.",
			Handler:     List,
		}),
		parser.NewBotCommand("done", &parser.CommandDefinition{
			Description: "Terminate the running cluster",
			Handler:     Done,
		}),
		parser.NewBotCommand("refresh", &parser.CommandDefinition{
			Description: "If the cluster is currently marked as failed, retry fetching its credentials in case of an error.",
			Handler:     Refresh,
		}),
		parser.NewBotCommand("auth", &parser.CommandDefinition{
			Description: "Send the credentials for the cluster you most recently requested",
			Handler:     Auth,
		}),
		parser.NewBotCommand("test upgrade <from> <to> <options>", &parser.CommandDefinition{
			Description: fmt.Sprintf("Run the upgrade tests between two release images. The arguments may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org. You may change the upgrade test by passing `test=NAME` in options with one of %s", strings.Join(CodeSlice(manager.SupportedUpgradeTests), ", ")),
			Handler:     TestUpgrade,
		}),
		parser.NewBotCommand("test <name> <image_or_version_or_pr> <options>", &parser.CommandDefinition{
			Description: fmt.Sprintf("Run the requested test suite from an image or release or built PRs. Supported test suites are %s. The from argument may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org. ", strings.Join(CodeSlice(manager.SupportedTests), ", ")),
			Handler:     Test,
		}),
		parser.NewBotCommand("build <pullrequest>", &parser.CommandDefinition{
			Description: "Create a new release image from one or more pull requests. The successful build location will be sent to you when it completes and then preserved for 12 hours.  Example: `build openshift/operator-framework-olm#68,operator-framework/operator-marketplace#396`. To obtain a pull secret use `oc registry login --to /path/to/pull-secret` after using `oc login` to login to the relevant CI cluster.",
			Handler:     Build,
		}),
		parser.NewBotCommand("workflow-launch <name> <image_or_version_or_pr> <parameters>", &parser.CommandDefinition{
			Description: "Launch a cluster using the requested workflow from an image or release or built PRs. The from argument may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org.",
			Handler:     WorkflowLaunch,
		}),
		parser.NewBotCommand("workflow-upgrade <name> <from_image_or_version_or_pr> <to_image_or_version_or_pr> <parameters>", &parser.CommandDefinition{
			Description: "Run a custom upgrade using the requested workflow from an image or release or built PRs to a specified version/image/pr from https://amd64.ocp.releases.ci.openshift.org. ",
			Handler:     WorkflowUpgrade,
		}),
		parser.NewBotCommand("version", &parser.CommandDefinition{
			Description: "Report the version of the bot",
			Handler:     Version,
		}),
		parser.NewBotCommand("lookup <image_or_version_or_pr> <architecture>", &parser.CommandDefinition{
			Description: "Get info about a version.",
			Handler:     Lookup,
		}),
	}
}

func GetUserName(client *slack.Client, userID string) string {
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
			if utils.Contains(manager.SupportedArchitectures, workflow.Architecture) {
				architecture = workflow.Architecture
			} else {
				return "", "", fmt.Errorf("architecture %s not supported by cluster-bot", workflow.Architecture)
			}
		}
	}
	return platform, architecture, nil
}

func BuildJobParams(params string) (map[string]string, error) {
	var splitParams []string
	if len(params) > 0 {
		splitParams = strings.Split(params, "\",\"")
		// first item will have a double quote at the beginning
		splitParams[0] = strings.TrimPrefix(splitParams[0], "\"")
		// last item will have a double quote at the end
		splitParams[len(splitParams)-1] = strings.TrimSuffix(splitParams[len(splitParams)-1], "\"")
	}
	jobParams := make(map[string]string)
	for _, combinedParam := range splitParams {
		split := strings.Split(combinedParam, "=")
		if len(split) != 2 {
			return nil, fmt.Errorf("unable to interpret `%s` as a parameter. Please ensure that all parameters are in the form of KEY=VALUE", combinedParam)
		}
		jobParams[split[0]] = split[1]
	}
	return jobParams, nil
}

func NotifyJob(client *slack.Client, job *manager.Job) {
	switch job.Mode {
	case manager.JobTypeLaunch, manager.JobTypeWorkflowLaunch:
		switch {
		case len(job.Failure) > 0 && len(job.URL) > 0:
			message := fmt.Sprintf("your cluster failed to launch: %s (<%s|logs>)", job.Failure, job.URL)
			_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(message, false))
			if err != nil {
				klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, job.RequestedChannel)
			}
		case len(job.Failure) > 0:
			message := fmt.Sprintf("your cluster failed to launch: %s", job.Failure)
			_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(message, false))
			if err != nil {
				klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, job.RequestedChannel)
			}
		case len(job.Credentials) == 0 && len(job.URL) > 0:
			message := fmt.Sprintf("cluster is still starting (launched %d minutes ago, <%s|logs>)", time.Since(job.RequestedAt)/time.Minute, job.URL)
			_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(message, false))
			if err != nil {
				klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, job.RequestedChannel)
			}
		case len(job.Credentials) == 0:
			message := fmt.Sprintf("cluster is still starting (launched %d minutes ago)", time.Since(job.RequestedAt)/time.Minute)
			_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(message, false))
			if err != nil {
				klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, job.RequestedChannel)
			}
		default:
			comment := fmt.Sprintf(
				"Your cluster is ready, it will be shut down automatically in ~%d minutes.",
				time.Until(job.ExpiresAt)/time.Minute,
			)
			if len(job.PasswordSnippet) > 0 {
				comment += "\n" + job.PasswordSnippet
			}
			SendKubeConfig(client, job.RequestedChannel, job.Credentials, comment, job.RequestedAt.Format("2006-01-02-150405"))
		}
		return
	}

	if len(job.URL) > 0 {
		switch job.State {
		case prowapiv1.FailureState, prowapiv1.AbortedState, prowapiv1.ErrorState:
			message := fmt.Sprintf("job <%s|%s> failed", job.URL, job.OriginalMessage)
			_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(message, false))
			if err != nil {
				klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, job.RequestedChannel)
			}
			return
		case prowapiv1.SuccessState:
			message := fmt.Sprintf("job <%s|%s> succeeded", job.URL, job.OriginalMessage)
			_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(message, false))
			if err != nil {
				klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, job.RequestedChannel)
			}
			return
		}
	} else {
		switch job.State {
		case prowapiv1.FailureState, prowapiv1.AbortedState, prowapiv1.ErrorState:
			message := fmt.Sprintf("job %s failed, but no details could be retrieved", job.OriginalMessage)
			_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(message, false))
			if err != nil {
				klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, job.RequestedChannel)
			}
			return
		case prowapiv1.SuccessState:
			message := fmt.Sprintf("job %s succeded, but no details could be retrieved", job.OriginalMessage)
			_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(message, false))
			if err != nil {
				klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, job.RequestedChannel)
			}
			return
		}
	}

	switch {
	case len(job.Credentials) == 0 && len(job.URL) > 0:
		if len(job.OriginalMessage) > 0 {
			message := fmt.Sprintf("job <%s|%s> is running", job.URL, job.OriginalMessage)
			_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(message, false))
			if err != nil {
				klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, job.RequestedChannel)
			}
		} else {
			message := fmt.Sprintf("job is running, see %s for details", job.URL)
			_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(message, false))
			if err != nil {
				klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, job.RequestedChannel)
			}
		}
	case len(job.Credentials) == 0:
		message := fmt.Sprintf("job is running (launched %d minutes ago)", time.Since(job.RequestedAt)/time.Minute)
		_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(message, false))
		if err != nil {
			klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, job.RequestedChannel)
		}
	default:
		comment := "Your job has started a cluster, it will be shut down when the test ends."
		if len(job.URL) > 0 {
			comment += fmt.Sprintf(" See %s for details.", job.URL)
		}
		if len(job.PasswordSnippet) > 0 {
			comment += "\n" + job.PasswordSnippet
		}
		SendKubeConfig(client, job.RequestedChannel, job.Credentials, comment, job.RequestedAt.Format("2006-01-02-150405"))
	}
}

func SendKubeConfig(client *slack.Client, channel, contents, comment, identifier string) {
	params := slack.FileUploadParameters{
		Content:        contents,
		Channels:       []string{channel},
		Filename:       fmt.Sprintf("cluster-bot-%s.kubeconfig", identifier),
		Filetype:       "text",
		InitialComment: comment,
	}
	_, err := client.UploadFile(params)
	if err != nil {
		klog.Errorf("error: unable to send attachment with message: %v", err)
		return
	}
	klog.Infof("successfully uploaded file to %s", channel)
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
	for _, part := range parts {
		if len(part) == 0 {
			return nil, fmt.Errorf("image inputs must not contain empty items")
		}
	}
	return parts, nil
}

func ParseOptions(options string) (string, string, map[string]string, error) {
	params, err := utils.ParamsFromAnnotation(options)
	if err != nil {
		return "", "", nil, fmt.Errorf("options could not be parsed: %w", err)
	}
	var platform, architecture string
	for opt := range params {
		switch {
		case utils.Contains(manager.SupportedPlatforms, opt):
			if len(platform) > 0 {
				return "", "", nil, fmt.Errorf("you may only specify one platform in options")
			}
			platform = opt
			delete(params, opt)
		case utils.Contains(manager.SupportedArchitectures, opt):
			if len(architecture) > 0 {
				return "", "", nil, fmt.Errorf("you may only specify one architecture in options")
			}
			architecture = opt
			delete(params, opt)
		case opt == "":
			delete(params, opt)
		case utils.Contains(manager.SupportedParameters, opt):
			// do nothing
		default:
			return "", "", nil, fmt.Errorf("unrecognized option: %s", opt)
		}
	}
	if len(architecture) == 0 {
		architecture = "amd64"
	}
	if len(platform) == 0 {
		switch architecture {
		case "amd64", "arm64", "multi":
			platform = "aws"
		default:
			return "", "", nil, fmt.Errorf("unknown architecture: %s", architecture)
		}
	}
	return platform, architecture, params, nil
}
