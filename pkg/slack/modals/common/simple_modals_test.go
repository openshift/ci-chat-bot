package common

import (
	"encoding/json"
	"testing"

	"github.com/openshift/ci-chat-bot/pkg/slack/modals"
	"github.com/slack-go/slack"
)

func TestBuildSimpleView(t *testing.T) {
	tests := []struct {
		name       string
		identifier string
		title      string
		message    string
	}{
		{
			name:       "basic view",
			identifier: "test-id",
			title:      "Test Title",
			message:    "Hello, world!",
		},
		{
			name:       "empty message",
			identifier: "empty",
			title:      "Empty",
			message:    "",
		},
		{
			name:       "markdown message",
			identifier: "md",
			title:      "Markdown",
			message:    "*bold* _italic_ `code`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := BuildSimpleView(tt.identifier, tt.title, tt.message)

			if view.Type != slack.VTModal {
				t.Errorf("Type = %q, want %q", view.Type, slack.VTModal)
			}
			if view.Title == nil || view.Title.Text != tt.title {
				t.Errorf("Title.Text = %q, want %q", view.Title.Text, tt.title)
			}
			if view.Close == nil || view.Close.Text != "Cancel" {
				t.Errorf("Close.Text = %q, want %q", view.Close.Text, "Cancel")
			}
			if view.Submit == nil || view.Submit.Text != "Submit" {
				t.Errorf("Submit.Text = %q, want %q", view.Submit.Text, "Submit")
			}

			if len(view.Blocks.BlockSet) != 1 {
				t.Fatalf("BlockSet length = %d, want 1", len(view.Blocks.BlockSet))
			}
			section, ok := view.Blocks.BlockSet[0].(*slack.SectionBlock)
			if !ok {
				t.Fatalf("block is %T, want *slack.SectionBlock", view.Blocks.BlockSet[0])
			}
			if section.Text.Type != slack.MarkdownType {
				t.Errorf("section text type = %q, want %q", section.Text.Type, slack.MarkdownType)
			}
			if section.Text.Text != tt.message {
				t.Errorf("section text = %q, want %q", section.Text.Text, tt.message)
			}

			// Verify identifier is in PrivateMetadata
			var metaData modals.CallbackDataAndIdentifier
			if err := json.Unmarshal([]byte(view.PrivateMetadata), &metaData); err != nil {
				t.Fatalf("failed to unmarshal PrivateMetadata: %v", err)
			}
			if metaData.Identifier != tt.identifier {
				t.Errorf("metadata Identifier = %q, want %q", metaData.Identifier, tt.identifier)
			}
		})
	}
}

func TestAppendKubeconfigBlock(t *testing.T) {
	tests := []struct {
		name            string
		kubeconfig      string
		headerText      string
		initialBlocks   int
		wantBlocksAdded int
	}{
		{
			name:            "empty kubeconfig is no-op",
			kubeconfig:      "",
			headerText:      "Kubeconfig",
			initialBlocks:   1,
			wantBlocksAdded: 0,
		},
		{
			name:            "non-empty kubeconfig appends 3 blocks",
			kubeconfig:      "apiVersion: v1\nclusters: []",
			headerText:      "Kubeconfig",
			initialBlocks:   1,
			wantBlocksAdded: 3,
		},
		{
			name:            "custom header text",
			kubeconfig:      "some-config",
			headerText:      "Custom Header",
			initialBlocks:   0,
			wantBlocksAdded: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := &slack.ModalViewRequest{
				Blocks: slack.Blocks{BlockSet: make([]slack.Block, tt.initialBlocks)},
			}
			// Fill initial blocks with placeholder sections
			for i := range tt.initialBlocks {
				view.Blocks.BlockSet[i] = &slack.SectionBlock{Type: slack.MBTSection}
			}

			AppendKubeconfigBlock(view, tt.kubeconfig, tt.headerText)

			gotBlocks := len(view.Blocks.BlockSet)
			wantBlocks := tt.initialBlocks + tt.wantBlocksAdded
			if gotBlocks != wantBlocks {
				t.Errorf("block count = %d, want %d", gotBlocks, wantBlocks)
			}

			if tt.wantBlocksAdded == 3 {
				// Check divider
				if _, ok := view.Blocks.BlockSet[tt.initialBlocks].(*slack.DividerBlock); !ok {
					t.Errorf("block[%d] is %T, want *slack.DividerBlock", tt.initialBlocks, view.Blocks.BlockSet[tt.initialBlocks])
				}
				// Check header
				header, ok := view.Blocks.BlockSet[tt.initialBlocks+1].(*slack.HeaderBlock)
				if !ok {
					t.Fatalf("block[%d] is %T, want *slack.HeaderBlock", tt.initialBlocks+1, view.Blocks.BlockSet[tt.initialBlocks+1])
				}
				if header.Text.Text != tt.headerText {
					t.Errorf("header text = %q, want %q", header.Text.Text, tt.headerText)
				}
				// Check rich text block
				if _, ok := view.Blocks.BlockSet[tt.initialBlocks+2].(*slack.RichTextBlock); !ok {
					t.Errorf("block[%d] is %T, want *slack.RichTextBlock", tt.initialBlocks+2, view.Blocks.BlockSet[tt.initialBlocks+2])
				}
			}
		})
	}
}

func TestBuildListResultModal(t *testing.T) {
	tests := []struct {
		name      string
		title     string
		beginning string
		elements  []string
	}{
		{
			name:      "empty elements",
			title:     "List",
			beginning: "Start here",
			elements:  nil,
		},
		{
			name:      "single element",
			title:     "Results",
			beginning: "Beginning text",
			elements:  []string{"item 1"},
		},
		{
			name:      "multiple elements",
			title:     "Results",
			beginning: "Beginning",
			elements:  []string{"item 1", "item 2", "item 3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := BuildListResultModal(tt.title, tt.beginning, tt.elements)

			if view.Type != slack.VTModal {
				t.Errorf("Type = %q, want %q", view.Type, slack.VTModal)
			}
			if view.Title.Text != tt.title {
				t.Errorf("Title = %q, want %q", view.Title.Text, tt.title)
			}
			if view.Submit != nil {
				t.Error("Submit should be nil")
			}
			if view.Close == nil || view.Close.Text != "Close" {
				t.Errorf("Close.Text = %q, want %q", view.Close.Text, "Close")
			}

			wantBlocks := 1 + len(tt.elements)
			if len(view.Blocks.BlockSet) != wantBlocks {
				t.Errorf("block count = %d, want %d", len(view.Blocks.BlockSet), wantBlocks)
			}
		})
	}
}

func TestMetadataBuilder(t *testing.T) {
	tests := []struct {
		name  string
		pairs [][2]string // key, value pairs
		want  string
	}{
		{
			name:  "empty builder",
			pairs: nil,
			want:  "",
		},
		{
			name:  "single pair",
			pairs: [][2]string{{"Key", "val"}},
			want:  "Key: val",
		},
		{
			name:  "multiple pairs",
			pairs: [][2]string{{"A", "1"}, {"B", "2"}, {"C", "3"}},
			want:  "A: 1; B: 2; C: 3",
		},
		{
			name:  "empty values skipped",
			pairs: [][2]string{{"A", "1"}, {"B", ""}, {"C", "3"}},
			want:  "A: 1; C: 3",
		},
		{
			name:  "all empty values",
			pairs: [][2]string{{"A", ""}, {"B", ""}},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mb := NewMetadataBuilder()
			for _, pair := range tt.pairs {
				mb.Add(pair[0], pair[1])
			}
			got := mb.Build()
			if got != tt.want {
				t.Errorf("Build() = %q, want %q", got, tt.want)
			}
		})
	}

	// Test chaining
	t.Run("chaining", func(t *testing.T) {
		got := NewMetadataBuilder().Add("X", "1").Add("Y", "2").Build()
		want := "X: 1; Y: 2"
		if got != want {
			t.Errorf("chained Build() = %q, want %q", got, want)
		}
	})
}
