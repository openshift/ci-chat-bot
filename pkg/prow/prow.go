package prow

import (
	"fmt"
	"strings"

	"github.com/openshift/ci-chat-bot/pkg/utils"
	"sigs.k8s.io/prow/pkg/pjutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	prowapiv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
)

type ProwConfigLoader interface {
	Config() *config.Config
}

func JobForLabels(prowConfigLoader ProwConfigLoader, selector labels.Selector) (*prowapiv1.ProwJob, error) {
	prowConfig := prowConfigLoader.Config()
	if prowConfig == nil {
		return nil, fmt.Errorf("cannot locate prow job: no prow jobs have been defined")
	}
	periodicConfig, ok := hasProwJobWithLabels(prowConfig, selector)
	if !ok {
		return nil, fmt.Errorf("no prow job matches the label selector %s", selector.String())
	}

	spec := prowSpecForPeriodicConfig(periodicConfig)

	pj := &prowapiv1.ProwJob{
		TypeMeta: metav1.TypeMeta{APIVersion: "prow.k8s.io/v1", Kind: "ProwJob"},
		Spec:     *spec,
		Status: prowapiv1.ProwJobStatus{
			StartTime: metav1.Now(),
			State:     prowapiv1.TriggeredState,
		},
	}
	return pj, nil
}

func JobForConfig(prowConfigLoader ProwConfigLoader, jobName string) (*prowapiv1.ProwJob, error) {
	prowConfig := prowConfigLoader.Config()
	if prowConfig == nil {
		return nil, fmt.Errorf("the prow job %s is not valid: no prow jobs have been defined", jobName)
	}
	periodicConfig, ok := hasProwJob(prowConfig, jobName)
	if !ok {
		return nil, fmt.Errorf("the prow job %s is not valid: no job with that name", jobName)
	}

	spec := prowSpecForPeriodicConfig(periodicConfig)
	state := prowapiv1.TriggeredState
	if prowConfig.Scheduler.Enabled {
		state = prowapiv1.SchedulingState
	}

	pj := &prowapiv1.ProwJob{
		TypeMeta: metav1.TypeMeta{APIVersion: "prow.k8s.io/v1", Kind: "ProwJob"},
		Spec:     *spec,
		Status: prowapiv1.ProwJobStatus{
			StartTime: metav1.Now(),
			State:     state,
		},
	}
	pj = pj.DeepCopy()
	return pj, nil
}

func OverrideJobEnvironment(spec *prowapiv1.ProwJobSpec, image, initialImage, targetRelease, namespace string, variants []string) {
	for i := range spec.PodSpec.Containers {
		c := &spec.PodSpec.Containers[i]
		for j := range c.Env {
			switch name := c.Env[j].Name; name {
			case "RELEASE_IMAGE_LATEST":
				c.Env[j].Value = image
			case "RELEASE_IMAGE_INITIAL":
				c.Env[j].Value = initialImage
			case "NAMESPACE":
				c.Env[j].Value = namespace
			case "CLUSTER_VARIANT":
				c.Env[j].Value = strings.Join(variants, ",")
			case "BRANCH":
				if len(targetRelease) > 0 {
					c.Env[j].Value = targetRelease
				}
			}
		}
	}
}

func RemoveJobEnvVar(spec *prowapiv1.ProwJobSpec, names ...string) {
	for i := range spec.PodSpec.Containers {
		c := &spec.PodSpec.Containers[i]
		changed := make([]corev1.EnvVar, 0, len(c.Env))
		for _, env := range c.Env {
			if utils.Contains(names, env.Name) {
				continue
			}
			changed = append(changed, env)
		}
		c.Env = changed
	}
}

func SetJobEnvVar(spec *prowapiv1.ProwJobSpec, name, value string) {
	for i := range spec.PodSpec.Containers {
		var hasSet bool
		c := &spec.PodSpec.Containers[i]
		for j := range c.Env {
			if c.Env[j].Name == name {
				c.Env[j].Value = value
				c.Env[j].ValueFrom = nil
				hasSet = true
			}
		}
		if !hasSet {
			// place variable substitutions at the end, base values at the beginning
			if strings.Contains(value, "$(") {
				c.Env = append(c.Env, corev1.EnvVar{Name: name, Value: value})
			} else {
				c.Env = append([]corev1.EnvVar{{Name: name, Value: value}}, c.Env...)
			}
		}
	}
}

func OverrideJobEnvVar(spec *prowapiv1.ProwJobSpec, name, value string) {
	for i := range spec.PodSpec.Containers {
		c := &spec.PodSpec.Containers[i]
		for j := range c.Env {
			if c.Env[j].Name == name {
				c.Env[j].Value = value
				c.Env[j].ValueFrom = nil
			}
		}
	}
}

func prowSpecForPeriodicConfig(config *config.Periodic) *prowapiv1.ProwJobSpec {
	spec := pjutil.PeriodicSpec(*config)
	isTrue := true
	spec.DecorationConfig.SkipCloning = &isTrue

	if spec.PodSpec.NodeSelector == nil {
		spec.PodSpec.NodeSelector = make(map[string]string)
	}
	spec.PodSpec.NodeSelector = map[string]string{"kubernetes.io/arch": "amd64"}

	return &spec
}

func hasProwJob(config *config.Config, name string) (*config.Periodic, bool) {
	for i := range config.Periodics {
		if config.Periodics[i].Name == name {
			return &config.Periodics[i], true
		}
	}
	return nil, false
}

func hasProwJobWithLabels(config *config.Config, selector labels.Selector) (*config.Periodic, bool) {
	for i := range config.Periodics {
		if selector.Matches(labels.Set(config.Periodics[i].Labels)) {
			return &config.Periodics[i], true
		}
	}
	return nil, false
}
