package common

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFetchReleases(t *testing.T) {
	tests := []struct {
		name         string
		responseBody string
		statusCode   int
		wantErr      bool
		wantStreams  int
		wantTags     map[string]int // stream -> expected tag count
	}{
		{
			name:         "valid JSON",
			responseBody: `{"4-stable":["4.15.0","4.14.0"],"4-dev":["4.16.0-0.nightly"]}`,
			statusCode:   http.StatusOK,
			wantErr:      false,
			wantStreams:  2,
			wantTags:     map[string]int{"4-stable": 2, "4-dev": 1},
		},
		{
			name:         "empty JSON object",
			responseBody: `{}`,
			statusCode:   http.StatusOK,
			wantErr:      false,
			wantStreams:  0,
		},
		{
			name:         "invalid JSON",
			responseBody: `not json`,
			statusCode:   http.StatusOK,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{
				Transport: responseTransport{
					statusCode:   tt.statusCode,
					responseBody: tt.responseBody,
					expectedURL:  "https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestreams/accepted",
				},
			}

			releases, err := FetchReleases(client, "amd64")

			if (err != nil) != tt.wantErr {
				t.Fatalf("FetchReleases() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if len(releases) != tt.wantStreams {
				t.Errorf("stream count = %d, want %d", len(releases), tt.wantStreams)
			}

			for stream, wantCount := range tt.wantTags {
				gotTags, ok := releases[stream]
				if !ok {
					t.Errorf("stream %q not found in result", stream)
					continue
				}
				if len(gotTags) != wantCount {
					t.Errorf("stream %q: tag count = %d, want %d", stream, len(gotTags), wantCount)
				}
			}
		})
	}

	t.Run("server unreachable returns error", func(t *testing.T) {
		client := &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("connection refused")
			}),
		}

		_, err := FetchReleases(client, "amd64")
		if err == nil {
			t.Fatal("expected error for unreachable server")
		}
	})
}

type responseTransport struct {
	statusCode   int
	responseBody string
	expectedURL  string
}

func (t responseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return nil, fmt.Errorf("request method = %s, want %s", req.Method, http.MethodGet)
	}
	if req.URL.String() != t.expectedURL {
		return nil, fmt.Errorf("request URL = %s, want %s", req.URL, t.expectedURL)
	}
	return &http.Response{
		StatusCode: t.statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(t.responseBody)),
		Request:    req,
	}, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
