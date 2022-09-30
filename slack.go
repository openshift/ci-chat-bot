package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"k8s.io/klog"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	prowapiv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/pjutil/pprof"
	"k8s.io/test-infra/prow/simplifypath"
)

type Bot struct {
	botToken         string
	botSigningSecret string
	gracePeriod      time.Duration
	port             int
	userID           string
	workflowConfig   *WorkflowConfig
}

const (
	MetricsPort           = 9090
	PProfPort             = 6060
	HealthPort            = 8081
	MemoryProfileInterval = 30 * time.Second
)

var (
	promMetrics = metrics.NewMetrics("cluster_bot")
)

func NewBot(botToken, botSigningSecret string, graceperiod time.Duration, port int, workflowConfig *WorkflowConfig) *Bot {
	return &Bot{
		botToken:         botToken,
		botSigningSecret: botSigningSecret,
		gracePeriod:      graceperiod,
		port:             port,
		userID:           "unknown",
		workflowConfig:   workflowConfig,
	}
}

func l(fragment string, children ...simplifypath.Node) simplifypath.Node {
	return simplifypath.L(fragment, children...)
}

func (b *Bot) SupportedCommands() []BotCommand {
	return []BotCommand{
		NewBotCommand("launch <image_or_version_or_pr> <options>", &CommandDefinition{
			Description: fmt.Sprintf("Launch an OpenShift cluster using a known image, version, or PR. You may omit both arguments. Use `nightly` for the latest OCP build, `ci` for the the latest CI build, provide a version directly from any listed on https://amd64.ocp.releases.ci.openshift.org, a stream name (4.1.0-0.ci, 4.1.0-0.nightly, etc), a major/minor `X.Y` to load the \"next stable\" version, from nightly, for that version (`4.1`), `<org>/<repo>#<pr>` to launch from a PR, or an image for the first argument. Options is a comma-delimited list of variations including platform (%s) and variant (%s).",
				strings.Join(codeSlice(supportedPlatforms), ", "),
				strings.Join(codeSlice(supportedParameters), ", ")),
			Example: "launch openshift/origin#49563 gcp",
			Handler: LaunchCluster,
		}),
		NewBotCommand("list", &CommandDefinition{
			Description: "See who is hogging all the clusters.",
			Handler:     List,
		}),
		NewBotCommand("done", &CommandDefinition{
			Description: "Terminate the running cluster",
			Handler:     Done,
		}),
		NewBotCommand("refresh", &CommandDefinition{
			Description: "If the cluster is currently marked as failed, retry fetching its credentials in case of an error.",
			Handler:     Refresh,
		}),
		NewBotCommand("auth", &CommandDefinition{
			Description: "Send the credentials for the cluster you most recently requested",
			Handler:     Auth,
		}),
		NewBotCommand("test upgrade <from> <to> <options>", &CommandDefinition{
			Description: fmt.Sprintf("Run the upgrade tests between two release images. The arguments may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org. You may change the upgrade test by passing `test=NAME` in options with one of %s", strings.Join(codeSlice(supportedUpgradeTests), ", ")),
			Handler:     TestUpgrade,
		}),
		NewBotCommand("test <name> <image_or_version_or_pr> <options>", &CommandDefinition{
			Description: fmt.Sprintf("Run the requested test suite from an image or release or built PRs. Supported test suites are %s. The from argument may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org. ", strings.Join(codeSlice(supportedTests), ", ")),
			Handler:     Test,
		}),
		NewBotCommand("build <pullrequest>", &CommandDefinition{
			Description: "Create a new release image from one or more pull requests. The successful build location will be sent to you when it completes and then preserved for 12 hours.  Example: `build openshift/operator-framework-olm#68,operator-framework/operator-marketplace#396`. To obtain a pull secret use `oc registry login --to /path/to/pull-secret` after using `oc login` to login to the relevant CI cluster.",
			Handler:     Build,
		}),
		NewBotCommand("workflow-launch <name> <image_or_version_or_pr> <parameters>", &CommandDefinition{
			Description: "Launch a cluster using the requested workflow from an image or release or built PRs. The from argument may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org.",
			Handler:     b.WorkflowLaunch,
		}),
		NewBotCommand("workflow-upgrade <name> <from_image_or_version_or_pr> <to_image_or_version_or_pr> <parameters>", &CommandDefinition{
			Description: "Run a custom upgrade using the requested workflow from an image or release or built PRs to a specified version/image/pr from https://amd64.ocp.releases.ci.openshift.org. ",
			Handler:     b.WorkflowUpgrade,
		}),
		NewBotCommand("version", &CommandDefinition{
			Description: "Report the version of the bot",
			Handler:     Version,
		}),
		NewBotCommand("lookup <image_or_version_or_pr> <architecture>", &CommandDefinition{
			Description: "Get info about a version.",
			Handler:     Lookup,
		}),
	}
}

func (b *Bot) Start(manager JobManager) {
	slackClient := slack.New(b.botToken)
	manager.SetNotifier(b.jobResponder(slackClient))

	metrics.ExposeMetrics("ci-chat-bot", config.PushGateway{}, MetricsPort)
	simplifier := simplifypath.NewSimplifier(l("", // shadow element mimicking the root
		l(""), // for black-box health checks
		l("slack",
			l("events-endpoint"),
		),
	))
	handler := metrics.TraceHandler(simplifier, promMetrics.HTTPRequestDuration, promMetrics.HTTPResponseSize)
	pprof.Instrument(prowflagutil.InstrumentationOptions{
		MetricsPort:           MetricsPort,
		PProfPort:             PProfPort,
		HealthPort:            HealthPort,
		MemoryProfileInterval: MemoryProfileInterval,
	})
	health := pjutil.NewHealth()

	mux := http.NewServeMux()
	// handle the root to allow for a simple uptime probe
	mux.Handle("/", handler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(http.StatusOK) })))
	mux.Handle("/slack/events-endpoint", handler(handleEvent(b.botSigningSecret, slackClient, manager, b.SupportedCommands())))
	server := &http.Server{Addr: ":" + strconv.Itoa(b.port), Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	health.ServeReady()

	interrupts.ListenAndServe(server, b.gracePeriod)
	interrupts.WaitForGracefulShutdown()

	klog.Infof("ci-chat-bot up and listening to slack")
}

func handleEvent(signingSecret string, client *slack.Client, manager JobManager, botCommands []BotCommand) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		body, ok := verifiedBody(request, signingSecret)
		if !ok {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		event, err := slackevents.ParseEvent(body, slackevents.OptionNoVerifyToken())
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		if event.Type == slackevents.URLVerification {
			var response *slackevents.ChallengeResponse
			err := json.Unmarshal(body, &response)
			if err != nil {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			writer.Header().Set("Content-Type", "text")
			if _, err := writer.Write([]byte(response.Challenge)); err != nil {
				klog.Errorf("Failed to write response. %v", err)
			}
		}

		// we always want to respond with 200 immediately
		writer.WriteHeader(http.StatusOK)
		// we don't really care how long this takes
		go func() {
			if err := HandleEvent(client, &event, manager, botCommands); err != nil {
				klog.Errorf("Failed to handle event: %v", err)
			}
		}()
	}
}

func verifiedBody(request *http.Request, signingSecret string) ([]byte, bool) {
	verifier, err := slack.NewSecretsVerifier(request.Header, signingSecret)
	if err != nil {
		klog.Errorf("Failed to create a secrets verifier. %v", err)
		return nil, false
	}

	body, err := ioutil.ReadAll(request.Body)
	if err != nil {
		klog.Errorf("Failed to read an event payload. %v", err)
		return nil, false
	}

	// need to use body again when unmarshalling
	request.Body = ioutil.NopCloser(bytes.NewBuffer(body))

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

func getPlatformArchFromWorkflowConfig(workflowConfig *WorkflowConfig, name string) (string, string, error) {
	platform := ""
	architecture := "amd64"
	workflowConfig.mutex.RLock()
	defer workflowConfig.mutex.RUnlock()
	if workflow, ok := workflowConfig.Workflows[name]; !ok {
		return "", "", fmt.Errorf("Workflow %s not in workflow list. Please add %s to the workflows list before retrying this command", name, name)
	} else {
		platform = workflow.Platform
		if workflow.Architecture != "" {
			if contains(supportedArchitectures, workflow.Architecture) {
				architecture = workflow.Architecture
			} else {
				return "", "", fmt.Errorf("Architecture %s not supported by cluster-bot", workflow.Architecture)
			}
		}
	}
	return platform, architecture, nil
}

func buildJobParams(params string) (map[string]string, error) {
	splitParams := []string{}
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
			return nil, fmt.Errorf("Unable to interpret `%s` as a parameter. Please ensure that all parameters are in the form of KEY=VALUE", combinedParam)
		}
		jobParams[split[0]] = split[1]
	}
	return jobParams, nil
}

func (b *Bot) jobResponder(s *slack.Client) func(Job) {
	return func(job Job) {
		if len(job.RequestedChannel) == 0 || len(job.RequestedBy) == 0 {
			klog.Infof("job %q has no requested channel or user, can't notify", job.Name)
			return
		}
		switch job.Mode {
		case JobTypeLaunch, JobTypeWorkflowLaunch:
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

func NotifyJob(client *slack.Client, job *Job) {
	switch job.Mode {
	case JobTypeLaunch, JobTypeWorkflowLaunch:
		if job.LegacyConfig {
			message := "WARNING: using legacy template based job for this cluster. This is unsupported and the cluster may not install as expected. Contact #forum-crt for more information."
			_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(message, false))
			if err != nil {
				klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, job.RequestedChannel)
			}
		}
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
			message := fmt.Sprintf("cluster is still starting (launched %d minutes ago, <%s|logs>)", time.Now().Sub(job.RequestedAt)/time.Minute, job.URL)
			_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(message, false))
			if err != nil {
				klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, job.RequestedChannel)
			}
		case len(job.Credentials) == 0:
			message := fmt.Sprintf("cluster is still starting (launched %d minutes ago)", time.Now().Sub(job.RequestedAt)/time.Minute)
			_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(message, false))
			if err != nil {
				klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, job.RequestedChannel)
			}
		default:
			comment := fmt.Sprintf(
				"Your cluster is ready, it will be shut down automatically in ~%d minutes.",
				job.ExpiresAt.Sub(time.Now())/time.Minute,
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
		message := fmt.Sprintf("job is running (launched %d minutes ago)", time.Now().Sub(job.RequestedAt)/time.Minute)
		_, _, err := client.PostMessage(job.RequestedChannel, slack.MsgOptionText(message, false))
		if err != nil {
			klog.Warningf("Failed to post the message: %s\nto the channel: %s.", message, job.RequestedChannel)
		}
	default:
		comment := fmt.Sprintf("Your job has started a cluster, it will be shut down when the test ends.")
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

func codeSlice(items []string) []string {
	code := make([]string, 0, len(items))
	for _, item := range items {
		code = append(code, fmt.Sprintf("`%s`", item))
	}
	return code
}

func parseImageInput(input string) ([]string, error) {
	input = strings.TrimSpace(input)
	if len(input) == 0 {
		return nil, nil
	}
	input = stripLinks(input)
	parts := strings.Split(input, ",")
	for _, part := range parts {
		if len(part) == 0 {
			return nil, fmt.Errorf("image inputs must not contain empty items")
		}
	}
	return parts, nil
}

func stripLinks(input string) string {
	var b strings.Builder
	for {
		open := strings.Index(input, "<")
		if open == -1 {
			b.WriteString(input)
			break
		}
		close := strings.Index(input[open:], ">")
		if close == -1 {
			b.WriteString(input)
			break
		}
		pipe := strings.Index(input[open:], "|")
		if pipe == -1 || pipe > close {
			b.WriteString(input[0:open])
			b.WriteString(input[open+1 : open+close])
			input = input[open+close+1:]
			continue
		}
		b.WriteString(input[0:open])
		b.WriteString(input[open+pipe+1 : open+close])
		input = input[open+close+1:]
	}
	return b.String()
}

func parseOptions(options string) (string, string, map[string]string, error) {
	params, err := paramsFromAnnotation(options)
	if err != nil {
		return "", "", nil, fmt.Errorf("options could not be parsed: %v", err)
	}
	var platform, architecture string
	for opt := range params {
		switch {
		case contains(supportedPlatforms, opt):
			if len(platform) > 0 {
				return "", "", nil, fmt.Errorf("you may only specify one platform in options")
			}
			platform = opt
			delete(params, opt)
		case contains(supportedArchitectures, opt):
			if len(architecture) > 0 {
				return "", "", nil, fmt.Errorf("you may only specify one architecture in options")
			}
			architecture = opt
			delete(params, opt)
		case opt == "":
			delete(params, opt)
		case contains(supportedParameters, opt):
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
		case "amd64":
			if rand.Intn(2) == 0 {
				platform = "aws"
			} else {
				platform = "aws-2"
			}
		case "arm64", "multi":
			platform = "aws"
		default:
			return "", "", nil, fmt.Errorf("unknown architecture: %s", architecture)
		}
	}
	return platform, architecture, params, nil
}
