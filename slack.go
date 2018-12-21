package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

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

func (b *Bot) Start(manager ClusterManager) error {
	slack, err := hanu.New(b.token)
	if err != nil {
		return err
	}

	manager.SetNotifier(b.clusterResponder(slack))

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

		msg, err := manager.LaunchClusterForUser(&ClusterRequest{
			User:         user,
			ReleaseImage: image,
			Channel:      channel,
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
			"launch <image>",
			"Launch an OpenShift 4.0 cluster on AWS and specify the release image. Will still use the latest installer.",
			launch,
		),

		hanu.NewCommand(
			"list",
			"See who is hogging all the clusters.",
			func(conv hanu.ConversationInterface) {
				conv.Reply(manager.ListClusters())
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
				msg, err := manager.SyncClusterForUser(conv.Message().User())
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
				cluster, err := manager.GetCluster(conv.Message().User())
				if err != nil {
					conv.Reply(err.Error())
					return
				}
				b.notifyCluster(conv, cluster)
			},
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

func (b *Bot) clusterResponder(slack *hanu.Bot) func(Cluster) {
	return func(cluster Cluster) {
		if len(cluster.RequestedChannel) == 0 || len(cluster.RequestedBy) == 0 {
			log.Printf("no requested channel or user, can't notify")
			return
		}
		if len(cluster.Credentials) == 0 && len(cluster.Failure) == 0 {
			log.Printf("no credentials or failure, still pending")
			return
		}
		msg := hanu.Message{
			Type:    "text",
			Channel: cluster.RequestedChannel,
			ID:      1,
			UserID:  cluster.RequestedBy,
		}
		conv := hanu.NewConversation(nil, msg, slack.Socket)
		b.notifyCluster(conv, &cluster)
	}
}

func (b *Bot) notifyCluster(conv hanu.ConversationInterface, cluster *Cluster) {
	switch {
	case len(cluster.Failure) > 0:
		conv.Reply("your cluster failed to launch: %s", cluster.Failure)
	case len(cluster.Credentials) == 0:
		conv.Reply(fmt.Sprintf("cluster is still starting (launched %d minutes ago)", time.Now().Sub(cluster.RequestedAt)/time.Minute))
	default:
		b.sendKubeconfig(conv, cluster.Credentials, cluster.RequestedAt.Format("2006-01-02-150405"), cluster.ExpiresAt)
	}
}

func (b *Bot) sendKubeconfig(conv hanu.ConversationInterface, contents, identifier string, expires time.Time) {
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
	v.Set("initial_comment", fmt.Sprintf(
		"Cluster is now available and your credentials are attached.\nThe cluster will be shut down automatically in ~%d minutes",
		expires.Sub(time.Now())/time.Minute,
	))
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
