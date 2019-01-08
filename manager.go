package main

import (
	"bytes"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/openshift/ci-chat-bot/pkg/prow"
	prowapiv1 "github.com/openshift/ci-chat-bot/pkg/prow/apiv1"
)

// ClusterRequest keeps information about the request a user made to create
// a cluster. This is reconstructable from a ProwJob.
type ClusterRequest struct {
	User string

	ReleaseImage string

	Channel string

	RequestedAt time.Time
	Cluster     string
}

// ClusterManager responds to user actions and tracks the state of the launched
// clusters.
type ClusterManager interface {
	SetNotifier(ClusterCallbackFunc)

	LaunchClusterForUser(req *ClusterRequest) (string, error)
	SyncClusterForUser(user string) (string, error)
	GetCluster(user string) (*Cluster, error)
	ListClusters() string
}

// ClusterCallbackFunc is invoked when the cluster changes state in a significant
// way.
type ClusterCallbackFunc func(Cluster)

// Cluster responds to user requests and tracks the state of the launched
// clusters. This object must be recreatable from a ProwJob, but the RequestedChannel
// field may be empty to indicate the user has already been notified.
type Cluster struct {
	Name string

	ReleaseImage string

	Credentials string
	Failure     string

	RequestedBy      string
	RequestedChannel string

	RequestedAt time.Time
	ExpiresAt   time.Time
}

type clusterManager struct {
	lock         sync.Mutex
	requests     map[string]*ClusterRequest
	clusters     map[string]*Cluster
	waitList     []string
	lastEstimate time.Duration
	started      time.Time

	clusterPrefix string
	maxClusters   int
	maxAge        time.Duration

	prowConfigLoader prow.ProwConfigLoader
	prowClient       dynamic.NamespaceableResourceInterface
	coreClient       clientset.Interface
	coreConfig       *rest.Config
	prowNamespace    string
	prowJobName      string

	notifierFn ClusterCallbackFunc
}

// NewClusterManager creates a manager that will track the requests made by a user to create clusters
// and reflect that state into ProwJobs that launch clusters. It attempts to recreate state on startup
// by querying prow, but does not guarantee that some notifications to users may not be sent or may be
// sent twice.
func NewClusterManager(prowConfigLoader prow.ProwConfigLoader, prowClient dynamic.NamespaceableResourceInterface, coreClient clientset.Interface, config *rest.Config) *clusterManager {
	return &clusterManager{
		requests:      make(map[string]*ClusterRequest),
		clusters:      make(map[string]*Cluster),
		clusterPrefix: "chat-bot-",
		maxClusters:   5,
		maxAge:        2 * time.Hour,
		lastEstimate:  10 * time.Minute,

		prowConfigLoader: prowConfigLoader,
		prowClient:       prowClient,
		coreClient:       coreClient,
		coreConfig:       config,
		prowNamespace:    "ci",
		prowJobName:      "release-openshift-origin-installer-launch-aws-4.0",
	}
}

func (m *clusterManager) Start() error {
	if err := m.init(); err != nil {
		return err
	}
	go m.startSync()
	return nil
}

func (m *clusterManager) startSync() {
	for {
		m.expireClusters()
		time.Sleep(20 * time.Second)
	}
}

func (m *clusterManager) init() error {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.started = time.Now()

	u, err := m.prowClient.Namespace(m.prowNamespace).List(metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{
			"ci-chat-bot.openshift.io/launch": "true",
		}).String(),
	})
	if err != nil {
		return err
	}
	list := &prowapiv1.ProwJobList{}
	if err := prow.UnstructuredToObject(u, list); err != nil {
		return err
	}

	for _, job := range list.Items {
		switch job.Status.State {
		case prowapiv1.FailureState:
			user := job.Annotations["ci-chat-bot.openshift.io/user"]
			if len(user) == 0 {
				continue
			}
			if _, ok := m.clusters[job.Name]; !ok {
				cluster := &Cluster{
					Name:             job.Name,
					ReleaseImage:     job.Annotations["ci-chat-bot.openshift.io/releaseImage"],
					RequestedBy:      user,
					RequestedChannel: job.Annotations["ci-chat-bot.openshift.io/channel"],
					RequestedAt:      job.CreationTimestamp.Time,
					ExpiresAt:        job.CreationTimestamp.Time.Add(m.maxAge),
					Failure:          fmt.Sprintf("job failed, see logs at %s", job.Status.URL),
				}
				m.clusters[job.Name] = cluster
				go m.finishedCluster(*cluster)
			}

		case prowapiv1.TriggeredState, prowapiv1.PendingState, "":
			user := job.Annotations["ci-chat-bot.openshift.io/user"]
			if len(user) == 0 {
				continue
			}
			if _, ok := m.requests[user]; !ok {
				m.requests[user] = &ClusterRequest{
					User:         user,
					Cluster:      job.Name,
					ReleaseImage: job.Annotations["ci-chat-bot.openshift.io/releaseImage"],
					RequestedAt:  job.CreationTimestamp.Time,
					Channel:      job.Annotations["ci-chat-bot.openshift.io/channel"],
				}
			}
			if _, ok := m.clusters[job.Name]; !ok {
				cluster := &Cluster{
					Name:             job.Name,
					ReleaseImage:     job.Annotations["ci-chat-bot.openshift.io/releaseImage"],
					RequestedBy:      user,
					RequestedChannel: job.Annotations["ci-chat-bot.openshift.io/channel"],
					RequestedAt:      job.CreationTimestamp.Time,
					ExpiresAt:        job.CreationTimestamp.Time.Add(m.maxAge),
				}
				m.clusters[job.Name] = cluster
				go m.handleClusterStartup(*cluster)
			}
		}
	}
	log.Printf("Initialization complete, %d clusters and %d requests", len(m.clusters), len(m.requests))
	return nil
}

func (m *clusterManager) expireClusters() {
	m.lock.Lock()
	defer m.lock.Unlock()

	// forget everything that is too old
	now := time.Now()
	for _, cluster := range m.clusters {
		if cluster.ExpiresAt.Before(now) {
			log.Printf("cluster %q is expired", cluster.Name)
			delete(m.clusters, cluster.Name)
		}
	}
	for _, req := range m.requests {
		if req.RequestedAt.Add(m.maxAge * 2).Before(now) {
			log.Printf("request %q is expired", req.User)
			delete(m.requests, req.User)
		}
	}
	remaining := make([]string, 0, len(m.waitList))
	for _, user := range m.waitList {
		if _, ok := m.requests[user]; !ok {
			continue
		}
		remaining = append(remaining, user)
	}
	m.waitList = remaining
}

func (m *clusterManager) SetNotifier(fn ClusterCallbackFunc) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.notifierFn = fn
}

func (m *clusterManager) estimateCompletion(requestedAt time.Time) time.Duration {
	if requestedAt.IsZero() {
		return m.lastEstimate.Truncate(time.Second)
	}
	lastEstimate := m.lastEstimate - time.Now().Sub(requestedAt)
	if lastEstimate < 0 {
		return time.Minute
	}
	return lastEstimate.Truncate(time.Second)
}

func (m *clusterManager) ListClusters() string {
	m.lock.Lock()
	defer m.lock.Unlock()

	var clusters []*Cluster
	for _, cluster := range m.clusters {
		clusters = append(clusters, cluster)
	}
	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].RequestedAt.Before(clusters[j].RequestedAt) {
			return true
		}
		if clusters[i].Name < clusters[j].Name {
			return true
		}
		return false
	})
	buf := &bytes.Buffer{}
	now := time.Now()
	if len(clusters) == 0 {
		fmt.Fprintf(buf, "No clusters up (start time is approximately %.1f minutes):\n\n", m.lastEstimate.Seconds()/60)
	} else {
		fmt.Fprintf(buf, "%d/%d clusters up (start time is approximately %.1f minutes):\n\n", len(clusters), m.maxClusters, m.lastEstimate.Seconds()/60)
		for _, cluster := range clusters {
			switch {
			case len(cluster.Credentials) > 0:
				fmt.Fprintf(buf, "* `%s` %d minutes ago by <@%s> - available and will be torn down in %d minutes\n", cluster.Name, int(now.Sub(cluster.RequestedAt)/time.Minute), cluster.RequestedBy, int(cluster.ExpiresAt.Sub(now)/time.Minute))
			case len(cluster.Failure) > 0:
				fmt.Fprintf(buf, "* `%s` %d minutes ago by <@%s> - failure: %s\n", cluster.Name, int(now.Sub(cluster.RequestedAt)/time.Minute), cluster.RequestedBy, cluster.Failure)
			default:
				fmt.Fprintf(buf, "* `%s` %d minutes ago by <@%s> - pending\n", cluster.Name, int(now.Sub(cluster.RequestedAt)/time.Minute), cluster.RequestedBy)
			}
		}
		fmt.Fprintf(buf, "\n")
	}
	if len(m.waitList) > 0 {
		fmt.Fprintf(buf, "%d people on the waitlist\n", len(m.waitList))
	}
	fmt.Fprintf(buf, "\nbot uptime is %.1f minutes", now.Sub(m.started).Seconds()/60)
	return buf.String()
}

type callbackFunc func(cluster Cluster)

func (m *clusterManager) GetCluster(user string) (*Cluster, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	existing, ok := m.requests[user]
	if !ok {
		return nil, fmt.Errorf("you haven't requested a cluster or your cluster expired")
	}
	if len(existing.Cluster) == 0 {
		return nil, fmt.Errorf("you are still on the waitlist")
	}
	cluster, ok := m.clusters[existing.Cluster]
	if !ok {
		return nil, fmt.Errorf("your cluster has expired and credentials are no longer available")
	}
	copied := *cluster
	return &copied, nil
}

func (m *clusterManager) LaunchClusterForUser(req *ClusterRequest) (string, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	user := req.User
	if len(user) == 0 {
		return "", fmt.Errorf("must specify the name of the user who requested this cluster")
	}

	existing, ok := m.requests[user]
	if ok {
		if len(existing.Cluster) == 0 {
			log.Printf("user %q already requested cluster", user)
			return "", fmt.Errorf("you have already requested a cluster and it should be ready in ~ %d minutes", m.estimateCompletion(existing.RequestedAt)/time.Minute)
		}
		if cluster, ok := m.clusters[existing.Cluster]; ok {
			if len(cluster.Credentials) > 0 {
				log.Printf("user %q cluster is already up", user)
				return "your cluster is already running, see your credentials again with the 'auth' command", nil
			}
			if len(cluster.Failure) == 0 {
				log.Printf("user %q cluster has no credentials yet", user)
				return "", fmt.Errorf("you have already requested a cluster and it should be ready in ~ %d minutes", m.estimateCompletion(existing.RequestedAt)/time.Minute)
			}

			log.Printf("user %q cluster failed, allowing them to request another", user)
			delete(m.clusters, existing.Cluster)
			delete(m.requests, user)
		}
	}
	req.RequestedAt = time.Now()
	m.requests[user] = req

	if len(m.clusters) >= m.maxClusters {
		log.Printf("user %q is waitlisted", user)
		m.waitList = append(m.waitList, user)
		return "", fmt.Errorf("no clusters are currently available, I'll msg you when one frees up")
	}

	newCluster := &Cluster{
		Name:             fmt.Sprintf("%s%s", m.clusterPrefix, req.RequestedAt.UTC().Format("2006-01-02-150405.9999")),
		RequestedBy:      user,
		RequestedChannel: req.Channel,
		RequestedAt:      req.RequestedAt,
		ReleaseImage:     req.ReleaseImage,
		ExpiresAt:        req.RequestedAt.Add(m.maxAge),
	}
	req.Cluster = newCluster.Name
	m.clusters[newCluster.Name] = newCluster

	log.Printf("user %q requests cluster %q", user, newCluster.Name)
	go m.handleClusterStartup(*newCluster)

	return "", fmt.Errorf("a cluster is being created - I'll send you the credentials in about ~%d minutes", m.estimateCompletion(req.RequestedAt)/time.Minute)
}

func (m *clusterManager) SyncClusterForUser(user string) (string, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	if len(user) == 0 {
		return "", fmt.Errorf("must specify the name of the user who requested this cluster")
	}

	existing, ok := m.requests[user]
	if !ok || len(existing.Cluster) == 0 {
		return "", fmt.Errorf("no cluster has been requested by you")
	}
	cluster, ok := m.clusters[existing.Cluster]
	if !ok {
		return "", fmt.Errorf("cluster hasn't been initialized yet, cannot refresh")
	}

	var msg string
	switch {
	case len(cluster.Failure) == 0 && len(cluster.Credentials) == 0:
		return "cluster is still being loaded, please be patient", nil
	case len(cluster.Failure) > 0:
		msg = fmt.Sprintf("cluster had previously been marked as failed, checking again: %s", cluster.Failure)
	case len(cluster.Credentials) > 0:
		msg = fmt.Sprintf("cluster had previously been marked as successful, checking again")
	}

	copied := *cluster
	copied.Failure = ""
	log.Printf("user %q requests cluster %q to be refreshed", user, copied.Name)
	go m.handleClusterStartup(copied)

	return msg, nil
}

func (m *clusterManager) handleClusterStartup(cluster Cluster) {
	if err := m.launchCluster(&cluster); err != nil {
		log.Printf("failed to launch cluster: %v", err)
		cluster.Failure = err.Error()
	}
	m.finishedCluster(cluster)
}

func (m *clusterManager) finishedCluster(cluster Cluster) {
	m.lock.Lock()
	defer m.lock.Unlock()

	now := time.Now()
	if len(cluster.Credentials) > 0 {
		m.lastEstimate = now.Sub(cluster.RequestedAt)
	}

	log.Printf("completed cluster request for %s and notifying participants (%s)", cluster.Name, cluster.RequestedBy)

	m.clusters[cluster.Name] = &cluster
	for _, request := range m.requests {
		if request.Cluster == cluster.Name {
			log.Printf("notify %q that cluster %q is complete", request.User, request.Cluster)
			if m.notifierFn != nil {
				go m.notifierFn(cluster)
			}
		}
	}
}
