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

	clusterv1 "open-cluster-management.io/api/cluster/v1"
	prowClient "sigs.k8s.io/prow/pkg/client/clientset/versioned/typed/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/scheduler/strategy"

	"github.com/openshift/ci-chat-bot/pkg/prow"
	"github.com/openshift/ci-chat-bot/pkg/utils"
	"github.com/sirupsen/logrus"

	"github.com/prometheus/client_golang/prometheus"

	prowapiv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/metrics"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"

	clustermgmtv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	imagev1 "github.com/openshift/api/image/v1"
	citools "github.com/openshift/ci-tools/pkg/api"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned"
	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/rosa/pkg/ocm"
	"github.com/openshift/rosa/pkg/rosa"

	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog"

	"maps"
	"slices"

	"github.com/blang/semver"
	reference "github.com/containers/image/v5/docker/reference"
	"gopkg.in/yaml.v2"
	prowInformer "sigs.k8s.io/prow/pkg/client/informers/externalversions/prowjobs/v1"
)

func init() {
	prometheus.MustRegister(rosaReadyTimeMetric)
	prometheus.MustRegister(rosaAuthTimeMetric)
	prometheus.MustRegister(rosaReadyToAuthTimeMetric)
	prometheus.MustRegister(rosaConsoleTimeMetric)
	prometheus.MustRegister(rosaReadyToConsoleTimeMetric)
	prometheus.MustRegister(rosaSyncTimeMetric)
	prometheus.MustRegister(rosaClustersMetric)
}

const (
	// maxJobsPerUser limits the number of simultaneous jobs a user can launch to prevent
	// a single user from consuming the infrastructure account.
	maxJobsPerUser = 23

	// maxTotalClusters limits the number of simultaneous clusters across all users to
	// prevent saturating the infrastructure account.
	maxTotalClusters = 80

	maxTotalMCEClusters = 15
	MaxMCEDuration      = time.Duration(8 * time.Hour)
)

const (
	JobTypeCatalog = "catalog"
	JobTypeBuild   = "build"
	// TODO: remove this const. It seems out of date and replaced by launch everywhere except for in JobRequest.JobType. Gets changed to "launch" for job.Mode
	JobTypeInstall         = "install"
	JobTypeLaunch          = "launch"
	JobTypeTest            = "test"
	JobTypeUpgrade         = "upgrade"
	JobTypeWorkflowLaunch  = "workflow-launch"
	JobTypeWorkflowTest    = "workflow-test"
	JobTypeWorkflowUpgrade = "workflow-upgrade"
	JobTypeMCECustomImage  = "mce-custom-image"
)

var CurrentRelease = semver.Version{
	Major: 4,
	Minor: 19,
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
	prowClient prowClient.ProwV1Interface,
	prowInformer prowInformer.ProwJobInformer,
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
	rosaClusterAdminUsername string,
	errorRate *prometheus.CounterVec,
	rosaOidcConfigId string,
	rosaBillingAccount string,
	dpcrOcmClient crclient.Client,
	dpcrHiveClient crclient.Client,
	dpcrNamespaceClient typedcorev1.NamespaceInterface,
	dpcrCoreClient *typedcorev1.CoreV1Client,
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
		prowScheduler:    strategy.Get(prowConfigLoader.Config(), logrus.WithField("interface", "scheduler")),
		prowLister:       prowInformer.Lister(),
		imageClient:      imageClient,
		clusterClients:   buildClusterClientConfigMap,
		prowNamespace:    "ci",
		forcePROwner:     forcePROwner,

		configResolver: configResolver,
		workflowConfig: workflowConfig,

		lClient: lClient,

		hiveConfigMapClient:      hiveConfigMapClient,
		rosaSecretClient:         rosaSecretClient,
		rClient:                  rosaClient,
		maxRosaAge:               8 * time.Hour,
		defaultRosaAge:           6 * time.Hour,
		rosaSubnets:              rosaSubnetList,
		rosaClusterLimit:         rosaClusterLimit,
		rosaClusterAdminUsername: rosaClusterAdminUsername,
		rosaOidcConfigId:         rosaOidcConfigId,
		rosaBillingAccount:       rosaBillingAccount,
		errorMetric:              errorRate,
		dpcrCoreClient:           dpcrCoreClient,
		dpcrOcmClient:            dpcrOcmClient,
		dpcrHiveClient:           dpcrHiveClient,
		dpcrNamespaceClient:      dpcrNamespaceClient,
	}
	m.muJob.running = make(map[string]struct{})
	initializeErrorMetrics(m.errorMetric)
	return m
}

func (m *jobManager) updateImageSetList() error {
	imagesetList := hivev1.ClusterImageSetList{}
	if err := m.dpcrHiveClient.List(context.TODO(), &imagesetList); err != nil {
		metrics.RecordError(errorMCEListImagesets, m.errorMetric)
		return err
	}
	updatedImageSets := sets.Set[string]{}
	m.mceClusters.lock.RLock()
	for _, imageset := range imagesetList.Items {
		// clean up orphaned imagesets; wait until managed cluster list is not empty to avoid deleting in-use imagesets on restart
		if len(m.mceClusters.clusters) > 0 && imageset.Labels[utils.LaunchLabel] == "true" {
			if _, ok := m.mceClusters.clusters[imageset.Name]; !ok {
				if err := m.dpcrHiveClient.Delete(context.TODO(), &imageset); err != nil {
					metrics.RecordError(errorMCECleanupImagesets, m.errorMetric)
					klog.Errorf("Failed to delete orphaned imageset %s: %v", imageset.Name, err)
				}
				continue
			}
		}
		updatedImageSets.Insert(imageset.GetName())
	}
	m.mceClusters.lock.RUnlock()
	m.mceClusters.lock.Lock()
	defer m.mceClusters.lock.Unlock()
	m.mceClusters.imagesets = updatedImageSets
	return nil
}

func (m *jobManager) updateHypershiftSupportedVersions() error {
	if m.hiveConfigMapClient == nil {
		HypershiftSupportedVersions.Mu.Lock()
		defer HypershiftSupportedVersions.Mu.Unlock()
		// assume that current default release for chat-bot is supported
		HypershiftSupportedVersions.Versions = sets.New(fmt.Sprintf("%d.%d", CurrentRelease.Major, CurrentRelease.Minor))
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
	HypershiftSupportedVersions.Versions = sets.New(convertedVersions.Versions...)
	klog.Infof("Hypershift Supported Versions: %+v", sets.List(HypershiftSupportedVersions.Versions))
	return nil
}

func (m *jobManager) updateRosaVersions() error {
	if m.rClient == nil {
		return nil
	}
	vs, err := m.rClient.OCMClient.GetVersionsWithProduct(ocm.HcpProduct, ocm.DefaultChannelGroup, false)
	if err != nil {
		return fmt.Errorf("failed to retrieve versions: %s", err)
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
		return fmt.Errorf("could not find versions for the provided channel-group: '%s'", ocm.DefaultChannelGroup)
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
		if err := m.mceSync(); err != nil {
			klog.Infof("error during mce sync: %v", err)
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
	go wait.Forever(func() {
		if err := m.updateImageSetList(); err != nil {
			klog.Warningf("error during updateImageSetList: %v", err)
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

func (m *jobManager) mceSync() error {
	start := time.Now()
	// wrap Observe function into inline function so that time.Since doesn't get immediately evaluated
	defer func() { mceSyncTimeMetric.Observe(time.Since(start).Seconds()) }()
	clusters, deployments, err := m.listManagedClusters()
	if err != nil {
		return fmt.Errorf("failed to list managed clusters: %v", err)
	}
	now := time.Now()
	managedClusters := map[string]*clusterv1.ManagedCluster{}
	for _, cluster := range clusters {
		expiryString := cluster.Annotations[utils.ExpiryTimeTag]
		expiryTime, err := time.Parse(time.RFC3339, expiryString)
		if err != nil {
			return err
		}
		if expiryTime.Before(now) {
			if err := m.deleteManagedCluster(cluster); err != nil {
				return err
			}
		}
		managedClusters[cluster.Name] = cluster
	}
	clusterDeployments := map[string]*hivev1.ClusterDeployment{}
	provisions := map[string]*hivev1.ClusterProvision{}
	for _, deployment := range deployments {
		clusterDeployments[deployment.Name] = deployment
		provisionList := &hivev1.ClusterProvisionList{}
		if err := m.dpcrHiveClient.List(context.TODO(), provisionList, &crclient.ListOptions{LabelSelector: labels.SelectorFromSet(labels.Set{"hive.openshift.io/cluster-deployment-name": deployment.Name})}); err != nil {
			metrics.RecordError(errorMCEListClusterProvisions, m.errorMetric)
			klog.Errorf("Failed to get cluster provision ref: %v", err)
			continue
		}
		if len(provisionList.Items) == 0 {
			klog.Warningf("No matching provision found for cluster %s", deployment.Name)
			continue
		}
		newestProvision := &provisionList.Items[0]
		for _, extraProvision := range provisionList.Items {
			if extraProvision.CreationTimestamp.After(newestProvision.CreationTimestamp.Time) {
				newestProvision = &extraProvision
			}
		}
		provisions[deployment.Name] = newestProvision
	}
	for name, cluster := range managedClusters {
		m.mceClusters.lock.RLock()
		_, ok := m.mceClusters.clusterKubeconfigs[name]
		_, ok2 := m.mceClusters.clusterPasswords[name]
		previousProvision := m.mceClusters.provisions[name]
		previousDeployment := m.mceClusters.deployments[name]
		m.mceClusters.lock.RUnlock()
		if !ok || !ok2 {
			_, _, err := m.getClusterAuth(name)
			if err != nil {
				klog.Errorf("Failed to get cluster auth for %s: %v", name, err)
			}
		}

		var availability string
		for _, condition := range cluster.Status.Conditions {
			if condition.Type == "ManagedClusterConditionAvailable" {
				availability = string(condition.Status)
			}
		}

		if availability == "True" {
			if notified, ok := cluster.Annotations[utils.UserNotifiedTag]; !ok || notified != "true" {
				if cloud, ok := cluster.Labels["cloud"]; ok {
					switch cloud {
					case "Amazon":
						mceAWSReadyTimeMetric.Observe(time.Since(cluster.CreationTimestamp.Time).Minutes())
					case "Google":
						mceGCPReadyTimeMetric.Observe(time.Since(cluster.CreationTimestamp.Time).Minutes())
					}
				}
				// notify that the cluster is available and retrieve auth
				kubeconfig, password, err := m.getClusterAuth(name)
				if err != nil {
					return fmt.Errorf("failed to get mce cluster auth: %v", err)
				}
				m.mceNotifierFn(cluster, clusterDeployments[name], provisions[name], kubeconfig, password, nil)
				cluster.Annotations[utils.UserNotifiedTag] = "true"
				if err := m.dpcrOcmClient.Update(context.TODO(), cluster); err != nil {
					metrics.RecordError(errorMCEAnnotateNotify, m.errorMetric)
					klog.Errorf("Failed to update managed cluster annotations: %v", err)
				}
			}
		} else {
			if provision, ok := provisions[name]; ok {
				if previousProvision != nil && previousProvision.Spec.Stage != provision.Spec.Stage && provision.Spec.Stage == hivev1.ClusterProvisionStageFailed {
					metrics.RecordError(errorMCEProvisionFailed, m.errorMetric)
					m.mceNotifierFn(cluster, clusterDeployments[name], provisions[name], "", "", nil)
				}
			} else {
				// in some cases, an early provisioning fail may result in a ClusterProvision not being created
				if currentDeployment, ok := clusterDeployments[name]; ok {
					for _, provisionCondition := range currentDeployment.Status.Conditions {
						if provisionCondition.Type == hivev1.ProvisionFailedCondition {
							if provisionCondition.Status == "True" {
								var prevFailed bool
								if previousDeployment != nil {
									for _, previousCondition := range previousDeployment.Status.Conditions {
										if previousCondition.Type == hivev1.ProvisionFailedCondition {
											if previousCondition.Status == "True" {
												prevFailed = true
											}
											break
										}
									}
								}
								if !prevFailed {
									metrics.RecordError(errorMCEProvisionFailed, m.errorMetric)
									m.mceNotifierFn(cluster, currentDeployment, provision, "", "", nil)
								}
							}
							break
						}
					}
				}
			}
		}
	}
	m.lock.RLock()
	var awsClusters, gcpClusters int
	for name, cluster := range managedClusters {
		if cloud, ok := cluster.Labels["cloud"]; ok {
			switch cloud {
			case "Amazon":
				awsClusters++
			case "Google":
				gcpClusters++
			}
		}
		if _, exists := clusterDeployments[name]; !exists {
			hasJob := false
			for _, job := range m.jobs {
				if job.ManagedClusterName == name {
					hasJob = true
					break
				}
			}
			if !hasJob {
				klog.Infof("Deleting stale cluster %s", name)
				if err := m.deleteManagedCluster(cluster); err != nil {
					klog.Errorf("Failed to delete stale managed cluster: %v", err)
				}
				delete(managedClusters, name)
			}
		}
	}
	m.lock.RUnlock()
	klog.Infof("Found %d chat-bot owned mce clusters", len(managedClusters))
	mceAWSClustersMetric.Set(float64(awsClusters))
	mceGCPClustersMetric.Set(float64(gcpClusters))
	m.mceClusters.lock.Lock()
	m.mceClusters.clusters = managedClusters
	m.mceClusters.deployments = clusterDeployments
	m.mceClusters.provisions = provisions
	m.mceClusters.lock.Unlock()
	userConfig, err := m.dpcrCoreClient.ConfigMaps("crt-argocd").Get(context.TODO(), "users", metav1.GetOptions{})
	if err != nil {
		metrics.RecordError(errorMCERetrieveUserConfig, m.errorMetric)
		return fmt.Errorf("failed to retrieve mce user configs: %v", err)
	}
	mceUserConfig := map[string]MceUser{}
	if err := yaml.Unmarshal([]byte(userConfig.Data["config.yaml"]), mceUserConfig); err != nil {
		metrics.RecordError(errorMCEParseUserConfig, m.errorMetric)
		return fmt.Errorf("failed to unmarshal MCE user config: %v", err)
	}
	m.mceConfig.Mutex.Lock()
	defer m.mceConfig.Mutex.Unlock()
	m.mceConfig.Users = mceUserConfig
	return nil
}

func (m *jobManager) rosaSync() error {
	if m.rClient == nil {
		return nil
	}
	start := time.Now()
	// wrap Observe function into inline function so that time.Since doesn't get immediately evaluated
	defer func() { rosaSyncTimeMetric.Observe(time.Since(start).Seconds()) }()
	klog.Infof("Getting ROSA clusters")
	clusterList, err := m.rClient.OCMClient.GetAllClusters(m.rClient.Creator)
	if err != nil {
		metrics.RecordError(errorRosaGetAll, m.errorMetric)
		klog.Warningf("Failed to get clusters: %v", err)
	}
	klog.Infof("Found %d rosa clusters", len(clusterList))
	rosaClustersMetric.Set(float64(len(clusterList)))

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
					readyTime := time.Now()
					alreadyExists, err := m.addClusterAuthAndWait(cluster, readyTime)
					if err != nil {
						// addClusterAuthAndWait records metrics itself
						klog.Errorf("Failed to add cluster auth: %v", err)
					}
					// don't renotify users on chat-bot restart
					if !alreadyExists {
						activeRosaIDs, err := m.rosaSecretClient.Get(context.TODO(), RosaClusterSecretName, metav1.GetOptions{})
						if err != nil {
							metrics.RecordError(errorRosaGetSecret, m.errorMetric)
							klog.Errorf("Failed to get `%s` secret: %v", RosaClusterSecretName, err)
						}
						// notify that the cluster has auth
						m.rosaNotifierFn(cluster, string(activeRosaIDs.Data[cluster.ID()]))
						if err := m.waitForConsole(cluster, readyTime); err != nil {
							klog.Errorf("Failed to wait for console: %v", err)
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
						// update cluster list to include console
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
				if m.rosaErrorReported == nil {
					m.rosaErrorReported = sets.New[string]()
				}
				if !m.rosaErrorReported.Has(cluster.ID()) {
					klog.Infof("Reporting failure for cluster %s", cluster.ID())
					m.rosaNotifierFn(cluster, "")
					m.rosaErrorReported.Insert(cluster.ID())
				}
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
			return fmt.Errorf("failed to update `%s` secret to remove stale clusters from list: %w", RosaClusterSecretName, err)
		}
	}

	return utilerrors.NewAggregate(awsCleanupErrors)
}

func (m *jobManager) sync() error {
	prowjobs, err := m.prowLister.ProwJobs(m.prowNamespace).List(labels.SelectorFromSet(labels.Set{
		utils.LaunchLabel: "true",
	}))
	if err != nil {
		return err
	}

	m.lock.Lock()
	defer m.lock.Unlock()

	now := time.Now()

	for _, job := range prowjobs {
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
			buildCluster, err = m.schedule(job)
			if err != nil {
				klog.Error(err.Error())
				buildCluster = job.Spec.Cluster
			}
		}
		var isOperator bool
		if job.Annotations["ci-chat-bot.openshift.io/IsOperator"] == "true" {
			isOperator = true
		}
		var hasIndex bool
		if job.Annotations["ci-chat-bot.openshift.io/HasIndex"] == "true" {
			hasIndex = true
		}
		j := &Job{
			Name:             job.Name,
			State:            job.Status.State,
			URL:              job.Status.URL,
			OriginalMessage:  job.Annotations["ci-chat-bot.openshift.io/originalMessage"],
			Mode:             job.Annotations["ci-chat-bot.openshift.io/mode"],
			JobName:          job.Spec.Job,
			Platform:         job.Annotations["ci-chat-bot.openshift.io/platform"],
			Inputs:           inputs,
			RequestedBy:      job.Annotations["ci-chat-bot.openshift.io/user"],
			RequestedChannel: job.Annotations["ci-chat-bot.openshift.io/channel"],
			RequestedAt:      job.CreationTimestamp.Time,
			Architecture:     architecture,
			BuildCluster:     buildCluster,
			Operator: OperatorInfo{
				Is:         isOperator,
				HasIndex:   hasIndex,
				BundleName: job.Annotations["ci-chat-bot.openshift.io/OperatorBundleName"],
			},
			ManagedClusterName: job.Annotations["ci-chat-bot.openshift.io/managedClusterName"],
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
			j.ExpiresAt = job.CreationTimestamp.Add(m.maxAge)
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
			m.mceClusters.lock.RLock()
			if mCluster, ok := m.mceClusters.clusters[j.ManagedClusterName]; ok {
				metrics.RecordError(errorMCEImagesetJobRun, m.errorMetric)
				errMsg := fmt.Sprintf("Failed to generate imageset for managed cluster. See logs for details: %s.", job.Status.URL)
				if err := m.deleteManagedCluster(mCluster); err != nil {
					errMsg = fmt.Sprintf("\nAn error also occurred when attempting to delete the previously created resources: %v", err)
					klog.Errorf("Failed to delete managed cluster %s: %v", j.ManagedClusterName, err)
				}
				go m.mceSync() // nolint:errcheck
				m.mceNotifierFn(mCluster, nil, nil, "", "", errors.New(errMsg))
				m.mceClusters.lock.RUnlock()
				break
			}
			m.mceClusters.lock.RUnlock()
		case prowapiv1.SuccessState:
			j.Failure = ""

			m.jobs[job.Name] = j
			if (previous == nil || previous.State != j.State) && j.ManagedClusterName == "" {
				go m.finishedJob(*j)
			} else if j.ManagedClusterName != "" {
				m.mceClusters.lock.RLock()
				if mCluster, ok := m.mceClusters.clusters[j.ManagedClusterName]; ok {
					if _, ok := m.mceClusters.deployments[j.ManagedClusterName]; ok {
						// deployment already exists; ignore
						m.mceClusters.lock.RUnlock()
						break
					}
					ciOpNamespace, ok := job.Annotations["ci-chat-bot.openshift.io/ns"]
					if !ok {
						// this shouldn't happen
						msg := fmt.Sprintf("Could not identify ci-operator namespace for job %s.", job.Name)
						klog.Error(msg)
						if err := m.deleteManagedCluster(mCluster); err != nil {
							msg += fmt.Sprintf("\nAn error also occurred when attempting to delete the previously created resources: %v", err)
							klog.Errorf("Failed to delete managed cluster %s: %v", j.ManagedClusterName, err)
						}
						go m.mceSync() // nolint:errcheck
						m.mceNotifierFn(mCluster, nil, nil, "", "", errors.New(msg))
						m.mceClusters.lock.RUnlock()
						break
					}
					registryURL := fmt.Sprintf("registry.%s.ci.openshift.org/%s/release:latest", j.BuildCluster, ciOpNamespace)
					if err := m.createCustomImageset(registryURL, j.ManagedClusterName); err != nil {
						metrics.RecordError(errorMCEImagesetCreateRef, m.errorMetric)
						msg := fmt.Sprintf("Failed to create imageset for release created by ci-operator: %v.", err)
						klog.Errorf("Failed to create cluster imageset: %v", err)
						if err := m.deleteManagedCluster(mCluster); err != nil {
							msg += fmt.Sprintf("\nAn error also occurred when attempting to delete the previously created resources: %v", err)
							klog.Errorf("Failed to delete managed cluster %s: %v", j.ManagedClusterName, err)
						}
						go m.mceSync() // nolint:errcheck
						m.mceNotifierFn(mCluster, nil, nil, "", "", errors.New(msg))
						m.mceClusters.lock.RUnlock()
						break
					}
					klog.Infof("Created imageset %s pointing to %s", j.ManagedClusterName, registryURL)
					platform := ""
					switch mCluster.Labels["Cloud"] {
					case "Amazon":
						platform = "aws"
					case "Google":
						platform = "gcp"
					}
					if err := m.createClusterDeployment(j.ManagedClusterName, j.ManagedClusterName, mCluster.Annotations[utils.BaseDomain], platform); err != nil {
						msg := fmt.Sprintf("Failed to create Cluster Deployment: %v", err)
						klog.Errorf("Failed to create cluster deployment: %v", err)
						if err := m.deleteManagedCluster(mCluster); err != nil {
							msg += fmt.Sprintf("\nAn error also occurred when attempting to delete the previously created resources: %v", err)
							klog.Errorf("Failed to delete managed cluster %s: %v", j.ManagedClusterName, err)
						}
						go m.mceSync() // nolint:errcheck
						m.mceNotifierFn(mCluster, nil, nil, "", "", errors.New(msg))
						m.mceClusters.lock.RUnlock()
						break
					}
					klog.Infof("Created cluster deployment %s", j.ManagedClusterName)
				}
				m.mceClusters.lock.RUnlock()
			}

		case prowapiv1.SchedulingState, prowapiv1.TriggeredState, prowapiv1.PendingState, "":
			j.State = prowapiv1.PendingState
			j.Failure = ""

			if j.Mode == JobTypeLaunch || j.Mode == JobTypeWorkflowLaunch {
				if user := j.RequestedBy; len(user) > 0 {
					// Check if the user has an existing request.  If they do, then move on
					if _, ok := m.requests[user]; !ok {
						// If not, then most likely, the clusterbot has recently (re)started, and we need to populate the
						// request to ensure that the user can't start a second cluster (instead of waiting for the second
						// invocation of the sync loop to populate it accordingly).
						// The 2 scenarios where we need to handle populating the request entry are:
						//  * A new request (i.e. there is no "previous" job for this user)
						//  OR
						//  * A previous job does exist, but it hasn't reached the "Complete" state yet
						if previous == nil || !previous.Complete {
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

func (m *jobManager) SetMceNotifier(fn MCECallbackFunc) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.mceNotifierFn = fn
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

func (m *jobManager) GetUserCluster(user string) *Job {
	m.lock.RLock()
	defer m.lock.RUnlock()

	for _, job := range m.jobs {
		if user == job.RequestedBy && (job.Mode == JobTypeLaunch || job.Mode == JobTypeWorkflowLaunch) && (job.State != prowapiv1.SuccessState && !job.Complete) {
			return job
		}
	}
	return nil
}

func (m *jobManager) ListJobs(user string, filters ListFilters) (string, string, []string) {
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
	var beginning string
	var elements []string
	now := time.Now()
	if len(clusters) == 0 {
		beginning = fmt.Sprintf("No clusters up (start time is approximately %d minutes):\n\n", m.estimateCompletion(time.Time{})/time.Minute)
		fmt.Fprint(buf, beginning)
	} else {
		beginning = fmt.Sprintf("%d/%d clusters up (start time is approximately %d minutes):\n\n", runningClusters, m.maxClusters, m.estimateCompletion(time.Time{})/time.Minute)
		fmt.Fprint(buf, beginning)
		for _, job := range clusters {
			var jobInput JobInput
			if len(job.Inputs) > 0 {
				jobInput = job.Inputs[0]
			}
			if filters.Requestor != "" && filters.Requestor != job.RequestedBy || filters.Platform != "" && filters.Platform != job.Platform || filters.Version != "" && !strings.Contains(jobInput.Version, filters.Version) {
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
			maps.Copy(params, job.JobParams)
			if len(job.Platform) > 0 {
				params[job.Platform] = ""
			}
			if s := paramsToString(params); len(s) > 0 {
				options = fmt.Sprintf(" (%s)", s)
			}

			switch {
			case job.State == prowapiv1.SuccessState:
				element := fmt.Sprintf("• <@%s>%s - cluster has been shut down%s\n", job.RequestedBy, imageOrVersion, details)
				elements = append(elements, element)
				fmt.Fprint(buf, element)
			case job.State == prowapiv1.FailureState:
				element := fmt.Sprintf("• <@%s>%s%s - cluster failed to start%s\n", job.RequestedBy, imageOrVersion, options, details)
				elements = append(elements, element)
				fmt.Fprint(buf, element)
			case job.Complete:
				element := fmt.Sprintf("• <@%s>%s%s - cluster has requested shut down%s\n", job.RequestedBy, imageOrVersion, options, details)
				elements = append(elements, element)
				fmt.Fprint(buf, element)
			case len(job.Credentials) > 0:
				element := fmt.Sprintf("• <@%s>%s%s - available and will be torn down in %d minutes%s\n", job.RequestedBy, imageOrVersion, options, int(job.ExpiresAt.Sub(now)/time.Minute), details)
				elements = append(elements, element)
				fmt.Fprint(buf, element)
			case len(job.Failure) > 0:
				element := fmt.Sprintf("• <@%s>%s%s - failure: %s%s\n", job.RequestedBy, imageOrVersion, options, job.Failure, details)
				elements = append(elements, element)
				fmt.Fprint(buf, element)
			default:
				element := fmt.Sprintf("• <@%s>%s%s - starting, %d minutes elapsed%s\n", job.RequestedBy, imageOrVersion, options, int(now.Sub(job.RequestedAt)/time.Minute), details)
				elements = append(elements, element)
				fmt.Fprint(buf, element)
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
	return buf.String(), beginning, elements
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
	// Sanitize against linkified pullSpecs because they will fail the resultant prowjob...
	unresolved = strings.TrimPrefix(unresolved, "http://")
	unresolved = strings.TrimPrefix(unresolved, "https://")

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
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp", Imagestream: "konflux-release"})
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "origin", Imagestream: "release"})
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "origin", Imagestream: "release-scos"})
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "origin", Imagestream: "release-scos-next"})
	case "arm64":
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp-arm64", Imagestream: "release-arm64", ArchSuffix: "-arm64"})
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp-arm64", Imagestream: "4-dev-preview-arm64", ArchSuffix: "-arm64"})
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp-arm64", Imagestream: "konflux-release-arm64", ArchSuffix: "-arm64"})
	case "multi":
		// the release-controller cannot assemble multi-arch release, so we must use the `art-latest` streams instead of `release-multi`
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp-multi", Imagestream: "release-multi", ArchSuffix: "-multi"})
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp-multi", Imagestream: "4-dev-preview-multi", ArchSuffix: "-multi"})
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp-multi", Imagestream: "konflux-release-multi", ArchSuffix: "-multi"})
		HypershiftSupportedVersions.Mu.RLock()
		defer HypershiftSupportedVersions.Mu.RUnlock()
		for version := range HypershiftSupportedVersions.Versions {
			imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp-multi", Imagestream: fmt.Sprintf("%s-art-latest-multi", version), ArchSuffix: "-multi"})
		}
	default:
		return "", "", "", fmt.Errorf("unsupported architecture: %s", architecture)
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
			if architecture == "amd64" || architecture == "multi" || strings.Contains(unresolved, "konflux") {
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
	switch architecture {
	case "arm64":
		archSuffix = "-arm64"
	case "multi":
		archSuffix = "-multi"
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

func (m *jobManager) GetMceUserConfig() *MceConfig {
	return &m.mceConfig
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
				if architecture == "arm64" && (len(runImage) == 0 || len(version) == 0) && !strings.Contains(image, "konflux") {
					return nil, defaultedVersion, fmt.Errorf("only version numbers (like: 4.19.0) may be used for arm64 based clusters")
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

		ManagedClusterName: req.ManagedClusterName,
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
					return nil, fmt.Errorf("hypershift currently only supports the following releases: %v", sets.List(HypershiftSupportedVersions.Versions))
				}
			}
		}
		HypershiftSupportedVersions.Mu.RUnlock()
	}

	switch req.Type {
	case JobTypeMCECustomImage: // currently identical to JobTypeBuild
		if req.Architecture != "amd64" {
			return nil, fmt.Errorf("builds are not currently supported for non-amd64 releases")
		}
		var prs int
		for _, input := range jobInputs {
			for _, ref := range input.Refs {
				prs += len(ref.Pulls)
			}
		}
		if len(jobInputs) != 1 {
			return nil, fmt.Errorf("at least one input is required to build a release image")
		}
		job.Mode = JobTypeMCECustomImage
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
	case JobTypeCatalog:
		if req.Architecture != "amd64" {
			return nil, fmt.Errorf("operator builds are not currently supported for non-amd64 releases")
		}
		var prs int
		for _, input := range jobInputs {
			for _, ref := range input.Refs {
				prs += len(ref.Pulls)
			}
		}
		if len(jobInputs) != 1 || prs == 0 {
			return nil, fmt.Errorf("at least one pull request is required to build an operator catalog")
		}
		job.Mode = JobTypeCatalog
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
		if req.Architecture != "amd64" && !strings.Contains(req.OriginalMessage, "konflux") {
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
		if req.Architecture != "amd64" && !strings.Contains(req.OriginalMessage, "konflux") {
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
			if req.Architecture != "multi" && req.Platform != "hypershift-hosted" {
				return nil, fmt.Errorf("workflow launches are not currently supported for non-amd64 releases")
			}
		}
		if len(jobInputs) != 1 {
			return nil, fmt.Errorf("launching a cluster requires one image, version, or pull request")
		}
		if len(req.Platform) == 0 {
			return nil, fmt.Errorf("platform must be set when launching clusters")
		}
		job.Mode = JobTypeWorkflowLaunch
	case JobTypeWorkflowTest:
		if req.Architecture != "amd64" {
			return nil, fmt.Errorf("workflow launches are not currently supported for non-amd64 releases")
		}
		if len(jobInputs) != 1 {
			return nil, fmt.Errorf("launching a cluster requires one image, version, or pull request")
		}
		job.Mode = JobTypeWorkflowTest
	default:
		return nil, fmt.Errorf("unexpected job type: %q", req.Type)
	}
	job.Inputs = jobInputs

	return job, nil
}

func multistageParamsForPlatform(platform string) sets.Set[string] {
	params := sets.New[string]()
	for param, env := range MultistageParameters {
		if env.Platforms.Has(platform) {
			params.Insert(param)
		}
	}
	return params
}

func multistageNameFromParams(params map[string]string, platform, jobType string) (string, error) {
	if jobType == JobTypeWorkflowLaunch || jobType == JobTypeBuild || jobType == JobTypeCatalog || jobType == JobTypeMCECustomImage {
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
	case JobTypeWorkflowTest:
		prefix = "workflow-e2e"
	case JobTypeUpgrade:
		prefix = "upgrade"
	default:
		return "", fmt.Errorf("unknown job type %s", jobType)
	}
	_, okTest := params["test"]
	_, okNoSpot := params["no-spot"]
	if len(params) == 0 || (len(params) == 1 && (okTest || okNoSpot)) {
		return prefix, nil
	}
	platformParams := multistageParamsForPlatform(platform)
	variants := sets.New[string]()
	for k := range params {
		if slices.Contains(SupportedParameters, k) && !platformParams.Has(k) && k != "test" && k != "bundle" && k != "no-spot" { // we only need parameters that are not configured via multistage env vars
			variants.Insert(k)
		}
	}
	if len(variants) == 0 {
		return prefix, nil
	}
	return fmt.Sprintf("%s-%s", prefix, strings.Join(sets.List(variants), "-")), nil
}

func configContainsVariant(params map[string]string, platform, unresolvedConfig, jobType string) (bool, string, error) {
	if jobType == JobTypeWorkflowLaunch {
		return true, "launch", nil
	}
	if jobType == JobTypeWorkflowTest {
		return true, "e2e-test", nil
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

// allowedRegistryNames contains the exact names of allowed registries
var allowedRegistryNames = []string{
	"quay.io",
}

// allowedRegistryRegexes contains regex patterns for allowed registries
var allowedRegistryRegexes = []*regexp.Regexp{
	regexp.MustCompile(`^registry\..*\.openshift\.org`),
}

// validVersionRegexes represents all the valid version formats we recognize for OpenShift.
// We allow an optional comma in the front so we can account for both 4.19,xxxx and xxxx,4.19
var validVersionRegexes = []*regexp.Regexp{
	regexp.MustCompile(`^\d+\.\d+`),               // 4.19
	regexp.MustCompile(`^nightly`),                // nightly
	regexp.MustCompile(`^ci`),                     // ci
	regexp.MustCompile(`^\d+\.\d+\.nightly`),      // 4.19.nightly
	regexp.MustCompile(`^\d+\.\d+\.ci`),           // 4.19.ci
	regexp.MustCompile(`^\d+\.\d+\.0-0\.nightly`), // 4.19.0-0.nightly
	regexp.MustCompile(`^\d+\.\d+\.0-0\.ci`),      // 4.19.0-0.ci

	// Release specs for nightly and ci
	regexp.MustCompile(`^(quay\.io|registry\.ci\.openshift\.org).*release:\d+\.\d+\.\d+-0\.nightly-\d{4}-\d{2}-\d{2}-\d{6}$`),
	regexp.MustCompile(`^(quay\.io|registry\.ci\.openshift\.org).*release:\d+\.\d+\.\d+-0\.ci-\d{4}-\d{2}-\d{2}-\d{6}$`),

	// Quay.io pullspecs
	regexp.MustCompile(`^quay\.io/openshift-release-dev/.+:(\d+\.\d+\.\d+(-ec\.\d+)?|.+-0\.(nightly|ci)(-priv)?-\d{4}-\d{2}-\d{2}-\d{6})$`),

	// quay.io/openshift-release-dev/ocp-release:4.19.0-ec.4-x86_64
	regexp.MustCompile(`^quay\.io/openshift-release-dev/ocp-release:\d+\.\d+\.\d+-ec\.\d+(-\w+)?$`),

	// OKD versions
	// quay.io/okd/scos-release:4.19.0-okd-scos.ec.8
	regexp.MustCompile(`^quay\.io/okd/scos-release:\d+\.\d+\.\d+-okd-scos\.ec\.\d+$`),
	regexp.MustCompile(`^(quay\.io/okd|quay\.io/openshift/okd|registry\.ci\.openshift\.org/origin/release-scos):\d+\.\d+\.\d+-0\.okd(-scos)?(\.ec\.\d+)?(-\d{4}-\d{2}-\d{2}-\d{6})?$`),

	// Private releases
	regexp.MustCompile(`^registry\.ci\.openshift\.org/ocp-priv/release-priv:\d+\.\d+\.\d+-0\.nightly-priv-\d{4}-\d{2}-\d{2}-\d{6}`),

	// Konflux releases
	regexp.MustCompile(`^registry\.ci\.openshift\.org/ocp/konflux-release:\d+\.\d+\.\d+-0\.konflux-nightly-\d{4}-\d{2}-\d{2}-\d{6}`),

	// Releases built with clusterbot 'build' command
	regexp.MustCompile(`^registry\.build.*\.ci\.openshift\.org/ci.*/release`),
}

// containsValidVersion checks if the provided list of images, versions, or PRs contains a valid version.
// A valid version is of the list of patterns in validVersions.
// The function returns true if any of the items in the list matches a valid version pattern or pullspec.
// This ensures that we definitively know what version of Openshift to use when creating a cluster.
// When a user says "launch openshift/installer#7160,4.19", when this function is called, listOfImageOrVersionOrPRs
// will be a slice of images, versions, or PRs and look like []{"openshift/installer#7160", "4.19"}
func containsValidVersion(listOfImageOrVersionOrPRs []string) bool {
	for _, item := range listOfImageOrVersionOrPRs {
		for _, re := range validVersionRegexes {
			if re.MatchString(item) {
				return true
			}
		}

		// Try to parse as a pullspec
		named, err := reference.ParseNormalizedNamed(item)
		if err == nil {
			// Check if the reference has a tag or digest
			_, isTagged := named.(reference.Tagged)
			_, isDigested := named.(reference.Digested)
			if isTagged || isDigested {
				domain := reference.Domain(named)

				// Check registry name exact matches
				if slices.Contains(allowedRegistryNames, domain) {
					return true
				}

				// Check registry name regex patterns
				for _, re := range allowedRegistryRegexes {
					if re.MatchString(domain) {
						return true
					}
				}
			}
		}
	}
	return false
}

func (m *jobManager) LaunchJobForUser(req *JobRequest) (string, error) {
	if cluster, _ := m.getROSAClusterForUser(req.User); cluster != nil {
		return "", fmt.Errorf("you have already requested a cluster via the `rosa create` command; %d minutes have elapsed", int(time.Since(cluster.CreationTimestamp())/time.Minute))
	}

	// Check the req.Inputs and ensure they all contain a valid version.
	if len(req.Inputs) == 0 {
		return "", fmt.Errorf("the `image_or_version_or_prs` parameter must be specified")
	}
	for _, input := range req.Inputs {
		if !containsValidVersion(input) {
			return "", fmt.Errorf("each use of the `image_or_version_or_prs` parameter must specify a valid OpenShift version.\n\n`%s` has no valid OpenShift version", input)
		}
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
	job.BuildCluster, err = m.schedule(prowJob)
	if err != nil {
		klog.Error(err.Error())
		job.BuildCluster = prowJob.Spec.Cluster
	}

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
		// In the case where a ProwJob has been created, but we fail to get its URL, we shouldn't delete anything
		if !strings.HasPrefix(err.Error(), "timed out waiting for your prowjob") {
			m.lock.Lock()
			defer m.lock.Unlock()
			// Cleanup any active requests and/or jobs
			delete(m.requests, req.User)
			delete(m.jobs, req.Name)

			return "", fmt.Errorf("the requested job cannot be started: %v", err)
		}
		return "", fmt.Errorf("the requested job is taking longer than expected to start: %v", err)
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
		msg = fmt.Sprintf("%s (e.g. `launch 4.19 aws`).\n\n", msg)
	}

	if job.Mode == JobTypeLaunch || job.Mode == JobTypeWorkflowLaunch {
		msg = fmt.Sprintf("%sa <%s|cluster is being created>", msg, prowJobUrl)
		if job.Operator.Is {
			msg = fmt.Sprintf("%s - On completion of the creation of the cluster, your optional operator will begin installation", msg)
			if job.Operator.BundleName != "" {
				msg = fmt.Sprintf("%s using the configuration for the `%s` bundle", msg, job.Operator.BundleName)
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
		if errors.Is(err, errJobCompleted) || strings.Contains(err.Error(), errJobCompleted.Error()) {
			klog.Infof("Job %q aborted due to detecting completion (%s): %v", job.Name, source, err)
		} else {
			if strings.HasPrefix(err.Error(), "timed out waiting for your prowjob") {
				klog.Errorf("Job %q timed out waiting for prowjob to start (%s): %v", job.Name, source, err)
			} else {
				klog.Errorf("Job %q failed to launch (%s): %v", job.Name, source, err)
				job.Failure = err.Error()
			}
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
		slices.Sort(m.recentStartEstimates)
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

func (m *jobManager) CreateMceCluster(user, channel, platform string, from [][]string, duration time.Duration) (string, error) {
	if len(m.mceClusters.clusters) >= maxTotalMCEClusters {
		return "", fmt.Errorf("The maximum number of active MCE clusters (%d) has been reached. Please try again later.", maxTotalMCEClusters) //nolint:staticcheck
	}
	imageset := ""
	if len(from) > 0 && len(from[0]) == 1 {
		imageset = fmt.Sprintf("img%s-multi-appsub", from[0][0])
	}
	var req *JobRequest
	if err := func() error {
		m.mceConfig.Mutex.RLock()
		// this section is nested to allow the defer to be executed before calling the createManagedCluster function
		defer m.mceConfig.Mutex.RUnlock()
		var userConfig MceUser
		userConfig, ok := m.mceConfig.Users[user]
		if !ok {
			// defaults configs for non-defined users
			userConfig = MceUser{
				MaxClusters:   1,
				MaxClusterAge: int(MaxMCEDuration),
			}
		}
		// configure defaults
		if platform == "" {
			platform = "aws"
		}
		if duration == 0 {
			duration = min(time.Duration(userConfig.MaxClusterAge)*time.Hour, MaxMCEDuration)
		}
		m.mceClusters.lock.RLock()
		defer m.mceClusters.lock.RUnlock()
		managed, _, _, _, _ := m.GetManagedClustersForUser(user)
		if len(managed) >= userConfig.MaxClusters {
			return fmt.Errorf("Maximum number of MCE clusters (%d) reached. Please delete an existing cluster before creating a new one. If you have recently deleted a cluster, please wait 1 minute to allow a resync.", userConfig.MaxClusters) //nolint:staticcheck
		}
		if duration.Hours() > float64(userConfig.MaxClusterAge) {
			return fmt.Errorf("Your user's maximum duration for an MCE cluster is %d hours.", userConfig.MaxClusterAge) //nolint:staticcheck
		}
		if !MCEPlatforms.Has(platform) {
			return fmt.Errorf("%s is not a supported platform for MCE.", platform) //nolint:staticcheck
		}
		if !m.mceClusters.imagesets.Has(imageset) {
			// user is requesting a non-GA release or PR; create a job request to generate the requested images/release
			req = &JobRequest{
				User:         user,
				Inputs:       from,
				Type:         JobTypeMCECustomImage,
				Platform:     platform,
				RequestedAt:  time.Now(),
				Architecture: "amd64",
			}
		}
		return nil
	}(); err != nil {
		return "", err
	}
	mceRequestedLifetimeMetric.Observe(duration.Hours())
	cluster, err := m.createManagedCluster(imageset, platform, user, channel, req, duration)
	if err != nil {
		return "", fmt.Errorf("failed to create cluster: %v", err)
	}
	go m.mceSync() // nolint:errcheck
	if req != nil {
		imageset = m.GetMceCustomVersion(cluster)
	}
	msg := fmt.Sprintf("Installing cluster `%s` on platform %s using image %s. Cluster will be alive for %d minutes", cluster.GetName(), platform, imageset, int(duration.Minutes()))
	if req != nil {
		msg += "\nThe release version you have selected is not in the default imageset list. In order to create an MCE cluster with the requested release, a prowjob has been created"
		msg += " to allow MCE to access the images during install. To see this build job, use the `list` command. The MCE cluster will begin installation once"
		msg += " image build is complete. It may take up to 5 minutes after completion of the job for the cluster to begin provisioning."
	}
	return msg, nil
}

func (m *jobManager) GetMceCustomVersion(cluster *clusterv1.ManagedCluster) string {
	var version string
	m.lock.RLock()
	defer m.lock.RUnlock()
	var job *Job
	for _, listedJob := range m.jobs {
		if listedJob.ManagedClusterName == cluster.Name {
			job = listedJob
			break
		}
	}
	if job == nil {
		version = "unspecified version"
	} else {
		var jobInput JobInput
		if len(job.Inputs) > 0 {
			jobInput = job.Inputs[0]
		}
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
		version = strings.Join(inputParts, ",")
	}
	return version
}

func (m *jobManager) DeleteMceCluster(user, clusterName string) (string, error) {
	m.mceClusters.lock.RLock()
	defer m.mceClusters.lock.RUnlock()
	var cluster *clusterv1.ManagedCluster
	for name, existingCluster := range m.mceClusters.clusters {
		if name == clusterName {
			if existingCluster.Annotations[utils.UserTag] != user {
				return fmt.Sprintf("You are not the owner of cluster `%s`", clusterName), nil
			}
			cluster = existingCluster
			break
		}
	}
	if cluster == nil {
		return fmt.Sprintf("Cluster `%s` not found", clusterName), nil
	}
	if err := m.deleteManagedCluster(cluster); err != nil {
		return "", fmt.Errorf("failed to delete cluster: %v", err)
	}
	go m.mceSync() // nolint:errcheck
	return fmt.Sprintf("Cluster %s marked for deletion", clusterName), nil
}

func (m *jobManager) GetManagedClustersForUser(user string) (map[string]*clusterv1.ManagedCluster, map[string]*hivev1.ClusterDeployment, map[string]*hivev1.ClusterProvision, map[string]string, map[string]string) {
	m.mceClusters.lock.RLock()
	defer m.mceClusters.lock.RUnlock()
	managed := make(map[string]*clusterv1.ManagedCluster)
	deployments := make(map[string]*hivev1.ClusterDeployment)
	provisions := make(map[string]*hivev1.ClusterProvision)
	kubeconfigs := make(map[string]string)
	passwords := make(map[string]string)
	for _, mc := range m.mceClusters.clusters {
		if mc.Annotations[utils.UserTag] == user {
			managed[mc.GetName()] = mc
			deployments[mc.GetName()] = m.mceClusters.deployments[mc.GetName()]
			provisions[mc.GetName()] = m.mceClusters.provisions[mc.GetName()]
			kubeconfigs[mc.GetName()] = m.mceClusters.clusterKubeconfigs[mc.GetName()]
			passwords[mc.GetName()] = m.mceClusters.clusterPasswords[mc.GetName()]
		}
	}
	return managed, deployments, provisions, kubeconfigs, passwords
}

func (m *jobManager) ListManagedClusters(user string) (string, string, []string) {
	m.mceClusters.lock.RLock()
	defer m.mceClusters.lock.RUnlock()
	numClusters := len(m.mceClusters.clusters)
	buf := &bytes.Buffer{}
	if numClusters == 0 {
		return "No clusters up", "No clusters up", nil
	}
	beginning := fmt.Sprintf("%d clusters currently running:\n", numClusters)
	fmt.Fprint(buf, beginning)
	var elements []string
	for name, cluster := range m.mceClusters.clusters {
		if user != "" {
			if userTag, ok := cluster.Annotations[utils.UserTag]; ok {
				if userTag != user {
					continue
				}
			}
		}
		expiryTimeTag := cluster.Annotations[utils.ExpiryTimeTag]
		expiryTime, err := time.Parse(time.RFC3339, expiryTimeTag)
		if err != nil {
			klog.Errorf("Failed to parse expiryTime: %v", err)
			element := fmt.Sprintf("- %s (Requested by @%s; Remaining Time: error)\n", name, cluster.Annotations[utils.UserTag])
			elements = append(elements, element)
			fmt.Fprint(buf, element)
			continue
		}
		remainingTime := time.Until(expiryTime)
		provisionStage := "unknown"
		var version string
		if deployment, ok := m.mceClusters.deployments[name]; !ok {
			provisionStage = "waiting for imageset generation"
			version = m.GetMceCustomVersion(cluster)
		} else {
			if strings.HasSuffix(deployment.Spec.Provisioning.ImageSetRef.Name, "appsub") {
				version = deployment.Spec.Provisioning.ImageSetRef.Name
			} else {
				version = m.GetMceCustomVersion(cluster)
			}
			if provision, ok := m.mceClusters.provisions[name]; ok {
				provisionStage = string(provision.Spec.Stage)
			}
			if provisionStage == "unknown" {
				if deployment, ok := m.mceClusters.deployments[name]; ok {
					for _, condition := range deployment.Status.Conditions {
						if condition.Type == hivev1.ProvisionFailedCondition {
							if condition.Status == "True" {
								provisionStage = "failed"
							}
							break
						}
					}
				}
			}
		}
		platform := ""
		switch cluster.Labels["Cloud"] {
		case "Amazon":
			platform = "aws"
		case "Google":
			platform = "gcp"
		}
		element := fmt.Sprintf("- %s (Requested by <@%s>; Provision Status: %s; Remaining Time: %d minutes; Version: %s; Platform: %s)\n", name, cluster.Annotations[utils.UserTag], provisionStage, int(remainingTime/time.Minute), version, platform)
		elements = append(elements, element)
		fmt.Fprint(buf, element)
	}
	return buf.String(), beginning, elements
}

func (m *jobManager) ListMceVersions() string {
	m.mceClusters.lock.RLock()
	defer m.mceClusters.lock.RUnlock()
	imagesets := m.mceClusters.imagesets.UnsortedList()
	imageSemVers := []semver.Version{}
	for _, imageset := range imagesets {
		if strings.HasSuffix(imageset, "-multi-appsub") {
			verString := strings.TrimPrefix(strings.TrimSuffix(imageset, "-multi-appsub"), "img")
			semver, err := semver.ParseTolerant(verString)
			if err != nil {
				continue
			}
			imageSemVers = append(imageSemVers, semver)
		}
	}
	semver.Sort(imageSemVers)
	imageVersions := []string{}
	for _, version := range imageSemVers {
		imageVersions = append(imageVersions, fmt.Sprintf("%d.%d.%d", version.Major, version.Minor, version.Patch))
	}
	return fmt.Sprintf("Available versions for MCE clusters: %s", strings.Join(imageVersions, ", "))
}

func (m *jobManager) CreateRosaCluster(user, channel, version string, duration time.Duration) (string, error) {
	if duration > m.maxRosaAge {
		return "", fmt.Errorf("max duration for a ROSA cluster is %s", m.maxRosaAge.String())
	}
	cluster, _ := m.getROSAClusterForUser(user)
	if cluster != nil {
		return "", fmt.Errorf("you have already requested a cluster; %d minutes have elapsed", int(time.Since(cluster.CreationTimestamp())/time.Minute))
	}
	m.lock.Lock()
	if existing, ok := m.requests[user]; ok {
		klog.Infof("user %q already requested cluster", user)
		m.lock.Unlock()
		return "", fmt.Errorf("you have already requested a cluster via the `launch` command and it should be ready in ~ %d minutes", m.estimateCompletion(existing.RequestedAt)/time.Minute)
	}
	m.lock.Unlock()
	m.rosaClusters.lock.Lock()
	if m.rosaClusterLimit != 0 && len(m.rosaClusters.clusters)+m.rosaClusters.pendingClusters >= m.rosaClusterLimit {
		m.rosaClusters.lock.Unlock()
		return "", errors.New("The maximum number of ROSA clusters has been reached. Please try again later.") //nolint:staticcheck
	}
	m.rosaClusters.pendingClusters++
	m.rosaClusters.lock.Unlock()
	defer func() { m.rosaClusters.lock.Lock(); m.rosaClusters.pendingClusters--; m.rosaClusters.lock.Unlock() }()
	cluster, version, err := m.createRosaCluster(version, user, channel, duration)
	if err != nil {
		return "", fmt.Errorf("failed to create cluster: %w", err)
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

func (m *jobManager) getSupportedRosaVersions() string {
	m.rosaVersions.lock.RLock()
	defer m.rosaVersions.lock.RUnlock()
	var versions []string
	versions = append(versions, m.rosaVersions.versions...)
	sort.Sort(sort.Reverse(sort.StringSlice(versions)))
	return strings.Join(versions, ", ")
}

func (m *jobManager) LookupRosaInputs(versionPrefix string) (string, error) {
	matchedVersions := m.lookupRosaVersions(versionPrefix)
	if len(matchedVersions) == 0 {
		return "", fmt.Errorf("no version with prefix `%s` found", versionPrefix)
	} else if versionPrefix == "" {
		return fmt.Sprintf("The following versions are supported: %v", matchedVersions), nil
	} else {
		return fmt.Sprintf("Found the following version with a prefix of `%s`: %v", versionPrefix, matchedVersions), nil
	}
}

func (m *jobManager) schedule(pj *prowapiv1.ProwJob) (string, error) {
	cluster, err := m.prowScheduler.Schedule(context.TODO(), pj)
	if err != nil {
		return "", fmt.Errorf("failed to schedule job %s: %v", pj.Name, err)
	}
	return cluster.Cluster, nil
}
