package apiv1



func ProwSpecForPeriodicConfig(config *PeriodicConfig, decorationConfig *DecorationConfig) *prowapiv1.ProwJobSpec {
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