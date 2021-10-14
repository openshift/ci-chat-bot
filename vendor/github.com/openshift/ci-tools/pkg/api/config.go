package api

import (
	"fmt"
	"strings"
	"time"

	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

// Default sets default values after loading but before validation
func (config *ReleaseBuildConfiguration) Default() {
	defLeases := func(l []StepLease) {
		for i := range l {
			if l[i].Count == 0 {
				l[i].Count = 1
			}
		}
	}
	def := func(s *LiteralTestStep) {
		defLeases(s.Leases)
	}
	defClusterClaim := func(c *ClusterClaim) {
		if c == nil {
			return
		}
		if c.Product == "" {
			c.Product = ReleaseProductOCP
		}
		if c.Architecture == "" {
			c.Architecture = ReleaseArchitectureAMD64
		}
		if c.Timeout == nil {
			c.Timeout = &prowv1.Duration{Duration: time.Hour}
		}
	}
	defTest := func(t *TestStepConfiguration) {
		defClusterClaim(t.ClusterClaim)
		if s := t.MultiStageTestConfigurationLiteral; s != nil {
			defLeases(s.Leases)
			for i := range s.Pre {
				def(&s.Pre[i])
			}
			for i := range s.Test {
				def(&s.Test[i])
			}
			for i := range s.Post {
				def(&s.Post[i])
			}
		}
	}
	for i := range config.RawSteps {
		if test := config.RawSteps[i].TestStepConfiguration; test != nil {
			defTest(test)
		}
	}
	for i := range config.Tests {
		defTest(&config.Tests[i])
	}
}

// ImageStreamFor guesses at the ImageStream that will hold a tag.
// We use this to decipher the user's intent when they provide a
// naked tag in configuration; we support such behavior in order to
// allow users a simpler workflow for the most common cases, like
// referring to `pipeline:src`. If they refer to an ambiguous image,
// however, they will get bad behavior and will need to specify an
// ImageStream as well, for instance release-initial:installer.
// We also return whether the stream is explicit or inferred.
func (config *ReleaseBuildConfiguration) ImageStreamFor(image string) (string, bool) {
	if config.IsPipelineImage(image) || config.BuildsImage(image) {
		return PipelineImageStream, true
	} else {
		return StableImageStream, false
	}
}

// DependencyParts returns the imageStream and tag name from a user-provided
// reference to an image in the test namespace. In situations where a user
// defines a cluster claim and wants to import the cluster claim's release, the
// user may provide a release name that conflicts with a release defined at the
// global config level (e.g. the `latest` release, or `stable` imagestream). To
// prevent conflicts, the name of the imagestream is modified based on the test
// name. ClaimRelease is used in this function to identify whether to override
// the imagestream provided by the user to use the cluster claim's imagestream.
func (config *ReleaseBuildConfiguration) DependencyParts(dependency StepDependency, claimRelease *ClaimRelease) (stream string, name string, explicit bool) {
	if !strings.Contains(dependency.Name, ":") {
		stream, explicit = config.ImageStreamFor(dependency.Name)
		name = dependency.Name
	} else {
		parts := strings.Split(dependency.Name, ":")
		stream = parts[0]
		name = parts[1]
		explicit = true
	}
	if claimRelease != nil {
		if stream == ReleaseImageStream && claimRelease.OverrideName == name {
			// handle release images like `release:latest`
			name = claimRelease.ReleaseName
		} else if stream == ReleaseStreamFor(claimRelease.OverrideName) {
			// handle images from release streams like `stable:cli`
			stream = ReleaseStreamFor(claimRelease.ReleaseName)
		}
	}
	return stream, name, explicit
}

// WithPresubmitFrom returns a new configuration, where a selected test from the source
// configuration is injected into the base configuration, together with all elements from
// the source configuration that are potentially necessary to allow that test to function
// in the context of the base configuration. The intended use case is to inject the test
// definition of a "release job" (informing/blocking) into a component ci-operator config
// to allow dynamically executing any such job on any component PR, without the need to
// clutter each individual component ci-operator config.
//
// WARNING: This code is currently experimental and should not be used outside of the
// "release jobs on PRs" effort
// TODO: handle the presubmit/periodic better, extract code etc.
func (config *ReleaseBuildConfiguration) WithPresubmitFrom(source *ReleaseBuildConfiguration, test string) (*ReleaseBuildConfiguration, error) {
	var result ReleaseBuildConfiguration
	config.DeepCopyInto(&result)

	for name, isTagRef := range source.BaseImages {
		// TODO: handle conflicts better
		if _, ok := result.BaseImages[name]; ok {
			return nil, fmt.Errorf("conflicting base_images: %s", name)
		}
		result.BaseImages[name] = isTagRef
	}

	for name, release := range source.Releases {
		if name == "latest" && result.ReleaseTagConfiguration != nil {
			continue
		}
		if result.Releases == nil {
			result.Releases = map[string]UnresolvedRelease{}
		}
		result.Releases[name] = release
	}

	// TODO: handle resources, likely needs to be union, with max(config, source) on conflicts

	for i := range source.Tests {
		if source.Tests[i].As == test {
			test := source.Tests[i]
			test.Interval = nil
			test.Cron = nil
			test.Postsubmit = false
			result.Tests = []TestStepConfiguration{test}

			return &result, nil
		}
	}
	return nil, fmt.Errorf("test '%s' not found in source configuration", test)
}
