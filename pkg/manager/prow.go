package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"io"
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

	"github.com/openshift/ci-chat-bot/pkg/catalog"
	"github.com/openshift/ci-chat-bot/pkg/prow"
	"github.com/openshift/ci-chat-bot/pkg/utils"

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
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/transport"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowapiv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	citools "github.com/openshift/ci-tools/pkg/api"
)

var errJobCompleted = fmt.Errorf("job is complete")

// SupportedTests lists any of the suites defined in the standard launch jobs (copied here for user friendliness)
var SupportedTests = []string{"e2e", "e2e-serial", "e2e-all", "e2e-disruptive", "e2e-disruptive-all", "e2e-builds", "e2e-image-ecosystem", "e2e-image-registry", "e2e-network-stress"}

// SupportedUpgradeTests lists any of the upgrade suites defined in the standard launch jobs (copied here for user friendliness)
var SupportedUpgradeTests = []string{"e2e-upgrade", "e2e-upgrade-all", "e2e-upgrade-partial", "e2e-upgrade-rollback"}

// SupportedPlatforms requires a job within the release periodics that can launch a
// cluster that has the label job-env: platform-name.
var SupportedPlatforms = []string{"aws", "gcp", "azure", "vsphere", "metal", "ovirt", "openstack", "hypershift-hosted", "nutanix", "alibaba", "hypershift-hosted-powervs", "azure-stackhub"}

// SupportedParameters are the allowed parameter keys that can be passed to jobs
var SupportedParameters = []string{"ovn", "ovn-hybrid", "proxy", "compact", "fips", "mirror", "shared-vpc", "large", "xlarge", "ipv4", "ipv6", "dualstack", "dualstack-primaryv6", "preserve-bootstrap", "test", "rt", "single-node", "cgroupsv2", "techpreview", "upi", "crun", "nfv", "kuryr", "sdn", "no-spot", "no-capabilities", "virtualization-support", "multi-zone", "multi-zone-techpreview", "bundle", "private"}

// MultistageParameters is the mapping of SupportedParameters that can be configured via multistage parameters to the correct environment variable format
var MultistageParameters = map[string]EnvVar{
	"compact": {
		name:      "SIZE_VARIANT",
		value:     "compact",
		Platforms: sets.New[string]("aws", "aws-2", "gcp", "azure"),
	},
	"large": {
		name:      "SIZE_VARIANT",
		value:     "large",
		Platforms: sets.New[string]("aws", "aws-2", "gcp", "azure"),
	},
	"xlarge": {
		name:      "SIZE_VARIANT",
		value:     "xlarge",
		Platforms: sets.New[string]("aws", "aws-2", "gcp", "azure"),
	},
	"preserve-bootstrap": {
		name:      "OPENSHIFT_INSTALL_PRESERVE_BOOTSTRAP",
		value:     "true",
		Platforms: sets.New[string]("aws", "aws-2", "gcp", "azure", "vsphere", "ovirt", "nutanix"),
	},
}

// envsForTestType maps tests given by users to corresponding parameters/env vars that need to be set on the test. Currently not platform dependent, so the Platforms
// list for the envs will be left blank
var envsForTestType = map[string][]EnvVar{
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

// SupportedArchitectures are the allowed architectures that can be passed to jobs
var SupportedArchitectures = []string{"amd64", "arm64", "multi"}

var (
	// reVersion detects whether a version appears to correlate to a major.minor release
	reVersion = regexp.MustCompile(`^(\d+\.\d+)`)
)

func testStepForPlatform(platform string) string {
	switch platform {
	case "aws", "aws-2", "gcp", "azure", "vsphere", "ovirt", "openstack", "nutanix", "alibaba":
		return "openshift-e2e-test"
	case "metal":
		return "baremetalds-e2e-test"
	default:
		// currently hypershift has no workflows that do any tests, so we can't override a launch job for e2e tests
		return ""
	}
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
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, false, fmt.Errorf("url resolve failed to read body: %v", err)
			}
			return data, true, nil
		case 404:
			return nil, false, nil
		default:
			data, _ := io.ReadAll(resp.Body)
			return nil, false, fmt.Errorf("url resolve failed with status code %d: %s", resp.StatusCode, string(bytes.TrimSpace(data)))
		}
	case "file":
		filename := fmt.Sprintf("%s-%s-%s", org, repo, branch)
		if len(variant) > 0 {
			filename = fmt.Sprintf("%s_%s", filename, variant)
		}
		path := filepath.Join(r.URL.Path, org, repo, filename+".yaml")
		klog.V(2).Infof("Attempting to read config from %s", path)
		data, err := os.ReadFile(path)
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
	pj, err := m.prowClient.ProwJobs(m.prowNamespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// There may have been an issue creating the prowjob; treat as success
			return nil
		}
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
	_, err = m.prowClient.ProwJobs(m.prowNamespace).Update(context.Background(), pj, metav1.UpdateOptions{})
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
			utils.LaunchLabel: "true",

			"prow.k8s.io/type": string(pj.Spec.Type),
			"prow.k8s.io/job":  pj.Spec.Job,
		},
	}
	if job.ManagedClusterName != "" {
		pj.Annotations["ci-chat-bot.openshift.io/managedClusterName"] = job.ManagedClusterName
	}

	// sort the variant inputs
	var variants []string
	for k := range job.JobParams {
		if utils.Contains(SupportedParameters, k) {
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
	if job.Mode == JobTypeBuild || job.Mode == JobTypeCatalog {
		// keep the built payload images around for a week
		prow.SetJobEnvVar(&pj.Spec, "PRESERVE_DURATION", "168h")
		prow.SetJobEnvVar(&pj.Spec, "DELETE_AFTER", "168h")
	} else if job.Mode == JobTypeMCECustomImage {
		// set preserve long enough for 2 install attempts of an mce cluster
		prow.SetJobEnvVar(&pj.Spec, "PRESERVE_DURATION", "2h")
		prow.SetJobEnvVar(&pj.Spec, "DELETE_AFTER", "12h")
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
	version := job.Inputs[lastJobInput].Version
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

	if len(image) > 0 && len(version) == 0 && len(runImage) == 0 {
		prow.OverrideJobEnvironment(&pj.Spec, image, initialImage, targetRelease, namespace, variants)
	} else {
		prow.OverrideJobEnvironment(&pj.Spec, runImage, initialImage, targetRelease, namespace, variants)
	}

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

	// errors should never occur here as this has already been checked by the calling function
	_, targetName, _ := configContainsVariant(job.JobParams, job.Platform, sourceEnv.Value, job.Mode)

	// For workflows, we configure the tests we run; for others, we need to load and modify the tests
	if job.Mode == JobTypeWorkflowLaunch || job.Mode == JobTypeWorkflowUpgrade || job.Mode == JobTypeWorkflowTest {
		// use "launch" test name to identify proper cluster profile
		var profile citools.ClusterProfile
		var leases []citools.StepLease
		for _, test := range sourceConfig.Tests {
			if test.As == "launch" {
				profile = test.MultiStageTestConfiguration.ClusterProfile
				leases = test.MultiStageTestConfiguration.Leases
				break
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
				Leases:         leases,
			},
		}
		if job.Mode == JobTypeWorkflowLaunch {
			waitRef := "clusterbot-wait"
			test.MultiStageTestConfiguration.Test = []citools.TestStep{{
				Reference: &waitRef,
			}}
		}

		baseImages := sourceConfig.BaseImages
		if sourceConfig.BaseImages == nil {
			baseImages = make(map[string]citools.ImageStreamTagReference, 0)
		}
		for imageName, imageDef := range m.workflowConfig.Workflows[job.WorkflowName].BaseImages {
			baseImages[imageName] = imageDef
		}
		sourceConfig.BaseImages = baseImages
		sourceConfig.Tests = []citools.TestStepConfiguration{test}
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
		if UseSpotInstances(job) {
			matchedTarget.MultiStageTestConfiguration.Environment["SPOT_INSTANCES"] = "true"
		}
		envParams := sets.New[string]()
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
				envForParam := MultistageParameters[param]
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
		if job.Mode == JobTypeMCECustomImage {
			// only run the rbac configuration step for mce custom images
			installRBACStep := "ipi-install-rbac"
			matchedTarget.MultiStageTestConfiguration.Test = []citools.TestStep{{
				Reference: &installRBACStep,
			}}
			matchedTarget.MultiStageTestConfiguration.Pre = nil
			matchedTarget.MultiStageTestConfiguration.Post = nil
			matchedTarget.MultiStageTestConfiguration.Workflow = nil
			matchedTarget.MultiStageTestConfiguration.Environment = nil
		}

		if targetName != "launch" {
			// launch jobs always target 'launch'. If we have selected a different target, rename
			// it in the configuration so that it will be run.
			matchedTarget.As = "launch"
			sourceConfig.Tests = []citools.TestStepConfiguration{*matchedTarget}
		}
	}

	// if a step based config, launch should now be the test config we will run; time to update the config for lease balancing
	if job.UseSecondaryAccount {
		switch job.Platform {
		case "aws":
			if err := convertAWSToAWS2(pj, sourceConfig); err != nil {
				return "", fmt.Errorf("failed updating aws job to aws-2: %w", err)
			}
		case "gcp":
			if err := convertGCPToGCP2(pj, sourceConfig); err != nil {
				return "", fmt.Errorf("failed updating gcp job to gcp-openshift-gce-devel-ci-2: %w", err)
			}
		case "azure":
			if err := convertAzureToAzure2(pj, sourceConfig); err != nil {
				return "", fmt.Errorf("failed updating azure job to azure-2: %w", err)
			}
		}
	}

	// set releases field and unset tag_specification for all jobs
	sourceConfig.Releases = map[string]citools.UnresolvedRelease{
		"initial": {
			Integration: &citools.Integration{
				Name:      "$(BRANCH)",
				Namespace: "ocp",
			},
		},
		"latest": {
			Integration: &citools.Integration{
				Name:               "$(BRANCH)",
				Namespace:          "ocp",
				IncludeBuiltImages: true,
			},
		},
	}
	if job.Architecture == "arm64" {
		sourceConfig.Releases["arm64-latest"] = citools.UnresolvedRelease{
			// as this just gets overridden by the env var, the actual details here don't matter
			Candidate: &citools.Candidate{
				ReleaseDescriptor: citools.ReleaseDescriptor{
					Architecture: "arm64",
					Product:      "ocp",
				},
				Stream:  "nightly",
				Version: "4.18",
			},
		}
	}
	sourceConfig.ReleaseTagConfiguration = nil

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
			args = append(args, `--namespace=$(NAMESPACE)`)

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
		// we can only support one operator bundle build, so we need to know if other bundles may be declared here
		operatorRepo := ""
		if sourceConfig.BaseImages == nil {
			sourceConfig.BaseImages = make(map[string]citools.ImageStreamTagReference)
		}
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
				if klog.V(4) {
					data, _ := json.MarshalIndent(targetConfig, "", "  ")
					klog.Infof("Found target job config:\n%s", string(data))
				}

				newOperatorRepo, err := processOperatorPR(operatorRepo, sourceConfig, targetConfig, job, &ref, pj)
				if err != nil {
					return "", err
				}
				if newOperatorRepo != "" {
					operatorRepo = newOperatorRepo
				}

				// delete sections we don't need
				targetConfig.Tests = nil
				// since this run of ci-operator is being run separate of the run doing the install, it does not
				// have a full graph of dependencies. This can cause optional images that are needed to not be built.
				// This simplest way to handle this is to just override the optional field for all images
				updatedImageList := []citools.ProjectDirectoryImageBuildStepConfiguration{}
				for _, image := range targetConfig.Images {
					newImage := image
					newImage.Optional = false
					updatedImageList = append(updatedImageList, newImage)
					// if a job is building an operator, images built from other repos may be an operand,
					// and thus need to be accessible as a pipeline image for the bundle build
					sourceConfig.BaseImages[string(image.To)] = citools.ImageStreamTagReference{
						Name:      "stable",
						Tag:       string(image.To),
						Namespace: "$(NAMESPACE)",
					}
				}
				targetConfig.Images = updatedImageList

				if i == 0 && len(job.Inputs) > 1 {
					targetConfig.PromotionConfiguration = &citools.PromotionConfiguration{
						Targets: []citools.PromotionTarget{
							{
								Name:      "stable-initial",
								Namespace: "$(NAMESPACE)",
							},
						},
						RegistryOverride:  registryHost,
						DisableBuildCache: true,
					}
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
				} else {
					targetConfig.PromotionConfiguration = &citools.PromotionConfiguration{
						Targets: []citools.PromotionTarget{
							{
								Name:      "stable",
								Namespace: "$(NAMESPACE)",
							},
						},
						RegistryOverride:  registryHost,
						DisableBuildCache: true,
					}
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
				if newOperatorRepo != "" {
					// when the job spec is patched to only include the operator extra_ref, we need
					// the imported git repo to be in the correct location (i.e. index 0), so we
					// need to prepend this ref instead of append
					pj.Spec.ExtraRefs = append([]prowapiv1.Refs{copiedRef}, pj.Spec.ExtraRefs...)
					operatorData, err := json.MarshalIndent(JobSpec{ExtraRefs: []prowapiv1.Refs{copiedRef}}, "", "  ")
					if err != nil {
						return "", fmt.Errorf("unable to make operator specific extra refs for %#v: %v", ref, err)
					}
					prow.SetJobEnvVar(&pj.Spec, "OPERATOR_REFS", string(operatorData))
				} else {
					pj.Spec.ExtraRefs = append(pj.Spec.ExtraRefs, copiedRef)
				}
				prow.SetJobEnvVar(&pj.Spec, fmt.Sprintf("REPO_PATH_%d", index), pathAlias)

				index++
			}
		}

		if klog.V(2) {
			data, _ := json.MarshalIndent(pj.Spec, "", "  ")
			klog.Infof("Job config after override:\n%s", string(data))
		}
	}

	// error if bundle name is provided but no operator repo PR was provided
	if bundleName, ok := job.JobParams["bundle"]; ok && !job.Operator.Is {
		return "", fmt.Errorf("bundle name %s provided, but no PR for an operator repo provided", bundleName)
	}

	// build jobs do not launch contents
	if job.Mode == JobTypeBuild {
		if err := replaceTargetArgument(pj.Spec.PodSpec, "launch", "[release:latest]"); err != nil {
			return "", fmt.Errorf("unable to configure pod spec to alter launch target: %v", err)
		}
	} else if job.Mode == JobTypeCatalog {
		if err := replaceTargetArgument(pj.Spec.PodSpec, "launch", job.JobParams["bundle"]); err != nil {
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

	_, err = m.prowClient.ProwJobs(m.prowNamespace).Create(context.TODO(), pj, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return "", err
	}

	// TODO: Any errors returned after this point need to make sure that they are properly handled by the enclosing logic calling newJob()
	var prowJobURL string
	// Wait for ProwJob URL to be assigned
	err = wait.PollUntilContextTimeout(context.TODO(), 10*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		latestPJ, err := m.prowClient.ProwJobs(m.prowNamespace).Get(context.TODO(), job.Name, metav1.GetOptions{})
		if err != nil {
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

func processOperatorPR(oldOperatorRepo string, sourceConfig, targetConfig *citools.ReleaseBuildConfiguration, job *Job, ref *prowapiv1.Refs, pj *prowapiv1.ProwJob) (string, error) {
	newOperatorRepo := ""
	if targetConfig.Operator != nil && len(targetConfig.Operator.Bundles) > 0 {
		if job.Operator.Is {
			if oldOperatorRepo != fmt.Sprintf("%s/%s", ref.Org, ref.Repo) {
				return "", fmt.Errorf("multiple operator sources were configured; this is not currently supported")
			}
		} else {
			newOperatorRepo = fmt.Sprintf("%s/%s", ref.Org, ref.Repo)
			job.Operator.Is = true
			pj.Annotations["ci-chat-bot.openshift.io/IsOperator"] = "true"
			sourceConfig.Operator = targetConfig.Operator
			// find test definition referencing the bundle for dependencies and environment
			var environment []citools.StepParameter
			var indexName string
			if bundleName, ok := job.JobParams["bundle"]; ok {
				indexName = fmt.Sprintf("ci-index-%s", bundleName)
				job.Operator.BundleName = bundleName
			} else {
				// if no bundle name is provided, default to first bundle in list; unnamed bundles only support the index method
				indexName = "ci-index"
				if targetConfig.Operator.Bundles[0].As != "" {
					indexName = fmt.Sprintf("ci-index-%s", targetConfig.Operator.Bundles[0].As)
					job.Operator.BundleName = targetConfig.Operator.Bundles[0].As
					pj.Annotations["ci-chat-bot.openshift.io/OperatorBundleName"] = job.Operator.BundleName
				}
			}
			var foundTest, hasIndex bool
			var bundleStep citools.LiteralTestStep
		TestLoop:
			for _, test := range targetConfig.Tests {
				// since we retrieved the config from the resolver, the tests are fully resolved "literal" configurations
				if test.MultiStageTestConfigurationLiteral != nil {
					// fully resolved configs more all dependency and environment declarations to the step instead of global setting
					for _, step := range test.MultiStageTestConfigurationLiteral.Pre {
						// dependency naming for index based operator tests is deterministic,
						// so we can use that to search for the correct test
						for _, env := range step.Dependencies {
							if env.Env == "OO_INDEX" && env.Name == indexName {
								environment = step.Environment
								foundTest = true
								hasIndex = true
								break TestLoop
							}
						}
					}
					for _, step := range test.MultiStageTestConfigurationLiteral.Test {
						// non-index based operator tests must have a step called "install" in the test section
						// with an OO_BUNDLE dependency to be identified by ci-chat-bot
						if step.As != "install" {
							continue
						}
						for _, env := range step.Dependencies {
							if env.Env == "OO_BUNDLE" {
								foundTest = true
								bundleStep = step
								break TestLoop
							}
						}
					}
				}
			}
			if !foundTest {
				bundleName := job.Operator.BundleName
				if bundleName == "" {
					bundleName = "unnamed"
				}
				return "", fmt.Errorf("unable to locate test using %s bundle", bundleName)
			}
			for index, test := range sourceConfig.Tests {
				if test.As == "launch" {
					if sourceConfig.Tests[index].MultiStageTestConfiguration.Environment == nil {
						sourceConfig.Tests[index].MultiStageTestConfiguration.Environment = make(citools.TestEnvironment)
					}
					for _, env := range environment {
						sourceConfig.Tests[index].MultiStageTestConfiguration.Environment[env.Name] = *env.Default
					}
					if sourceConfig.Tests[index].MultiStageTestConfiguration.Dependencies == nil {
						sourceConfig.Tests[index].MultiStageTestConfiguration.Dependencies = make(citools.TestDependencies)
					}
					if hasIndex {
						sourceConfig.Tests[index].MultiStageTestConfiguration.Dependencies["OO_INDEX"] = indexName
						if len(test.MultiStageTestConfiguration.Test) == 0 {
							testStep := testStepForPlatform(job.Platform)
							test.MultiStageTestConfiguration.Test = []citools.TestStep{{Reference: &testStep}}
						}
						subscribe := "optional-operators-subscribe"
						test.MultiStageTestConfiguration.Test = append([]citools.TestStep{{Reference: &subscribe}}, test.MultiStageTestConfiguration.Test...)
						job.Operator.HasIndex = true
						pj.Annotations["ci-chat-bot.openshift.io/OperatorHasIndex"] = "true"
					} else {
						// if foundTest && !hasIndex, then bundleStep must be set
						test.MultiStageTestConfiguration.Test = append([]citools.TestStep{{LiteralTestStep: &bundleStep}, {LiteralTestStep: &operatorInstallCompleteStep}}, test.MultiStageTestConfiguration.Test...)
					}
				}
			}
			// specify root
			sourceConfig.BuildRootImage = targetConfig.BuildRootImage
			// specify base_images
			if len(targetConfig.BaseImages) > 0 && sourceConfig.BaseImages == nil {
				sourceConfig.BaseImages = make(map[string]citools.ImageStreamTagReference)
			}
			for name, ref := range targetConfig.BaseImages {
				if _, ok := sourceConfig.BaseImages[name]; !ok {
					sourceConfig.BaseImages[name] = ref
				}
			}
			// the `operator` stanza needs the built images to be in `pipeline`. This is a hacky way to acheieve that
			for _, image := range targetConfig.Images {
				if sourceConfig.BaseImages == nil {
					sourceConfig.BaseImages = make(map[string]citools.ImageStreamTagReference)
				}
				sourceConfig.BaseImages[string(image.To)] = citools.ImageStreamTagReference{
					Name:      "stable",
					Tag:       string(image.To),
					Namespace: "$(NAMESPACE)",
				}
			}
			sourceConfig.Metadata = targetConfig.Metadata
		}
	}
	return newOperatorRepo, nil
}

func getClusterClient(m *jobManager, job *Job) (*utils.BuildClusterClientConfig, error) {
	clusterClient, ok := m.clusterClients[job.BuildCluster]
	if !ok {
		return nil, fmt.Errorf("Cluster %s not found in %v", job.BuildCluster, m.clusterClients)
	}
	return clusterClient, nil
}

func createBotCatalogRBAC(namespace string, client ctrlruntimeclient.Client) error {
	roleBinding := &rbacapi.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-bot-catalog-access",
			Namespace: namespace,
		},
		Subjects: []rbacapi.Subject{{Kind: "ServiceAccount", Name: "ci-chat-bot", Namespace: "ci"}},
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

	// set up the access RBAC for the cluster Initiator
	go m.setupAccessRBAC(job, namespace)

	klog.Infof("Job %q started a prow job that will create pods in namespace %s", job.Name, namespace)
	var pj *prowapiv1.ProwJob
	err := wait.PollUntilContextTimeout(context.TODO(), 10*time.Second, 15*time.Minute, true, func(ctx context.Context) (bool, error) {
		if m.jobIsComplete(job) {
			return false, errJobCompleted
		}
		latest, err := m.prowClient.ProwJobs(m.prowNamespace).Get(context.TODO(), job.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		pj = latest

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

	// Some Platforms take longer to set up
	setupContainerTimeout := 60 * time.Minute
	if job.Platform == "metal" {
		setupContainerTimeout = 90 * time.Minute
	} else if job.Operator.Is {
		setupContainerTimeout = 105 * time.Minute
	}

	if job.Mode != JobTypeLaunch && job.Mode != JobTypeWorkflowLaunch {
		klog.Infof("Job %s will report results at %s (to %s / %s)", job.Name, job.URL, job.RequestedBy, job.RequestedChannel)

		// loop waiting for job to complete
		err = wait.PollUntilContextTimeout(context.TODO(), time.Minute, 5*setupContainerTimeout, true, func(ctx context.Context) (bool, error) {
			if m.jobIsComplete(job) {
				return false, errJobCompleted
			}
			pj, err := m.prowClient.ProwJobs(m.prowNamespace).Get(context.TODO(), job.Name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			switch pj.Status.State {
			case prowapiv1.AbortedState, prowapiv1.ErrorState, prowapiv1.FailureState:
				job.Failure = "job failed"
				job.State = pj.Status.State
				if job.Mode == JobTypeCatalog {
					job.CatalogComplete = true
				}
				return true, nil
			case prowapiv1.SuccessState:
				job.Failure = ""
				job.State = pj.Status.State
				if job.Mode == JobTypeCatalog {
					clusterClient, err := getClusterClient(m, job)
					if err != nil {
						return false, err
					}
					// give the bot admin access to create resources
					rbacClient, err := ctrlruntimeclient.New(clusterClient.CoreConfig, ctrlruntimeclient.Options{})
					if err != nil {
						return false, err
					}
					if err := createBotCatalogRBAC(namespace, rbacClient); err != nil {
						return false, err
					}
					fullBundlePullRef := fmt.Sprintf("registry.%s.ci.openshift.org/%s/pipeline:%s", job.BuildCluster, namespace, job.Operator.BundleName)
					klog.Infof("Creating bundle for %s", fullBundlePullRef)
					newErr := catalog.CreateBuild(context.TODO(), *clusterClient, fullBundlePullRef, namespace)
					job.CatalogComplete = true
					if newErr != nil {
						job.CatalogError = true
						return false, newErr
					}
				}
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
	case JobTypeCatalog:
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
	err = wait.PollUntilContextTimeout(context.TODO(), 5*time.Second, 15*time.Minute, true, func(ctx context.Context) (bool, error) {
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
	err = wait.PollUntilContextTimeout(context.TODO(), 15*time.Second, setupContainerTimeout, true, func(ctx context.Context) (bool, error) {
		if m.jobIsComplete(job) {
			return false, errJobCompleted
		}

		clusterClient, err := getClusterClient(m, job)
		if err != nil {
			return false, err
		}
		prowJobPod, err := clusterClient.CoreClient.CoreV1().Pods(m.prowNamespace).Get(context.TODO(), job.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if prowJobPod.Status.Phase == "Succeeded" || prowJobPod.Status.Phase == "Failed" {
			return false, errJobCompleted
		}
		secretDir, err := clusterClient.CoreClient.CoreV1().Secrets(namespace).Get(context.TODO(), targetName, metav1.GetOptions{})
		if err != nil {
			// It will take awhile before the secret is established and for the ci-chat-bot serviceaccount
			// to get permission to access it (see openshift-cluster-bot-rbac step). Ignore errors.
			return false, nil
		}
		if _, ok := secretDir.Data["console.url"]; ok {
			// If the console.url is established, the cluster was setup.
			if job.Operator.Is {
				if job.Operator.HasIndex {
					if _, ok := secretDir.Data["oo_deployment_details.yaml"]; ok {
						return true, nil
					}
				} else {
					if _, ok := secretDir.Data["operator_complete.txt"]; ok {
						return true, nil
					}
				}
			} else {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
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

	secretDir, err := clusterClient.CoreClient.CoreV1().Secrets(namespace).Get(context.TODO(), targetName, metav1.GetOptions{})
	if err == nil {
		if content, ok := secretDir.Data["kubeconfig"]; ok {
			kubeconfig = string(content)
		} else {
			klog.Errorf("job %q unable to find kubeconfig entry in step secret in %s/%s", job.Name, namespace, targetName)
			return fmt.Errorf("could not retrieve kubeconfig from pod %s/%s", namespace, targetName)
		}
	} else {
		klog.Errorf("job %q unable to access step secret in %s/%s", job.Name, namespace, targetName)
		return fmt.Errorf("could not retrieve kubeconfig from secret %s/%s: %v", namespace, targetName, err)
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
	var operatorDeploymentInfo string
	if consoleURL, ok := secretDir.Data["console.url"]; ok {
		job.PasswordSnippet = string(consoleURL)
	} else {
		return fmt.Errorf("unable to retrieve console.url from step secret %s/%s", namespace, targetName)
	}
	if password, ok := secretDir.Data["kubeadmin-password"]; ok {
		kubeadminPassword = strings.ReplaceAll(string(password), "\n", "")
	}
	if deployment, ok := secretDir.Data["oo_deployment_details.yaml"]; ok {
		operatorDeploymentInfo = string(deployment)
	}
	if len(kubeadminPassword) > 0 {
		job.PasswordSnippet += fmt.Sprintf("\nLog in to the console with user `kubeadmin` and password `%s`", kubeadminPassword)
		if len(operatorDeploymentInfo) > 0 {
			job.PasswordSnippet += fmt.Sprintf("\nThis following is the deployment information for you operator:\n%s", operatorDeploymentInfo)
		}
	} else {
		job.PasswordSnippet = "\nError: Unable to retrieve kubeadmin password, you must use the kubeconfig file to access the cluster"
	}

	created := len(pj.Annotations["ci-chat-bot.openshift.io/expires"]) == 0
	startDuration := time.Since(started)
	m.clearNotificationAnnotations(job, created, startDuration)

	return waitErr
}

// clearNotificationAnnotations removes the channel notification annotations in case we crash,
// so we don't attempt to redeliver, and set the best estimate we have of the expiration time if we created the cluster
func (m *jobManager) clearNotificationAnnotations(job *Job, created bool, startDuration time.Duration) {
	var patch []byte
	if created {
		patch = []byte(fmt.Sprintf(`{"metadata":{"annotations":{"ci-chat-bot.openshift.io/channel":"","ci-chat-bot.openshift.io/expires":"%d"}}}`, int(startDuration.Seconds()+m.maxAge.Seconds())))
	} else {
		patch = []byte(`{"metadata":{"annotations":{"ci-chat-bot.openshift.io/channel":""}}}`)
	}
	if _, err := m.prowClient.ProwJobs(m.prowNamespace).Patch(context.TODO(), job.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
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

	return wait.PollUntilContextTimeout(context.TODO(), 15*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
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

// LoadKubeconfig loads connection configuration
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

// this step runs after non-index operators to indicate completion of operator install before the clusterbot-wait step
var operatorInstallCompleteStep = citools.LiteralTestStep{
	As:       "chat-bot-operator-complete",
	From:     "pipeline:src",
	Commands: "echo 'complete' > ${SHARED_DIR}/operator_complete.txt",
	Resources: citools.ResourceRequirements{
		Requests: citools.ResourceList{
			"cpu":    "100m",
			"memory": "200Mi",
		},
	},
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
initial_dir=$(pwd)
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
cd ${initial_dir}
# update job spec with operator ref changes if needed
if [ -n "${OPERATOR_REFS-}" ]; then
	curl -s -L https://github.com/jqlang/jq/releases/download/jq-1.7.1/jq-linux-$(uname -m | sed 's/aarch64/arm64/;s/x86_64/amd64/') -o jq
	chmod +x jq
	JOB_SPEC=$(( echo $JOB_SPEC ; echo $OPERATOR_REFS ) | ./jq -s add)
fi
`

const permissionsScript = `
# prow doesn't allow init containers or a second container
export PATH=$PATH:/tmp/bin
mkdir /tmp/bin
curl -sL https://mirror.openshift.com/pub/openshift-v4/$(uname -m | sed 's/aarch64/arm64/;s/x86_64/amd64/')/clients/ocp/4.17.0/openshift-client-linux.tar.gz | tar xvzf - -C /tmp/bin/ oc
chmod ug+x /tmp/bin/oc

# grant all authenticated users access to the images in this namespace
oc policy add-role-to-group system:image-puller -n $(NAMESPACE) system:authenticated
oc policy add-role-to-group system:image-puller -n $(NAMESPACE) system:unauthenticated
`

type JobSpec struct {
	ExtraRefs []prowapiv1.Refs `json:"extra_refs,omitempty"`
	Refs      *prowapiv1.Refs  `json:"refs,omitempty"`
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

func convertToAccount2(job *prowapiv1.ProwJob, sourceConfig *citools.ReleaseBuildConfiguration, profileName, profileSecret, profileConfigMap, accountDomain string) error {
	job.Annotations["ci-operator.openshift.io/cloud-cluster-profile"] = profileName
	for index, volume := range job.Spec.PodSpec.Volumes {
		if volume.Name == "cluster-profile" {
			if volume.Projected == nil {
				volume.Projected = &corev1.ProjectedVolumeSource{}
			}
			volume.Projected.Sources = []corev1.VolumeProjection{{Secret: &corev1.SecretProjection{LocalObjectReference: corev1.LocalObjectReference{Name: profileSecret}}}}
			// gcp-2 has both a secret and a configmap for some reason...
			if profileConfigMap != "" {
				volume.Projected.Sources = append(volume.Projected.Sources, corev1.VolumeProjection{ConfigMap: &corev1.ConfigMapProjection{LocalObjectReference: corev1.LocalObjectReference{Name: profileConfigMap}}})
			}
			job.Spec.PodSpec.Volumes[index] = volume
		}
	}
	var matchedTarget *citools.TestStepConfiguration
	for _, test := range sourceConfig.Tests {
		if test.As == "launch" {
			matchedTarget = &test
			break
		}
	}
	if matchedTarget == nil {
		return fmt.Errorf("invalid job; could not find `launch` test")
	}
	if matchedTarget.MultiStageTestConfiguration == nil {
		return fmt.Errorf("invalid job; `launch` test is not a multistage test")
	}
	matchedTarget.MultiStageTestConfiguration.ClusterProfile = citools.ClusterProfile(profileName)
	if accountDomain != "" && matchedTarget.MultiStageTestConfiguration != nil && matchedTarget.MultiStageTestConfiguration.Environment != nil {
		matchedTarget.MultiStageTestConfiguration.Environment["BASE_DOMAIN"] = accountDomain
	}
	return nil
}

func convertAWSToAWS2(job *prowapiv1.ProwJob, sourceConfig *citools.ReleaseBuildConfiguration) error {
	return convertToAccount2(job, sourceConfig, "aws-2", "cluster-secrets-aws-2", "", "aws-2.ci.openshift.org")
}

func convertAzureToAzure2(job *prowapiv1.ProwJob, sourceConfig *citools.ReleaseBuildConfiguration) error {
	return convertToAccount2(job, sourceConfig, "azure-2", "cluster-secrets-azure-2", "", "ci2.azure.devcluster.openshift.com")
}

func convertGCPToGCP2(job *prowapiv1.ProwJob, sourceConfig *citools.ReleaseBuildConfiguration) error {
	return convertToAccount2(job, sourceConfig, "gcp-openshift-gce-devel-ci-2", "cluster-secrets-gcp-openshift-gce-devel-ci-2", "cluster-profile-gcp-openshift-gce-devel-ci-2", "")
}
