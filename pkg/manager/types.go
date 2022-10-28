package manager

import (
	"github.com/openshift/ci-chat-bot/pkg/prow"
	"github.com/openshift/ci-chat-bot/pkg/utils"
	citools "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/lease"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	prowapiv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/github"
	"net/url"
	"sync"
	"time"
)

type envVar struct {
	name      string
	value     string
	platforms sets.String
}

// ConfigResolver finds a ci-operator config for the given tuple of organization, repository,
// branch, and variant.
type ConfigResolver interface {
	Resolve(org, repo, branch, variant string) ([]byte, bool, error)
}

type URLConfigResolver struct {
	URL *url.URL
}

type callbackFunc func(job Job)

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

	prowConfigLoader prow.ProwConfigLoader
	prowClient       dynamic.NamespaceableResourceInterface
	imageClient      imageclientset.Interface
	clusterClients   utils.BuildClusterClientConfigMap
	prowNamespace    string
	githubClient     github.Client
	forcePROwner     string

	configResolver ConfigResolver

	muJob struct {
		lock    sync.Mutex
		running map[string]struct{}
	}

	notifierFn     JobCallbackFunc
	workflowConfig *WorkflowConfig

	lClient LeaseClient
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

	LaunchJobForUser(req *JobRequest) (string, error)
	SyncJobForUser(user string) (string, error)
	TerminateJobForUser(user string) (string, error)
	GetLaunchJob(user string) (*Job, error)
	LookupInputs(inputs []string, architecture string) (string, error)
	ListJobs(users ...string) string
}

// JobCallbackFunc is invoked when the job changes state in a significant
// way.
type JobCallbackFunc func(Job)

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

	TargetType string
	Mode       string

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

	LegacyConfig bool

	WorkflowName string

	UseSecondaryAccount bool

	IsOperator bool
}
