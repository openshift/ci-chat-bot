package apiv1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewProwJob initializes a ProwJob out of a ProwJobSpec.
func NewProwJob(spec ProwJobSpec, extraLabels, extraAnnotations map[string]string) ProwJob {
	labels, annotations := LabelsAndAnnotationsForSpec(spec, extraLabels, extraAnnotations)

	return ProwJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "prow.k8s.io/v1",
			Kind:       "ProwJob",
		},
		ObjectMeta: metav1.ObjectMeta{
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: spec,
		Status: ProwJobStatus{
			StartTime: metav1.Now(),
			State:     TriggeredState,
		},
	}
}

// PresubmitSpec initializes a ProwJobSpec for a given presubmit job.
func PresubmitSpec(p Presubmit, refs Refs) ProwJobSpec {
	pjs := specFromJobBase(p.JobBase)
	pjs.Type = PresubmitJob
	pjs.Context = p.Context
	pjs.Report = !p.SkipReport
	pjs.RerunCommand = p.RerunCommand
	if p.JenkinsSpec != nil {
		pjs.JenkinsSpec = &JenkinsSpec{
			GitHubBranchSourceJob: p.JenkinsSpec.GitHubBranchSourceJob,
		}
	}
	pjs.Refs = CompletePrimaryRefs(refs, p.JobBase)

	return pjs
}

// PostsubmitSpec initializes a ProwJobSpec for a given postsubmit job.
func PostsubmitSpec(p Postsubmit, refs Refs) ProwJobSpec {
	pjs := specFromJobBase(p.JobBase)
	pjs.Type = PostsubmitJob
	pjs.Context = p.Context
	pjs.Report = !p.SkipReport
	pjs.Refs = CompletePrimaryRefs(refs, p.JobBase)
	if p.JenkinsSpec != nil {
		pjs.JenkinsSpec = &JenkinsSpec{
			GitHubBranchSourceJob: p.JenkinsSpec.GitHubBranchSourceJob,
		}
	}

	return pjs
}

// PeriodicSpec initializes a ProwJobSpec for a given periodic job.
func PeriodicSpec(p Periodic) ProwJobSpec {
	pjs := specFromJobBase(p.JobBase)
	pjs.Type = PeriodicJob

	return pjs
}

// BatchSpec initializes a ProwJobSpec for a given batch job and ref spec.
func BatchSpec(p Presubmit, refs Refs) ProwJobSpec {
	pjs := specFromJobBase(p.JobBase)
	pjs.Type = BatchJob
	pjs.Context = p.Context
	pjs.Refs = CompletePrimaryRefs(refs, p.JobBase)

	return pjs
}

func specFromJobBase(jb JobBase) ProwJobSpec {
	var namespace string
	if jb.Namespace != nil {
		namespace = *jb.Namespace
	}
	var rerunAuthConfig RerunAuthConfig
	if jb.RerunAuthConfig != nil {
		rerunAuthConfig = *jb.RerunAuthConfig
	}
	return ProwJobSpec{
		Job:             jb.Name,
		Agent:           ProwJobAgent(jb.Agent),
		Cluster:         jb.Cluster,
		Namespace:       namespace,
		MaxConcurrency:  jb.MaxConcurrency,
		ErrorOnEviction: jb.ErrorOnEviction,

		ExtraRefs:        jb.ExtraRefs,
		DecorationConfig: jb.DecorationConfig,

		PodSpec:         jb.Spec,

		ReporterConfig:  jb.ReporterConfig,
		RerunAuthConfig: rerunAuthConfig,
		Hidden:          jb.Hidden,
	}
}

func CompletePrimaryRefs(refs Refs, jb JobBase) *Refs {
	if jb.PathAlias != "" {
		refs.PathAlias = jb.PathAlias
	}
	if jb.CloneURI != "" {
		refs.CloneURI = jb.CloneURI
	}
	refs.SkipSubmodules = jb.SkipSubmodules
	refs.CloneDepth = jb.CloneDepth
	return &refs
}

// PartitionActive separates the provided prowjobs into pending and triggered
// and returns them inside channels so that they can be consumed in parallel
// by different goroutines. Complete prowjobs are filtered out. Controller
// loops need to handle pending jobs first so they can conform to maximum
// concurrency requirements that different jobs may have.
func PartitionActive(pjs []ProwJob) (pending, triggered chan ProwJob) {
	// Size channels correctly.
	pendingCount, triggeredCount := 0, 0
	for _, pj := range pjs {
		switch pj.Status.State {
		case PendingState:
			pendingCount++
		case TriggeredState:
			triggeredCount++
		}
	}
	pending = make(chan ProwJob, pendingCount)
	triggered = make(chan ProwJob, triggeredCount)

	// Partition the jobs into the two separate channels.
	for _, pj := range pjs {
		switch pj.Status.State {
		case PendingState:
			pending <- pj
		case TriggeredState:
			triggered <- pj
		}
	}
	close(pending)
	close(triggered)
	return pending, triggered
}

// GetLatestProwJobs filters through the provided prowjobs and returns
// a map of jobType jobs to their latest prowjobs.
func GetLatestProwJobs(pjs []ProwJob, jobType ProwJobType) map[string]ProwJob {
	latestJobs := make(map[string]ProwJob)
	for _, j := range pjs {
		if j.Spec.Type != jobType {
			continue
		}
		name := j.Spec.Job
		if j.Status.StartTime.After(latestJobs[name].Status.StartTime.Time) {
			latestJobs[name] = j
		}
	}
	return latestJobs
}
