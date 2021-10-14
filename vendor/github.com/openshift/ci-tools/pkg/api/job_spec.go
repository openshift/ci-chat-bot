package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"runtime/debug"

	"github.com/sirupsen/logrus"

	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"
)

// JobSpec is a superset of the upstream spec.
// +k8s:deepcopy-gen=false
type JobSpec struct {
	downwardapi.JobSpec `json:",inline"`

	// rawSpec is the serialized form of the Spec
	rawSpec string

	// these fields allow the job to be targeted at a location
	namespace     string
	BaseNamespace string

	// if set, any new artifacts will be a child of this object
	owner *meta.OwnerReference

	Metadata Metadata
	Target   string
}

// Namespace returns the namespace of the job. Must not be evaluated
// at step construction time because its unset there
func (s *JobSpec) Namespace() string {
	if s.namespace == "" {
		logrus.Warn("Warning, namespace accessed before it was set, this is a bug in ci-operator. Stack:")
		logrus.Warn(string(debug.Stack()))
	}
	return s.namespace
}

func (s *JobSpec) SetNamespace(namespace string) {
	s.namespace = namespace
}

func (s *JobSpec) RawSpec() string {
	return s.rawSpec
}

func (s *JobSpec) Owner() *meta.OwnerReference {
	return s.owner
}

func (s *JobSpec) SetOwner(owner *meta.OwnerReference) {
	s.owner = owner
}

// Inputs returns the definition of the job as an input to
// the execution graph.
func (s *JobSpec) Inputs() InputDefinition {
	spec := &JobSpec{
		JobSpec: downwardapi.JobSpec{
			Refs: s.Refs,
		},
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		panic(err)
	}
	return InputDefinition{string(raw)}
}

func (s JobSpec) JobNameHash() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(s.Job)))[:5]
}

// ResolveSpecFromEnv will determine the Refs being
// tested in by parsing Prow environment variable contents
func ResolveSpecFromEnv() (*JobSpec, error) {
	apiSpec, err := downwardapi.ResolveSpecFromEnv()
	if err != nil {
		return nil, fmt.Errorf("malformed $JOB_SPEC: %w", err)
	}
	raw, err := json.Marshal(apiSpec)
	if err != nil {
		panic(err)
	}
	return &JobSpec{
		JobSpec: *apiSpec,
		rawSpec: string(raw),
	}, nil
}
