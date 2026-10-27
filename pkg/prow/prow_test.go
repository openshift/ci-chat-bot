package prow

import (
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	prowapiv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
)

type fakeProwConfigLoader struct {
	cfg *config.Config
}

func (f fakeProwConfigLoader) Config() *config.Config {
	return f.cfg
}

func launchPeriodicConfig() *config.Periodic {
	return &config.Periodic{
		JobBase: config.JobBase{
			Name:   "periodic-launch",
			Labels: map[string]string{"job-type": "launch"},
			UtilityConfig: config.UtilityConfig{
				DecorationConfig: &prowapiv1.DecorationConfig{},
			},
			Spec: &corev1.PodSpec{
				Containers: []corev1.Container{{
					Name: "test",
					Env:  []corev1.EnvVar{{Name: "RELEASE_IMAGE_LATEST", Value: "original"}},
				}},
			},
		},
	}
}

var jobLookups = []struct {
	name string
	call func(loader ProwConfigLoader) (*prowapiv1.ProwJob, error)
}{
	{
		name: "JobForConfig",
		call: func(loader ProwConfigLoader) (*prowapiv1.ProwJob, error) {
			return JobForConfig(loader, "periodic-launch")
		},
	},
	{
		name: "JobForLabels",
		call: func(loader ProwConfigLoader) (*prowapiv1.ProwJob, error) {
			return JobForLabels(loader, labels.SelectorFromSet(labels.Set{"job-type": "launch"}))
		},
	},
}

// The specs returned by these functions share structure with the live prow
// config, so setting SkipCloning on them must not write through to it: the
// config is read concurrently by other lookups and replaced by reloads.
func TestJobSpecsDoNotMutateProwConfig(t *testing.T) {
	for _, tc := range jobLookups {
		for _, decorated := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/decorated=%t", tc.name, decorated), func(t *testing.T) {
				periodic := launchPeriodicConfig()
				if !decorated {
					periodic.DecorationConfig = nil
				}
				loader := fakeProwConfigLoader{cfg: &config.Config{JobConfig: config.JobConfig{Periodics: []config.Periodic{*periodic}}}}

				pj, err := tc.call(loader)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !decorated {
					if pj.Spec.DecorationConfig != nil {
						t.Errorf("expected the returned job spec to remain undecorated, got %+v", pj.Spec.DecorationConfig)
					}
				} else if pj.Spec.DecorationConfig == nil || pj.Spec.DecorationConfig.SkipCloning == nil || !*pj.Spec.DecorationConfig.SkipCloning {
					t.Errorf("expected the returned job spec to have SkipCloning set to true")
				}
				source := loader.cfg.Periodics[0].DecorationConfig
				if !decorated {
					if source != nil {
						t.Errorf("expected the source config's DecorationConfig to remain nil, got %+v", source)
					}
				} else if source.SkipCloning != nil {
					t.Errorf("expected the source config's DecorationConfig to be unmodified, got SkipCloning=%v", *source.SkipCloning)
				}
			})
		}
	}
}

// The specs are deep-copied so that callers can customize the returned job
// without writing through to the live prow config.
func TestJobSpecIsIndependent(t *testing.T) {
	for _, tc := range jobLookups {
		t.Run(tc.name, func(t *testing.T) {
			periodic := launchPeriodicConfig()
			loader := fakeProwConfigLoader{cfg: &config.Config{JobConfig: config.JobConfig{Periodics: []config.Periodic{*periodic}}}}

			pj, err := tc.call(loader)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			OverrideJobEnvironment(&pj.Spec, "latest", "initial", "4.19", "test-namespace", nil)
			if got := pj.Spec.PodSpec.Containers[0].Env[0].Value; got != "latest" {
				t.Errorf("expected the returned job's env var to be overridden, got %q", got)
			}
			if got := loader.cfg.Periodics[0].Spec.Containers[0].Env[0].Value; got != "original" {
				t.Errorf("expected the source config's pod spec to be unmodified, got %q", got)
			}
		})
	}
}
