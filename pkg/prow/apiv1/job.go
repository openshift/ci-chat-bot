package apiv1

import (
	"k8s.io/apimachinery/pkg/labels"
)

func ProwSpecForPeriodicConfig(config *PeriodicConfig, decorationConfig *DecorationConfig) *ProwJobSpec {
	spec := &ProwJobSpec{
		Type:  PeriodicJob,
		Job:   config.Name,
		Agent: KubernetesAgent,

		Refs: &Refs{},

		PodSpec: config.Spec.DeepCopy(),
	}

	if decorationConfig != nil {
		spec.DecorationConfig = decorationConfig.DeepCopy()
	} else {
		spec.DecorationConfig = &DecorationConfig{}
	}
	spec.DecorationConfig.SkipCloning = true

	return spec
}

func HasProwJob(config *Config, name string) (*PeriodicConfig, bool) {
	for i := range config.Periodics {
		if config.Periodics[i].Name == name {
			return &config.Periodics[i], true
		}
	}
	return nil, false
}

func HasProwJobWithLabels(config *Config, selector labels.Selector) (*PeriodicConfig, bool) {
	for i := range config.Periodics {
		if selector.Matches(labels.Set(config.Periodics[i].Labels)) {
			return &config.Periodics[i], true
		}
	}
	return nil, false
}
