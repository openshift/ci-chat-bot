package done

import (
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals/common"
	"github.com/slack-go/slack"
)

const identifier = "done"
const title = "Terminate a Cluster"

func Register(client *slack.Client, jobmanager manager.JobManager) *modals.FlowWithViewAndFollowUps {
	return common.RegisterSimpleModal(
		common.SimpleModalConfig{
			Identifier: identifier,
			Title:      title,
			ViewFunc:   View,
		},
		common.MakeSimpleProcessHandler(
			identifier,
			title,
			func(jobManager manager.JobManager, callback *slack.InteractionCallback) (string, error) {
				return jobManager.TerminateJobForUser(callback.User.ID)
			},
			"terminating job",
		),
	)(client, jobmanager)
}
