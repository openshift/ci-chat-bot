package manager

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openshift/ci-chat-bot/pkg/prow"
	"github.com/openshift/ci-chat-bot/pkg/utils"

	"github.com/prometheus/client_golang/prometheus"

	prowapiv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/metrics"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/strings/slices"

	clustermgmtv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	imagev1 "github.com/openshift/api/image/v1"
	citools "github.com/openshift/ci-tools/pkg/api"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned"

	"github.com/openshift/rosa/pkg/ocm"
	"github.com/openshift/rosa/pkg/rosa"

	"k8s.io/client-go/dynamic"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog"

	"github.com/blang/semver"
	"gopkg.in/yaml.v2"
)

func init() {
	prometheus.MustRegister(rosaReadyTimeMetric)
	prometheus.MustRegister(rosaAuthTimeMetric)
	prometheus.MustRegister(rosaReadyToAuthTimeMetric)
	prometheus.MustRegister(rosaConsoleTimeMetric)
	prometheus.MustRegister(rosaReadyToConsoleTimeMetric)
	prometheus.MustRegister(rosaSyncTimeMetric)
}

const (
	// maxJobsPerUser limits the number of simultaneous jobs a user can launch to prevent
	// a single user from consuming the infrastructure account.
	maxJobsPerUser = 23

	// maxTotalClusters limits the number of simultaneous clusters across all users to
	// prevent saturating the infrastructure account.
	maxTotalClusters = 80
)

const (
	JobTypeBuild = "build"
	// TODO: remove this const. It seems out of date and replaced by launch everywhere except for in JobRequest.JobType. Gets changed to "launch" for job.Mode
	JobTypeInstall         = "install"
	JobTypeLaunch          = "launch"
	JobTypeTest            = "test"
	JobTypeUpgrade         = "upgrade"
	JobTypeWorkflowLaunch  = "workflow-launch"
	JobTypeWorkflowUpgrade = "workflow-upgrade"
)

var CurrentRelease = semver.Version{
	Major: 4,
	Minor: 13,
}

var HypershiftSupportedVersions = HypershiftSupportedVersionsType{}

var reBranchVersion = regexp.MustCompile(`^(openshift-|release-)(\d+\.\d+)$`)
var reMajorMinorVersion = regexp.MustCompile(`^(\d+)\.(\d+)$`)

func (j Job) IsComplete() bool {
	return j.Complete || len(j.Credentials) > 0 || (len(j.State) > 0 && j.State != prowapiv1.PendingState)
}

// initializeErrorMetrics initializes all labels used by the error metrics to 0. This allows
// prometheus to output a non-zero rate when an error occurs (unset values becoming set is a `0` rate)
func initializeErrorMetrics(vec *prometheus.CounterVec) {
	for label := range errorMetricList {
		vec.WithLabelValues(label).Add(0)
	}
}

// NewJobManager creates a manager that will track the requests made by a user to create clusters
// and reflect that state into ProwJobs that launch clusters. It attempts to recreate state on startup
// by querying prow, but does not guarantee that some notifications to users may not be sent or may be
// sent twice.
func NewJobManager(
	prowConfigLoader prow.ProwConfigLoader,
	configResolver ConfigResolver,
	prowClient dynamic.NamespaceableResourceInterface,
	imageClient imageclientset.Interface,
	buildClusterClientConfigMap utils.BuildClusterClientConfigMap,
	githubClient github.Client,
	forcePROwner string,
	workflowConfig *WorkflowConfig,
	lClient LeaseClient,
	hiveConfigMapClient typedcorev1.ConfigMapInterface,
	rosaClient *rosa.Runtime,
	rosaSecretClient typedcorev1.SecretInterface,
	rosaSubnetList *RosaSubnets,
	rosaClusterLimit int,
	errorRate *prometheus.CounterVec,
) *jobManager {
	m := &jobManager{
		requests:         make(map[string]*JobRequest),
		jobs:             make(map[string]*Job),
		clusterPrefix:    "chat-bot-",
		maxClusters:      maxTotalClusters,
		maxAge:           3 * time.Hour,
		githubClient:     githubClient,
		prowConfigLoader: prowConfigLoader,
		prowClient:       prowClient,
		imageClient:      imageClient,
		clusterClients:   buildClusterClientConfigMap,
		prowNamespace:    "ci",
		forcePROwner:     forcePROwner,

		configResolver: configResolver,
		workflowConfig: workflowConfig,

		lClient: lClient,

		hiveConfigMapClient: hiveConfigMapClient,
		rosaSecretClient:    rosaSecretClient,
		rClient:             rosaClient,
		maxRosaAge:          6 * time.Hour,
		defaultRosaAge:      6 * time.Hour,
		rosaSubnets:         rosaSubnetList,
		rosaClusterLimit:    rosaClusterLimit,
		errorMetric:         errorRate,
	}
	m.muJob.running = make(map[string]struct{})
	initializeErrorMetrics(m.errorMetric)
	return m
}

func (m *jobManager) updateHypershiftSupportedVersions() error {
	if m.hiveConfigMapClient == nil {
		HypershiftSupportedVersions.Mu.Lock()
		defer HypershiftSupportedVersions.Mu.Unlock()
		// assume that current default release for chat-bot is supported
		HypershiftSupportedVersions.Versions = sets.NewString(fmt.Sprintf("%d.%d", CurrentRelease.Major, CurrentRelease.Minor))
		return nil
	}
	supportedVersionConfigMap, err := m.hiveConfigMapClient.Get(context.TODO(), "supported-versions", metav1.GetOptions{})
	if err != nil {
		return err
	}
	if supportedVersionConfigMap.Data == nil {
		return errors.New("supported-versions configmap is empty")
	}
	if _, ok := supportedVersionConfigMap.Data["supported-versions"]; !ok {
		return errors.New("supported-versions configmap missing `supported-versions` key")
	}
	rawVersions := supportedVersionConfigMap.Data["supported-versions"]
	convertedVersions := struct {
		Versions []string `json:"versions"`
	}{}
	if err := json.Unmarshal([]byte(rawVersions), &convertedVersions); err != nil {
		return fmt.Errorf("failed to convert configmap json supported-versions to struct: %w", err)
	}
	HypershiftSupportedVersions.Mu.Lock()
	defer HypershiftSupportedVersions.Mu.Unlock()
	HypershiftSupportedVersions.Versions = sets.NewString(convertedVersions.Versions...)
	// 4.11 is currently broken in hypershift 4.13 with no plans to fix at the moment
	HypershiftSupportedVersions.Versions.Delete("4.11")
	klog.Infof("Hypershift Supported Versions: %+v", HypershiftSupportedVersions.Versions.List())
	return nil
}

func (m *jobManager) updateRosaVersions() error {
	vs, err := m.rClient.OCMClient.GetVersions(ocm.DefaultChannelGroup)
	if err != nil {
		return fmt.Errorf("Failed to retrieve versions: %s", err)
	}

	var versionList []string
	for _, v := range vs {
		// we only run in STS mode
		if !ocm.HasSTSSupport(v.RawID(), v.ChannelGroup()) {
			continue
		}
		valid, err := ocm.HasHostedCPSupport(v)
		if err != nil {
			return fmt.Errorf("failed to check HostedCP support: %v", err)
		}
		if !valid {
			continue
		}
		if !slices.Contains(versionList, v.RawID()) {
			versionList = append(versionList, v.RawID())
		}
	}

	if len(versionList) == 0 {
		return fmt.Errorf("Could not find versions for the provided channel-group: '%s'", ocm.DefaultChannelGroup)
	}

	m.rosaVersions.lock.Lock()
	defer m.rosaVersions.lock.Unlock()
	m.rosaVersions.versions = versionList
	return nil
}

func (m *jobManager) Start() error {
	m.started = time.Now()
	go wait.Forever(func() {
		if err := m.sync(); err != nil {
			klog.Infof("error during sync: %v", err)
			return
		}
	}, time.Minute*5)
	go wait.Forever(func() {
		if err := m.rosaSync(); err != nil {
			klog.Infof("error during rosa sync: %v", err)
			return
		}
	}, time.Minute)
	go wait.Forever(func() {
		if err := m.updateHypershiftSupportedVersions(); err != nil {
			klog.Warningf("error during updateSupportedVersions: %v", err)
		}
	}, time.Minute*5)
	go wait.Forever(func() {
		if err := m.updateRosaVersions(); err != nil {
			klog.Warningf("error during updateRosaVersions: %v", err)
		}
	}, time.Minute*5)
	return nil
}

func paramsToString(params map[string]string) string {
	var pairs []string
	for k, v := range params {
		if len(k) == 0 {
			continue
		}
		if len(v) == 0 {
			pairs = append(pairs, k)
			continue
		}
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

func (m *jobManager) rosaSync() error {
	start := time.Now()
	// wrap Observe function into inline function so that time.Since doesn't get immediately evauluated
	defer func() { rosaSyncTimeMetric.Observe(time.Since(start).Seconds()) }()
	klog.Infof("Getting ROSA clusters")
	clusterList, err := m.rClient.OCMClient.GetAllClusters(m.rClient.Creator)
	if err != nil {
		metrics.RecordError(errorRosaGetAll, m.errorMetric)
		klog.Warningf("Failed to get clusters: %v", err)
	}
	klog.Infof("Found %d rosa clusters", len(clusterList))

	m.rosaClusters.lock.RLock()
	defer m.rosaClusters.lock.RUnlock()
	clustersByID := map[string]*clustermgmtv1.Cluster{}
	for idx := range clusterList {
		cluster := clusterList[idx]
		if cluster.AWS().Tags() == nil || cluster.AWS().Tags()[trimTagName(utils.LaunchLabel)] != "true" {
			continue
		}
		previous := m.rosaClusters.clusters[cluster.ID()]

		switch cluster.State() {
		case clustermgmtv1.ClusterStateReady:
			if previous == nil || previous.State() != cluster.State() {
				// this prevents extra/incorrect metrics updates, but may miss an event if the cluster bot happens to restart right
				// before this event occurs
				if previous != nil {
					rosaReadyTimeMetric.Observe(time.Since(cluster.CreationTimestamp()).Minutes())
				}
				go func() {
					alreadyExists, err := m.addClusterAuthAndWait(cluster)
					if err != nil {
						// addClusterAuthAndWait records metrics itself
						klog.Errorf("Failed to add cluster auth: %v", err)
					}
					// don't renottify users on chat-bot restart
					if !alreadyExists {
						activeRosaIDs, err := m.rosaSecretClient.Get(context.TODO(), RosaClusterSecretName, metav1.GetOptions{})
						if err != nil {
							metrics.RecordError(errorRosaGetSecret, m.errorMetric)
							klog.Errorf("Failed to get `%s` secret: %v", RosaClusterSecretName, err)
						}
						// update cluster info to get console URL
						updatedCluster, err := m.rClient.OCMClient.GetCluster(cluster.ID(), m.rClient.Creator)
						if err != nil {
							metrics.RecordError(errorRosaGetSingle, m.errorMetric)
							klog.Errorf("Failed to get updated cluster info after deletion call: %v", err)
						} else {
							cluster = updatedCluster
						}
						m.rosaNotifierFn(cluster, string(activeRosaIDs.Data[cluster.ID()]))
						// update cluster list
						go m.rosaSync() // nolint:errcheck
					} else {
						klog.Infof("Cluster %s has existing auth; will not notify user", cluster.ID())
					}
				}()
			}
		case clustermgmtv1.ClusterStateError:
			if previous == nil || previous.State() != cluster.State() {
				// this prevents extra/incorrect metrics updates, but may miss an event if the cluster bot happens to restart right
				// before this event occurs
				if previous != nil {
					metrics.RecordError(errorRosaFailure, m.errorMetric)
				}
				klog.Infof("Reporting failure for cluster %s", cluster.ID())
				m.rosaNotifierFn(cluster, "")
			}
		}
		expiryTime, err := base64.RawStdEncoding.DecodeString(cluster.AWS().Tags()[utils.ExpiryTimeTag])
		if err != nil {
			klog.Errorf("Failed to base64 decode expiry time tag: %v", err)
		} else if parsedExpiryTime, err := time.Parse(time.RFC3339, string(expiryTime)); err == nil && parsedExpiryTime.Before(time.Now()) {
			if err := m.deleteCluster(cluster.ID()); err != nil {
				// deleteCluster function records metrics errors itself
				klog.Errorf("Failed to delete cluster %s due to expiry: %v", cluster.ID(), err)
			}
			updatedCluster, err := m.rClient.OCMClient.GetCluster(cluster.ID(), m.rClient.Creator)
			if err != nil {
				metrics.RecordError(errorRosaGetSingle, m.errorMetric)
				klog.Errorf("Failed to get updated cluster info after deletion call: %v", err)
			} else {
				cluster = updatedCluster
			}
		}
		clustersByID[cluster.ID()] = cluster
	}
	klog.Infof("Found %d chat-bot owned rosa clusters", len(clustersByID))

	activeRosaIDs, err := m.rosaSecretClient.Get(context.TODO(), RosaClusterSecretName, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		activeRosaIDs, err = m.rosaSecretClient.Create(context.TODO(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      RosaClusterSecretName,
				Namespace: "ci",
			},
			Data: map[string][]byte{},
			Type: corev1.SecretTypeOpaque,
		}, metav1.CreateOptions{})
	}
	if err != nil {
		metrics.RecordError(errorRosaGetSecret, m.errorMetric)
		return err
	}
	klog.Infof("Found %d entries in ROSA ID secret", len(activeRosaIDs.Data))

	var toDelete []string
	passwords := map[string]string{}
	for id, password := range activeRosaIDs.Data {
		if _, ok := clustersByID[id]; !ok {
			toDelete = append(toDelete, id)
		} else {
			passwords[id] = string(password)
		}
	}

	// temporarily use full lock to update cluster list
	m.rosaClusters.lock.RUnlock()
	m.rosaClusters.lock.Lock()
	m.rosaClusters.clusters = clustersByID
	m.rosaClusters.clusterPasswords = passwords
	m.rosaClusters.lock.Unlock()
	m.rosaClusters.lock.RLock()

	var deletedEntries []string
	var awsCleanupErrors []error
	for _, id := range toDelete {
		if err := m.removeAssociatedAWSResources(id); err != nil {
			// removeAssociatedAWSResources records error metrics itself
			awsCleanupErrors = append(awsCleanupErrors, err)
		} else {
			deletedEntries = append(deletedEntries, id)
		}
	}

	if len(deletedEntries) != 0 {
		klog.Infof("Removing %d stale entries from rosa ID list", len(deletedEntries))
		if err := utils.UpdateSecret(RosaClusterSecretName, m.rosaSecretClient, func(secret *corev1.Secret) {
			for _, id := range deletedEntries {
				klog.Infof("Removed %s from Rosa ID list", id)
				delete(secret.Data, id)
			}
		}); err != nil {
			metrics.RecordError(errorRosaUpdateSecret, m.errorMetric)
			return fmt.Errorf("Failed to update `%s` secret to remove stale clusters from list: %w", RosaClusterSecretName, err)
		}
	}

	return utilerrors.NewAggregate(awsCleanupErrors)
}

func (m *jobManager) sync() error {
	u, err := m.prowClient.Namespace(m.prowNamespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{
			utils.LaunchLabel: "true",
		}).String(),
	})
	if err != nil {
		return err
	}
	list := &prowapiv1.ProwJobList{}
	if err := prow.UnstructuredToObject(u, list); err != nil {
		return err
	}

	m.lock.Lock()
	defer m.lock.Unlock()

	now := time.Now()

	for _, job := range list.Items {
		previous := m.jobs[job.Name]

		value := job.Annotations["ci-chat-bot.openshift.io/jobInputs"]
		var inputs []JobInput
		if len(value) > 0 {
			if err := json.Unmarshal([]byte(value), &inputs); err != nil {
				klog.Warningf("Could not deserialize job input annotation from build %s: %v", job.Name, err)
			}
		}
		if len(inputs) == 0 {
			klog.Infof("No job inputs for %s", job.Name)
			continue
		}
		architecture := job.Annotations["release.openshift.io/architecture"]
		if len(architecture) == 0 {
			architecture = "amd64"
		}
		buildCluster := job.Annotations["release.openshift.io/buildCluster"]
		if len(buildCluster) == 0 {
			buildCluster = job.Spec.Cluster
		}
		var isOperator bool
		if job.Annotations["ci-chat-bot.openshift.io/IsOperator"] == "true" {
			isOperator = true
		}
		j := &Job{
			Name:               job.Name,
			State:              job.Status.State,
			URL:                job.Status.URL,
			OriginalMessage:    job.Annotations["ci-chat-bot.openshift.io/originalMessage"],
			Mode:               job.Annotations["ci-chat-bot.openshift.io/mode"],
			JobName:            job.Spec.Job,
			Platform:           job.Annotations["ci-chat-bot.openshift.io/platform"],
			Inputs:             inputs,
			RequestedBy:        job.Annotations["ci-chat-bot.openshift.io/user"],
			RequestedChannel:   job.Annotations["ci-chat-bot.openshift.io/channel"],
			RequestedAt:        job.CreationTimestamp.Time,
			Architecture:       architecture,
			BuildCluster:       buildCluster,
			IsOperator:         isOperator,
			OperatorBundleName: job.Annotations["ci-chat-bot.openshift.io/OperatorBundleName"],
		}

		var err error
		j.JobParams, err = utils.ParamsFromAnnotation(job.Annotations["ci-chat-bot.openshift.io/jobParams"])
		if err != nil {
			klog.Infof("Unable to unmarshal parameters from %s: %v", job.Name, err)
			continue
		}

		if expirationString := job.Annotations["ci-chat-bot.openshift.io/expires"]; len(expirationString) > 0 {
			if maxSeconds, err := strconv.Atoi(expirationString); err == nil && maxSeconds > 0 {
				j.ExpiresAt = job.CreationTimestamp.Add(time.Duration(maxSeconds) * time.Second)
			}
		}
		if j.ExpiresAt.IsZero() {
			j.ExpiresAt = job.CreationTimestamp.Time.Add(m.maxAge)
		}
		if job.Status.CompletionTime != nil {
			j.Complete = true
			j.ExpiresAt = job.Status.CompletionTime.Add(15 * time.Minute)
		}
		if j.ExpiresAt.Before(now) {
			continue
		}

		switch job.Status.State {
		case prowapiv1.FailureState:
			j.Failure = "job failed, see logs"

			m.jobs[job.Name] = j
			if previous == nil || previous.State != j.State {
				go m.finishedJob(*j)
			}
		case prowapiv1.SuccessState:
			j.Failure = ""

			m.jobs[job.Name] = j
			if previous == nil || previous.State != j.State {
				go m.finishedJob(*j)
			}

		case prowapiv1.TriggeredState, prowapiv1.PendingState, "":
			j.State = prowapiv1.PendingState
			j.Failure = ""

			if (j.Mode == JobTypeLaunch || j.Mode == JobTypeWorkflowLaunch) && (previous != nil && !previous.Complete) {
				if user := j.RequestedBy; len(user) > 0 {
					if _, ok := m.requests[user]; !ok {
						var inputStrings [][]string
						for _, input := range inputs {
							var current []string
							switch {
							case len(input.Version) > 0:
								current = append(current, input.Version)
							case len(input.Image) > 0:
								current = append(current, input.Image)
							}
							for _, ref := range input.Refs {
								for _, pull := range ref.Pulls {
									current = append(current, fmt.Sprintf("%s/%s#%d", ref.Org, ref.Repo, pull.Number))
								}
							}
							if len(current) > 0 {
								inputStrings = append(inputStrings, current)
							}
						}
						params, err := utils.ParamsFromAnnotation(job.Annotations["ci-chat-bot.openshift.io/jobParams"])
						if err != nil {
							klog.Infof("Unable to unmarshal parameters from %s: %v", job.Name, err)
							continue
						}

						m.requests[user] = &JobRequest{
							OriginalMessage: job.Annotations["ci-chat-bot.openshift.io/originalMessage"],

							User:         user,
							Name:         job.Name,
							JobName:      job.Spec.Job,
							Platform:     job.Annotations["ci-chat-bot.openshift.io/platform"],
							JobParams:    params,
							Inputs:       inputStrings,
							RequestedAt:  job.CreationTimestamp.Time,
							Channel:      job.Annotations["ci-chat-bot.openshift.io/channel"],
							Architecture: architecture,
						}
					}
				}
			}

			m.jobs[job.Name] = j
			if previous == nil || previous.State != j.State || !previous.IsComplete() {
				go m.handleJobStartup(*j, "sync")
			}
		}
	}

	// forget everything that is too old
	for _, job := range m.jobs {
		if job.ExpiresAt.Before(now) {
			klog.Infof("job %q is expired", job.Name)
			delete(m.jobs, job.Name)
		}
	}
	for _, req := range m.requests {
		if req.RequestedAt.Add(m.maxAge * 2).Before(now) {
			klog.Infof("request %q is expired", req.User)
			delete(m.requests, req.User)
		}
	}
	klog.Infof("Job sync complete, %d jobs and %d requests", len(m.jobs), len(m.requests))

	return nil
}

func (m *jobManager) SetNotifier(fn JobCallbackFunc) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.jobNotifierFn = fn
}

func (m *jobManager) SetRosaNotifier(fn RosaCallbackFunc) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.rosaNotifierFn = fn
}

func (m *jobManager) estimateCompletion(requestedAt time.Time) time.Duration {
	// find the median, or default to 30m
	var median time.Duration
	if l := len(m.recentStartEstimates); l > 0 {
		median = m.recentStartEstimates[l/2]
	}
	if median < time.Minute {
		median = 30 * time.Minute
	}

	if requestedAt.IsZero() {
		return median.Truncate(time.Second)
	}

	lastEstimate := median - time.Since(requestedAt)
	if lastEstimate < 0 {
		return time.Minute
	}
	return lastEstimate.Truncate(time.Second)
}

func (m *jobManager) ListJobs(user string, filters ListFilters) string {
	m.lock.Lock()
	defer m.lock.Unlock()

	var clusters []*Job
	var jobs []*Job
	var totalJobs int
	var runningClusters int
	for _, job := range m.jobs {
		if job.Mode == JobTypeLaunch || job.Mode == JobTypeWorkflowLaunch {
			if !job.Complete {
				runningClusters++
			}
			clusters = append(clusters, job)
		} else {
			totalJobs++
			if user == job.RequestedBy {
				jobs = append(jobs, job)
			}
		}
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
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].RequestedAt.Before(jobs[j].RequestedAt) {
			return true
		}
		if jobs[i].Name < jobs[j].Name {
			return true
		}
		return false
	})

	buf := &bytes.Buffer{}
	now := time.Now()
	if len(clusters) == 0 {
		fmt.Fprintf(buf, "No clusters up (start time is approximately %d minutes):\n\n", m.estimateCompletion(time.Time{})/time.Minute)
	} else {
		fmt.Fprintf(buf, "%d/%d clusters up (start time is approximately %d minutes):\n\n", runningClusters, m.maxClusters, m.estimateCompletion(time.Time{})/time.Minute)
		for _, job := range clusters {
			var jobInput JobInput
			if len(job.Inputs) > 0 {
				jobInput = job.Inputs[0]
			}
			if !((filters.Requestor == "" || filters.Requestor == job.RequestedBy) && (filters.Platform == "" || filters.Platform == job.Platform) && (filters.Version == "" || strings.Contains(jobInput.Version, filters.Version))) {
				continue
			}
			var details string
			if len(job.URL) > 0 {
				details = fmt.Sprintf(", <%s|view logs>", job.URL)
			}
			var imageOrVersion string
			var inputParts []string
			switch {
			case len(jobInput.Version) > 0:
				inputParts = append(inputParts, fmt.Sprintf("<https://%s.ocp.releases.ci.openshift.org/releasetag/%s|%s>", job.Architecture, url.PathEscape(jobInput.Version), jobInput.Version))
			case len(jobInput.Image) > 0:
				inputParts = append(inputParts, "(image)")
			}
			for _, ref := range jobInput.Refs {
				for _, pull := range ref.Pulls {
					inputParts = append(inputParts, fmt.Sprintf(" <https://github.com/%s/%s/pull/%d|%s/%s#%d>", url.PathEscape(ref.Org), url.PathEscape(ref.Repo), pull.Number, ref.Org, ref.Repo, pull.Number))
				}
			}
			imageOrVersion = strings.Join(inputParts, ",")

			// summarize the job parameters
			var options string
			params := make(map[string]string)
			for k, v := range job.JobParams {
				params[k] = v
			}
			if len(job.Platform) > 0 {
				params[job.Platform] = ""
			}
			if s := paramsToString(params); len(s) > 0 {
				options = fmt.Sprintf(" (%s)", s)
			}

			switch {
			case job.State == prowapiv1.SuccessState:
				fmt.Fprintf(buf, "• <@%s>%s - cluster has been shut down%s\n", job.RequestedBy, imageOrVersion, details)
			case job.State == prowapiv1.FailureState:
				fmt.Fprintf(buf, "• <@%s>%s%s - cluster failed to start%s\n", job.RequestedBy, imageOrVersion, options, details)
			case job.Complete:
				fmt.Fprintf(buf, "• <@%s>%s%s - cluster has requested shut down%s\n", job.RequestedBy, imageOrVersion, options, details)
			case len(job.Credentials) > 0:
				fmt.Fprintf(buf, "• <@%s>%s%s - available and will be torn down in %d minutes%s\n", job.RequestedBy, imageOrVersion, options, int(job.ExpiresAt.Sub(now)/time.Minute), details)
			case len(job.Failure) > 0:
				fmt.Fprintf(buf, "• <@%s>%s%s - failure: %s%s\n", job.RequestedBy, imageOrVersion, options, job.Failure, details)
			default:
				fmt.Fprintf(buf, "• <@%s>%s%s - starting, %d minutes elapsed%s\n", job.RequestedBy, imageOrVersion, options, int(now.Sub(job.RequestedAt)/time.Minute), details)
			}
		}
		fmt.Fprintf(buf, "\n")
	}

	if len(jobs) > 0 {
		fmt.Fprintf(buf, "Running jobs:\n\n")
		for _, job := range jobs {
			fmt.Fprintf(buf, "• %d minutes ago - ", int(now.Sub(job.RequestedAt)/time.Minute))
			switch {
			case job.State == prowapiv1.SuccessState:
				fmt.Fprint(buf, "*succeeded* ")
			case job.State == prowapiv1.FailureState:
				fmt.Fprint(buf, "*failed* ")
			case len(job.URL) > 0:
				fmt.Fprint(buf, "running ")
			default:
				fmt.Fprint(buf, "pending ")
			}
			var details string
			switch {
			case len(job.URL) > 0 && len(job.OriginalMessage) > 0:
				details = fmt.Sprintf("<%s|%s>", job.URL, utils.StripLinks(job.OriginalMessage))
			case len(job.URL) > 0:
				details = fmt.Sprintf("<%s|%s>", job.URL, job.JobName)
			case len(job.OriginalMessage) > 0:
				details = utils.StripLinks(job.OriginalMessage)
			default:
				details = job.JobName
			}
			if len(job.RequestedBy) > 0 {
				details += fmt.Sprintf(" <@%s>", job.RequestedBy)
			}
			fmt.Fprintln(buf, details)
		}
	} else if totalJobs > 0 {
		fmt.Fprintf(buf, "\nThere are %d test jobs being run by the bot right now\n", len(jobs))
	}

	m.rosaClusters.lock.RLock()
	defer m.rosaClusters.lock.RUnlock()
	fmt.Fprintf(buf, "%d/%d ROSA Clusters up:", len(m.rosaClusters.clusters), m.rosaClusterLimit)
	for _, cluster := range m.rosaClusters.clusters {
		if cluster.AWS().Tags() != nil {
			clusterUser := cluster.AWS().Tags()[utils.UserTag]
			switch cluster.State() {
			case clustermgmtv1.ClusterStateReady:
				expiryTime, err := base64.RawStdEncoding.DecodeString(cluster.AWS().Tags()[utils.ExpiryTimeTag])
				if err != nil {
					klog.Errorf("Failed to base64 decode expiry time tag: %v", err)
					fmt.Fprintf(buf, "\n<@%s> - ROSA Cluster `%s` is ready\n", clusterUser, cluster.Name())
				} else if parsedExpiryTime, err := time.Parse(time.RFC3339, string(expiryTime)); err != nil {
					klog.Errorf("Failed to parse expiry time: %v", err)
					fmt.Fprintf(buf, "\n<@%s> - ROSA Cluster `%s` is ready\n", clusterUser, cluster.Name())
				} else {
					fmt.Fprintf(buf, "\n<@%s> - ROSA Cluster `%s` is ready and will be torn down in %d minutes\n", clusterUser, cluster.Name(), int(parsedExpiryTime.Sub(now)/time.Minute))
				}
			case clustermgmtv1.ClusterStateInstalling:
				fmt.Fprintf(buf, "\n<@%s> - ROSA Cluster `%s` is starting; %d minutes have elapsed\n", clusterUser, cluster.Name(), int(time.Since(cluster.CreationTimestamp())/time.Minute))
			case clustermgmtv1.ClusterStateError:
				fmt.Fprintf(buf, "\n<@%s> - ROSA Cluster `%s` has experienced an error. Please run `done` to delete the cluster before starting a new one\n", clusterUser, cluster.Name())
			case clustermgmtv1.ClusterStateUninstalling:
				fmt.Fprintf(buf, "\n<@%s> - ROSA Cluster `%s` is uninstalling\n", clusterUser, cluster.Name())
			default:
				fmt.Fprintf(buf, "\n<@%s> - ROSA Cluster `%s` requested; %d minutes have elapsed\n", clusterUser, cluster.Name(), int(time.Since(cluster.CreationTimestamp())/time.Minute))
			}
		}
	}

	fmt.Fprintf(buf, "\nbot uptime is %.1f minutes", now.Sub(m.started).Seconds()/60)
	return buf.String()
}

func (m *jobManager) GetROSACluster(user string) (*clustermgmtv1.Cluster, string) {
	return m.getROSAClusterForUser(user)
}

func (m *jobManager) DescribeROSACluster(name string) (string, error) {
	return m.describeROSACluster(name)
}

func (m *jobManager) GetLaunchJob(user string) (*Job, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	existing, ok := m.requests[user]
	if !ok {
		return nil, fmt.Errorf("you haven't requested a cluster or your cluster expired")
	}
	if len(existing.Name) == 0 {
		return nil, fmt.Errorf("you are still on the waitlist")
	}
	job, ok := m.jobs[existing.Name]
	if !ok {
		return nil, fmt.Errorf("your cluster has expired and credentials are no longer available")
	}
	copied := *job
	copied.Inputs = make([]JobInput, len(job.Inputs))
	copy(copied.Inputs, job.Inputs)
	return &copied, nil
}

func versionForRefs(refs *prowapiv1.Refs) string {
	if refs == nil || len(refs.BaseRef) == 0 {
		return ""
	}
	if refs.BaseRef == "master" || refs.BaseRef == "main" {
		return fmt.Sprintf("%d.%d.0-0.latest", CurrentRelease.Major, CurrentRelease.Minor)
	}
	if m := reBranchVersion.FindStringSubmatch(refs.BaseRef); m != nil {
		return fmt.Sprintf("%s.0-0.latest", m[2])
	}
	return ""
}

func buildPullSpec(namespace, tagName, isName string) string {
	var delimiter = ":"
	if strings.HasPrefix(tagName, "sha256:") {
		delimiter = "@"
	}
	return fmt.Sprintf("registry.ci.openshift.org/%s/%s%s%s", namespace, isName, delimiter, tagName)
}

// ResolveImageOrVersion returns installSpec, tag name or version, runSpec, and error
func (m *jobManager) ResolveImageOrVersion(imageOrVersion, defaultImageOrVersion, architecture string) (string, string, string, error) {
	if len(strings.TrimSpace(imageOrVersion)) == 0 {
		if len(defaultImageOrVersion) == 0 {
			return "", "", "", nil
		}
		imageOrVersion = defaultImageOrVersion
	}

	unresolved := imageOrVersion
	if strings.Contains(unresolved, "/") {
		return unresolved, "", "", nil
	}

	type namespaceAndStream struct {
		Namespace   string
		Imagestream string
		ArchSuffix  string
	}

	imagestreams := []namespaceAndStream{}
	switch architecture {
	case "amd64":
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp", Imagestream: "release"})
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp", Imagestream: "4-dev-preview"})
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "origin", Imagestream: "release"})
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "origin", Imagestream: "scos-release"})
	case "arm64":
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp-arm64", Imagestream: "release-arm64", ArchSuffix: "-arm64"})
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp-arm64", Imagestream: "4-dev-preview-arm64", ArchSuffix: "-arm64"})
	case "multi":
		// the release-controller cannot assemble multi-arch release, so we must use the `art-latest` streams instead of `release-multi`
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp-multi", Imagestream: "4.13-art-latest-multi", ArchSuffix: "-multi"})
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp-multi", Imagestream: "4.12-art-latest-multi", ArchSuffix: "-multi"})
	default:
		return "", "", "", fmt.Errorf("Unsupported architecture: %s", architecture)
	}

	for _, nsAndStream := range imagestreams {
		ns := nsAndStream.Namespace
		isName := nsAndStream.Imagestream
		archSuffix := nsAndStream.ArchSuffix
		is, err := m.imageClient.ImageV1().ImageStreams(ns).Get(context.TODO(), isName, metav1.GetOptions{})
		if err != nil {
			continue
		}

		var amd64IS *imagev1.ImageStream
		if architecture != "amd64" && architecture != "multi" {
			amd64IS, err = m.imageClient.ImageV1().ImageStreams("ocp").Get(context.TODO(), strings.TrimSuffix(isName, archSuffix), metav1.GetOptions{})
			if err != nil {
				return "", "", "", fmt.Errorf("failed to get ocp release imagstream: %w", err)
			}
		}

		currentReleasePrefix := fmt.Sprintf("%d.%d", CurrentRelease.Major, CurrentRelease.Minor)
		if m := reMajorMinorVersion.FindStringSubmatch(unresolved); m != nil {
			if tag := findNewestImageSpecTagWithStream(is, fmt.Sprintf("%s.0-0.nightly%s", unresolved, archSuffix)); tag != nil {
				klog.Infof("Resolved major.minor %s to nightly tag %s", imageOrVersion, tag.Name)
				installSpec := buildPullSpec(ns, tag.Name, isName)
				runSpec := ""
				if architecture == "amd64" || architecture == "multi" {
					runSpec = installSpec
				} else {
					runTag := findNewestImageSpecTagWithStream(amd64IS, fmt.Sprintf("%s.0-0.nightly", unresolved))
					runSpec = buildPullSpec("ocp", runTag.Name, "release")
				}
				return installSpec, tag.Name, runSpec, nil
			}
			if tag := findNewestImageSpecTagWithStream(is, fmt.Sprintf("%s.0-0.ci%s", unresolved, archSuffix)); tag != nil {
				klog.Infof("Resolved major.minor %s to ci tag %s", imageOrVersion, tag.Name)
				installSpec := buildPullSpec(ns, tag.Name, isName)
				runSpec := ""
				if architecture == "amd64" || architecture == "multi" {
					runSpec = installSpec
				} else {
					runTag := findNewestImageSpecTagWithStream(amd64IS, fmt.Sprintf("%s.0-0.ci", unresolved))
					runSpec = buildPullSpec("ocp", runTag.Name, "release")
				}
				return installSpec, tag.Name, runSpec, nil
			}
			if tag := findNewestStableImageSpecTagBySemanticMajor(is, unresolved, architecture); tag != nil {
				klog.Infof("Resolved major.minor %s to semver tag %s", imageOrVersion, tag.Name)
				installSpec := buildPullSpec(ns, tag.Name, isName)
				runSpec := ""
				if architecture == "amd64" || architecture == "multi" {
					runSpec = installSpec
				} else {
					runTag := findNewestImageSpecTagWithStream(amd64IS, unresolved)
					runSpec = buildPullSpec("ocp", runTag.Name, "release")
				}
				return installSpec, tag.Name, runSpec, nil
			}
			return "", "", "", fmt.Errorf("no stable, official prerelease, or nightly version published yet for %s", imageOrVersion)
		} else if unresolved == "nightly" {
			unresolved = fmt.Sprintf("%s.0-0.nightly%s", currentReleasePrefix, archSuffix)
		} else if unresolved == "ci" {
			unresolved = fmt.Sprintf("%s.0-0.ci%s", currentReleasePrefix, archSuffix)
		} else if unresolved == "prerelease" {
			unresolved = fmt.Sprintf("%s.0-0.ci%s", currentReleasePrefix, archSuffix)
		}

		if tag, name := findImageStatusTag(is, unresolved); tag != nil {
			klog.Infof("Resolved %s to image %s", imageOrVersion, tag.Image)
			// identify nightly stream for runspec if not amd64
			installSpec := buildPullSpec(ns, tag.Image, isName)
			runSpec := ""
			if architecture == "amd64" || architecture == "multi" {
				runSpec = installSpec
			} else {
				// if it's a nightly, just get the latest image from the nightly stream
				if strings.Contains(unresolved, "nightly") {
					// identify major and minor and use corresponding image
					ver, err := semver.ParseTolerant(unresolved)
					if err != nil {
						return "", "", "", fmt.Errorf("failed to identify semver for image %s: %w", tag.Image, err)
					}
					runTag := findNewestImageSpecTagWithStream(amd64IS, fmt.Sprintf("4.%d.0-0.nightly", ver.Minor))
					runSpec = buildPullSpec("ocp", runTag.Name, "release")
				} else {
					runTag, _ := findImageStatusTag(amd64IS, unresolved)
					runSpec = buildPullSpec("ocp", runTag.Image, "release")
				}
			}
			return installSpec, name, runSpec, nil
		}

		if tag := findNewestImageSpecTagWithStream(is, unresolved); tag != nil {
			klog.Infof("Resolved %s to tag %s", imageOrVersion, tag.Name)
			// identify nightly stream for runspec if not amd64
			installSpec := buildPullSpec(ns, tag.Name, isName)
			runSpec := ""
			if architecture == "amd64" || architecture == "multi" {
				runSpec = installSpec
			} else {
				// if it's a nightly, just get the latest image from the nightly stream
				if strings.Contains(unresolved, "nightly") {
					// identify major and minor and use corresponding image
					ver, err := semver.ParseTolerant(unresolved)
					if err != nil {
						return "", "", "", fmt.Errorf("failed to identify semver for image %s: %w", tag.Name, err)
					}
					runTag := findNewestImageSpecTagWithStream(amd64IS, fmt.Sprintf("4.%d.0-0.nightly", ver.Minor))
					runSpec = buildPullSpec("ocp", runTag.Name, "release")
				} else {
					runTag := findNewestImageSpecTagWithStream(amd64IS, unresolved)
					runSpec = buildPullSpec("ocp", runTag.Name, "release")
				}
			}
			return installSpec, tag.Name, runSpec, nil
		}
	}

	errMsg := fmt.Errorf("unable to find a release matching %q on https://%s.ocp.releases.ci.openshift.org", imageOrVersion, architecture)
	if architecture == "amd64" {
		errMsg = fmt.Errorf("%s or https://amd64.origin.releases.ci.openshift.org", errMsg)
	}
	return "", "", "", errMsg
}

func findNewestStableImageSpecTagBySemanticMajor(is *imagev1.ImageStream, majorMinor, architecture string) *imagev1.TagReference {
	base, err := semver.ParseTolerant(majorMinor)
	if err != nil {
		return nil
	}
	archSuffix := ""
	if architecture == "arm64" {
		archSuffix = "-arm64"
	}
	var candidates semver.Versions
	for _, tag := range is.Spec.Tags {
		if tag.Annotations["release.openshift.io/name"] != fmt.Sprintf("4-stable%s", archSuffix) {
			continue
		}
		v, err := semver.ParseTolerant(tag.Name)
		if err != nil {
			continue
		}
		if v.Major != base.Major || v.Minor != base.Minor {
			continue
		}
		candidates = append(candidates, v)
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Sort(candidates)
	tagName := candidates[len(candidates)-1].String()
	for i, tag := range is.Spec.Tags {
		if tag.Name == tagName {
			return &is.Spec.Tags[i]
		}
	}
	return nil
}

func findNewestImageSpecTagWithStream(is *imagev1.ImageStream, name string) *imagev1.TagReference {
	var newest *imagev1.TagReference
	for i := range is.Spec.Tags {
		tag := &is.Spec.Tags[i]
		if tag.Annotations["release.openshift.io/phase"] != "Accepted" {
			continue
		}
		if tag.Annotations["release.openshift.io/name"] != name {
			continue
		}
		if newest == nil || newest.Annotations["release.openshift.io/creationTimestamp"] < tag.Annotations["release.openshift.io/creationTimestamp"] {
			newest = tag
		}
	}
	return newest
}

func findImageStatusTag(is *imagev1.ImageStream, name string) (*imagev1.TagEvent, string) {
	for _, tag := range is.Status.Tags {
		if tag.Tag == name {
			if len(tag.Items) == 0 {
				return nil, ""
			}
			return &tag.Items[0], tag.Tag
		}
	}
	return nil, ""
}
func (m *jobManager) GetWorkflowConfig() *WorkflowConfig {
	return m.workflowConfig
}

func (m *jobManager) LookupInputs(inputs []string, architecture string) (string, error) {
	jobInputs, defaultedVersion, err := m.lookupInputs([][]string{inputs}, architecture)
	if err != nil {
		return "", err
	}
	// len(inputs) must match len(JobInputs), so if lookupInputs defaulted a version, we need to update inputs
	if defaultedVersion != "" {
		inputs = []string{defaultedVersion}
	}
	var out []string
	for i, job := range jobInputs {
		if len(job.Refs) > 0 {
			out = append(out, fmt.Sprintf("`%s` will build from PRs", inputs[i]))
			continue
		}
		if len(job.Version) == 0 {
			out = append(out, fmt.Sprintf("`%s` uses a release image at `%s`", inputs[i], job.Image))
			continue
		}
		if len(job.Image) == 0 {
			out = append(out, fmt.Sprintf("`%s` uses version `%s`", inputs[i], job.Version))
			continue
		}
		out = append(out, fmt.Sprintf("`%s` launches version <https://%s.ocp.releases.ci.openshift.org/releasetag/%s|%s>", inputs[i], architecture, job.Version, job.Version))
	}
	return strings.Join(out, "\n"), nil
}

func (m *jobManager) lookupInputs(inputs [][]string, architecture string) ([]JobInput, string, error) {
	// LookupInputs needs len(inputs) to match len(JobInputs), so we need to return the defaulted version for it
	defaultedVersion := ""
	// default lookups to "nightly"
	if len(inputs) == 0 || (len(inputs) == 1 && len(inputs[0]) == 0) {
		_, version, _, err := m.ResolveImageOrVersion("nightly", "", architecture)
		if err != nil {
			return nil, "", err
		}
		inputs = [][]string{{version}}
		defaultedVersion = version
	}

	var jobInputs []JobInput
	for _, input := range inputs {
		var jobInput JobInput
		for _, part := range input {
			// if the user provided a pull spec (org/repo#number) we'll build from that
			pr, err := m.ResolveAsPullRequest(part)
			if err != nil {
				return nil, defaultedVersion, err
			}
			if pr != nil {
				var existing bool
				for i, ref := range jobInput.Refs {
					if ref.Org == pr.Org && ref.Repo == pr.Repo {
						jobInput.Refs[i].Pulls = append(jobInput.Refs[i].Pulls, pr.Pulls...)
						existing = true
						break
					}
				}
				if !existing {
					jobInput.Refs = append(jobInput.Refs, *pr)
				}
			} else {
				// otherwise, resolve as a semantic version (as a tag on the release image stream) or as an image
				image, version, runImage, err := m.ResolveImageOrVersion(part, "", architecture)
				if err != nil {
					return nil, defaultedVersion, err
				}
				if len(image) == 0 {
					return nil, defaultedVersion, fmt.Errorf("unable to resolve %q to an image", part)
				}
				if len(jobInput.Image) > 0 {
					return nil, defaultedVersion, fmt.Errorf("only one image or version may be specified in a list of installs")
				}
				if architecture == "arm64" && (len(runImage) == 0 || len(version) == 0) {
					return nil, defaultedVersion, fmt.Errorf("only version numbers (like: 4.11.0) may be used for arm64 based clusters")
				}
				jobInput.Image = image
				jobInput.Version = version
				jobInput.RunImage = runImage
			}
		}
		if len(jobInput.Version) == 0 && len(jobInput.Refs) > 0 {
			jobInput.Version = versionForRefs(&jobInput.Refs[0])
		}
		jobInputs = append(jobInputs, jobInput)
	}
	return jobInputs, defaultedVersion, nil
}

func (m *jobManager) ResolveAsPullRequest(spec string) (*prowapiv1.Refs, error) {
	var parts []string
	switch {
	case strings.HasPrefix(spec, "https://github.com/"):
		if u, err := url.Parse(spec); err == nil {
			path := strings.Trim(u.Path, "/")
			if segments := strings.Split(path, "/"); len(segments) == 4 && segments[2] == "pull" {
				parts = []string{
					strings.Join(segments[:2], "/"),
					segments[3],
				}
			}
		}
	case strings.Contains(spec, "#"):
		parts = strings.SplitN(spec, "#", 2)
	}
	if len(parts) != 2 {
		return nil, nil
	}
	locationParts := strings.Split(parts[0], "/")
	if len(locationParts) != 2 || len(locationParts[0]) == 0 || len(locationParts[1]) == 0 {
		return nil, fmt.Errorf("when specifying a pull request, you must provide ORG/REPO#NUMBER")
	}
	num, err := strconv.Atoi(parts[1])
	if err != nil || num < 1 {
		return nil, fmt.Errorf("when specifying a pull request, you must provide ORG/REPO#NUMBER")
	}

	pr, err := m.githubClient.GetPullRequest(url.PathEscape(locationParts[0]), url.PathEscape(locationParts[1]), num)
	if err != nil {
		return nil, fmt.Errorf("unable to lookup pull request %s: %v", spec, err)
	}

	if pr.Merged {
		return nil, fmt.Errorf("pull request %s has already been merged to %s", spec, pr.Base.Ref)
	}
	if pr.Mergable != nil && !*pr.Mergable {
		return nil, fmt.Errorf("pull request %s needs to be rebased to branch %s", spec, pr.Base.Ref)
	}

	owner := m.forcePROwner
	if len(owner) == 0 {
		owner = pr.User.Login
	}

	baseRefSHA, err := m.githubClient.GetRef(url.PathEscape(locationParts[0]), url.PathEscape(locationParts[1]), "heads/"+pr.Base.Ref)
	if err != nil {
		return nil, fmt.Errorf("unable to lookup pull request ref: %v", err)
	}

	return &prowapiv1.Refs{
		Org:  locationParts[0],
		Repo: locationParts[1],

		BaseRef: pr.Base.Ref,
		BaseSHA: baseRefSHA,

		Pulls: []prowapiv1.Pull{
			{
				Number: num,
				SHA:    pr.Head.SHA,
				Author: owner,
			},
		},
	}, nil
}

func (m *jobManager) resolveToJob(req *JobRequest) (*Job, error) {
	user := req.User
	if len(user) == 0 {
		return nil, fmt.Errorf("must specify the name of the user who requested this cluster")
	}

	if len(req.Type) == 0 {
		req.Type = JobTypeBuild
	}

	req.RequestedAt = time.Now()
	name := fmt.Sprintf("%s%s", m.clusterPrefix, req.RequestedAt.UTC().Format("2006-01-02-150405.9999"))
	req.Name = name

	job := &Job{
		OriginalMessage: req.OriginalMessage,
		Name:            name,
		State:           prowapiv1.PendingState,

		Platform:  req.Platform,
		JobParams: req.JobParams,

		RequestedBy:      user,
		RequesterUserID:  req.UserName,
		RequestedChannel: req.Channel,
		RequestedAt:      req.RequestedAt,

		ExpiresAt: req.RequestedAt.Add(m.maxAge),

		Architecture: req.Architecture,
		WorkflowName: req.WorkflowName,
	}

	jobInputs, _, err := m.lookupInputs(req.Inputs, job.Architecture)
	if err != nil {
		return nil, err
	}

	if req.Platform == "hypershift-hosted" {
		HypershiftSupportedVersions.Mu.RLock()
		for _, input := range jobInputs {
			if input.Version != "" {
				var isValidVersion bool
				for version := range HypershiftSupportedVersions.Versions {
					if strings.HasPrefix(input.Version, version) {
						isValidVersion = true
						break
					}
				}
				if !isValidVersion {
					HypershiftSupportedVersions.Mu.RUnlock()
					return nil, fmt.Errorf("hypershift currently only supports the following releases: %v", HypershiftSupportedVersions.Versions.List())
				}
			}
		}
		HypershiftSupportedVersions.Mu.RUnlock()
	}

	switch req.Type {
	case JobTypeBuild:
		if req.Architecture != "amd64" {
			return nil, fmt.Errorf("builds are not currently supported for non-amd64 releases")
		}
		var prs int
		for _, input := range jobInputs {
			for _, ref := range input.Refs {
				prs += len(ref.Pulls)
			}
		}
		if len(jobInputs) != 1 || prs == 0 {
			return nil, fmt.Errorf("at least one pull request is required to build a release image")
		}
		job.Mode = JobTypeBuild
	case JobTypeInstall:
		if req.Architecture != "amd64" {
			for _, input := range jobInputs {
				for _, ref := range input.Refs {
					if len(ref.Pulls) != 0 {
						return nil, fmt.Errorf("launching releases built from PRs is not currently supported for non-amd64 releases")
					}
				}
			}
		}
		if len(jobInputs) != 1 {
			return nil, fmt.Errorf("launching a cluster requires one image, version, or pull request")
		}
		if len(req.Platform) == 0 {
			return nil, fmt.Errorf("platform must be set when launching clusters")
		}
		job.Mode = JobTypeLaunch
	case JobTypeUpgrade:
		if req.Architecture != "amd64" {
			return nil, fmt.Errorf("upgrade tests are not currently supported for non-amd64 releases")
		}
		if len(jobInputs) != 2 {
			return nil, fmt.Errorf("upgrading a cluster requires two images, versions, or pull requests")
		}
		if len(req.Platform) == 0 {
			return nil, fmt.Errorf("platform must be set when upgrading clusters")
		}
		job.Mode = JobTypeUpgrade
		if len(job.JobParams["test"]) == 0 {
			return nil, fmt.Errorf("a test type is required for upgrading, default is e2e-upgrade")
		}
	case JobTypeTest:
		if req.Architecture != "amd64" {
			return nil, fmt.Errorf("tests are not currently supported for non-amd64 releases")
		}
		if len(jobInputs) != 1 {
			return nil, fmt.Errorf("launching a cluster requires one image, version, or pull request")
		}
		if len(req.Platform) == 0 {
			return nil, fmt.Errorf("platform must be set when testing clusters")
		}
		if len(job.JobParams["test"]) == 0 {
			return nil, fmt.Errorf("a test type is required for testing, see help")
		}
		job.Mode = JobTypeTest
	case JobTypeWorkflowUpgrade:
		if req.Architecture != "amd64" {
			return nil, fmt.Errorf("workflow upgrades are not currently supported for non-amd64 releases")
		}
		if len(jobInputs) != 2 {
			return nil, fmt.Errorf("upgrade test requires two images, versions, or pull requests")
		}
		if len(req.Platform) == 0 {
			return nil, fmt.Errorf("platform must be set when launching clusters")
		}
		job.Mode = JobTypeWorkflowUpgrade
	case JobTypeWorkflowLaunch:
		if req.Architecture != "amd64" {
			return nil, fmt.Errorf("workflow launches are not currently supported for non-amd64 releases")
		}
		if len(jobInputs) != 1 {
			return nil, fmt.Errorf("launching a cluster requires one image, version, or pull request")
		}
		if len(req.Platform) == 0 {
			return nil, fmt.Errorf("platform must be set when launching clusters")
		}
		job.Mode = JobTypeWorkflowLaunch
	default:
		return nil, fmt.Errorf("unexpected job type: %q", req.Type)
	}
	job.Inputs = jobInputs

	return job, nil
}

func multistageParamsForPlatform(platform string) sets.String {
	params := sets.NewString()
	for param, env := range MultistageParameters {
		if env.Platforms.Has(platform) {
			params.Insert(param)
		}
	}
	return params
}

func multistageNameFromParams(params map[string]string, platform, jobType string) (string, error) {
	if jobType == JobTypeWorkflowLaunch || jobType == JobTypeBuild {
		return "launch", nil
	}
	if jobType == JobTypeWorkflowUpgrade {
		return "upgrade", nil
	}
	var prefix string
	switch jobType {
	case JobTypeLaunch:
		prefix = "launch"
	case JobTypeTest:
		prefix = "e2e"
	case JobTypeUpgrade:
		prefix = "upgrade"
	default:
		return "", fmt.Errorf("Unknown job type %s", jobType)
	}
	_, okTest := params["test"]
	_, okNoSpot := params["no-spot"]
	if len(params) == 0 || (len(params) == 1 && (okTest || okNoSpot)) {
		return prefix, nil
	}
	platformParams := multistageParamsForPlatform(platform)
	variants := sets.NewString()
	for k := range params {
		if utils.Contains(SupportedParameters, k) && !platformParams.Has(k) && k != "test" && k != "bundle" && k != "no-spot" { // we only need parameters that are not configured via multistage env vars
			variants.Insert(k)
		}
	}
	if len(variants) == 0 {
		return prefix, nil
	}
	return fmt.Sprintf("%s-%s", prefix, strings.Join(variants.List(), "-")), nil
}

func configContainsVariant(params map[string]string, platform, unresolvedConfig, jobType string) (bool, string, error) {
	if jobType == JobTypeWorkflowLaunch {
		return true, "launch", nil
	}
	name, err := multistageNameFromParams(params, platform, jobType)
	if err != nil {
		return false, "", err
	}
	var config citools.ReleaseBuildConfiguration
	if err := yaml.Unmarshal([]byte(unresolvedConfig), &config); err != nil {
		return false, "", fmt.Errorf("failed to unmarshal CONFIG_SPEC: %w", err)
	}
	for _, test := range config.Tests {
		if test.As == name {
			return true, name, nil
		}
	}
	// most e2e jobs will be simply be applied on top of launch jobs; specific jobs for e2e will be only for non-standard tests
	if jobType == JobTypeTest && testStepForPlatform(platform) != "" {
		name, err := multistageNameFromParams(params, platform, JobTypeLaunch)
		if err != nil {
			return false, "", err
		}
		for _, test := range config.Tests {
			if test.As == name {
				return true, name, nil
			}
		}
	}
	return false, "", nil
}

// TODO remove duplicated code
func (m *jobManager) CheckValidJobConfiguration(req *JobRequest) error {
	job, err := m.resolveToJob(req)
	if err != nil {
		return err
	}
	// try to pick a job that matches the install version, if we can, otherwise use the first that
	// matches us (we can do better)
	var prowJob *prowapiv1.ProwJob
	jobType := JobTypeLaunch
	if req.Type == JobTypeWorkflowUpgrade {
		jobType = JobTypeUpgrade
	}
	selector := labels.Set{"job-env": req.Platform, "job-type": jobType, "job-architecture": req.Architecture} // TODO: handle versioned variants better
	if len(job.Inputs[0].Version) > 0 {
		if v, err := semver.ParseTolerant(job.Inputs[0].Version); err == nil {
			withRelease := labels.Merge(selector, labels.Set{"job-release": fmt.Sprintf("%d.%d", v.Major, v.Minor)})
			prowJob, _ = prow.JobForLabels(m.prowConfigLoader, labels.SelectorFromSet(withRelease))
		}
	}
	if prowJob == nil {
		architectureLabel := req.Architecture
		// multiarch image launches use amd64 jobs
		if architectureLabel == "multi" {
			architectureLabel = "amd64"
		}
		selector := labels.Set{"job-env": req.Platform, "job-type": JobTypeLaunch, "config-type": "modern", "job-architecture": architectureLabel} // these jobs will only contain configs using non-deprecated features
		prowJob, _ = prow.JobForLabels(m.prowConfigLoader, labels.SelectorFromSet(selector))
		if prowJob != nil {
			if sourceEnv, _, ok := firstEnvVar(prowJob.Spec.PodSpec, "UNRESOLVED_CONFIG"); ok { // all multistage configs will be unresolved
				configHasVariant, _, err := configContainsVariant(req.JobParams, req.Platform, sourceEnv.Value, job.Mode)
				if err != nil {
					return err
				}
				// if the config does not contain the wanted variant, reset prowjob to cause configuration error
				if !configHasVariant {
					prowJob = nil
				}
			}
		}
	}
	if prowJob == nil {
		return fmt.Errorf("configuration error, unable to find prow job matching %s with parameters=%v", selector, paramsToString(job.JobParams))
	}
	return nil
}

func (m *jobManager) LaunchJobForUser(req *JobRequest) (string, error) {
	if cluster, _ := m.getROSAClusterForUser(req.User); cluster != nil {
		return "", fmt.Errorf("you have already requested a cluster via the `rosa create` command; %d minutes have elapsed", int(time.Since(cluster.CreationTimestamp())/time.Minute))
	}

	job, err := m.resolveToJob(req)
	if err != nil {
		return "", err
	}

	// try to pick a job that matches the install version, if we can, otherwise use the first that
	// matches us (we can do better)
	var prowJob *prowapiv1.ProwJob
	jobType := JobTypeLaunch
	if req.Type == JobTypeWorkflowUpgrade {
		jobType = JobTypeUpgrade
	}
	selector := labels.Set{"job-env": req.Platform, "job-type": jobType, "job-architecture": req.Architecture} // TODO: handle versioned variants better
	if len(job.Inputs[0].Version) > 0 {
		if v, err := semver.ParseTolerant(job.Inputs[0].Version); err == nil {
			withRelease := labels.Merge(selector, labels.Set{"job-release": fmt.Sprintf("%d.%d", v.Major, v.Minor)})
			prowJob, _ = prow.JobForLabels(m.prowConfigLoader, labels.SelectorFromSet(withRelease))
		}
	}
	if prowJob == nil {
		architectureLabel := req.Architecture
		// multiarch image launches use amd64 jobs
		if architectureLabel == "multi" {
			architectureLabel = "amd64"
		}
		selector := labels.Set{"job-env": req.Platform, "job-type": JobTypeLaunch, "config-type": "modern", "job-architecture": architectureLabel} // these jobs will only contain configs using non-deprecated features
		prowJob, _ = prow.JobForLabels(m.prowConfigLoader, labels.SelectorFromSet(selector))
		if prowJob != nil {
			if sourceEnv, _, ok := firstEnvVar(prowJob.Spec.PodSpec, "UNRESOLVED_CONFIG"); ok { // all multistage configs will be unresolved
				configHasVariant, _, err := configContainsVariant(req.JobParams, req.Platform, sourceEnv.Value, job.Mode)
				if err != nil {
					return "", err
				}
				// if the config does not contain the wanted variant, reset prowjob to cause configuration error
				if !configHasVariant {
					prowJob = nil
				}
			}
		}
	}
	if prowJob == nil {
		return "", fmt.Errorf("configuration error, unable to find prow job matching %s with parameters=%v", selector, paramsToString(job.JobParams))
	}
	job.JobName = prowJob.Spec.Job
	job.BuildCluster = prowJob.Spec.Cluster

	klog.Infof("Job %q requested by user %q with mode %s prow job %s(%s) - params=%s, inputs=%#v", job.Name, req.User, job.Mode, job.JobName, job.BuildCluster, paramsToString(job.JobParams), job.Inputs)

	// check what leases are available for platform
	if req.Architecture == "amd64" && m.lClient != nil {
		switch req.Platform {
		case "aws":
			metrics1, err := m.lClient.Metrics("aws-quota-slice")
			if err != nil {
				return "", fmt.Errorf("failed to get metrics for `aws` leases: %v", err)
			}
			metrics2, err := m.lClient.Metrics("aws-2-quota-slice")
			if err != nil {
				return "", fmt.Errorf("failed to get metrics for `aws-2` leases: %v", err)
			}
			if metrics2.Free > metrics1.Free {
				job.UseSecondaryAccount = true
			}
		case "azure":
			metrics1, err := m.lClient.Metrics("azure4-quota-slice")
			if err != nil {
				return "", fmt.Errorf("failed to get metrics for `azure` leases: %v", err)
			}
			metrics2, err := m.lClient.Metrics("azure-2-quota-slice")
			if err != nil {
				return "", fmt.Errorf("failed to get metrics for `azure-2` leases: %v", err)
			}
			if metrics2.Free > metrics1.Free {
				job.UseSecondaryAccount = true
			}
		case "gcp":
			metrics1, err := m.lClient.Metrics("gcp-quota-slice")
			if err != nil {
				return "", fmt.Errorf("failed to get metrics for `gcp` leases: %v", err)
			}
			metrics2, err := m.lClient.Metrics("gcp-openshift-gce-devel-ci-2-quota-slice")
			if err != nil {
				return "", fmt.Errorf("failed to get metrics for `gcp-openshift-gce-devel-ci-2` leases: %v", err)
			}
			if metrics2.Free > metrics1.Free {
				job.UseSecondaryAccount = true
			}
		}
	}

	msg, err := func() (string, error) {
		m.lock.Lock()
		defer m.lock.Unlock()

		user := req.User
		if job.Mode == JobTypeLaunch || job.Mode == JobTypeWorkflowLaunch {
			existing, ok := m.requests[user]
			if ok {
				if len(existing.Name) == 0 {
					klog.Infof("user %q already requested cluster", user)
					return "", fmt.Errorf("you have already requested a cluster and it should be ready in ~ %d minutes", m.estimateCompletion(existing.RequestedAt)/time.Minute)
				}
				if job, ok := m.jobs[existing.Name]; ok {
					if len(job.Credentials) > 0 {
						klog.Infof("user %q cluster is already up", user)
						return "your cluster is already running, see your credentials again with the 'auth' command", nil
					}
					if len(job.Failure) == 0 {
						klog.Infof("user %q cluster has no credentials yet", user)
						return "", fmt.Errorf("you have already requested a cluster and it should be ready in ~ %d minutes", m.estimateCompletion(existing.RequestedAt)/time.Minute)
					}

					klog.Infof("user %q cluster failed, allowing them to request another", user)
					delete(m.jobs, existing.Name)
					delete(m.requests, user)
				}
			}
			m.requests[user] = req

			launchedClusters := 0
			for _, job := range m.jobs {
				if job != nil && (job.Mode == JobTypeLaunch || job.Mode == JobTypeWorkflowLaunch) && !job.Complete && len(job.Failure) == 0 {
					launchedClusters++
				}
			}
			if launchedClusters >= m.maxClusters {
				klog.Infof("user %q is will have to wait", user)
				var waitUntil time.Time
				for _, c := range m.jobs {
					if c == nil || (c.Mode != JobTypeLaunch && c.Mode != JobTypeWorkflowLaunch) {
						continue
					}
					if waitUntil.Before(c.ExpiresAt) {
						waitUntil = c.ExpiresAt
					}
				}
				minutes := time.Until(waitUntil).Minutes()
				if minutes < 1 {
					return "", fmt.Errorf("no clusters are currently available, unable to estimate when next cluster will be free")
				}
				return "", fmt.Errorf("no clusters are currently available, next slot available in %d minutes", int(math.Ceil(minutes)))
			}
		} else {
			running := 0
			for _, job := range m.jobs {
				if job != nil && job.Mode != JobTypeLaunch && job.Mode != JobTypeWorkflowLaunch && job.RequestedBy == user {
					running++
				}
			}
			if running > maxJobsPerUser {
				return "", fmt.Errorf("you can't have more than %d running jobs at a time", maxJobsPerUser)
			}
		}
		m.jobs[job.Name] = job
		klog.Infof("Job %q starting cluster for %q", job.Name, user)
		return "", nil
	}()
	if err != nil || len(msg) > 0 {
		return msg, err
	}

	prowJobUrl, err := m.newJob(job)
	if err != nil {
		return "", fmt.Errorf("the requested job cannot be started: %v", err)
	}

	go m.handleJobStartup(*job, "start")

	msg = ""
	if UseSpotInstances(job) {
		msg = fmt.Sprintf("%s\nThis AWS cluster will use Spot instances for the worker nodes.", msg)
		msg = fmt.Sprintf("%s This means that worker nodes may unexpectedly disappear, but will be replaced automatically.", msg)
		msg = fmt.Sprintf("%s If your workload cannot tolerate disruptions, add the `no-spot` option to the options argument when launching your cluster.", msg)
		msg = fmt.Sprintf("%s For more information on Spot instances, see this blog post: https://cloud.redhat.com/blog/a-guide-to-red-hat-openshift-and-aws-spot-instances.\n\n", msg)
	}
	if job.Platform == "hypershift-hosted" {
		msg = fmt.Sprintf("%s\nI noticed that you've created a `hypershift-hosted` cluster.  Next time, you might want to give ROSA's hypershift a try.", msg)
		msg = fmt.Sprintf("%s  You can launch a cluster with: `rosa create <version> [duration]`.  See the `help` message for more information.\n", msg)
		msg = fmt.Sprintf("%s\nThis cluster is being launched with a <https://hypershift-docs.netlify.app/|hosted control plane (hypershift)>.", msg)
		msg = fmt.Sprintf("%s This means that the control plane will run as pods (not virtual machines) on another cluster managed by DPTP; also by default there is 1 worker node.", msg)
		msg = fmt.Sprintf("%s This has the advantage of much faster startup times and lower costs.", msg)
		msg = fmt.Sprintf("%s However, if you are testing specific functionality relating to the control plane in the release version you provided or you require", msg)
		msg = fmt.Sprintf("%s multiple worker nodes, please end abort this launch with `done` and launch a cluster using another platform such as `aws` or `gcp`", msg)
		msg = fmt.Sprintf("%s (e.g. `launch 4.13 aws`).\n\n", msg)
	}

	if job.Mode == JobTypeLaunch || job.Mode == JobTypeWorkflowLaunch {
		msg = fmt.Sprintf("%sa <%s|cluster is being created>", msg, prowJobUrl)
		if job.IsOperator {
			msg = fmt.Sprintf("%s - On completion of the creation of the cluster, your optional operator will begin installation", msg)
			if job.OperatorBundleName != "" {
				msg = fmt.Sprintf("%s using the configuration for the `%s` bundle", msg, job.OperatorBundleName)
			}
			msg = fmt.Sprintf("%s. I'll send you the credentials once both the cluster and the operator are ready", msg)
		} else {
			msg = fmt.Sprintf("%s - I'll send you the credentials when the cluster is ready.", msg)
		}
		return "", errors.New(msg)
	}
	return "", fmt.Errorf("%s<%s|job> started, you will be notified on completion", msg, prowJobUrl)
}

func (m *jobManager) clusterDetailsForUser(user string) (string, string, error) {
	if len(user) == 0 {
		return "", "", fmt.Errorf("must specify the name of the user who requested this cluster")
	}

	m.lock.Lock()
	defer m.lock.Unlock()

	existing, ok := m.requests[user]
	if !ok || len(existing.Name) == 0 {
		return "", "", fmt.Errorf("no cluster has been requested by you")
	}
	job, ok := m.jobs[existing.Name]
	if !ok || len(job.BuildCluster) == 0 {
		return "", "", fmt.Errorf("unable to determine build cluster for your job")
	}
	return existing.Name, job.BuildCluster, nil
}

func (m *jobManager) TerminateJobForUser(user string) (string, error) {
	if cluster, _ := m.getROSAClusterForUser(user); cluster != nil {
		if err := m.deleteCluster(cluster.ID()); err != nil {
			return "", fmt.Errorf("failed to terminate ROSA cluster `%s`: %v", cluster.ID(), err)
		} else {
			// resync clusters to update cluster state
			go m.rosaSync() //nolint:errcheck
			return fmt.Sprintf("Cluster `%s` successfully marked for deletion", cluster.Name()), nil
		}
	}

	name, cluster, err := m.clusterDetailsForUser(user)
	if err != nil {
		return "", err
	}

	if err := m.stopJob(name, cluster); err != nil {
		return "", fmt.Errorf("unable to terminate: %v", err)
	}

	m.lock.Lock()
	defer m.lock.Unlock()

	klog.Infof("user %q requests name %q to be terminated", user, name)
	if job, ok := m.jobs[name]; ok {
		job.Failure = "deletion requested"
		job.ExpiresAt = time.Now().Add(15 * time.Minute)
		job.Complete = true
	}

	// mark the cluster as failed, clear the request, and allow the user to launch again
	existing, ok := m.requests[user]
	if !ok || existing.Name != name {
		return "", fmt.Errorf("another cluster was launched while trying to stop this cluster")
	}
	delete(m.requests, user)
	return "the cluster was flagged for shutdown, you may now launch another", nil
}

func (m *jobManager) SyncJobForUser(user string) (string, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	if len(user) == 0 {
		return "", fmt.Errorf("must specify the name of the user who requested this cluster")
	}

	existing, ok := m.requests[user]
	if !ok || len(existing.Name) == 0 {
		return "", fmt.Errorf("no cluster has been requested by you")
	}
	job, ok := m.jobs[existing.Name]
	if !ok {
		return "", fmt.Errorf("cluster hasn't been initialized yet, cannot refresh")
	}

	var msg string
	switch {
	case len(job.Failure) == 0 && len(job.Credentials) == 0:
		return "cluster is still being loaded, please be patient", nil
	case len(job.Failure) > 0:
		msg = fmt.Sprintf("cluster had previously been marked as failed, checking again: %s", job.Failure)
	case len(job.Credentials) > 0:
		msg = "cluster had previously been marked as successful, checking again"
	}

	copied := *job
	copied.Failure = ""
	klog.Infof("user %q requests job %q to be refreshed", user, copied.Name)
	go m.handleJobStartup(copied, "refresh")

	return msg, nil
}

func (m *jobManager) jobIsComplete(job *Job) bool {
	m.lock.Lock()
	defer m.lock.Unlock()
	current, ok := m.jobs[job.Name]
	if !ok {
		return false
	}
	if current.IsComplete() {
		job.State = current.State
		job.URL = current.URL
		job.Complete = current.Complete
		return true
	}
	return false
}

func (m *jobManager) handleJobStartup(job Job, source string) {
	if !m.tryJob(job.Name) {
		klog.Infof("Job %q already has a worker (%s)", job.Name, source)
		return
	}
	defer m.finishJob(job.Name)

	if err := m.waitForJob(&job); err != nil {
		if err == errJobCompleted || strings.Contains(err.Error(), errJobCompleted.Error()) {
			klog.Infof("Job %q aborted due to detecting completion (%s): %v", job.Name, source, err)
		} else {
			klog.Errorf("Job %q failed to launch (%s): %v", job.Name, source, err)
			job.Failure = err.Error()
		}
	}
	m.finishedJob(job)
}

func (m *jobManager) finishedJob(job Job) {
	m.lock.Lock()
	defer m.lock.Unlock()

	// track the 10 most recent starts in sorted order
	if (job.Mode == JobTypeLaunch || job.Mode == JobTypeWorkflowLaunch) && len(job.Credentials) > 0 && job.StartDuration > 0 {
		m.recentStartEstimates = append(m.recentStartEstimates, job.StartDuration)
		if len(m.recentStartEstimates) > 10 {
			m.recentStartEstimates = m.recentStartEstimates[:10]
		}
		sort.Slice(m.recentStartEstimates, func(i, j int) bool {
			return m.recentStartEstimates[i] < m.recentStartEstimates[j]
		})
	}

	if len(job.RequestedChannel) > 0 && len(job.RequestedBy) > 0 {
		klog.Infof("Job %q complete, notify %q", job.Name, job.RequestedBy)
		if m.jobNotifierFn != nil {
			go m.jobNotifierFn(job)
		}
	}

	// ensure we send no further notifications
	job.RequestedChannel = ""
	m.jobs[job.Name] = &job
}

func (m *jobManager) tryJob(name string) bool {
	m.muJob.lock.Lock()
	defer m.muJob.lock.Unlock()

	_, ok := m.muJob.running[name]
	if ok {
		return false
	}
	m.muJob.running[name] = struct{}{}
	return true
}

func (m *jobManager) finishJob(name string) {
	m.muJob.lock.Lock()
	defer m.muJob.lock.Unlock()

	delete(m.muJob.running, name)
}

func UseSpotInstances(job *Job) bool {
	return job.Mode == JobTypeLaunch && len(job.JobParams) == 0 && (job.Platform == "aws" || job.Platform == "aws-2")
}

func (m *jobManager) CreateRosaCluster(user, channel, version string, duration time.Duration) (string, error) {
	if duration > m.maxRosaAge {
		return "", fmt.Errorf("Max duration for a ROSA cluster is %s", m.maxRosaAge.String())
	}
	cluster, _ := m.getROSAClusterForUser(user)
	if cluster != nil {
		return "", fmt.Errorf("you have already requested a cluster; %d minutes have elapsed", int(time.Since(cluster.CreationTimestamp())/time.Minute))
	}
	m.lock.Lock()
	if existing, ok := m.requests[user]; ok {
		klog.Infof("user %q already requested cluster", user)
		m.lock.Unlock()
		return "", fmt.Errorf("you have already requested a cluster via the `launch` commaned and it should be ready in ~ %d minutes", m.estimateCompletion(existing.RequestedAt)/time.Minute)
	}
	m.lock.Unlock()
	m.rosaClusters.lock.Lock()
	if m.rosaClusterLimit != 0 && len(m.rosaClusters.clusters)+m.rosaClusters.pendingClusters >= m.rosaClusterLimit {
		m.rosaClusters.lock.Unlock()
		return "", errors.New("The maximum number of ROSA clusters has been reached. Please try again later.")
	}
	m.rosaClusters.pendingClusters++
	m.rosaClusters.lock.Unlock()
	defer func() { m.rosaClusters.lock.Lock(); m.rosaClusters.pendingClusters--; m.rosaClusters.lock.Unlock() }()
	cluster, version, err := m.createRosaCluster(version, user, channel, duration)
	if err != nil {
		return "", fmt.Errorf("Failed to create cluster: %w", err)
	}
	return fmt.Sprintf("Created cluster `%s` with version `%s`.", cluster.Name(), version), nil
}

func (m *jobManager) lookupRosaVersions(prefix string) []string {
	m.rosaVersions.lock.RLock()
	defer m.rosaVersions.lock.RUnlock()
	var matchedVersions []string
	for _, rosaVersion := range m.rosaVersions.versions {
		if strings.HasPrefix(rosaVersion, prefix) {
			matchedVersions = append(matchedVersions, rosaVersion)
		}
	}
	return matchedVersions
}

func (m *jobManager) LookupRosaInputs(versionPrefix string) (string, error) {
	matchedVersions := m.lookupRosaVersions(versionPrefix)
	if len(matchedVersions) == 0 {
		return "", fmt.Errorf("No version with prefix `%s` found", versionPrefix)
	} else if versionPrefix == "" {
		return fmt.Sprintf("The following versions are supported: %v", matchedVersions), nil
	} else {
		return fmt.Sprintf("Found the following version with a prefix of `%s`: %v", versionPrefix, matchedVersions), nil
	}
}
