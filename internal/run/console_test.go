package run

import (
	"bytes"
	"github.com/chobits02/provena/internal/piagent"
	"strings"
	"testing"
	"time"

	"github.com/chobits02/provena/internal/fgs"
)

func TestGraphLinesOrdersActiveStepsFirst(t *testing.T) {
	now := time.Now()
	snapshot := fgs.Snapshot{
		Version: 7,
		Nodes: []fgs.Node{
			{ID: "origin", Kind: "origin", Label: "Origin"},
			{ID: "goal", Kind: "goal", Label: "Goal"},
			{ID: "step-done", Kind: fgs.KindStep, Label: "recon", Status: fgs.StatusCompleted, CreatedAt: now},
			{ID: "fact-1", Kind: fgs.KindFact, Label: "target reachable", Status: fgs.StatusConfirmed, CreatedAt: now},
			{ID: "step-live", Kind: fgs.KindStep, Label: "sqli", Status: fgs.StatusActive, CreatedAt: now},
			{ID: "finding-1", Kind: fgs.KindFinding, Label: "csp bypass", Status: fgs.StatusConfirmed, CreatedAt: now},
		},
	}

	lines := graphLines(snapshot)
	if len(lines) != 4 {
		t.Fatalf("lines = %d, want 4 (origin/goal are structural noise): %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "step-live") || !strings.Contains(lines[0], "●") {
		t.Errorf("active step should lead: %q", lines[0])
	}
	if !strings.Contains(lines[1], "step-done") || !strings.Contains(lines[1], "✓") {
		t.Errorf("completed step should follow: %q", lines[1])
	}
	if !strings.Contains(lines[2], "finding-1") || !strings.Contains(lines[2], "⚑") {
		t.Errorf("findings should come before facts: %q", lines[2])
	}
	if !strings.Contains(lines[3], "fact-1") || !strings.Contains(lines[3], "◆") {
		t.Errorf("facts should be last: %q", lines[3])
	}
}

func TestConsolePlainModeKeepsLineOutput(t *testing.T) {
	var buf bytes.Buffer
	c := newConsole(&buf, Options{Plain: true})

	c.activityStart(1, 3)
	c.toolStart("http-framework-test", nil)
	c.activityEnd(2, 3*time.Second, nil)

	out := buf.String()
	for _, want := range []string{"activity 1/3...", "  -> http-framework-test"} {
		if !strings.Contains(out, want) {
			t.Errorf("plain output missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"▌", "╭", thinkingGutter} {
		if strings.Contains(out, unwanted) {
			t.Errorf("plain mode drew %q:\n%s", unwanted, out)
		}
	}
}

func TestConsoleStreamsThinkingPerLine(t *testing.T) {
	var buf bytes.Buffer
	c := newConsole(&buf, Options{ShowThinking: true})

	c.messageUpdate(map[string]interface{}{
		"assistantMessageEvent": map[string]interface{}{
			"type":  "thinking_delta",
			"delta": "first\nsecond",
		},
	})
	c.endStream()

	out := buf.String()
	if got := strings.Count(out, thinkingGutter); got != 2 {
		t.Errorf("thinking gutter repeated %d times, want 2:\n%s", got, out)
	}
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Errorf("thinking text was lost:\n%s", out)
	}
}

func TestConsoleHidesThinkingWhenDisabled(t *testing.T) {
	var buf bytes.Buffer
	c := newConsole(&buf, Options{ShowThinking: false})

	c.messageUpdate(map[string]interface{}{
		"assistantMessageEvent": map[string]interface{}{"type": "thinking_delta", "delta": "secret"},
	})
	c.endStream()

	if strings.Contains(buf.String(), "secret") {
		t.Errorf("thinking leaked while disabled:\n%s", buf.String())
	}
}

func TestConsoleGraphModeFiltersTriggers(t *testing.T) {
	snapshot := fgs.Snapshot{Version: 2, Nodes: []fgs.Node{
		{ID: "step-1", Kind: fgs.KindStep, Label: "recon", Status: fgs.StatusActive},
	}}

	var activityBuf bytes.Buffer
	activityConsole := newConsole(&activityBuf, Options{GraphMode: graphActivity})
	activityConsole.graph(snapshot, triggerMutation)
	if activityBuf.Len() != 0 {
		t.Errorf("graph=activity printed on a mutation:\n%s", activityBuf.String())
	}
	activityConsole.graph(snapshot, triggerActivity)
	if !strings.Contains(activityBuf.String(), "step-1") {
		t.Errorf("graph=activity did not print at the activity boundary:\n%s", activityBuf.String())
	}

	var liveBuf bytes.Buffer
	liveConsole := newConsole(&liveBuf, Options{GraphMode: graphLive})
	liveConsole.graph(snapshot, triggerMutation)
	if !strings.Contains(liveBuf.String(), "step-1") {
		t.Errorf("graph=live skipped a mutation:\n%s", liveBuf.String())
	}

	var offBuf bytes.Buffer
	offConsole := newConsole(&offBuf, Options{GraphMode: graphOff})
	offConsole.graph(snapshot, triggerFinal)
	if offBuf.Len() != 0 {
		t.Errorf("graph=off printed something:\n%s", offBuf.String())
	}
}

func TestToolArgSummaryPrefersTheTellingArgument(t *testing.T) {
	got := toolArgSummary(map[string]interface{}{
		"method": "GET",
		"url":    "http://example.com/login",
	})
	if !strings.Contains(got, "url=") || !strings.Contains(got, "login") {
		t.Errorf("summary = %q, want it to surface the url", got)
	}
	if got := toolArgSummary(nil); got != "" {
		t.Errorf("empty args should give an empty summary, got %q", got)
	}
}

func TestConsoleSurfacesModelErrors(t *testing.T) {
	var buf bytes.Buffer
	c := newConsole(&buf, Options{})

	c.messageUpdate(map[string]interface{}{
		"assistantMessageEvent": map[string]interface{}{
			"type":   "error",
			"reason": "error",
			"error":  map[string]interface{}{"message": "Insufficient account balance"},
		},
	})

	out := buf.String()
	if !strings.Contains(out, "model error") || !strings.Contains(out, "Insufficient account balance") {
		t.Errorf("an upstream model failure must be visible:\n%s", out)
	}
}

// TestConsoleSurfacesFailedMessageMeta uses the shape Pi actually emits when the
// upstream provider refuses: an assistant message with empty content,
// stopReason "error" and an errorMessage. This is how the real
// "403 status code (no body)" / quota failures reach the client.
func TestConsoleSurfacesFailedMessageMeta(t *testing.T) {
	var buf bytes.Buffer
	c := newConsole(&buf, Options{})

	c.event(piagent.Event{
		Type: "message_end",
		Raw: map[string]interface{}{
			"type": "message_end",
			"message": map[string]interface{}{
				"role":         "assistant",
				"content":      []interface{}{},
				"stopReason":   "error",
				"errorMessage": "403 status code (no body)",
			},
		},
	})

	out := buf.String()
	if !strings.Contains(out, "403 status code") {
		t.Errorf("a failed assistant message must be reported:\n%s", out)
	}

	// The duplicate turn_end for the same failure must not double-print.
	before := strings.Count(buf.String(), "model error")
	c.event(piagent.Event{
		Type: "message_end",
		Raw: map[string]interface{}{
			"type":    "message_end",
			"message": map[string]interface{}{"stopReason": "error", "errorMessage": "403 status code (no body)"},
		},
	})
	if got := strings.Count(buf.String(), "model error"); got != before {
		t.Errorf("repeated failure printed %d times, want %d", got, before)
	}
}

func TestConsoleIgnoresHealthyMessageEnd(t *testing.T) {
	var buf bytes.Buffer
	c := newConsole(&buf, Options{})

	c.event(piagent.Event{
		Type: "message_end",
		Raw: map[string]interface{}{
			"type": "message_end",
			"message": map[string]interface{}{
				"role":       "assistant",
				"stopReason": "stop",
			},
		},
	})

	if strings.Contains(buf.String(), "model error") {
		t.Errorf("a healthy message end must stay quiet:\n%s", buf.String())
	}
}

func TestConsoleFlagsEmptyRounds(t *testing.T) {
	var buf bytes.Buffer
	c := newConsole(&buf, Options{})
	c.activityEnd(0, 6*time.Second, nil)
	if !strings.Contains(buf.String(), "no tool calls") {
		t.Errorf("an empty round should be called out, not reported as plain success:\n%s", buf.String())
	}

	var busy bytes.Buffer
	c = newConsole(&busy, Options{})
	c.activityEnd(7, 40*time.Second, nil)
	if strings.Contains(busy.String(), "no tool calls") {
		t.Errorf("a productive round must not be flagged:\n%s", busy.String())
	}
}
