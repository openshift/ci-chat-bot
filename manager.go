package main

import (
	"bytes"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/blang/semver"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/openshift/ci-chat-bot/pkg/prow"
	prowapiv1 "github.com/openshift/ci-chat-bot/pkg/prow/apiv1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned"
)

// JobRequest keeps information about the request a user made to create
// a job. This is reconstructable from a ProwJob.
type JobRequest struct {
	User string

	// InstallImageVersion is a version or image to install
	InstallImageVersion string
	// UpgradeImageVersion, if specified, is the version to upgrade to
	UpgradeImageVersion string

	Channel     string
	RequestedAt time.Time
	Name        string
	JobName     string
}

// JobManager responds to user actions and tracks the state of the launched
// clusters.
type JobManager interface {
	SetNotifier(JobCallbackFunc)

	LaunchJobForUser(req *JobRequest) (string, error)
	SyncJobForUser(user string) (string, error)
	GetLaunchJob(user string) (*Job, error)
	ListJobs(users ...string) string
}

// JobCallbackFunc is invoked when the job changes state in a significant
// way.
type JobCallbackFunc func(Job)

// Job responds to user requests and tracks the state of the launched
// jobs. This object must be recreatable from a ProwJob, but the RequestedChannel
// field may be empty to indicate the user has already been notified.
type Job struct {
	Name string

	State   prowapiv1.ProwJobState
	JobName string
	URL     string

	Mode         string
	InstallImage string
	UpgradeImage string

	// these fields are only set when this is an upgrade job between two known versions
	InstallVersion string
	UpgradeVersion string

	Credentials     string
	PasswordSnippet string
	Failure         string

	RequestedBy      string
	RequestedChannel string

	RequestedAt time.Time
	ExpiresAt   time.Time
}

type jobManager struct {
	lock         sync.Mutex
	requests     map[string]*JobRequest
	jobs         map[string]*Job
	lastEstimate time.Duration
	started      time.Time

	clusterPrefix string
	maxClusters   int
	maxAge        time.Duration

	prowConfigLoader prow.ProwConfigLoader
	prowClient       dynamic.NamespaceableResourceInterface
	coreClient       clientset.Interface
	imageClient      imageclientset.Interface
	coreConfig       *rest.Config
	prowNamespace    string

	notifierFn JobCallbackFunc
}

// NewJobManager creates a manager that will track the requests made by a user to create clusters
// and reflect that state into ProwJobs that launch clusters. It attempts to recreate state on startup
// by querying prow, but does not guarantee that some notifications to users may not be sent or may be
// sent twice.
func NewJobManager(prowConfigLoader prow.ProwConfigLoader, prowClient dynamic.NamespaceableResourceInterface, coreClient clientset.Interface, imageClient imageclientset.Interface, config *rest.Config) *jobManager {
	return &jobManager{
		requests:      make(map[string]*JobRequest),
		jobs:          make(map[string]*Job),
		clusterPrefix: "chat-bot-",
		maxClusters:   10,
		maxAge:        2 * time.Hour,
		lastEstimate:  10 * time.Minute,

		prowConfigLoader: prowConfigLoader,
		prowClient:       prowClient,
		coreClient:       coreClient,
		coreConfig:       config,
		imageClient:      imageClient,
		prowNamespace:    "ci",
	}
}

func (m *jobManager) Start() error {
	go wait.Forever(func() {
		if err := m.sync(); err != nil {
			log.Printf("error during sync: %v", err)
			return
		}
		time.Sleep(5 * time.Minute)
	}, time.Minute)
	return nil
}

func (m *jobManager) sync() error {
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

	m.lock.Lock()
	defer m.lock.Unlock()

	now := time.Now()
	if m.started.IsZero() {
		m.started = now
	}

	for _, job := range list.Items {
		previous := m.jobs[job.Name]

		j := &Job{
			Name:             job.Name,
			State:            job.Status.State,
			URL:              job.Status.URL,
			Mode:             job.Annotations["ci-chat-bot.openshift.io/mode"],
			JobName:          job.Spec.Job,
			InstallImage:     job.Annotations["ci-chat-bot.openshift.io/releaseImage"],
			UpgradeImage:     job.Annotations["ci-chat-bot.openshift.io/upgradeImage"],
			InstallVersion:   job.Annotations["release.openshift.io/from-tag"],
			UpgradeVersion:   job.Annotations["release.openshift.io/tag"],
			RequestedBy:      job.Annotations["ci-chat-bot.openshift.io/user"],
			RequestedChannel: job.Annotations["ci-chat-bot.openshift.io/channel"],
			RequestedAt:      job.CreationTimestamp.Time,
			ExpiresAt:        job.CreationTimestamp.Time.Add(m.maxAge),
		}
		if job.Status.CompletionTime != nil {
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

			if j.Mode == "launch" {
				if user := j.RequestedBy; len(user) > 0 {
					if _, ok := m.requests[user]; !ok {
						m.requests[user] = &JobRequest{
							User:                user,
							Name:                job.Name,
							JobName:             job.Spec.Job,
							InstallImageVersion: job.Annotations["ci-chat-bot.openshift.io/releaseImage"],
							UpgradeImageVersion: job.Annotations["ci-chat-bot.openshift.io/upgradeImage"],
							RequestedAt:         job.CreationTimestamp.Time,
							Channel:             job.Annotations["ci-chat-bot.openshift.io/channel"],
						}
					}
				}
			}

			m.jobs[job.Name] = j
			if previous == nil || previous.State != j.State || len(j.Credentials) == 0 {
				go m.handleJobStartup(*j)
			}
		}
	}

	// forget everything that is too old
	for _, job := range m.jobs {
		if job.ExpiresAt.Before(now) {
			log.Printf("job %q is expired", job.Name)
			delete(m.jobs, job.Name)
		}
	}
	for _, req := range m.requests {
		if req.RequestedAt.Add(m.maxAge * 2).Before(now) {
			log.Printf("request %q is expired", req.User)
			delete(m.requests, req.User)
		}
	}

	log.Printf("Job sync complete, %d jobs and %d requests", len(m.jobs), len(m.requests))
	return nil
}

func (m *jobManager) SetNotifier(fn JobCallbackFunc) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.notifierFn = fn
}

func (m *jobManager) estimateCompletion(requestedAt time.Time) time.Duration {
	if requestedAt.IsZero() {
		return m.lastEstimate.Truncate(time.Second)
	}
	lastEstimate := m.lastEstimate - time.Now().Sub(requestedAt)
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
	for _, job := range m.jobs {
		if job.Mode == "launch" {
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
	buf := &bytes.Buffer{}
	now := time.Now()
	if len(clusters) == 0 {
		fmt.Fprintf(buf, "No clusters up (start time is approximately %.1f minutes):\n\n", m.lastEstimate.Seconds()/60)
	} else {
		fmt.Fprintf(buf, "%d/%d clusters up (start time is approximately %.1f minutes):\n\n", len(clusters), m.maxClusters, m.lastEstimate.Seconds()/60)
		for _, job := range clusters {
			var details string
			if len(job.URL) > 0 {
				details = fmt.Sprintf(", <%s|view logs>", job.URL)
			}
			switch {
			case job.State == prowapiv1.SuccessState:
				fmt.Fprintf(buf, "• %d minutes ago by <@%s> - cluster has been shut down%s\n", int(now.Sub(job.RequestedAt)/time.Minute), job.RequestedBy, details)
			case job.State == prowapiv1.FailureState:
				fmt.Fprintf(buf, "• %d minutes ago by <@%s> - cluster failed to start%s\n", int(now.Sub(job.RequestedAt)/time.Minute), job.RequestedBy, details)
			case len(job.Credentials) > 0:
				fmt.Fprintf(buf, "• %d minutes ago by <@%s> - available and will be torn down in %d minutes\n", int(now.Sub(job.RequestedAt)/time.Minute), job.RequestedBy, int(job.ExpiresAt.Sub(now)/time.Minute))
			case len(job.Failure) > 0:
				fmt.Fprintf(buf, "• %d minutes ago by <@%s> - failure: %s%s\n", int(now.Sub(job.RequestedAt)/time.Minute), job.RequestedBy, job.Failure, details)
			default:
				fmt.Fprintf(buf, "• %d minutes ago by <@%s> - starting%s\n", int(now.Sub(job.RequestedAt)/time.Minute), job.RequestedBy, details)
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
			if len(job.URL) > 0 {
				details = fmt.Sprintf("<%s|%s>", job.URL, job.JobName)
			} else {
				details = job.JobName
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
	return &copied, nil
}

func (m *jobManager) resolveImageOrVersion(imageOrVersion string) (string, string, error) {
	if len(strings.TrimSpace(imageOrVersion)) == 0 {
		return "", "", nil
	}
	if strings.Contains(imageOrVersion, "/") {
		return imageOrVersion, "", nil
	}
	tag, err := m.imageClient.ImageV1().ImageStreamTags("ocp").Get(fmt.Sprintf("release:%s", imageOrVersion), metav1.GetOptions{})
	if err != nil {
		return "", "", fmt.Errorf("unable to find release %q on https://openshift-release.svc.ci.openshift.org", imageOrVersion)
	}
	return fmt.Sprintf("registry.svc.ci.openshift.org/ocp/release@%s", tag.Image.Name), imageOrVersion, nil
}

func (m *jobManager) LaunchJobForUser(req *JobRequest) (string, error) {
	installImage, installVersion, err := m.resolveImageOrVersion(req.InstallImageVersion)
	if err != nil {
		return "", err
	}
	upgradeImage, upgradeVersion, err := m.resolveImageOrVersion(req.UpgradeImageVersion)
	if err != nil {
		return "", err
	}

	mode := "launch"
	if len(upgradeImage) > 0 {
		mode = "upgrade"
	}

	// try to pick a job that matches the install version, if we can, otherwise use the first that
	// matches us (we can do better)
	var job *prowapiv1.ProwJob
	selector := labels.Set{"job-env": "aws", "job-type": mode}
	if len(installVersion) > 0 {
		if v, err := semver.ParseTolerant(installVersion); err == nil {
			withRelease := labels.Merge(selector, labels.Set{"job-release": fmt.Sprintf("%d.%d", v.Major, v.Minor)})
			job, _ = prow.JobForLabels(m.prowConfigLoader, labels.SelectorFromSet(withRelease))
		}
	}
	if job == nil {
		job, _ = prow.JobForLabels(m.prowConfigLoader, labels.SelectorFromSet(selector))
	}
	if job == nil {
		return "", fmt.Errorf("configuration error, unable to find job matching %s", selector)
	}
	jobName := job.Spec.Job
	log.Printf("Selected %s job %s for user - %s->%s %s->%s", mode, jobName, installVersion, upgradeVersion, installImage, upgradeImage)

	m.lock.Lock()
	defer m.lock.Unlock()

	user := req.User
	if len(user) == 0 {
		return "", fmt.Errorf("must specify the name of the user who requested this cluster")
	}

	req.RequestedAt = time.Now()

	if mode == "launch" {
		existing, ok := m.requests[user]
		if ok {
			if len(existing.Name) == 0 {
				log.Printf("user %q already requested cluster", user)
				return "", fmt.Errorf("you have already requested a cluster and it should be ready in ~ %d minutes", m.estimateCompletion(existing.RequestedAt)/time.Minute)
			}
			if job, ok := m.jobs[existing.Name]; ok {
				if len(job.Credentials) > 0 {
					log.Printf("user %q cluster is already up", user)
					return "your cluster is already running, see your credentials again with the 'auth' command", nil
				}
				if len(job.Failure) == 0 {
					log.Printf("user %q cluster has no credentials yet", user)
					return "", fmt.Errorf("you have already requested a cluster and it should be ready in ~ %d minutes", m.estimateCompletion(existing.RequestedAt)/time.Minute)
				}

				log.Printf("user %q cluster failed, allowing them to request another", user)
				delete(m.jobs, existing.Name)
				delete(m.requests, user)
			}
		}
		m.requests[user] = req

		launchedClusters := 0
		for _, job := range m.jobs {
			if job != nil && job.Mode == "launch" {
				launchedClusters++
			}
		}
		if launchedClusters >= m.maxClusters {
			log.Printf("user %q is will have to wait", user)
			var waitUntil time.Time
			for _, c := range m.jobs {
				if c == nil || c.Mode != "launch" {
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
			if job != nil && job.Mode != "launch" && job.RequestedBy == user {
				running++
			}
		}
		if running > 3 {
			return "", fmt.Errorf("you can't have more than 3 running jobs at a time")
		}
	}

	newJob := &Job{
		Name:    fmt.Sprintf("%s%s", m.clusterPrefix, req.RequestedAt.UTC().Format("2006-01-02-150405.9999")),
		State:   prowapiv1.PendingState,
		JobName: jobName,
		Mode:    mode,

		InstallImage:   installImage,
		UpgradeImage:   upgradeImage,
		InstallVersion: installVersion,
		UpgradeVersion: upgradeVersion,

		RequestedBy:      user,
		RequestedChannel: req.Channel,
		RequestedAt:      req.RequestedAt,

		ExpiresAt: req.RequestedAt.Add(m.maxAge),
	}
	req.Name = newJob.Name
	m.jobs[newJob.Name] = newJob

	log.Printf("user %q requests cluster %q", user, newJob.Name)
	go m.handleJobStartup(*newJob)

	if mode == "launch" {
		return "", fmt.Errorf("a cluster is being created - I'll send you the credentials in about ~%d minutes", m.estimateCompletion(req.RequestedAt)/time.Minute)
	}
	return "", fmt.Errorf("job has been started")
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
	log.Printf("user %q requests job %q to be refreshed", user, copied.Name)
	go m.handleJobStartup(copied)

	return msg, nil
}

func (m *jobManager) handleJobStartup(job Job) {
	if err := m.launchJob(&job); err != nil {
		log.Printf("failed to launch job: %v", err)
		job.Failure = err.Error()
	}
	if job.Mode == "launch" {
		m.finishedJob(job)
	}
}

func (m *jobManager) finishedJob(job Job) {
	m.lock.Lock()
	defer m.lock.Unlock()

	now := time.Now()
	if job.Mode == "launch" && len(job.Credentials) > 0 {
		m.lastEstimate = now.Sub(job.RequestedAt)
	}

	log.Printf("completed job request for %s and notifying participants (%s)", job.Name, job.RequestedBy)

	m.jobs[job.Name] = &job
	for _, request := range m.requests {
		if request.Name == job.Name {
			log.Printf("notify %q that job %q is complete", request.User, request.Name)
			if m.notifierFn != nil {
				go m.notifierFn(job)
			}
		}
	}
}
