package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
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

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"

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
	projectclientset "github.com/openshift/client-go/project/clientset/versioned"
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
	// An optional string controlling the platform type
	Platform string

	Channel     string
	RequestedAt time.Time
	Name        string

	JobName   string
	JobParams map[string]string
}

// JobManager responds to user actions and tracks the state of the launched
// clusters.
type JobManager interface {
	SetNotifiers(state JobCallbackFunc, soonDone JobCallbackFunc)

	LaunchJobForUser(req *JobRequest) (string, error)
	SyncJobForUser(user string) (string, error)
	TerminateJobForUser(user string) (string, error)
	KeepJobForUser(user string) (string, error)
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

	Platform  string
	JobParams map[string]string

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
	projectClient    projectclientset.Interface
	coreConfig       *rest.Config
	prowNamespace    string
	githubURL        string
	forcePROwner     string

	muJob struct {
		lock    sync.Mutex
		running map[string]struct{}
	}

	stateNotifierFn, soonDoneNotifierFn JobCallbackFunc
}

// NewJobManager creates a manager that will track the requests made by a user to create clusters
// and reflect that state into ProwJobs that launch clusters. It attempts to recreate state on startup
// by querying prow, but does not guarantee that some notifications to users may not be sent or may be
// sent twice.
func NewJobManager(prowConfigLoader prow.ProwConfigLoader, prowClient dynamic.NamespaceableResourceInterface, coreClient clientset.Interface, imageClient imageclientset.Interface, projectClient projectclientset.Interface, config *rest.Config, githubURL, forcePROwner string) *jobManager {
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
		projectClient:    projectClient,
		prowNamespace:    "ci",
		forcePROwner:     forcePROwner,
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
			Platform:         job.Annotations["ci-chat-bot.openshift.io/platform"],
			InstallImage:     job.Annotations["ci-chat-bot.openshift.io/releaseImage"],
			UpgradeImage:     job.Annotations["ci-chat-bot.openshift.io/upgradeImage"],
			InstallVersion:   job.Annotations["ci-chat-bot.openshift.io/releaseVersion"],
			UpgradeVersion:   job.Annotations["ci-chat-bot.openshift.io/upgradeVersion"],
			RequestedBy:      job.Annotations["ci-chat-bot.openshift.io/user"],
			RequestedChannel: job.Annotations["ci-chat-bot.openshift.io/channel"],
			RequestedAt:      job.CreationTimestamp.Time,
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

		if refString, ok := job.Annotations["ci-chat-bot.openshift.io/releaseRefs"]; ok {
			var refs prowapiv1.Refs
			if err := json.Unmarshal([]byte(refString), &refs); err != nil {
				klog.Infof("Unable to unmarshal release refs from %s: %v", job.Name, err)
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
						params, err := paramsFromAnnotation(job.Annotations["ci-chat-bot.openshift.io/jobParams"])
						if err != nil {
							klog.Infof("Unable to unmarshal parameters from %s: %v", job.Name, err)
							continue
						}

						m.requests[user] = &JobRequest{
							User:                user,
							Name:                job.Name,
							JobName:             job.Spec.Job,
							Platform:            job.Annotations["ci-chat-bot.openshift.io/platform"],
							JobParams:           params,
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

			if notifiedString := job.Annotations["ci-chat-bot.openshift.io/notified"]; len(notifiedString) == 0 && j.Mode == "" && j.Failure == "" && time.Now().Add(time.Minute*15).After(j.ExpiresAt) {
				patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"ci-chat-bot.openshift.io/notified":"%d"}}}`, int(time.Now().Sub(j.RequestedAt).Seconds())))
				if _, err := m.prowClient.Namespace(m.prowNamespace).Patch(job.Name, types.MergePatchType, patch, metav1.UpdateOptions{}); err != nil {
					klog.Infof("error: unable to patch notified annotation for prow job: %v", err)
				} else {
					m.soonDoneNotifierFn(*j)
				}
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

func (m *jobManager) SetNotifiers(stateFn, soonDoneFn JobCallbackFunc) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.stateNotifierFn = stateFn
	m.soonDoneNotifierFn = soonDoneFn
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
			case len(job.Credentials) > 0:
				fmt.Fprintf(buf, "• <@%s>%s%s - available and will be torn down in %d minutes%s\n", job.RequestedBy, imageOrVersion, options, int(job.ExpiresAt.Sub(now)/time.Minute), details)
			case len(job.Failure) > 0:
				fmt.Fprintf(buf, "• <@%s>%s%s - failure: %s%s\n", job.RequestedBy, imageOrVersion, options, job.Failure, details)
			default:
				fmt.Fprintf(buf, "• <@%s>%s%s - starting, %d minutes elapsed %s\n", job.RequestedBy, imageOrVersion, options, int(now.Sub(job.RequestedAt)/time.Minute), details)
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
		return "4.3.0-0.latest"
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

	// workaround Slack's annoying habit of turning 4.3.0-0.ci into a hyperlink in slackdown
	if strings.HasPrefix(imageOrVersion, "<") && strings.HasSuffix(imageOrVersion, ">") && strings.Contains(imageOrVersion, "|") {
		imageOrVersion = imageOrVersion[strings.Index(imageOrVersion, "|")+1 : len(imageOrVersion)-1]
	}

	unresolved := imageOrVersion
	if strings.Contains(unresolved, "/") {
		return unresolved, "", nil
	}

	is, err := m.imageClient.ImageV1().ImageStreams("ocp").Get("release", metav1.GetOptions{})
	if err != nil {
		return "", "", fmt.Errorf("unable to find release image stream: %v", err)
	}

	if m := reMajorMinorVersion.FindStringSubmatch(unresolved); m != nil {
		if tag := findNewestStableImageSpecTagBySemanticMajor(is, unresolved); tag != nil {
			klog.Infof("Resolved major.minor %s to semver tag %s", imageOrVersion, tag.Name)
			return fmt.Sprintf("registry.svc.ci.openshift.org/ocp/release:%s", tag.Name), tag.Name, nil
		}
		if tag := findNewestImageSpecTagWithStream(is, fmt.Sprintf("%s.0-0.nightly", unresolved)); tag != nil {
			klog.Infof("Resolved major.minor %s to nightly tag %s", imageOrVersion, tag.Name)
			return fmt.Sprintf("registry.svc.ci.openshift.org/ocp/release:%s", tag.Name), tag.Name, nil
		}
		if tag := findNewestImageSpecTagWithStream(is, fmt.Sprintf("%s.0-0.ci", unresolved)); tag != nil {
			klog.Infof("Resolved major.minor %s to ci tag %s", imageOrVersion, tag.Name)
			return fmt.Sprintf("registry.svc.ci.openshift.org/ocp/release:%s", tag.Name), tag.Name, nil
		}
		return "", "", fmt.Errorf("no stable, official prerelease, or nightly version published yet for %s", imageOrVersion)
	} else if unresolved == "nightly" {
		unresolved = "4.2.0-0.nightly"
	} else if unresolved == "ci" {
		unresolved = "4.3.0-0.ci"
	} else if unresolved == "prerelease" {
		unresolved = "4.3.0-0.ci"
	}

	if tag, name := findImageStatusTag(is, unresolved); tag != nil {
		klog.Infof("Resolved %s to image %s", imageOrVersion, tag.Image)
		return fmt.Sprintf("registry.svc.ci.openshift.org/ocp/release@%s", tag.Image), name, nil
	}

	if tag := findNewestImageSpecTagWithStream(is, unresolved); tag != nil {
		klog.Infof("Resolved %s to tag %s", imageOrVersion, tag.Name)
		return fmt.Sprintf("registry.svc.ci.openshift.org/ocp/release:%s", tag.Name), tag.Name, nil
	}

	return "", "", fmt.Errorf("unable to find a release matching %q on https://openshift-release.svc.ci.openshift.org", imageOrVersion)
}

func findNewestStableImageSpecTagBySemanticMajor(is *imagev1.ImageStream, majorMinor string) *imagev1.TagReference {
	base, err := semver.ParseTolerant(majorMinor)
	if err != nil {
		return nil
	}
	var candidates semver.Versions
	for _, tag := range is.Spec.Tags {
		if tag.Annotations["release.openshift.io/name"] != "4-stable" {
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
		klog.Errorf("Failed to access server:\n%s", string(data))
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

	owner := m.forcePROwner
	if len(owner) == 0 {
		owner = pr.User.Login
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

	req.RequestedAt = time.Now()
	name := fmt.Sprintf("%s%s", m.clusterPrefix, req.RequestedAt.UTC().Format("2006-01-02-150405.9999"))
	req.Name = name

	job := &Job{
		Mode:  "launch",
		Name:  name,
		State: prowapiv1.PendingState,

		Platform:  req.Platform,
		JobParams: req.JobParams,

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
	if len(req.Platform) == 0 {
		return "", fmt.Errorf("platform must be set when launching clusters")
	}
	job, err := m.resolveToJob(req)
	if err != nil {
		return "", err
	}

	// try to pick a job that matches the install version, if we can, otherwise use the first that
	// matches us (we can do better)
	var prowJob *prowapiv1.ProwJob
	selector := labels.Set{"job-env": req.Platform, "job-type": job.Mode}
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
	klog.Infof("Selected %s job %s for user - %s->%s %s->%s, params=%s", job.Mode, job.JobName, job.InstallVersion, job.UpgradeVersion, job.InstallImage, job.UpgradeImage, paramsToString(job.JobParams))

	m.lock.Lock()
	defer m.lock.Unlock()

	user := req.User
	if job.Mode == "launch" {
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
			if job != nil && job.Mode == "launch" && !job.Complete {
				launchedClusters++
			}
		}
		if launchedClusters >= m.maxClusters {
			klog.Infof("user %q is will have to wait", user)
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

	klog.Infof("user %q requests cluster %q", user, job.Name)
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
	klog.Infof("user %q requests job %q to be terminated", user, name)
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

func (m *jobManager) KeepJobForUser(user string) (string, error) {
	name, err := m.clusterNameForUser(user)
	if err != nil {
		return "", err
	}

	m.lock.Lock()
	defer m.lock.Unlock()
	job, ok := m.jobs[name]
	if !ok {
		return "", fmt.Errorf("unable to keep cluster: not found")
	}

	klog.Infof("user %q requests job %q to be kept", user, name)
	if err := m.keepJob(job); err != nil {
		return "", fmt.Errorf("unable to keep running cluster: %v", err)
	}

	return fmt.Sprintf("the cluster is kept for another hour until %s", job.ExpiresAt), nil
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
	go m.handleJobStartup(copied)

	return msg, nil
}

func (m *jobManager) handleJobStartup(job Job) {
	if !m.tryJob(job.Name) {
		klog.Infof("another worker is already running for %s", job.Name)
		return
	}
	defer m.finishJob(job.Name)

	if err := m.launchJob(&job); err != nil {
		klog.Errorf("Failed to launch job: %v", err)
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

	klog.Infof("completed job request for %s and notifying participants (%s)", job.Name, job.RequestedBy)

	m.jobs[job.Name] = &job
	for _, request := range m.requests {
		if request.Name == job.Name {
			klog.Infof("notify %q that job %q is complete", request.User, request.Name)
			if m.stateNotifierFn != nil {
				go m.stateNotifierFn(job)
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
