package common

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	clustermgmtv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/slack-go/slack"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	prowapiv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
)

// mockJobManager implements manager.JobManager with configurable ResolveAsPullRequest
type mockJobManager struct {
	resolveAsPullRequestFunc func(spec string) (*prowapiv1.Refs, error)
}

func (m *mockJobManager) SetNotifier(manager.JobCallbackFunc)      {}
func (m *mockJobManager) SetRosaNotifier(manager.RosaCallbackFunc) {}
func (m *mockJobManager) SetMceNotifier(manager.MCECallbackFunc)   {}
func (m *mockJobManager) LaunchJobForUser(req *manager.JobRequest) (string, error) {
	return "", nil
}
func (m *mockJobManager) CreateRosaCluster(user, channel, version string, duration time.Duration) (string, error) {
	return "", nil
}
func (m *mockJobManager) CheckValidJobConfiguration(req *manager.JobRequest) error { return nil }
func (m *mockJobManager) SyncJobForUser(user string) (string, error)               { return "", nil }
func (m *mockJobManager) TerminateJobForUser(user string) (string, error)          { return "", nil }
func (m *mockJobManager) GetLaunchJob(user string) (*manager.Job, error)           { return nil, nil }
func (m *mockJobManager) GetROSACluster(user string) (*clustermgmtv1.Cluster, string) {
	return nil, ""
}
func (m *mockJobManager) DescribeROSACluster(cluster string) (string, error) { return "", nil }
func (m *mockJobManager) LookupInputs(inputs []string, architecture string) (string, error) {
	return "", nil
}
func (m *mockJobManager) LookupRosaInputs(versionPrefix string) (string, error) { return "", nil }
func (m *mockJobManager) ListJobs(users string, filters manager.ListFilters) (string, string, []string) {
	return "", "", nil
}
func (m *mockJobManager) GetWorkflowConfig() *manager.WorkflowConfig { return nil }
func (m *mockJobManager) ResolveImageOrVersion(imageOrVersion, defaultImageOrVersion, architecture string) (string, string, string, error) {
	return "", "", "", nil
}
func (m *mockJobManager) ResolveAsPullRequest(spec string) (*prowapiv1.Refs, error) {
	if m.resolveAsPullRequestFunc != nil {
		return m.resolveAsPullRequestFunc(spec)
	}
	return nil, nil
}
func (m *mockJobManager) CreateMceCluster(user, channel, platform string, from [][]string, duration time.Duration) (string, error) {
	return "", nil
}
func (m *mockJobManager) DeleteMceCluster(user, clusterName string) (string, error) {
	return "", nil
}
func (m *mockJobManager) GetManagedClustersForUser(user string) (map[string]*clusterv1.ManagedCluster, map[string]*hivev1.ClusterDeployment, map[string]*hivev1.ClusterProvision, map[string]string, map[string]string) {
	return nil, nil, nil, nil, nil
}
func (m *mockJobManager) ListManagedClusters(user string) (string, string, []string) {
	return "", "", nil
}
func (m *mockJobManager) ListMceVersions() string                 { return "" }
func (m *mockJobManager) GetMceUserConfig() *manager.MceConfig    { return nil }
func (m *mockJobManager) GetUserCluster(user string) *manager.Job { return nil }
func (m *mockJobManager) GrantGCPAccess(email, requestedBy, justification, resource string) (string, []byte, error) {
	return "", nil, nil
}
func (m *mockJobManager) RevokeGCPAccess(email, requestedBy string) (string, error) {
	return "", nil
}
func (m *mockJobManager) GetGCPAccessManager() *manager.GCPAccessManager { return nil }
func (m *mockJobManager) GetOrgDataService() manager.OrgDataService      { return nil }

func TestParseModeSelections(t *testing.T) {
	tests := []struct {
		name       string
		selections []string
		wantPR     bool
		wantVer    bool
	}{
		{
			name:       "empty selections",
			selections: nil,
			wantPR:     false,
			wantVer:    false,
		},
		{
			name:       "PR key",
			selections: []string{modals.LaunchModePRKey},
			wantPR:     true,
			wantVer:    false,
		},
		{
			name:       "version key",
			selections: []string{modals.LaunchModeVersionKey},
			wantPR:     false,
			wantVer:    true,
		},
		{
			name:       "both keys",
			selections: []string{modals.LaunchModePRKey, modals.LaunchModeVersionKey},
			wantPR:     true,
			wantVer:    true,
		},
		{
			name:       "unknown key ignored",
			selections: []string{"something_else"},
			wantPR:     false,
			wantVer:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseModeSelections(tt.selections)
			if got := result.Has(modals.LaunchModePR); got != tt.wantPR {
				t.Errorf("Has(LaunchModePR) = %v, want %v", got, tt.wantPR)
			}
			if got := result.Has(modals.LaunchModeVersion); got != tt.wantVer {
				t.Errorf("Has(LaunchModeVersion) = %v, want %v", got, tt.wantVer)
			}
		})
	}
}

func TestHasPRMode(t *testing.T) {
	tests := []struct {
		name string
		mode []string
		want bool
	}{
		{
			name: "empty",
			mode: nil,
			want: false,
		},
		{
			name: "contains PR key",
			mode: []string{modals.LaunchModePRKey},
			want: true,
		},
		{
			name: "whitespace-padded PR key",
			mode: []string{"  " + modals.LaunchModePRKey + "  "},
			want: true,
		},
		{
			name: "version key only",
			mode: []string{modals.LaunchModeVersionKey},
			want: false,
		},
		{
			name: "both keys",
			mode: []string{modals.LaunchModeVersionKey, modals.LaunchModePRKey},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasPRMode(tt.mode); got != tt.want {
				t.Errorf("HasPRMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateFilterVersion(t *testing.T) {
	tests := []struct {
		name      string
		input     map[string]string
		wantNil   bool
		wantError bool
	}{
		{
			name:    "no inputs",
			input:   map[string]string{},
			wantNil: true,
		},
		{
			name:    "single input - latest build",
			input:   map[string]string{modals.LaunchFromLatestBuild: "nightly"},
			wantNil: true,
		},
		{
			name:    "single input - custom",
			input:   map[string]string{modals.LaunchFromCustom: "quay.io/foo:bar"},
			wantNil: true,
		},
		{
			name:    "single input - stream",
			input:   map[string]string{modals.LaunchFromStream: "4-stable"},
			wantNil: true,
		},
		{
			name: "two inputs set",
			input: map[string]string{
				modals.LaunchFromLatestBuild: "nightly",
				modals.LaunchFromCustom:      "quay.io/foo:bar",
			},
			wantNil:   false,
			wantError: true,
		},
		{
			name: "all three set",
			input: map[string]string{
				modals.LaunchFromLatestBuild: "nightly",
				modals.LaunchFromCustom:      "quay.io/foo:bar",
				modals.LaunchFromStream:      "4-stable",
			},
			wantNil:   false,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := modals.CallbackData{Input: tt.input}
			result := ValidateFilterVersion(data)

			if tt.wantNil && result != nil {
				t.Fatalf("expected nil, got %s", string(result))
			}
			if !tt.wantNil && result == nil {
				t.Fatal("expected non-nil validation error")
			}
			if result == nil {
				return
			}

			var resp slack.ViewSubmissionResponse
			if err := json.Unmarshal(result, &resp); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}
			if resp.ResponseAction != slack.RAErrors {
				t.Errorf("ResponseAction = %q, want %q", resp.ResponseAction, slack.RAErrors)
			}
		})
	}
}

func TestValidatePRInput(t *testing.T) {
	tests := []struct {
		name       string
		input      map[string]string
		mockFunc   func(spec string) (*prowapiv1.Refs, error)
		wantNil    bool
		wantErrors []string // substrings expected in error
	}{
		{
			name:    "no PR key",
			input:   map[string]string{},
			wantNil: true,
		},
		{
			name:  "valid single PR",
			input: map[string]string{modals.LaunchFromPR: "org/repo#123"},
			mockFunc: func(spec string) (*prowapiv1.Refs, error) {
				return &prowapiv1.Refs{Org: "org", Repo: "repo"}, nil
			},
			wantNil: true,
		},
		{
			name:  "nil refs returns error",
			input: map[string]string{modals.LaunchFromPR: "bad/pr#0"},
			mockFunc: func(spec string) (*prowapiv1.Refs, error) {
				return nil, nil
			},
			wantNil:    false,
			wantErrors: []string{"invalid PR"},
		},
		{
			name:  "mock returns error",
			input: map[string]string{modals.LaunchFromPR: "org/repo#999"},
			mockFunc: func(spec string) (*prowapiv1.Refs, error) {
				return &prowapiv1.Refs{}, fmt.Errorf("not found")
			},
			wantNil:    false,
			wantErrors: []string{"not found"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jm := &mockJobManager{resolveAsPullRequestFunc: tt.mockFunc}
			data := modals.CallbackData{Input: tt.input}

			result := ValidatePRInput(data, jm)

			if tt.wantNil && result != nil {
				t.Fatalf("expected nil, got %s", string(result))
			}
			if !tt.wantNil && result == nil {
				t.Fatal("expected non-nil validation error")
			}
			if result == nil {
				return
			}

			resultStr := string(result)
			for _, substr := range tt.wantErrors {
				if !strings.Contains(resultStr, substr) {
					t.Errorf("result %q does not contain %q", resultStr, substr)
				}
			}
		})
	}
}
