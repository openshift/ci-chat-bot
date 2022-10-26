package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	util "github.com/openshift/ci-chat-bot/pkg"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	slackCommandParser "github.com/openshift/ci-chat-bot/pkg/slack"
	eventhandler "github.com/openshift/ci-chat-bot/pkg/slack/events"
	eventrouter "github.com/openshift/ci-chat-bot/pkg/slack/events/router"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"io/ioutil"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/pkg/version"
	"k8s.io/klog"
	prowapiv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/pjutil/pprof"
	"k8s.io/test-infra/prow/simplifypath"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	MetricsPort           = 9090
	PProfPort             = 6060
	HealthPort            = 8081
	MemoryProfileInterval = 30 * time.Second
)

var (
	promMetrics = metrics.NewMetrics("cluster_bot")
)

type Bot struct {
	botToken         string
	botSigningSecret string
	gracePeriod      time.Duration
	port             int
	userID           string
	workflowConfig   *manager.WorkflowConfig
}

func (b *Bot) jobResponder(s *slack.Client) func(manager.Job) {
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
		botToken:         botToken,
		botSigningSecret: botSigningSecret,
		gracePeriod:      graceperiod,
		port:             port,
		userID:           "unknown",
		workflowConfig:   workflowConfig,
	}
}

func (b *Bot) WorkflowLaunch(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *slackCommandParser.Properties) string {
	userName := GetUserName(client, event.User)
	from, err := parseImageInput(properties.StringParam("image_or_version_or_pr", ""))
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
	platform, architecture, err := GetPlatformArchFromWorkflowConfig(b.workflowConfig, name)
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

func (b *Bot) WorkflowUpgrade(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *slackCommandParser.Properties) string {
	userName := GetUserName(client, event.User)
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
		return fmt.Sprintf("you must specify the name of a workflow: %s", strings.Join(CodeSlice(manager.SupportedTests), ", "))
	}
	platform, architecture, err := GetPlatformArchFromWorkflowConfig(b.workflowConfig, name)
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

func (b *Bot) SupportedCommands() []slackCommandParser.BotCommand {
	return []slackCommandParser.BotCommand{
		slackCommandParser.NewBotCommand("launch <image_or_version_or_pr> <options>", &slackCommandParser.CommandDefinition{
			Description: fmt.Sprintf("Launch an OpenShift cluster using a known image, version, or PR. You may omit both arguments. Use `nightly` for the latest OCP build, `ci` for the the latest CI build, provide a version directly from any listed on https://amd64.ocp.releases.ci.openshift.org, a stream name (4.1.0-0.ci, 4.1.0-0.nightly, etc), a major/minor `X.Y` to load the \"next stable\" version, from nightly, for that version (`4.1`), `<org>/<repo>#<pr>` to launch from a PR, or an image for the first argument. Options is a comma-delimited list of variations including platform (%s) and variant (%s).",
				strings.Join(CodeSlice(manager.SupportedPlatforms), ", "),
				strings.Join(CodeSlice(manager.SupportedParameters), ", ")),
			Example: "launch openshift/origin#49563 gcp",
			Handler: LaunchCluster,
		}),
		slackCommandParser.NewBotCommand("list", &slackCommandParser.CommandDefinition{
			Description: "See who is hogging all the clusters.",
			Handler:     List,
		}),
		slackCommandParser.NewBotCommand("done", &slackCommandParser.CommandDefinition{
			Description: "Terminate the running cluster",
			Handler:     Done,
		}),
		slackCommandParser.NewBotCommand("refresh", &slackCommandParser.CommandDefinition{
			Description: "If the cluster is currently marked as failed, retry fetching its credentials in case of an error.",
			Handler:     Refresh,
		}),
		slackCommandParser.NewBotCommand("auth", &slackCommandParser.CommandDefinition{
			Description: "Send the credentials for the cluster you most recently requested",
			Handler:     Auth,
		}),
		slackCommandParser.NewBotCommand("test upgrade <from> <to> <options>", &slackCommandParser.CommandDefinition{
			Description: fmt.Sprintf("Run the upgrade tests between two release images. The arguments may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org. You may change the upgrade test by passing `test=NAME` in options with one of %s", strings.Join(CodeSlice(manager.SupportedUpgradeTests), ", ")),
			Handler:     TestUpgrade,
		}),
		slackCommandParser.NewBotCommand("test <name> <image_or_version_or_pr> <options>", &slackCommandParser.CommandDefinition{
			Description: fmt.Sprintf("Run the requested test suite from an image or release or built PRs. Supported test suites are %s. The from argument may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org. ", strings.Join(CodeSlice(manager.SupportedTests), ", ")),
			Handler:     Test,
		}),
		slackCommandParser.NewBotCommand("build <pullrequest>", &slackCommandParser.CommandDefinition{
			Description: "Create a new release image from one or more pull requests. The successful build location will be sent to you when it completes and then preserved for 12 hours.  Example: `build openshift/operator-framework-olm#68,operator-framework/operator-marketplace#396`. To obtain a pull secret use `oc registry login --to /path/to/pull-secret` after using `oc login` to login to the relevant CI cluster.",
			Handler:     Build,
		}),
		slackCommandParser.NewBotCommand("workflow-launch <name> <image_or_version_or_pr> <parameters>", &slackCommandParser.CommandDefinition{
			Description: "Launch a cluster using the requested workflow from an image or release or built PRs. The from argument may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org.",
			Handler:     b.WorkflowLaunch,
		}),
		slackCommandParser.NewBotCommand("workflow-upgrade <name> <from_image_or_version_or_pr> <to_image_or_version_or_pr> <parameters>", &slackCommandParser.CommandDefinition{
			Description: "Run a custom upgrade using the requested workflow from an image or release or built PRs to a specified version/image/pr from https://amd64.ocp.releases.ci.openshift.org. ",
			Handler:     b.WorkflowUpgrade,
		}),
		slackCommandParser.NewBotCommand("version", &slackCommandParser.CommandDefinition{
			Description: "Report the version of the bot",
			Handler:     Version,
		}),
		slackCommandParser.NewBotCommand("lookup <image_or_version_or_pr> <architecture>", &slackCommandParser.CommandDefinition{
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

func LaunchCluster(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *slackCommandParser.Properties) string {
	userName := GetUserName(client, event.User)
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

func Lookup(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *slackCommandParser.Properties) string {
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
	if !sets.NewString(manager.SupportedArchitectures...).Has(architecture) {
		return fmt.Sprintf("Error: %s is an invalid architecture. Supported architectures: %v", architecture, manager.SupportedArchitectures)
	}
	msg, err := jobManager.LookupInputs(from, architecture)
	if err != nil {
		return err.Error()
	}
	return msg
}

func List(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *slackCommandParser.Properties) string {
	return jobManager.ListJobs(event.User)
}

func Done(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *slackCommandParser.Properties) string {
	msg, err := jobManager.TerminateJobForUser(event.User)
	if err != nil {
		return err.Error()
	}
	return msg
}

func Refresh(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *slackCommandParser.Properties) string {
	msg, err := jobManager.SyncJobForUser(event.User)
	if err != nil {
		return err.Error()
	}
	return msg
}

func Auth(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *slackCommandParser.Properties) string {
	job, err := jobManager.GetLaunchJob(event.User)
	if err != nil {
		return err.Error()
	}
	job.RequestedChannel = event.Channel
	NotifyJob(client, job)
	return " "
}

func TestUpgrade(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *slackCommandParser.Properties) string {
	userName := GetUserName(client, event.User)
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

func Test(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *slackCommandParser.Properties) string {
	userName := GetUserName(client, event.User)
	from, err := parseImageInput(properties.StringParam("image_or_version_or_pr", ""))
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
	case manager.Contains(manager.SupportedTests, test):
	default:
		return fmt.Sprintf("warning: You are using a custom test name, may not be supported for all platforms: %s", strings.Join(CodeSlice(manager.SupportedTests), ", "))
	}

	platform, architecture, params, err := parseOptions(properties.StringParam("options", ""))
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

func Build(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *slackCommandParser.Properties) string {
	userName := GetUserName(client, event.User)
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

func Version(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *slackCommandParser.Properties) string {
	return fmt.Sprintf("Running `%s` from https://github.com/openshift/ci-chat-bot", version.Get().String())
}

func l(fragment string, children ...simplifypath.Node) simplifypath.Node {
	return simplifypath.L(fragment, children...)
}

func (b *Bot) Start(jobManager manager.JobManager) {
	slackClient := slack.New(b.botToken)
	jobManager.SetNotifier(b.jobResponder(slackClient))

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
	mux.Handle("/slack/events-endpoint", handler(handleEvent(b.botSigningSecret, eventrouter.ForEvents(slackClient, jobManager, b.SupportedCommands()))))
	server := &http.Server{Addr: ":" + strconv.Itoa(b.port), Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	health.ServeReady()

	interrupts.ListenAndServe(server, b.gracePeriod)
	interrupts.WaitForGracefulShutdown()

	klog.Infof("ci-chat-bot up and listening to slack")
}

func handleEvent(signingSecret string, handler eventhandler.Handler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		logger := logrus.WithField("api", "events")
		logger.Debug("Got an event payload.")
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
			if err := handler.Handle(&event, logger); err != nil {
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
		return "", "", fmt.Errorf("Workflow %s not in workflow list ( https://github.com/openshift/release/blob/master/core-services/ci-chat-bot/workflows-config.yaml ). Please add %s to the workflows list before retrying this command, or use a workflow from: %s", name, name, strings.Join(workflows, ", "))
	} else {
		platform = workflow.Platform
		if workflow.Architecture != "" {
			if manager.Contains(manager.SupportedArchitectures, workflow.Architecture) {
				architecture = workflow.Architecture
			} else {
				return "", "", fmt.Errorf("Architecture %s not supported by cluster-bot", workflow.Architecture)
			}
		}
	}
	return platform, architecture, nil
}

func BuildJobParams(params string) (map[string]string, error) {
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

func NotifyJob(client *slack.Client, job *manager.Job) {
	switch job.Mode {
	case manager.JobTypeLaunch, manager.JobTypeWorkflowLaunch:
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

func CodeSlice(items []string) []string {
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
	input = util.StripLinks(input)
	parts := strings.Split(input, ",")
	for _, part := range parts {
		if len(part) == 0 {
			return nil, fmt.Errorf("image inputs must not contain empty items")
		}
	}
	return parts, nil
}

func parseOptions(options string) (string, string, map[string]string, error) {
	params, err := manager.ParamsFromAnnotation(options)
	if err != nil {
		return "", "", nil, fmt.Errorf("options could not be parsed: %v", err)
	}
	var platform, architecture string
	for opt := range params {
		switch {
		case manager.Contains(manager.SupportedPlatforms, opt):
			if len(platform) > 0 {
				return "", "", nil, fmt.Errorf("you may only specify one platform in options")
			}
			platform = opt
			delete(params, opt)
		case manager.Contains(manager.SupportedArchitectures, opt):
			if len(architecture) > 0 {
				return "", "", nil, fmt.Errorf("you may only specify one architecture in options")
			}
			architecture = opt
			delete(params, opt)
		case opt == "":
			delete(params, opt)
		case manager.Contains(manager.SupportedParameters, opt):
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
