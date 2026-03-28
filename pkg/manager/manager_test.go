package manager

import (
	"fmt"
	"testing"

	"github.com/openshift/ci-tools/pkg/lease"
)

type mockLeaseClient struct {
	metrics map[string]lease.Metrics
}

func (m *mockLeaseClient) Metrics(rtype string) (lease.Metrics, error) {
	if metrics, ok := m.metrics[rtype]; ok {
		return metrics, nil
	}
	return lease.Metrics{}, fmt.Errorf("resource type %q not found", rtype)
}

func Test_selectCloudAccountProfile(t *testing.T) {
	tests := []struct {
		name        string
		platform    string
		metrics     map[string]lease.Metrics
		wantNil     bool
		wantProfile string
		wantErr     bool
	}{
		{
			name:     "platform not in map returns nil",
			platform: "metal",
			metrics:  map[string]lease.Metrics{},
			wantNil:  true,
		},
		{
			name:     "primary has more free resources returns nil",
			platform: "aws",
			metrics: map[string]lease.Metrics{
				"aws-quota-slice":   {Free: 10, Leased: 5},
				"aws-2-quota-slice": {Free: 3, Leased: 12},
			},
			wantNil: true,
		},
		{
			name:     "secondary has more free resources returns that profile",
			platform: "aws",
			metrics: map[string]lease.Metrics{
				"aws-quota-slice":   {Free: 2, Leased: 13},
				"aws-2-quota-slice": {Free: 8, Leased: 7},
			},
			wantProfile: "aws-2",
		},
		{
			name:     "equal free counts returns nil (primary wins)",
			platform: "aws",
			metrics: map[string]lease.Metrics{
				"aws-quota-slice":   {Free: 5, Leased: 10},
				"aws-2-quota-slice": {Free: 5, Leased: 10},
			},
			wantNil: true,
		},
		{
			name:     "all zero free returns nil",
			platform: "aws",
			metrics: map[string]lease.Metrics{
				"aws-quota-slice":   {Free: 0, Leased: 15},
				"aws-2-quota-slice": {Free: 0, Leased: 15},
			},
			wantNil: true,
		},
		{
			name:     "gcp secondary wins",
			platform: "gcp",
			metrics: map[string]lease.Metrics{
				"gcp-quota-slice":                          {Free: 1, Leased: 14},
				"gcp-openshift-gce-devel-ci-2-quota-slice": {Free: 7, Leased: 8},
			},
			wantProfile: "gcp-openshift-gce-devel-ci-2",
		},
		{
			name:     "azure secondary wins",
			platform: "azure",
			metrics: map[string]lease.Metrics{
				"azure4-quota-slice":  {Free: 3, Leased: 12},
				"azure-2-quota-slice": {Free: 9, Leased: 6},
			},
			wantProfile: "azure-2",
		},
		{
			name:     "metrics error returns error",
			platform: "aws",
			metrics:  map[string]lease.Metrics{},
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &mockLeaseClient{metrics: tt.metrics}
			got, err := selectCloudAccountProfile(tt.platform, client)
			if (err != nil) != tt.wantErr {
				t.Errorf("selectCloudAccountProfile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if tt.wantNil && got != nil {
				t.Errorf("selectCloudAccountProfile() = %+v, want nil", got)
				return
			}
			if !tt.wantNil && got == nil {
				t.Errorf("selectCloudAccountProfile() = nil, want profile %q", tt.wantProfile)
				return
			}
			if !tt.wantNil && got.ProfileName != tt.wantProfile {
				t.Errorf("selectCloudAccountProfile() ProfileName = %q, want %q", got.ProfileName, tt.wantProfile)
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsValidVersion(tt.args.listOfImageOrVersionOrPRs); got != tt.want {
				t.Errorf("containsValidVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}
