package main

import (
	"context"
	"fmt"
	"github.com/shomali11/proper"
	"io"
	"strings"
	"time"

	"github.com/shomali11/slacker"
	"github.com/slack-go/slack"
	"k8s.io/client-go/pkg/version"
	"k8s.io/klog"
	prowapiv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

const (
	authCommandExample        = "auth"
	buildCommandExample       = "build openshift/origin#49563"
	doneCommandExample        = "done"
	launchCommandExample      = "launch openshift/origin#49563 gcp"
	listCommandExample        = "list"
	lookupCommandExample      = "lookup 4.7.0"
	refreshCommandExample     = "refresh"
	testCommandExample        = "test e2e openshift/origin#49563 gcp"
	testCommandUpgradeExample = "test upgrade 4.7.0 4.7.14 gcp"
	versionCommandExample     = "version"
)

type Bot struct {
	token          string
	workflowConfig *WorkflowConfig
}

func NewBot(token string, workflowConfig *WorkflowConfig) *Bot {
	return &Bot{
		token:          token,
		workflowConfig: workflowConfig,
	}
}

func (b *Bot) initialize(slack *slacker.Slacker, manager JobManager) {
	slack.DefaultCommand(func(request slacker.Request, response slacker.ResponseWriter) {
		response.Reply("unrecognized command, msg me `help` for a list of all commands")
	})

	slack.Command("launch <image_or_version_or_pr> <options>", &slacker.CommandDefinition{
		Description: fmt.Sprintf(
			"Launch an OpenShift cluster using a known image, version, or PR. You may omit both arguments. Use `nightly` for the latest OCP build, `ci` for the the latest CI build, provide a version directly from any listed on https://amd64.ocp.releases.ci.openshift.org, a stream name (4.1.0-0.ci, 4.1.0-0.nightly, etc), a major/minor `X.Y` to load the \"next stable\" version, from nightly, for that version (`4.1`), `<org>/<repo>#<pr>` to launch from a PR, or an image for the first argument. Options is a comma-delimited list of variations including platform (%s) and variant (%s).",
			strings.Join(codeSlice(supportedPlatforms), ", "),
			strings.Join(codeSlice(supportedParameters), ", "),
		),
		Example: launchCommandExample,
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			user, channel, message, parameters, err := processSlackRequest(request)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			req, err := generateJobRequest(user, channel, message, parameters, launchCommandExample)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			msg, err := manager.LaunchJobForUser(req)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			response.Reply(msg)
		},
	})

	slack.Command("lookup <image_or_version_or_pr>", &slacker.CommandDefinition{
		Description: "Get info about a version.",
		Example:     lookupCommandExample,
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			from, err := parseImageInput(request.StringParam("image_or_version_or_pr", ""))
			if err != nil {
				response.Reply(err.Error())
				return
			}
			msg, err := manager.LookupInputs(from)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			response.Reply(msg)
		},
	})

	slack.Command("list", &slacker.CommandDefinition{
		Description: "See who is hogging all the clusters.",
		Example:     listCommandExample,
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			response.Reply(manager.ListJobs(request.Event().User))
		},
	})

	slack.Command("refresh", &slacker.CommandDefinition{
		Description: "If the cluster is currently marked as failed, retry fetching its credentials in case of an error.",
		Example:     refreshCommandExample,
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			user, _, _, _, err := processSlackRequest(request)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			msg, err := manager.SyncJobForUser(user)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			response.Reply(msg)
		},
	})
	slack.Command("done", &slacker.CommandDefinition{
		Description: "Terminate the running cluster",
		Example:     doneCommandExample,
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			user, _, _, _, err := processSlackRequest(request)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			msg, err := manager.TerminateJobForUser(user)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			response.Reply(msg)
		},
	})

	slack.Command("auth", &slacker.CommandDefinition{
		Description: "Send the credentials for the cluster you most recently requested",
		Example:     authCommandExample,
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			user, channel, _, _, err := processSlackRequest(request)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			job, err := manager.GetLaunchJob(user)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			job.RequestedChannel = channel
			b.notifyJob(slacker.NewResponse(request.Event(), slack.Client(), slack.RTM()), job)
		},
	})

	slack.Command("test upgrade <from> <to> <options>", &slacker.CommandDefinition{
		Description: fmt.Sprintf("Run the upgrade tests between two release images. The arguments may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org. You may change the upgrade test by passing `test=NAME` in options with one of %s", strings.Join(codeSlice(supportedUpgradeTests), ", ")),
		Example:     testCommandUpgradeExample,
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			user, channel, message, parameters, err := processSlackRequest(request)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			req, err := generateJobRequest(user, channel, message, parameters, testCommandUpgradeExample)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			msg, err := manager.LaunchJobForUser(req)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			response.Reply(msg)
		},
	})

	slack.Command("test <name> <image_or_version_or_pr> <options>", &slacker.CommandDefinition{
		Description: fmt.Sprintf("Run the requested test suite from an image or release or built PRs. Supported test suites are %s. The from argument may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org. ", strings.Join(codeSlice(supportedTests), ", ")),
		Example:     testCommandExample,
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			user, channel, message, parameters, err := processSlackRequest(request)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			req, err := generateJobRequest(user, channel, message, parameters, testCommandExample)
			if err != nil {
				response.Reply(err.Error())
				return
			}

			msg, err := manager.LaunchJobForUser(req)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			response.Reply(msg)
		},
	})

	slack.Command("build <pullrequest>", &slacker.CommandDefinition{
		Description: "Create a new release image from one or more pull requests. The successful build location will be sent to you when it completes and then preserved for 12 hours.  Example: `build openshift/operator-framework-olm#68,operator-framework/operator-marketplace#396`",
		Example:     buildCommandExample,
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			user, channel, message, parameters, err := processSlackRequest(request)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			req, err := generateJobRequest(user, channel, message, parameters, buildCommandExample)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			msg, err := manager.LaunchJobForUser(req)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			response.Reply(msg)
		},
	})

	slack.Command("workflow-launch <name> <image_or_version_or_pr> <parameters>", &slacker.CommandDefinition{
		Description: fmt.Sprintf("Launch a cluster using the requested workflow from an image or release or built PRs. The from argument may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org. "),
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			user := request.Event().User
			channel := request.Event().Channel
			if !isDirectMessage(channel) {
				response.Reply("this command is only accepted via direct message")
				return
			}

			from, err := parseImageInput(request.StringParam("image_or_version_or_pr", ""))
			if err != nil {
				response.Reply(err.Error())
				return
			}
			if len(from) == 0 {
				response.Reply("you must specify what will be tested")
				return
			}

			name := request.StringParam("name", "")
			if len(name) == 0 {
				response.Reply(fmt.Sprintf("you must specify the name of a workflow: %s", strings.Join(codeSlice(supportedTests), ", ")))
				return
			}
			platform, architecture, err := getPlatformArchFromWorkflowConfig(b.workflowConfig, name)
			if err != nil {
				response.Reply(err.Error())
				return
			}

			params := request.StringParam("parameters", "")
			jobParams, err := buildJobParams(params)
			if err != nil {
				response.Reply(err.Error())
				return
			}

			msg, err := manager.LaunchJobForUser(&JobRequest{
				OriginalMessage: stripLinks(request.Event().Text),
				User:            user,
				Inputs:          [][]string{from},
				Type:            JobTypeWorkflowLaunch,
				Channel:         channel,
				Platform:        platform,
				JobParams:       jobParams,
				Architecture:    architecture,
				WorkflowName:    name,
			})
			if err != nil {
				response.Reply(err.Error())
				return
			}
			response.Reply(msg)
		},
	})

	slack.Command("workflow-upgrade <name> <from_image_or_version_or_pr> <to_image_or_version_or_pr> <parameters>", &slacker.CommandDefinition{
		Description: fmt.Sprintf("Run a custom upgrade using the requested workflow from an image or release or built PRs to a specified version/image/pr from https://amd64.ocp.releases.ci.openshift.org. "),
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			user := request.Event().User
			channel := request.Event().Channel
			if !isDirectMessage(channel) {
				response.Reply("this command is only accepted via direct message")
				return
			}

			from, err := parseImageInput(request.StringParam("from_image_or_version_or_pr", ""))
			if err != nil {
				response.Reply(err.Error())
				return
			}
			if len(from) == 0 {
				response.Reply("you must specify initial release")
				return
			}

			to, err := parseImageInput(request.StringParam("to_image_or_version_or_pr", ""))
			if err != nil {
				response.Reply(err.Error())
				return
			}
			if len(to) == 0 {
				response.Reply("you must specify the target release")
				return
			}

			name := request.StringParam("name", "")
			if len(name) == 0 {
				response.Reply(fmt.Sprintf("you must specify the name of a workflow: %s", strings.Join(codeSlice(supportedTests), ", ")))
				return
			}
			platform, architecture, err := getPlatformArchFromWorkflowConfig(b.workflowConfig, name)
			if err != nil {
				response.Reply(err.Error())
				return
			}

			params := request.StringParam("parameters", "")
			jobParams, err := buildJobParams(params)
			if err != nil {
				response.Reply(err.Error())
				return
			}

			msg, err := manager.LaunchJobForUser(&JobRequest{
				OriginalMessage: stripLinks(request.Event().Text),
				User:            user,
				Inputs:          [][]string{from, to},
				Type:            JobTypeWorkflowUpgrade,
				Channel:         channel,
				Platform:        platform,
				JobParams:       jobParams,
				Architecture:    architecture,
				WorkflowName:    name,
			})
			if err != nil {
				response.Reply(err.Error())
				return
			}
			response.Reply(msg)
		},
	})

	slack.Command("version", &slacker.CommandDefinition{
		Description: "Report the version of the bot",
		Example:     versionCommandExample,
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			response.Reply(fmt.Sprintf("Running `%s` from https://github.com/openshift/ci-chat-bot", version.Get().String()))
		},
	})
}

func processSlackRequest(request slacker.Request) (user, channel, message string, parameters *proper.Properties, error error) {
	user = request.Event().User
	channel = request.Event().Channel
	message = stripLinks(request.Event().Text)
	if !isDirectMessage(channel) {
		return "", "", "", nil, fmt.Errorf("this command is only accepted via direct message")
	}

	return user, channel, message, request.Properties(), nil
}

func generateJobRequest(user, channel, message string, parameters *proper.Properties, example string) (*JobRequest, error) {
	switch example {
	case buildCommandExample:
		from, err := parseImageInput(parameters.StringParam("pullrequest", ""))
		if err != nil {
			return nil, err
		}
		if len(from) == 0 {
			return nil, fmt.Errorf("you must specify at least one pull request to build a release image")
		}

		platform, architecture, params, err := parseOptions(parameters.StringParam("options", ""))
		if err != nil {
			return nil, err
		}

		return &JobRequest{
			OriginalMessage: message,
			User:            user,
			Inputs:          [][]string{from},
			Type:            JobTypeBuild,
			Channel:         channel,
			Platform:        platform,
			JobParams:       params,
			Architecture:    architecture,
		}, nil

	case launchCommandExample:
		from, err := parseImageInput(parameters.StringParam("image_or_version_or_pr", ""))
		if err != nil {
			return nil, err
		}
		var inputs [][]string
		if len(from) > 0 {
			inputs = [][]string{from}
		}

		platform, architecture, params, err := parseOptions(parameters.StringParam("options", ""))
		if err != nil {
			return nil, err
		}
		if len(params["test"]) > 0 {
			return nil, fmt.Errorf("test arguments may not be passed from the launch command")
		}

		return &JobRequest{
			OriginalMessage: message,
			User:            user,
			Inputs:          inputs,
			Type:            JobTypeInstall,
			Channel:         channel,
			Platform:        platform,
			JobParams:       params,
			Architecture:    architecture,
		}, nil

	case testCommandExample:
		from, err := parseImageInput(parameters.StringParam("image_or_version_or_pr", ""))
		if err != nil {
			return nil, err
		}
		if len(from) == 0 {
			return nil, fmt.Errorf("you must specify what will be tested")
		}

		test := parameters.StringParam("name", "")
		if len(test) == 0 {
			return nil, fmt.Errorf("you must specify the name of a test: %s", strings.Join(codeSlice(supportedTests), ", "))
		}
		switch {
		case contains(supportedTests, test):
		default:
			return nil, fmt.Errorf("warning: You are using a custom test name, may not be supported for all platforms: %s", strings.Join(codeSlice(supportedTests), ", "))
		}

		platform, architecture, params, err := parseOptions(parameters.StringParam("options", ""))
		if err != nil {
			return nil, err
		}

		params["test"] = test
		if strings.Contains(params["test"], "-upgrade") {
			return nil, fmt.Errorf("upgrade type tests require the 'test upgrade' command")
		}

		return &JobRequest{
			OriginalMessage: message,
			User:            user,
			Inputs:          [][]string{from},
			Type:            JobTypeTest,
			Channel:         channel,
			Platform:        platform,
			JobParams:       params,
			Architecture:    architecture,
		}, nil

	case testCommandUpgradeExample:
		from, err := parseImageInput(parameters.StringParam("from", ""))
		if err != nil {
			return nil, err
		}
		if len(from) == 0 {
			return nil, fmt.Errorf("you must specify an image to upgrade from and to")
		}
		to, err := parseImageInput(parameters.StringParam("to", ""))
		if err != nil {
			return nil, err
		}
		// default to to from
		if len(to) == 0 {
			to = from
		}

		platform, architecture, params, err := parseOptions(parameters.StringParam("options", ""))
		if err != nil {
			return nil, err
		}

		if v := params["test"]; len(v) == 0 {
			params["test"] = "e2e-upgrade"
		}
		if !strings.Contains(params["test"], "-upgrade") {
			return nil, fmt.Errorf("only upgrade type tests may be run from this command")
		}

		return &JobRequest{
			OriginalMessage: message,
			User:            user,
			Inputs:          [][]string{from, to},
			Type:            JobTypeUpgrade,
			Channel:         channel,
			Platform:        platform,
			JobParams:       params,
			Architecture:    architecture,
		}, nil
	}
	return nil, fmt.Errorf("unable to generate JobRequest")
}

func (b *Bot) ProcessCommand(manager JobManager, command, output string) error {
	client := &slacker.Slacker{}
	b.initialize(client, manager)

	for _, cmd := range client.BotCommands() {
		parameters, isMatch := cmd.Match(command)
		if !isMatch {
			continue
		}

		definition := cmd.Definition()

		switch definition.Example {
		case buildCommandExample, launchCommandExample, testCommandExample, testCommandUpgradeExample:
			req, err := generateJobRequest("cli-generated-prowjob", "", command, parameters, definition.Example)
			if err != nil {
				return err
			}
			err = manager.GenerateProwJobForCli(req, output)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("the specified command: %q does not generate a prow job", command)
		}
		// Don't continue after a match (i.e "test" and "test upgrade" match twice)
		return nil
	}
	return fmt.Errorf("unsupported command: %q", command)
}

func (b *Bot) Listen(manager JobManager) error {
	client := slacker.NewClient(b.token)

	manager.SetNotifier(b.jobResponder(client))

	b.initialize(client, manager)

	klog.Infof("ci-chat-bot up and listening to slack")
	return client.Listen(context.Background())
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

func (b *Bot) jobResponder(s *slacker.Slacker) func(Job) {
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
		b.notifyJob(slacker.NewResponse(&slack.MessageEvent{Msg: slack.Msg{Channel: job.RequestedChannel}}, s.Client(), s.RTM()), &job)
	}
}

func (b *Bot) notifyJob(response slacker.ResponseWriter, job *Job) {
	switch job.Mode {
	case JobTypeLaunch, JobTypeWorkflowLaunch:
		if job.LegacyConfig {
			response.Reply(fmt.Sprintf("WARNING: using legacy template based job for this cluster. This is unsupported and the cluster may not install as expected. Contact #forum-crt for more information."))
		}
		switch {
		case len(job.Failure) > 0 && len(job.URL) > 0:
			response.Reply(fmt.Sprintf("your cluster failed to launch: %s (<%s|logs>)", job.Failure, job.URL))
		case len(job.Failure) > 0:
			response.Reply(fmt.Sprintf("your cluster failed to launch: %s", job.Failure))
		case len(job.Credentials) == 0 && len(job.URL) > 0:
			response.Reply(fmt.Sprintf("cluster is still starting (launched %d minutes ago, <%s|logs>)", time.Now().Sub(job.RequestedAt)/time.Minute, job.URL))
		case len(job.Credentials) == 0:
			response.Reply(fmt.Sprintf("cluster is still starting (launched %d minutes ago)", time.Now().Sub(job.RequestedAt)/time.Minute))
		default:
			comment := fmt.Sprintf(
				"Your cluster is ready, it will be shut down automatically in ~%d minutes.",
				job.ExpiresAt.Sub(time.Now())/time.Minute,
			)
			if len(job.PasswordSnippet) > 0 {
				comment += "\n" + job.PasswordSnippet
			}
			b.sendKubeconfig(response, job.RequestedChannel, job.Credentials, comment, job.RequestedAt.Format("2006-01-02-150405"))
		}
		return
	}

	if len(job.URL) > 0 {
		switch job.State {
		case prowapiv1.FailureState, prowapiv1.AbortedState, prowapiv1.ErrorState:
			response.Reply(fmt.Sprintf("job <%s|%s> failed", job.URL, job.OriginalMessage))
			return
		case prowapiv1.SuccessState:
			response.Reply(fmt.Sprintf("job <%s|%s> succeeded", job.URL, job.OriginalMessage))
			return
		}
	} else {
		switch job.State {
		case prowapiv1.FailureState, prowapiv1.AbortedState, prowapiv1.ErrorState:
			response.Reply(fmt.Sprintf("job %s failed, but no details could be retrieved", job.OriginalMessage))
			return
		case prowapiv1.SuccessState:
			response.Reply(fmt.Sprintf("job %s succeded, but no details could be retrieved", job.OriginalMessage))
			return
		}
	}

	switch {
	case len(job.Credentials) == 0 && len(job.URL) > 0:
		if len(job.OriginalMessage) > 0 {
			response.Reply(fmt.Sprintf("job <%s|%s> is running", job.URL, job.OriginalMessage))
		} else {
			response.Reply(fmt.Sprintf("job is running, see %s for details", job.URL))
		}
	case len(job.Credentials) == 0:
		response.Reply(fmt.Sprintf("job is running (launched %d minutes ago)", time.Now().Sub(job.RequestedAt)/time.Minute))
	default:
		comment := fmt.Sprintf("Your job has started a cluster, it will be shut down when the test ends.")
		if len(job.URL) > 0 {
			comment += fmt.Sprintf(" See %s for details.", job.URL)
		}
		if len(job.PasswordSnippet) > 0 {
			comment += "\n" + job.PasswordSnippet
		}
		b.sendKubeconfig(response, job.RequestedChannel, job.Credentials, comment, job.RequestedAt.Format("2006-01-02-150405"))
	}
}

func (b *Bot) sendKubeconfig(response slacker.ResponseWriter, channel, contents, comment, identifier string) {
	_, err := response.Client().UploadFile(slack.FileUploadParameters{
		Content:        contents,
		Channels:       []string{channel},
		Filename:       fmt.Sprintf("cluster-bot-%s.kubeconfig", identifier),
		Filetype:       "text",
		InitialComment: comment,
	})
	if err != nil {
		klog.Infof("error: unable to send attachment with message: %v", err)
		return
	}
	klog.Infof("successfully uploaded file to %s", channel)
}

type slackResponse struct {
	Ok    bool
	Error string
}

func isRetriable(err error) bool {
	// there are several conditions that result from closing the connection on our side
	switch {
	case err == nil,
		err == io.EOF,
		strings.Contains(err.Error(), "use of closed network connection"):
		return true
	case strings.Contains(err.Error(), "cannot unmarshal object into Go struct field"):
		// this could be a legitimate error, so log it to ensure we can debug
		klog.Infof("warning: Ignoring serialization error and continuing: %v", err)
		return true
	default:
		return false
	}
}

func isDirectMessage(channel string) bool {
	return strings.HasPrefix(channel, "D")
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
	if len(platform) == 0 {
		platform = "gcp"
	}
	if len(architecture) == 0 {
		architecture = "amd64"
	}
	return platform, architecture, params, nil
}
