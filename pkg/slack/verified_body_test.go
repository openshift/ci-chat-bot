package slack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signedRequest(t *testing.T, secret, body string) *http.Request {
	t.Helper()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + timestamp + ":" + body))
	request := httptest.NewRequest(http.MethodPost, "/slack/events-endpoint", strings.NewReader(body))
	request.Header.Set("X-Slack-Request-Timestamp", timestamp)
	request.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
	return request
}

func TestVerifiedBody(t *testing.T) {
	const secret = "signing-secret"
	for _, tc := range []struct {
		name       string
		body       string
		secret     string
		wantStatus int
	}{
		{
			name:       "valid signature",
			body:       `{"type":"event_callback"}`,
			secret:     secret,
			wantStatus: http.StatusOK,
		},
		{
			name:       "invalid signature",
			body:       `{"type":"event_callback"}`,
			secret:     "wrong-secret",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "body over the size limit",
			body:       strings.Repeat("x", maxVerifiedBodySize+1),
			secret:     secret,
			wantStatus: http.StatusRequestEntityTooLarge,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := signedRequest(t, tc.secret, tc.body)
			recorder := httptest.NewRecorder()
			body, ok := VerifiedBody(recorder, request, secret)
			if wantOK := tc.wantStatus == http.StatusOK; ok != wantOK {
				t.Fatalf("expected ok=%t, got %t", wantOK, ok)
			}
			if recorder.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, recorder.Code)
			}
			if !ok {
				return
			}
			if string(body) != tc.body {
				t.Errorf("expected body %q, got %q", tc.body, body)
			}
			// handlers re-read the body (e.g. via FormValue), so it must be restored
			restored, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("failed to re-read the request body: %v", err)
			}
			if string(restored) != tc.body {
				t.Errorf("expected restored body %q, got %q", tc.body, restored)
			}
		})
	}
}
