package manager

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	citools "github.com/openshift/ci-tools/pkg/api"
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
				BaseImages:     map[string]citools.ImageStreamTagReference{"an-image": {Namespace: "ci", Name: "test-image", Tag: "4.17"}},
			},
			Images: []citools.ProjectDirectoryImageBuildStepConfiguration{{
				From: "base",
				To:   "my-operator",
				ProjectDirectoryImageBuildInputs: citools.ProjectDirectoryImageBuildInputs{
					DockerfilePath: "path/to/dockerfile",
				},
			}},
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
								Default: ptrTo("dev"),
							},
							{
								Name:    "OO_INSTALL_NAMESPACE",
								Default: ptrTo("my-namespace"),
							},
							{
								Name:    "OO_PACKAGE",
								Default: ptrTo("my-operator"),
							},
							{
								Name:    "OO_TARGET_NAMESPACE",
								Default: ptrTo("!install"),
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
					Test: []citools.TestStep{{Reference: ptrTo("clusterbot-wait")}},
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
					"an-image":    {Namespace: "ci", Name: "test-image", Tag: "4.17"},
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
					Test: []citools.TestStep{{Reference: ptrTo("optional-operators-subscribe")}, {Reference: ptrTo("clusterbot-wait")}},
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
				BaseImages:     map[string]citools.ImageStreamTagReference{"an-image": {Namespace: "ci", Name: "test-image", Tag: "4.17"}},
			},
			Images: []citools.ProjectDirectoryImageBuildStepConfiguration{{
				From: "base",
				To:   "my-operator",
				ProjectDirectoryImageBuildInputs: citools.ProjectDirectoryImageBuildInputs{
					DockerfilePath: "path/to/dockerfile",
				},
			}},
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
					Test: []citools.TestStep{{Reference: ptrTo("clusterbot-wait")}},
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
					"an-image":    {Namespace: "ci", Name: "test-image", Tag: "4.17"},
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
						{Reference: ptrTo("clusterbot-wait")}},
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

func ptrTo[T any](v T) *T {
	return &v
}
