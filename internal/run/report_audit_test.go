package run

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chobits02/provena/internal/database"
	"github.com/chobits02/provena/internal/fgs"
)

func TestParseCodeFlowStep(t *testing.T) {
	cases := []struct {
		in         string
		path       string
		start, end int
	}{
		{"src/UserDao.java:42", "src/UserDao.java", 42, 42},
		{"src/UserDao.java:42-45", "src/UserDao.java", 42, 45},
		{"src/UserDao.java", "src/UserDao.java", 0, 0},
		{"  src/A.java:7  ", "src/A.java", 7, 7},
		// Windows drive letters survive because we split on the last colon.
		{`C:\src\A.java:42`, `C:\src\A.java`, 42, 42},
		{`C:\src\A.java`, `C:\src\A.java`, 0, 0},
		// A colon with a non-numeric tail is not a location; keep the entry whole.
		{"src/A.java:abc", "src/A.java:abc", 0, 0},
		{"", "", 0, 0},
	}
	for _, c := range cases {
		path, start, end := parseCodeFlowStep(c.in)
		if path != c.path || start != c.start || end != c.end {
			t.Errorf("parseCodeFlowStep(%q) = (%q,%d,%d), want (%q,%d,%d)",
				c.in, path, start, end, c.path, c.start, c.end)
		}
	}
}

func TestVulnLocation(t *testing.T) {
	cases := []struct {
		name string
		vuln database.Vulnerability
		want string
	}{
		{"no file", database.Vulnerability{}, ""},
		{"file only", database.Vulnerability{FilePath: "src/A.java"}, "src/A.java"},
		{"single line", database.Vulnerability{FilePath: "src/A.java", StartLine: 42}, "src/A.java:42"},
		{"range", database.Vulnerability{FilePath: "src/A.java", StartLine: 42, EndLine: 45}, "src/A.java:42-45"},
		// An end line equal to the start is a single line, not a range.
		{"degenerate range", database.Vulnerability{FilePath: "src/A.java", StartLine: 42, EndLine: 42}, "src/A.java:42"},
	}
	for _, c := range cases {
		if got := vulnLocation(&c.vuln); got != c.want {
			t.Errorf("%s: vulnLocation() = %q, want %q", c.name, got, c.want)
		}
	}
}

// sarifFor marshals a run report and returns the first result plus the rules,
// so the assertions can talk about the actual JSON a consumer would see.
func sarifFor(t *testing.T, vulns []*database.Vulnerability) (map[string]any, []any) {
	t.Helper()
	raw, err := buildSARIF(Result{Target: "https://example.com"}, fgs.Snapshot{}, vulns)
	if err != nil {
		t.Fatalf("buildSARIF: %v", err)
	}
	var log map[string]any
	if err := json.Unmarshal(raw, &log); err != nil {
		t.Fatalf("unmarshal sarif: %v", err)
	}
	runs, ok := log["runs"].([]any)
	if !ok || len(runs) == 0 {
		t.Fatalf("no runs in the sarif log")
	}
	run := runs[0].(map[string]any)
	results := run["results"].([]any)
	if len(results) == 0 {
		t.Fatalf("no results in the sarif log")
	}
	rules := run["tool"].(map[string]any)["driver"].(map[string]any)["rules"].([]any)
	return results[0].(map[string]any), rules
}

func TestBuildSARIFCarriesAuditLocationAndConfidence(t *testing.T) {
	audit := &database.Vulnerability{
		ID:          "v1",
		Title:       "SQL injection in user lookup",
		Description: "id is concatenated into the query",
		Severity:    "high",
		Status:      "open",
		Type:        "SQL注入",
		CWEID:       "CWE-89",
		FilePath:    "src/UserDao.java",
		StartLine:   42,
		EndLine:     45,
		CodeSnippet: "stmt.execute(\"select * from users where id=\" + id)",
		CodeFlow:    "src/Api.java:17\nsrc/UserDao.java:42-45",
		RuleID:      "java/sql-injection",
		Confidence:  database.ConfidenceDataflowReachable,
	}

	result, rules := sarifFor(t, []*database.Vulnerability{audit})

	if got := result["ruleId"]; got != "java/sql-injection" {
		t.Errorf("ruleId = %v, want the originating rule", got)
	}

	physical := result["locations"].([]any)[0].(map[string]any)["physicalLocation"].(map[string]any)
	if got := physical["artifactLocation"].(map[string]any)["uri"]; got != "src/UserDao.java" {
		t.Errorf("artifact uri = %v, want the file path", got)
	}
	region := physical["region"].(map[string]any)
	if region["startLine"] != float64(42) || region["endLine"] != float64(45) {
		t.Errorf("region = %v, want 42-45", region)
	}
	snippet := region["snippet"].(map[string]any)["text"]
	if !strings.Contains(snippet.(string), "select * from users") {
		t.Errorf("region snippet = %v, want the code excerpt", snippet)
	}

	props := result["properties"].(map[string]any)
	if props["cwe"] != "CWE-89" {
		t.Errorf("properties.cwe = %v, want CWE-89", props["cwe"])
	}
	if props["confidence"] != database.ConfidenceDataflowReachable {
		t.Errorf("properties.confidence = %v", props["confidence"])
	}

	flows := result["codeFlows"].([]any)
	steps := flows[0].(map[string]any)["threadFlows"].([]any)[0].(map[string]any)["locations"].([]any)
	if len(steps) != 2 {
		t.Fatalf("codeFlow steps = %d, want 2", len(steps))
	}
	first := steps[0].(map[string]any)["location"].(map[string]any)["physicalLocation"].(map[string]any)
	if got := first["artifactLocation"].(map[string]any)["uri"]; got != "src/Api.java" {
		t.Errorf("first step uri = %v, want src/Api.java", got)
	}

	rule := rules[0].(map[string]any)
	ruleProps := rule["properties"].(map[string]any)
	if ruleProps["cwe"] != "CWE-89" || ruleProps["confidence"] != database.ConfidenceDataflowReachable {
		t.Errorf("rule properties = %v, want cwe and confidence", ruleProps)
	}
}

// A pentest record must produce exactly the same SARIF shape as before, so that
// existing consumers do not see new fields appear out of nowhere.
func TestBuildSARIFPentestRecordIsUnchanged(t *testing.T) {
	pentest := &database.Vulnerability{
		ID:          "v2",
		Title:       "Reflected XSS",
		Description: "the name parameter is echoed unencoded",
		Severity:    "medium",
		Status:      "open",
		Type:        "XSS",
		Target:      "https://example.com/search?q=",
		Evidence:    "GET /search?q=<script>alert(1)</script>",
	}

	result, rules := sarifFor(t, []*database.Vulnerability{pentest})

	if got := result["ruleId"]; !strings.HasPrefix(got.(string), "provena/vulnerability/") {
		t.Errorf("ruleId = %v, want the provena prefix when there is no rule id", got)
	}
	if _, ok := result["codeFlows"]; ok {
		t.Error("codeFlows must be absent when no data flow was recorded")
	}
	props := result["properties"].(map[string]any)
	if _, ok := props["cwe"]; ok {
		t.Error("properties.cwe must be absent when no CWE was recorded")
	}
	if _, ok := props["confidence"]; ok {
		t.Error("properties.confidence must be absent when no tier was recorded")
	}
	physical := result["locations"].([]any)[0].(map[string]any)["physicalLocation"].(map[string]any)
	if got := physical["artifactLocation"].(map[string]any)["uri"]; got != pentest.Target {
		t.Errorf("artifact uri = %v, want the target", got)
	}
	if _, ok := physical["region"]; ok {
		t.Error("region must be absent when no line numbers were recorded")
	}
	if ruleProps, ok := rules[0].(map[string]any)["properties"]; ok {
		t.Errorf("rule properties must be absent for a pentest record, got %v", ruleProps)
	}
}

func TestBuildMarkdownShowsAuditFields(t *testing.T) {
	vulns := []*database.Vulnerability{
		{
			ID: "v1", Title: "Path traversal", Description: "d", Severity: "high", Status: "open",
			FilePath: "src/Download.java", StartLine: 88,
			CWEID: "CWE-22", Confidence: database.ConfidenceDynamicallyConfirmed,
			CodeFlow: "src/Api.java:12\nsrc/Download.java:88",
		},
		{
			ID: "v2", Title: "Missing authorization", Description: "d", Severity: "high", Status: "open",
			FilePath: "src/Admin.java", CWEID: "CWE-862", Confidence: database.ConfidenceHeuristic,
		},
	}

	md := buildMarkdown(Result{Target: "./src", RunID: "r1", Status: "completed"}, fgs.Snapshot{}, vulns)

	for _, want := range []string{
		"### By confidence",
		"| " + database.ConfidenceDynamicallyConfirmed + " | 1 |",
		"| " + database.ConfidenceHeuristic + " | 1 |",
		"- Location: `src/Download.java:88`",
		"- CWE: CWE-22",
		"- Confidence: " + database.ConfidenceDynamicallyConfirmed,
		"**Data flow**",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown is missing %q", want)
		}
	}
}

// The confidence summary is an audit-only affordance: a pentest report must not
// grow a section that only ever says "unlabelled".
func TestBuildMarkdownOmitsConfidenceSummaryWithoutTiers(t *testing.T) {
	vulns := []*database.Vulnerability{{
		ID: "v1", Title: "XSS", Description: "d", Severity: "medium", Status: "open",
		Target: "https://example.com",
	}}

	md := buildMarkdown(Result{Target: "https://example.com"}, fgs.Snapshot{}, vulns)
	if strings.Contains(md, "By confidence") {
		t.Error("a report without confidence tiers must not render the confidence summary")
	}
	if strings.Contains(md, "- Location:") {
		t.Error("a report without file paths must not render a location line")
	}
}
