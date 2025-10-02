package manager

import (
	"net/url"
	"sync"
	"time"

	"github.com/openshift/ci-chat-bot/pkg/prow"
	"github.com/openshift/ci-chat-bot/pkg/utils"
	"github.com/openshift/rosa/pkg/rosa"
	"github.com/prometheus/client_golang/prometheus"
	clusterv1 "open-cluster-management.io/api/cluster/v1"

	clustermgmtv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	citools "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/lease"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowapiv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowjobClient "sigs.k8s.io/prow/pkg/client/clientset/versioned/typed/prowjobs/v1"
	prowjobLister "sigs.k8s.io/prow/pkg/client/listers/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/scheduler/strategy"
)

// ROSA Errors
const (
	errorRosaGetAll       = "rosa_get_clusters"
	errorRosaGetSingle    = "rosa_get_cluster"
	errorRosaCreate       = "rosa_create_cluster"
	errorRosaRoles        = "rosa_add_roles"
	errorRosaAuth         = "rosa_add_auth"
	errorRosaGetIDP       = "rosa_get_idp"
	errorRosaCreateUser   = "rosa_create_user"
	errorRosaBuildIDP     = "rosa_build_idp"
	errorRosaCreateIDP    = "rosa_create_idp"
	errorRosaConsole      = "rosa_console_ready"
	errorRosaGetSecret    = "rosa_get_secret"
	errorRosaUpdateSecret = "rosa_update_secret"
	errorRosaFailure      = "rosa_cluster_error"
	errorRosaDelete       = "rosa_delete_cluster"
	errorRosaCleanup      = "rosa_cleanup"
	errorRosaDescribe     = "rosa_describe"
	//there are a lot of AWS calls when configuring a cluster; just use one error for them and check the logs for specifics
	errorRosaAWS            = "rosa_aws"
	errorRosaOCM            = "rosa_ocm"
	errorRosaMissingSubnets = "rosa_missing_subnets"
)

// MCE Errors
const (
	errorMCEListImagesets             = "mce_list_imagesets"
	errorMCECleanupImagesets          = "mce_cleanup_imagesets"
	errorMCEListClusterProvisions     = "mce_list_cluster_provisions"
	errorMCEAnnotateNotify            = "mce_annotate_notify"
	errorMCERetrieveUserConfig        = "mce_retrive_user_config"
	errorMCEParseUserConfig           = "mce_parse_user_config"
	errorMCEImagesetJobRun            = "mce_imageset_job_run"
	errorMCEImagesetCreateRef         = "mce_imageset_create_ref"
	errorMCELaunchImagesetJob         = "mce_launch_imageset_job"
	errorMCECreateNamespace           = "mce_create_namespace"
	errorMCEGetPlatformCredentials    = "mce_get_platform_credentials"
	errorMCECreatePlatformCredentials = "mce_create_platform_credentials"
	errorMCEGetPullSecret             = "mce_get_pull_secret"
	errorMCECreatePullSecret          = "mce_create_pull_secret"
	errorMCECreateInstallConfig       = "mce_create_install_config"
	errorMCECreateManagedCluster      = "mce_create_managed_cluster"
	errorMCECreateDeployment          = "mce_create_deployment"
	errorMCEListManagedNamespaces     = "mce_list_managed_namespaces"
	errorMCEGetDeployment             = "mce_get_deployment"
	errorMCEGetAuthSecrets            = "mce_get_auth_secrets"
	errorMCEDeleteClusterImageset     = "mce_delete_cluster_imageset"
	errorMCEDeleteDeployment          = "mce_delete_deployment"
	errorMCEDeleteManagedCluster      = "mce_delete_managed_cluster"
	errorMCEProvisionFailed           = "mce_provision_failed"
)

var errorMetricList = sets.NewString(
	errorRosaGetAll,
	errorRosaGetSingle,
	errorRosaCreate,
	errorRosaRoles,
	errorRosaAuth,
	errorRosaGetIDP,
	errorRosaCreateUser,
	errorRosaBuildIDP,
	errorRosaCreateIDP,
	errorRosaConsole,
	errorRosaGetSecret,
	errorRosaUpdateSecret,
	errorRosaFailure,
	errorRosaDelete,
	errorRosaCleanup,
	errorRosaDescribe,
	errorRosaAWS,
	errorRosaOCM,
	errorRosaMissingSubnets,
	errorMCEListImagesets,
	errorMCECleanupImagesets,
	errorMCEListClusterProvisions,
	errorMCEAnnotateNotify,
	errorMCERetrieveUserConfig,
	errorMCEParseUserConfig,
	errorMCEImagesetJobRun,
	errorMCEImagesetCreateRef,
	errorMCELaunchImagesetJob,
	errorMCECreateNamespace,
	errorMCEGetPlatformCredentials,
	errorMCECreatePlatformCredentials,
	errorMCEGetPullSecret,
	errorMCECreatePullSecret,
	errorMCECreateInstallConfig,
	errorMCECreateManagedCluster,
	errorMCECreateDeployment,
	errorMCEListManagedNamespaces,
	errorMCEGetDeployment,
	errorMCEGetAuthSecrets,
	errorMCEDeleteClusterImageset,
	errorMCEDeleteDeployment,
	errorMCEDeleteManagedCluster,
	errorMCEProvisionFailed,
)

var rosaReadyTimeMetric = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "ci_chat_bot_rosa_ready_duration_minutes",
		Help:    "cluster bot time until rosa cluster is ready duration in minutes",
		Buckets: prometheus.LinearBuckets(1, 1, 30),
	},
)
var rosaReadyToAuthTimeMetric = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "ci_chat_bot_rosa_ready_to_auth_duration_minutes",
		Help:    "cluster bot time for rosa auth to be ready after cluster marked ready duration in minutes",
		Buckets: prometheus.LinearBuckets(1, 1, 30),
	},
)
var rosaAuthTimeMetric = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "ci_chat_bot_rosa_auth_duration_minutes",
		Help:    "cluster bot time until rosa cluster has auth duration in minutes",
		Buckets: prometheus.LinearBuckets(1, 1, 30),
	},
)
var rosaReadyToConsoleTimeMetric = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "ci_chat_bot_rosa_ready_to_console_duration_minutes",
		Help:    "cluster bot time until rosa cluster has console after cluster marked ready and auth succeeds duration in minutes",
		Buckets: prometheus.LinearBuckets(1, 1, 30),
	},
)
var rosaConsoleTimeMetric = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "ci_chat_bot_rosa_console_duration_minutes",
		Help:    "cluster bot time until rosa cluster has console duration in minutes",
		Buckets: prometheus.LinearBuckets(1, 1, 30),
	},
)
var rosaSyncTimeMetric = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "ci_chat_bot_rosa_sync_duration_seconds",
		Help:    "cluster bot rosa sync time in seconds",
		Buckets: prometheus.LinearBuckets(0.05, 0.05, 20),
	},
)

var rosaClustersMetric = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: "ci_chat_bot_rosa_cluster_count",
		Help: "cluster bot number of rosa clusters",
	},
)

var mceSyncTimeMetric = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "ci_chat_bot_mce_sync_duration_seconds",
		Help:    "cluster bot mce sync time in seconds",
		Buckets: prometheus.LinearBuckets(0.05, 0.05, 20),
	},
)

var mceAWSClustersMetric = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: "ci_chat_bot_mce_aws_cluster_count",
		Help: "cluster bot number of MCE AWS clusters",
	},
)
var mceGCPClustersMetric = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: "ci_chat_bot_mce_gcp_cluster_count",
		Help: "cluster bot number of MCE GCP clusters",
	},
)

var mceAWSReadyTimeMetric = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "ci_chat_bot_mce_aws_ready_duration_minutes",
		Help:    "cluster bot time until MCE AWS cluster is ready duration in minutes",
		Buckets: prometheus.LinearBuckets(1, 1, 60),
	},
)

var mceGCPReadyTimeMetric = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "ci_chat_bot_mce_gcp_ready_duration_minutes",
		Help:    "cluster bot time until MCE GCP cluster is ready duration in minutes",
		Buckets: prometheus.LinearBuckets(1, 1, 60),
	},
)

var mceRequestedLifetimeMetric = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Name: "ci_chat_bot_mce_requested_lifetime_hours",
		Help: "cluster bot requested lifetime for MCE clusters in hours",
		// adjust last number to match maximum number of hours allowed
		Buckets: prometheus.LinearBuckets(1, 1, 8),
	},
)

type EnvVar struct {
	name      string
	value     string
	Platforms sets.Set[string]
}

// ConfigResolver finds a ci-operator config for the given tuple of organization, repository,
// branch, and variant.
type ConfigResolver interface {
	Resolve(org, repo, branch, variant string) ([]byte, bool, error)
}

type URLConfigResolver struct {
	URL *url.URL
}

type RosaSubnets struct {
	Subnets sets.Set[string]
	Lock    sync.RWMutex
}

type WorkflowConfig struct {
	Workflows map[string]WorkflowConfigItem `yaml:"workflows"`
	Mutex     sync.RWMutex                  `yaml:"-"` // this field just allows us to update the above values without races
}

type WorkflowConfigItem struct {
	BaseImages   map[string]citools.ImageStreamTagReference `yaml:"base_images,omitempty"`
	Architecture string                                     `yaml:"architecture,omitempty"`
	Platform     string                                     `yaml:"platform"`
}

// LeaseClient only include the metrics function, as we don't want to create leases
type LeaseClient interface {
	// Metrics queries the states of a particular resource, for informational
	// purposes.
	Metrics(rtype string) (lease.Metrics, error)
}

type jobManager struct {
	lock                 sync.RWMutex
	requests             map[string]*JobRequest
	jobs                 map[string]*Job
	started              time.Time
	recentStartEstimates []time.Duration

	clusterPrefix string
	maxClusters   int
	maxAge        time.Duration

	prowConfigLoader    prow.ProwConfigLoader
	prowClient          prowjobClient.ProwV1Interface
	prowLister          prowjobLister.ProwJobLister
	prowScheduler       strategy.Interface
	imageClient         imageclientset.Interface
	hiveConfigMapClient corev1.ConfigMapInterface
	clusterClients      utils.BuildClusterClientConfigMap
	prowNamespace       string
	githubClient        github.Client
	forcePROwner        string

	configResolver ConfigResolver

	muJob struct {
		lock    sync.Mutex
		running map[string]struct{}
	}

	jobNotifierFn  JobCallbackFunc
	workflowConfig *WorkflowConfig

	lClient LeaseClient

	// ROSA fields
	rClient      *rosa.Runtime
	rosaClusters struct {
		lock             sync.RWMutex
		pendingClusters  int
		clusters         map[string]*clustermgmtv1.Cluster
		clusterPasswords map[string]string
	}
	rosaVersions struct {
		lock     sync.RWMutex
		versions []string
	}
	rosaClusterLimit         int
	rosaClusterAdminUsername string
	rosaSubnets              *RosaSubnets
	rosaErrorReported        sets.Set[string]
	rosaOidcConfigId         string
	rosaBillingAccount       string

	maxRosaAge       time.Duration
	defaultRosaAge   time.Duration
	rosaSecretClient corev1.SecretInterface
	rosaNotifierFn   RosaCallbackFunc

	errorMetric *prometheus.CounterVec

	// mce on DPCR cluster
	dpcrCoreClient      *corev1.CoreV1Client
	dpcrOcmClient       crclient.Client
	dpcrHiveClient      crclient.Client
	dpcrNamespaceClient corev1.NamespaceInterface

	mceClusters struct {
		lock               sync.RWMutex
		clusters           map[string]*clusterv1.ManagedCluster
		deployments        map[string]*hivev1.ClusterDeployment
		provisions         map[string]*hivev1.ClusterProvision
		clusterKubeconfigs map[string]string
		clusterPasswords   map[string]string
		imagesets          sets.Set[string]
	}
	mceNotifierFn MCECallbackFunc
	mceConfig     MceConfig
}

// JobRequest keeps information about the request a user made to create
// a job. This is reconstructable from a ProwJob.
type JobRequest struct {
	OriginalMessage string

	User     string
	UserName string

	// Inputs is one or more list of inputs to build a release image. For each input there may be zero or one images or versions, and
	// zero or more pull requests. If a base image or version is present, the PRs are built relative to that version. If no build
	// is necessary then this can be a single build.
	Inputs [][]string

	// Type is the type of job to run. Allowed types are 'install', 'upgrade', 'test', or 'build'. The default is 'build'.
	Type JobType

	// An optional string controlling the platform type for jobs that launch clusters. Required for install or upgrade jobs.
	Platform string

	// WorkflowName is a field used to store the name of the workflow to run for workflow commands
	WorkflowName string

	Channel     string
	RequestedAt time.Time
	Name        string

	JobName   string
	JobParams map[string]string

	Architecture       string
	ManagedClusterName string
}

type JobType string

// JobManager responds to user actions and tracks the state of the launched
// clusters.
type JobManager interface {
	SetNotifier(JobCallbackFunc)
	SetRosaNotifier(RosaCallbackFunc)
	SetMceNotifier(MCECallbackFunc)

	LaunchJobForUser(req *JobRequest) (string, error)
	CreateRosaCluster(user, channel, version string, duration time.Duration) (string, error)
	CheckValidJobConfiguration(req *JobRequest) error
	SyncJobForUser(user string) (string, error)
	TerminateJobForUser(user string) (string, error)
	GetLaunchJob(user string) (*Job, error)
	GetROSACluster(user string) (*clustermgmtv1.Cluster, string)
	DescribeROSACluster(cluster string) (string, error)
	LookupInputs(inputs []string, architecture string) (string, error)
	LookupRosaInputs(versionPrefix string) (string, error)
	ListJobs(users string, filters ListFilters) string
	GetWorkflowConfig() *WorkflowConfig
	ResolveImageOrVersion(imageOrVersion, defaultImageOrVersion, architecture string) (string, string, string, error)
	ResolveAsPullRequest(spec string) (*prowapiv1.Refs, error)

	CreateMceCluster(user, channel, platform string, from [][]string, duration time.Duration) (string, error)
	DeleteMceCluster(user, clusterName string) (string, error)
	GetManagedClustersForUser(user string) (map[string]*clusterv1.ManagedCluster, map[string]*hivev1.ClusterDeployment, map[string]*hivev1.ClusterProvision, map[string]string, map[string]string)
	ListManagedClusters(user string) string
	ListMceVersions() string
	GetMceUserConfig() *MceConfig
	GetUserCluster(user string) *Job
}

// JobCallbackFunc is invoked when the job changes state in a significant
// way.
type JobCallbackFunc func(Job)

// RosaCallbackFunc is invoked when the rosa cluster changes state in a significant
// way. Takes the cluster object and admin password.
type RosaCallbackFunc func(*clustermgmtv1.Cluster, string)

// RosaCallbackFunc is invoked when the rosa cluster changes state in a significant
// way. Takes the ManagedCluster object, kubeconfig, and admin password.
type MCECallbackFunc func(*clusterv1.ManagedCluster, *hivev1.ClusterDeployment, *hivev1.ClusterProvision, string, string, error)

// JobInput defines the input to a job. Different modes need different inputs.
type JobInput struct {
	Image    string
	RunImage string
	Version  string
	Refs     []prowapiv1.Refs
}

// Job responds to user requests and tracks the state of the launched
// jobs. This object must be recreatable from a ProwJob, but the RequestedChannel
// field may be empty to indicate the user has already been notified.
type Job struct {
	Name string

	OriginalMessage string

	State   prowapiv1.ProwJobState
	JobName string
	URL     string

	Platform  string
	JobParams map[string]string

	Mode string

	Inputs []JobInput

	Credentials     string
	PasswordSnippet string
	Failure         string

	RequestedBy      string
	RequesterUserID  string
	RequestedChannel string

	RequestedAt   time.Time
	ExpiresAt     time.Time
	StartDuration time.Duration
	Complete      bool

	Architecture string
	BuildCluster string

	WorkflowName string

	UseSecondaryAccount bool

	Operator        OperatorInfo
	CatalogComplete bool
	CatalogError    bool

	ManagedClusterName string
}

type OperatorInfo struct {
	Is         bool
	HasIndex   bool
	BundleName string
}

type HypershiftSupportedVersionsType struct {
	Mu       sync.RWMutex
	Versions sets.Set[string]
}

type ListFilters struct {
	Platform  string
	Version   string
	Requestor string
}

type MceConfig struct {
	Users map[string]MceUser `yaml:"users"`
	Mutex sync.RWMutex       `yaml:"-"` // this field just allows us to update the above values without races
}

type MceUser struct {
	MaxClusters   int    `yaml:"max_clusters,omitempty"`
	MaxClusterAge int    `yaml:"max_cluster_age_hours,omitempty"`
	AwsSecret     string `yaml:"aws_secret,omitempty"`
	AwsBaseDomain string `yaml:"aws_base_domain,omitempty"`
	GcpSecret     string `yaml:"gcp_secret,omitempty"`
	GcpBaseDomain string `yaml:"gcp_base_domain,omitempty"`
	GcpProjectID  string `yaml:"gcp_project_id,omitempty"`
}
