package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"k8s.io/klog"
	"sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"
	rbacapi "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport"
	prowapiv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-chat-bot/pkg/prow"
	citools "github.com/openshift/ci-tools/pkg/api"
)

type envVar struct {
	name      string
	value     string
	platforms sets.String
}

var errJobCompleted = fmt.Errorf("job is complete")

// supportedTests lists any of the suites defined in the standard launch jobs (copied here for user friendliness)
var supportedTests = []string{"e2e", "e2e-serial", "e2e-all", "e2e-disruptive", "e2e-disruptive-all", "e2e-builds", "e2e-image-ecosystem", "e2e-image-registry", "e2e-network-stress"}

// supportedTests lists any of the upgrade suites defined in the standard launch jobs (copied here for user friendliness)
var supportedUpgradeTests = []string{"e2e-upgrade", "e2e-upgrade-all", "e2e-upgrade-partial", "e2e-upgrade-rollback"}

// supportedPlatforms requires a job within the release periodics that can launch a
// cluster that has the label job-env: platform-name.
var supportedPlatforms = []string{"aws", "gcp", "azure", "vsphere", "metal", "hypershift", "ovirt", "openstack"}

// supportedParameters are the allowed parameter keys that can be passed to jobs
var supportedParameters = []string{"ovn", "ovn-hybrid", "proxy", "compact", "fips", "mirror", "shared-vpc", "large", "xlarge", "ipv4", "ipv6", "dualstack", "preserve-bootstrap", "test", "rt", "single-node", "cgroupsv2", "techpreview", "upi", "crun", "nfv", "kuryr"}

// multistageParameters is the mapping of supportedParameters that can be configured via multistage parameters to the correct environment variable format
var multistageParameters = map[string]envVar{
	"compact": {
		name:      "SIZE_VARIANT",
		value:     "compact",
		platforms: sets.NewString("aws", "gcp", "azure"),
	},
	"large": {
		name:      "SIZE_VARIANT",
		value:     "large",
		platforms: sets.NewString("aws", "gcp", "azure"),
	},
	"xlarge": {
		name:      "SIZE_VARIANT",
		value:     "xlarge",
		platforms: sets.NewString("aws", "gcp", "azure"),
	},
	"preserve-bootstrap": {
		name:      "OPENSHIFT_INSTALL_PRESERVE_BOOTSTRAP",
		value:     "true",
		platforms: sets.NewString("aws", "gcp", "azure", "vsphere", "ovirt"),
	},
}

// envsForTestType maps tests given by users to corresponding parameters/env vars that need to be set on the test. Currently not platform dependent, so the platforms
// list for the envs will be left blank
var envsForTestType = map[string][]envVar{
	"e2e": {{
		name:  "TEST_SUITE",
		value: "openshift/conformance/parallel",
	}},
	"e2e-serial": {{
		name:  "TEST_SUITE",
		value: "openshift/conformance/serial",
	}},
	// Metal IPI specific IPv6 cluster
	"e2e-ipv6": {{
		name:  "TEST_SUITE",
		value: "openshift/conformance/parallel",
	}},
	// Metal IPI specific IPv4v6 dualstack cluster
	"e2e-dualstack": {{
		name:  "TEST_SUITE",
		value: "openshift/conformance/parallel",
	}},
	"e2e-all": {{
		name:  "TEST_SUITE",
		value: "openshift/conformance",
	}},
	"e2e-builds": {{
		name:  "TEST_SUITE",
		value: "openshift/build",
	}},
	"e2e-image-ecosystem": {{
		name:  "TEST_SUITE",
		value: "openshift/image-ecosystem",
	}},
	"e2e-image-registry": {{
		name:  "TEST_SUITE",
		value: "openshift/image-registry",
	}},
	"e2e-network-stress": {{
		name:  "TEST_SUITE",
		value: "openshift/network/stress",
	}},
	"e2e-disruptive": {{
		name:  "TEST_REQUIRES_SSH",
		value: "true",
	}, {
		name:  "TEST_SUITE",
		value: "openshift/disruptive",
	}},
	"e2e-disruptive-all": {{
		name:  "TEST_REQUIRES_SSH",
		value: "true",
	}, {
		name:  "TEST_SUITE",
		value: "openshift/disruptive",
	}, {
		name:  "TEST_TYPE",
		value: "suite-conformance",
	}},
	"e2e-upgrade": {{
		name:  "TEST_TYPE",
		value: "upgrade",
	}},
	// Currently, the openshift-e2e-test `upgrade-conformance` test type hardcodes the parallel conformance suite to run after the upgrade test.
	// However, the template based cluster-bot jobs use the full conformance suite, so there is not a 1-to-1 replacement that can be done here.
	// This is not an issue for the upgrade-partial and upgrade-rollback tests, as those were using the parallel conformance suite.
	// TODO: Talk with installer about this difference and see if it can be changed to allow for a full conformance test post-upgrade.
	/*
		"e2e-upgrade-all": {{
			name:  "TEST_TYPE",
			value: "upgrade-conformance",
		}},
	*/
	"e2e-upgrade-partial": {{
		name:  "TEST_TYPE",
		value: "upgrade-conformance",
	}, {
		name:  "TEST_UPGRADE_OPTIONS",
		value: "abort-at=random",
	}},
	"e2e-upgrade-rollback": {{
		name:  "TEST_TYPE",
		value: "upgrade-conformance",
	}, {
		name:  "TEST_UPGRADE_OPTIONS",
		value: "abort-at=100",
	}},
}

func testStepForPlatform(platform string) string {
	switch platform {
	case "aws", "gcp", "azure", "vsphere", "ovirt", "openstack":
		return "openshift-e2e-test"
	case "metal":
		return "baremetalds-e2e-test"
	default:
		// currently hypershift has no workflows that do any tests, so we can't override a launch job for e2e tests
		return ""
	}
}

// supportedArchitectures are the allowed architectures that can be passed to jobs
var supportedArchitectures = []string{"amd64", "arm64"}

var (
	// reReleaseVersion detects whether a branch appears to correlate to a release branch
	reReleaseVersion = regexp.MustCompile(`^(release|openshift)-(\d+\.\d+)`)
	// reVersion detects whether a version appears to correlate to a major.minor release
	reVersion = regexp.MustCompile(`^(\d+\.\d+)`)
)

// ConfigResolver finds a ci-operator config for the given tuple of organization, repository,
// branch, and variant.
type ConfigResolver interface {
	Resolve(org, repo, branch, variant string) ([]byte, bool, error)
}

type URLConfigResolver struct {
	URL *url.URL
}

func (r *URLConfigResolver) Resolve(org, repo, branch, variant string) ([]byte, bool, error) {
	switch r.URL.Scheme {
	case "http", "https":
		u := *r.URL
		v := make(url.Values)
		v.Add("org", org)
		v.Add("repo", repo)
		v.Add("branch", branch)
		if len(variant) > 0 {
			v.Add("variant", variant)
		}
		u.RawQuery = v.Encode()
		rt, err := transport.HTTPWrappersForConfig(&transport.Config{}, http.DefaultTransport)
		if err != nil {
			return nil, false, fmt.Errorf("url resolve failed: %v", err)
		}
		client := http.Client{Transport: rt}
		resp, err := client.Get(u.String())
		if err != nil {
			return nil, false, fmt.Errorf("url resolve failed: %v", err)
		}
		defer resp.Body.Close()
		switch resp.StatusCode {
		case 200:
			data, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				return nil, false, fmt.Errorf("url resolve failed to read body: %v", err)
			}
			return data, true, nil
		case 404:
			return nil, false, nil
		default:
			data, _ := ioutil.ReadAll(resp.Body)
			return nil, false, fmt.Errorf("url resolve failed with status code %d: %s", resp.StatusCode, string(bytes.TrimSpace(data)))
		}
	case "file":
		filename := fmt.Sprintf("%s-%s-%s", org, repo, branch)
		if len(variant) > 0 {
			filename = fmt.Sprintf("%s_%s", filename, variant)
		}
		path := filepath.Join(r.URL.Path, org, repo, filename+".yaml")
		klog.V(2).Infof("Attempting to read config from %s", path)
		data, err := ioutil.ReadFile(path)
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("file resolve failed: %v", err)
		}
		return data, true, nil
	default:
		return nil, false, fmt.Errorf("unrecognized URL config resolver scheme: %s", r.URL.Scheme)
	}
}

// stopJob triggers graceful cluster teardown. If this method returns nil,
// it is safe to consider the cluster released.
func (m *jobManager) stopJob(name, cluster string) error {
	uns, err := m.prowClient.Namespace(m.prowNamespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// There may have been an issue creating the prowjob; treat as success
			return nil
		}
		return err
	}
	var pj prowapiv1.ProwJob
	if err := prow.UnstructuredToObject(uns, &pj); err != nil {
		return err
	}

	_, err = m.clusterClients[cluster].CoreClient.CoreV1().Pods(m.prowNamespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			if pj.Status.State == prowapiv1.TriggeredState {
				return fmt.Errorf("original request is still initializing -- please try again in a few minutes")
			}
			// Since prowjob State != Triggered, pod creation should have been attempted.
			// If it is not here, there's nothing to stop
			return nil
		}
		return err
	}

	klog.Infof("ProwJob pod for job %q will be aborted", name)
	pj.Status.State = prowapiv1.AbortedState
	_, err = m.prowClient.Namespace(m.prowNamespace).Update(context.Background(), prow.ObjectToUnstructured(&pj), metav1.UpdateOptions{})
	return err
}

// newJob creates a ProwJob for running the provided job and exits.
func (m *jobManager) newJob(job *Job) (string, error) {
	if !m.tryJob(job.Name) {
		klog.Infof("Job %q already has a worker", job.Name)
		return "", nil
	}
	defer m.finishJob(job.Name)

	if job.IsComplete() && len(job.PasswordSnippet) > 0 {
		return "", nil
	}
	namespace := fmt.Sprintf("ci-ln-%s", namespaceSafeHash(job.Name))

	launchDeadline := 45 * time.Minute

	// launch a prow job, tied back to this cluster user
	pj, err := prow.JobForConfig(m.prowConfigLoader, job.JobName)
	if err != nil {
		return "", err
	}

	jobInputData, err := json.Marshal(job.Inputs)
	if err != nil {
		return "", err
	}

	pj.ObjectMeta = metav1.ObjectMeta{
		Name:      job.Name,
		Namespace: m.prowNamespace,
		Annotations: map[string]string{
			"ci-chat-bot.openshift.io/originalMessage": job.OriginalMessage,
			"ci-chat-bot.openshift.io/mode":            job.Mode,
			"ci-chat-bot.openshift.io/jobParams":       paramsToString(job.JobParams),
			"ci-chat-bot.openshift.io/user":            job.RequestedBy,
			"ci-chat-bot.openshift.io/channel":         job.RequestedChannel,
			"ci-chat-bot.openshift.io/ns":              namespace,
			"ci-chat-bot.openshift.io/platform":        job.Platform,
			"ci-chat-bot.openshift.io/jobInputs":       string(jobInputData),
			"ci-chat-bot.openshift.io/buildCluster":    job.BuildCluster,

			"prow.k8s.io/job": pj.Spec.Job,

			"release.openshift.io/architecture": job.Architecture,
		},
		Labels: map[string]string{
			"ci-chat-bot.openshift.io/launch": "true",

			"prow.k8s.io/type": string(pj.Spec.Type),
			"prow.k8s.io/job":  pj.Spec.Job,
		},
	}

	// sort the variant inputs
	var variants []string
	for k := range job.JobParams {
		if contains(supportedParameters, k) {
			variants = append(variants, k)
		}
	}
	sort.Strings(variants)

	// register annotations the release controller can use to assess the success
	// of this job if it is upgrading between two edges
	if len(job.Inputs) == 2 && len(job.Inputs[0].Refs) == 0 && len(job.Inputs[1].Refs) == 0 && len(job.Inputs[0].Version) > 0 && len(job.Inputs[1].Version) > 0 {
		pj.Labels["release.openshift.io/verify"] = "true"
		pj.Annotations["release.openshift.io/from-tag"] = job.Inputs[0].Version
		pj.Annotations["release.openshift.io/tag"] = job.Inputs[1].Version
	}
	// set standard annotations and environment variables
	pj.Annotations["ci-chat-bot.openshift.io/expires"] = strconv.Itoa(int(m.maxAge.Seconds() + launchDeadline.Seconds()))
	prow.OverrideJobEnvVar(&pj.Spec, "CLUSTER_DURATION", strconv.Itoa(int(m.maxAge.Seconds())))
	if job.Mode == "build" {
		// keep the built payload images around for a week
		prow.SetJobEnvVar(&pj.Spec, "PRESERVE_DURATION", "168h")
		prow.SetJobEnvVar(&pj.Spec, "DELETE_AFTER", "168h")
	} else {
		prow.SetJobEnvVar(&pj.Spec, "PRESERVE_DURATION", "1h")
		prow.SetJobEnvVar(&pj.Spec, "DELETE_AFTER", "12h")
	}

	// guess the most recent branch used by an input (taken from the last possible job input)
	var targetRelease string
	for _, input := range job.Inputs {
		if len(input.Version) == 0 {
			continue
		}
		if m := reVersion.FindStringSubmatch(input.Version); m != nil {
			targetRelease = m[1]
		}
	}

	// Identify the images to be placed in RELEASE_IMAGE_INITIAL and RELEASE_IMAGE_LATEST,
	// depending on whether this is an upgrade job or not. Create env var definitions for
	// use with the final step job (if we build, we unset both variables before the images
	// are built and need to restore them for the last step).
	var restoreImageVariableScript []string
	lastJobInput := len(job.Inputs) - 1
	image := job.Inputs[lastJobInput].Image
	runImage := job.Inputs[lastJobInput].RunImage
	var initialImage string
	if len(job.Inputs) > 1 {
		initialImage = job.Inputs[0].Image
		if len(job.Inputs[0].Refs) == 0 && len(initialImage) > 0 {
			restoreImageVariableScript = append(restoreImageVariableScript, fmt.Sprintf("RELEASE_IMAGE_INITIAL=%s", initialImage))
		}
		if len(job.Inputs[lastJobInput].Refs) == 0 && len(runImage) > 0 {
			restoreImageVariableScript = append(restoreImageVariableScript, fmt.Sprintf("RELEASE_IMAGE_LATEST=%s", runImage))
		}
	}
	prow.OverrideJobEnvironment(&pj.Spec, runImage, initialImage, targetRelease, namespace, variants)

	if job.Architecture == "arm64" {
		for i := range pj.Spec.PodSpec.Containers {
			c := &pj.Spec.PodSpec.Containers[i]
			exists := false
			for j := range c.Env {
				switch name := c.Env[j].Name; name {
				case "RELEASE_IMAGE_ARM64_LATEST":
					exists = true
					c.Env[j].Value = image
				}
			}
			if !exists {
				c.Env = append(c.Env, corev1.EnvVar{Name: "RELEASE_IMAGE_ARM64_LATEST", Value: image})
			}
		}
	}

	// find the ci-operator config for the job we will run
	sourceEnv, _, ok := firstEnvVar(pj.Spec.PodSpec, "CONFIG_SPEC")
	if !ok {
		sourceEnv, _, ok = firstEnvVar(pj.Spec.PodSpec, "UNRESOLVED_CONFIG")
		if !ok {
			return "", fmt.Errorf("UNRESOLVED_CONFIG or CONFIG_SPEC for the launch job could not be found in the prow job %s", job.JobName)
		}
	}

	clusterClient, err := getClusterClient(m, job)
	if err != nil {
		return "", err
	}

	sourceConfig, srcNamespace, srcName, err := loadJobConfigSpec(clusterClient.CoreClient, sourceEnv, "ci")
	if err != nil {
		return "", fmt.Errorf("the launch job definition could not be loaded: %v", err)
	}

	// Which target in the ci-operator config are we going to run?
	targetName := "launch"
	if job.LegacyConfig {
		if testName, ok := job.JobParams["test"]; ok {
			targetName = testName
		}
	} else {
		// errors should never occur here as this has already been checked by the calling function
		_, targetName, _ = configContainsVariant(job.JobParams, job.Platform, sourceEnv.Value, job.Mode)
	}

	var stepBasedTarget bool

	// For workflows, we configure the tests we run; for others, we need to load and modify the tests
	if job.Mode == JobTypeWorkflowLaunch || job.Mode == JobTypeWorkflowUpgrade {
		// use "launch" test name to identify proper cluster profile
		var profile citools.ClusterProfile
		for _, test := range sourceConfig.Tests {
			if test.As == "launch" {
				profile = test.MultiStageTestConfiguration.ClusterProfile
			}
		}
		environment := citools.TestEnvironment{}
		for name, value := range job.JobParams {
			environment[name] = value
		}
		test := citools.TestStepConfiguration{
			As: "launch",
			MultiStageTestConfiguration: &citools.MultiStageTestConfiguration{
				ClusterProfile: profile,
				Workflow:       &job.WorkflowName,
				Environment:    environment,
			},
		}
		if job.Mode == JobTypeWorkflowLaunch {
			waitRef := "clusterbot-wait"
			test.MultiStageTestConfiguration.Test = []citools.TestStep{{
				Reference: &waitRef,
			}}
		}
		baseImages := sourceConfig.BaseImages
		for imageName, imageDef := range m.workflowConfig.Workflows[job.WorkflowName].BaseImages {
			baseImages[imageName] = imageDef
		}
		sourceConfig.BaseImages = baseImages
		sourceConfig.Tests = []citools.TestStepConfiguration{test}
		stepBasedTarget = true
	} else {
		var matchedTarget *citools.TestStepConfiguration
		for _, test := range sourceConfig.Tests {
			if test.As == targetName {
				matchedTarget = &test
				break
			}
		}
		if matchedTarget == nil {
			return "", fmt.Errorf("no test definition matched the expected name %q", targetName)
		}
		commands := matchedTarget.Commands
		stepBasedTarget = len(commands) == 0 // if commands are specified, this is a template based target

		if !stepBasedTarget {
			// TODO: Remove once all launch jobs are step based
			prow.SetJobEnvVar(&pj.Spec, "TEST_COMMAND", commands)
		} else {
			envParams := sets.NewString()
			platformParams := multistageParamsForPlatform(job.Platform)
			for k := range job.JobParams {
				if platformParams.Has(k) {
					envParams.Insert(k)
				}
			}
			if len(envParams) != 0 {
				if matchedTarget.MultiStageTestConfiguration.Environment == nil {
					matchedTarget.MultiStageTestConfiguration.Environment = citools.TestEnvironment{}
				}
				for param := range envParams {
					envForParam := multistageParameters[param]
					matchedTarget.MultiStageTestConfiguration.Environment[envForParam.name] = envForParam.value
				}
			}
			if job.Mode == JobTypeTest {
				if strings.HasPrefix(targetName, "launch") {
					testStep := testStepForPlatform(job.Platform)
					matchedTarget.MultiStageTestConfiguration.Test = []citools.TestStep{{
						Reference: &testStep,
					}}
				}
				// CLUSTER_DURATION unused by tests; remove to prevent ci-operator from complaining
				delete(matchedTarget.MultiStageTestConfiguration.Environment, "CLUSTER_DURATION")
			}
			if job.Mode == JobTypeUpgrade {
				if matchedTarget.MultiStageTestConfiguration.Dependencies == nil {
					matchedTarget.MultiStageTestConfiguration.Dependencies = make(citools.TestDependencies)
				}
				matchedTarget.MultiStageTestConfiguration.Dependencies["OPENSHIFT_INSTALL_RELEASE_IMAGE_OVERRIDE"] = "release:initial"
				matchedTarget.MultiStageTestConfiguration.Dependencies["OPENSHIFT_UPGRADE_RELEASE_IMAGE_OVERRIDE"] = "release:latest"
			}
			if job.Mode == JobTypeTest || job.Mode == JobTypeUpgrade {
				if matchedTarget.MultiStageTestConfiguration.Environment == nil {
					matchedTarget.MultiStageTestConfiguration.Environment = make(citools.TestEnvironment)
				}
				if envs, ok := envsForTestType[job.JobParams["test"]]; ok {
					for _, env := range envs {
						matchedTarget.MultiStageTestConfiguration.Environment[env.name] = env.value
					}
				} else {
					return "", fmt.Errorf("unknown test type %s", job.JobParams["test"])
				}
			}
		}

		if targetName != "launch" {
			// launch jobs always target 'launch'. If we have selected a different target, rename
			// it in the configuration so that it will be run.
			matchedTarget.As = "launch"
			sourceConfig.Tests = []citools.TestStepConfiguration{*matchedTarget}
		}
	}

	// set releases field and unset tag_specification for all modern jobs
	if !job.LegacyConfig {
		sourceConfig.Releases = map[string]citools.UnresolvedRelease{
			"initial": {
				Integration: &citools.Integration{
					Name:      "ocp",
					Namespace: "$(BRANCH)",
				},
			},
			"latest": {
				Integration: &citools.Integration{
					Name:               "ocp",
					Namespace:          "$(BRANCH)",
					IncludeBuiltImages: true,
				},
			},
		}
		if job.Architecture == "arm64" {
			sourceConfig.Releases["arm64-latest"] = citools.UnresolvedRelease{
				// as this just gets overridden by the env var, the actual details here don't matter
				Candidate: &citools.Candidate{
					Architecture: "arm64",
					Product:      "ocp",
					Stream:       "nightly",
					Version:      "4.12",
				},
			}
		}
		sourceConfig.ReleaseTagConfiguration = nil
	}

	var hasRefs bool
	for _, input := range job.Inputs {
		if len(input.Refs) > 0 {
			hasRefs = true
		}
	}
	if hasRefs {
		launchDeadline += 30 * time.Minute

		// in order to build repos, we need to clone all the refs
		boolFalse := false
		pj.Spec.DecorationConfig.SkipCloning = &boolFalse

		clusterClient, err := getClusterClient(m, job)
		if err != nil {
			return "", err
		}

		is, err := clusterClient.TargetImageClient.ImageV1().ImageStreams("openshift").Get(context.TODO(), "cli", metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("unable to lookup registry URL for job")
		}
		registryHost := strings.SplitN(is.Status.PublicDockerImageRepository, "/", 2)[0]

		// NAMESPACE must be set for this job, and be in the first position, so remove it if set
		prow.RemoveJobEnvVar(&pj.Spec, "NAMESPACE")
		prow.SetJobEnvVar(&pj.Spec, "NAMESPACE", namespace)

		switch container := &pj.Spec.PodSpec.Containers[0]; {
		case reflect.DeepEqual(container.Command, []string{"ci-operator"}):
			var args []string
			for _, arg := range container.Args {
				if strings.HasPrefix(arg, "--namespace") {
					continue
				}
				args = append(args, arg)
			}
			args = append(args, fmt.Sprintf(`--namespace=$(NAMESPACE)`))

			envPrefix := strings.Join(restoreImageVariableScript, " ")
			container.Command = []string{"/bin/bash", "-c"}
			if job.Mode == "build" {
				container.Command = append(container.Command, fmt.Sprintf("registry_host=%s\n%s\n\n%s\n%s exec ci-operator $@", registryHost, script, permissionsScript, envPrefix), "")
			} else {
				container.Command = append(container.Command, fmt.Sprintf("registry_host=%s\n%s\n%s exec ci-operator $@", registryHost, script, envPrefix), "")
			}
			container.Args = args

			prow.SetJobEnvVar(&pj.Spec, "INITIAL", configInitial)
		default:
			return "", fmt.Errorf("the prow job %s does not have a recognizable command/args setup and cannot be used with pull request builds", job.JobName)
		}

		// For template based jobs, we must rely on "tag_specification"
		if job.LegacyConfig {
			sourceConfig.Releases = nil
			sourceConfig.ReleaseTagConfiguration = &citools.ReleaseTagConfiguration{
				Name:      "pipeline",
				Namespace: "$(NAMESPACE)",
			}
		} else {
			sourceConfig.ReleaseTagConfiguration = nil
			sourceConfig.Releases = map[string]citools.UnresolvedRelease{
				"initial": {
					Integration: &citools.Integration{
						Name:      "pipeline",
						Namespace: "$(NAMESPACE)",
					},
				},
				"latest": {
					Integration: &citools.Integration{
						Name:               "pipeline",
						Namespace:          "$(NAMESPACE)",
						IncludeBuiltImages: true,
					},
				},
			}
		}
		if len(sourceConfig.Tests) == 0 {
			sourceConfig.Tests = []citools.TestStepConfiguration{{
				As:       "none",
				Commands: "true",
				ContainerTestConfiguration: &citools.ContainerTestConfiguration{
					From: "src",
				},
			}}
		}

		index := 0
		for i, input := range job.Inputs {
			for _, ref := range input.Refs {
				configData, ok, err := m.configResolver.Resolve(ref.Org, ref.Repo, ref.BaseRef, "")
				if err != nil {
					return "", fmt.Errorf("could not resolve config for %s/%s/%s: %v", ref.Org, ref.Repo, ref.BaseRef, err)
				}
				if !ok {
					return "", fmt.Errorf("there is no defined configuration for the organization %s with repo %s and branch %s", ref.Org, ref.Repo, ref.BaseRef)
				}

				var cfg citools.ReleaseBuildConfiguration
				if err := yaml.Unmarshal([]byte(configData), &cfg); err != nil {
					return "", fmt.Errorf("unable to parse ci-operator config definition from resolver: %v", err)
				}
				targetConfig := &cfg
				if klog.V(2) {
					data, _ := json.MarshalIndent(targetConfig, "", "  ")
					klog.Infof("Found target job config:\n%s", string(data))
				}

				// delete sections we don't need
				targetConfig.Tests = nil

				if i == 0 && len(job.Inputs) > 1 {
					targetConfig.PromotionConfiguration = &citools.PromotionConfiguration{
						Name:              "stable-initial",
						Namespace:         "$(NAMESPACE)",
						RegistryOverride:  registryHost,
						DisableBuildCache: true,
					}
					// For template based jobs, we must rely on "tag_specification"
					if job.LegacyConfig {
						targetConfig.Releases = nil
						targetConfig.ReleaseTagConfiguration = &citools.ReleaseTagConfiguration{
							Name:      "stable-initial",
							Namespace: "$(NAMESPACE)",
						}
					} else {
						targetConfig.ReleaseTagConfiguration = nil
						targetConfig.Releases = map[string]citools.UnresolvedRelease{
							"initial": {
								Integration: &citools.Integration{
									Name:      "stable-initial",
									Namespace: "$(NAMESPACE)",
								},
							},
							"latest": {
								Integration: &citools.Integration{
									Name:               "stable-initial",
									Namespace:          "$(NAMESPACE)",
									IncludeBuiltImages: true,
								},
							},
						}
					}
				} else {
					targetConfig.PromotionConfiguration = &citools.PromotionConfiguration{
						Name:              "stable",
						Namespace:         "$(NAMESPACE)",
						RegistryOverride:  registryHost,
						DisableBuildCache: true,
					}
					// For template based jobs, we must rely on "tag_specification"
					if job.LegacyConfig {
						targetConfig.Releases = nil
						targetConfig.ReleaseTagConfiguration = &citools.ReleaseTagConfiguration{
							Name:      "stable",
							Namespace: "$(NAMESPACE)",
						}
					} else {
						targetConfig.Releases = map[string]citools.UnresolvedRelease{
							"initial": {
								Integration: &citools.Integration{
									Name:      "stable",
									Namespace: "$(NAMESPACE)",
								},
							},
							"latest": {
								Integration: &citools.Integration{
									Name:               "stable",
									Namespace:          "$(NAMESPACE)",
									IncludeBuiltImages: true,
								},
							},
						}
					}
				}

				data, err := json.MarshalIndent(targetConfig, "", "  ")
				if err != nil {
					return "", fmt.Errorf("unable to reformat child job for %#v: %v", ref, err)
				}
				prow.SetJobEnvVar(&pj.Spec, fmt.Sprintf("CONFIG_SPEC_%d", index), string(data))

				data, err = json.MarshalIndent(JobSpec{Refs: &ref}, "", "  ")
				if err != nil {
					return "", fmt.Errorf("unable to reformat child job for %#v: %v", ref, err)
				}
				prow.SetJobEnvVar(&pj.Spec, fmt.Sprintf("JOB_SPEC_%d", index), string(data))

				// this is used by ci-operator to resolve per repo configuration (which is cloned above)
				pathAlias := fmt.Sprintf("%d/github.com/%s/%s", index, ref.Org, ref.Repo)
				copiedRef := ref
				copiedRef.PathAlias = pathAlias
				pj.Spec.ExtraRefs = append(pj.Spec.ExtraRefs, copiedRef)
				prow.SetJobEnvVar(&pj.Spec, fmt.Sprintf("REPO_PATH_%d", index), pathAlias)

				index++
			}
		}

		if klog.V(2) {
			data, _ := json.MarshalIndent(pj.Spec, "", "  ")
			klog.Infof("Job config after override:\n%s", string(data))
		}
	}

	if stepBasedTarget {
		job.TargetType = "steps"
	} else {
		job.TargetType = "template"
	}
	pj.Annotations["ci-chat-bot.openshift.io/targetType"] = job.TargetType

	// build jobs do not launch contents
	if job.Mode == JobTypeBuild {
		if err := replaceTargetArgument(pj.Spec.PodSpec, "launch", "[release:latest]"); err != nil {
			return "", fmt.Errorf("unable to configure pod spec to alter launch target: %v", err)
		}
	}

	data, _ := json.MarshalIndent(sourceConfig, "", "  ")
	klog.V(2).Infof("Found target job config %s/%s:\n%s", srcNamespace, srcName, string(data))
	// Always use UNRESOLVED_CONFIG to support workflow-based runs
	prow.SetJobEnvVar(&pj.Spec, "UNRESOLVED_CONFIG", string(data))
	prow.RemoveJobEnvVar(&pj.Spec, "CONFIG_SPEC")

	if klog.V(2) {
		data, _ := json.MarshalIndent(pj, "", "  ")
		klog.Infof("Job %q will create prow job:\n%s", job.Name, string(data))
	}

	_, err = m.prowClient.Namespace(m.prowNamespace).Create(context.TODO(), prow.ObjectToUnstructured(pj), metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return "", err
	}

	var prowJobURL string
	// Wait for ProwJob URL to be assigned
	err = wait.PollImmediate(10*time.Second, 5*time.Minute, func() (bool, error) {
		uns, err := m.prowClient.Namespace(m.prowNamespace).Get(context.TODO(), job.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		var latestPJ prowapiv1.ProwJob
		if err := prow.UnstructuredToObject(uns, &latestPJ); err != nil {
			return false, err
		}

		if len(latestPJ.Status.URL) > 0 {
			prowJobURL = latestPJ.Status.URL
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return "", fmt.Errorf("did not retrieve job url due to an error: %v", err)
	}

	return prowJobURL, nil
}

func getClusterClient(m *jobManager, job *Job) (*BuildClusterClientConfig, error) {
	clusterClient, ok := m.clusterClients[job.BuildCluster]
	if !ok {
		return nil, fmt.Errorf("Cluster %s not found in %v", job.BuildCluster, m.clusterClients)
	}
	return clusterClient, nil
}

func createAccessRBAC(namespace string, user string, client ctrlruntimeclient.Client) error {
	roleBinding := &rbacapi.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-bot-user-access",
			Namespace: namespace,
		},
		Subjects: []rbacapi.Subject{{Kind: "User", Name: user}},
		RoleRef: rbacapi.RoleRef{
			Kind: "ClusterRole",
			Name: "admin",
		},
	}
	if err := client.Create(context.Background(), roleBinding); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (m *jobManager) setupAccessRBAC(job *Job, namespace string) {
	err := wait.ExponentialBackoff(wait.Backoff{Steps: 10, Duration: 2 * time.Second, Factor: 2}, func() (bool, error) {
		if job.Complete {
			return true, nil
		}
		clusterClient, err := getClusterClient(m, job)
		if err != nil {
			return false, err
		}
		if job.RequesterUserID != "" {
			client, err := ctrlruntimeclient.New(clusterClient.CoreConfig, ctrlruntimeclient.Options{})
			if err != nil {
				return false, err
			}
			if err := createAccessRBAC(namespace, job.RequesterUserID, client); err != nil {
				klog.Errorf("could not create role binding for %s: %v", job.RequestedBy, err)
				// the namespace might not yet exist when this step is executed
				// we want to retry if this step fails, hence the nil return
				return false, nil
			}
			klog.Infof("created the access RoleBinding for %s:", job.RequestedBy)
			return true, nil
		}
		return false, fmt.Errorf("failed to parse the RequesterUserID for %s", job.RequestedBy)
	})
	if err != nil {
		klog.Errorf("Failed to create the access role binding for %s: %v", job.RequestedBy, err)
	}
}

func (m *jobManager) waitForJob(job *Job) error {
	if job.IsComplete() && len(job.PasswordSnippet) > 0 {
		return nil
	}
	namespace := fmt.Sprintf("ci-ln-%s", namespaceSafeHash(job.Name))
	stepBasedMode := job.TargetType == "steps"

	// set up the access RBAC for the cluster Initiator
	go m.setupAccessRBAC(job, namespace)

	klog.Infof("Job %q started a prow job that will create pods in namespace %s", job.Name, namespace)
	var pj *prowapiv1.ProwJob
	err := wait.PollImmediate(10*time.Second, 15*time.Minute, func() (bool, error) {
		if m.jobIsComplete(job) {
			return false, errJobCompleted
		}
		uns, err := m.prowClient.Namespace(m.prowNamespace).Get(context.TODO(), job.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		var latestPJ prowapiv1.ProwJob
		if err := prow.UnstructuredToObject(uns, &latestPJ); err != nil {
			return false, err
		}
		pj = &latestPJ

		var done bool
		switch pj.Status.State {
		case prowapiv1.AbortedState, prowapiv1.ErrorState, prowapiv1.FailureState:
			job.Failure = "job failed"
			job.State = pj.Status.State
			done = true
		case prowapiv1.SuccessState:
			job.Failure = ""
			job.State = pj.Status.State
			done = true
		}
		if len(pj.Status.URL) > 0 {
			job.URL = pj.Status.URL
			done = true
		}
		return done, nil
	})
	if err != nil {
		return fmt.Errorf("did not retrieve job url due to an error: %v", err)
	}

	if job.IsComplete() {
		if value := pj.Annotations["ci-chat-bot.openshift.io/channel"]; len(value) > 0 {
			m.clearNotificationAnnotations(job, false, 0)
		}
		return nil
	}

	started := pj.Status.StartTime.Time

	// Some platforms take longer to set up
	setupContainerTimeout := 60 * time.Minute
	if job.Platform == "metal" {
		setupContainerTimeout = 90 * time.Minute
	}

	if job.Mode != JobTypeLaunch && job.Mode != JobTypeWorkflowLaunch {
		klog.Infof("Job %s will report results at %s (to %s / %s)", job.Name, job.URL, job.RequestedBy, job.RequestedChannel)

		// loop waiting for job to complete
		err = wait.PollImmediate(time.Minute, 5*setupContainerTimeout, func() (bool, error) {
			if m.jobIsComplete(job) {
				return false, errJobCompleted
			}
			uns, err := m.prowClient.Namespace(m.prowNamespace).Get(context.TODO(), job.Name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			var pj prowapiv1.ProwJob
			if err := prow.UnstructuredToObject(uns, &pj); err != nil {
				return false, err
			}
			switch pj.Status.State {
			case prowapiv1.AbortedState, prowapiv1.ErrorState, prowapiv1.FailureState:
				job.Failure = "job failed"
				job.State = pj.Status.State
				return true, nil
			case prowapiv1.SuccessState:
				job.Failure = ""
				job.State = pj.Status.State
				return true, nil
			}
			return false, nil
		})
		if err != nil {
			return fmt.Errorf("did not retrieve job completion state due to an error: %v", err)
		}

		m.clearNotificationAnnotations(job, false, 0)
		return nil
	}

	var targetName string
	switch job.Mode {
	case JobTypeBuild:
	default:
		targetName, err = findTargetName(pj.Spec.PodSpec)
		if err != nil {
			if klog.V(2) {
				data, _ := json.MarshalIndent(pj.Spec.PodSpec, "", "  ")
				klog.Infof("Could not find --target in:\n%s", string(data))
			}
			return err
		}
	}

	seen := false
	err = wait.PollImmediate(5*time.Second, 15*time.Minute, func() (bool, error) {
		if m.jobIsComplete(job) {
			return false, errJobCompleted
		}
		clusterClient, err := getClusterClient(m, job)
		if err != nil {
			return false, err
		}
		pod, err := clusterClient.CoreClient.CoreV1().Pods(m.prowNamespace).Get(context.TODO(), job.Name, metav1.GetOptions{})
		if err != nil {
			if !errors.IsNotFound(err) {
				return false, err
			}
			if seen {
				return false, fmt.Errorf("cluster has already been torn down")
			}
			return false, nil
		}
		seen = true
		if pod.Status.Phase == "Succeeded" || pod.Status.Phase == "Failed" {
			return false, fmt.Errorf("cluster has already been torn down")
		}
		return true, nil
	})
	if err != nil {
		if strings.HasPrefix(err.Error(), "cluster ") {
			return err
		}
		return fmt.Errorf("unable to check launch status: %v", err)
	}

	klog.Infof("Job %q waiting for setup container in pod %s to complete", job.Name, namespace)

	seen = false
	var lastErr error
	err = wait.PollImmediate(15*time.Second, setupContainerTimeout, func() (bool, error) {
		if m.jobIsComplete(job) {
			return false, errJobCompleted
		}

		clusterClient, err := getClusterClient(m, job)
		if err != nil {
			return false, err
		}
		if stepBasedMode {
			prowJobPod, err := clusterClient.CoreClient.CoreV1().Pods(m.prowNamespace).Get(context.TODO(), job.Name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			if prowJobPod.Status.Phase == "Succeeded" || prowJobPod.Status.Phase == "Failed" {
				return false, errJobCompleted
			}
			launchSecret, err := clusterClient.CoreClient.CoreV1().Secrets(namespace).Get(context.TODO(), targetName, metav1.GetOptions{})
			if err != nil {
				// It will take awhile before the secret is established and for the ci-chat-bot serviceaccount
				// to get permission to access it (see openshift-cluster-bot-rbac step). Ignore errors.
				return false, nil
			}
			if _, ok := launchSecret.Data["console.url"]; ok {
				// If the console.url is established, the cluster was setup.
				return true, nil
			}
			return false, nil
		} else {
			// Execute in template based mode where actual installation pod is monitored.
			pod, err := clusterClient.CoreClient.CoreV1().Pods(namespace).Get(context.TODO(), targetName, metav1.GetOptions{})
			if err != nil {
				// pod could not be created or we may not have permission yet
				if !errors.IsNotFound(err) && !errors.IsForbidden(err) {
					lastErr = err
					return false, err
				}
				if seen {
					return false, fmt.Errorf("cluster has already been torn down")
				}
				return false, nil
			}
			seen = true
			if pod.DeletionTimestamp != nil {
				return false, fmt.Errorf("cluster is being torn down")
			}
			if pod.Status.Phase == "Succeeded" || pod.Status.Phase == "Failed" {
				return false, fmt.Errorf("cluster has already been torn down")
			}
			ok, err := containerSuccessful(pod, "setup")
			if err != nil {
				return false, err
			}
			if containerTerminated(pod, "test") {
				return false, fmt.Errorf("cluster is shutting down")
			}
			return ok, nil
		}
	})
	if err != nil {
		if lastErr != nil && err == wait.ErrWaitTimeout {
			err = lastErr
		}
		if strings.HasPrefix(err.Error(), "cluster ") {
			return err
		}
		return fmt.Errorf("cluster never became available: %v", err)
	}

	var kubeconfig string
	clusterClient, err := getClusterClient(m, job)
	if err != nil {
		return err
	}

	if stepBasedMode {
		launchSecret, err := clusterClient.CoreClient.CoreV1().Secrets(namespace).Get(context.TODO(), targetName, metav1.GetOptions{})
		if err == nil {
			if content, ok := launchSecret.Data["kubeconfig"]; ok {
				kubeconfig = string(content)
			} else {
				klog.Errorf("job %q unable to find kubeconfig entry in step secret in %s/%s", job.Name, namespace, targetName)
				return fmt.Errorf("could not retrieve kubeconfig from pod %s/%s", namespace, targetName)
			}
		} else {
			klog.Errorf("job %q unable to access step secret in %s/%s", job.Name, namespace, targetName)
			return fmt.Errorf("could not retrieve kubeconfig from secret %s/%s: %v", namespace, targetName, err)
		}
	} else {
		klog.Infof("Job %q waiting for kubeconfig from pod %s/%s", job.Name, namespace, targetName)
		err = wait.PollImmediate(30*time.Second, 10*time.Minute, func() (bool, error) {
			if m.jobIsComplete(job) {
				return false, errJobCompleted
			}
			contents, err := commandContents(clusterClient.CoreClient.CoreV1(), clusterClient.CoreConfig, namespace, targetName, "test", []string{"cat", "/tmp/admin.kubeconfig"})
			if err != nil {
				if strings.Contains(err.Error(), "container not found") {
					// periodically check whether the still exists and is not succeeded or failed
					pod, err := clusterClient.CoreClient.CoreV1().Pods(namespace).Get(context.TODO(), targetName, metav1.GetOptions{})
					if errors.IsNotFound(err) || (pod != nil && (pod.Status.Phase == "Succeeded" || pod.Status.Phase == "Failed")) {
						return false, fmt.Errorf("pod cannot be found or has been deleted, assume cluster won't come up")
					}

					return false, nil
				}
				klog.Infof("Unable to retrieve config contents for %s/%s: %v", namespace, targetName, err)
				return false, nil
			}
			kubeconfig = contents
			return len(contents) > 0, nil
		})
		if err != nil {
			return fmt.Errorf("could not retrieve kubeconfig from pod %s/%s: %v", namespace, targetName, err)
		}
	}

	job.Credentials = kubeconfig

	// once the cluster is reachable, we're ok to send credentials
	// TODO: better criteria?
	var waitErr error
	if err := waitForClusterReachable(kubeconfig, func() bool { return m.jobIsComplete(job) }); err != nil {
		klog.Infof("error: Job %q failed waiting for cluster to become reachable in %s: %v", job.Name, namespace, err)
		job.Credentials = ""
		waitErr = fmt.Errorf("cluster did not become reachable: %v", err)
	}

	var kubeadminPassword string
	if stepBasedMode {
		launchSecret, err := clusterClient.CoreClient.CoreV1().Secrets(namespace).Get(context.TODO(), targetName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("unable to retrieve step secret %s/%s: %v", namespace, targetName, err)
		}
		if consoleURL, ok := launchSecret.Data["console.url"]; ok {
			job.PasswordSnippet = string(consoleURL)
		} else {
			return fmt.Errorf("unable to retrieve console.url from step secret %s/%s", namespace, targetName)
		}
		if password, ok := launchSecret.Data["kubeadmin-password"]; ok {
			kubeadminPassword = string(password)
		}
	} else {
		lines := int64(2)
		logs, err := clusterClient.CoreClient.CoreV1().Pods(namespace).GetLogs(targetName, &corev1.PodLogOptions{Container: "setup", TailLines: &lines}).DoRaw(context.TODO())
		if err != nil {
			klog.Infof("error: Job %q unable to get setup logs: %v", job.Name, err)
		}
		job.PasswordSnippet = strings.TrimSpace(reFixLines.ReplaceAllString(string(logs), "$1"))
		password, err := commandContents(clusterClient.CoreClient.CoreV1(), clusterClient.CoreConfig, namespace, targetName, "test", []string{"cat", "/tmp/artifacts/installer/auth/kubeadmin-password"})
		if err != nil {
			klog.Infof("error: Job %q unable to get kubeadmin password: %v", job.Name, err)
			password, err = commandContents(clusterClient.CoreClient.CoreV1(), clusterClient.CoreConfig, namespace, targetName, "test", []string{"cat", "/tmp/shared/installer/auth/kubeadmin-password"})
		}
		if err != nil {
			klog.Infof("error: Job %q unable to locate kubeadmin password: %v", job.Name, err)
		} else {
			kubeadminPassword = password
		}
	}

	if len(kubeadminPassword) > 0 {
		job.PasswordSnippet += fmt.Sprintf("\nLog in to the console with user `kubeadmin` and password `%s`", kubeadminPassword)
	} else {
		job.PasswordSnippet = fmt.Sprintf("\nError: Unable to retrieve kubeadmin password, you must use the kubeconfig file to access the cluster")
	}

	created := len(pj.Annotations["ci-chat-bot.openshift.io/expires"]) == 0
	startDuration := time.Now().Sub(started)
	m.clearNotificationAnnotations(job, created, startDuration)

	return waitErr
}

var reFixLines = regexp.MustCompile(`(?m)^level=info msg=\"(.*)\"$`)

// clearNotificationAnnotations removes the channel notification annotations in case we crash,
// so we don't attempt to redeliver, and set the best estimate we have of the expiration time if we created the cluster
func (m *jobManager) clearNotificationAnnotations(job *Job, created bool, startDuration time.Duration) {
	var patch []byte
	if created {
		patch = []byte(fmt.Sprintf(`{"metadata":{"annotations":{"ci-chat-bot.openshift.io/channel":"","ci-chat-bot.openshift.io/expires":"%d"}}}`, int(startDuration.Seconds()+m.maxAge.Seconds())))
	} else {
		patch = []byte(`{"metadata":{"annotations":{"ci-chat-bot.openshift.io/channel":""}}}`)
	}
	if _, err := m.prowClient.Namespace(m.prowNamespace).Patch(context.TODO(), job.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		klog.Infof("error: Job %q unable to clear channel annotation from prow job: %v", job.Name, err)
	}
}

// waitForClusterReachable performs a slow poll, waiting for the cluster to come alive.
// It returns an error if the cluster doesn't respond within the time limit.
func waitForClusterReachable(kubeconfig string, abortFn func() bool) error {
	cfg, err := loadKubeconfigContents(kubeconfig)
	if err != nil {
		return err
	}
	cfg.Timeout = 15 * time.Second
	client, err := clientset.NewForConfig(cfg)
	if err != nil {
		return err
	}

	return wait.PollImmediate(15*time.Second, 30*time.Minute, func() (bool, error) {
		if abortFn() {
			return false, errJobCompleted
		}
		_, err := client.CoreV1().Namespaces().Get(context.TODO(), "openshift-apiserver", metav1.GetOptions{})
		if err == nil {
			return true, nil
		}
		klog.Infof("cluster is not yet reachable %s: %v", cfg.Host, err)
		return false, nil
	})
}

// commandContents fetches the result of invoking a command in the provided container from stdout.
func commandContents(podClient coreclientset.CoreV1Interface, podRESTConfig *rest.Config, ns, name, containerName string, command []string) (string, error) {
	u := podClient.RESTClient().Post().Resource("pods").Namespace(ns).Name(name).SubResource("exec").VersionedParams(&corev1.PodExecOptions{
		Container: containerName,
		Stdout:    true,
		Stderr:    false,
		Command:   command,
	}, scheme.ParameterCodec).URL()

	e, err := remotecommand.NewSPDYExecutor(podRESTConfig, "POST", u)
	if err != nil {
		return "", fmt.Errorf("could not initialize a new SPDY executor: %v", err)
	}
	buf := &bytes.Buffer{}
	if err := e.Stream(remotecommand.StreamOptions{
		Stdout: buf,
		Stdin:  nil,
		Stderr: nil,
	}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// loadKubeconfig loads connection configuration
// for the cluster we're deploying to. We prefer to
// use in-cluster configuration if possible, but will
// fall back to using default rules otherwise.
func loadKubeconfig() (*rest.Config, string, bool, error) {
	cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{})
	clusterConfig, err := cfg.ClientConfig()
	if err != nil {
		return nil, "", false, fmt.Errorf("could not load client configuration: %v", err)
	}
	ns, isSet, err := cfg.Namespace()
	if err != nil {
		return nil, "", false, fmt.Errorf("could not load client namespace: %v", err)
	}
	return clusterConfig, ns, isSet, nil
}

func loadKubeconfigFromFlagOrDefault(path string, def *rest.Config) (*rest.Config, error) {
	if path == "" {
		return def, nil
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: path}, &clientcmd.ConfigOverrides{},
	).ClientConfig()
}

// loadKubeconfig loads connection configuration
// for the cluster we're deploying to. We prefer to
// use in-cluster configuration if possible, but will
// fall back to using default rules otherwise.
func loadKubeconfigContents(contents string) (*rest.Config, error) {
	cfg, err := clientcmd.NewClientConfigFromBytes([]byte(contents))
	if err != nil {
		return nil, fmt.Errorf("could not load client configuration: %v", err)
	}
	clusterConfig, err := cfg.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("could not load client configuration: %v", err)
	}
	return clusterConfig, nil
}

// oneWayEncoding can be used to encode hex to a 62-character set (0 and 1 are duplicates) for use in
// short display names that are safe for use in kubernetes as resource names.
var oneWayNameEncoding = base32.NewEncoding("bcdfghijklmnpqrstvwxyz0123456789").WithPadding(base32.NoPadding)

func namespaceSafeHash(values ...string) string {
	hash := sha256.New()

	// the inputs form a part of the hash
	for _, s := range values {
		hash.Write([]byte(s))
	}

	// Object names can't be too long so we truncate
	// the hash. This increases chances of collision
	// but we can tolerate it as our input space is
	// tiny.
	return oneWayNameEncoding.EncodeToString(hash.Sum(nil)[:4])
}

func containerSuccessful(pod *corev1.Pod, containerName string) (bool, error) {
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name != containerName {
			continue
		}
		if container.State.Terminated == nil {
			return false, nil
		}
		if container.State.Terminated.ExitCode == 0 {
			return true, nil
		}
		return false, fmt.Errorf("container %s did not succeed, see logs for details", containerName)
	}
	return false, nil
}

func containerTerminated(pod *corev1.Pod, containerName string) bool {
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name != containerName {
			continue
		}
		if container.State.Terminated != nil {
			return true
		}
	}
	return false
}

func errorAppliesToResource(err error, resource string) bool {
	apierr, ok := err.(errors.APIStatus)
	return ok && apierr.Status().Details != nil && apierr.Status().Details.Kind == resource
}

const configInitial = `
resources:
  '*':
    limits:
      memory: 4Gi
    requests:
      cpu: 100m
      memory: 200Mi
releases:
  initial:
    integration:
      name: "$(BRANCH)"
      namespace: ocp
  latest:
    integration:
      include_built_images: true
      name: "$(BRANCH)"
      namespace: ocp
tests:
- as: none
  commands: "true"
  container:
    from: src
`

const script = `set -euo pipefail

trap 'e=$!; jobs -p | xargs -r kill || true; exit $e' TERM EXIT

encoded_token="$( echo -n "serviceaccount:$( cat /var/run/secrets/kubernetes.io/serviceaccount/token )" | base64 -w 0 - )"
echo "{\"auths\":{\"${registry_host}\":{\"auth\":\"${encoded_token}\"}}}" > /tmp/push-auth

mkdir -p "$(ARTIFACTS)/initial" "$(ARTIFACTS)/final"

# HACK: clonerefs infers a directory from the refs provided to the prowjob, there's no way
# to override it outside the job today, so simply reset to the working dir
cd "/home/prow/go/src"
working_dir="$(pwd)"

targets=("--target=[release:latest]")
if [[ -z "${RELEASE_IMAGE_INITIAL-}" ]]; then
  unset RELEASE_IMAGE_INITIAL
else
  targets+=("--target=[release:initial]")
fi
if [[ -z "${RELEASE_IMAGE_LATEST-}" ]]; then
  unset RELEASE_IMAGE_LATEST
fi

# import the initial release, if any
UNRESOLVED_CONFIG=$INITIAL ARTIFACTS=$(ARTIFACTS)/initial ci-operator \
  --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson \
  --image-mirror-push-secret=/tmp/push-auth \
  --gcs-upload-secret=/secrets/gcs/service-account.json \
  --namespace=$(NAMESPACE) \
  --delete-when-idle=$(PRESERVE_DURATION) \
  --delete-after=$(DELETE_AFTER) \
  "${targets[@]}"

unset RELEASE_IMAGE_INITIAL
unset RELEASE_IMAGE_LATEST

# spawn one child ci-operator job per repo type
pids=()
for var in "${!CONFIG_SPEC_@}"; do
  suffix="${var/CONFIG_SPEC_/}"
  jobvar="JOB_SPEC_$suffix"
	srcpath="REPO_PATH_$suffix"
	srcpath="${working_dir}/${!srcpath}"
  mkdir -p "$(ARTIFACTS)/$suffix"
  (
    set +e
    echo "Starting $suffix:${srcpath} ..."
    if [[ -d "${srcpath}" ]]; then pushd "${srcpath}" >/dev/null; else echo "does not have a source directory ${srcpath}"; fi
    JOB_SPEC="${!jobvar}" ARTIFACTS=$(ARTIFACTS)/$suffix UNRESOLVED_CONFIG="${!var}" ci-operator \
      --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson \
      --image-mirror-push-secret=/tmp/push-auth \
      --gcs-upload-secret=/secrets/gcs/service-account.json \
      --namespace=$(NAMESPACE)-${suffix} \
      --target=[images] -promote >"$(ARTIFACTS)/$suffix/build.log" 2>&1
    code=$?
    cat "$(ARTIFACTS)/$suffix/build.log" 1>&2
    exit $code
  ) & pids+=($!)
done

# drain the job results
for i in ${pids[@]}; do if ! wait $i; then exit 1; fi; done
`

const permissionsScript = `
# prow doesn't allow init containers or a second container
export PATH=$PATH:/tmp/bin
mkdir /tmp/bin
curl -s https://mirror.openshift.com/pub/openshift-v4/clients/oc/4.4/linux/oc.tar.gz | tar xvzf - -C /tmp/bin/ oc
chmod ug+x /tmp/bin/oc

# grant all authenticated users access to the images in this namespace
oc policy add-role-to-group system:image-puller -n $(NAMESPACE) system:authenticated
`

type JobSpec struct {
	Refs *prowapiv1.Refs `json:"refs"`
}

func replaceTargetArgument(spec *corev1.PodSpec, from, to string) error {
	if spec == nil {
		return fmt.Errorf("prow job has no pod spec, cannot find target pod name")
	}
	for i, container := range spec.Containers {
		if container.Name != "" {
			continue
		}
		var updated []string
		for _, arg := range container.Args {
			if strings.HasPrefix(arg, "--target=") {
				arg = strings.TrimPrefix(arg, "--target=")
				if arg == from && len(to) > 0 {
					updated = append(updated, fmt.Sprintf("--target=%s", to))
				}
				continue
			}
			updated = append(updated, arg)
		}
		spec.Containers[i].Args = updated
	}
	return nil
}

// Returns the name of the --target in the ci-operator invocation.
func findTargetName(spec *corev1.PodSpec) (string, error) {
	if spec == nil {
		return "", fmt.Errorf("prow job has no pod spec, cannot find target pod name")
	}
	for _, container := range spec.Containers {
		if container.Name != "" {
			continue
		}
		for _, arg := range container.Args {
			if strings.HasPrefix(arg, "--target=") {
				value := strings.TrimPrefix(arg, "--target=")
				if len(value) > 0 {
					value := (&resolvedEnvironment{env: container.Env}).Resolve(value)
					if len(value) == 0 {
						return "", fmt.Errorf("bug in resolving %s", value)
					}
					return value, nil
				}
			}
		}
	}
	return "", fmt.Errorf("could not find argument --target=X in prow job pod spec to identify target pod name")
}

func ciOperatorConfigRefForRefs(refs *prowapiv1.Refs) *corev1.ConfigMapKeySelector {
	if refs == nil || len(refs.Org) == 0 || len(refs.Repo) == 0 {
		return nil
	}
	baseRef := refs.BaseRef
	if len(refs.BaseRef) == 0 {
		baseRef = "master"
	}
	keyName := fmt.Sprintf("%s-%s-%s.yaml", refs.Org, refs.Repo, baseRef)
	if m := reBranchVersion.FindStringSubmatch(baseRef); m != nil {
		return &corev1.ConfigMapKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: fmt.Sprintf("ci-operator-%s-configs", m[2]),
			},
			Key: keyName,
		}
	}
	return &corev1.ConfigMapKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{
			Name: fmt.Sprintf("ci-operator-%s-configs", baseRef),
		},
		Key: keyName,
	}
}

func loadJobConfigSpec(client clientset.Interface, env corev1.EnvVar, namespace string) (*citools.ReleaseBuildConfiguration, string, string, error) {
	if len(env.Value) > 0 {
		var cfg citools.ReleaseBuildConfiguration
		if err := yaml.Unmarshal([]byte(env.Value), &cfg); err != nil {
			return nil, "", "", fmt.Errorf("unable to parse ci-operator config definition: %v", err)
		}
		return &cfg, "", "", nil
	}
	if env.ValueFrom == nil {
		return &citools.ReleaseBuildConfiguration{}, "", "", nil
	}
	if env.ValueFrom.ConfigMapKeyRef == nil {
		return nil, "", "", fmt.Errorf("only config spec values inline or referenced in config maps may be used")
	}
	configMap, keyName := env.ValueFrom.ConfigMapKeyRef.Name, env.ValueFrom.ConfigMapKeyRef.Key
	cm, err := client.CoreV1().ConfigMaps(namespace).Get(context.TODO(), configMap, metav1.GetOptions{})
	if err != nil {
		return nil, "", "", fmt.Errorf("unable to identify a ci-operator configuration for the provided refs: %v", err)
	}
	configData, ok := cm.Data[keyName]
	if !ok {
		return nil, "", "", fmt.Errorf("no ci-operator config was found in config map %s/%s with key %s", namespace, configMap, keyName)
	}
	var cfg citools.ReleaseBuildConfiguration
	if err := yaml.Unmarshal([]byte(configData), &cfg); err != nil {
		return nil, "", "", fmt.Errorf("unable to parse ci-operator config definition from %s/%s[%s]: %v", namespace, configMap, keyName, err)
	}
	return &cfg, namespace, configMap, nil
}

func firstEnvVar(spec *corev1.PodSpec, name string) (corev1.EnvVar, *corev1.Container, bool) {
	for i, container := range spec.InitContainers {
		for j, env := range container.Env {
			if env.Name == name {
				env.Value = (&resolvedEnvironment{env: container.Env[:j]}).Resolve(env.Value)
				return env, &spec.InitContainers[i], true
			}
		}
	}
	for i, container := range spec.Containers {
		for j, env := range container.Env {
			if env.Name == name {
				env.Value = (&resolvedEnvironment{env: container.Env[:j]}).Resolve(env.Value)
				return env, &spec.Containers[i], true
			}
		}
	}
	return corev1.EnvVar{}, nil, false
}

var reEnvSubstitute = regexp.MustCompile(`$([a-zA-Z0-9_]+)`)

type resolvedEnvironment struct {
	env    []corev1.EnvVar
	cached map[string]string
}

func (e *resolvedEnvironment) Resolve(value string) string {
	return reEnvSubstitute.ReplaceAllStringFunc(value, func(s string) string {
		name := s[2 : len(s)-1]
		if value, ok := e.cached[name]; ok {
			return value
		}
		return e.Lookup(s)
	})
}

func (e *resolvedEnvironment) Lookup(name string) string {
	if value, ok := e.cached[name]; ok {
		return value
	}
	for i, env := range e.env {
		if env.Name != name {
			continue
		}
		if env.ValueFrom != nil {
			return ""
		}
		if !strings.Contains(env.Value, "$(") {
			return env.Value
		}
		if e.cached == nil {
			e.cached = make(map[string]string)
		}
		value := (&resolvedEnvironment{cached: e.cached, env: e.env[:i]}).Resolve(env.Value)
		e.cached[name] = value
	}
	return ""
}
