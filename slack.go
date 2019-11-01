package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nlopes/slack"
	prowapiv1 "github.com/openshift/ci-chat-bot/pkg/prow/apiv1"
	"github.com/shomali11/slacker"
	"k8s.io/klog"
)

type Bot struct {
	token string
}

func NewBot(token string) *Bot {
	return &Bot{
		token: token,
	}
}

func (b *Bot) Start(manager JobManager) error {
	slack := slacker.NewClient(b.token)

	manager.SetNotifier(b.jobResponder(slack))

	slack.DefaultCommand(func(request slacker.Request, response slacker.ResponseWriter) {
		response.Reply("unrecognized command, msg me `help` for a list of all commands")
	})

	slack.Command("launch <image_or_version_or_pr> <options>", &slacker.CommandDefinition{
		Description: fmt.Sprintf(
			"Launch an OpenShift cluster using a known image, version, or PR. You may omit both arguments. Use `nightly` for the latest OCP build, `ci` for the the latest CI build, provide a version directly from any listed on https://openshift-release.svc.ci.openshift.org, a stream name (4.1.0-0.ci, 4.1.0-0.nightly, etc), a major/minor `X.Y` to load the latest stable version for that version (`4.1`), `<org>/<repo>#<pr>` to launch from a PR, or an image for the first argument. Options is a comma-delimited list of variations including platform (%s).",
			strings.Join(codeSlice(supportedPlatforms), ", "),
		),
		Example: "launch openshift/origin#49563 gcp",
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			user := request.Event().User
			channel := request.Event().Channel
			if !isDirectMessage(channel) {
				response.Reply("this command is only accepted via direct message")
				return
			}

			image := request.StringParam("image_or_version_or_pr", "")

			options := request.StringParam("options", "")
			var platform string
			for _, opt := range strings.Split(options, ",") {
				switch {
				case contains(supportedPlatforms, opt):
					if len(platform) > 0 {
						response.Reply("you may only specify one platform in options")
						return
					}
					platform = opt
				case opt == "":
				default:
					response.Reply(fmt.Sprintf("unrecognized option: %s", opt))
					return
				}
			}
			if len(platform) == 0 {
				platform = "aws"
			}

			msg, err := manager.LaunchJobForUser(&JobRequest{
				User:                user,
				InstallImageVersion: image,
				Channel:             channel,
				Platform:            platform,
			})
			if err != nil {
				response.Reply(err.Error())
				return
			}
			response.Reply(msg)
		},
	})

	slack.Command("lookup <image_or_version>", &slacker.CommandDefinition{
		Description: "Get info about a version.",
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			image := request.StringParam("image_or_version", "")

			msg, err := manager.LookupImageOrVersion(image)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			response.Reply(msg)
		},
	})
	slack.Command("list", &slacker.CommandDefinition{
		Description: "See who is hogging all the clusters.",
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			response.Reply(manager.ListJobs(request.Event().User))
		},
	})
	slack.Command("refresh", &slacker.CommandDefinition{
		Description: "If the cluster is currently marked as failed, retry fetching its credentials in case of an error.",
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			user := request.Event().User
			channel := request.Event().Channel
			if !isDirectMessage(channel) {
				response.Reply("you must direct message me this request")
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
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			user := request.Event().User
			channel := request.Event().Channel
			if !isDirectMessage(channel) {
				response.Reply("you must direct message me this request")
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
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			user := request.Event().User
			channel := request.Event().Channel
			if !isDirectMessage(channel) {
				response.Reply("you must direct message me this request")
				return
			}
			job, err := manager.GetLaunchJob(user)
			if err != nil {
				response.Reply(err.Error())
				return
			}
			b.notifyJob(slacker.NewResponse(job.RequestedChannel, slack.Client(), slack.RTM()), job)
		},
	})

	slack.Command("test upgrade <from> <to>", &slacker.CommandDefinition{
		Description: "Run the upgrade tests between two release images. The arguments may be a pull spec of a release image or tags from https://openshift-release.svc.ci.openshift.org",
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			user := request.Event().User
			channel := request.Event().Channel
			if !isDirectMessage(channel) {
				response.Reply("this command is only accepted via direct message")
				return
			}

			from := request.StringParam("from", "")
			to := request.StringParam("to", "")

			msg, err := manager.LaunchJobForUser(&JobRequest{
				User:                user,
				InstallImageVersion: from,
				UpgradeImageVersion: to,
				Channel:             channel,
				Platform:            "aws",
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
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			response.Reply(fmt.Sprintf("Thanks for asking! I'm running `%s` ( https://github.com/openshift/ci-chat-bot )", Version))
		},
	})

	klog.Infof("ci-chat-bot up and listening to slack")
	return slack.Listen(context.Background())
}

func (b *Bot) jobResponder(slack *slacker.Slacker) func(Job) {
	return func(job Job) {
		if len(job.RequestedChannel) == 0 || len(job.RequestedBy) == 0 {
			klog.Infof("no requested channel or user, can't notify")
			return
		}
		if len(job.Credentials) == 0 && len(job.Failure) == 0 {
			klog.Infof("no credentials or failure, still pending")
			return
		}
		b.notifyJob(slacker.NewResponse(job.RequestedChannel, slack.Client(), slack.RTM()), &job)
	}
}

func (b *Bot) notifyJob(response slacker.ResponseWriter, job *Job) {
	if job.Mode == "launch" {
		switch {
		case len(job.Failure) > 0 && len(job.URL) > 0:
			response.Reply(fmt.Sprintf("your cluster failed to launch: %s (see %s for details)", job.Failure, job.URL))
		case len(job.Failure) > 0:
			response.Reply(fmt.Sprintf("your cluster failed to launch: %s", job.Failure))
		case len(job.Credentials) == 0 && len(job.URL) > 0:
			response.Reply(fmt.Sprintf("cluster is still starting (launched %d minutes ago), see %s for details", time.Now().Sub(job.RequestedAt)/time.Minute, job.URL))
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
		case prowapiv1.FailureState:
			response.Reply(fmt.Sprintf("your job failed, see %s for details", job.URL))
			return
		case prowapiv1.SuccessState:
			response.Reply(fmt.Sprintf("your job succeeded, see %s for details", job.URL))
			return
		}
	} else {
		switch job.State {
		case prowapiv1.FailureState:
			response.Reply("your job failed, no details could be retrieved")
			return
		case prowapiv1.SuccessState:
			response.Reply("your job succeded, but no details could be retrieved")
			return
		}
	}

	switch {
	case len(job.Credentials) == 0 && len(job.URL) > 0:
		response.Reply(fmt.Sprintf("job is still running (launched %d minutes ago), see %s for details", time.Now().Sub(job.RequestedAt)/time.Minute, job.URL))
	case len(job.Credentials) == 0:
		response.Reply(fmt.Sprintf("job is still running (launched %d minutes ago)", time.Now().Sub(job.RequestedAt)/time.Minute))
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
	// msg := conv.Message().(hanu.Message)
	// if len(msg.Channel) == 0 {
	// 	klog.Infof("error: no channel in response: %#v", msg)
	// 	return
	// }
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

	// v := url.Values{}
	// v.Set("content", contents)
	// v.Set("token", b.token)
	// v.Set("channels", msg.Channel)
	// v.Set("filename", fmt.Sprintf("cluster-bot-%s.kubeconfig", identifier))
	// v.Set("filetype", "text")
	// v.Set("initial_comment", comment)
	// req, err := http.NewRequest("POST", "https://slack.com/api/files.upload", strings.NewReader(v.Encode()))
	// if err != nil {
	// 	klog.Infof("error: unable to send attachment with message: %v", err)
	// 	return
	// }
	// req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// resp, err := http.DefaultClient.Do(req)
	// if err != nil {
	// 	klog.Infof("error: unable to send attachment with message: %v", err)
	// 	return
	// }
	// defer resp.Body.Close()
	// if resp.StatusCode != 200 {
	// 	klog.Infof("error: unable to send attachment with message: %d", resp.StatusCode)
	// 	return
	// }
	// data, _ := ioutil.ReadAll(resp.Body)
	// out := &slackResponse{}
	// if err := json.Unmarshal(data, out); err != nil {
	// 	klog.Infof("error: unable to send attachment with message: %v", err)
	// 	return
	// }
	// if !out.Ok {
	// 	klog.Infof("error: unable to send attachment with message: response was invalid:\n%s", string(data))
	// 	return
	// }
	// klog.Infof("successfully uploaded file to %s", msg.Channel)
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
