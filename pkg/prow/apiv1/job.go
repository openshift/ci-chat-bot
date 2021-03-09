package apiv1

import (
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/pjutil"

	prowapiv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

func ProwSpecForPeriodicConfig(config *config.Periodic) *prowapiv1.ProwJobSpec {
	spec := pjutil.PeriodicSpec(*config)
	isTrue := true
	spec.DecorationConfig.SkipCloning = &isTrue
	return &spec
}

func HasProwJob(config *config.Config, name string) (*config.Periodic, bool) {
	for i := range config.Periodics {
		if config.Periodics[i].Name == name {
			return &config.Periodics[i], true
		}
	}
	return nil, false
}

func HasProwJobWithLabels(config *config.Config, selector labels.Selector) (*config.Periodic, bool) {
	for i := range config.Periodics {
		if selector.Matches(labels.Set(config.Periodics[i].Labels)) {
			return &config.Periodics[i], true
		}
	}
	return nil, false
}
