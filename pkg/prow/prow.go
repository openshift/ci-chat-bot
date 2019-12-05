package prow

import (
	"bytes"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"

	prowapiv1 "github.com/openshift/ci-chat-bot/pkg/prow/apiv1"
)

type ProwConfigLoader interface {
	Config() *prowapiv1.Config
}

func JobForLabels(prowConfigLoader ProwConfigLoader, selector labels.Selector) (*prowapiv1.ProwJob, error) {
	config := prowConfigLoader.Config()
	if config == nil {
		return nil, fmt.Errorf("cannot locate prow job: no prow jobs have been defined")
	}
	periodicConfig, ok := prowapiv1.HasProwJobWithLabels(config, selector)
	if !ok {
		return nil, fmt.Errorf("no prow job matches the label selector %s", selector.String())
	}

	spec := prowapiv1.ProwSpecForPeriodicConfig(periodicConfig)

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
	config := prowConfigLoader.Config()
	if config == nil {
		return nil, fmt.Errorf("the prow job %s is not valid: no prow jobs have been defined", jobName)
	}
	periodicConfig, ok := prowapiv1.HasProwJob(config, jobName)
	if !ok {
		return nil, fmt.Errorf("the prow job %s is not valid: no job with that name", jobName)
	}

	spec := prowapiv1.ProwSpecForPeriodicConfig(periodicConfig)

	pj := &prowapiv1.ProwJob{
		TypeMeta: metav1.TypeMeta{APIVersion: "prow.k8s.io/v1", Kind: "ProwJob"},
		Spec:     *spec,
		Status: prowapiv1.ProwJobStatus{
			StartTime: metav1.Now(),
			State:     prowapiv1.TriggeredState,
		},
	}
	pj = pj.DeepCopy()
	return pj, nil
}

func ObjectToUnstructured(obj runtime.Object) *unstructured.Unstructured {
	buf := &bytes.Buffer{}
	if err := unstructured.UnstructuredJSONScheme.Encode(obj, buf); err != nil {
		panic(err)
	}
	u := &unstructured.Unstructured{}
	if _, _, err := unstructured.UnstructuredJSONScheme.Decode(buf.Bytes(), nil, u); err != nil {
		panic(err)
	}
	return u
}

func UnstructuredToObject(in runtime.Unstructured, out runtime.Object) error {
	return runtime.DefaultUnstructuredConverter.FromUnstructured(in.UnstructuredContent(), out)
}

func OverrideJobEnvironment(spec *prowapiv1.ProwJobSpec, image, initialImage, namespace string, variants []string) {
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
			}
		}
	}
}

func contains(slice []string, value string) bool {
	for _, s := range slice {
		if s == value {
			return true
		}
	}
	return false
}

func RemoveEnvVar(c *corev1.Container, names ...string) {
	for i, env := range c.Env {
		if !contains(names, env.Name) {
			continue
		}

		removed := make([]corev1.EnvVar, 0, len(c.Env))
		removed = append(removed, c.Env[:i]...)
		for _, env := range c.Env[i+1:] {
			if contains(names, env.Name) {
				continue
			}
			removed = append(removed, env)
		}
		c.Env = removed
		return
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

func OverrideJobConfig(spec *prowapiv1.ProwJobSpec, refs *prowapiv1.Refs, value string, installImage string) {
	spec.Refs = refs

	for i := range spec.PodSpec.Containers {
		c := &spec.PodSpec.Containers[i]
		var clearImages bool
		var hasInitialImage bool
		for j := range c.Env {
			switch c.Env[j].Name {
			case "CONFIG_SPEC":
				clearImages = true
				c.Env[j].Value = value
				c.Env[j].ValueFrom = nil
			case "RELEASE_IMAGE_INITIAL":
				hasInitialImage = true
			}
		}
		if clearImages {
			RemoveEnvVar(c, "RELEASE_IMAGE_INITIAL", "RELEASE_IMAGE_LATEST")
		}
		if hasInitialImage && len(installImage) > 0 {
			c.Env = append(c.Env, corev1.EnvVar{Name: "RELEASE_IMAGE_INITIAL", Value: installImage})
		}
	}
}
