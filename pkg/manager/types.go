package manager

import (
	"net/url"
	"sync"
	"time"

	"github.com/openshift/ci-chat-bot/pkg/prow"
	"github.com/openshift/ci-chat-bot/pkg/utils"
	"github.com/openshift/rosa/pkg/rosa"
	"github.com/prometheus/client_golang/prometheus"

	citools "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/lease"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"

	clustermgmtv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	prowapiv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/github"
)

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

type EnvVar struct {
	name      string
	value     string
	Platforms sets.String
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
	Subnets sets.String
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
	lock                 sync.Mutex
	requests             map[string]*JobRequest
	jobs                 map[string]*Job
	started              time.Time
	recentStartEstimates []time.Duration

	clusterPrefix string
	maxClusters   int
	maxAge        time.Duration

	prowConfigLoader    prow.ProwConfigLoader
	prowClient          dynamic.NamespaceableResourceInterface
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
	rosaClusterLimit  int
	rosaSubnets       *RosaSubnets
	rosaErrorReported sets.String

	maxRosaAge       time.Duration
	defaultRosaAge   time.Duration
	rosaSecretClient corev1.SecretInterface
	rosaNotifierFn   RosaCallbackFunc

	errorMetric *prometheus.CounterVec
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

	Architecture string
}

type JobType string

// JobManager responds to user actions and tracks the state of the launched
// clusters.
type JobManager interface {
	SetNotifier(JobCallbackFunc)
	SetRosaNotifier(RosaCallbackFunc)

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
}

// JobCallbackFunc is invoked when the job changes state in a significant
// way.
type JobCallbackFunc func(Job)

// RosaCallbackFunc is invoked when the rosa cluster changes state in a significant
// way. Takes the cluster object and admin password.
type RosaCallbackFunc func(*clustermgmtv1.Cluster, string)

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

	IsOperator         bool
	OperatorBundleName string
}

type HypershiftSupportedVersionsType struct {
	Mu       sync.RWMutex
	Versions sets.String
}

type ListFilters struct {
	Platform  string
	Version   string
	Requestor string
}
