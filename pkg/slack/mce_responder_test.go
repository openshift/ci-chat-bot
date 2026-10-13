package slack

import (
	"errors"
	"testing"

	"github.com/openshift/ci-chat-bot/pkg/utils"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/slack-go/slack"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

// The manager only marks a cluster as notified when the responder reports a
// delivered notification, so it must return false whenever it skips one and
// whenever Slack rejects the post.
func TestMceResponder(t *testing.T) {
	tagged := map[string]string{utils.UserTag: "user", utils.ChannelTag: "channel"}
	provisionInStage := func(stage hivev1.ClusterProvisionStage) *hivev1.ClusterProvision {
		return &hivev1.ClusterProvision{Spec: hivev1.ClusterProvisionSpec{Stage: stage}}
	}
	for _, tc := range []struct {
		name        string
		annotations map[string]string
		deployment  *hivev1.ClusterDeployment
		provision   *hivev1.ClusterProvision
		errMsg      error
		postFails   bool
		want        bool
	}{
		{
			name:      "missing tags",
			provision: provisionInStage(hivev1.ClusterProvisionStageComplete),
		},
		{
			name:        "provision still provisioning",
			annotations: tagged,
			provision:   provisionInStage(hivev1.ClusterProvisionStageProvisioning),
		},
		{
			name:        "no provision and no failure",
			annotations: tagged,
			deployment:  &hivev1.ClusterDeployment{},
		},
		{
			name:        "provision complete",
			annotations: tagged,
			provision:   provisionInStage(hivev1.ClusterProvisionStageComplete),
			want:        true,
		},
		{
			name:        "deployment failed to provision",
			annotations: tagged,
			deployment: &hivev1.ClusterDeployment{Status: hivev1.ClusterDeploymentStatus{Conditions: []hivev1.ClusterDeploymentCondition{
				{Type: hivev1.ProvisionFailedCondition, Status: corev1.ConditionTrue},
			}}},
			want: true,
		},
		{
			name:        "error message",
			annotations: tagged,
			errMsg:      errors.New("failed"),
			want:        true,
		},
		{
			name:        "slack rejects the message",
			annotations: tagged,
			provision:   provisionInStage(hivev1.ClusterProvisionStageComplete),
			postFails:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster := &clusterv1.ManagedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Annotations: tc.annotations}}
			slackClient := &mockSlackClient{}
			if !tc.postFails {
				slackClient.postMessageFunc = func(channelID string, options ...slack.MsgOption) (string, string, error) {
					return "", "", nil
				}
			}
			notify := (&Bot{}).MceResponder(slackClient)
			if got := notify(cluster, tc.deployment, tc.provision, "kubeconfig", "password", tc.errMsg); got != tc.want {
				t.Errorf("expected %t, got %t", tc.want, got)
			}
		})
	}
}

// The auth modal presents the message itself, so NotifyMce must not upload the
// install log of a failed provision to the channel when postMessage is false.
func TestNotifyMceFailedProvisionWithoutPosting(t *testing.T) {
	installLog := "install log"
	cluster := &clusterv1.ManagedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Annotations: map[string]string{utils.UserTag: "user", utils.ChannelTag: "channel"}}}
	provision := &hivev1.ClusterProvision{Spec: hivev1.ClusterProvisionSpec{Stage: hivev1.ClusterProvisionStageFailed, InstallLog: &installLog}}
	slackClient := &mockSlackClient{
		postMessageFunc: func(channelID string, options ...slack.MsgOption) (string, string, error) {
			t.Error("expected no message to be posted")
			return "", "", nil
		},
		uploadFileFunc: func(params slack.UploadFileParameters) (*slack.FileSummary, error) {
			t.Error("expected no file to be uploaded")
			return &slack.FileSummary{}, nil
		},
	}
	msg, kubeconfig, delivered := NotifyMce(slackClient, cluster, nil, provision, "", "", false, nil)
	if msg == "" || kubeconfig != "" || !delivered {
		t.Errorf("expected the failure message only, got message %q, kubeconfig %q, delivered %t", msg, kubeconfig, delivered)
	}
}
