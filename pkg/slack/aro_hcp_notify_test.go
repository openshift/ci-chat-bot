package slack

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

type aroHcpDeliveryClient struct {
	mockSlackClient
	postMessage func(string, ...slack.MsgOption) (string, string, error)
}

func (c *aroHcpDeliveryClient) PostMessage(channel string, options ...slack.MsgOption) (string, string, error) {
	return c.postMessage(channel, options...)
}

type aroHcpAuthJobManager struct {
	mockJobManager
	job *manager.Job
}

func (m *aroHcpAuthJobManager) GetLaunchJob(string) (*manager.Job, error) {
	return m.job, nil
}

func TestSendAroHcpKubeconfigs(t *testing.T) {
	t.Parallel()
	uploadErr := errors.New("upload failed")
	for _, tc := range []struct {
		name        string
		failUpload  int
		wantMgmtID  string
		wantSvcID   string
		wantUploads int
	}{
		{name: "success", wantMgmtID: "mgmt-id", wantSvcID: "svc-id", wantUploads: 2},
		{name: "management failure", failUpload: 1, wantUploads: 1},
		{name: "service failure", failUpload: 2, wantMgmtID: "mgmt-id", wantUploads: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uploads := 0
			client := &mockSlackClient{uploadFileFunc: func(params slack.UploadFileParameters) (*slack.FileSummary, error) {
				uploads++
				if uploads == tc.failUpload {
					return nil, uploadErr
				}
				id := "mgmt-id"
				if uploads == 2 {
					id = "svc-id"
				}
				return &slack.FileSummary{ID: id}, nil
			}}
			mgmtID, svcID, err := SendAroHcpKubeconfigs(client, "channel", "svc", "mgmt", "identifier")
			if mgmtID != tc.wantMgmtID || svcID != tc.wantSvcID || uploads != tc.wantUploads {
				t.Fatalf("got IDs (%q, %q) and %d uploads; want (%q, %q) and %d uploads", mgmtID, svcID, uploads, tc.wantMgmtID, tc.wantSvcID, tc.wantUploads)
			}
			if tc.failUpload == 0 && err != nil || tc.failUpload != 0 && !errors.Is(err, uploadErr) {
				t.Fatalf("unexpected upload error: %v", err)
			}
		})
	}
}

func TestAroHcpAuthDeliveryRetries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		failStep string
		wantErr  string
	}{
		{name: "management upload", failStep: "mgmt", wantErr: "management kubeconfig"},
		{name: "service upload", failStep: "svc", wantErr: "service kubeconfig"},
		{name: "ready message", failStep: "ready", wantErr: "ready message"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := &manager.Job{
				Mode:             manager.JobTypeAroHcp,
				Credentials:      "svc",
				Credentials2:     "mgmt",
				RequestedChannel: "original-channel",
				RequestedAt:      time.Now(),
				ExpiresAt:        time.Now().Add(time.Hour),
			}
			jobManager := &aroHcpAuthJobManager{job: job}
			event := &slackevents.MessageEvent{User: "user", Channel: "auth-channel"}
			var steps []string
			fail := true
			client := &aroHcpDeliveryClient{
				mockSlackClient: mockSlackClient{uploadFileFunc: func(params slack.UploadFileParameters) (*slack.FileSummary, error) {
					if params.Channel != event.Channel {
						t.Fatalf("upload channel = %q, want %q", params.Channel, event.Channel)
					}
					steps = append(steps, params.Content)
					if fail && params.Content == tc.failStep {
						return nil, errors.New("delivery failed")
					}
					return &slack.FileSummary{ID: params.Content + "-id"}, nil
				}},
				postMessage: func(channel string, options ...slack.MsgOption) (string, string, error) {
					if channel != event.Channel {
						t.Fatalf("message channel = %q, want %q", channel, event.Channel)
					}
					steps = append(steps, "ready")
					if fail && tc.failStep == "ready" {
						return "", "", errors.New("delivery failed")
					}
					return channel, "timestamp", nil
				},
			}
			for attempt := range 3 {
				steps = nil
				fail = attempt < 2
				response := AroHcpAuth(client, jobManager, event, nil)
				wantSteps := "mgmt,svc,ready"
				if fail {
					if !strings.Contains(response, tc.wantErr) || !strings.Contains(response, "delivery failed") {
						t.Fatalf("attempt %d did not report failure: %q", attempt, response)
					}
					switch tc.failStep {
					case "mgmt":
						wantSteps = "mgmt"
					case "svc":
						wantSteps = "mgmt,svc"
					}
				} else if response != "" {
					t.Fatalf("successful retry returned %q", response)
				}
				if got := strings.Join(steps, ","); got != wantSteps {
					t.Fatalf("attempt %d delivery order = %q, want %q", attempt, got, wantSteps)
				}
			}
		})
	}
}

func TestNotifyAroHcpPreview(t *testing.T) {
	t.Parallel()
	job := &manager.Job{Credentials: "svc", Credentials2: "mgmt", ExpiresAt: time.Now().Add(time.Hour)}
	msg, kubeconfig, err := NotifyAroHcp(&mockSlackClient{}, job, false)
	if err != nil || !strings.Contains(msg, "is ready") || kubeconfig != job.Credentials {
		t.Fatalf("unexpected preview: (%q, %q, %v)", msg, kubeconfig, err)
	}
}

func TestNotifyJobAroHcpUploadFailure(t *testing.T) {
	t.Parallel()
	job := &manager.Job{Mode: manager.JobTypeAroHcp, Credentials: "svc", Credentials2: "mgmt"}
	msg, kubeconfig := NotifyJob(&mockSlackClient{}, job, true)
	if !strings.Contains(msg, "unable to upload") || kubeconfig != "" {
		t.Fatalf("upload failure returned (%q, %q)", msg, kubeconfig)
	}
}
