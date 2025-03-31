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
				listOfImageOrVersionOrPRs: []string{"4.19,openshift/installer#7160"},
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsValidVersion(tt.args.listOfImageOrVersionOrPRs); got != tt.want {
				t.Errorf("containsValidVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}
