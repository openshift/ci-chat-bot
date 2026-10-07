package manager

import (
	"strings"
	"testing"

	"github.com/openshift/ci-chat-bot/pkg/prow"
	prowapiv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
)

type aroHcpConfigLoader struct{ config *config.Config }

func (l aroHcpConfigLoader) Config() *config.Config { return l.config }

func TestAroHcpJobSelectionMatchesConfiguredJobs(t *testing.T) {
	t.Parallel()
	azure := prowapiv1.Refs{Org: "Azure", Repo: "ARO-HCP"}
	hypershift := prowapiv1.Refs{Org: "openshift", Repo: "hypershift"}
	loader := aroHcpConfigLoader{config: &config.Config{JobConfig: config.JobConfig{
		Periodics: []config.Periodic{
			{JobBase: config.JobBase{Name: "release-openshift-origin-installer-launch-aro-hcp", UtilityConfig: config.UtilityConfig{DecorationConfig: &prowapiv1.DecorationConfig{}}}},
			{JobBase: config.JobBase{Name: "release-openshift-origin-installer-launch-aro-hcp-hypershift", UtilityConfig: config.UtilityConfig{DecorationConfig: &prowapiv1.DecorationConfig{}}}},
			{JobBase: config.JobBase{Name: "release-openshift-origin-installer-launch-aro-hcp-combined", UtilityConfig: config.UtilityConfig{DecorationConfig: &prowapiv1.DecorationConfig{}}}},
		},
	}}}
	for _, test := range []struct {
		name string
		refs []prowapiv1.Refs
		want string
	}{
		{name: "azure", refs: []prowapiv1.Refs{azure}, want: "release-openshift-origin-installer-launch-aro-hcp"},
		{name: "hypershift", refs: []prowapiv1.Refs{hypershift}, want: "release-openshift-origin-installer-launch-aro-hcp-hypershift"},
		{name: "combined", refs: []prowapiv1.Refs{azure, hypershift}, want: "release-openshift-origin-installer-launch-aro-hcp-combined"},
	} {
		t.Run(test.name, func(t *testing.T) {
			name, err := aroHcpJobNameFromInputs([]JobInput{{Refs: test.refs}})
			if err != nil {
				t.Fatal(err)
			}
			job, err := prow.JobForConfig(loader, name)
			if err != nil {
				t.Fatalf("selected job %q is not configured: %v", name, err)
			}
			if job.Spec.Job != test.want {
				t.Fatalf("selected job = %q, want %q", job.Spec.Job, test.want)
			}
		})
	}
}

func TestLookupAroHcpInputsWithDigestImage(t *testing.T) {
	t.Parallel()
	for _, image := range []string{
		"quay.io/openshift-release-dev/ocp-release@sha256:" + strings.Repeat("a", 64),
		"registry.example.com/release@sha256:" + strings.Repeat("b", 64),
		"quay.io/openshift-release-dev/ocp-release:latest@sha512:" + strings.Repeat("c", 128),
	} {
		for _, ref := range []string{"Azure/ARO-HCP#123", "openshift/hypershift@feature/test"} {
			for _, parts := range [][]string{{image, ref}, {ref, image}} {
				t.Run(strings.Join(parts, ","), func(t *testing.T) {
					m := &jobManager{githubClient: &lookupGitHubClient{}}
					inputs, _, err := m.lookupInputs([][]string{parts}, "amd64", true)
					if err != nil {
						t.Fatalf("lookupInputs() returned error: %v", err)
					}
					if len(inputs) != 1 || inputs[0].Image != image || len(inputs[0].Refs) != 1 {
						t.Fatalf("lookupInputs() = %#v, want image %q and one source ref", inputs, image)
					}
				})
			}
		}
	}
}
