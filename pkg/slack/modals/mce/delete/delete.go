package delete

import (
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

const identifier = "mce_delete"
const title = "Delete an MCE Cluster"

func Register(client *slack.Client, jobmanager manager.JobManager) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(identifier, View()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: process(client, jobmanager),
	})
}

// process has custom logic and can't use MakeSimpleProcessHandler
func process(updater *slack.Client, jobManager manager.JobManager) interactions.Handler {
	return interactions.HandlerFunc(identifier, func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		go func() {
			managed, _, _, _, _ := jobManager.GetManagedClustersForUser(callback.User.ID)
			if len(managed) == 0 {
				msg := "You do not have any running MCE Clusters."
				modals.OverwriteView(updater, modals.SubmissionView(title, msg), callback, logger)
				return
			}
			var clusterName string
			if len(managed) == 1 {
				// we need to get the key of the 1 cluster the user has
				for managedName := range managed {
					clusterName = managedName
				}
			} else {
				// only a select few users have been given permission to use multiple MCE clusters
				msg := "You user has multiple running clusters. Please use the `Messages` tab for more advanced MCE Cluster management."
				modals.OverwriteView(updater, modals.SubmissionView(title, msg), callback, logger)
				return
			}
			msg, err := jobManager.DeleteMceCluster(callback.User.ID, clusterName)
			if err != nil {
				modals.OverwriteView(updater, modals.ErrorView("deleting managed cluster", err), callback, logger)
				return
			}
			modals.OverwriteView(updater, modals.SubmissionView(title, msg), callback, logger)
		}()
		return modals.SubmitPrepare(title, identifier, logger)
	})
}
