package slack

import (
	"fmt"
	"strings"
	"testing"
	"time"

	orgdatacore "github.com/openshift-eng/cyborg-data/go"
	clustermgmtv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/parser"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	prowapiv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
)

// mockSlackClient is a mock implementation of the Slack client for testing
type mockSlackClient struct {
	getUserInfoFunc  func(userID string) (*slack.User, error)
	uploadFileV2Func func(params slack.UploadFileV2Parameters) (*slack.FileSummary, error)
}

func (m *mockSlackClient) GetUserInfo(userID string) (*slack.User, error) {
	if m.getUserInfoFunc != nil {
		return m.getUserInfoFunc(userID)
	}
	return nil, fmt.Errorf("mock GetUserInfo not implemented")
}

func (m *mockSlackClient) UploadFileV2(params slack.UploadFileV2Parameters) (*slack.FileSummary, error) {
	if m.uploadFileV2Func != nil {
		return m.uploadFileV2Func(params)
	}
	return nil, fmt.Errorf("mock UploadFileV2 not implemented")
}

func (m *mockSlackClient) PostMessage(channelID string, options ...slack.MsgOption) (string, string, error) {
	// Not needed for Request/Revoke tests but required by interface
	return "", "", fmt.Errorf("mock PostMessage not implemented")
}

// mockJobManager is a mock implementation for testing
type mockJobManager struct {
	getOrgDataServiceFunc func() manager.OrgDataService
	grantGCPAccessFunc    func(email, slackID, justification, resource string) (string, error)
	revokeGCPAccessFunc   func(email, slackID string) (string, error)
}

func (m *mockJobManager) GetOrgDataService() manager.OrgDataService {
	if m.getOrgDataServiceFunc != nil {
		return m.getOrgDataServiceFunc()
	}
	return nil
}

func (m *mockJobManager) GrantGCPAccess(email, slackID, justification, resource string) (string, []byte, error) {
	if m.grantGCPAccessFunc != nil {
		msg, err := m.grantGCPAccessFunc(email, slackID, justification, resource)
		// Return a mock service account key JSON
		mockKey := []byte(`{"type":"service_account","project_id":"test-project"}`)
		return msg, mockKey, err
	}
	return "", nil, fmt.Errorf("mock GrantGCPAccess not implemented")
}

func (m *mockJobManager) RevokeGCPAccess(email, slackID string) (string, error) {
	if m.revokeGCPAccessFunc != nil {
		return m.revokeGCPAccessFunc(email, slackID)
	}
	return "", fmt.Errorf("mock RevokeGCPAccess not implemented")
}

// Stub implementations of other JobManager methods not used by Request/Revoke tests
func (m *mockJobManager) SetNotifier(manager.JobCallbackFunc)                      {}
func (m *mockJobManager) SetRosaNotifier(manager.RosaCallbackFunc)                 {}
func (m *mockJobManager) SetMceNotifier(manager.MCECallbackFunc)                   {}
func (m *mockJobManager) LaunchJobForUser(req *manager.JobRequest) (string, error) { return "", nil }
func (m *mockJobManager) CreateRosaCluster(user, channel, version string, duration time.Duration) (string, error) {
	return "", nil
}
func (m *mockJobManager) CheckValidJobConfiguration(req *manager.JobRequest) error    { return nil }
func (m *mockJobManager) SyncJobForUser(user string) (string, error)                  { return "", nil }
func (m *mockJobManager) TerminateJobForUser(user string) (string, error)             { return "", nil }
func (m *mockJobManager) GetLaunchJob(user string) (*manager.Job, error)              { return nil, nil }
func (m *mockJobManager) GetROSACluster(user string) (*clustermgmtv1.Cluster, string) { return nil, "" }
func (m *mockJobManager) DescribeROSACluster(cluster string) (string, error)          { return "", nil }
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
func (m *mockJobManager) ResolveAsPullRequest(spec string) (*prowapiv1.Refs, error) { return nil, nil }
func (m *mockJobManager) CreateMceCluster(user, channel, platform string, from [][]string, duration time.Duration) (string, error) {
	return "", nil
}
func (m *mockJobManager) DeleteMceCluster(user, clusterName string) (string, error) { return "", nil }
func (m *mockJobManager) GetManagedClustersForUser(user string) (map[string]*clusterv1.ManagedCluster, map[string]*hivev1.ClusterDeployment, map[string]*hivev1.ClusterProvision, map[string]string, map[string]string) {
	return nil, nil, nil, nil, nil
}
func (m *mockJobManager) ListManagedClusters(user string) (string, string, []string) {
	return "", "", nil
}
func (m *mockJobManager) ListMceVersions() string                        { return "" }
func (m *mockJobManager) GetMceUserConfig() *manager.MceConfig           { return nil }
func (m *mockJobManager) GetUserCluster(user string) *manager.Job        { return nil }
func (m *mockJobManager) GetGCPAccessManager() *manager.GCPAccessManager { return nil }

// mockOrgDataService is a mock implementation of OrgDataService for testing
type mockOrgDataService struct {
	isSlackUserInOrgFunc   func(slackID, orgName string) bool
	getEmployeeByEmailFunc func(email string) *orgdatacore.Employee
	isEmployeeInOrgFunc    func(uid, orgName string) bool
}

func (m *mockOrgDataService) IsSlackUserInOrg(slackID, orgName string) bool {
	if m.isSlackUserInOrgFunc != nil {
		return m.isSlackUserInOrgFunc(slackID, orgName)
	}
	return false
}

func (m *mockOrgDataService) GetEmployeeByEmail(email string) *orgdatacore.Employee {
	if m.getEmployeeByEmailFunc != nil {
		return m.getEmployeeByEmailFunc(email)
	}
	return nil
}

func (m *mockOrgDataService) IsEmployeeInOrg(uid, orgName string) bool {
	if m.isEmployeeInOrgFunc != nil {
		return m.isEmployeeInOrgFunc(uid, orgName)
	}
	return false
}

func TestRequest(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                    string
		userID                  string
		resource                string
		justification           string
		getUserInfoError        error
		userEmail               string
		orgDataServiceNil       bool
		userInHybridPlatforms   bool // Is user in Hybrid Platforms org?
		grantAccessResult       string
		grantAccessError        error
		uploadFileError         error
		expectedMessageContains string
		expectError             bool
	}{
		{
			name:                    "Successful access grant",
			userID:                  "U12345",
			resource:                "gcp-access",
			justification:           "Need to debug CI infrastructure",
			getUserInfoError:        nil,
			userEmail:               "user@example.com",
			orgDataServiceNil:       false,
			userInHybridPlatforms:   true,
			grantAccessResult:       "Access granted successfully",
			grantAccessError:        nil,
			expectedMessageContains: "Access granted successfully",
			expectError:             false,
		},
		{
			name:                    "Missing parameters - both resource and justification empty",
			userID:                  "U12345",
			resource:                "",
			justification:           "",
			getUserInfoError:        nil,
			userEmail:               "user@example.com",
			orgDataServiceNil:       false,
			userInHybridPlatforms:   true,
			grantAccessResult:       "",
			grantAccessError:        nil,
			expectedMessageContains: "Invalid command format",
			expectError:             false,
		},
		{
			name:                    "Invalid resource - rejects non-gcp-access resources",
			userID:                  "U12345",
			resource:                "aws",
			justification:           "Testing",
			getUserInfoError:        nil,
			userEmail:               "user@example.com",
			orgDataServiceNil:       false,
			userInHybridPlatforms:   true,
			grantAccessResult:       "",
			grantAccessError:        nil,
			expectedMessageContains: "only available for the 'gcp-access' resource",
			expectError:             false,
		},
		{
			name:                    "Failed to get user info",
			userID:                  "U12345",
			resource:                "gcp-access",
			justification:           "Testing",
			getUserInfoError:        fmt.Errorf("API error"),
			userEmail:               "",
			orgDataServiceNil:       false,
			userInHybridPlatforms:   true,
			grantAccessResult:       "",
			grantAccessError:        nil,
			expectedMessageContains: "Failed to retrieve your user information",
			expectError:             false,
		},
		{
			name:                    "User email not configured",
			userID:                  "U12345",
			resource:                "gcp-access",
			justification:           "Testing",
			getUserInfoError:        nil,
			userEmail:               "", // Empty email
			orgDataServiceNil:       false,
			userInHybridPlatforms:   true,
			grantAccessResult:       "",
			grantAccessError:        nil,
			expectedMessageContains: "Could not determine your email address",
			expectError:             false,
		},
		{
			name:                    "Organizational data service not available",
			userID:                  "U12345",
			resource:                "gcp-access",
			justification:           "Testing",
			getUserInfoError:        nil,
			userEmail:               "user@example.com",
			orgDataServiceNil:       true, // OrgDataService is nil
			userInHybridPlatforms:   true,
			grantAccessResult:       "",
			grantAccessError:        nil,
			expectedMessageContains: "Organizational data service is not available",
			expectError:             false,
		},
		{
			name:                    "User not in Hybrid Platforms organization - access denied",
			userID:                  "U12345",
			resource:                "gcp-access",
			justification:           "Testing",
			getUserInfoError:        nil,
			userEmail:               "user@example.com",
			orgDataServiceNil:       false,
			userInHybridPlatforms:   false, // Not in Hybrid Platforms
			grantAccessResult:       "",
			grantAccessError:        nil,
			expectedMessageContains: "not a member of the 'Hybrid Platforms' organization",
			expectError:             false,
		},
		{
			name:                    "Grant access fails - GCP API error",
			userID:                  "U12345",
			resource:                "gcp-access",
			justification:           "Testing",
			getUserInfoError:        nil,
			userEmail:               "user@example.com",
			orgDataServiceNil:       false,
			userInHybridPlatforms:   true,
			grantAccessResult:       "",
			grantAccessError:        fmt.Errorf("failed to add user to group"),
			expectedMessageContains: "Failed to grant access",
			expectError:             false,
		},
		{
			name:                    "File upload fails - Slack API error after successful grant",
			userID:                  "U12345",
			resource:                "gcp-access",
			justification:           "Testing file upload failure",
			getUserInfoError:        nil,
			userEmail:               "user@example.com",
			orgDataServiceNil:       false,
			userInHybridPlatforms:   true,
			grantAccessResult:       "Access granted successfully",
			grantAccessError:        nil,
			uploadFileError:         fmt.Errorf("network error"),
			expectedMessageContains: "Failed to upload key file",
			expectError:             false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock Slack client
			mockSlack := &mockSlackClient{
				getUserInfoFunc: func(userID string) (*slack.User, error) {
					if tc.getUserInfoError != nil {
						return nil, tc.getUserInfoError
					}
					return &slack.User{
						ID: userID,
						Profile: slack.UserProfile{
							Email: tc.userEmail,
						},
					}, nil
				},
				uploadFileV2Func: func(params slack.UploadFileV2Parameters) (*slack.FileSummary, error) {
					if tc.uploadFileError != nil {
						return nil, tc.uploadFileError
					}
					// Skip filename verification if no email (for some test cases)
					if tc.userEmail != "" {
						expectedFilename := fmt.Sprintf("gcp-access-%s.json",
							strings.ReplaceAll(strings.ReplaceAll(tc.userEmail, "@", "-"), ".", "-"))
						if params.Filename != expectedFilename {
							t.Errorf("Expected filename %s, got %s", expectedFilename, params.Filename)
						}
					}
					return &slack.FileSummary{ID: "F12345"}, nil
				},
			}

			// Create mock OrgDataService
			var orgDataService manager.OrgDataService
			if !tc.orgDataServiceNil {
				orgDataService = &mockOrgDataService{
					isSlackUserInOrgFunc: func(slackID, orgName string) bool {
						if slackID != tc.userID {
							t.Errorf("Expected slackID %s, got %s", tc.userID, slackID)
						}
						// Return membership for Hybrid Platforms only
						if orgName == "Hybrid Platforms" {
							return tc.userInHybridPlatforms
						}
						return false
					},
				}
			}

			// Create mock JobManager
			mockManager := &mockJobManager{
				getOrgDataServiceFunc: func() manager.OrgDataService {
					return orgDataService
				},
				grantGCPAccessFunc: func(email, slackID, justification, resource string) (string, error) {
					if email != tc.userEmail {
						t.Errorf("Expected email %s, got %s", tc.userEmail, email)
					}
					if slackID != tc.userID {
						t.Errorf("Expected slackID %s, got %s", tc.userID, slackID)
					}
					if justification != tc.justification {
						t.Errorf("Expected justification %s, got %s", tc.justification, justification)
					}
					if resource != tc.resource {
						t.Errorf("Expected resource %s, got %s", tc.resource, resource)
					}
					return tc.grantAccessResult, tc.grantAccessError
				},
			}

			// Create event
			event := &slackevents.MessageEvent{
				User: tc.userID,
			}

			// Create properties with parameters
			properties := parser.NewProperties(map[string]string{
				"resource":      tc.resource,
				"justification": tc.justification,
			})

			// Call the production Request function directly (via wrapper to handle mock interfaces)
			event.Channel = "C12345" // Add channel for file upload
			result := Request(mockSlack, mockManager, event, properties)

			// Verify result
			if !strings.Contains(result, tc.expectedMessageContains) {
				t.Errorf("Expected message to contain '%s', got: %s", tc.expectedMessageContains, result)
			}
		})
	}
}

func TestRevoke(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                    string
		userID                  string
		resource                string
		getUserInfoError        error
		userEmail               string
		orgDataServiceNil       bool
		userInOrg               bool
		revokeAccessResult      string
		revokeAccessError       error
		expectedMessageContains string
		expectError             bool
	}{
		{
			name:                    "Successful access revocation",
			userID:                  "U12345",
			resource:                "gcp-access",
			getUserInfoError:        nil,
			userEmail:               "user@example.com",
			orgDataServiceNil:       false,
			userInOrg:               true,
			revokeAccessResult:      "Access revoked successfully",
			revokeAccessError:       nil,
			expectedMessageContains: "Access revoked successfully",
			expectError:             false,
		},
		{
			name:                    "Missing parameters - resource empty",
			userID:                  "U12345",
			resource:                "",
			getUserInfoError:        nil,
			userEmail:               "user@example.com",
			orgDataServiceNil:       false,
			userInOrg:               true,
			revokeAccessResult:      "",
			revokeAccessError:       nil,
			expectedMessageContains: "Invalid command format",
			expectError:             false,
		},
		{
			name:                    "Invalid resource - rejects non-gcp-access resources",
			userID:                  "U12345",
			resource:                "aws",
			getUserInfoError:        nil,
			userEmail:               "user@example.com",
			orgDataServiceNil:       false,
			userInOrg:               true,
			revokeAccessResult:      "",
			revokeAccessError:       nil,
			expectedMessageContains: "only available for the 'gcp-access' resource",
			expectError:             false,
		},
		{
			name:                    "Failed to get user info",
			userID:                  "U12345",
			resource:                "gcp-access",
			getUserInfoError:        fmt.Errorf("API error"),
			userEmail:               "",
			orgDataServiceNil:       false,
			userInOrg:               true,
			revokeAccessResult:      "",
			revokeAccessError:       nil,
			expectedMessageContains: "Failed to retrieve your user information",
			expectError:             false,
		},
		{
			name:                    "User email not configured",
			userID:                  "U12345",
			resource:                "gcp-access",
			getUserInfoError:        nil,
			userEmail:               "",
			orgDataServiceNil:       false,
			userInOrg:               true,
			revokeAccessResult:      "",
			revokeAccessError:       nil,
			expectedMessageContains: "Could not determine your email address",
			expectError:             false,
		},
		{
			name:                    "Organizational data service not available",
			userID:                  "U12345",
			resource:                "gcp-access",
			getUserInfoError:        nil,
			userEmail:               "user@example.com",
			orgDataServiceNil:       true,
			userInOrg:               true,
			revokeAccessResult:      "",
			revokeAccessError:       nil,
			expectedMessageContains: "Organizational data service is not available",
			expectError:             false,
		},
		{
			name:                    "User not in Hybrid Platforms organization - access denied",
			userID:                  "U12345",
			resource:                "gcp-access",
			getUserInfoError:        nil,
			userEmail:               "user@example.com",
			orgDataServiceNil:       false,
			userInOrg:               false,
			revokeAccessResult:      "",
			revokeAccessError:       nil,
			expectedMessageContains: "only available to members of the Hybrid Platforms organization",
			expectError:             false,
		},
		{
			name:                    "Revoke access fails - GCP API error",
			userID:                  "U12345",
			resource:                "gcp-access",
			getUserInfoError:        nil,
			userEmail:               "user@example.com",
			orgDataServiceNil:       false,
			userInOrg:               true,
			revokeAccessResult:      "",
			revokeAccessError:       fmt.Errorf("failed to remove user from group"),
			expectedMessageContains: "Failed to revoke access",
			expectError:             false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock Slack client
			mockSlack := &mockSlackClient{
				getUserInfoFunc: func(userID string) (*slack.User, error) {
					if tc.getUserInfoError != nil {
						return nil, tc.getUserInfoError
					}
					return &slack.User{
						ID: userID,
						Profile: slack.UserProfile{
							Email: tc.userEmail,
						},
					}, nil
				},
				// Revoke doesn't upload files, so no need for uploadFileV2Func
			}

			// Create mock OrgDataService
			var orgDataService manager.OrgDataService
			if !tc.orgDataServiceNil {
				orgDataService = &mockOrgDataService{
					isSlackUserInOrgFunc: func(slackID, orgName string) bool {
						if slackID != tc.userID {
							t.Errorf("Expected slackID %s, got %s", tc.userID, slackID)
						}
						if orgName != "Hybrid Platforms" {
							t.Errorf("Expected orgName 'Hybrid Platforms', got %s", orgName)
						}
						return tc.userInOrg
					},
				}
			}

			// Create mock JobManager
			mockManager := &mockJobManager{
				getOrgDataServiceFunc: func() manager.OrgDataService {
					return orgDataService
				},
				revokeGCPAccessFunc: func(email, slackID string) (string, error) {
					if email != tc.userEmail {
						t.Errorf("Expected email %s, got %s", tc.userEmail, email)
					}
					if slackID != tc.userID {
						t.Errorf("Expected slackID %s, got %s", tc.userID, slackID)
					}
					return tc.revokeAccessResult, tc.revokeAccessError
				},
			}

			// Create event
			event := &slackevents.MessageEvent{
				User: tc.userID,
			}

			// Create properties with parameters
			properties := parser.NewProperties(map[string]string{
				"resource": tc.resource,
			})

			// Call the function
			result := Revoke(mockSlack, mockManager, event, properties)

			// Verify result
			if !strings.Contains(result, tc.expectedMessageContains) {
				t.Errorf("Expected message to contain '%s', got: %s", tc.expectedMessageContains, result)
			}
		})
	}
}

// TestRequestValidatesOrganization ensures organization check happens before grant
func TestRequestValidatesOrganization(t *testing.T) {
	t.Parallel()

	mockSlack := &mockSlackClient{
		getUserInfoFunc: func(userID string) (*slack.User, error) {
			return &slack.User{
				ID: userID,
				Profile: slack.UserProfile{
					Email: "user@example.com",
				},
			}, nil
		},
	}

	orgCheckCalled := false
	grantCalled := false

	orgDataService := &mockOrgDataService{
		isSlackUserInOrgFunc: func(slackID, orgName string) bool {
			orgCheckCalled = true
			return false // User not in org
		},
	}

	mockManager := &mockJobManager{
		getOrgDataServiceFunc: func() manager.OrgDataService {
			return orgDataService
		},
		grantGCPAccessFunc: func(email, slackID, justification, resource string) (string, error) {
			grantCalled = true
			return "Should not be called", nil
		},
	}

	event := &slackevents.MessageEvent{
		User: "U12345",
	}

	properties := parser.NewProperties(map[string]string{
		"org":           "Hybrid Platforms",
		"resource":      "gcp-access",
		"justification": "Testing",
	})

	event.Channel = "C12345"
	result := Request(mockSlack, mockManager, event, properties)

	// Verify organization check was called
	if !orgCheckCalled {
		t.Error("Organization check should have been called")
	}

	// Verify grant was NOT called (because org check failed)
	if grantCalled {
		t.Error("Grant should not have been called when user is not in organization")
	}

	// Verify error message
	if !strings.Contains(result, "not a member of the 'Hybrid Platforms' organization") {
		t.Errorf("Expected organization membership error, got: %s", result)
	}
}

// TestRevokeValidatesOrganization ensures organization check happens before revoke
func TestRevokeValidatesOrganization(t *testing.T) {
	t.Parallel()

	mockSlack := &mockSlackClient{
		getUserInfoFunc: func(userID string) (*slack.User, error) {
			return &slack.User{
				ID: userID,
				Profile: slack.UserProfile{
					Email: "user@example.com",
				},
			}, nil
		},
	}

	orgCheckCalled := false
	revokeCalled := false

	orgDataService := &mockOrgDataService{
		isSlackUserInOrgFunc: func(slackID, orgName string) bool {
			orgCheckCalled = true
			return false // User not in org
		},
	}

	mockManager := &mockJobManager{
		getOrgDataServiceFunc: func() manager.OrgDataService {
			return orgDataService
		},
		revokeGCPAccessFunc: func(email, slackID string) (string, error) {
			revokeCalled = true
			return "Should not be called", nil
		},
	}

	event := &slackevents.MessageEvent{
		User: "U12345",
	}

	properties := parser.NewProperties(map[string]string{
		"resource": "gcp-access",
	})

	result := Revoke(mockSlack, mockManager, event, properties)

	// Verify organization check was called
	if !orgCheckCalled {
		t.Error("Organization check should have been called")
	}

	// Verify revoke was NOT called (because org check failed)
	if revokeCalled {
		t.Error("Revoke should not have been called when user is not in organization")
	}

	// Verify error message
	if !strings.Contains(result, "only available to members of the Hybrid Platforms organization") {
		t.Errorf("Expected organization membership error, got: %s", result)
	}
}

// TestIsUserInOrg_SlackIDLookup tests organization validation via Slack ID
func TestIsUserInOrg_SlackIDLookup(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		slackID        string
		email          string
		org            string
		slackUserInOrg bool
		expected       bool
	}{
		{
			name:           "User found by Slack ID",
			slackID:        "U12345",
			email:          "user@example.com",
			slackUserInOrg: true,
			expected:       true,
		},
		{
			name:           "User not found by Slack ID",
			slackID:        "U12345",
			email:          "user@example.com",
			slackUserInOrg: false,
			expected:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			orgDataService := &mockOrgDataService{
				isSlackUserInOrgFunc: func(slackID, orgName string) bool {
					if slackID != tc.slackID {
						t.Errorf("Expected slackID %s, got %s", tc.slackID, slackID)
					}
					if orgName != tc.org {
						t.Errorf("Expected org %s, got %s", tc.org, orgName)
					}
					return tc.slackUserInOrg
				},
			}

			result := isUserInOrg(orgDataService, tc.slackID, tc.email, tc.org)
			if result != tc.expected {
				t.Errorf("Expected %v, got %v", tc.expected, result)
			}
		})
	}
}

// TestIsUserInOrg_EmailFallback tests fallback to email-based lookup
func TestIsUserInOrg_EmailFallback(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		slackID       string
		email         string
		org           string
		employee      *orgdatacore.Employee
		employeeInOrg bool
		expected      bool
	}{
		{
			name:    "Slack ID not found, email found, user in org",
			slackID: "U12345",
			email:   "user@example.com",
			employee: &orgdatacore.Employee{
				UID:   "employee123",
				Email: "user@example.com",
			},
			employeeInOrg: true,
			expected:      true,
		},
		{
			name:    "Slack ID not found, email found, user not in org",
			slackID: "U12345",
			email:   "user@example.com",
			employee: &orgdatacore.Employee{
				UID:   "employee123",
				Email: "user@example.com",
			},
			employeeInOrg: false,
			expected:      false,
		},
		{
			name:          "Slack ID not found, email not found",
			slackID:       "U12345",
			email:         "notfound@example.com",
			employee:      nil,
			employeeInOrg: false,
			expected:      false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			orgDataService := &mockOrgDataService{
				isSlackUserInOrgFunc: func(slackID, orgName string) bool {
					// Slack ID lookup always fails in these tests
					return false
				},
				getEmployeeByEmailFunc: func(email string) *orgdatacore.Employee {
					if email != tc.email {
						t.Errorf("Expected email %s, got %s", tc.email, email)
					}
					return tc.employee
				},
				isEmployeeInOrgFunc: func(uid, orgName string) bool {
					if tc.employee == nil {
						t.Error("IsEmployeeInOrg should not be called when employee is nil")
						return false
					}
					if uid != tc.employee.UID {
						t.Errorf("Expected UID %s, got %s", tc.employee.UID, uid)
					}
					if orgName != tc.org {
						t.Errorf("Expected org %s, got %s", tc.org, orgName)
					}
					return tc.employeeInOrg
				},
			}

			result := isUserInOrg(orgDataService, tc.slackID, tc.email, tc.org)
			if result != tc.expected {
				t.Errorf("Expected %v, got %v", tc.expected, result)
			}
		})
	}
}

// TestIsUserInOrg_DualLookupPreference tests that Slack ID lookup is tried first
func TestIsUserInOrg_DualLookupPreference(t *testing.T) {
	t.Parallel()

	slackIDCalled := false
	emailCalled := false

	orgDataService := &mockOrgDataService{
		isSlackUserInOrgFunc: func(slackID, orgName string) bool {
			slackIDCalled = true
			return true // Found via Slack ID
		},
		getEmployeeByEmailFunc: func(email string) *orgdatacore.Employee {
			emailCalled = true
			return &orgdatacore.Employee{UID: "employee123", Email: email}
		},
	}

	result := isUserInOrg(orgDataService, "U12345", "user@example.com", "test-org")

	// Should return true
	if !result {
		t.Error("Expected true when Slack ID lookup succeeds")
	}

	// Should have called Slack ID lookup
	if !slackIDCalled {
		t.Error("Expected Slack ID lookup to be called")
	}

	// Should NOT have called email lookup (short-circuit)
	if emailCalled {
		t.Error("Expected email lookup to NOT be called when Slack ID succeeds")
	}
}

// TestIsUserInOrg_MultipleOrganizations tests that the function works correctly with different organization names
func TestIsUserInOrg_MultipleOrganizations(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		slackID     string
		email       string
		org         string
		userOrgs    map[string]bool // Map of org names the user is a member of
		expected    bool
		expectedOrg string // The org name that should be checked
	}{
		{
			name:    "User in Hybrid Platforms organization",
			slackID: "U12345",
			email:   "user@example.com",
			org:     "Hybrid Platforms",
			userOrgs: map[string]bool{
				"Hybrid Platforms": true,
			},
			expected:    true,
			expectedOrg: "Hybrid Platforms",
		},
		{
			name:    "User in different organization",
			slackID: "U12345",
			email:   "user@example.com",
			org:     "Engineering",
			userOrgs: map[string]bool{
				"Engineering": true,
			},
			expected:    true,
			expectedOrg: "Engineering",
		},
		{
			name:    "User not in requested organization",
			slackID: "U12345",
			email:   "user@example.com",
			org:     "Sales",
			userOrgs: map[string]bool{
				"Engineering": true,
			},
			expected:    false,
			expectedOrg: "Sales",
		},
		{
			name:    "User in multiple organizations - checking one they're in",
			slackID: "U12345",
			email:   "user@example.com",
			org:     "Engineering",
			userOrgs: map[string]bool{
				"Hybrid Platforms": true,
				"Engineering":      true,
				"Product":          true,
			},
			expected:    true,
			expectedOrg: "Engineering",
		},
		{
			name:    "User in multiple organizations - checking one they're not in",
			slackID: "U12345",
			email:   "user@example.com",
			org:     "Sales",
			userOrgs: map[string]bool{
				"Hybrid Platforms": true,
				"Engineering":      true,
				"Product":          true,
			},
			expected:    false,
			expectedOrg: "Sales",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			orgDataService := &mockOrgDataService{
				isSlackUserInOrgFunc: func(slackID, orgName string) bool {
					if slackID != tc.slackID {
						t.Errorf("Expected slackID %s, got %s", tc.slackID, slackID)
					}
					if orgName != tc.expectedOrg {
						t.Errorf("Expected org %s, got %s", tc.expectedOrg, orgName)
					}
					// Check if user is in the requested organization
					return tc.userOrgs[orgName]
				},
			}

			result := isUserInOrg(orgDataService, tc.slackID, tc.email, tc.org)
			if result != tc.expected {
				t.Errorf("Expected %v for org %s, got %v", tc.expected, tc.org, result)
			}
		})
	}
}
