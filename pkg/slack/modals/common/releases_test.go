package common

import (
	"net/http"
	"net/http/httptest"
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
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			// Use a custom transport that rewrites the URL to our test server
			client := &http.Client{
				Transport: &rewriteTransport{targetURL: server.URL},
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
			Transport: &rewriteTransport{targetURL: "http://127.0.0.1:1"},
		}

		_, err := FetchReleases(client, "amd64")
		if err == nil {
			t.Fatal("expected error for unreachable server")
		}
	})
}

// rewriteTransport redirects all requests to the test server URL
type rewriteTransport struct {
	targetURL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	parsed, err := http.NewRequest(req.Method, t.targetURL+req.URL.Path, req.Body)
	if err != nil {
		return nil, err
	}
	parsed.Header = req.Header
	return http.DefaultTransport.RoundTrip(parsed)
}
