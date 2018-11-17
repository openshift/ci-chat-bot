package prow

import (
	"bytes"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"

	prowapiv1 "github.com/openshift/ci-chat-bot/pkg/prow/apiv1"
)

type ProwConfigLoader interface {
	Config() *prowapiv1.Config
}

func JobForConfig(prowConfigLoader ProwConfigLoader, jobName string) (*prowapiv1.ProwJob, error) {
	config := prowConfigLoader.Config()
	if config == nil {
		return fmt.Errorf("the prow job %s is not valid: no prow jobs have been defined", jobName)
	}
	periodicConfig, ok := hasProwJob(config, jobName)
	if !ok {
		return fmt.Errorf("the prow job %s is not valid: no job with that name", jobName)
	}

	spec := prowSpecForPeriodicConfig(periodicConfig, config.Plank.DefaultDecorationConfig)

	pj := &prowapiv1.ProwJob{
		TypeMeta: metav1.TypeMeta{APIVersion: "prow.k8s.io/v1", Kind: "ProwJob"},
		ObjectMeta: metav1.ObjectMeta{
			Name: prowJobName,
			Annotations: map[string]string{
				releaseAnnotationSource: fmt.Sprintf("%s/%s", release.Source.Namespace, release.Source.Name),

				"prow.k8s.io/job": spec.Job,
			},
			Labels: map[string]string{
				"release.openshift.io/verify": "true",

				"prow.k8s.io/type": string(spec.Type),
				"prow.k8s.io/job":  spec.Job,
			},
		},
		Spec: *spec,
		Status: prowapiv1.ProwJobStatus{
			State: prowapiv1.PendingState,
		},
	}
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

func OverrideJobEnvironment(spec *prowapiv1.ProwJobSpec, image string) error {
	if spec.PodSpec == nil {
		return nil
	}
	for i := range spec.PodSpec.Containers {
		c := &spec.PodSpec.Containers[i]
		for j := range c.Env {
			switch name := c.Env[j].Name; {
			case name == "RELEASE_IMAGE_LATEST":
				c.Env[j].Value = image
			}
		}
	}
	return nil
}
