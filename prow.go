package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/openshift/ci-chat-bot/pkg/prow"
	prowapiv1 "github.com/openshift/ci-chat-bot/pkg/prow/apiv1"
)

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

func loadJobConfigSpec(client clientset.Interface, env corev1.EnvVar, namespace string) (*unstructured.Unstructured, error) {
	if len(env.Value) > 0 {
		var cfg unstructured.Unstructured
		if err := yaml.Unmarshal([]byte(env.Value), &cfg.Object); err != nil {
			return nil, fmt.Errorf("unable to parse ci-operator config definition: %v", err)
		}
		return &cfg, nil
	}
	if env.ValueFrom == nil {
		return &unstructured.Unstructured{}, nil
	}
	if env.ValueFrom.ConfigMapKeyRef == nil {
		return nil, fmt.Errorf("only config spec values inline or referenced in config maps may be used")
	}
	configMap, keyName := env.ValueFrom.ConfigMapKeyRef.Name, env.ValueFrom.ConfigMapKeyRef.Key
	cm, err := client.CoreV1().ConfigMaps(namespace).Get(configMap, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to identify a ci-operator configuration for the provided refs: %v", err)
	}
	configData, ok := cm.Data[keyName]
	if !ok {
		return nil, fmt.Errorf("no ci-operator config was found in config map %s/%s with key %s", namespace, configMap, keyName)
	}
	var cfg unstructured.Unstructured
	if err := yaml.Unmarshal([]byte(configData), &cfg.Object); err != nil {
		return nil, fmt.Errorf("unable to parse ci-operator config definition from %s/%s[%s]: %v", namespace, configMap, keyName, err)
	}
	return &cfg, nil
}

func firstEnvVar(spec *corev1.PodSpec, name string) (*corev1.EnvVar, *corev1.Container) {
	for i, container := range spec.InitContainers {
		for j := range container.Env {
			env := &container.Env[j]
			if env.Name == name {
				return env, &spec.InitContainers[i]
			}
		}
	}
	for i, container := range spec.Containers {
		for j := range container.Env {
			env := &container.Env[j]
			if env.Name == name {
				return env, &spec.Containers[i]
			}
		}
	}
	return nil, nil
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

// stopJob triggers namespace deletion, which will cause graceful shutdown of the
// cluster. If this method returns nil, it is safe to consider the cluster released.
func (m *jobManager) stopJob(name string) error {
	namespace := fmt.Sprintf("ci-ln-%s", namespaceSafeHash(name))
	if err := m.coreClient.CoreV1().Namespaces().Delete(namespace, nil); err != nil && !errors.IsNotFound(err) {
		return err
	}
	return nil
}

// launchJob creates a ProwJob and watches its status as it goes.
// This is a long running function but should also be reentrant.
func (m *jobManager) launchJob(job *Job) error {
	launchDeadline := 45 * time.Minute

	if len(job.Credentials) > 0 && len(job.PasswordSnippet) > 0 {
		return nil
	}

	namespace := fmt.Sprintf("ci-ln-%s", namespaceSafeHash(job.Name))
	// launch a prow job, tied back to this cluster user
	pj, err := prow.JobForConfig(m.prowConfigLoader, job.JobName)
	if err != nil {
		return err
	}

	targetPodName, err := findTargetName(pj.Spec.PodSpec)
	if err != nil {
		return err
	}

	pj.ObjectMeta = metav1.ObjectMeta{
		Name:      job.Name,
		Namespace: m.prowNamespace,
		Annotations: map[string]string{
			"ci-chat-bot.openshift.io/mode":           job.Mode,
			"ci-chat-bot.openshift.io/user":           job.RequestedBy,
			"ci-chat-bot.openshift.io/channel":        job.RequestedChannel,
			"ci-chat-bot.openshift.io/ns":             namespace,
			"ci-chat-bot.openshift.io/releaseVersion": job.InstallVersion,
			"ci-chat-bot.openshift.io/upgradeVersion": job.UpgradeVersion,
			"ci-chat-bot.openshift.io/releaseImage":   job.InstallImage,
			"ci-chat-bot.openshift.io/upgradeImage":   job.UpgradeImage,

			"prow.k8s.io/job": pj.Spec.Job,
		},
		Labels: map[string]string{
			"ci-chat-bot.openshift.io/launch": "true",

			"prow.k8s.io/type": string(pj.Spec.Type),
			"prow.k8s.io/job":  pj.Spec.Job,
		},
	}

	// if the user specified a set of Git coordinates to build from, load the appropriate ci-operator
	// configuration and inject the appropriate launch job
	if refs := job.InstallRefs; refs != nil {
		// budget additional 45m to build anything we need
		launchDeadline += 45 * time.Minute

		data, _ := json.Marshal(refs)
		pj.Annotations["ci-chat-bot.openshift.io/releaseRefs"] = string(data)
		// TODO: calculate this from the destination cluster
		job.InstallImage = fmt.Sprintf("registry.svc.ci.openshift.org/%s/release:latest", namespace)

		// find the ci-operator config for the job we will run
		sourceEnv, _ := firstEnvVar(pj.Spec.PodSpec, "CONFIG_SPEC")
		if sourceEnv == nil {
			return fmt.Errorf("the CONFIG_SPEC for the launch job could not be found in the prow job")
		}
		sourceConfig, err := loadJobConfigSpec(m.coreClient, *sourceEnv, "ci")
		if err != nil {
			return fmt.Errorf("the launch job definition could not be loaded: %v", err)
		}

		// find the ci-operator config for the repo of the PR
		configMapSelector := ciOperatorConfigRefForRefs(refs)
		if configMapSelector == nil {
			return fmt.Errorf("unable to identify a ci-operator configuration for the provided refs: %#v", refs)
		}
		targetConfig, err := loadJobConfigSpec(m.coreClient, corev1.EnvVar{ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: configMapSelector}}, "ci")
		if err != nil {
			return fmt.Errorf("unable to load ci-operator configuration for the provided refs: %v", err)
		}

		// check if the referenced PR repo already defines a test for our given cluster type (i.e. launch-aws)
		tests, _, err := unstructured.NestedSlice(targetConfig.Object, "tests")
		if err != nil {
			return fmt.Errorf("invalid ci-operator config definition %v: 'tests' is not a slice", configMapSelector)
		}
		var newTests []interface{}
		for _, test := range tests {
			obj, ok := test.(map[string]interface{})
			if !ok {
				continue
			}
			if as, _, _ := unstructured.NestedString(obj, "as"); as == targetPodName {
				newTests = append(newTests, test)
			}
		}
		if len(newTests) > 0 {
			targetConfig.Object["tests"] = newTests
		} else {
			targetConfig.Object["tests"] = sourceConfig.Object["tests"]
		}
		jobConfigData, err := yaml.Marshal(targetConfig.Object)
		if err != nil {
			return fmt.Errorf("unable to update ci-operator config definition: %v", err)
		}
		// log.Printf("running against %#v with:\n%s", refs, jobConfigData)
		prow.OverrideJobConfig(&pj.Spec, refs, string(jobConfigData))
	}

	// register annotations the release controller can use to assess the success
	// of this job if it is upgrading between two edges
	if job.InstallRefs == nil && len(job.InstallVersion) > 0 && len(job.UpgradeVersion) > 0 {
		pj.Labels["release.openshift.io/verify"] = "true"
		pj.Annotations["release.openshift.io/from-tag"] = job.InstallVersion
		pj.Annotations["release.openshift.io/tag"] = job.UpgradeVersion
	}

	pj.Annotations["ci-chat-bot.openshift.io/expires"] = strconv.Itoa(int(m.maxAge.Seconds() + launchDeadline.Seconds()))
	prow.OverrideJobEnvVar(&pj.Spec, "CLUSTER_DURATION", strconv.Itoa(int(m.maxAge.Seconds())))

	image := job.InstallImage
	var initialImage string
	if len(job.UpgradeImage) > 0 {
		initialImage = image
		image = job.UpgradeImage
	}
	prow.OverrideJobEnvironment(&pj.Spec, image, initialImage, namespace)

	created := true
	started := time.Now()
	_, err = m.prowClient.Namespace(m.prowNamespace).Create(prow.ObjectToUnstructured(pj), metav1.CreateOptions{})
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}
		created = false
	}

	log.Printf("prow job %s launched to target namespace %s", job.Name, namespace)
	err = wait.PollImmediate(10*time.Second, 15*time.Minute, func() (bool, error) {
		uns, err := m.prowClient.Namespace(m.prowNamespace).Get(job.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		var pj prowapiv1.ProwJob
		if err := prow.UnstructuredToObject(uns, &pj); err != nil {
			return false, err
		}
		if len(pj.Status.URL) > 0 {
			job.URL = pj.Status.URL
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("did not retrieve job url due to an error: %v", err)
	}

	if job.Mode != "launch" {
		return nil
	}

	seen := false
	err = wait.PollImmediate(5*time.Second, 15*time.Minute, func() (bool, error) {
		pod, err := m.coreClient.Core().Pods(m.prowNamespace).Get(job.Name, metav1.GetOptions{})
		if err != nil {
			if !errors.IsNotFound(err) {
				return false, err
			}
			if seen {
				return false, fmt.Errorf("pod was deleted")
			}
			return false, nil
		}
		seen = true
		if pod.Status.Phase == "Succeeded" || pod.Status.Phase == "Failed" {
			return false, fmt.Errorf("pod has already exited")
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("unable to check launch status: %v", err)
	}

	log.Printf("waiting for setup container in pod %s/%s to complete", namespace, targetPodName)

	seen = false
	var lastErr error
	err = wait.PollImmediate(5*time.Second, launchDeadline, func() (bool, error) {
		pod, err := m.coreClient.Core().Pods(namespace).Get(targetPodName, metav1.GetOptions{})
		if err != nil {
			// pod could not be created or we may not have permission yet
			if !errors.IsNotFound(err) && !errors.IsForbidden(err) {
				lastErr = err
				return false, err
			}
			if seen {
				return false, fmt.Errorf("pod was deleted")
			}
			return false, nil
		}
		seen = true
		if pod.Status.Phase == "Succeeded" || pod.Status.Phase == "Failed" {
			return false, fmt.Errorf("pod has already exited")
		}
		ok, err := containerSuccessful(pod, "setup")
		if err != nil {
			return false, err
		}
		return ok, nil
	})
	if err != nil {
		if lastErr != nil && err == wait.ErrWaitTimeout {
			err = lastErr
		}
		return fmt.Errorf("pod never became available: %v", err)
	}

	startDuration := time.Now().Sub(started)
	if created {
		job.StartDuration = startDuration
		job.ExpiresAt = started.Add(startDuration + m.maxAge)
		log.Printf("cluster took %s from start to become available", startDuration)
	}

	log.Printf("trying to grab the kubeconfig from launched pod %s/%s", namespace, targetPodName)

	var kubeconfig string
	err = wait.PollImmediate(30*time.Second, 10*time.Minute, func() (bool, error) {
		contents, err := commandContents(m.coreClient.Core(), m.coreConfig, namespace, targetPodName, "test", []string{"cat", "/tmp/admin.kubeconfig"})
		if err != nil {
			if strings.Contains(err.Error(), "container not found") {
				// periodically check whether the still exists and is not succeeded or failed
				pod, err := m.coreClient.Core().Pods(namespace).Get(targetPodName, metav1.GetOptions{})
				if errors.IsNotFound(err) || (pod != nil && (pod.Status.Phase == "Succeeded" || pod.Status.Phase == "Failed")) {
					return false, fmt.Errorf("pod cannot be found or has been deleted, assume cluster won't come up")
				}

				return false, nil
			}
			log.Printf("Unable to retrieve config contents for %s/%s: %v", namespace, targetPodName, err)
			return false, nil
		}
		kubeconfig = contents
		return len(contents) > 0, nil
	})
	if err != nil {
		return fmt.Errorf("could not retrieve kubeconfig from pod %s/%s: %v", namespace, targetPodName, err)
	}

	job.Credentials = kubeconfig

	// once the cluster is reachable, we're ok to send credentials
	// TODO: better criteria?
	var waitErr error
	if err := waitForClusterReachable(kubeconfig); err != nil {
		log.Printf("error: unable to wait for the cluster %s/%s to start: %v", namespace, targetPodName, err)
		job.Credentials = ""
		waitErr = fmt.Errorf("cluster did not become reachable: %v", err)
	}

	lines := int64(2)
	logs, err := m.coreClient.Core().Pods(namespace).GetLogs(targetPodName, &corev1.PodLogOptions{Container: "setup", TailLines: &lines}).DoRaw()
	if err != nil {
		log.Printf("error: unable to get setup logs")
	}
	job.PasswordSnippet = reFixLines.ReplaceAllString(string(logs), "$1")

	// clear the channel notification in case we crash so we don't attempt to redeliver, and set the best
	// estimate we have of the expiration time if we created the cluster
	var patch []byte
	if created {
		patch = []byte(fmt.Sprintf(`{"metadata":{"annotations":{"ci-chat-bot.openshift.io/channel":"","ci-chat-bot.openshift.io/expires":"%d"}}}`, int(startDuration.Seconds()+m.maxAge.Seconds())))
	} else {
		patch = []byte(`{"metadata":{"annotations":{"ci-chat-bot.openshift.io/channel":""}}}`)
	}
	if _, err := m.prowClient.Namespace(m.prowNamespace).Patch(job.Name, types.MergePatchType, patch, metav1.UpdateOptions{}); err != nil {
		log.Printf("error: unable to clear channel annotation from prow job: %v", err)
	}

	return waitErr
}

var reFixLines = regexp.MustCompile(`(?m)^level=info msg=\"(.*)\"$`)

// waitForClusterReachable performs a slow poll, waiting for the cluster to come alive.
// It returns an error if the cluster doesn't respond within the time limit.
func waitForClusterReachable(kubeconfig string) error {
	cfg, err := loadKubeconfigContents(kubeconfig)
	if err != nil {
		return err
	}
	cfg.Timeout = 15 * time.Second
	client, err := clientset.NewForConfig(cfg)
	if err != nil {
		return err
	}

	return wait.PollImmediate(15*time.Second, 20*time.Minute, func() (bool, error) {
		_, err := client.Core().Namespaces().Get("openshift-apiserver", metav1.GetOptions{})
		if err == nil {
			return true, nil
		}
		log.Printf("cluster is not yet reachable %s: %v", cfg.Host, err)
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
