package slack

import (
	"maps"
	"testing"
)

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
			expected:    nil,
			errorString: "unable to parse `“KEY1=VALUE1”` for parameters. Please ensure that you're using double quotes to enclose variables",
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
