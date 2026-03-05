package common

import (
	"slices"
	"testing"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/slack-go/slack"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestBuildPRInputMetadata(t *testing.T) {
	tests := []struct {
		name         string
		data         modals.CallbackData
		baseMetadata string
		want         string
	}{
		{
			name: "no version returns base unchanged",
			data: modals.CallbackData{
				Input: map[string]string{
					modals.LaunchMode: modals.LaunchFromPRYes,
				},
			},
			baseMetadata: "Platform: aws",
			want:         "Platform: aws",
		},
		{
			name: "with LaunchVersion",
			data: modals.CallbackData{
				Input: map[string]string{
					modals.LaunchVersion: "4.15.0-rc.1",
				},
			},
			baseMetadata: "Platform: aws",
			want:         "Platform: aws; Version: 4.15.0-rc.1",
		},
		{
			name: "falls back to LaunchFromCustom",
			data: modals.CallbackData{
				Input: map[string]string{
					modals.LaunchFromCustom: "quay.io/foo:bar",
				},
			},
			baseMetadata: "Base",
			want:         "Base; Version: quay.io/foo:bar",
		},
		{
			name:         "empty input",
			data:         modals.CallbackData{},
			baseMetadata: "Base",
			want:         "Base",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildPRInputMetadata(tt.data, tt.baseMetadata)
			if got != tt.want {
				t.Errorf("BuildPRInputMetadata() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFilterStreams tests the unexported filterStreams function.
// Tests that mutate HypershiftSupportedVersions must NOT use t.Parallel().
func TestFilterStreams(t *testing.T) {
	tests := []struct {
		name       string
		releases   map[string][]string
		platform   string
		versions   sets.Set[string] // hypershift supported versions to set
		wantCount  int
		wantStream string // if non-empty, verify this stream is in results
	}{
		{
			name: "non-hypershift returns all streams",
			releases: map[string][]string{
				"4-stable":  {"4.15.0", "4.14.0"},
				"4-dev":     {"4.16.0-0.nightly-2024-01-01"},
				"something": {"1.0"},
			},
			platform:  "aws",
			wantCount: 3,
		},
		{
			name: "hypershift filters to matching versions and stable/dev streams",
			releases: map[string][]string{
				"4.15-stable":   {"4.15.0"},
				"4.15-dev":      {"4.15.0-0.nightly"},
				"4.14-stable":   {"4.14.0"},
				"3.11-stable":   {"3.11.0"},
				"custom-stream": {"1.0.0"},
			},
			platform:  "hypershift-hosted",
			versions:  sets.New("4.15"),
			wantCount: 4, // 4.15-stable, 4.15-dev (prefix match), 4.14-stable, 3.11-stable (suffix match)
		},
		{
			name:      "empty releases returns empty result",
			releases:  map[string][]string{},
			platform:  "hypershift-hosted",
			versions:  sets.New("4.15"),
			wantCount: 0,
		},
		{
			name: "stream without dash does not panic",
			releases: map[string][]string{
				"nodash": {"1.0.0"},
			},
			platform:  "hypershift-hosted",
			versions:  sets.New("4.15"),
			wantCount: 0,
		},
		{
			name: "stream with prefix match",
			releases: map[string][]string{
				"4.15.0-rc.1": {"4.15.0-rc.1"},
			},
			platform:   "hypershift-hosted",
			versions:   sets.New("4.15"),
			wantCount:  1,
			wantStream: "4.15.0-rc.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.versions != nil {
				manager.HypershiftSupportedVersions.Mu.Lock()
				old := manager.HypershiftSupportedVersions.Versions
				manager.HypershiftSupportedVersions.Versions = tt.versions
				manager.HypershiftSupportedVersions.Mu.Unlock()
				defer func() {
					manager.HypershiftSupportedVersions.Mu.Lock()
					manager.HypershiftSupportedVersions.Versions = old
					manager.HypershiftSupportedVersions.Mu.Unlock()
				}()
			}

			got := filterStreams(tt.releases, tt.platform)
			if len(got) != tt.wantCount {
				t.Errorf("filterStreams() returned %d streams %v, want %d", len(got), got, tt.wantCount)
			}

			if tt.wantStream != "" {
				if !slices.Contains(got, tt.wantStream) {
					t.Errorf("filterStreams() result %v does not contain %q", got, tt.wantStream)
				}
			}
		})
	}
}

func TestBuildSelectModeView(t *testing.T) {
	t.Run("nil callback returns empty view", func(t *testing.T) {
		config := SelectModeViewConfig{
			BaseViewConfig: BaseViewConfig{
				Callback: nil,
			},
		}
		view := BuildSelectModeView(config)
		if view.Type != "" {
			t.Errorf("Type = %q, want empty string for nil callback", view.Type)
		}
	})

	t.Run("valid callback returns proper view with dropdown", func(t *testing.T) {
		config := SelectModeViewConfig{
			BaseViewConfig: BaseViewConfig{
				Callback:        &slack.InteractionCallback{},
				Data:            modals.CallbackData{},
				ModalIdentifier: "test-mode",
				Title:           "Select Mode",
				PreviousStep:    "step1",
				ContextMetadata: "Platform: aws",
			},
		}

		view := BuildSelectModeView(config)

		if view.Type != slack.VTModal {
			t.Errorf("Type = %q, want %q", view.Type, slack.VTModal)
		}
		if view.Title.Text != "Select Mode" {
			t.Errorf("Title = %q, want %q", view.Title.Text, "Select Mode")
		}
		if view.Submit == nil || view.Submit.Text != "Next" {
			t.Error("expected Submit button with text 'Next'")
		}

		// Should have blocks: back button, header, input (dropdown), divider, context
		if len(view.Blocks.BlockSet) < 3 {
			t.Fatalf("block count = %d, want at least 3", len(view.Blocks.BlockSet))
		}

		// Verify dropdown exists (not checkbox)
		foundDropdown := false
		for _, block := range view.Blocks.BlockSet {
			if input, ok := block.(*slack.InputBlock); ok {
				if input.BlockID == modals.LaunchMode {
					_, isSelect := input.Element.(*slack.SelectBlockElement)
					if !isSelect {
						t.Error("expected SelectBlockElement, got different element type")
					}
					foundDropdown = true
					break
				}
			}
		}
		if !foundDropdown {
			t.Error("expected dropdown block with LaunchMode BlockID")
		}
	})

	t.Run("preserves initial selection", func(t *testing.T) {
		config := SelectModeViewConfig{
			BaseViewConfig: BaseViewConfig{
				Callback: &slack.InteractionCallback{},
				Data: modals.CallbackData{
					Input: map[string]string{
						modals.LaunchMode: modals.LaunchFromPRYes,
					},
				},
				ModalIdentifier: "test-mode",
				Title:           "Select Mode",
			},
		}

		view := BuildSelectModeView(config)

		// Find the input block with dropdown
		for _, block := range view.Blocks.BlockSet {
			if input, ok := block.(*slack.InputBlock); ok && input.BlockID == modals.LaunchMode {
				dropdown, ok := input.Element.(*slack.SelectBlockElement)
				if !ok {
					t.Fatal("expected SelectBlockElement")
				}
				if dropdown.InitialOption == nil {
					t.Error("expected InitialOption to be set")
				} else if dropdown.InitialOption.Value != modals.LaunchFromPRYes {
					t.Errorf("InitialOption.Value = %q, want %q", dropdown.InitialOption.Value, modals.LaunchFromPRYes)
				}
				return
			}
		}
		t.Error("did not find LaunchMode input block")
	})
}

func TestBuildPRInputView(t *testing.T) {
	t.Run("nil callback returns empty view", func(t *testing.T) {
		config := BaseViewConfig{Callback: nil}
		view := BuildPRInputView(config)
		if view.Type != "" {
			t.Errorf("Type = %q, want empty string for nil callback", view.Type)
		}
	})

	t.Run("valid callback returns proper view", func(t *testing.T) {
		callback := &slack.InteractionCallback{}
		config := BaseViewConfig{
			Callback:        callback,
			Data:            modals.CallbackData{},
			ModalIdentifier: "test-pr",
			Title:           "Enter PR",
			PreviousStep:    "prev",
			ContextMetadata: "Platform: gcp",
		}

		view := BuildPRInputView(config)

		if view.Type != slack.VTModal {
			t.Errorf("Type = %q, want %q", view.Type, slack.VTModal)
		}
		if view.Title.Text != "Enter PR" {
			t.Errorf("Title = %q, want %q", view.Title.Text, "Enter PR")
		}

		// Verify PR input block exists
		foundPRInput := false
		for _, block := range view.Blocks.BlockSet {
			if input, ok := block.(*slack.InputBlock); ok {
				if input.BlockID == modals.LaunchFromPR {
					foundPRInput = true
					break
				}
			}
		}
		if !foundPRInput {
			t.Error("expected input block with LaunchFromPR BlockID")
		}

		// Verify back button exists
		foundBackButton := false
		for _, block := range view.Blocks.BlockSet {
			if action, ok := block.(*slack.ActionBlock); ok {
				if action.BlockID == modals.BackButtonBlockID {
					foundBackButton = true
					break
				}
			}
		}
		if !foundBackButton {
			t.Error("expected back button action block")
		}

		// Verify context metadata
		foundContext := false
		for _, block := range view.Blocks.BlockSet {
			if ctx, ok := block.(*slack.ContextBlock); ok && ctx.BlockID == modals.LaunchStepContext {
				foundContext = true
				break
			}
		}
		if !foundContext {
			t.Error("expected context block with LaunchStepContext BlockID")
		}
	})
}
