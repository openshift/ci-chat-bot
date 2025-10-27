package done

import (
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

const identifier = "done"
const title = "Terminate a Cluster"

func Register(client *slack.Client, jobmanager manager.JobManager) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(identifier, View()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: process(client, jobmanager),
	})
}

func process(updater *slack.Client, jobManager manager.JobManager) interactions.Handler {
	return interactions.HandlerFunc(identifier, func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		go func() {
			msg, err := jobManager.TerminateJobForUser(callback.User.ID)
			if err != nil {
				modals.OverwriteView(updater, modals.ErrorView("terminating the job", err), callback, logger)
				return
			}
			modals.OverwriteView(updater, modals.SubmissionView(title, msg), callback, logger)
		}()
		return modals.SubmitPrepare(title, identifier, logger)
	})
}
