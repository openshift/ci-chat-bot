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
				"text": "App Home for Cluster Bot Coming Soon"
			}
		}
	]
}
`
