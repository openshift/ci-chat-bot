package manager

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openshift/ci-chat-bot/pkg/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	prowapiv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowjobLister "sigs.k8s.io/prow/pkg/client/listers/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/github"
)

func TestDefaultMceUserConfigDuration(t *testing.T) {
	config := defaultMceUserConfig()

	if config.MaxClusters != 1 {
		t.Errorf("MaxClusters = %d, want 1", config.MaxClusters)
	}
	if config.MaxClusterAge != int(MaxMCEDuration/time.Hour) {
		t.Errorf("MaxClusterAge = %d, want %d hours", config.MaxClusterAge, int(MaxMCEDuration/time.Hour))
	}
	if got := defaultMceDuration(config.MaxClusterAge); got != MaxMCEDuration {
		t.Errorf("defaultMceDuration() = %s, want %s", got, MaxMCEDuration)
	}
}

type lookupGitHubClient struct{ github.Client }

func (*lookupGitHubClient) GetPullRequest(_, _ string, number int) (*github.PullRequest, error) {
	return &github.PullRequest{
		User: github.User{Login: "test-user"},
		Base: github.PullRequestBranch{Ref: "main"},
		Head: github.PullRequestBranch{SHA: fmt.Sprintf("pull-%d", number)},
	}, nil
}

func (*lookupGitHubClient) GetRef(_, _, ref string) (string, error) {
	return "sha-" + ref, nil
}

func Test_platformProfileSets(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		want     string
	}{
		{name: "aws maps to its profile set", platform: "aws", want: "openshift-org-aws"},
		{name: "azure maps to its profile set", platform: "azure", want: "openshift-org-azure"},
		{name: "gcp maps to its profile set", platform: "gcp", want: "openshift-org-gcp"},
		{name: "unknown platform has no profile set", platform: "metal", want: ""},
		{name: "empty platform has no profile set", platform: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := platformProfileSets[tt.platform]; got != tt.want {
				t.Errorf("platformProfileSets[%q] = %q, want %q", tt.platform, got, tt.want)
			}
		})
	}
}

func Test_containsValidVersion(t *testing.T) {
	type args struct {
		listOfImageOrVersionOrPRs []string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "Empty arguments: 'launch'",
			args: args{
				listOfImageOrVersionOrPRs: []string{""},
			},
			want: false,
		},
		{
			name: "Valid version by itself: 'launch 4.19'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"4.19"},
			},
			want: true,
		},
		{
			name: "Valid nightly version by itself: 'launch 4.19-nightly'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"4.19"},
			},
			want: true,
		},
		{
			name: "Valid dot nightly version by itself: 'launch 4.19.nightly'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"4.19"},
			},
			want: true,
		},
		{
			name: "Valid nightly version with 6 digit tail by itself: 'launch 4.20.0-0.nightly-2025-04-02-081925'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"4.20.0-0.nightly-2025-04-02-081925"},
			},
			want: true,
		},
		{
			name: "Valid latest nightly version by itself : 'launch 4.20.0-0.nightly'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"4.20.0-0.nightly"},
			},
			want: true,
		},
		{
			name: "Valid nightly version with 6 digit tail by itself: 'launch 4.20.0-0.nightly-2025-04-02-081925'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"4.20.0-0.nightly-2025-04-02-081925"},
			},
			want: true,
		},

		{
			name: "Valid latest ci version by itself : 'launch 4.20.0-0.ci'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"4.20.0-0.ci"},
			},
			want: true,
		},
		{
			name: "Valid ci version with 6 digit tail by itself: 'launch 4.20.ci-2025-04-02-081925'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"4.20.ci-2025-04-02-081925"},
			},
			want: true,
		},
		{
			name: "Valid ci version by itself: 'launch 4.20.ci'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"4.20.ci"},
			},
			want: true,
		},
		{
			name: "Valid nightly registry version by itself: 'launch registry.ci.openshift.org/ocp/release:4.20.0-0.nightly-2025-04-02-081925'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"registry.ci.openshift.org/ocp/release:4.20.0-0.nightly-2025-04-02-081925"},
			},
			want: true,
		},
		{
			name: "Valid ci registry version by itself: 'launch registry.ci.openshift.org/ocp/release:4.20.0-0.ci-2025-04-02-081925'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"registry.ci.openshift.org/ocp/release:4.20.0-0.ci-2025-04-02-081925"},
			},
			want: true,
		},
		{
			name: "Valid registry.ci PullSpec with tag: 'launch registry.ci.openshift.org/rhcos-devel/v4.20.0:4.20.0-ec.5'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"registry.ci.openshift.org/rhcos-devel/v4.20.0:4.20.0-ec.5"},
			},
			want: true,
		},
		{
			name: "Valid registry.ci PullSpec with SHA: 'launch registry.ci.openshift.org/rhcos-devel/v4.20.0@sha256:3eb762ec9a082184f5b10adf850e897ab51556403a1f6aed70063aa0b7ad507d'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"registry.ci.openshift.org/rhcos-devel/v4.20.0@sha256:3eb762ec9a082184f5b10adf850e897ab51556403a1f6aed70063aa0b7ad507d"},
			},
			want: true,
		},
		{
			name: "Valid quay.io PullSpec with SHA: 'launch quay.io/myname/v4.20.0@sha256:3eb762ec9a082184f5b10adf850e897ab51556403a1f6aed70063aa0b7ad507d'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"quay.io/myname/v4.20.0@sha256:3eb762ec9a082184f5b10adf850e897ab51556403a1f6aed70063aa0b7ad507d"},
			},
			want: true,
		},
		{
			name: "Valid registry.mydomain.openshift.org PullSpec with SHA: 'launch registry.mydomain.openshift.org/some-ns/v4.20.0@sha256:3eb762ec9a082184f5b10adf850e897ab51556403a1f6aed70063aa0b7ad507d'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"registry.mydomain.openshift.org/some-ns/v4.20.0@sha256:3eb762ec9a082184f5b10adf850e897ab51556403a1f6aed70063aa0b7ad507d"},
			},
			want: true,
		},
		{
			name: "Invalid registry.ci PullSpec (missing tag or SHA): 'launch registry.ci.openshift.org/rhcos-devel/v4.20.0'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"registry.ci.openshift.org/rhcos-devel/v4.20.0"},
			},
			want: false,
		},
		{
			name: "Invalid registry.ci PullSpec (missing v prefix and tag): 'launch registry.ci.openshift.org/rhcos-devel/4.20.0'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"registry.ci.openshift.org/rhcos-devel/4.20.0"},
			},
			want: false,
		},
		{
			name: "Invalid registry.ci PullSpec (missing tag but has v prefix): 'launch registry.ci.openshift.org/rhcos-devel/v4.20.0'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"registry.ci.openshift.org/rhcos-devel/v4.20.0"},
			},
			want: false,
		},
		{
			name: "Using a pull request without version specified: 'launch openshift/installer#7160'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"openshift/installer#7160"},
			},
			want: false,
		},
		{
			name: "Using a pull request with a version specified: 'launch 4.19,openshift/installer#7160'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"4.19", "openshift/installer#7160"},
			},
			want: true,
		},
		{
			name: "Using a pull request with a version specified as the second parameter: 'launch openshift/installer#7160,4.19'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"openshift/installer#7160", "4.19"},
			},
			want: true,
		},
		{
			name: "Using two pull requests: 'launch openshift/installer#7160,openshift/machine-config-operator#3688'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"openshift/installer#7160", "openshift/machine-config-operator#3688"},
			},
			want: false,
		},
		{
			name: "Using two pull requests with version: 'launch 4.19,openshift/installer#7160,openshift/machine-config-operator#3688'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"4.19", "openshift/installer#7160", "openshift/machine-config-operator#3688"},
			},
			want: true,
		},
		{
			name: "Quay with ec tag: 'launch quay.io/openshift-release-dev/ocp-release:4.19.0-ec.4-x86_64'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"quay.io/openshift-release-dev/ocp-release:4.19.0-ec.4-x86_64"},
			},
			want: true,
		},
		{
			name: "Quay nightly dev release: 'launch quay.io/openshift-release-dev/dev-release:4.20.0-0.nightly-2025-04-17-181203'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"quay.io/openshift-release-dev/dev-release:4.20.0-0.nightly-2025-04-17-181203"},
			},
			want: true,
		},
		{
			name: "Quay nightly-priv release: 'launch quay.io/openshift-release-dev/dev-release-priv:4.20.0-0.nightly-priv-2025-04-15-035225'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"quay.io/openshift-release-dev/dev-release-priv:4.20.0-0.nightly-priv-2025-04-15-035225"},
			},
			want: true,
		},
		{
			name: "OKD SCOS EC tag: 'launch quay.io/okd/scos-release:4.19.0-okd-scos.ec.8'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"quay.io/okd/scos-release:4.19.0-okd-scos.ec.8"},
			},
			want: true,
		},
		{
			name: "OKD release: 'launch quay.io/openshift/okd:4.15.0-0.okd-2024-03-10-010116'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"quay.io/openshift/okd:4.15.0-0.okd-2024-03-10-010116"},
			},
			want: true,
		},
		{
			name: "OKD registry release: 'launch registry.ci.openshift.org/origin/release-scos:4.19.0-0.okd-scos-2025-04-17-091854'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"registry.ci.openshift.org/origin/release-scos:4.19.0-0.okd-scos-2025-04-17-091854"},
			},
			want: true,
		},
		{
			name: "nightly-priv release: 'launch registry.ci.openshift.org/ocp-priv/release-priv:4.20.0-0.nightly-priv-2025-04-15-170718'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"registry.ci.openshift.org/ocp-priv/release-priv:4.20.0-0.nightly-priv-2025-04-15-170718"},
			},
			want: true,
		},
		{
			name: "Konflux nightly release: 'launch registry.ci.openshift.org/ocp/konflux-release:4.20.0-0.konflux-nightly-2025-04-17-181101'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"registry.ci.openshift.org/ocp/konflux-release:4.20.0-0.konflux-nightly-2025-04-17-181101"},
			},
			want: true,
		},
		{
			name: "Konflux nightly release image as second parameter: 'launch openshift/installer#7160,registry.ci.openshift.org/ocp/konflux-release:4.20.0-0.konflux-nightly-2025-04-17-181101'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"openshift/installer#7160", "registry.ci.openshift.org/ocp/konflux-release:4.20.0-0.konflux-nightly-2025-04-17-181101"},
			},
			want: true,
		},
		{
			name: "Invalid container registry docker.io: 'launch docker.io/ocp/release:4.19.0-0.ci-2025-04-28-053740'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"docker.io/ocp/release:4.19.0-0.ci-2025-04-28-053740"},
			},
			want: false,
		},
		{
			name: "Release built with the clusterbot 'build' command: 'launch registry.build06.ci.openshift.org/ci-ln-s6v83tt/release:latest'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"registry.build06.ci.openshift.org/ci-ln-s6v83tt/release:latest"},
			},
			want: true,
		},
		{
			name: "Quay-proxy ci-quay release: 'launch quay-proxy.ci.openshift.org/openshift/ci:rc_payload__5.0.0-0.ci-quay-2026-05-04-204309'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"quay-proxy.ci.openshift.org/openshift/ci:rc_payload__5.0.0-0.ci-quay-2026-05-04-204309"},
			},
			want: true,
		},
		{
			name: "Quay-proxy ci-quay release with PR: 'launch openshift/installer#7160,quay-proxy.ci.openshift.org/openshift/ci:rc_payload__5.0.0-0.ci-quay-2026-05-04-204309'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"openshift/installer#7160", "quay-proxy.ci.openshift.org/openshift/ci:rc_payload__5.0.0-0.ci-quay-2026-05-04-204309"},
			},
			want: true,
		},
		{
			name: "Quay-proxy ci release without -quay: 'launch quay-proxy.ci.openshift.org/openshift/ci:rc_payload__5.0.0-0.ci-2026-05-04-204309'",
			args: args{
				listOfImageOrVersionOrPRs: []string{"quay-proxy.ci.openshift.org/openshift/ci:rc_payload__5.0.0-0.ci-2026-05-04-204309"},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsValidVersion(tt.args.listOfImageOrVersionOrPRs); got != tt.want {
				t.Errorf("containsValidVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveToJobRejectsBuildWithBundle(t *testing.T) {
	t.Parallel()

	m := &jobManager{clusterPrefix: "bot-"}
	tests := []struct {
		name            string
		bundle          string
		wantBundleInErr bool
	}{
		{
			name:            "non-empty bundle",
			bundle:          "my-operator-bundle",
			wantBundleInErr: true,
		},
		{
			name:            "empty bundle",
			bundle:          "",
			wantBundleInErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			job, err := m.resolveToJob(&JobRequest{
				User:      "user",
				Type:      JobTypeBuild,
				JobParams: map[string]string{"bundle": tt.bundle},
				Inputs:    [][]string{{"openshift/installer#1"}},
			})
			if job != nil {
				t.Fatalf("expected nil job, got: %#v", job)
			}
			if err == nil {
				t.Fatal("expected error for build job with bundle parameter")
			}
			if !strings.Contains(err.Error(), "catalog build") {
				t.Fatalf("expected catalog build guidance, got: %v", err)
			}
			if tt.wantBundleInErr && !strings.Contains(err.Error(), tt.bundle) {
				t.Fatalf("expected bundle name in error, got: %v", err)
			}
		})
	}
}

func TestCheckAroHcpLimits(t *testing.T) {
	t.Parallel()

	jobs := make(map[string]*Job)
	for i := range maxTotalAroHcpClusters {
		user := fmt.Sprintf("user-%d", i)
		if err := checkAroHcpLimits(jobs, user); err != nil {
			t.Fatalf("checkAroHcpLimits() rejected active environment %d: %v", i+1, err)
		}
		jobs[fmt.Sprintf("aro-%d", i)] = &Job{
			Mode:        JobTypeAroHcp,
			RequestedBy: user,
		}
	}

	if err := checkAroHcpLimits(jobs, "new-user"); err == nil {
		t.Fatal("checkAroHcpLimits() allowed a sixth active ARO-HCP environment")
	}
	if err := checkAroHcpLimits(jobs, "user-0"); err == nil {
		t.Fatal("checkAroHcpLimits() allowed a second active ARO-HCP environment for a user")
	}
}

func TestCheckAroHcpLimitsIgnoresInactiveAndOtherJobs(t *testing.T) {
	t.Parallel()

	jobs := map[string]*Job{
		"active-1": {
			Mode:        JobTypeAroHcp,
			RequestedBy: "user-1",
		},
		"active-2": {
			Mode:        JobTypeAroHcp,
			RequestedBy: "user-2",
		},
		"active-3": {
			Mode:        JobTypeAroHcp,
			RequestedBy: "user-3",
		},
		"active-4": {
			Mode:        JobTypeAroHcp,
			RequestedBy: "user-4",
		},
		"complete": {
			Mode:        JobTypeAroHcp,
			Complete:    true,
			RequestedBy: "user-5",
		},
		"failed": {
			Mode:        JobTypeAroHcp,
			Failure:     "job failed",
			RequestedBy: "user-6",
		},
		"other-mode": {
			Mode:        JobTypeLaunch,
			RequestedBy: "user-7",
		},
		"nil": nil,
	}

	if err := checkAroHcpLimits(jobs, "new-user"); err != nil {
		t.Fatalf("checkAroHcpLimits() counted inactive or non-ARO jobs: %v", err)
	}
}

func TestValidRequesterEmail(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		email string
		want  bool
	}{
		{name: "valid", email: "user@example.com", want: true},
		{name: "missing", email: "", want: false},
		{name: "missing domain", email: "user@", want: false},
		{name: "display name is not an email identity", email: "User <user@example.com>", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validRequesterEmail(test.email); got != test.want {
				t.Fatalf("validRequesterEmail(%q) = %t, want %t", test.email, got, test.want)
			}
		})
	}
}

func TestLookupInputsDoesNotTreatPullRequestsAsImages(t *testing.T) {
	t.Parallel()

	image := "registry.ci.openshift.org/ocp/release:4.23"
	tests := []struct {
		name            string
		parts           []string
		allowBranchRefs bool
		wantImage       string
		wantRefs        int
	}{
		{name: "single pull request", parts: []string{"Azure/ARO-HCP#123"}, wantRefs: 1},
		{name: "image before pull request", parts: []string{image, "Azure/ARO-HCP#123"}, wantImage: image, wantRefs: 1},
		{name: "image after pull request", parts: []string{"Azure/ARO-HCP#123", image}, wantImage: image, wantRefs: 1},
		{name: "two pull requests", parts: []string{"Azure/ARO-HCP#123", "openshift/hypershift#456"}, allowBranchRefs: true, wantRefs: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := &jobManager{githubClient: &lookupGitHubClient{}}
			inputs, _, err := m.lookupInputs([][]string{test.parts}, "amd64", test.allowBranchRefs)
			if err != nil {
				t.Fatalf("lookupInputs() returned error: %v", err)
			}
			if len(inputs) != 1 || inputs[0].Image != test.wantImage || len(inputs[0].Refs) != test.wantRefs {
				t.Fatalf("lookupInputs() = %#v, want image %q and %d refs", inputs, test.wantImage, test.wantRefs)
			}
		})
	}
}

func TestLookupInputsChecksBranchAndPullBaseInEitherOrder(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		parts   []string
		wantErr bool
	}{
		{name: "branch then matching pull", parts: []string{"Azure/ARO-HCP@main", "Azure/ARO-HCP#123"}},
		{name: "pull then matching branch", parts: []string{"Azure/ARO-HCP#123", "Azure/ARO-HCP@main"}},
		{name: "branch then conflicting pull", parts: []string{"Azure/ARO-HCP@feature", "Azure/ARO-HCP#123"}, wantErr: true},
		{name: "pull then conflicting branch", parts: []string{"Azure/ARO-HCP#123", "Azure/ARO-HCP@feature"}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := &jobManager{githubClient: &lookupGitHubClient{}}
			inputs, _, err := m.lookupInputs([][]string{test.parts}, "amd64", true)
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "conflicts") {
					t.Fatalf("lookupInputs() error = %v, want branch conflict", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("lookupInputs() returned error: %v", err)
			}
			if len(inputs) != 1 || len(inputs[0].Refs) != 1 || len(inputs[0].Refs[0].Pulls) != 1 || inputs[0].Refs[0].BaseRef != "main" {
				t.Fatalf("lookupInputs() = %#v, want one pull request on main", inputs)
			}
		})
	}
}

func TestSyncRetainsAroHcpCredentials(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	created := metav1.Now()
	prowJob := &prowapiv1.ProwJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "aro-job",
			Namespace:         "ci",
			CreationTimestamp: created,
			Labels:            map[string]string{utils.LaunchLabel: "true"},
			Annotations: map[string]string{
				"ci-chat-bot.openshift.io/jobInputs": `[{"Version":"4.23"}]`,
				"ci-chat-bot.openshift.io/user":      "U123",
				"ci-chat-bot.openshift.io/mode":      JobTypeAroHcp,
				"ci-chat-bot.openshift.io/expires":   "3600",
				"release.openshift.io/buildCluster":  "build01",
			},
		},
		Status: prowapiv1.ProwJobStatus{State: prowapiv1.PendingState},
	}
	if err := indexer.Add(prowJob); err != nil {
		t.Fatalf("add ProwJob: %v", err)
	}
	manager := &jobManager{
		jobs: map[string]*Job{
			"aro-job": {
				Name:               "aro-job",
				Mode:               JobTypeAroHcp,
				State:              prowapiv1.PendingState,
				Credentials:        "service kubeconfig",
				Credentials2:       "management kubeconfig",
				CredentialsSnippet: "login details",
				StartDuration:      12 * time.Minute,
			},
		},
		requests:      map[string]*JobRequest{"U123": {User: "U123", Name: "aro-job", RequestedAt: created.Time}},
		prowLister:    prowjobLister.NewProwJobLister(indexer),
		prowNamespace: "ci",
		maxAge:        time.Hour,
	}
	if err := manager.sync(); err != nil {
		t.Fatalf("sync() returned error: %v", err)
	}
	job, err := manager.GetLaunchJob("U123")
	if err != nil {
		t.Fatalf("GetLaunchJob() returned error after sync: %v", err)
	}
	if job.Credentials != "service kubeconfig" || job.Credentials2 != "management kubeconfig" || job.CredentialsSnippet != "login details" || job.StartDuration != 12*time.Minute {
		t.Fatalf("sync() lost ready credentials: %#v", job)
	}
}
