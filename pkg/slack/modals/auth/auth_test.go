package auth

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

type authJobManager struct{ manager.JobManager }

func (authJobManager) GetLaunchJob(string) (*manager.Job, error) {
	return &manager.Job{
		Mode:         manager.JobTypeAroHcp,
		Credentials:  "service-kubeconfig-content",
		Credentials2: "management-kubeconfig-content",
	}, nil
}

type authTransport func(*http.Request) (*http.Response, error)

func (f authTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestAuthModalRedirectsAroHcpToMessageCommand(t *testing.T) {
	t.Parallel()
	updates := make(chan string, 1)
	client := slack.New("token", slack.OptionHTTPClient(&http.Client{
		Transport: authTransport(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			updates <- string(body)
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
		}),
	}))
	callback := &slack.InteractionCallback{View: slack.View{ID: "view-id"}}
	if _, err := process(client, authJobManager{}).Handle(callback, logrus.NewEntry(logrus.New())); err != nil {
		t.Fatal(err)
	}
	select {
	case update := <-updates:
		if !strings.Contains(update, "aro-hcp auth") {
			t.Fatalf("modal did not direct the user to aro-hcp auth: %s", update)
		}
		if strings.Contains(update, "service-kubeconfig-content") || strings.Contains(update, "management-kubeconfig-content") {
			t.Fatal("shared auth modal rendered ARO-HCP credentials")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for modal update")
	}
}
