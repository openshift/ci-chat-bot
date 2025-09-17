package apphome

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/events"
	"github.com/openshift/ci-chat-bot/pkg/utils"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"k8s.io/klog"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
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
		_, err = client.PublishView(event.User, View(jobManager, event.User), "")
		if err != nil {
			return false, err
		}
		return true, err
	})
}

func View(jobManager manager.JobManager, user string) slack.HomeTabViewRequest {
	home := slack.HomeTabViewRequest{}
	err := json.Unmarshal([]byte(homeJson), &home)
	if err != nil {
		klog.Warningf("Failed to unmarshall the app home JSON: %s", err)
		return slack.HomeTabViewRequest{}
	}
	userJob := jobManager.GetUserCluster(user)
	if userJob != nil {
		for index, block := range home.Blocks.BlockSet {
			if block.BlockType() == slack.MBTHeader {
				header := block.(*slack.HeaderBlock)
				if header.Text.Text == "Launch a Cluster" {
					// TODO: this looks kinda messy; we need to clean it up; maybe place it in its own block?
					header.Text.Text = fmt.Sprintf("Launch a Cluster (Your active %s cluster will expire at %s; switch to another view to update this text)", userJob.Platform, userJob.ExpiresAt.Format(time.Kitchen))
					home.Blocks.BlockSet[index] = header
					break
				}
			}
		}
	}

	managed, _, _, _, _ := jobManager.GetManagedClustersForUser(user)
	// for now, just assume a maximum of one managed cluster per user
	if len(managed) != 0 {
		var cluster *clusterv1.ManagedCluster
		for _, value := range managed {
			cluster = value
		}
		for index, block := range home.Blocks.BlockSet {
			if block.BlockType() == slack.MBTHeader {
				header := block.(*slack.HeaderBlock)
				if header.Text.Text == "MCE Clusters" {
					expiryTime, err := time.Parse(time.RFC3339, cluster.Annotations[utils.ExpiryTimeTag])
					if err != nil {
						break
					}
					// TODO: this looks kinda messy; we need to clean it up; maybe place it in its own block?
					header.Text.Text = fmt.Sprintf("MCE Clusters (Your active %s MCE cluster will expire at %s; switch to another view to update this text)", cluster.Labels["Cloud"], expiryTime.Format(time.Kitchen+" on Mon Jan 2"))
					home.Blocks.BlockSet[index] = header
					break
				}
			}
		}
	}
	return home
}

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
