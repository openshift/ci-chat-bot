package slack

import (
	"maps"
	"strings"
	"testing"

	"github.com/openshift/ci-chat-bot/pkg/manager"
)

func TestParseImageInput(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name        string
		input       string
		expected    []string
		expectError bool
		errorMsg    string
	}{
		{
			name:     "Empty input",
			input:    "",
			expected: nil,
		},
		{
			name:     "Single version",
			input:    "4.19",
			expected: []string{"4.19"},
		},
		{
			name:     "Version with comma (original problem case)",
			input:    "4.19, aws",
			expected: []string{"4.19", "aws"}, // Should filter empty strings
		},
		{
			name:     "Trailing comma (user's problem)",
			input:    "4.19,openshift/installer#123,",
			expected: []string{"4.19", "openshift/installer#123"},
		},
		{
			name:     "Leading comma",
			input:    ",4.19,aws",
			expected: []string{"4.19", "aws"},
		},
		{
			name:     "Multiple spaces",
			input:    "4.19  ,   aws  ,  gcp",
			expected: []string{"4.19", "aws", "gcp"},
		},
		{
			name:     "Mixed PRs and versions",
			input:    "4.19,openshift/installer#7160,openshift/machine-config-operator#3688",
			expected: []string{"4.19", "openshift/installer#7160", "openshift/machine-config-operator#3688"},
		},
		{
			name:        "Only commas and spaces",
			input:       " , , , ",
			expected:    nil,
			expectError: true,
			errorMsg:    "no valid inputs found",
		},
		{
			name:     "Version with trailing space and comma",
			input:    "4.19.0-0.nightly-arm64-2025-09-11-024736, metal",
			expected: []string{"4.19.0-0.nightly-arm64-2025-09-11-024736", "metal"},
		},
		{
			name:     "Input with tabs and newlines",
			input:    "4.19\t,\n  aws\n,\tgcp  ",
			expected: []string{"4.19", "aws", "gcp"},
		},
		{
			name:     "Multiple consecutive commas",
			input:    "4.19,,,,aws,,,gcp",
			expected: []string{"4.19", "aws", "gcp"},
		},
		{
			name:     "Whitespace-only items between commas",
			input:    "4.19,   ,\t,aws,  \n  ,gcp",
			expected: []string{"4.19", "aws", "gcp"},
		},
		{
			name:     "Stream names with ci and nightly",
			input:    "4.19.0-0.ci,4.18.0-0.nightly",
			expected: []string{"4.19.0-0.ci", "4.18.0-0.nightly"},
		},
		{
			name:     "Image pull specs",
			input:    "registry.ci.openshift.org/ocp/release:4.19.0-0.nightly-2025-09-11-024736,quay.io/openshift-release-dev/ocp-release:4.18.0",
			expected: []string{"registry.ci.openshift.org/ocp/release:4.19.0-0.nightly-2025-09-11-024736", "quay.io/openshift-release-dev/ocp-release:4.18.0"},
		},
		{
			name:     "Complex PR references",
			input:    "openshift/installer#7160,openshift/machine-config-operator#3688,kubernetes/kubernetes#12345",
			expected: []string{"openshift/installer#7160", "openshift/machine-config-operator#3688", "kubernetes/kubernetes#12345"},
		},
		{
			name:     "Mixed formats",
			input:    "4.19,ci,nightly,openshift/installer#123,registry.ci.openshift.org/ocp/release:latest",
			expected: []string{"4.19", "ci", "nightly", "openshift/installer#123", "registry.ci.openshift.org/ocp/release:latest"},
		},
		{
			name:     "Special characters in PR names",
			input:    "openshift/cluster-api-provider-aws#123,openshift/machine-config-operator#456",
			expected: []string{"openshift/cluster-api-provider-aws#123", "openshift/machine-config-operator#456"},
		},
		{
			name:        "Single comma only",
			input:       ",",
			expected:    nil,
			expectError: true,
			errorMsg:    "no valid inputs found",
		},
		{
			name:        "Multiple commas only",
			input:       ",,,,",
			expected:    nil,
			expectError: true,
			errorMsg:    "no valid inputs found",
		},
		{
			name:     "Very long version string",
			input:    "4.19.0-0.nightly-arm64-2025-09-11-024736-very-long-build-name-with-extra-details",
			expected: []string{"4.19.0-0.nightly-arm64-2025-09-11-024736-very-long-build-name-with-extra-details"},
		},
		{
			name:     "Version with underscores and dots",
			input:    "4.19_candidate.2024-12-01,4.18.0_rc.1",
			expected: []string{"4.19_candidate.2024-12-01", "4.18.0_rc.1"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ParseImageInput(tc.input)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if tc.errorMsg != "" && !strings.Contains(err.Error(), tc.errorMsg) {
					t.Errorf("Expected error containing '%s', got: %s", tc.errorMsg, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if len(result) != len(tc.expected) {
				t.Errorf("Expected %d items, got %d: %v", len(tc.expected), len(result), result)
				return
			}

			for i, expected := range tc.expected {
				if result[i] != expected {
					t.Errorf("Expected result[%d] = '%s', got '%s'", i, expected, result[i])
				}
			}
		})
	}
}

func TestFindClosestMatch(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		input    string
		options  []string
		expected string
	}{
		{
			name:     "Exact match",
			input:    "aws",
			options:  []string{"aws", "gcp", "azure"},
			expected: "aws",
		},
		{
			name:     "Typo in architecture (user's problem case)",
			input:    "arm",
			options:  []string{"amd64", "arm64", "multi"},
			expected: "arm64",
		},
		{
			name:     "Partial platform match",
			input:    "gc",
			options:  []string{"aws", "gcp", "azure"},
			expected: "gcp",
		},
		{
			name:     "Typo in platform",
			input:    "azur",
			options:  []string{"aws", "gcp", "azure"},
			expected: "azure",
		},
		{
			name:     "No reasonable match",
			input:    "xyz",
			options:  []string{"aws", "gcp", "azure"},
			expected: "",
		},
		{
			name:     "Case insensitive",
			input:    "AWS",
			options:  []string{"aws", "gcp", "azure"},
			expected: "aws",
		},
		{
			name:     "Prefix match scores higher",
			input:    "hyper",
			options:  []string{"hypershift-hosted", "hypershift-hosted-powervs", "metal"},
			expected: "hypershift-hosted",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := findClosestMatch(tc.input, tc.options)
			if result != tc.expected {
				t.Errorf("Expected '%s', got '%s'", tc.expected, result)
			}
		})
	}
}

func TestParseOptionsWithSuggestions(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name          string
		options       string
		expectError   bool
		errorContains string
	}{
		{
			name:    "Valid options",
			options: "aws,amd64,compact",
		},
		{
			name:          "Invalid option with suggestion (user's typo case)",
			options:       "arm", // Should suggest arm64
			expectError:   true,
			errorContains: "Did you mean 'arm64'?",
		},
		{
			name:          "Invalid platform with suggestion",
			options:       "gc", // Should suggest gcp
			expectError:   true,
			errorContains: "Did you mean 'gcp'?",
		},
		{
			name:          "Invalid option no suggestion",
			options:       "invalidoption123",
			expectError:   true,
			errorContains: "Valid options:",
		},
		{
			name:          "Multiple invalid options",
			options:       "arm,gc", // Should suggest both
			expectError:   true,
			errorContains: "Did you mean", // Should suggest for first one
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Import manager package to get supported options, but mock for testing
			_, _, _, err := ParseOptions(tc.options, [][]string{{"4.19"}}, manager.JobTypeInstall)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if tc.errorContains != "" && !strings.Contains(err.Error(), tc.errorContains) {
					t.Errorf("Expected error containing '%s', got: %s", tc.errorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestBuildJobParams(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name        string
		params      string
		expected    map[string]string
		errorString string
	}{
		{
			name:        "NoParameters",
			params:      "",
			expected:    map[string]string{},
			errorString: "",
		},
		{
			name:        "SimpleParameter",
			params:      "\"KEY1=VALUE1\"",
			expected:    map[string]string{"KEY1": "VALUE1"},
			errorString: "",
		},
		{
			name:        "IncorrectlyQuotedParameter",
			params:      "“KEY1=VALUE1”",
			expected:    map[string]string{"KEY1": "VALUE1"},
			errorString: "",
		},
		{
			name:        "IncorrectlyDeliminatedParameter",
			params:      "\"KEY1:VALUE1\"",
			expected:    nil,
			errorString: "unable to interpret `KEY1:VALUE1` as a parameter. Please ensure that all parameters are in the form of KEY=VALUE",
		},
		{
			name:        "MarkDownLinkParameter",
			params:      "\"KEY1=<http://abc123.com|VALUE1>\"",
			expected:    map[string]string{"KEY1": "VALUE1"},
			errorString: "",
		},
		{
			name:        "SingleNESTED_PARAMETER",
			params:      "\"NESTED_PARAMETER=KEY1=VALUE1\"",
			expected:    map[string]string{"NESTED_PARAMETER": "KEY1=VALUE1"},
			errorString: "",
		},
		{
			name:        "MultipleNESTED_PARAMETERs",
			params:      "\"NESTED_PARAMETER=KEY1=VALUE1;KEY2=VALUE2\"",
			expected:    map[string]string{"NESTED_PARAMETER": "KEY1=VALUE1\nKEY2=VALUE2"},
			errorString: "",
		},
		{
			name:        "MixedParametersWithNESTED_PARAMETERs",
			params:      "\"BASELINE_CAPABILITY_SET=None\",\"NESTED_PARAMETER=KEY1=VALUE1;KEY2=VALUE2\"",
			expected:    map[string]string{"BASELINE_CAPABILITY_SET": "None", "NESTED_PARAMETER": "KEY1=VALUE1\nKEY2=VALUE2"},
			errorString: "",
		},
		{
			name:        "MultipleMixedParameters",
			params:      "\"BASELINE_CAPABILITY_SET=None\",\"NESTED_PARAMETER=KEY1=VALUE1;KEY2=VALUE2\",\"OTHER_CONFIG=KEY1=VALUE1a;KEY2=VALUE2a\"",
			expected:    map[string]string{"BASELINE_CAPABILITY_SET": "None", "NESTED_PARAMETER": "KEY1=VALUE1\nKEY2=VALUE2", "OTHER_CONFIG": "KEY1=VALUE1a\nKEY2=VALUE2a"},
			errorString: "",
		},
		{
			name:        "InvalidMixedParameters",
			params:      "\"BASELINE_CAPABILITY_SET=None\",\"NESTED_PARAMETER=KEY1=VALUE1;KEY2;KEY3=VALUE3\",\"OTHER_CONFIG=KEY1=VALUE1a;KEY2=VALUE2a\"",
			expected:    nil,
			errorString: "unable to interpret parameter in `KEY2`. Each nested parameter must be in the form of KEY=VALUE",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildJobParams(tc.params)
			if !maps.Equal(got, tc.expected) {
				t.Errorf("got %q, expected %q", got, tc.expected)
			}
			if err != nil && err.Error() != tc.errorString {
				t.Errorf("got error %q, expected error %q", err, tc.errorString)
			}
		})
	}
}

func TestParseParameterValue(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		value    string
		expected string
	}{
		{
			name:     "NoValue",
			value:    "",
			expected: "",
		},
		{
			name:     "SimpleValue",
			value:    "VALUE1",
			expected: "VALUE1",
		},
		{
			name:     "MarkDownLinkValue",
			value:    "<http://abc123.com|VALUE1>",
			expected: "VALUE1",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseParameterValue(tc.value)
			if got != tc.expected {
				t.Errorf("got %q, want %q", got, tc.expected)
			}
		})
	}
}
