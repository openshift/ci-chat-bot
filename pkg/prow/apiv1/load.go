package apiv1

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	// DefaultJobTimeout represents the default deadline for a prow job.
	DefaultJobTimeout = 24 * time.Hour
)

// Load loads and parses the config at path.
func Load(prowConfig, jobConfig string) (c *Config, err error) {
	// we never want config loading to take down the prow components
	defer func() {
		if r := recover(); r != nil {
			c, err = nil, fmt.Errorf("panic loading config: %v", r)
		}
	}()
	c, err = loadConfig(prowConfig, jobConfig)
	if err != nil {
		return nil, err
	}
	if err := c.finalizeJobConfig(); err != nil {
		return nil, err
	}
	if err := c.validateComponentConfig(); err != nil {
		return nil, err
	}
	if err := c.validateJobConfig(); err != nil {
		return nil, err
	}
	return c, nil
}

// loadConfig loads one or multiple config files and returns a config object.
func loadConfig(prowConfig, jobConfig string) (*Config, error) {
	stat, err := os.Stat(prowConfig)
	if err != nil {
		return nil, err
	}

	if stat.IsDir() {
		return nil, fmt.Errorf("prowConfig cannot be a dir - %s", prowConfig)
	}

	var nc Config
	if err := yamlToConfig(prowConfig, &nc); err != nil {
		return nil, err
	}
	if err := parseProwConfig(&nc); err != nil {
		return nil, err
	}

	// TODO(krzyzacy): temporary allow empty jobconfig
	//                 also temporary allow job config in prow config
	if jobConfig == "" {
		return &nc, nil
	}

	stat, err = os.Stat(jobConfig)
	if err != nil {
		return nil, err
	}

	if !stat.IsDir() {
		// still support a single file
		var jc JobConfig
		if err := yamlToConfig(jobConfig, &jc); err != nil {
			return nil, err
		}
		if err := nc.mergeJobConfig(jc); err != nil {
			return nil, err
		}
		return &nc, nil
	}

	// we need to ensure all config files have unique basenames,
	// since updateconfig plugin will use basename as a key in the configmap
	uniqueBasenames := sets.String{}

	err = filepath.Walk(jobConfig, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			logrus.WithError(err).Errorf("walking path %q.", path)
			// bad file should not stop us from parsing the directory
			return nil
		}

		if strings.HasPrefix(info.Name(), "..") {
			// kubernetes volumes also include files we
			// should not look be looking into for keys
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml" {
			return nil
		}

		if info.IsDir() {
			return nil
		}

		base := filepath.Base(path)
		if uniqueBasenames.Has(base) {
			return fmt.Errorf("duplicated basename is not allowed: %s", base)
		}
		uniqueBasenames.Insert(base)

		var subConfig JobConfig
		if err := yamlToConfig(path, &subConfig); err != nil {
			return err
		}
		return nc.mergeJobConfig(subConfig)
	})

	if err != nil {
		return nil, err
	}

	return &nc, nil
}

// yamlToConfig converts a yaml file into a Config object
func yamlToConfig(path string, nc interface{}) error {
	b, err := ReadFileMaybeGZIP(path)
	if err != nil {
		return fmt.Errorf("error reading %s: %v", path, err)
	}
	if err := yaml.Unmarshal(b, nc); err != nil {
		return fmt.Errorf("error unmarshaling %s: %v", path, err)
	}
	var jc *JobConfig
	switch v := nc.(type) {
	case *JobConfig:
		jc = v
	case *Config:
		jc = &v.JobConfig
	}

	var fix func(*Periodic)
	fix = func(job *Periodic) {
		job.SourcePath = path
	}
	for i := range jc.Periodics {
		fix(&jc.Periodics[i])
	}
	return nil
}

// ReadFileMaybeGZIP wraps ioutil.ReadFile, returning the decompressed contents
// if the file is gzipped, or otherwise the raw contents
func ReadFileMaybeGZIP(path string) ([]byte, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// check if file contains gzip header: http://www.zlib.org/rfc-gzip.html
	if !bytes.HasPrefix(b, []byte("\x1F\x8B")) {
		// go ahead and return the contents if not gzipped
		return b, nil
	}
	// otherwise decode
	gzipReader, err := gzip.NewReader(bytes.NewBuffer(b))
	if err != nil {
		return nil, err
	}
	return ioutil.ReadAll(gzipReader)
}

// mergeConfig merges two JobConfig together
// It will try to merge:
//	- Presubmits
//	- Postsubmits
// 	- Periodics
//	- PodPresets
func (c *Config) mergeJobConfig(jc JobConfig) error {
	// Merge everything
	// *** Presets ***
	// c.Presets = append(c.Presets, jc.Presets...)

	// validate no duplicated preset key-value pairs
	// validLabels := map[string]bool{}
	// for _, preset := range c.Presets {
	// 	for label, val := range preset.Labels {
	// 		pair := label + ":" + val
	// 		if _, ok := validLabels[pair]; ok {
	// 			return fmt.Errorf("duplicated preset 'label:value' pair : %s", pair)
	// 		}
	// 		validLabels[pair] = true
	// 	}
	// }

	// *** Periodics ***
	c.Periodics = append(c.Periodics, jc.Periodics...)

	// *** Presubmits ***
	// if c.Presubmits == nil {
	// 	c.Presubmits = make(map[string][]Presubmit)
	// }
	// for repo, jobs := range jc.Presubmits {
	// 	c.Presubmits[repo] = append(c.Presubmits[repo], jobs...)
	// }

	// *** Postsubmits ***
	// if c.Postsubmits == nil {
	// 	c.Postsubmits = make(map[string][]Postsubmit)
	// }
	// for repo, jobs := range jc.Postsubmits {
	// 	c.Postsubmits[repo] = append(c.Postsubmits[repo], jobs...)
	// }

	return nil
}

func setPeriodicDecorationDefaults(c *Config, ps *Periodic) {
	if ps.Decorate {
		ps.DecorationConfig = ps.DecorationConfig.ApplyDefault(c.Plank.DefaultDecorationConfig)
	}
}

// finalizeJobConfig mutates and fixes entries for jobspecs
func (c *Config) finalizeJobConfig() error {
	if c.decorationRequested() {
		if c.Plank.DefaultDecorationConfig == nil {
			return errors.New("no default decoration config provided for plank")
		}
		if c.Plank.DefaultDecorationConfig.UtilityImages == nil {
			return errors.New("no default decoration image pull specs provided for plank")
		}
		if c.Plank.DefaultDecorationConfig.GCSConfiguration == nil {
			return errors.New("no default GCS decoration config provided for plank")
		}
		if c.Plank.DefaultDecorationConfig.GCSCredentialsSecret == "" {
			return errors.New("no default GCS credentials secret provided for plank")
		}

		for i := range c.Periodics {
			setPeriodicDecorationDefaults(c, &c.Periodics[i])
		}
	}

	c.defaultPeriodicFields(c.Periodics)

	for _, v := range c.AllPeriodics() {
		if err := resolvePresets(v.Name, v.Labels, v.Spec, c.Presets); err != nil {
			return err
		}
	}

	return nil
}

// validateComponentConfig validates the infrastructure component configuration
func (c *Config) validateComponentConfig() error {
	if c.Plank.JobURLPrefix != "" && c.Plank.JobURLPrefixConfig["*"] != "" {
		return errors.New(`Planks job_url_prefix must be unset when job_url_prefix_config["*"] is set. The former is deprecated, use the latter`)
	}
	for k, v := range c.Plank.JobURLPrefixConfig {
		if _, err := url.Parse(v); err != nil {
			return fmt.Errorf(`Invalid value for Planks job_url_prefix_config["%s"]: %v`, k, err)
		}
	}
	return nil
}

var jobNameRegex = regexp.MustCompile(`^[A-Za-z0-9-._]+$`)

func validateJobBase(v JobBase, jobType ProwJobType, podNamespace string) error {
	if !jobNameRegex.MatchString(v.Name) {
		return fmt.Errorf("name: must match regex %q", jobNameRegex.String())
	}
	// Ensure max_concurrency is non-negative.
	if v.MaxConcurrency < 0 {
		return fmt.Errorf("max_concurrency: %d must be a non-negative number", v.MaxConcurrency)
	}
	if err := validateAgent(v, podNamespace); err != nil {
		return err
	}
	if err := validatePodSpec(jobType, v.Spec); err != nil {
		return err
	}
	if err := validateLabels(v.Labels); err != nil {
		return err
	}
	if v.Spec == nil || len(v.Spec.Containers) == 0 {
		return nil // knative-build and jenkins jobs have no spec
	}
	return validateDecoration(v.Spec.Containers[0], v.DecorationConfig)
}

// validateJobConfig validates if all the jobspecs/presets are valid
// if you are mutating the jobs, please add it to finalizeJobConfig above
func (c *Config) validateJobConfig() error {
	type orgRepoJobName struct {
		orgRepo, jobName string
	}

	// validate no duplicated periodics
	validPeriodics := sets.NewString()
	// Ensure that the periodic durations are valid and specs exist.
	for _, p := range c.AllPeriodics() {
		if validPeriodics.Has(p.Name) {
			return fmt.Errorf("duplicated periodic job : %s", p.Name)
		}
		validPeriodics.Insert(p.Name)
		if err := validateJobBase(p.JobBase, PeriodicJob, c.PodNamespace); err != nil {
			return fmt.Errorf("invalid periodic job %s: %v", p.Name, err)
		}
	}
	// Set the interval on the periodic jobs. It doesn't make sense to do this
	// for child jobs.
	for j, p := range c.Periodics {
		if p.Cron != "" && p.Interval != "" {
			return fmt.Errorf("cron and interval cannot be both set in periodic %s", p.Name)
		} else if p.Cron == "" && p.Interval == "" {
			return fmt.Errorf("cron and interval cannot be both empty in periodic %s", p.Name)
		} else if p.Cron != "" {
			// if _, err := cron.Parse(p.Cron); err != nil {
			// 	return fmt.Errorf("invalid cron string %s in periodic %s: %v", p.Cron, p.Name, err)
			// }
		} else {
			d, err := time.ParseDuration(c.Periodics[j].Interval)
			if err != nil {
				return fmt.Errorf("cannot parse duration for %s: %v", c.Periodics[j].Name, err)
			}
			c.Periodics[j].interval = d
		}
	}

	return nil
}

// ConfigPath returns the value for the component's configPath if provided
// explicityly or default otherwise.
func ConfigPath(value string) string {

	// TODO: require setting this explicitly
	defaultConfigPath := "/etc/config/config.yaml"

	if value != "" {
		return value
	}
	logrus.Warningf("defaulting to %s until 15 July 2019, please migrate", defaultConfigPath)
	return defaultConfigPath
}

func parseProwConfig(c *Config) error {
	if err := ValidateController(&c.Plank.Controller); err != nil {
		return fmt.Errorf("validating plank config: %v", err)
	}

	if c.Plank.PodPendingTimeoutString == "" {
		c.Plank.PodPendingTimeout = 24 * time.Hour
	} else {
		podPendingTimeout, err := time.ParseDuration(c.Plank.PodPendingTimeoutString)
		if err != nil {
			return fmt.Errorf("cannot parse duration for plank.pod_pending_timeout: %v", err)
		}
		c.Plank.PodPendingTimeout = podPendingTimeout
	}

	if c.ProwJobNamespace == "" {
		c.ProwJobNamespace = "default"
	}
	if c.PodNamespace == "" {
		c.PodNamespace = "default"
	}

	if c.Plank.JobURLPrefixConfig == nil {
		c.Plank.JobURLPrefixConfig = map[string]string{}
	}
	if c.Plank.JobURLPrefix != "" && c.Plank.JobURLPrefixConfig["*"] == "" {
		c.Plank.JobURLPrefixConfig["*"] = c.Plank.JobURLPrefix
		// Set JobURLPrefix to an empty string to indicate we've moved
		// it to JobURLPrefixConfig["*"] without overwriting the latter
		// so validation succeeds
		c.Plank.JobURLPrefix = ""
	}

	if c.GitHubOptions.LinkURLFromConfig == "" {
		c.GitHubOptions.LinkURLFromConfig = "https://github.com"
	}
	linkURL, err := url.Parse(c.GitHubOptions.LinkURLFromConfig)
	if err != nil {
		return fmt.Errorf("unable to parse github.link_url, might not be a valid url: %v", err)
	}
	c.GitHubOptions.LinkURL = linkURL

	if c.StatusErrorLink == "" {
		c.StatusErrorLink = "https://github.com/kubernetes/test-infra/issues"
	}

	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	lvl, err := logrus.ParseLevel(c.LogLevel)
	if err != nil {
		return err
	}
	logrus.SetLevel(lvl)

	// Avoid using a job timeout of infinity by setting the default value to 24 hours
	if c.DefaultJobTimeout == 0 {
		c.DefaultJobTimeout = DefaultJobTimeout
	}

	return nil
}

func (c *JobConfig) decorationRequested() bool {
	for i := range c.Periodics {
		if c.Periodics[i].Decorate {
			return true
		}
	}

	return false
}

func validateLabels(labels map[string]string) error {
	for label, value := range labels {
		for _, prowLabel := range Labels() {
			if label == prowLabel {
				return fmt.Errorf("label %s is reserved for decoration", label)
			}
		}
		if errs := validation.IsQualifiedName(label); len(errs) != 0 {
			return fmt.Errorf("invalid label %s: %v", label, errs)
		}
		if errs := validation.IsValidLabelValue(labels[label]); len(errs) != 0 {
			return fmt.Errorf("label %s has invalid value %s: %v", label, value, errs)
		}
	}
	return nil
}

func validateAgent(v JobBase, podNamespace string) error {
	k := string(KubernetesAgent)
	b := string(KnativeBuildAgent)
	j := string(JenkinsAgent)
	agents := sets.NewString(k, b, j)
	agent := v.Agent
	switch {
	case !agents.Has(agent):
		return fmt.Errorf("agent must be one of %s (found %q)", strings.Join(agents.List(), ", "), agent)
	case v.Spec != nil && agent != k:
		return fmt.Errorf("job specs require agent: %s (found %q)", k, agent)
	case agent == k && v.Spec == nil:
		return errors.New("kubernetes jobs require a spec")
	case v.DecorationConfig != nil && agent != k && agent != b:
		// TODO(fejta): only source decoration supported...
		return fmt.Errorf("decoration requires agent: %s or %s (found %q)", k, b, agent)
	case v.ErrorOnEviction && agent != k:
		return fmt.Errorf("error_on_eviction only applies to agent: %s (found %q)", k, agent)
	case v.Namespace == nil || *v.Namespace == "":
		return fmt.Errorf("failed to default namespace")
	case *v.Namespace != podNamespace && agent != b:
		// TODO(fejta): update plank to allow this (depends on client change)
		return fmt.Errorf("namespace customization requires agent: %s (found %q)", b, agent)
	}
	return nil
}

func validateDecoration(container v1.Container, config *DecorationConfig) error {
	if config == nil {
		return nil
	}

	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid decoration config: %v", err)
	}
	var args []string
	args = append(append(args, container.Command...), container.Args...)
	if len(args) == 0 || args[0] == "" {
		return errors.New("decorated job containers must specify command and/or args")
	}
	return nil
}

func resolvePresets(name string, labels map[string]string, spec *v1.PodSpec, presets []Preset) error {
	for _, preset := range presets {
		if spec != nil {
			if err := mergePreset(preset, labels, spec.Containers, &spec.Volumes); err != nil {
				return fmt.Errorf("job %s failed to merge presets for podspec: %v", name, err)
			}
		}
	}

	return nil
}

func validatePodSpec(jobType ProwJobType, spec *v1.PodSpec) error {
	if spec == nil {
		return nil
	}

	if len(spec.InitContainers) != 0 {
		return errors.New("pod spec may not use init containers")
	}

	if n := len(spec.Containers); n != 1 {
		return fmt.Errorf("pod spec must specify exactly 1 container, found: %d", n)
	}

	for _, env := range spec.Containers[0].Env {
		for _, prowEnv := range EnvForType(jobType) {
			if env.Name == prowEnv {
				// TODO(fejta): consider allowing this
				return fmt.Errorf("env %s is reserved", env.Name)
			}
		}
	}

	for _, mount := range spec.Containers[0].VolumeMounts {
		for _, prowMount := range VolumeMounts() {
			if mount.Name == prowMount {
				return fmt.Errorf("volumeMount name %s is reserved for decoration", prowMount)
			}
		}
		for _, prowMountPath := range VolumeMountPaths() {
			if strings.HasPrefix(mount.MountPath, prowMountPath) || strings.HasPrefix(prowMountPath, mount.MountPath) {
				return fmt.Errorf("mount %s at %s conflicts with decoration mount at %s", mount.Name, mount.MountPath, prowMountPath)
			}
		}
	}

	for _, volume := range spec.Volumes {
		for _, prowVolume := range VolumeMounts() {
			if volume.Name == prowVolume {
				return fmt.Errorf("volume %s is a reserved for decoration", volume.Name)
			}
		}
	}

	return nil
}

// ValidateController validates the provided controller config.
func ValidateController(c *Controller) error {
	urlTmpl, err := template.New("JobURL").Parse(c.JobURLTemplateString)
	if err != nil {
		return fmt.Errorf("parsing template: %v", err)
	}
	c.JobURLTemplate = urlTmpl

	reportTmpl, err := template.New("Report").Parse(c.ReportTemplateString)
	if err != nil {
		return fmt.Errorf("parsing template: %v", err)
	}
	c.ReportTemplate = reportTmpl
	if c.MaxConcurrency < 0 {
		return fmt.Errorf("controller has invalid max_concurrency (%d), it needs to be a non-negative number", c.MaxConcurrency)
	}
	if c.MaxGoroutines == 0 {
		c.MaxGoroutines = 20
	}
	if c.MaxGoroutines <= 0 {
		return fmt.Errorf("controller has invalid max_goroutines (%d), it needs to be a positive number", c.MaxGoroutines)
	}
	return nil
}

// DefaultTriggerFor returns the default regexp string used to match comments
// that should trigger the job with this name.
func DefaultTriggerFor(name string) string {
	return fmt.Sprintf(`(?m)^/test( | .* )%s,?($|\s.*)`, name)
}

// DefaultRerunCommandFor returns the default rerun command for the job with
// this name.
func DefaultRerunCommandFor(name string) string {
	return fmt.Sprintf("/test %s", name)
}

// defaultJobBase configures common parameters, currently Agent and Namespace.
func (c *ProwConfig) defaultJobBase(base *JobBase) {
	if base.Agent == "" { // Use kubernetes by default
		base.Agent = string(KubernetesAgent)
	}
	if base.Namespace == nil || *base.Namespace == "" {
		s := c.PodNamespace
		base.Namespace = &s
	}
	if base.Cluster == "" {
		base.Cluster = "default"
	}
}

func (c *ProwConfig) defaultPeriodicFields(js []Periodic) {
	for i := range js {
		c.defaultJobBase(&js[i].JobBase)
	}
}
