package apphome

import (
	"encoding/json"
	"github.com/openshift/ci-chat-bot/pkg/slack/events"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"k8s.io/klog"
)

type publishView interface {
	PublishView(userID string, view slack.HomeTabViewRequest, hash string) (*slack.ViewResponse, error)
}

func Handler(client publishView) events.PartialHandler {
	return events.PartialHandlerFunc("workflow-execution-event", func(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (handled bool, err error) {
		if callback.Type != slackevents.CallbackEvent {
			return false, nil
		}
		event, ok := callback.InnerEvent.Data.(*slackevents.AppHomeOpenedEvent)
		if !ok {
			return false, nil
		}
		_, err = client.PublishView(event.User, View(), "")
		if err != nil {
			return false, err
		}
		return true, err
	})
}

func View() slack.HomeTabViewRequest {
	home := slack.HomeTabViewRequest{}
	err := json.Unmarshal([]byte(homeJson), &home)
	if err != nil {
		klog.Warningf("Failed to unmarshall the app home JSON: %s", err)
		return slack.HomeTabViewRequest{}
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
      "type": "divider"
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
        "action_id": "launch"
      }
    },
    {
      "type": "divider"
    },
    {
      "type": "header",
      "text": {
        "type": "plain_text",
        "text": "Workflows",
        "emoji": true
      }
    },
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "Launch a cluster using the requested workflow from an image,release or built PRs"
      },
      "accessory": {
        "type": "button",
        "text": {
          "type": "plain_text",
          "text": "Workflow-Launch",
          "emoji": true
        },
        "value": "workflow_launch",
        "action_id": "workflow_launch"
      }
    },
    {
      "type": "divider"
    },
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "Run a custom upgrade using the requested workflow from an image or release or built PRs to a specified version/image/pr from https://amd64.ocp.releases.ci.openshift.org"
      },
      "accessory": {
        "type": "button",
        "text": {
          "type": "plain_text",
          "text": "Workflow-Upgrade",
          "emoji": true
        },
        "value": "workflow_upgrade",
        "action_id": "workflow_upgrade"
      }
    },
    {
      "type": "divider"
    },
    {
      "type": "header",
      "text": {
        "type": "plain_text",
        "text": "Test",
        "emoji": true
      }
    },
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "Launch a cluster using the requested workflow from an image,release or built PRs"
      },
      "accessory": {
        "type": "button",
        "text": {
          "type": "plain_text",
          "text": "Test",
          "emoji": true
        },
        "value": "test",
        "action_id": "test"
      }
    },
    {
      "type": "divider"
    },
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "Run the upgrade tests between two release images"
      },
      "accessory": {
        "type": "button",
        "text": {
          "type": "plain_text",
          "text": "Test Upgrade",
          "emoji": true
        },
        "value": "test_upgrade",
        "action_id": "test_upgrade"
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
        "action_id": "list"
      }
    },
    {
      "type": "divider"
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
        "action_id": "done"
      }
    },
    {
      "type": "divider"
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
        "action_id": "refresh"
      }
    },
    {
      "type": "divider"
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
        "action_id": "auth"
      }
    },
    {
      "type": "divider"
    },
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "Report the version of the bot\n"
      },
      "accessory": {
        "type": "button",
        "text": {
          "type": "plain_text",
          "text": "Version",
          "emoji": true
        },
        "value": "version",
        "action_id": "version"
      }
    },
    {
      "type": "divider"
    },
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "Get info about a version\n"
      },
      "accessory": {
        "type": "button",
        "text": {
          "type": "plain_text",
          "text": "Lookup",
          "emoji": true
        },
        "value": "lookup",
        "action_id": "lookup"
      }
    }
  ]
}
`
