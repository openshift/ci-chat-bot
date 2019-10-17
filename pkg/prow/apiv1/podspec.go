package apiv1

import (
	"strings"
	"path/filepath"
	"strconv"

	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	logMountName            = "logs"
	logMountPath            = "/logs"
	artifactsEnv            = "ARTIFACTS"
	artifactsPath           = logMountPath + "/artifacts"
	codeMountName           = "code"
	codeMountPath           = "/home/prow/go"
	gopathEnv               = "GOPATH"
	toolsMountName          = "tools"
	toolsMountPath          = "/tools"
	gcsCredentialsMountName = "gcs-credentials"
	gcsCredentialsMountPath = "/secrets/gcs"
)

// Labels returns a string slice with label consts from kube.
func Labels() []string {
	return []string{ProwJobTypeLabel, CreatedByProw, ProwJobIDLabel}
}

// VolumeMounts returns a string slice with *MountName consts in it.
func VolumeMounts() []string {
	return []string{logMountName, codeMountName, toolsMountName, gcsCredentialsMountName}
}

// VolumeMountPaths returns a string slice with *MountPath consts in it.
func VolumeMountPaths() []string {
	return []string{logMountPath, codeMountPath, toolsMountPath, gcsCredentialsMountPath}
}

// LabelsAndAnnotationsForSpec returns a minimal set of labels to add to prowjobs or its owned resources.
//
// User-provided extraLabels and extraAnnotations values will take precedence over auto-provided values.
func LabelsAndAnnotationsForSpec(spec ProwJobSpec, extraLabels, extraAnnotations map[string]string) (map[string]string, map[string]string) {
	jobNameForLabel := spec.Job
	if len(jobNameForLabel) > validation.LabelValueMaxLength {
		// TODO(fejta): consider truncating middle rather than end.
		jobNameForLabel = strings.TrimRight(spec.Job[:validation.LabelValueMaxLength], ".-")
	}
	labels := map[string]string{
		CreatedByProw:     "true",
		ProwJobTypeLabel:  string(spec.Type),
		ProwJobAnnotation: jobNameForLabel,
	}
	if spec.Type != PeriodicJob && spec.Refs != nil {
		labels[OrgLabel] = spec.Refs.Org
		labels[RepoLabel] = spec.Refs.Repo
		if len(spec.Refs.Pulls) > 0 {
			labels[PullLabel] = strconv.Itoa(spec.Refs.Pulls[0].Number)
		}
	}

	for k, v := range extraLabels {
		labels[k] = v
	}

	// let's validate labels
	for key, value := range labels {
		if errs := validation.IsValidLabelValue(value); len(errs) > 0 {
			// try to use basename of a path, if path contains invalid //
			base := filepath.Base(value)
			if errs := validation.IsValidLabelValue(base); len(errs) == 0 {
				labels[key] = base
				continue
			}
			delete(labels, key)
		}
	}

	annotations := map[string]string{
		ProwJobAnnotation: spec.Job,
	}
	for k, v := range extraAnnotations {
		annotations[k] = v
	}

	return labels, annotations
}