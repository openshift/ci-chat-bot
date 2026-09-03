package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestNormalizeCommand(t *testing.T) {
	tests := []struct {
		name    string
		usage   string
		message string
		want    string
	}{
		{
			name:    "launch uses literal command name",
			usage:   "launch <image_or_version_or_prs> <options>",
			message: "launch 4.19 aws,justification@example.com",
			want:    "launch",
		},
		{
			name:    "request gcp access has dedicated label",
			usage:   "request <resource?> <justification?>",
			message: `request gcp-access "Need help from user@example.com"`,
			want:    "request-gcp-access",
		},
		{
			name:    "other request resource remains bounded",
			usage:   "request <resource?> <justification?>",
			message: `request aws "user@example.com"`,
			want:    "request",
		},
		{
			name:    "revoke gcp access has dedicated label",
			usage:   "revoke <resource?>",
			message: "revoke gcp-access",
			want:    "revoke-gcp-access",
		},
		{
			name:    "unknown usage is bounded",
			usage:   "future-command <argument>",
			message: "future-command email@example.com justification",
			want:    "unknown",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NormalizeCommand(test.usage, test.message); got != test.want {
				t.Fatalf("NormalizeCommand() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestMetricsRecordCommand(t *testing.T) {
	registry := prometheus.NewRegistry()
	usageMetrics, err := New(registry)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	usageMetrics.RecordCommand("request-gcp-access", "U12345", MembershipNonMember)
	usageMetrics.RecordCommand("request gcp-access user@example.com", "U12345", MembershipNonMember)
	usageMetrics.RecordCommand("request-gcp-access", "U12345", "not-a-membership")

	if got := counterValue(t, registry, CommandExecutionsMetricName, map[string]string{
		"command":                     "request-gcp-access",
		"hybrid_platforms_membership": MembershipNonMember,
	}); got != 1 {
		t.Fatalf("command counter = %v, want 1", got)
	}
	if got := counterValue(t, registry, UserCommandActivityMetricName, map[string]string{
		"slack_user_id":               "U12345",
		"command":                     "request-gcp-access",
		"hybrid_platforms_membership": MembershipNonMember,
	}); got != 1 {
		t.Fatalf("user activity counter = %v, want 1", got)
	}

	if metricHasLabelValue(t, registry, UserCommandActivityMetricName, "slack_user_id", "user@example.com") {
		t.Fatal("email unexpectedly appeared as a user label")
	}
}

func TestMetricsRecordGCPAccessOutcome(t *testing.T) {
	registry := prometheus.NewRegistry()
	usageMetrics, err := New(registry)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	usageMetrics.RecordGCPAccessOutcome("U12345", MembershipMember, GCPAccessOutcomeGranted)
	usageMetrics.RecordGCPAccessOutcome("U67890", MembershipNonMember, GCPAccessOutcomeDenied)
	usageMetrics.RecordGCPAccessOutcome("U99999", MembershipUnknown, GCPAccessOutcomeDenied)

	if got := counterValue(t, registry, GCPAccessRequestsMetricName, map[string]string{
		"slack_user_id": "U67890",
		"membership":    MembershipNonMember,
		"outcome":       GCPAccessOutcomeDenied,
	}); got != 1 {
		t.Fatalf("denial counter = %v, want 1", got)
	}
	if got := counterValue(t, registry, GCPAccessRequestsMetricName, map[string]string{
		"slack_user_id": "U99999",
		"membership":    MembershipUnknown,
		"outcome":       GCPAccessOutcomeDenied,
	}); got != 0 {
		t.Fatalf("unknown membership was counted as denial with value %v", got)
	}
}

func TestNewReusesAlreadyRegisteredCollectors(t *testing.T) {
	registry := prometheus.NewRegistry()
	first, err := New(registry)
	if err != nil {
		t.Fatalf("first New() failed: %v", err)
	}
	second, err := New(registry)
	if err != nil {
		t.Fatalf("second New() failed: %v", err)
	}

	first.RecordCommand("launch", "U12345", MembershipMember)
	second.RecordCommand("launch", "U12345", MembershipMember)
	if got := counterValue(t, registry, CommandExecutionsMetricName, map[string]string{
		"command":                     "launch",
		"hybrid_platforms_membership": MembershipMember,
	}); got != 2 {
		t.Fatalf("shared command counter = %v, want 2", got)
	}
}

func counterValue(t *testing.T, registry *prometheus.Registry, name string, wantLabels map[string]string) float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() failed: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := make(map[string]string, len(metric.GetLabel()))
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if sameLabels(labels, wantLabels) {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func metricHasLabelValue(t *testing.T, registry *prometheus.Registry, name, labelName, wantValue string) bool {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() failed: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == labelName && label.GetValue() == wantValue {
					return true
				}
			}
		}
	}
	return false
}

func sameLabels(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}
