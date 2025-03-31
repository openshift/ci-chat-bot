package slack

import (
	"testing"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"k8s.io/apimachinery/pkg/util/sets"
)

func Test_containsValidVersion(t *testing.T) {
	type args struct {
		parameters []string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "Empty arguments: 'launch'",
			args: args{
				parameters: []string{""},
			},
			want: false,
		},
		{
			name: "Valid version by itself: 'launch 4.19'",
			args: args{
				parameters: []string{"4.19"},
			},
			want: true,
		},
		{
			name: "Using a pull request without version specified: 'launch openshift/installer#7160'",
			args: args{
				parameters: []string{"openshift/installer#7160"},
			},
			want: false,
		},
		{
			name: "Using a pull request with a version specified: 'launch 4.19,openshift/installer#7160'",
			args: args{
				parameters: []string{"4.19,openshift/installer#7160"},
			},
			want: true,
		},
		{
			name: "Using two pull requests: 'launch openshift/installer#7160,openshift/machine-config-operator#3688'",
			args: args{
				parameters: []string{"openshift/installer#7160", "openshift/machine-config-operator#3688"},
			},
			want: false,
		},
		{
			name: "Using two pull requests with version: 'launch 4.19,openshift/installer#7160,openshift/machine-config-operator#3688'",
			args: args{
				parameters: []string{"4.19", "openshift/installer#7160", "openshift/machine-config-operator#3688"},
			},
			want: true,
		},
	}

	manager.HypershiftSupportedVersions.Versions = sets.New("4.19")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsValidVersion(tt.args.parameters); got != tt.want {
				t.Errorf("containsValidVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}
