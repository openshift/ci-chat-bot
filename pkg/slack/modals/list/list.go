package list

import (
	"encoding/json"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

const (
	Identifier       = "list"
	filterByVersion  = "filter_by_version"
	filterByPlatform = "filter_by_platform"
	filterByUser     = "filter_by_user"
)

func Register(client *slack.Client, jobmanager manager.JobManager) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(Identifier, View()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processNextForFirstStep(client, jobmanager),
	})
}

func processNextForFirstStep(updater *slack.Client, jobmanager manager.JobManager) interactions.Handler {
	return interactions.HandlerFunc("list", func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		go func() {
			inputs := modals.CallBackInputAll(callback)
			var filters manager.ListFilters
			for key, input := range inputs {
				switch key {
				case filterByPlatform:
					filters.Platform = input
				case filterByVersion:
					filters.Version = input
				case filterByUser:
					filters.Requestor = input
				}
			}
			runningJobs := jobmanager.ListJobs([]string{callback.User.ID}, filters)
			overwriteView := func(view slack.ModalViewRequest) {
				// don't pass a hash, so we overwrite the View always
				response, err := updater.UpdateView(view, "", "", callback.View.ID)
				if err != nil {
					logger.WithError(err).Warn("Failed to update a modal View.")
				}
				logger.WithField("response", response).Trace("Got a modal response.")
			}
			overwriteView(SubmissionView(runningJobs))
		}()
		response, err := json.Marshal(&slack.ViewSubmissionResponse{
			ResponseAction: slack.RAUpdate,
			View:           PrepareNextStepView(),
		})
		if err != nil {
			logger.WithError(err).Error("Failed to marshal FirstStepView update submission response.")
			return nil, err
		}
		return response, nil
	})
}
