package apphome

import (
	"fmt"
	"log"
	"time"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/events"
	"github.com/openshift/ci-chat-bot/pkg/utils"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	prowapiv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
)

type publishView interface {
	PublishView(userID string, view slack.HomeTabViewRequest, hash string) (*slack.ViewResponse, error)
}

func Handler(client publishView, jobManager manager.JobManager) events.PartialHandler {
	return events.PartialHandlerFunc("workflow-execution-event", func(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (handled bool, err error) {
		if callback.Type != slackevents.CallbackEvent {
			return false, nil
		}
		event, ok := callback.InnerEvent.Data.(*slackevents.AppHomeOpenedEvent)
		if !ok {
			return false, nil
		}
		resp, err := client.PublishView(event.User, View(jobManager, event.User), "")
		if err != nil {
			log.Printf("Error: %v; %+v", err, resp.ResponseMetadata)
			return false, err
		}
		return true, err
	})
}

func View(jobManager manager.JobManager, user string) slack.HomeTabViewRequest {
	view := &slack.HomeTabViewRequest{
		Type: "home",
	}
	// Expertimental Warning
	addBlockToView(view, slack.NewHeaderBlock(slack.NewTextBlockObject(slack.PlainTextType, ":warning: Experimental :warning:", true, false)))
	addBlockToView(view, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, " The App Home doesn't currently support all of Cluster Bot's functionality and may have bugs. Switch to the Messages tab above to use the traditional message commands.", false, false), nil, nil))
	// Divider
	addBlockToView(view, slack.NewDividerBlock())
	// Intro
	addBlockToView(view, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, "*Cluster Bot gives users the ability to launch and test OpenShift Clusters from any existing custom built releases*\n\n<https://github.com/openshift/ci-chat-bot/blob/master/docs/FAQ.md|Frequently Asked Questions>\n<https://amd64.ocp.releases.ci.openshift.org/|OpenShift Releases>", false, false), nil, nil))
	// CI Cluster Header
	addBlockToView(view, slack.NewHeaderBlock(slack.NewTextBlockObject(slack.PlainTextType, "CI Clusters", true, false)))
	// CI Cluster Subtext
	addBlockToView(view, slack.NewSectionBlock(slack.NewTextBlockObject(slack.PlainTextType, "Clusters launched with this method run in the CI environment managed by the Test Platform team. It is the same environment used for GitHub Pull Requests and is very flexible.", false, false), nil, nil))
	// Divider
	addBlockToView(view, slack.NewDividerBlock())
	userJob := jobManager.GetUserCluster(user)
	if userJob == nil || userJob.State == prowapiv1.SuccessState || userJob.Complete {
		// CI Cluster Launch
		addBlockToView(view, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, "Launch an OpenShift cluster using a known image, version, or PR", false, false), nil,
			slack.NewAccessory(slack.NewButtonBlockElement("launch", "launch", slack.NewTextBlockObject(slack.PlainTextType, "Launch", true, false)).WithStyle(slack.StylePrimary)),
		))
	} else {
		// Current Cluster Info
		addBlockToView(view, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType,
			fmt.Sprintf("*As of %s UTC, you have an active %s cluster that will expire at %s UTC. Please delete the cluster when you're done and use the below buttons to manage the cluster. Change to another view to update this text.*",
				time.Now().Format(time.Kitchen), userJob.Platform, userJob.ExpiresAt.Format(time.Kitchen)), false, false), nil, nil))
		// Divider
		addBlockToView(view, slack.NewDividerBlock())
		// Done
		addBlockToView(view, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, "Terminate the running cluster", false, false), nil,
			slack.NewAccessory(slack.NewButtonBlockElement("done", "done", slack.NewTextBlockObject(slack.PlainTextType, "Done", true, false)).WithStyle(slack.StyleDanger)),
		))
		// Refresh
		addBlockToView(view, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, "If the cluster is currently marked as failed, retry fetching its credentials in case of an error", false, false), nil,
			slack.NewAccessory(slack.NewButtonBlockElement("refresh", "refresh", slack.NewTextBlockObject(slack.PlainTextType, "Refresh", true, false)).WithStyle(slack.StylePrimary)),
		))
		// Auth
		addBlockToView(view, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, "Send the credentials for the cluster you most recently requested", false, false), nil,
			slack.NewAccessory(slack.NewButtonBlockElement("auth", "auth", slack.NewTextBlockObject(slack.PlainTextType, "Auth", true, false)).WithStyle(slack.StylePrimary)),
		))
	}
	// List
	addBlockToView(view, slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType, "See who is hogging all the clusters", false, false), nil,
		slack.NewAccessory(slack.NewButtonBlockElement("list", "list", slack.NewTextBlockObject(slack.PlainTextType, "List", true, false)).WithStyle(slack.StylePrimary)),
	))
	// MCE Header
	addBlockToView(view, slack.NewHeaderBlock(slack.NewTextBlockObject(slack.PlainTextType, "MCE Clusters", true, false)))
	// MCE Cluster Subtext
	addBlockToView(view, slack.NewSectionBlock(slack.NewTextBlockObject(slack.PlainTextType, "Clusters created with this method are created via Multicluster Engine (MCE) running on a cluster managed by CRT. These clusters have a longer configurable duration and can be run on AWS or GCP. However, they currently have no other configuration options and have a much lower concurrent user limit.", false, false), nil, nil))
	// Divider
	addBlockToView(view, slack.NewDividerBlock())
	// for now, just assume a maximum of one managed cluster per user
	managed, _, _, _, _ := jobManager.GetManagedClustersForUser(user)
	if len(managed) == 0 {
		// MCE Create
		addBlockToView(view, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, "Launch an MCE Cluster", false, false), nil,
			slack.NewAccessory(slack.NewButtonBlockElement("mce_create", "mce_create", slack.NewTextBlockObject(slack.PlainTextType, "Launch", true, false)).WithStyle(slack.StylePrimary)),
		))
	} else {
		var cluster *clusterv1.ManagedCluster
		for _, value := range managed {
			cluster = value
		}
		expiryTime, _ := time.Parse(time.RFC3339, cluster.Annotations[utils.ExpiryTimeTag])
		// Current MCE Cluster Info
		addBlockToView(view, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType,
			fmt.Sprintf("*As of %s UTC, you have an active %s cluster that will expire at %s. Please delete the cluster when you're done and use the below buttons to manage the cluster. Change to another view to update this text.*",
				time.Now().Format(time.Kitchen), cluster.Labels["Cloud"], expiryTime.Format(time.Kitchen+" UTC on Mon Jan 2")), false, false), nil, nil))
		// Divider
		addBlockToView(view, slack.NewDividerBlock())
		// MCE Delete
		addBlockToView(view, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, "Terminate MCE Cluster", false, false), nil,
			slack.NewAccessory(slack.NewButtonBlockElement("mce_delete", "mce_delete", slack.NewTextBlockObject(slack.PlainTextType, "Delete", true, false)).WithStyle(slack.StyleDanger)),
		))
		// MCE Auth
		addBlockToView(view, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, "Retreive MCE Cluster Auth", false, false), nil,
			slack.NewAccessory(slack.NewButtonBlockElement("mce_auth", "mce_auth", slack.NewTextBlockObject(slack.PlainTextType, "Auth", true, false)).WithStyle(slack.StylePrimary)),
		))
	}
	// MCE List
	addBlockToView(view, slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType, "List All Running MCE Clusters", false, false), nil,
		slack.NewAccessory(slack.NewButtonBlockElement("mce_list", "mce_list", slack.NewTextBlockObject(slack.PlainTextType, "List", true, false)).WithStyle(slack.StylePrimary)),
	))
	return *view
}

func addBlockToView(view *slack.HomeTabViewRequest, block slack.Block) {
	view.Blocks.BlockSet = append(view.Blocks.BlockSet, block)
}

/*
const homeJson = `
{
  "type":"home",
 	"blocks": [
		{
			"type": "section",
			"text": {
				"type": "mrkdwn",
				"text": "*Cluster Bot gives users the ability to launch and test OpenShift Clusters from any existing custom built releases*\n\n<https://github.com/openshift/ci-chat-bot/blob/master/docs/FAQ.md|Frequently Asked Questions>\n<https://amd64.ocp.releases.ci.openshift.org/|OpenShift Releases>"
			}
		},
		{
			"type": "header",
			"text": {
				"type": "plain_text",
				"text": "Launch a Cluster",
				"emoji": true
			}
		},
		{
			"type": "divider"
		},
		{
			"type": "section",
			"text": {
				"type": "mrkdwn",
				"text": "Launch an OpenShift cluster using a known image, version, or PR"
			},
			"accessory": {
				"type": "button",
				"text": {
					"type": "plain_text",
					"text": "Launch",
					"emoji": true
				},
				"value": "launch",
				"action_id": "launch",
				"style": "primary"
			}
		},
		{
			"type": "header",
			"text": {
				"type": "plain_text",
				"text": "Helpers",
				"emoji": true
			}
		},
		{
			"type": "divider"
		},
		{
			"type": "section",
			"text": {
				"type": "mrkdwn",
				"text": "See who is hogging all the clusters"
			},
			"accessory": {
				"type": "button",
				"text": {
					"type": "plain_text",
					"text": "List",
					"emoji": true
				},
				"value": "list",
				"action_id": "list",
				"style": "primary"
			}
		},
		{
			"type": "section",
			"text": {
				"type": "mrkdwn",
				"text": "Terminate the running cluster"
			},
			"accessory": {
				"type": "button",
				"text": {
					"type": "plain_text",
					"text": "Done",
					"emoji": true
				},
				"value": "done",
				"action_id": "done",
				"style": "primary"
			}
		},
		{
			"type": "section",
			"text": {
				"type": "mrkdwn",
				"text": "If the cluster is currently marked as failed, retry fetching its credentials in case of an error"
			},
			"accessory": {
				"type": "button",
				"text": {
					"type": "plain_text",
					"text": "Refresh",
					"emoji": true
				},
				"value": "refresh",
				"action_id": "refresh",
				"style": "primary"
			}
		},
		{
			"type": "section",
			"text": {
				"type": "mrkdwn",
				"text": "Send the credentials for the cluster you most recently requested"
			},
			"accessory": {
				"type": "button",
				"text": {
					"type": "plain_text",
					"text": "Auth",
					"emoji": true
				},
				"value": "auth",
				"action_id": "auth",
				"style": "primary"
			}
		},
		{
			"type": "header",
			"text": {
				"type": "plain_text",
				"text": "MCE Clusters",
				"emoji": true
			}
		},
		{
			"type": "divider"
		},
		{
			"type": "section",
			"text": {
				"type": "mrkdwn",
				"text": "Launch an MCE Cluster\n"
			},
			"accessory": {
				"type": "button",
				"text": {
					"type": "plain_text",
					"text": "Launch",
					"emoji": true
				},
				"value": "mce_create",
				"action_id": "mce_create",
				"style": "primary"
			}
		},
		{
			"type": "section",
			"text": {
				"type": "mrkdwn",
				"text": "Terminate MCE Cluster\n"
			},
			"accessory": {
				"type": "button",
				"text": {
					"type": "plain_text",
					"text": "Delete",
					"emoji": true
				},
				"value": "mce_delete",
				"action_id": "mce_delete",
				"style": "primary"
			}
		},
		{
			"type": "section",
			"text": {
				"type": "mrkdwn",
				"text": "Retrieve MCE Cluster Auth\n"
			},
			"accessory": {
				"type": "button",
				"text": {
					"type": "plain_text",
					"text": "Auth",
					"emoji": true
				},
				"value": "mce_auth",
				"action_id": "mce_auth",
				"style": "primary"
			}
		},
		{
			"type": "section",
			"text": {
				"type": "mrkdwn",
				"text": "List All Running MCE Clusters\n"
			},
			"accessory": {
				"type": "button",
				"text": {
					"type": "plain_text",
					"text": "List",
					"emoji": true
				},
				"value": "mce_list",
				"action_id": "mce_list",
				"style": "primary"
			}
		}
	]
}
`
*/
