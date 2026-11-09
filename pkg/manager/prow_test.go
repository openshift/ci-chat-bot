package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	imagev1 "github.com/openshift/api/image/v1"
	"github.com/openshift/ci-chat-bot/pkg/utils"
	citools "github.com/openshift/ci-tools/pkg/api"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	prowapiv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowjobClient "sigs.k8s.io/prow/pkg/client/clientset/versioned/typed/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
)

func Test_processOperatorPR(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                 string
		operatorRepo         string
		sourceConfig         citools.ReleaseBuildConfiguration
		targetConfig         citools.ReleaseBuildConfiguration
		job                  Job
		ref                  prowapiv1.Refs
		prowjob              prowapiv1.ProwJob
		expectedOperatorRepo string
		expectedSourceConfig citools.ReleaseBuildConfiguration
		expectedProwjob      prowapiv1.ProwJob
		expectedJob          Job
		expectedErr          bool
	}{{
		name:                 "different operator repo",
		operatorRepo:         "org/repo",
		targetConfig:         citools.ReleaseBuildConfiguration{Operator: &citools.OperatorStepConfiguration{Bundles: []citools.Bundle{{As: "test"}}}},
		sourceConfig:         citools.ReleaseBuildConfiguration{},
		prowjob:              prowapiv1.ProwJob{},
		ref:                  prowapiv1.Refs{Org: "org", Repo: "repo2"},
		job:                  Job{Operator: OperatorInfo{Is: true}},
		expectedJob:          Job{Operator: OperatorInfo{Is: true}},
		expectedSourceConfig: citools.ReleaseBuildConfiguration{},
		expectedProwjob:      prowapiv1.ProwJob{},
		expectedErr:          true,
	}, {
		name:                 "not an operator",
		operatorRepo:         "org/repo",
		targetConfig:         citools.ReleaseBuildConfiguration{},
		sourceConfig:         citools.ReleaseBuildConfiguration{},
		prowjob:              prowapiv1.ProwJob{},
		ref:                  prowapiv1.Refs{Org: "org", Repo: "repo"},
		job:                  Job{Operator: OperatorInfo{Is: false}},
		expectedJob:          Job{Operator: OperatorInfo{Is: false}},
		expectedSourceConfig: citools.ReleaseBuildConfiguration{},
		expectedProwjob:      prowapiv1.ProwJob{},
	}, {
		name:         "indexed operator",
		operatorRepo: "org/repo",
		targetConfig: citools.ReleaseBuildConfiguration{
			Metadata: citools.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			},
			InputConfiguration: citools.InputConfiguration{
				BuildRootImage: &citools.BuildRootImageConfiguration{FromRepository: true},
				BaseImages:     map[string]citools.ImageStreamTagReference{"an-image": {Namespace: "ci", Name: "test-image", Tag: "4.19"}},
			},
			Images: citools.ImageConfiguration{
				Items: []citools.ProjectDirectoryImageBuildStepConfiguration{{

					From: "base",
					To:   "my-operator",
					ProjectDirectoryImageBuildInputs: citools.ProjectDirectoryImageBuildInputs{
						DockerfilePath: "path/to/dockerfile",
					},
				}},
			},
			Operator: &citools.OperatorStepConfiguration{Bundles: []citools.Bundle{{As: "test"}}},
			Tests: []citools.TestStepConfiguration{{
				As: "my-test",
				MultiStageTestConfigurationLiteral: &citools.MultiStageTestConfigurationLiteral{
					ClusterProfileLiteral: &citools.ClusterProfileLiteral{Name: "aws"},
					Dependencies:          citools.TestDependencies{"OO_INDEX": "ci-index-test"},
					Environment: citools.TestEnvironment{
						"OO_CHANNEL":           "dev",
						"OO_INSTALL_NAMESPACE": "my-namespace",
						"OO_PACKAGE":           "my-operator",
						"OO_TARGET_NAMESPACE":  "!install",
					},
					Pre: []citools.LiteralTestStep{{
						As:           "install-operator",
						Dependencies: []citools.StepDependency{{Env: "OO_INDEX", Name: "ci-index-test"}},
						Environment: []citools.StepParameter{
							{
								Name:    "OO_CHANNEL",
								Default: new("dev"),
							},
							{
								Name:    "OO_INSTALL_NAMESPACE",
								Default: new("my-namespace"),
							},
							{
								Name:    "OO_PACKAGE",
								Default: new("my-operator"),
							},
							{
								Name:    "OO_TARGET_NAMESPACE",
								Default: new("!install"),
							},
						},
					}},
				},
			}},
		},
		sourceConfig: citools.ReleaseBuildConfiguration{
			Tests: []citools.TestStepConfiguration{{
				As: "launch",
				MultiStageTestConfiguration: &citools.MultiStageTestConfiguration{
					Test: []citools.TestStep{{Reference: new("clusterbot-wait")}},
				},
			}},
		},
		prowjob:              prowapiv1.ProwJob{ObjectMeta: v1.ObjectMeta{Annotations: map[string]string{}}},
		ref:                  prowapiv1.Refs{Org: "org", Repo: "repo"},
		job:                  Job{Operator: OperatorInfo{Is: false}},
		expectedOperatorRepo: "org/repo",
		expectedJob:          Job{Operator: OperatorInfo{Is: true, HasIndex: true, BundleName: "test"}},
		expectedSourceConfig: citools.ReleaseBuildConfiguration{
			Metadata: citools.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			},
			InputConfiguration: citools.InputConfiguration{
				BuildRootImage: &citools.BuildRootImageConfiguration{FromRepository: true},
				BaseImages: map[string]citools.ImageStreamTagReference{
					"an-image":    {Namespace: "ci", Name: "test-image", Tag: "4.19"},
					"my-operator": {Namespace: "$(NAMESPACE)", Name: "stable", Tag: "my-operator"},
				},
			},
			Operator: &citools.OperatorStepConfiguration{Bundles: []citools.Bundle{{As: "test"}}},
			Tests: []citools.TestStepConfiguration{{
				As: "launch",
				MultiStageTestConfiguration: &citools.MultiStageTestConfiguration{
					Dependencies: citools.TestDependencies{"OO_INDEX": "ci-index-test"},
					Environment: citools.TestEnvironment{
						"OO_CHANNEL":           "dev",
						"OO_INSTALL_NAMESPACE": "my-namespace",
						"OO_PACKAGE":           "my-operator",
						"OO_TARGET_NAMESPACE":  "!install",
					},
					Test: []citools.TestStep{{Reference: new("optional-operators-subscribe")}, {Reference: new("clusterbot-wait")}},
				},
			}},
		},
		expectedProwjob: prowapiv1.ProwJob{
			ObjectMeta: v1.ObjectMeta{
				Annotations: map[string]string{
					"ci-chat-bot.openshift.io/IsOperator":         "true",
					"ci-chat-bot.openshift.io/OperatorBundleName": "test",
					"ci-chat-bot.openshift.io/OperatorHasIndex":   "true",
				},
			},
		},
	}, {
		name:         "nonindexed operator",
		operatorRepo: "org/repo",
		targetConfig: citools.ReleaseBuildConfiguration{
			Metadata: citools.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			},
			InputConfiguration: citools.InputConfiguration{
				BuildRootImage: &citools.BuildRootImageConfiguration{FromRepository: true},
				BaseImages:     map[string]citools.ImageStreamTagReference{"an-image": {Namespace: "ci", Name: "test-image", Tag: "4.19"}},
			},
			Images: citools.ImageConfiguration{
				Items: []citools.ProjectDirectoryImageBuildStepConfiguration{{
					From: "base",
					To:   "my-operator",
					ProjectDirectoryImageBuildInputs: citools.ProjectDirectoryImageBuildInputs{
						DockerfilePath: "path/to/dockerfile",
					},
				}},
			},
			Operator: &citools.OperatorStepConfiguration{Bundles: []citools.Bundle{{As: "test"}}},
			Tests: []citools.TestStepConfiguration{{
				As: "my-test",
				MultiStageTestConfigurationLiteral: &citools.MultiStageTestConfigurationLiteral{
					ClusterProfileLiteral: &citools.ClusterProfileLiteral{Name: "aws"},
					Test: []citools.LiteralTestStep{{
						As:           "install",
						Dependencies: []citools.StepDependency{{Env: "OO_BUNDLE", Name: "test"}},
						Commands:     "This is a script",
						From:         "operator-sdk",
					}},
				},
			}},
		},
		sourceConfig: citools.ReleaseBuildConfiguration{
			Tests: []citools.TestStepConfiguration{{
				As: "launch",
				MultiStageTestConfiguration: &citools.MultiStageTestConfiguration{
					Test: []citools.TestStep{{Reference: new("clusterbot-wait")}},
				},
			}},
		},
		prowjob:              prowapiv1.ProwJob{ObjectMeta: v1.ObjectMeta{Annotations: map[string]string{}}},
		ref:                  prowapiv1.Refs{Org: "org", Repo: "repo"},
		job:                  Job{Operator: OperatorInfo{Is: false}},
		expectedOperatorRepo: "org/repo",
		expectedJob:          Job{Operator: OperatorInfo{Is: true, BundleName: "test"}},
		expectedSourceConfig: citools.ReleaseBuildConfiguration{
			Metadata: citools.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			},
			InputConfiguration: citools.InputConfiguration{
				BuildRootImage: &citools.BuildRootImageConfiguration{FromRepository: true},
				BaseImages: map[string]citools.ImageStreamTagReference{
					"an-image":    {Namespace: "ci", Name: "test-image", Tag: "4.19"},
					"my-operator": {Namespace: "$(NAMESPACE)", Name: "stable", Tag: "my-operator"},
				},
			},
			Operator: &citools.OperatorStepConfiguration{Bundles: []citools.Bundle{{As: "test"}}},
			Tests: []citools.TestStepConfiguration{{
				As: "launch",
				MultiStageTestConfiguration: &citools.MultiStageTestConfiguration{
					Environment:  make(citools.TestEnvironment),
					Dependencies: make(citools.TestDependencies),
					Test: []citools.TestStep{
						{LiteralTestStep: &citools.LiteralTestStep{
							As:           "install",
							Dependencies: []citools.StepDependency{{Env: "OO_BUNDLE", Name: "test"}},
							Commands:     "This is a script",
							From:         "operator-sdk",
						}},
						{LiteralTestStep: &citools.LiteralTestStep{
							As:       "chat-bot-operator-complete",
							From:     "pipeline:src",
							Commands: "echo 'complete' > ${SHARED_DIR}/operator_complete.txt",
							Resources: citools.ResourceRequirements{
								Requests: citools.ResourceList{
									"cpu":    "100m",
									"memory": "200Mi",
								},
							},
						}},
						{Reference: new("clusterbot-wait")}},
				},
			}},
		},
		expectedProwjob: prowapiv1.ProwJob{
			ObjectMeta: v1.ObjectMeta{
				Annotations: map[string]string{
					"ci-chat-bot.openshift.io/IsOperator":         "true",
					"ci-chat-bot.openshift.io/OperatorBundleName": "test",
				},
			},
		},
	}}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			operatorRepo, err := processOperatorPR(tc.operatorRepo, &tc.sourceConfig, &tc.targetConfig, &tc.job, &tc.ref, &tc.prowjob)
			if tc.expectedErr && err == nil {
				t.Fatal("Expected error but did not get one")
			}
			if !tc.expectedErr && err != nil {
				t.Fatalf("Received unexpected error: %v", err)
			}
			if operatorRepo != tc.expectedOperatorRepo {
				t.Errorf("Expected operatorRepo == `%s`, got `%s`", tc.expectedOperatorRepo, operatorRepo)
			}
			if diff := cmp.Diff(tc.sourceConfig, tc.expectedSourceConfig); diff != "" {
				t.Errorf("sourceConfig differs from expected: %s", diff)
			}
			if diff := cmp.Diff(tc.prowjob, tc.expectedProwjob); diff != "" {
				t.Errorf("prowjob differs from expected: %s", diff)
			}
			if diff := cmp.Diff(tc.job, tc.expectedJob); diff != "" {
				t.Errorf("job differs from expected: %s", diff)
			}
		})
	}
}

func Test_applyClusterProfile(t *testing.T) {
	newJob := func() *prowapiv1.ProwJob {
		return &prowapiv1.ProwJob{
			ObjectMeta: v1.ObjectMeta{
				Labels: map[string]string{"ci-operator.openshift.io/cloud-cluster-profile": "gcp"},
			},
			Spec: prowapiv1.ProwJobSpec{
				PodSpec: &corev1.PodSpec{
					Volumes: []corev1.Volume{{
						Name: "cluster-profile",
						VolumeSource: corev1.VolumeSource{
							Projected: &corev1.ProjectedVolumeSource{
								Sources: []corev1.VolumeProjection{{
									Secret: &corev1.SecretProjection{
										LocalObjectReference: corev1.LocalObjectReference{Name: "cluster-secrets-gcp"},
									},
								}},
							},
						},
					}},
				},
			},
		}
	}
	newConfig := func() *citools.ReleaseBuildConfiguration {
		return &citools.ReleaseBuildConfiguration{
			Tests: []citools.TestStepConfiguration{{
				As: "launch",
				MultiStageTestConfiguration: &citools.MultiStageTestConfiguration{
					ClusterProfile: "gcp",
					Environment:    citools.TestEnvironment{"BASE_DOMAIN": "gcp.devcluster.openshift.com"},
				},
			}},
		}
	}

	t.Run("sets label and launch ClusterProfile without touching secret volume or BASE_DOMAIN", func(t *testing.T) {
		job := newJob()
		config := newConfig()
		if err := applyClusterProfile(job, config, "openshift-org-gcp"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := job.Labels["ci-operator.openshift.io/cloud-cluster-profile"]; got != "openshift-org-gcp" {
			t.Errorf("label = %q, want %q", got, "openshift-org-gcp")
		}
		if got := config.Tests[0].MultiStageTestConfiguration.ClusterProfile; got != "openshift-org-gcp" {
			t.Errorf("launch ClusterProfile = %q, want %q", got, "openshift-org-gcp")
		}
		// the per-account secret volume must be left untouched; the runtime
		// resolves the secret from the account the profile set selects
		gotSecret := job.Spec.PodSpec.Volumes[0].Projected.Sources[0].Secret.Name
		if gotSecret != "cluster-secrets-gcp" {
			t.Errorf("cluster-profile secret = %q, want it left as %q", gotSecret, "cluster-secrets-gcp")
		}
		// BASE_DOMAIN must be left untouched
		if got := config.Tests[0].MultiStageTestConfiguration.Environment["BASE_DOMAIN"]; got != "gcp.devcluster.openshift.com" {
			t.Errorf("BASE_DOMAIN = %q, want it left unchanged", got)
		}
	})

	t.Run("errors when no launch test is present", func(t *testing.T) {
		job := newJob()
		config := &citools.ReleaseBuildConfiguration{Tests: []citools.TestStepConfiguration{{As: "other"}}}
		if err := applyClusterProfile(job, config, "openshift-org-gcp"); err == nil {
			t.Fatal("expected error for missing launch test, got nil")
		}
	})

	t.Run("errors when launch test is not multistage", func(t *testing.T) {
		job := newJob()
		config := &citools.ReleaseBuildConfiguration{Tests: []citools.TestStepConfiguration{{As: "launch"}}}
		if err := applyClusterProfile(job, config, "openshift-org-gcp"); err == nil {
			t.Fatal("expected error for non-multistage launch test, got nil")
		}
	})
}

type fakeProwConfigLoader struct {
	cfg *prowconfig.Config
}

func (f fakeProwConfigLoader) Config() *prowconfig.Config {
	return f.cfg
}

type fakeConfigResolver map[string]string

func (f fakeConfigResolver) Resolve(org, repo, branch, variant string) ([]byte, bool, error) {
	cfg, ok := f[org+"/"+repo]
	return []byte(cfg), ok, nil
}

// fakeProwJobs records the created ProwJob and reports it as scheduled
type fakeProwJobs struct {
	prowjobClient.ProwJobInterface
	created *prowapiv1.ProwJob
}

func (f *fakeProwJobs) Create(_ context.Context, pj *prowapiv1.ProwJob, _ v1.CreateOptions) (*prowapiv1.ProwJob, error) {
	f.created = pj.DeepCopy()
	return pj, nil
}

func (f *fakeProwJobs) Get(_ context.Context, name string, _ v1.GetOptions) (*prowapiv1.ProwJob, error) {
	if f.created == nil || f.created.Name != name {
		return nil, fmt.Errorf("prowjob %q not found", name)
	}
	pj := f.created.DeepCopy()
	pj.Status.URL = "https://prow.example.com/view/" + name
	return pj, nil
}

type fakeProwV1 struct {
	prowjobClient.ProwV1Interface
	jobs *fakeProwJobs
}

func (f *fakeProwV1) ProwJobs(string) prowjobClient.ProwJobInterface {
	return f.jobs
}

// newBuildJobTestManager returns a jobManager able to run newJob for a build job
// without a cluster, along with the fake used to capture the created ProwJob.
func newBuildJobTestManager(t *testing.T, targetConfigs fakeConfigResolver) (*jobManager, *fakeProwJobs) {
	t.Helper()
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/image.openshift.io/v1/namespaces/openshift/imagestreams/cli" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&imagev1.ImageStream{
			TypeMeta:   v1.TypeMeta{Kind: "ImageStream", APIVersion: "image.openshift.io/v1"},
			ObjectMeta: v1.ObjectMeta{Name: "cli", Namespace: "openshift"},
			Status:     imagev1.ImageStreamStatus{PublicDockerImageRepository: "registry.build01.example.com/openshift/cli"},
		})
	}))
	t.Cleanup(imageServer.Close)
	imageClient, err := imageclientset.NewForConfig(&rest.Config{Host: imageServer.URL})
	if err != nil {
		t.Fatalf("unable to create image client: %v", err)
	}

	sourceConfig := `tests:
- as: launch
  steps:
    cluster_profile: aws
    workflow: ipi-aws
`
	prowJobs := &fakeProwJobs{}
	m := &jobManager{
		prowConfigLoader: fakeProwConfigLoader{cfg: &prowconfig.Config{JobConfig: prowconfig.JobConfig{
			Periodics: []prowconfig.Periodic{{
				JobBase: prowconfig.JobBase{
					Name: "release-openshift-origin-installer-launch-aws",
					Spec: &corev1.PodSpec{Containers: []corev1.Container{{
						Command: []string{"ci-operator"},
						Env:     []corev1.EnvVar{{Name: "CONFIG_SPEC", Value: sourceConfig}},
					}}},
				},
			}},
		}}},
		prowClient:     &fakeProwV1{jobs: prowJobs},
		clusterClients: utils.BuildClusterClientConfigMap{"build01": {TargetImageClient: imageClient}},
		configResolver: targetConfigs,
		prowNamespace:  "ci",
		maxAge:         time.Hour,
	}
	m.muJob.running = make(map[string]struct{})
	return m, prowJobs
}

func TestNewJobBuildRefPathAliases(t *testing.T) {
	t.Parallel()
	pull := []prowapiv1.Pull{{Number: 1, SHA: "abcdef", Author: "someone"}}
	clusterAPIRef := prowapiv1.Refs{Org: "openshift", Repo: "cluster-api", BaseRef: "main", BaseSHA: "123456", Pulls: pull}
	installerRef := prowapiv1.Refs{Org: "openshift", Repo: "installer", BaseRef: "main", BaseSHA: "654321", Pulls: pull}
	withPathAlias := func(ref prowapiv1.Refs, pathAlias string) prowapiv1.Refs {
		ref.PathAlias = pathAlias
		return ref
	}

	testCases := []struct {
		name          string
		refs          []prowapiv1.Refs
		targetConfigs fakeConfigResolver
		// expectedBuildRefs are the refs in JOB_SPEC_<index> used by the child image build
		expectedBuildRefs []prowapiv1.Refs
		// expectedExtraRefs are the refs cloned by the launch job itself
		expectedExtraRefs []prowapiv1.Refs
	}{{
		name: "canonical_go_repository is used for the child build checkout",
		refs: []prowapiv1.Refs{clusterAPIRef},
		targetConfigs: fakeConfigResolver{
			"openshift/cluster-api": "canonical_go_repository: sigs.k8s.io/cluster-api\n",
		},
		expectedBuildRefs: []prowapiv1.Refs{withPathAlias(clusterAPIRef, "sigs.k8s.io/cluster-api")},
		expectedExtraRefs: []prowapiv1.Refs{withPathAlias(clusterAPIRef, "0/github.com/openshift/cluster-api")},
	}, {
		name: "matching canonical_go_repository_list entry is used for the child build checkout",
		refs: []prowapiv1.Refs{clusterAPIRef},
		targetConfigs: fakeConfigResolver{
			"openshift/cluster-api": `canonical_go_repository_list:
- ref: openshift.other
  repository: example.com/other
- ref: openshift.cluster-api
  repository: sigs.k8s.io/cluster-api
`,
		},
		expectedBuildRefs: []prowapiv1.Refs{withPathAlias(clusterAPIRef, "sigs.k8s.io/cluster-api")},
		expectedExtraRefs: []prowapiv1.Refs{withPathAlias(clusterAPIRef, "0/github.com/openshift/cluster-api")},
	}, {
		name: "repository without a canonical path keeps the default child build checkout",
		refs: []prowapiv1.Refs{installerRef},
		targetConfigs: fakeConfigResolver{
			"openshift/installer": "{}\n",
		},
		expectedBuildRefs: []prowapiv1.Refs{installerRef},
		expectedExtraRefs: []prowapiv1.Refs{withPathAlias(installerRef, "0/github.com/openshift/installer")},
	}, {
		name: "existing path alias is kept when the repository has no canonical path",
		refs: []prowapiv1.Refs{withPathAlias(installerRef, "example.com/installer")},
		targetConfigs: fakeConfigResolver{
			"openshift/installer": "{}\n",
		},
		expectedBuildRefs: []prowapiv1.Refs{withPathAlias(installerRef, "example.com/installer")},
		expectedExtraRefs: []prowapiv1.Refs{withPathAlias(installerRef, "0/github.com/openshift/installer")},
	}, {
		name: "canonical path overrides an existing path alias for the child build checkout",
		refs: []prowapiv1.Refs{withPathAlias(clusterAPIRef, "example.com/cluster-api")},
		targetConfigs: fakeConfigResolver{
			"openshift/cluster-api": "canonical_go_repository: sigs.k8s.io/cluster-api\n",
		},
		expectedBuildRefs: []prowapiv1.Refs{withPathAlias(clusterAPIRef, "sigs.k8s.io/cluster-api")},
		expectedExtraRefs: []prowapiv1.Refs{withPathAlias(clusterAPIRef, "0/github.com/openshift/cluster-api")},
	}, {
		name: "canonical path is resolved per repository",
		refs: []prowapiv1.Refs{clusterAPIRef, installerRef},
		targetConfigs: fakeConfigResolver{
			"openshift/cluster-api": "canonical_go_repository: sigs.k8s.io/cluster-api\n",
			"openshift/installer":   "{}\n",
		},
		expectedBuildRefs: []prowapiv1.Refs{withPathAlias(clusterAPIRef, "sigs.k8s.io/cluster-api"), installerRef},
		expectedExtraRefs: []prowapiv1.Refs{
			withPathAlias(clusterAPIRef, "0/github.com/openshift/cluster-api"),
			withPathAlias(installerRef, "1/github.com/openshift/installer"),
		},
	}}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, prowJobs := newBuildJobTestManager(t, tc.targetConfigs)
			job := &Job{
				Name:         "build-job",
				JobName:      "release-openshift-origin-installer-launch-aws",
				Mode:         JobTypeBuild,
				Platform:     "aws",
				BuildCluster: "build01",
				Inputs:       []JobInput{{Refs: tc.refs}},
			}
			if _, err := m.newJob(job); err != nil {
				t.Fatalf("newJob returned an unexpected error: %v", err)
			}
			pj := prowJobs.created
			if pj == nil {
				t.Fatal("newJob did not create a prow job")
			}

			for i, expectedRef := range tc.expectedBuildRefs {
				jobSpecEnv, _, ok := firstEnvVar(pj.Spec.PodSpec, fmt.Sprintf("JOB_SPEC_%d", i))
				if !ok {
					t.Fatalf("JOB_SPEC_%d was not set on the prow job", i)
				}
				var jobSpec JobSpec
				if err := json.Unmarshal([]byte(jobSpecEnv.Value), &jobSpec); err != nil {
					t.Fatalf("unable to parse JOB_SPEC_%d: %v", i, err)
				}
				if jobSpec.Refs == nil {
					t.Fatalf("JOB_SPEC_%d has no refs: %s", i, jobSpecEnv.Value)
				}
				if diff := cmp.Diff(JobSpec{Refs: &expectedRef}, jobSpec); diff != "" {
					t.Errorf("JOB_SPEC_%d differs from expected (-want +got):\n%s", i, diff)
				}

				repoPathEnv, _, ok := firstEnvVar(pj.Spec.PodSpec, fmt.Sprintf("REPO_PATH_%d", i))
				if !ok {
					t.Fatalf("REPO_PATH_%d was not set on the prow job", i)
				}
				if diff := cmp.Diff(tc.expectedExtraRefs[i].PathAlias, repoPathEnv.Value); diff != "" {
					t.Errorf("REPO_PATH_%d differs from expected (-want +got):\n%s", i, diff)
				}

				configEnv, _, ok := firstEnvVar(pj.Spec.PodSpec, fmt.Sprintf("CONFIG_SPEC_%d", i))
				if !ok {
					t.Fatalf("CONFIG_SPEC_%d was not set on the prow job", i)
				}
				var targetConfig citools.ReleaseBuildConfiguration
				if err := json.Unmarshal([]byte(configEnv.Value), &targetConfig); err != nil {
					t.Fatalf("unable to parse CONFIG_SPEC_%d: %v", i, err)
				}
				if canonical := targetConfig.DeterminePathAlias(expectedRef.Org, expectedRef.Repo); canonical != "" && canonical != jobSpec.Refs.PathAlias {
					t.Errorf("JOB_SPEC_%d path_alias %q does not match the canonical path %q in CONFIG_SPEC_%d", i, jobSpec.Refs.PathAlias, canonical, i)
				}
			}
			if _, _, ok := firstEnvVar(pj.Spec.PodSpec, fmt.Sprintf("JOB_SPEC_%d", len(tc.expectedBuildRefs))); ok {
				t.Errorf("unexpected JOB_SPEC_%d set on the prow job", len(tc.expectedBuildRefs))
			}
			if diff := cmp.Diff(tc.expectedExtraRefs, pj.Spec.ExtraRefs); diff != "" {
				t.Errorf("prow job extra_refs differ from expected (-want +got):\n%s", diff)
			}
		})
	}
}
