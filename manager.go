package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/test-infra/prow/github"

	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"

	"github.com/blang/semver"

	imagev1 "github.com/openshift/api/image/v1"
	"github.com/openshift/ci-chat-bot/pkg/prow"
	citools "github.com/openshift/ci-tools/pkg/api"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/dynamic"
	prowapiv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

const (
	// maxJobsPerUser limits the number of simultaneous jobs a user can launch to prevent
	// a single user from consuming the infrastructure account.
	maxJobsPerUser = 23

	// maxTotalClusters limits the number of simultaneous clusters across all users to
	// prevent saturating the infrastructure account.
	maxTotalClusters = 80
)

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

func (j Job) IsComplete() bool {
	return j.Complete || len(j.Credentials) > 0 || (len(j.State) > 0 && j.State != prowapiv1.PendingState)
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
	clusterClients   BuildClusterClientConfigMap
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

	lClient leaseClient
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
	buildClusterClientConfigMap BuildClusterClientConfigMap,
	githubClient github.Client,
	forcePROwner string,
	workflowConfig *WorkflowConfig,
	lClient leaseClient,
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
	}
	m.muJob.running = make(map[string]struct{})
	return m
}

func (m *jobManager) Start() error {
	go wait.Forever(func() {
		if err := m.sync(); err != nil {
			klog.Infof("error during sync: %v", err)
			return
		}
		time.Sleep(5 * time.Minute)
	}, time.Minute)
	return nil
}

func paramsFromAnnotation(value string) (map[string]string, error) {
	values := make(map[string]string)
	if len(value) == 0 {
		return values, nil
	}
	for _, part := range strings.Split(value, ",") {
		if len(part) == 0 {
			return nil, fmt.Errorf("parameter may not be empty")
		}
		parts := strings.SplitN(part, "=", 2)
		key := strings.TrimSpace(parts[0])
		if len(key) == 0 {
			return nil, fmt.Errorf("parameter name may not be empty")
		}
		if len(parts) == 1 {
			values[key] = ""
			continue
		}
		values[key] = parts[1]
	}
	return values, nil
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

func (m *jobManager) sync() error {
	u, err := m.prowClient.Namespace(m.prowNamespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{
			launchLabel: "true",
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
	if m.started.IsZero() {
		m.started = now
	}

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
		}

		// This is a new annotation and there may be a brief window where not all
		// prowjobs possess the annotation.
		if targetType, ok := job.Annotations["ci-chat-bot.openshift.io/targetType"]; ok {
			j.TargetType = targetType
		} else {
			j.TargetType = "template"
		}

		var err error
		j.JobParams, err = paramsFromAnnotation(job.Annotations["ci-chat-bot.openshift.io/jobParams"])
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
						params, err := paramsFromAnnotation(job.Annotations["ci-chat-bot.openshift.io/jobParams"])
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
	m.notifierFn = fn
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

	lastEstimate := median - time.Now().Sub(requestedAt)
	if lastEstimate < 0 {
		return time.Minute
	}
	return lastEstimate.Truncate(time.Second)
}

func contains(arr []string, s string) bool {
	for _, item := range arr {
		if s == item {
			return true
		}
	}
	return false
}

func (m *jobManager) ListJobs(users ...string) string {
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
			if contains(users, job.RequestedBy) {
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
			var details string
			if len(job.URL) > 0 {
				details = fmt.Sprintf(", <%s|view logs>", job.URL)
			}
			var imageOrVersion string
			var inputParts []string
			var jobInput JobInput
			if len(job.Inputs) > 0 {
				jobInput = job.Inputs[0]
			}
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
				details = fmt.Sprintf("<%s|%s>", job.URL, stripLinks(job.OriginalMessage))
			case len(job.URL) > 0:
				details = fmt.Sprintf("<%s|%s>", job.URL, job.JobName)
			case len(job.OriginalMessage) > 0:
				details = stripLinks(job.OriginalMessage)
			default:
				details = job.JobName
			}
			if len(job.RequestedBy) > 0 {
				details += fmt.Sprintf(" <@%s>", job.RequestedBy)
			}
			fmt.Fprintln(buf, details)
		}
	} else if totalJobs > 0 {
		fmt.Fprintf(buf, "\nThere are %d test jobs being run by the bot right now", len(jobs))
	}

	fmt.Fprintf(buf, "\nbot uptime is %.1f minutes", now.Sub(m.started).Seconds()/60)
	return buf.String()
}

type callbackFunc func(job Job)

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

var reBranchVersion = regexp.MustCompile(`^(openshift-|release-)(\d+\.\d+)$`)

func versionForRefs(refs *prowapiv1.Refs) string {
	if refs == nil || len(refs.BaseRef) == 0 {
		return ""
	}
	if refs.BaseRef == "master" || refs.BaseRef == "main" {
		return "4.12.0-0.latest"
	}
	if m := reBranchVersion.FindStringSubmatch(refs.BaseRef); m != nil {
		return fmt.Sprintf("%s.0-0.latest", m[2])
	}
	return ""
}

var reMajorMinorVersion = regexp.MustCompile(`^(\d+)\.(\d+)$`)

func buildPullSpec(namespace, tagName, isName string) string {
	var delimiter = ":"
	if strings.HasPrefix(tagName, "sha256:") {
		delimiter = "@"
	}
	return fmt.Sprintf("registry.ci.openshift.org/%s/%s%s%s", namespace, isName, delimiter, tagName)
}

// resolveImageOrVersion returns installSpec, tag name or version, runSpec, and error
func (m *jobManager) resolveImageOrVersion(imageOrVersion, defaultImageOrVersion, architecture string) (string, string, string, error) {
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
	case "arm64":
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp-arm64", Imagestream: "release-arm64", ArchSuffix: "-arm64"})
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp-arm64", Imagestream: "4-dev-preview-arm64", ArchSuffix: "-arm64"})
	case "multi":
		// the release-controller cannot assemble multi-arch release, so we must use the `art-latest` streams instead of `release-multi`
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp-multi", Imagestream: "4.12-art-latest-multi", ArchSuffix: "-multi"})
		imagestreams = append(imagestreams, namespaceAndStream{Namespace: "ocp-multi", Imagestream: "4.11-art-latest-multi", ArchSuffix: "-multi"})
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
			unresolved = fmt.Sprintf("4.12.0-0.nightly%s", archSuffix)
		} else if unresolved == "ci" {
			unresolved = fmt.Sprintf("4.12.0-0.ci%s", archSuffix)
		} else if unresolved == "prerelease" {
			unresolved = fmt.Sprintf("4.12.0-0.ci%s", archSuffix)
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

func (m *jobManager) LookupInputs(inputs []string, architecture string) (string, error) {
	// default install type jobs to "ci"
	if len(inputs) == 0 {
		_, version, _, err := m.resolveImageOrVersion("ci", "", architecture)
		if err != nil {
			return "", err
		}
		inputs = []string{version}
	}
	jobInputs, err := m.lookupInputs([][]string{inputs}, architecture)
	if err != nil {
		return "", err
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

func (m *jobManager) lookupInputs(inputs [][]string, architecture string) ([]JobInput, error) {
	var jobInputs []JobInput

	for _, input := range inputs {
		var jobInput JobInput
		for _, part := range input {
			// if the user provided a pull spec (org/repo#number) we'll build from that
			pr, err := m.resolveAsPullRequest(part)
			if err != nil {
				return nil, err
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
				image, version, runImage, err := m.resolveImageOrVersion(part, "", architecture)
				if err != nil {
					return nil, err
				}
				if len(image) == 0 {
					return nil, fmt.Errorf("unable to resolve %q to an image", part)
				}
				if len(jobInput.Image) > 0 {
					return nil, fmt.Errorf("only one image or version may be specified in a list of installs")
				}
				if architecture == "arm64" && (len(runImage) == 0 || len(version) == 0) {
					return nil, fmt.Errorf("only version numbers (like: 4.11.0) may be used for arm64 based clusters")
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
	return jobInputs, nil
}

func (m *jobManager) resolveAsPullRequest(spec string) (*prowapiv1.Refs, error) {
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

	// default install type jobs to "ci"
	if len(req.Inputs) == 0 && req.Type == JobTypeInstall {
		_, version, _, err := m.resolveImageOrVersion("ci", "", job.Architecture)
		if err != nil {
			return nil, err
		}
		req.Inputs = append(req.Inputs, []string{version})
	}
	jobInputs, err := m.lookupInputs(req.Inputs, job.Architecture)
	if err != nil {
		return nil, err
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
	for param, env := range multistageParameters {
		if env.platforms.Has(platform) {
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
		// the `no-spot` param is just a dummy param to disable use of spot instances for basic aws cluster launches
		if k == "no-spot" {
			continue
		}
		if contains(supportedParameters, k) && !platformParams.Has(k) && k != "test" { // we only need parameters that are not configured via multistage env vars
			variants.Insert(k)
		}
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

func (m *jobManager) LaunchJobForUser(req *JobRequest) (string, error) {
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
		var primaryHasVariant bool
		// Currently, there is an important difference between the template e2e-upgrade-all test and what can be done with the step-registry tests.
		// For now, fallback to templates for this test until this difference can be resolved.
		if test := job.JobParams["test"]; test != "e2e-upgrade-all" {
			architectureLabel := req.Architecture
			// multiarch image launches use amd64 jobs
			if architectureLabel == "multi" {
				architectureLabel = "amd64"
			}
			primarySelector := labels.Set{"job-env": req.Platform, "job-type": JobTypeLaunch, "config-type": "modern", "job-architecture": architectureLabel} // these jobs will only contain configs using non-deprecated features
			prowJob, _ = prow.JobForLabels(m.prowConfigLoader, labels.SelectorFromSet(primarySelector))
			if prowJob != nil {
				if sourceEnv, _, ok := firstEnvVar(prowJob.Spec.PodSpec, "UNRESOLVED_CONFIG"); ok { // all multistage configs will be unresolved
					primaryHasVariant, _, err = configContainsVariant(req.JobParams, req.Platform, sourceEnv.Value, job.Mode)
					if err != nil {
						return "", err
					}
				}
			}
		}
		if !primaryHasVariant && req.Architecture == "amd64" { // only support legacy templates for amd64 arch
			job.LegacyConfig = true
			fallbackSelector := labels.Set{"job-env": req.Platform, "job-type": JobTypeLaunch, "config-type": "legacy"} // these jobs will contain older, deprecated configs that can be used as fallback for the primary config typr
			prowJob, _ = prow.JobForLabels(m.prowConfigLoader, labels.SelectorFromSet(fallbackSelector))
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
				minutes := waitUntil.Sub(time.Now()).Minutes()
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
	if job.LegacyConfig {
		msg = "WARNING: using legacy template based job for this cluster. This is unsupported and the cluster may not install as expected. Contact #forum-crt for more information.\n"
	}

	if UseSpotInstances(job) {
		msg = fmt.Sprintf("%s\nThis AWS cluster will use Spot instances for the worker nodes.", msg)
		msg = fmt.Sprintf("%s This means that worker nodes may unexpectedly disappear, but will be replaced automatically.", msg)
		msg = fmt.Sprintf("%s If your workload cannot tolerate disruptions, add the `no-spot` option to the options argument when launching your cluster.", msg)
		msg = fmt.Sprintf("%s For more information on Spot instances, see this blog post: https://cloud.redhat.com/blog/a-guide-to-red-hat-openshift-and-aws-spot-instances.\n\n", msg)
	}

	if job.Mode == JobTypeLaunch || job.Mode == JobTypeWorkflowLaunch {
		msg = fmt.Sprintf("%sa <%s|cluster is being created>", msg, prowJobUrl)
		if job.IsOperator {
			msg = fmt.Sprintf("%s - On completion of the creation of the cluster, your optional operator will begin installation. I'll send you the credentials once both the cluster and the operator are ready", msg)
		} else {
			msg = fmt.Sprintf("%s - I'll send you the credentials in about %d minutes", msg, m.estimateCompletion(req.RequestedAt)/time.Minute)
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
		msg = fmt.Sprintf("cluster had previously been marked as successful, checking again")
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
		if m.notifierFn != nil {
			go m.notifierFn(job)
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
