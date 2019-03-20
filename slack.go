package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	prowapiv1 "github.com/openshift/ci-chat-bot/pkg/prow/apiv1"
	"github.com/sbstjn/hanu"
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
	slack, err := hanu.New(b.token)
	if err != nil {
		return err
	}

	manager.SetNotifier(b.jobResponder(slack))

	launch := func(conv hanu.ConversationInterface) {
		if !conv.Message().IsDirectMessage() {
			conv.Reply("this command is only accepted via direct message")
			return
		}

		image := "registry.svc.ci.openshift.org/openshift/origin-release:v4.0"
		if v, err := conv.String("image"); err == nil {
			image = v
		}

		user := conv.Message().User()
		channel := conv.Message().(hanu.Message).Channel

		msg, err := manager.LaunchJobForUser(&JobRequest{
			User:                user,
			InstallImageVersion: image,
			Channel:             channel,
		})
		if err != nil {
			conv.Reply(err.Error())
			return
		}
		conv.Reply(msg)
	}

	testUpgrade := func(conv hanu.ConversationInterface) {
		if !conv.Message().IsDirectMessage() {
			conv.Reply("this command is only accepted via direct message")
			return
		}

		var from string
		if v, err := conv.String("from"); err == nil {
			from = v
		}
		var to string
		if v, err := conv.String("to"); err == nil {
			to = v
		}

		user := conv.Message().User()
		channel := conv.Message().(hanu.Message).Channel

		msg, err := manager.LaunchJobForUser(&JobRequest{
			User:                user,
			InstallImageVersion: from,
			UpgradeImageVersion: to,
			Channel:             channel,
		})
		if err != nil {
			conv.Reply(err.Error())
			return
		}
		conv.Reply(msg)
	}

	slack.Commands = append(slack.Commands,

		hanu.NewCommand(
			"launch",
			"Launch an OpenShift 4.0 cluster on AWS. You will receive a response when the cluster is up for the credentials of the KUBECONFIG file. You must send this as a direct message.",
			launch,
		),

		hanu.NewCommand(
			"launch <image_or_version>",
			"Launch an OpenShift cluster using the provided release image or semantic version from https://openshift-release.svc.ci.openshift.org.",
			launch,
		),

		hanu.NewCommand(
			"list",
			"See who is hogging all the clusters.",
			func(conv hanu.ConversationInterface) {
				conv.Reply(manager.ListJobs(conv.Message().User()))
			},
		),

		hanu.NewCommand(
			"refresh",
			"If the cluster is currently marked as failed, retry fetching its credentials in case of an error.",
			func(conv hanu.ConversationInterface) {
				if !conv.Message().IsDirectMessage() {
					conv.Reply("you must direct message me this request")
					return
				}
				msg, err := manager.SyncJobForUser(conv.Message().User())
				if err != nil {
					conv.Reply(err.Error())
					return
				}
				conv.Reply(msg)
			},
		),

		hanu.NewCommand(
			"auth",
			"Send the credentials for the cluster you most recently requested",
			func(conv hanu.ConversationInterface) {
				if !conv.Message().IsDirectMessage() {
					conv.Reply("you must direct message me this request")
					return
				}
				job, err := manager.GetLaunchJob(conv.Message().User())
				if err != nil {
					conv.Reply(err.Error())
					return
				}
				b.notifyJob(conv, job)
			},
		),

		hanu.NewCommand(
			"test upgrade <from> <to>",
			"Run the upgrade tests between two release images. The arguments may be a pull spec of a release image or tags from https://openshift-release.svc.ci.openshift.org",
			testUpgrade,
		),

		hanu.NewCommand(
			"version",
			"Report the version of the bot",
			func(conv hanu.ConversationInterface) {
				conv.Reply("Thanks for asking! I'm running `%s`", Version)
			},
		),
	)

	log.Printf("ci-chat-bot up and listening to slack")
	return slack.Listen()
}

func (b *Bot) jobResponder(slack *hanu.Bot) func(Job) {
	return func(job Job) {
		if len(job.RequestedChannel) == 0 || len(job.RequestedBy) == 0 {
			log.Printf("no requested channel or user, can't notify")
			return
		}
		if len(job.Credentials) == 0 && len(job.Failure) == 0 {
			log.Printf("no credentials or failure, still pending")
			return
		}
		msg := hanu.Message{
			Type:    "text",
			Channel: job.RequestedChannel,
			ID:      1,
			UserID:  job.RequestedBy,
		}
		conv := hanu.NewConversation(nil, msg, slack.Socket)
		b.notifyJob(conv, &job)
	}
}

func (b *Bot) notifyJob(conv hanu.ConversationInterface, job *Job) {
	if job.Mode == "launch" {
		switch {
		case len(job.Failure) > 0 && len(job.URL) > 0:
			conv.Reply("your cluster failed to launch: %s (see %s for details)", job.Failure, job.URL)
		case len(job.Failure) > 0:
			conv.Reply("your cluster failed to launch: %s", job.Failure)
		case len(job.Credentials) == 0 && len(job.URL) > 0:
			conv.Reply(fmt.Sprintf("cluster is still starting (launched %d minutes ago), see %s for details", time.Now().Sub(job.RequestedAt)/time.Minute, job.URL))
		case len(job.Credentials) == 0:
			conv.Reply(fmt.Sprintf("cluster is still starting (launched %d minutes ago)", time.Now().Sub(job.RequestedAt)/time.Minute))
		default:
			comment := fmt.Sprintf(
				"Your cluster is ready, it will be shut down automatically in ~%d minutes.",
				job.ExpiresAt.Sub(time.Now())/time.Minute,
			)
			if len(job.PasswordSnippet) > 0 {
				comment += "\n" + job.PasswordSnippet
			}
			b.sendKubeconfig(conv, job.Credentials, comment, job.RequestedAt.Format("2006-01-02-150405"))
		}
		return
	}

	if len(job.URL) > 0 {
		switch job.State {
		case prowapiv1.FailureState:
			conv.Reply("your job failed, see %s for details", job.URL)
			return
		case prowapiv1.SuccessState:
			conv.Reply("your job succeeded, see %s for details", job.URL)
			return
		}
	} else {
		switch job.State {
		case prowapiv1.FailureState:
			conv.Reply("your job failed, no details could be retrieved")
			return
		case prowapiv1.SuccessState:
			conv.Reply("your job succeded, but no details could be retrieved")
			return
		}
	}

	switch {
	case len(job.Credentials) == 0 && len(job.URL) > 0:
		conv.Reply(fmt.Sprintf("job is still running (launched %d minutes ago), see %s for details", time.Now().Sub(job.RequestedAt)/time.Minute, job.URL))
	case len(job.Credentials) == 0:
		conv.Reply(fmt.Sprintf("job is still running (launched %d minutes ago)", time.Now().Sub(job.RequestedAt)/time.Minute))
	default:
		comment := fmt.Sprintf("Your job has started a cluster, it will be shut down when the test ends.")
		if len(job.URL) > 0 {
			comment += fmt.Sprintf(" See %s for details.", job.URL)
		}
		if len(job.PasswordSnippet) > 0 {
			comment += "\n" + job.PasswordSnippet
		}
		b.sendKubeconfig(conv, job.Credentials, comment, job.RequestedAt.Format("2006-01-02-150405"))
	}
}

func (b *Bot) sendKubeconfig(conv hanu.ConversationInterface, contents, comment, identifier string) {
	msg := conv.Message().(hanu.Message)
	if len(msg.Channel) == 0 {
		log.Printf("error: no channel in response: %#v", msg)
		return
	}
	v := url.Values{}
	v.Set("content", contents)
	v.Set("token", b.token)
	v.Set("channels", msg.Channel)
	v.Set("filename", fmt.Sprintf("cluster-bot-%s.kubeconfig", identifier))
	v.Set("filetype", "text")
	v.Set("initial_comment", comment)
	req, err := http.NewRequest("POST", "https://slack.com/api/files.upload", strings.NewReader(v.Encode()))
	if err != nil {
		log.Printf("error: unable to send attachment with message: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("error: unable to send attachment with message: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("error: unable to send attachment with message: %d", resp.StatusCode)
		return
	}
	data, _ := ioutil.ReadAll(resp.Body)
	out := &slackResponse{}
	if err := json.Unmarshal(data, out); err != nil {
		log.Printf("error: unable to send attachment with message: %v", err)
		return
	}
	if !out.Ok {
		log.Printf("error: unable to send attachment with message: response was invalid:\n%s", string(data))
		return
	}
	log.Printf("successfully uploaded file to %s", msg.Channel)
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
		log.Printf("warning: Ignoring serialization error and continuing: %v", err)
		return true
	default:
		return false
	}
}
