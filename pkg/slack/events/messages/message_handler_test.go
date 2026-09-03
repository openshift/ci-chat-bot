package messages

import (
	"fmt"
	"strings"
	"testing"

	orgdatacore "github.com/openshift-eng/cyborg-data/go"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	chatmetrics "github.com/openshift/ci-chat-bot/pkg/metrics"
	"github.com/openshift/ci-chat-bot/pkg/slack/parser"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// Mock bot commands for testing
var mockBotCommands = []parser.BotCommand{
	parser.NewBotCommand("launch <image_or_version_or_prs> <options>", &parser.CommandDefinition{
		Description: "Launch an OpenShift cluster",
		Example:     "launch 4.19 aws",
		Handler:     mockHandler,
	}, false),
	parser.NewBotCommand("rosa create <version> <duration>", &parser.CommandDefinition{
		Description: "Create a ROSA cluster",
		Example:     "rosa create 4.19 3h",
		Handler:     mockHandler,
	}, false),
	parser.NewBotCommand("list", &parser.CommandDefinition{
		Description: "List active clusters",
		Handler:     mockHandler,
	}, false),
	parser.NewBotCommand("test <name> <image_or_version_or_prs> <options>", &parser.CommandDefinition{
		Description: "Run test suite",
		Example:     "test e2e 4.19 aws",
		Handler:     mockHandler,
	}, false),
	parser.NewBotCommand("build <pullrequest>", &parser.CommandDefinition{
		Description: "Build image from PR",
		Example:     "build openshift/installer#123",
		Handler:     mockHandler,
	}, false),
	parser.NewBotCommand("mce create <version> <duration> <platform>", &parser.CommandDefinition{
		Description: "Create MCE cluster",
		Example:     "mce create 4.19 6h aws",
		Handler:     mockHandler,
	}, true), // private command
}

func mockHandler(client parser.SlackClient, manager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties) string {
	return "mock response"
}

type commandActivityRecorder struct {
	command     string
	slackUserID string
	membership  string
}

func (r *commandActivityRecorder) RecordCommand(command, slackUserID, membership string) {
	r.command = command
	r.slackUserID = slackUserID
	r.membership = membership
}

func (r *commandActivityRecorder) RecordGCPAccessOutcome(string, string, string) {}

type commandActivityOrgDataService struct {
	manager.OrgDataService
	employee        *orgdatacore.Employee
	employeeByEmail *orgdatacore.Employee
	inOrg           bool
}

func (s *commandActivityOrgDataService) GetEmployeeBySlackID(string) *orgdatacore.Employee {
	return s.employee
}

func (s *commandActivityOrgDataService) GetEmployeeByEmail(string) *orgdatacore.Employee {
	return s.employeeByEmail
}

func (s *commandActivityOrgDataService) IsEmployeeInOrg(string, string) bool {
	return s.inOrg
}

type commandActivitySlackClient struct {
	user    *slack.User
	err     error
	lookups int
}

func (c *commandActivitySlackClient) GetUserInfo(string) (*slack.User, error) {
	c.lookups++
	return c.user, c.err
}

func (c *commandActivitySlackClient) PostMessage(string, ...slack.MsgOption) (string, string, error) {
	return "", "", fmt.Errorf("unexpected PostMessage call")
}

func (c *commandActivitySlackClient) UploadFile(slack.UploadFileParameters) (*slack.FileSummary, error) {
	return nil, fmt.Errorf("unexpected UploadFile call")
}

type commandActivityJobManager struct {
	manager.JobManager
	orgDataService manager.OrgDataService
}

func (m *commandActivityJobManager) GetOrgDataService() manager.OrgDataService {
	return m.orgDataService
}

func TestExecuteCommandRecordsActivity(t *testing.T) {
	tests := []struct {
		name       string
		employee   *orgdatacore.Employee
		inOrg      bool
		membership string
	}{
		{name: "member", employee: &orgdatacore.Employee{UID: "employee123"}, inOrg: true, membership: chatmetrics.MembershipMember},
		{name: "non-member", employee: &orgdatacore.Employee{UID: "employee123"}, membership: chatmetrics.MembershipNonMember},
		{name: "unknown", membership: chatmetrics.MembershipUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &commandActivityRecorder{}
			jobManager := &commandActivityJobManager{
				orgDataService: &commandActivityOrgDataService{
					employee: test.employee,
					inOrg:    test.inOrg,
				},
			}
			var gotContext *parser.CommandExecutionContext
			command := parser.NewBotCommand("request <resource?> <justification?>", &parser.CommandDefinition{
				Handler: mockHandler,
				ContextualHandler: func(client parser.SlackClient, manager manager.JobManager, event *slackevents.MessageEvent, properties *parser.Properties, context *parser.CommandExecutionContext) string {
					gotContext = context
					return "mock response"
				},
			}, false)
			event := &slackevents.MessageEvent{
				User: "U12345",
				Text: `request gcp-access "justification@example.com"`,
			}

			if got := executeCommand(nil, jobManager, command, event, parser.NewProperties(nil), recorder); got != "mock response" {
				t.Fatalf("executeCommand() = %q, want mock response", got)
			}
			if recorder.command != "request-gcp-access" {
				t.Errorf("command = %q, want request-gcp-access", recorder.command)
			}
			if recorder.slackUserID != event.User {
				t.Errorf("slack user ID = %q, want %q", recorder.slackUserID, event.User)
			}
			if recorder.membership != test.membership {
				t.Errorf("membership = %q, want %q", recorder.membership, test.membership)
			}
			if gotContext == nil {
				t.Fatal("execution context was nil")
			}
			if gotContext.Membership != test.membership {
				t.Errorf("execution context membership = %q, want %q", gotContext.Membership, test.membership)
			}
			if gotContext.GCPAccessOutcomeRecorder == nil {
				t.Error("execution context did not receive the outcome recorder")
			}
		})
	}
}

func TestExecuteCommandUsesEmailFallbackForActivity(t *testing.T) {
	recorder := &commandActivityRecorder{}
	jobManager := &commandActivityJobManager{
		orgDataService: &commandActivityOrgDataService{
			employeeByEmail: &orgdatacore.Employee{UID: "employee123"},
		},
	}
	client := &commandActivitySlackClient{
		user: &slack.User{Profile: slack.UserProfile{Email: "person@example.com"}},
	}
	command := parser.NewBotCommand("list", &parser.CommandDefinition{Handler: mockHandler}, false)
	event := &slackevents.MessageEvent{User: "U12345", Text: "list"}

	if got := executeCommand(client, jobManager, command, event, parser.NewProperties(nil), recorder); got != "mock response" {
		t.Fatalf("executeCommand() = %q, want mock response", got)
	}
	if recorder.membership != chatmetrics.MembershipNonMember {
		t.Errorf("membership = %q, want %q", recorder.membership, chatmetrics.MembershipNonMember)
	}
	if client.lookups != 1 {
		t.Errorf("Slack user lookups = %d, want 1", client.lookups)
	}
}

func TestFindCommandSuggestion(t *testing.T) {
	testCases := []struct {
		name         string
		input        string
		allowPrivate bool
		expected     string
	}{
		{
			name:     "Exact category match",
			input:    "launch",
			expected: "launch",
		},
		{
			name:     "Partial category match",
			input:    "laun",
			expected: "launch",
		},
		{
			name:     "Rosa category",
			input:    "ros",
			expected: "rosa",
		},
		{
			name:     "Test category",
			input:    "test",
			expected: "test",
		},
		{
			name:     "Build category",
			input:    "buil",
			expected: "build",
		},
		{
			name:     "Management category",
			input:    "manage",
			expected: "manage",
		},
		{
			name:         "MCE category with permission",
			input:        "mce",
			allowPrivate: true,
			expected:     "mce",
		},
		{
			name:         "MCE category without permission",
			input:        "mce",
			allowPrivate: false,
			expected:     "",
		},
		{
			name:     "Individual command match",
			input:    "lis",
			expected: "list",
		},
		{
			name:     "No match",
			input:    "xyz",
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := findCommandSuggestion(tc.input, mockBotCommands, tc.allowPrivate)
			if result != tc.expected {
				t.Errorf("Expected '%s', got '%s'", tc.expected, result)
			}
		})
	}
}

func TestHelpOverview(t *testing.T) {
	// Test the help overview message generation directly
	message := GenerateHelpOverviewMessage(false)

	// Check that overview contains expected elements
	expectedContent := []string{
		"🤖 Cluster Bot - Quick Start",
		"Most Used Commands:",
		"help launch",
		"help rosa",
		"All Commands:",
		"Category Help:",
		"Examples:",
		"launch 4.19 aws",
	}

	for _, expected := range expectedContent {
		if !strings.Contains(message, expected) {
			t.Errorf("Expected message to contain '%s', got: %s", expected, message)
		}
	}

	// Should not contain MCE when not private
	if strings.Contains(message, "help mce") {
		t.Errorf("Should not contain MCE help for non-private user")
	}
}

func TestHelpOverviewWithPrivate(t *testing.T) {
	// Test the help overview message generation with private commands
	message := GenerateHelpOverviewMessage(true)

	// Should contain MCE when private
	if !strings.Contains(message, "help mce") {
		t.Errorf("Should contain MCE help for private user, got: %s", message)
	}
}

func TestHelpSpecificLaunch(t *testing.T) {
	// Test the launch help message generation directly
	message := GenerateLaunchHelpMessage()

	// Check launch-specific content
	expectedContent := []string{
		"🚀 Cluster Launching",
		"launch",
		"Launch an OpenShift cluster",
		"launch 4.19 aws",
	}

	for _, expected := range expectedContent {
		if !strings.Contains(message, expected) {
			t.Errorf("Expected message to contain '%s', got: %s", expected, message)
		}
	}

	// Should not contain ROSA commands
	if strings.Contains(message, "rosa create") {
		t.Errorf("Launch help should not contain ROSA commands")
	}
}

func TestHelpSpecificRosa(t *testing.T) {
	// Test the ROSA help message generation directly
	message := GenerateRosaHelpMessage()

	// Check ROSA-specific content
	expectedContent := []string{
		"☁️ ROSA",
		"rosa create",
		"Create a ROSA cluster",
		"rosa create 4.19 3h",
	}

	for _, expected := range expectedContent {
		if !strings.Contains(message, expected) {
			t.Errorf("Expected message to contain '%s', got: %s", expected, message)
		}
	}
}

func TestHelpSpecificUnknownTopic(t *testing.T) {
	// Test that findCommandSuggestion returns empty for truly unknown topics
	suggestion := findCommandSuggestion("unknown", mockBotCommands, false)
	if suggestion != "" {
		t.Errorf("Expected no suggestion for 'unknown', got '%s'", suggestion)
	}
}

func TestHelpSpecificWithSuggestion(t *testing.T) {
	// Test that findCommandSuggestion works for partial matches
	suggestion := findCommandSuggestion("laun", mockBotCommands, false)
	if suggestion != "launch" {
		t.Errorf("Expected 'launch' suggestion for 'laun', got '%s'", suggestion)
	}
}

func TestGenerateTestHelpMessage(t *testing.T) {
	// Test the test help message generation
	message := GenerateTestHelpMessage()

	expectedContent := []string{
		"🧪 Testing & Workflows",
		"test",
		"test upgrade",
		"workflow-test",
	}

	for _, expected := range expectedContent {
		if !strings.Contains(message, expected) {
			t.Errorf("Expected message to contain '%s'", expected)
		}
	}
}

func TestGenerateBuildHelpMessage(t *testing.T) {
	// Test the build help message generation
	message := GenerateBuildHelpMessage()

	expectedContent := []string{
		"🔨 Building Images",
		"build",
		"catalog build",
	}

	for _, expected := range expectedContent {
		if !strings.Contains(message, expected) {
			t.Errorf("Expected message to contain '%s'", expected)
		}
	}
}

func TestGenerateManageHelpMessage(t *testing.T) {
	// Test the manage help message generation
	message := GenerateManageHelpMessage()

	expectedContent := []string{
		"⚙️ Cluster Management",
		"list",
		"done",
		"auth",
		"refresh",
		"version",
	}

	for _, expected := range expectedContent {
		if !strings.Contains(message, expected) {
			t.Errorf("Expected message to contain '%s'", expected)
		}
	}
}

func TestGenerateMceHelpMessage(t *testing.T) {
	// Test the MCE help message generation
	message := GenerateMceHelpMessage()

	expectedContent := []string{
		"🏢 MCE",
		"mce create",
		"mce describe",
		"Multi-cluster management",
		"special authorization",
	}

	for _, expected := range expectedContent {
		if !strings.Contains(message, expected) {
			t.Errorf("Expected message to contain '%s'", expected)
		}
	}
}
