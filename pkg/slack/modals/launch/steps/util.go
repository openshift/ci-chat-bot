package steps

import (
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/launch"
	"github.com/slack-go/slack"
	"strings"
)

func callbackContext(callback *slack.InteractionCallback) map[string]string {
	contextMap := make(map[string]string)
	var context string
	for _, value := range callback.View.Blocks.BlockSet {
		if value.BlockType() == slack.MBTContext {
			metadata, ok := value.(*slack.ContextBlock)
			if ok {
				text, ok := metadata.ContextElements.Elements[0].(*slack.TextBlockObject)
				if ok {
					context = text.Text
				}
			}
		}
	}
	mSplit := strings.Split(context, ";")
	for _, v := range mSplit {
		key := strings.ToLower(strings.TrimSpace(strings.Split(v, ":")[0]))
		value := strings.ToLower(strings.TrimSpace(strings.Split(v, ":")[1]))
		switch key {
		case launch.LaunchArchitecture:
			contextMap[launch.LaunchArchitecture] = value
		case launch.LaunchPlatform:
			contextMap[launch.LaunchPlatform] = value
		case launch.LaunchVersion:
			contextMap[launch.LaunchVersion] = value
		case launch.LaunchFromPR:
			contextMap[launch.LaunchFromPR] = value
		case strings.ToLower(launch.LaunchModeContext):
			contextMap[launch.LaunchMode] = value
		}
	}
	return contextMap
}
