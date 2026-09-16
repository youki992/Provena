package run

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chobits02/provena/internal/database"
	"github.com/chobits02/provena/internal/fgs"
)

func testSnapshot() fgs.Snapshot {
	return fgs.Snapshot{
		ID:      "graph-1",
		Goal:    "http://target.test",
		Version: 4,
		Nodes: []fgs.Node{
			{ID: "scope", Kind: fgs.KindFact, Label: "authorized scope", Content: "Authorized scope: http://target.test", Status: fgs.StatusConfirmed},
			{ID: "n-finding", Kind: fgs.KindFinding, Label: "verbose error page", Content: "A stack trace is returned for malformed input.", Status: fgs.StatusActive, Evidence: []string{"GET /api?id=%0a -> 500 with trace"}},
			{ID: "n-abandoned", Kind: fgs.KindFinding, Label: "abandoned lead", Content: "not applicable", Status: fgs.StatusAbandoned},
		},
	}
}

func testResult() Result {
	return Result{
		RunID:          "run-123",
		Target:         "http://target.test",
		Objective:      "find issues",
		Status:         "goal_complete",
		Activities:     3,
		GraphPath:      "/tmp/run-123/graph.jsonl",
		ConversationID: "conv-1",
	}
}

func TestBuildMarkdownIncludesFindingsAndSkipsAbandoned(t *testing.T) {
	md := buildMarkdown(testResult(), testSnapshot(), []*database.Vulnerability{{
		ID:          "v-1",
		Title:       "SQL injection in search",
		Severity:    "high",
		Status:      "open",
		Description: "Union-based injection confirmed.",
		Target:      "http://target.test/search",
	}})

	for _, want := range []string{
		"run-123",
		"http://target.test",
		"verbose error page",
		"SQL injection in search",
		"authorized scope",
		"goal_complete",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown is missing %q", want)
		}
	}
	if strings.Contains(md, "abandoned lead") {
		t.Error("abandoned findings must not appear in the report")
	}
}

func TestBuildSARIFIsValidAndMapsSeverity(t *testing.T) {
	raw, err := buildSARIF(testResult(), testSnapshot(), []*database.Vulnerability{{
		ID: "v-1", Title: "SQL injection", Severity: "high", Status: "open",
	}})
	if err != nil {
		t.Fatalf("buildSARIF: %v", err)
	}

	var log struct {
		Version string `json:"version"`
		Schema  string `json:"$schema"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Name  string `json:"name"`
					Rules []struct {
						ID string `json:"id"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID string `json:"ruleId"`
				Level  string `json:"level"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(raw, &log); err != nil {
		t.Fatalf("SARIF is not valid JSON: %v", err)
	}

	if log.Version != "2.1.0" {
		t.Errorf("version = %q, want 2.1.0", log.Version)
	}
	if len(log.Runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(log.Runs))
	}
	run := log.Runs[0]
	if run.Tool.Driver.Name != "Provena" {
		t.Errorf("driver name = %q", run.Tool.Driver.Name)
	}
	// one evidence-backed finding + one recorded vulnerability
	if len(run.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(run.Results))
	}
	if len(run.Tool.Driver.Rules) != 2 {
		t.Errorf("rules = %d, want 2", len(run.Tool.Driver.Rules))
	}

	levels := map[string]string{}
	for _, r := range run.Results {
		levels[r.RuleID] = r.Level
	}
	if got := levels["provena/finding/verbose-error-page"]; got != "warning" {
		t.Errorf("finding level = %q, want warning", got)
	}
	if got := levels["provena/vulnerability/sql-injection"]; got != "error" {
		t.Errorf("high severity should map to error, got %q", got)
	}
}

func TestBuildJSONRoundTrips(t *testing.T) {
	raw, err := buildJSON(testResult(), testSnapshot(), nil)
	if err != nil {
		t.Fatalf("buildJSON: %v", err)
	}
	var payload struct {
		Run struct {
			ID     string `json:"id"`
			Target string `json:"target"`
		} `json:"run"`
		Findings []fgs.Node `json:"findings"`
		Facts    []fgs.Node `json:"facts"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("JSON is not valid: %v", err)
	}
	if payload.Run.ID != "run-123" {
		t.Errorf("run id = %q", payload.Run.ID)
	}
	if payload.Run.Target != "http://target.test" {
		t.Errorf("target = %q", payload.Run.Target)
	}
	if len(payload.Findings) != 1 {
		t.Errorf("findings = %d, want 1 (abandoned excluded)", len(payload.Findings))
	}
	if len(payload.Facts) != 1 {
		t.Errorf("facts = %d, want 1", len(payload.Facts))
	}
}

func TestNormalizeFormatAndSlug(t *testing.T) {
	cases := map[string]string{
		"":       formatMarkdown,
		"md":     formatMarkdown,
		"JSON":   formatJSON,
		"sarif":  formatSARIF,
		"weird":  formatMarkdown,
	}
	for input, want := range cases {
		if got := normalizeFormat(input); got != want {
			t.Errorf("normalizeFormat(%q) = %q, want %q", input, got, want)
		}
	}

	if got := slugify("Verbose error page / stack trace!"); got != "verbose-error-page-stack-trace" {
		t.Errorf("slugify = %q", got)
	}
	if got := slugify("///"); got != "unnamed" {
		t.Errorf("slugify empty = %q, want unnamed", got)
	}
}

func TestScopeDescriptionDefaultsToTarget(t *testing.T) {
	res := Result{Target: "http://only-target.test"}
	if got := scopeDescription(res); !strings.Contains(got, "http://only-target.test") {
		t.Errorf("scopeDescription = %q", got)
	}
	res.Scope = []string{"http://a.test", "http://b.test"}
	got := scopeDescription(res)
	if !strings.Contains(got, "http://a.test") || !strings.Contains(got, "http://b.test") {
		t.Errorf("scopeDescription with explicit scope = %q", got)
	}
}

func TestBuildActivityPromptCarriesTargetScopeAndGraph(t *testing.T) {
	prompt := buildActivityPrompt(Options{
		Target:    "http://target.test",
		Objective: "look for IDOR",
		Scope:     []string{"http://target.test"},
	}, "/tmp/run", `{"goal":"http://target.test"}`, 2, 6)

	for _, want := range []string{
		"http://target.test",
		"look for IDOR",
		"第 2 / 6 轮",
		"GOAL_COMPLETE",
		"fgs_read",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	if bytes.Contains([]byte(prompt), []byte("payload")) {
		t.Error("the activity contract must not embed payloads")
	}
}
