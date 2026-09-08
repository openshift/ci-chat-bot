package manager

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	citools "github.com/openshift/ci-tools/pkg/api"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	prowapiv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
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
					ClusterProfile: citools.ClusterProfileAWS,
					Dependencies:   citools.TestDependencies{"OO_INDEX": "ci-index-test"},
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
					ClusterProfile: citools.ClusterProfileAWS,
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
