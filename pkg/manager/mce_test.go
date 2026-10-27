package manager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openshift/ci-chat-bot/pkg/utils"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// fakeHiveClient is a minimal crclient.Client that supports only the List and
// Get calls syncProvisions makes; the other embedded interface methods panic
// if called. List returns object metadata only, like the metadata client the
// real code uses, and Get returns the full stored provision.
type fakeHiveClient struct {
	crclient.Client
	provisions []hivev1.ClusterProvision
	listErr    error
	getErr     error
	gets       []crclient.ObjectKey
}

func (f *fakeHiveClient) List(ctx context.Context, list crclient.ObjectList, opts ...crclient.ListOption) error {
	if f.listErr != nil {
		return f.listErr
	}
	options := &crclient.ListOptions{}
	for _, opt := range opts {
		opt.ApplyToList(options)
	}
	metadataList, ok := list.(*metav1.PartialObjectMetadataList)
	if !ok {
		return errors.New("unexpected list object type")
	}
	for i := range f.provisions {
		provision := &f.provisions[i]
		if options.LabelSelector != nil && !options.LabelSelector.Matches(labels.Set(provision.Labels)) {
			continue
		}
		metadataList.Items = append(metadataList.Items, metav1.PartialObjectMetadata{
			TypeMeta: metav1.TypeMeta{Kind: "ClusterProvision", APIVersion: hivev1.SchemeGroupVersion.String()},
			ObjectMeta: metav1.ObjectMeta{
				Name:              provision.Name,
				Namespace:         provision.Namespace,
				ResourceVersion:   provision.ResourceVersion,
				Labels:            provision.Labels,
				CreationTimestamp: provision.CreationTimestamp,
			},
		})
	}
	return nil
}

func (f *fakeHiveClient) Get(ctx context.Context, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
	f.gets = append(f.gets, key)
	if f.getErr != nil {
		return f.getErr
	}
	for i := range f.provisions {
		if f.provisions[i].Name == key.Name && f.provisions[i].Namespace == key.Namespace {
			provision, ok := obj.(*hivev1.ClusterProvision)
			if !ok {
				return errors.New("unexpected get object type")
			}
			*provision = f.provisions[i]
			return nil
		}
	}
	return errors.New("provision not found")
}

func testErrorMetric() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_errors"}, []string{"error"})
}

// The provision cache must never retain install logs and must only fetch full
// provision objects when they actually changed, since install logs can be
// several megabytes.
func TestSyncProvisions(t *testing.T) {
	now := metav1.Now()
	older := metav1.NewTime(now.Add(-time.Hour))

	deploymentFor := func(name string) *hivev1.ClusterDeployment {
		return &hivev1.ClusterDeployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: name}}
	}
	provisionNamed := func(deployment, name, rv string, created metav1.Time) hivev1.ClusterProvision {
		return hivev1.ClusterProvision{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Namespace:         deployment,
				ResourceVersion:   rv,
				Labels:            map[string]string{"hive.openshift.io/cluster-deployment-name": deployment},
				CreationTimestamp: created,
			},
		}
	}

	t.Run("an unchanged resource version reuses the cached provision without fetching it", func(t *testing.T) {
		fake := &fakeHiveClient{provisions: []hivev1.ClusterProvision{provisionNamed("cluster", "cluster-provision", "2", now)}}
		cached := provisionNamed("cluster", "cluster-provision", "2", now)
		m := &jobManager{dpcrHiveClient: fake, errorMetric: testErrorMetric()}

		provisions, fresh := m.syncProvisions([]*hivev1.ClusterDeployment{deploymentFor("cluster")}, map[string]*hivev1.ClusterProvision{"cluster": &cached})

		if got := provisions["cluster"]; got != &cached {
			t.Errorf("expected the cached provision to be reused, got %v", got)
		}
		if fresh.Len() != 0 {
			t.Errorf("expected no fresh provisions, got %v", fresh.UnsortedList())
		}
		if len(fake.gets) != 0 {
			t.Errorf("expected no full object fetches, got %v", fake.gets)
		}
	})

	t.Run("a changed resource version fetches the full object with its install log", func(t *testing.T) {
		provision := provisionNamed("cluster", "cluster-provision", "3", now)
		installLog := "install log"
		provision.Spec.Stage = hivev1.ClusterProvisionStageFailed
		provision.Spec.InstallLog = &installLog
		fake := &fakeHiveClient{provisions: []hivev1.ClusterProvision{provision}}
		cached := provisionNamed("cluster", "cluster-provision", "2", now)
		m := &jobManager{dpcrHiveClient: fake, errorMetric: testErrorMetric()}

		provisions, fresh := m.syncProvisions([]*hivev1.ClusterDeployment{deploymentFor("cluster")}, map[string]*hivev1.ClusterProvision{"cluster": &cached})

		if len(fake.gets) != 1 || fake.gets[0].Name != "cluster-provision" {
			t.Fatalf("expected one fetch of the full provision, got %v", fake.gets)
		}
		got := provisions["cluster"]
		if got == nil || got.ResourceVersion != "3" {
			t.Fatalf("expected the freshly fetched provision, got %v", got)
		}
		if got == &cached {
			t.Error("expected a freshly fetched object, not the cached one")
		}
		if got.Spec.InstallLog == nil {
			t.Error("expected the freshly fetched provision to retain its install log for failure notifications")
		}
		if !fresh.Has("cluster") {
			t.Error("expected the cluster to be marked fresh")
		}
	})

	t.Run("the newest provision is selected", func(t *testing.T) {
		fake := &fakeHiveClient{provisions: []hivev1.ClusterProvision{
			provisionNamed("cluster", "cluster-old", "1", older),
			provisionNamed("cluster", "cluster-new", "2", now),
		}}
		m := &jobManager{dpcrHiveClient: fake, errorMetric: testErrorMetric()}

		provisions, _ := m.syncProvisions([]*hivev1.ClusterDeployment{deploymentFor("cluster")}, nil)

		if len(fake.gets) != 1 || fake.gets[0].Name != "cluster-new" {
			t.Fatalf("expected the newest provision to be fetched, got %v", fake.gets)
		}
		if got := provisions["cluster"]; got == nil || got.Name != "cluster-new" {
			t.Errorf("expected the newest provision, got %v", got)
		}
	})

	t.Run("a list error carries over the previous provision", func(t *testing.T) {
		fake := &fakeHiveClient{listErr: errors.New("list failed")}
		cached := provisionNamed("cluster", "cluster-provision", "2", now)
		m := &jobManager{dpcrHiveClient: fake, errorMetric: testErrorMetric()}

		provisions, fresh := m.syncProvisions([]*hivev1.ClusterDeployment{deploymentFor("cluster")}, map[string]*hivev1.ClusterProvision{"cluster": &cached})

		if got := provisions["cluster"]; got != &cached {
			t.Errorf("expected the cached provision to be carried over, got %v", got)
		}
		if fresh.Len() != 0 {
			t.Errorf("expected no fresh provisions, got %v", fresh.UnsortedList())
		}
		if len(fake.gets) != 0 {
			t.Errorf("expected no full object fetches, got %v", fake.gets)
		}
	})

	t.Run("a get error carries over the previous provision", func(t *testing.T) {
		fake := &fakeHiveClient{
			provisions: []hivev1.ClusterProvision{provisionNamed("cluster", "cluster-provision", "3", now)},
			getErr:     errors.New("get failed"),
		}
		cached := provisionNamed("cluster", "cluster-provision", "2", now)
		m := &jobManager{dpcrHiveClient: fake, errorMetric: testErrorMetric()}

		provisions, fresh := m.syncProvisions([]*hivev1.ClusterDeployment{deploymentFor("cluster")}, map[string]*hivev1.ClusterProvision{"cluster": &cached})

		if got := provisions["cluster"]; got != &cached {
			t.Errorf("expected the cached provision to be carried over, got %v", got)
		}
		if fresh.Len() != 0 {
			t.Errorf("expected no fresh provisions, got %v", fresh.UnsortedList())
		}
	})

	t.Run("a get error with no previous provision drops the entry", func(t *testing.T) {
		fake := &fakeHiveClient{
			provisions: []hivev1.ClusterProvision{provisionNamed("cluster", "cluster-provision", "3", now)},
			getErr:     errors.New("get failed"),
		}
		m := &jobManager{dpcrHiveClient: fake, errorMetric: testErrorMetric()}

		provisions, fresh := m.syncProvisions([]*hivev1.ClusterDeployment{deploymentFor("cluster")}, nil)

		if _, ok := provisions["cluster"]; ok {
			t.Error("expected no provision to be cached for the cluster")
		}
		if fresh.Len() != 0 {
			t.Errorf("expected no fresh provisions, got %v", fresh.UnsortedList())
		}
	})

	t.Run("a deployment without provisions is skipped", func(t *testing.T) {
		fake := &fakeHiveClient{}
		m := &jobManager{dpcrHiveClient: fake, errorMetric: testErrorMetric()}

		provisions, fresh := m.syncProvisions([]*hivev1.ClusterDeployment{deploymentFor("cluster")}, nil)

		if _, ok := provisions["cluster"]; ok {
			t.Error("expected no provision to be cached for the cluster")
		}
		if fresh.Len() != 0 || len(fake.gets) != 0 {
			t.Errorf("expected no fresh provisions and no fetches, got fresh=%v gets=%v", fresh.UnsortedList(), fake.gets)
		}
	})

	t.Run("a provision carried over after an error is still replaced when it changes", func(t *testing.T) {
		fake := &fakeHiveClient{
			provisions: []hivev1.ClusterProvision{provisionNamed("cluster", "cluster-provision", "3", now)},
			getErr:     errors.New("get failed"),
		}
		cached := provisionNamed("cluster", "cluster-provision", "2", now)
		m := &jobManager{dpcrHiveClient: fake, errorMetric: testErrorMetric()}

		// the full fetch fails, so the previous provision is carried over
		provisions, _ := m.syncProvisions([]*hivev1.ClusterDeployment{deploymentFor("cluster")}, map[string]*hivev1.ClusterProvision{"cluster": &cached})
		if got := provisions["cluster"]; got != &cached {
			t.Fatalf("expected the cached provision to be carried over, got %v", got)
		}

		// the next sync still fetches the changed provision, because the
		// carried-over copy kept the old resource version
		fake.getErr = nil
		provisions, fresh := m.syncProvisions([]*hivev1.ClusterDeployment{deploymentFor("cluster")}, provisions)
		got := provisions["cluster"]
		if got == nil || got.ResourceVersion != "3" {
			t.Fatalf("expected the changed provision to be fetched, got %v", got)
		}
		if !fresh.Has("cluster") {
			t.Error("expected the cluster to be marked fresh")
		}
	})
}

// Cached provisions must not retain install logs; only freshly fetched
// provisions may be modified, because reused entries are shared with readers.
func TestStripInstallLogs(t *testing.T) {
	freshLog, reusedLog := "fresh log", "reused log"
	fresh := &hivev1.ClusterProvision{Spec: hivev1.ClusterProvisionSpec{InstallLog: &freshLog}}
	reused := &hivev1.ClusterProvision{Spec: hivev1.ClusterProvisionSpec{InstallLog: &reusedLog}}
	provisions := map[string]*hivev1.ClusterProvision{"fresh": fresh, "reused": reused}

	stripInstallLogs(provisions, sets.New[string]("fresh"))

	if fresh.Spec.InstallLog != nil {
		t.Error("expected the install log of the fresh provision to be stripped")
	}
	if reused.Spec.InstallLog == nil {
		t.Error("expected the reused provision to be left untouched")
	}
}

// Only failed provisions are re-fetched for notifications, since only they
// need the install log that the cache does not retain.
func TestGetClusterProvisionForNotification(t *testing.T) {
	installLog := "install log"
	stored := hivev1.ClusterProvision{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-provision", Namespace: "cluster"},
		Spec:       hivev1.ClusterProvisionSpec{Stage: hivev1.ClusterProvisionStageFailed, InstallLog: &installLog},
	}
	cachedWithStage := func(stage hivev1.ClusterProvisionStage) *hivev1.ClusterProvision {
		return &hivev1.ClusterProvision{
			ObjectMeta: stored.ObjectMeta,
			Spec:       hivev1.ClusterProvisionSpec{Stage: stage},
		}
	}
	managerWith := func(fake *fakeHiveClient, cached *hivev1.ClusterProvision) *jobManager {
		m := &jobManager{dpcrHiveClient: fake}
		m.mceClusters.provisions = map[string]*hivev1.ClusterProvision{}
		if cached != nil {
			m.mceClusters.provisions["cluster"] = cached
		}
		return m
	}

	t.Run("no cached provision returns nil", func(t *testing.T) {
		fake := &fakeHiveClient{provisions: []hivev1.ClusterProvision{stored}}
		if got := managerWith(fake, nil).GetClusterProvisionForNotification("cluster"); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
		if len(fake.gets) != 0 {
			t.Errorf("expected no fetches, got %v", fake.gets)
		}
	})

	t.Run("a provision that has not failed returns the cached copy", func(t *testing.T) {
		fake := &fakeHiveClient{provisions: []hivev1.ClusterProvision{stored}}
		cached := cachedWithStage(hivev1.ClusterProvisionStageProvisioning)
		if got := managerWith(fake, cached).GetClusterProvisionForNotification("cluster"); got != cached {
			t.Errorf("expected the cached provision, got %v", got)
		}
		if len(fake.gets) != 0 {
			t.Errorf("expected no fetches, got %v", fake.gets)
		}
	})

	t.Run("a failed provision is re-fetched with its install log", func(t *testing.T) {
		fake := &fakeHiveClient{provisions: []hivev1.ClusterProvision{stored}}
		got := managerWith(fake, cachedWithStage(hivev1.ClusterProvisionStageFailed)).GetClusterProvisionForNotification("cluster")
		if got == nil || got.Spec.InstallLog == nil || *got.Spec.InstallLog != installLog {
			t.Errorf("expected the full provision with its install log, got %v", got)
		}
		if len(fake.gets) != 1 {
			t.Errorf("expected one fetch, got %v", fake.gets)
		}
	})

	t.Run("a failed fetch falls back to the cached copy", func(t *testing.T) {
		fake := &fakeHiveClient{provisions: []hivev1.ClusterProvision{stored}, getErr: errors.New("boom")}
		cached := cachedWithStage(hivev1.ClusterProvisionStageFailed)
		if got := managerWith(fake, cached).GetClusterProvisionForNotification("cluster"); got != cached {
			t.Errorf("expected the cached provision, got %v", got)
		}
	})
}

// Failure notifications that Slack does not accept are retried a bounded number
// of times, since every retry re-fetches the install log, and clusters without
// a requesting user or channel are never notified at all.
func TestNotifyMceFailure(t *testing.T) {
	installLog := "install log"
	stored := hivev1.ClusterProvision{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-provision", Namespace: "cluster"},
		Spec:       hivev1.ClusterProvisionSpec{Stage: hivev1.ClusterProvisionStageFailed, InstallLog: &installLog},
	}
	cached := &hivev1.ClusterProvision{
		ObjectMeta: stored.ObjectMeta,
		Spec:       hivev1.ClusterProvisionSpec{Stage: hivev1.ClusterProvisionStageFailed},
	}
	tagged := &clusterv1.ManagedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Annotations: map[string]string{
		utils.UserTag:    "user",
		utils.ChannelTag: "channel",
	}}}

	t.Run("a retry re-fetches the install log and clears the pending attempt", func(t *testing.T) {
		fake := &fakeHiveClient{provisions: []hivev1.ClusterProvision{stored}}
		var gotInstallLog bool
		m := &jobManager{dpcrHiveClient: fake, mceFailureNotifyAttempts: map[string]int{"cluster": 1}}
		m.SetMceNotifier(func(_ *clusterv1.ManagedCluster, _ *hivev1.ClusterDeployment, provision *hivev1.ClusterProvision, _, _ string, _ error) bool {
			gotInstallLog = provision != nil && provision.Spec.InstallLog != nil && *provision.Spec.InstallLog == installLog
			return true
		})

		m.notifyMceFailure("cluster", tagged, nil, cached, false, false)

		if !gotInstallLog {
			t.Error("expected the install log to be fetched for the retry")
		}
		if len(fake.gets) != 1 {
			t.Errorf("expected one fetch, got %v", fake.gets)
		}
		if len(m.mceFailureNotifyAttempts) != 0 {
			t.Errorf("expected the pending attempt to be cleared, got %v", m.mceFailureNotifyAttempts)
		}
	})

	t.Run("retries stop after a bounded number of attempts", func(t *testing.T) {
		fake := &fakeHiveClient{provisions: []hivev1.ClusterProvision{stored}}
		calls := 0
		m := &jobManager{dpcrHiveClient: fake}
		m.SetMceNotifier(func(_ *clusterv1.ManagedCluster, _ *hivev1.ClusterDeployment, _ *hivev1.ClusterProvision, _, _ string, _ error) bool {
			calls++
			return false
		})

		for i := range maxMCENotificationAttempts {
			m.notifyMceFailure("cluster", tagged, nil, cached, i == 0, true)
		}
		m.notifyMceFailure("cluster", tagged, nil, cached, false, true)

		if calls != maxMCENotificationAttempts {
			t.Errorf("expected %d notification attempts, got %d", maxMCENotificationAttempts, calls)
		}
		if len(m.mceFailureNotifyAttempts) != 0 {
			t.Errorf("expected retries to be given up, got %v", m.mceFailureNotifyAttempts)
		}
	})

	t.Run("an untagged cluster is never notified or fetched", func(t *testing.T) {
		fake := &fakeHiveClient{provisions: []hivev1.ClusterProvision{stored}}
		calls := 0
		m := &jobManager{dpcrHiveClient: fake, mceFailureNotifyAttempts: map[string]int{"cluster": 1}}
		m.SetMceNotifier(func(_ *clusterv1.ManagedCluster, _ *hivev1.ClusterDeployment, _ *hivev1.ClusterProvision, _, _ string, _ error) bool {
			calls++
			return true
		})

		m.notifyMceFailure("cluster", &clusterv1.ManagedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}, nil, cached, true, false)

		if calls != 0 {
			t.Errorf("expected no notifications, got %d", calls)
		}
		if len(fake.gets) != 0 {
			t.Errorf("expected no fetches, got %v", fake.gets)
		}
		if len(m.mceFailureNotifyAttempts) != 0 {
			t.Errorf("expected the pending attempt to be dropped, got %v", m.mceFailureNotifyAttempts)
		}
	})
}

func TestNotifyMceFailureWithoutProvision(t *testing.T) {
	tagged := &clusterv1.ManagedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Annotations: map[string]string{
		utils.UserTag:    "user",
		utils.ChannelTag: "channel",
	}}}
	fake := &fakeHiveClient{}
	results := []bool{false, true}
	calls := 0
	m := &jobManager{dpcrHiveClient: fake}
	m.SetMceNotifier(func(_ *clusterv1.ManagedCluster, _ *hivev1.ClusterDeployment, provision *hivev1.ClusterProvision, _, _ string, _ error) bool {
		if provision != nil {
			t.Errorf("expected no provision, got %v", provision)
		}
		calls++
		return results[calls-1]
	})

	// the first sync observes the failure but delivery fails; the next sync
	// no longer sees a transition and must still retry
	m.notifyMceFailure("cluster", tagged, nil, nil, true, true)
	if m.mceFailureNotifyAttempts["cluster"] != 1 {
		t.Fatalf("expected one pending attempt, got %v", m.mceFailureNotifyAttempts)
	}
	m.notifyMceFailure("cluster", tagged, nil, nil, false, true)
	m.notifyMceFailure("cluster", tagged, nil, nil, false, true)

	if calls != 2 {
		t.Errorf("expected 2 notification attempts, got %d", calls)
	}
	if len(m.mceFailureNotifyAttempts) != 0 {
		t.Errorf("expected the pending attempt to be cleared, got %v", m.mceFailureNotifyAttempts)
	}
	if len(fake.gets) != 0 {
		t.Errorf("expected no fetches, got %v", fake.gets)
	}
}

func TestRecordMceNotifyAttempt(t *testing.T) {
	var attempts map[string]int
	for i := 1; i < maxMCENotificationAttempts; i++ {
		if recordMceNotifyAttempt(&attempts, "readiness", "cluster") {
			t.Fatalf("attempt %d: expected retries to continue", i)
		}
		if attempts["cluster"] != i {
			t.Fatalf("attempt %d: expected count %d, got %v", i, i, attempts)
		}
	}
	if !recordMceNotifyAttempt(&attempts, "readiness", "cluster") {
		t.Error("expected retries to be given up")
	}
	if len(attempts) != 0 {
		t.Errorf("expected the count to be dropped, got %v", attempts)
	}
}

func TestProvisionFailureIsNew(t *testing.T) {
	provision := func(name string, stage hivev1.ClusterProvisionStage) *hivev1.ClusterProvision {
		return &hivev1.ClusterProvision{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: hivev1.ClusterProvisionSpec{Stage: stage}}
	}
	failed := provision("a", hivev1.ClusterProvisionStageFailed)
	for _, tc := range []struct {
		name     string
		previous *hivev1.ClusterProvision
		want     bool
	}{
		{name: "no previous provision, as after a restart", previous: nil},
		{name: "already failed", previous: provision("a", hivev1.ClusterProvisionStageFailed)},
		{name: "stage changed", previous: provision("a", hivev1.ClusterProvisionStageProvisioning), want: true},
		{name: "retry provision failed between syncs", previous: provision("b", hivev1.ClusterProvisionStageFailed), want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := provisionFailureIsNew(tc.previous, failed); got != tc.want {
				t.Errorf("expected %t, got %t", tc.want, got)
			}
		})
	}
}

func TestDeploymentProvisionFailed(t *testing.T) {
	withCondition := func(status string) *hivev1.ClusterDeployment {
		return &hivev1.ClusterDeployment{Status: hivev1.ClusterDeploymentStatus{Conditions: []hivev1.ClusterDeploymentCondition{
			{Type: hivev1.ProvisionFailedCondition, Status: corev1.ConditionStatus(status)},
		}}}
	}
	if deploymentProvisionFailed(nil) {
		t.Error("expected a nil deployment not to be failed")
	}
	if deploymentProvisionFailed(withCondition("False")) {
		t.Error("expected a false condition not to be failed")
	}
	if !deploymentProvisionFailed(withCondition("True")) {
		t.Error("expected a true condition to be failed")
	}
}
