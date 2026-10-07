package apphome

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	prowapiv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
)

type homeJobManager struct {
	manager.JobManager
	job *manager.Job
}

func (m homeJobManager) GetUserCluster(string) *manager.Job { return m.job }

func (homeJobManager) GetManagedClustersForUser(string) (map[string]*clusterv1.ManagedCluster, map[string]*hivev1.ClusterDeployment, map[string]*hivev1.ClusterProvision, map[string]string, map[string]string) {
	return nil, nil, nil, nil, nil
}

func TestViewAuthControl(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{manager.JobTypeLaunch, manager.JobTypeWorkflowLaunch, manager.JobTypeAroHcp} {
		t.Run(mode, func(t *testing.T) {
			view := View(homeJobManager{job: &manager.Job{Mode: mode, State: prowapiv1.PendingState}}, "user")
			data, err := json.Marshal(view)
			if err != nil {
				t.Fatal(err)
			}
			hasAuth := strings.Contains(string(data), `"action_id":"auth"`)
			wantAuth := mode != manager.JobTypeAroHcp
			if hasAuth != wantAuth {
				t.Fatalf("Auth button present = %v, want %v", hasAuth, wantAuth)
			}
			if !wantAuth && !strings.Contains(string(data), "aro-hcp auth") {
				t.Fatal("ARO-HCP view does not explain how to retrieve credentials")
			}
		})
	}
}
