package manager

import "testing"

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
