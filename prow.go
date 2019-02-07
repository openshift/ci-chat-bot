package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/openshift/ci-chat-bot/pkg/prow"
)

// launchCluster creates a ProwJob and watches its status as it goes.
// This is a long running function but should also be reentrant.
func (m *clusterManager) launchCluster(cluster *Cluster) error {
	targetPodName := "release-launch-aws"
	namespace := fmt.Sprintf("ci-ln-%s", namespaceSafeHash(cluster.Name))
	// launch a prow job, tied back to this cluster user
	job, err := prow.JobForConfig(m.prowConfigLoader, m.prowJobName)
	if err != nil {
		return err
	}
	job.ObjectMeta = metav1.ObjectMeta{
		Name:      cluster.Name,
		Namespace: m.prowNamespace,
		Annotations: map[string]string{
			"ci-chat-bot.openshift.io/user":         cluster.RequestedBy,
			"ci-chat-bot.openshift.io/channel":      cluster.RequestedChannel,
			"ci-chat-bot.openshift.io/ns":           namespace,
			"ci-chat-bot.openshift.io/releaseImage": cluster.ReleaseImage,

			"prow.k8s.io/job": job.Spec.Job,
		},
		Labels: map[string]string{
			"ci-chat-bot.openshift.io/launch": "true",

			"prow.k8s.io/type": string(job.Spec.Type),
			"prow.k8s.io/job":  job.Spec.Job,
		},
	}
	prow.OverrideJobEnvironment(&job.Spec, cluster.ReleaseImage, namespace)
	_, err = m.prowClient.Namespace(m.prowNamespace).Create(prow.ObjectToUnstructured(job), metav1.CreateOptions{})
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}
	}

	log.Printf("prow job %s launched to target namespace %s", job.Name, namespace)

	seen := false
	err = wait.PollImmediate(5*time.Second, 15*time.Minute, func() (bool, error) {
		pod, err := m.coreClient.Core().Pods(m.prowNamespace).Get(cluster.Name, metav1.GetOptions{})
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
	err = wait.PollImmediate(5*time.Second, 45*time.Minute, func() (bool, error) {
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

	log.Printf("trying to grab the kubeconfig from launched pod")

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
			log.Printf("Unable to retrieve config contents: %v", err)
			return false, nil
		}
		kubeconfig = contents
		return len(contents) > 0, nil
	})
	if err != nil {
		return fmt.Errorf("could not retrieve kubeconfig from pod: %v", err)
	}

	cluster.Credentials = kubeconfig

	// once the cluster is reachable, we're ok to send credentials
	// TODO: better criteria?
	var waitErr error
	if err := waitForClusterReachable(kubeconfig); err != nil {
		log.Printf("error: unable to wait for the cluster to start: %v", err)
		cluster.Credentials = ""
		waitErr = fmt.Errorf("cluster did not become reachable: %v", err)
	}

	lines := int64(2)
	logs, err := m.coreClient.Core().Pods(namespace).GetLogs(targetPodName, &corev1.PodLogOptions{Container: "setup", TailLines: &lines}).DoRaw()
	if err != nil {
		log.Printf("error: unable to get setup logs")
	}
	cluster.PasswordSnippet = reFixLines.ReplaceAllString(string(logs), "$1")

	// clear the channel notification in case we crash so we don't attempt to redeliver
	patch := []byte(`{"metadata":{"annotations":{"ci-chat-bot.openshift.io/channel":""}}}`)
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
		return container.State.Terminated.ExitCode == 0, nil
	}
	return false, nil
}
