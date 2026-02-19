package auth

import (
	"net/http"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	localslack "github.com/openshift/ci-chat-bot/pkg/slack"
	"github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/common"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

const identifier = "mce_auth"
const title = "MCE Authentication"

func Register(client *slack.Client, jobmanager manager.JobManager, httpclient *http.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(identifier, View()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: process(client, jobmanager, httpclient),
	})
}

// process has custom kubeconfig and cluster selection logic
func process(updater *slack.Client, jobManager manager.JobManager, httpclient *http.Client) interactions.Handler {
	return interactions.HandlerFunc(identifier, func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		go func() {
			var name, msg, kubeconfig string
			managed, deployments, provisions, kubeconfigs, passwords := jobManager.GetManagedClustersForUser(callback.User.ID)
			if len(managed) == 0 {
				msg = "You have no running MCE clusters."
			} else if len(managed) == 1 {
				// we need to get the key of the 1 cluster the user has
				for clusterName := range managed {
					name = clusterName
				}
			} else {
				msg = "You have multiple running clusters. Please specify the name of the cluster you are requesting credentials for."
			}
			if msg == "" {
				msg, kubeconfig = localslack.NotifyMce(updater, managed[name], deployments[name], provisions[name], kubeconfigs[name], passwords[name], false, nil)
			}
			submission := modals.SubmissionView(title, msg)
			common.AppendKubeconfigBlock(&submission, kubeconfig, "KubeConfig File (to download the kubeconfig as a file, type `mce auth` in the Messages tab):")
			modals.OverwriteView(updater, submission, callback, logger)
		}()
		return modals.SubmitPrepare(title, identifier, logger)
	})
}
