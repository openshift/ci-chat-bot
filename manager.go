package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
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

	imagev1 "github.com/openshift/api/image/v1"
	"github.com/openshift/ci-chat-bot/pkg/prow"
	prowapiv1 "github.com/openshift/ci-chat-bot/pkg/prow/apiv1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned"
)

const (
	// maxJobsPerUser limits the number of simultaneous jobs a user can launch to prevent
	// a single user from consuming the infrastructure account.
	maxJobsPerUser = 10

	// maxTotalClusters limits the number of simultaneous clusters across all users to
	// prevent saturating the infrastructure account.
	maxTotalClusters = 15
)

// JobRequest keeps information about the request a user made to create
// a job. This is reconstructable from a ProwJob.
type JobRequest struct {
	User string

	// InstallImageVersion is a version, image, or PR to install
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
	TerminateJobForUser(user string) (string, error)
	GetLaunchJob(user string) (*Job, error)
	LookupImageOrVersion(imageOrVersion string) (string, error)
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

	Mode string

	InstallImage string
	// if set, the install image will be created via a pull request build
	InstallRefs *prowapiv1.Refs

	UpgradeImage string
	// if set, the upgrade image will be created via a pull request build
	UpgradeRefs *prowapiv1.Refs

	// these fields are only set when this is an upgrade job between two known versions
	InstallVersion string
	UpgradeVersion string

	Credentials     string
	PasswordSnippet string
	Failure         string

	RequestedBy      string
	RequestedChannel string

	RequestedAt   time.Time
	ExpiresAt     time.Time
	StartDuration time.Duration
	Complete      bool
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
	coreClient       clientset.Interface
	imageClient      imageclientset.Interface
	coreConfig       *rest.Config
	prowNamespace    string
	githubURL        string

	muJob struct {
		lock    sync.Mutex
		running map[string]struct{}
	}

	notifierFn JobCallbackFunc
}

// NewJobManager creates a manager that will track the requests made by a user to create clusters
// and reflect that state into ProwJobs that launch clusters. It attempts to recreate state on startup
// by querying prow, but does not guarantee that some notifications to users may not be sent or may be
// sent twice.
func NewJobManager(prowConfigLoader prow.ProwConfigLoader, prowClient dynamic.NamespaceableResourceInterface, coreClient clientset.Interface, imageClient imageclientset.Interface, config *rest.Config, githubURL string) *jobManager {
	m := &jobManager{
		requests:      make(map[string]*JobRequest),
		jobs:          make(map[string]*Job),
		clusterPrefix: "chat-bot-",
		maxClusters:   maxTotalClusters,
		maxAge:        2 * time.Hour,
		githubURL:     githubURL,

		prowConfigLoader: prowConfigLoader,
		prowClient:       prowClient,
		coreClient:       coreClient,
		coreConfig:       config,
		imageClient:      imageClient,
		prowNamespace:    "ci",
	}
	m.muJob.running = make(map[string]struct{})
	return m
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
			InstallVersion:   job.Annotations["ci-chat-bot.openshift.io/releaseVersion"],
			UpgradeVersion:   job.Annotations["ci-chat-bot.openshift.io/upgradeVersion"],
			RequestedBy:      job.Annotations["ci-chat-bot.openshift.io/user"],
			RequestedChannel: job.Annotations["ci-chat-bot.openshift.io/channel"],
			RequestedAt:      job.CreationTimestamp.Time,
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

		if refString, ok := job.Annotations["ci-chat-bot.openshift.io/releaseRefs"]; ok {
			var refs prowapiv1.Refs
			if err := json.Unmarshal([]byte(refString), &refs); err != nil {
				log.Printf("Unable to unmarshal release refs from %s: %v", job.Name, err)
			} else {
				j.InstallRefs = &refs
			}
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
						// if the user provided a release version (resolved, not the original input) use that
						// so that we get a slightly better value to report to the user
						from := job.Annotations["ci-chat-bot.openshift.io/releaseImage"]
						if version := job.Annotations["ci-chat-bot.openshift.io/releaseVersion"]; len(version) > 0 {
							from = version
						}
						if refString, ok := job.Annotations["ci-chat-bot.openshift.io/releaseRefs"]; ok {
							var refs prowapiv1.Refs
							if err := json.Unmarshal([]byte(refString), &refs); err == nil && len(refs.Pulls) > 0 {
								from = fmt.Sprintf("%s/%s#%d", refs.Org, refs.Repo, refs.Pulls[0].Number)
							}
						}
						to := job.Annotations["ci-chat-bot.openshift.io/upgradeImage"]
						if version := job.Annotations["ci-chat-bot.openshift.io/upgradeVersion"]; len(version) > 0 {
							to = version
						}
						m.requests[user] = &JobRequest{
							User:                user,
							Name:                job.Name,
							JobName:             job.Spec.Job,
							InstallImageVersion: from,
							UpgradeImageVersion: to,
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
		if job.Mode == "launch" {
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
			switch {
			case job.InstallRefs != nil && len(job.InstallRefs.Pulls) > 0:
				imageOrVersion = fmt.Sprintf(" <https://github.com/%s/%s/pull/%d|%s/%s#%d>", url.PathEscape(job.InstallRefs.Org), url.PathEscape(job.InstallRefs.Repo), job.InstallRefs.Pulls[0].Number, job.InstallRefs.Org, job.InstallRefs.Repo, job.InstallRefs.Pulls[0].Number)
			case len(job.InstallVersion) > 0:
				imageOrVersion = fmt.Sprintf(" <https://openshift-release.svc.ci.openshift.org/releasetag/%s|%s>", url.PathEscape(job.InstallVersion), job.InstallVersion)
			case len(job.InstallImage) > 0:
				imageOrVersion = " (from image)"
			default:
				imageOrVersion = ""
			}
			switch {
			case job.State == prowapiv1.SuccessState:
				fmt.Fprintf(buf, "• %dm ago <@%s>%s - cluster has been shut down%s\n", int(now.Sub(job.RequestedAt)/time.Minute), job.RequestedBy, imageOrVersion, details)
			case job.State == prowapiv1.FailureState:
				fmt.Fprintf(buf, "• %dm ago <@%s>%s - cluster failed to start%s\n", int(now.Sub(job.RequestedAt)/time.Minute), job.RequestedBy, imageOrVersion, details)
			case len(job.Credentials) > 0:
				fmt.Fprintf(buf, "• %dm ago <@%s>%s - available and will be torn down in %d minutes%s\n", int(now.Sub(job.RequestedAt)/time.Minute), job.RequestedBy, imageOrVersion, int(job.ExpiresAt.Sub(now)/time.Minute), details)
			case len(job.Failure) > 0:
				fmt.Fprintf(buf, "• %dm ago <@%s>%s - failure: %s%s\n", int(now.Sub(job.RequestedAt)/time.Minute), job.RequestedBy, imageOrVersion, job.Failure, details)
			default:
				fmt.Fprintf(buf, "• %dm ago <@%s>%s - starting%s\n", int(now.Sub(job.RequestedAt)/time.Minute), job.RequestedBy, imageOrVersion, details)
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

var reBranchVersion = regexp.MustCompile((`^(openshift-|release-)(\d+\.\d+)$`))

func versionForRefs(refs *prowapiv1.Refs) string {
	if refs == nil || len(refs.BaseRef) == 0 {
		return ""
	}
	if refs.BaseRef == "master" {
		return "4.2.0-0.latest"
	}
	if m := reBranchVersion.FindStringSubmatch(refs.BaseRef); m != nil {
		return fmt.Sprintf("%s.0-0.latest", m[2])
	}
	return ""
}

var reMajorMinorVersion = regexp.MustCompile(`^(\d)\.(\d)$`)

func (m *jobManager) resolveImageOrVersion(imageOrVersion, defaultImageOrVersion string) (string, string, error) {
	if len(strings.TrimSpace(imageOrVersion)) == 0 {
		if len(defaultImageOrVersion) == 0 {
			return "", "", nil
		}
		imageOrVersion = defaultImageOrVersion
	}

	unresolved := imageOrVersion
	if strings.Contains(unresolved, "/") {
		return unresolved, "", nil
	}

	if m := reMajorMinorVersion.FindStringSubmatch(unresolved); m != nil {
		//unresolved = fmt.Sprintf("%s", m[0])
	} else if unresolved == "nightly" {
		unresolved = "4.1.0-0.nightly"
	} else if unresolved == "ci" {
		unresolved = "4.2.0-0.ci"
	} else if unresolved == "prerelease" {
		unresolved = "4.2.0-0.ci"
	}

	is, err := m.imageClient.ImageV1().ImageStreams("ocp").Get("release", metav1.GetOptions{})
	if err != nil {
		return "", "", fmt.Errorf("unable to find release image stream: %v", err)
	}

	if tag := findImageStatusTag(is, unresolved); tag != nil {
		log.Printf("Resolved %s to %s", imageOrVersion, tag.Image)
		return fmt.Sprintf("registry.svc.ci.openshift.org/ocp/release@%s", tag.Image), unresolved, nil
	}

	if tag := findNewestImageSpecTagWithStream(is, unresolved); tag != nil {
		log.Printf("Resolved %s to %s", imageOrVersion, tag.Name)
		return fmt.Sprintf("registry.svc.ci.openshift.org/ocp/release:%s", tag.Name), tag.Name, nil
	}

	return "", "", fmt.Errorf("unable to find a release matching %q on https://openshift-release.svc.ci.openshift.org", imageOrVersion)
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

func findImageStatusTag(is *imagev1.ImageStream, name string) *imagev1.TagEvent {
	for _, tag := range is.Status.Tags {
		if tag.Tag == name {
			if len(tag.Items) == 0 {
				return nil
			}
			return &tag.Items[0]
		}
	}
	return nil
}

func (m *jobManager) LookupImageOrVersion(imageOrVersion string) (string, error) {
	installImage, installVersion, err := m.resolveImageOrVersion(imageOrVersion, "ci")
	if err != nil {
		return "", err
	}
	if len(installVersion) == 0 {
		return fmt.Sprintf("Will launch directly from the image `%s`", installImage), nil
	}
	return fmt.Sprintf("`%s` launches version <https://openshift-release.svc.ci.openshift.org/releasetag/%s|%s>", imageOrVersion, installVersion, installVersion), nil
}

type GitHubPullRequest struct {
	ID        int    `json:"id"`
	Number    int    `json:"number"`
	UpdatedAt string `json:"updated_at"`
	State     string `json:"state"`

	User GitHubPullRequestUser `json:"user"`

	Merged    bool `json:"merged"`
	Mergeable bool `json:"mergeable"`

	Head GitHubPullRequestHead `json:"head"`
	Base GitHubPullRequestBase `json:"base"`
}

type GitHubPullRequestUser struct {
	Login string `json:"login"`
}

type GitHubPullRequestHead struct {
	SHA string `json:"sha"`
}

type GitHubPullRequestBase struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

func (m *jobManager) resolveAsPullRequest(spec string) (*prowapiv1.Refs, error) {
	parts := strings.SplitN(spec, "#", 2)
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

	req, err := http.NewRequest("GET", fmt.Sprintf("%s/repos/%s/%s/pulls/%d", m.githubURL, url.PathEscape(locationParts[0]), url.PathEscape(locationParts[1]), num), nil)
	if err != nil {
		return nil, fmt.Errorf("unable to lookup pull request: %v", err)
	}
	if token := os.Getenv("GITHUB_TOKEN"); len(token) > 0 {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("unable to lookup pull request: %v", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 200:
		// retrieve
	case 403:
		data, _ := ioutil.ReadAll(resp.Body)
		log.Printf("failed to access server:\n%s", string(data))
		return nil, fmt.Errorf("unable to lookup pull request: forbidden")
	case 404:
		return nil, fmt.Errorf("pull request not found")
	default:
		return nil, fmt.Errorf("unable to lookup pull request: %d %s", resp.StatusCode, resp.Status)
	}

	var pr GitHubPullRequest
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("unable to retrieve pull request info: %v", err)
	}
	if pr.Merged {
		return nil, fmt.Errorf("pull request has already been merged to %s", pr.Base.Ref)
	}
	if !pr.Mergeable {
		return nil, fmt.Errorf("pull request needs to be rebased to branch %s", pr.Base.Ref)
	}

	return &prowapiv1.Refs{
		Org:  locationParts[0],
		Repo: locationParts[1],

		BaseRef: pr.Base.Ref,
		BaseSHA: pr.Base.SHA,

		Pulls: []prowapiv1.Pull{
			{
				Number: num,
				SHA:    pr.Head.SHA,
				Author: pr.User.Login,
			},
		},
	}, nil
}

func (m *jobManager) resolveToJob(req *JobRequest) (*Job, error) {
	user := req.User
	if len(user) == 0 {
		return nil, fmt.Errorf("must specify the name of the user who requested this cluster")
	}

	req.RequestedAt = time.Now()
	name := fmt.Sprintf("%s%s", m.clusterPrefix, req.RequestedAt.UTC().Format("2006-01-02-150405.9999"))
	req.Name = name

	job := &Job{
		Mode:  "launch",
		Name:  name,
		State: prowapiv1.PendingState,

		RequestedBy:      user,
		RequestedChannel: req.Channel,
		RequestedAt:      req.RequestedAt,

		ExpiresAt: req.RequestedAt.Add(m.maxAge),
	}

	// if the user provided a pull spec (org/repo#number) we'll build from that
	installRefs, err := m.resolveAsPullRequest(req.InstallImageVersion)
	if err != nil {
		return nil, err
	}
	if installRefs != nil {
		job.InstallRefs = installRefs
		job.InstallVersion = versionForRefs(installRefs)
	} else {
		// otherwise, resolve as a semantic version (as a tag on the release image stream) or as an image
		job.InstallImage, job.InstallVersion, err = m.resolveImageOrVersion(req.InstallImageVersion, "ci")
		if err != nil {
			return nil, err
		}
	}

	// upgrade image is optional - resolve it similarly, and if set make this an upgrade job
	upgradeRefs, err := m.resolveAsPullRequest(req.UpgradeImageVersion)
	if err != nil {
		return nil, err
	}
	if upgradeRefs != nil {
		job.UpgradeRefs = upgradeRefs
		job.UpgradeVersion = versionForRefs(upgradeRefs)
	} else {
		job.UpgradeImage, job.UpgradeVersion, err = m.resolveImageOrVersion(req.UpgradeImageVersion, "")
		if err != nil {
			return nil, err
		}
	}
	if job.UpgradeRefs != nil || len(job.UpgradeImage) > 0 {
		job.Mode = "upgrade"
	}

	// TODO: this should be possible but requires some thought, disable for now
	// (mainly we need two namespaces and jobs to build in)
	if job.UpgradeRefs != nil || (job.InstallRefs != nil && job.Mode == "upgrade") {
		return nil, fmt.Errorf("upgrading to a PR build is not supported at this time")
	}

	return job, nil
}

func (m *jobManager) LaunchJobForUser(req *JobRequest) (string, error) {
	job, err := m.resolveToJob(req)
	if err != nil {
		return "", err
	}

	// try to pick a job that matches the install version, if we can, otherwise use the first that
	// matches us (we can do better)
	var prowJob *prowapiv1.ProwJob
	selector := labels.Set{"job-env": "aws", "job-type": job.Mode}
	if len(job.InstallVersion) > 0 {
		if v, err := semver.ParseTolerant(job.InstallVersion); err == nil {
			withRelease := labels.Merge(selector, labels.Set{"job-release": fmt.Sprintf("%d.%d", v.Major, v.Minor)})
			prowJob, _ = prow.JobForLabels(m.prowConfigLoader, labels.SelectorFromSet(withRelease))
		}
	}
	if prowJob == nil {
		prowJob, _ = prow.JobForLabels(m.prowConfigLoader, labels.SelectorFromSet(selector))
	}
	if prowJob == nil {
		return "", fmt.Errorf("configuration error, unable to find prow job matching %s", selector)
	}
	job.JobName = prowJob.Spec.Job
	log.Printf("Selected %s job %s for user - %s->%s %s->%s", job.Mode, job.JobName, job.InstallVersion, job.UpgradeVersion, job.InstallImage, job.UpgradeImage)

	m.lock.Lock()
	defer m.lock.Unlock()

	user := req.User
	if job.Mode == "launch" {
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
			if job != nil && job.Mode == "launch" && !job.Complete {
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
		if running > maxJobsPerUser {
			return "", fmt.Errorf("you can't have more than %d running jobs at a time", maxJobsPerUser)
		}
	}

	m.jobs[job.Name] = job

	log.Printf("user %q requests cluster %q", user, job.Name)
	go m.handleJobStartup(*job)

	if job.Mode == "launch" {
		return "", fmt.Errorf("a cluster is being created - I'll send you the credentials in about ~%d minutes", m.estimateCompletion(req.RequestedAt)/time.Minute)
	}
	return "", fmt.Errorf("job has been started")
}

func (m *jobManager) clusterNameForUser(user string) (string, error) {
	if len(user) == 0 {
		return "", fmt.Errorf("must specify the name of the user who requested this cluster")
	}

	m.lock.Lock()
	defer m.lock.Unlock()

	existing, ok := m.requests[user]
	if !ok || len(existing.Name) == 0 {
		return "", fmt.Errorf("no cluster has been requested by you")
	}
	return existing.Name, nil
}

func (m *jobManager) TerminateJobForUser(user string) (string, error) {
	name, err := m.clusterNameForUser(user)
	if err != nil {
		return "", err
	}
	log.Printf("user %q requests job %q to be terminated", user, name)
	if err := m.stopJob(name); err != nil {
		return "", fmt.Errorf("unable to terminate running cluster: %v", err)
	}

	// mark the cluster as failed, clear the request, and allow the user to launch again
	m.lock.Lock()
	defer m.lock.Unlock()
	existing, ok := m.requests[user]
	if !ok || existing.Name != name {
		return "", fmt.Errorf("another cluster was launched while trying to stop this cluster")
	}
	delete(m.requests, user)
	if job, ok := m.jobs[name]; ok {
		job.Failure = "deletion requested"
		job.ExpiresAt = time.Now().Add(15 * time.Minute)
		job.Complete = true
	}
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
	log.Printf("user %q requests job %q to be refreshed", user, copied.Name)
	go m.handleJobStartup(copied)

	return msg, nil
}

func (m *jobManager) handleJobStartup(job Job) {
	if !m.tryJob(job.Name) {
		log.Printf("another worker is already running for %s", job.Name)
		return
	}
	defer m.finishJob(job.Name)

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

	// track the 10 most recent starts in sorted order
	if job.Mode == "launch" && len(job.Credentials) > 0 && job.StartDuration > 0 {
		m.recentStartEstimates = append(m.recentStartEstimates, job.StartDuration)
		if len(m.recentStartEstimates) > 10 {
			m.recentStartEstimates = m.recentStartEstimates[:10]
		}
		sort.Slice(m.recentStartEstimates, func(i, j int) bool {
			return m.recentStartEstimates[i] < m.recentStartEstimates[j]
		})
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
