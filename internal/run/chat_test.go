package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestChatAssistantTextFlattensContentBlocks matters because Pi sends the
// assistant message as content blocks, while finalText (used by the one-shot
// run) only understands single-string payloads. Getting this wrong would make
// every persisted answer empty.
func TestChatAssistantTextFlattensContentBlocks(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]interface{}
		want string
	}{
		{
			name: "content blocks",
			raw: map[string]interface{}{
				"message": map[string]interface{}{
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{"type": "thinking", "text": "hidden"},
						map[string]interface{}{"type": "text", "text": "first"},
						map[string]interface{}{"type": "text", "text": "second"},
					},
				},
			},
			want: "first\nsecond",
		},
		{
			name: "plain string",
			raw:  map[string]interface{}{"message": map[string]interface{}{"content": "answer"}},
			want: "answer",
		},
		{
			name: "no assistant text",
			raw:  map[string]interface{}{"message": map[string]interface{}{"role": "user"}},
			want: "",
		},
	}
	for _, tc := range cases {
		if got := chatAssistantText(tc.raw); got != tc.want {
			t.Errorf("%s: chatAssistantText = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestLatestSessionDirPicksTheNewest covers --continue: the directory name is a
// timestamp, so lexicographic order is chronological order.
func TestLatestSessionDirPicksTheNewest(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"20260101-090000-aaaaaaaa", "20260916-141743-003afe03", "20260301-120000-bbbbbbbb"} {
		if err := os.MkdirAll(filepath.Join(base, name), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "not-a-directory"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := latestSessionDir(base)
	if want := filepath.Join(base, "20260916-141743-003afe03"); got != want {
		t.Fatalf("latestSessionDir = %q, want %q", got, want)
	}
	if got := latestSessionDir(filepath.Join(base, "missing")); got != "" {
		t.Fatalf("latestSessionDir on a missing directory = %q, want empty", got)
	}
}

// TestPlainChatStillPrintsTheAnswer guards a regression that is invisible in a
// terminal: streamed deltas are suppressed in plain mode, so without this the
// piped session would show tool names but never the model's reply.
func TestPlainChatStillPrintsTheAnswer(t *testing.T) {
	var rich bytes.Buffer
	newConsole(&rich, Options{}).answer("hello")
	if rich.Len() != 0 {
		t.Errorf("rich mode already streamed the text; answer() must stay quiet: %q", rich.String())
	}

	var plain bytes.Buffer
	newConsole(&plain, Options{Plain: true}).answer("hello")
	if !strings.Contains(plain.String(), "hello") {
		t.Errorf("plain mode dropped the answer: %q", plain.String())
	}
}

// TestChatBannerNamesTheResumeHandle checks the one piece of the banner a user
// actually needs to copy: the session id.
func TestChatBannerNamesTheResumeHandle(t *testing.T) {
	var buf bytes.Buffer
	c := newConsole(&buf, Options{})
	c.chatBanner("http://example.com", "objective", "model-x", "20260916-141743-abc", "data/sessions/x", 97)

	out := buf.String()
	for _, want := range []string{"20260916-141743-abc", "model-x", "http://example.com", "97"} {
		if !strings.Contains(out, want) {
			t.Errorf("banner is missing %q:\n%s", want, out)
		}
	}
}
