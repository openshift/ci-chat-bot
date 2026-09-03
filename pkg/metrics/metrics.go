// Package metrics contains application metrics for ci-chat-bot.
package metrics

import (
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	// CommandExecutionsMetricName is the counter for recognized command volume.
	CommandExecutionsMetricName = "ci_chat_bot_command_executions_total"
	// UserCommandActivityMetricName is the counter for command volume by Slack user.
	UserCommandActivityMetricName = "ci_chat_bot_user_command_activity_total"
	// GCPAccessRequestsMetricName is the counter for known GCP access outcomes.
	GCPAccessRequestsMetricName = "ci_chat_bot_gcp_access_requests_total"
)

const (
	// MembershipMember identifies a confirmed Hybrid Platforms member.
	MembershipMember = "member"
	// MembershipNonMember identifies a resolved employee outside Hybrid Platforms.
	MembershipNonMember = "non_member"
	// MembershipUnknown identifies an unresolved or unavailable membership classification.
	MembershipUnknown = "unknown"
)

const (
	// GCPAccessOutcomeGranted identifies a successful access grant.
	GCPAccessOutcomeGranted = "granted"
	// GCPAccessOutcomeDenied identifies a confirmed non-member denial.
	GCPAccessOutcomeDenied = "denied"
	// GCPAccessOutcomeGrantError identifies a failed grant operation.
	GCPAccessOutcomeGrantError = "grant_error"
)

// Membership is the classification used for organizational membership
// metrics. Unknown means that the organizational data did not identify the
// user; it must not be treated as a confirmed non-member.
type Membership string

// CommandRecorder records accepted command activity.
type CommandRecorder interface {
	RecordCommand(command, slackUserID, membership string)
}

// GCPAccessOutcomeRecorder records the result of a GCP access request.
type GCPAccessOutcomeRecorder interface {
	RecordGCPAccessOutcome(slackUserID, membership, outcome string)
}

// NoopRecorder is a recorder for tests and contexts where metrics are not
// required.
type NoopRecorder struct{}

func (NoopRecorder) RecordCommand(string, string, string) {}

func (NoopRecorder) RecordGCPAccessOutcome(string, string, string) {}

// Metrics contains the command usage collectors. It is safe for concurrent
// use by Slack event handlers.
type Metrics struct {
	commandExecutions   *prometheus.CounterVec
	userCommandActivity *prometheus.CounterVec
	gcpAccessRequests   *prometheus.CounterVec
}

// New constructs and registers the ci-chat-bot usage collectors with
// registerer. Passing nil uses Prometheus' default registerer.
func New(registerer prometheus.Registerer) (*Metrics, error) {
	if registerer == nil {
		registerer = prometheus.DefaultRegisterer
	}

	commandExecutions, err := registerCounterVec(registerer, prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: CommandExecutionsMetricName,
			Help: "Total number of recognized ci-chat-bot action commands executed.",
		},
		[]string{"command", "hybrid_platforms_membership"},
	))
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", CommandExecutionsMetricName, err)
	}

	userCommandActivity, err := registerCounterVec(registerer, prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: UserCommandActivityMetricName,
			Help: "Total number of recognized ci-chat-bot action commands by Slack user.",
		},
		[]string{"slack_user_id", "command", "hybrid_platforms_membership"},
	))
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", UserCommandActivityMetricName, err)
	}

	gcpAccessRequests, err := registerCounterVec(registerer, prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: GCPAccessRequestsMetricName,
			Help: "Total number of GCP access requests and their outcomes by Slack user.",
		},
		[]string{"slack_user_id", "membership", "outcome"},
	))
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", GCPAccessRequestsMetricName, err)
	}

	return &Metrics{
		commandExecutions:   commandExecutions,
		userCommandActivity: userCommandActivity,
		gcpAccessRequests:   gcpAccessRequests,
	}, nil
}

// NewCommandMetrics is an explicit alias for New for callers that want to
// make the command-usage purpose clear at the call site.
func NewCommandMetrics(registerer prometheus.Registerer) (*Metrics, error) {
	return New(registerer)
}

// RecordCommand records one recognized command. The command and membership
// values are accepted only from the bounded sets used by this package.
func (m *Metrics) RecordCommand(command, slackUserID, membership string) {
	if m == nil || !validCommand(command) || !validMembership(membership) {
		return
	}
	m.commandExecutions.WithLabelValues(command, membership).Inc()
	m.userCommandActivity.WithLabelValues(slackUserID, command, membership).Inc()
}

// RecordGCPAccessOutcome records a known GCP access request outcome. Requests
// whose membership is unknown are intentionally omitted because they cannot
// be classified as confirmed grants or denials.
func (m *Metrics) RecordGCPAccessOutcome(slackUserID, membership, outcome string) {
	if m == nil || membership == MembershipUnknown || !validMembership(membership) || !validGCPAccessOutcome(outcome) {
		return
	}
	m.gcpAccessRequests.WithLabelValues(slackUserID, membership, outcome).Inc()
}

// NormalizeCommand converts a command definition and message into a bounded
// command label. Only literal command words from the definition are used;
// message arguments are inspected only for the exact gcp-access resource.
func NormalizeCommand(usage, message string) string {
	usageWords := strings.Fields(strings.ToLower(usage))
	literals := make([]string, 0, len(usageWords))
	for _, word := range usageWords {
		if strings.HasPrefix(word, "<") {
			break
		}
		literals = append(literals, word)
	}

	command := strings.Join(literals, "-")
	if command == "request" || command == "revoke" {
		messageWords := strings.Fields(strings.ToLower(strings.TrimSpace(message)))
		if len(messageWords) > 1 && strings.Trim(messageWords[1], "\"'") == "gcp-access" {
			command += "-gcp-access"
		}
	}
	if !validCommand(command) {
		return "unknown"
	}
	return command
}

func registerCounterVec(registerer prometheus.Registerer, collector *prometheus.CounterVec) (*prometheus.CounterVec, error) {
	if err := registerer.Register(collector); err != nil {
		alreadyRegistered, ok := err.(prometheus.AlreadyRegisteredError)
		if !ok {
			return nil, err
		}
		existing, ok := alreadyRegistered.ExistingCollector.(*prometheus.CounterVec)
		if !ok {
			return nil, fmt.Errorf("existing collector has type %T, want *prometheus.CounterVec", alreadyRegistered.ExistingCollector)
		}
		return existing, nil
	}
	return collector, nil
}

func validMembership(membership string) bool {
	switch membership {
	case MembershipMember, MembershipNonMember, MembershipUnknown:
		return true
	default:
		return false
	}
}

func validGCPAccessOutcome(outcome string) bool {
	switch outcome {
	case GCPAccessOutcomeGranted, GCPAccessOutcomeDenied, GCPAccessOutcomeGrantError:
		return true
	default:
		return false
	}
}

func validCommand(command string) bool {
	switch command {
	case "launch", "rosa-create", "rosa-lookup", "rosa-describe",
		"list", "done", "refresh", "auth", "test-upgrade", "test", "build",
		"workflow-launch", "workflow-test", "workflow-upgrade", "version", "lookup",
		"catalog-build", "mce-create", "mce-auth", "mce-delete", "mce-list", "mce-lookup",
		"request", "request-gcp-access", "revoke", "revoke-gcp-access", "unknown":
		return true
	default:
		return false
	}
}
