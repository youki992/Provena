package run

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chobits02/provena/internal/database"
	"github.com/chobits02/provena/internal/fgs"
)

const (
	formatMarkdown = "md"
	formatJSON     = "json"
	formatSARIF    = "sarif"
)

// normalizeFormat accepts a few spellings and falls back to markdown.
func normalizeFormat(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "sarif", "sarif-2.1.0":
		return formatSARIF
	case "json":
		return formatJSON
	default:
		return formatMarkdown
	}
}

func findingsOf(snapshot fgs.Snapshot) []fgs.Node {
	return nodesByKind(snapshot, fgs.KindFinding)
}

func factsOf(snapshot fgs.Snapshot) []fgs.Node {
	return nodesByKind(snapshot, fgs.KindFact)
}

func nodesByKind(snapshot fgs.Snapshot, kind fgs.NodeKind) []fgs.Node {
	out := make([]fgs.Node, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node.Kind == kind && node.Status != fgs.StatusAbandoned {
			out = append(out, node)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// buildMarkdown renders a human-readable engagement report from the graph and
// any structured vulnerabilities the run recorded through the platform tools.
func buildMarkdown(res Result, snapshot fgs.Snapshot, vulns []*database.Vulnerability) string {
	var b strings.Builder

	b.WriteString("# Provena run report\n\n")
	fmt.Fprintf(&b, "- Run ID: `%s`\n", res.RunID)
	fmt.Fprintf(&b, "- Target: `%s`\n", res.Target)
	if strings.TrimSpace(res.Objective) != "" {
		fmt.Fprintf(&b, "- Objective: %s\n", res.Objective)
	}
	if len(res.Scope) > 0 {
		fmt.Fprintf(&b, "- Scope: %s\n", strings.Join(res.Scope, ", "))
	}
	fmt.Fprintf(&b, "- Status: %s\n", res.Status)
	fmt.Fprintf(&b, "- Activities: %d\n", res.Activities)
	fmt.Fprintf(&b, "- Generated: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Graph: `%s`\n\n", res.GraphPath)

	findings := findingsOf(snapshot)
	facts := factsOf(snapshot)

	b.WriteString("## Summary\n\n")
	b.WriteString("| Item | Count |\n| --- | --- |\n")
	fmt.Fprintf(&b, "| Facts | %d |\n", len(facts))
	fmt.Fprintf(&b, "| Findings (evidence-backed, unconfirmed) | %d |\n", len(findings))
	fmt.Fprintf(&b, "| Recorded vulnerabilities | %d |\n\n", len(vulns))

	b.WriteString("> Findings are evidence-backed observations that were not necessarily confirmed as\n")
	b.WriteString("> exploitable issues. Recorded vulnerabilities come from the platform's structured\n")
	b.WriteString("> vulnerability store and carry an explicit severity.\n\n")

	b.WriteString("## Findings\n\n")
	if len(findings) == 0 {
		b.WriteString("No findings were recorded in this run.\n\n")
	} else {
		for i, node := range findings {
			fmt.Fprintf(&b, "### %d. %s\n\n", i+1, firstNonEmptyString(node.Label, node.ID))
			fmt.Fprintf(&b, "- Node: `%s`\n- Status: %s\n", node.ID, node.Status)
			if len(node.Evidence) > 0 {
				fmt.Fprintf(&b, "- Evidence: %s\n", strings.Join(node.Evidence, "; "))
			}
			b.WriteString("\n")
			if strings.TrimSpace(node.Content) != "" {
				b.WriteString(strings.TrimSpace(node.Content))
				b.WriteString("\n\n")
			}
		}
	}

	if len(vulns) > 0 {
		b.WriteString("## Recorded vulnerabilities\n\n")
		for i, vuln := range vulns {
			fmt.Fprintf(&b, "### %d. %s\n\n", i+1, firstNonEmptyString(vuln.Title, vuln.ID))
			fmt.Fprintf(&b, "- Severity: %s\n- Status: %s\n", firstNonEmptyString(vuln.Severity, "unknown"), vuln.Status)
			if strings.TrimSpace(vuln.Target) != "" {
				fmt.Fprintf(&b, "- Target: %s\n", vuln.Target)
			}
			b.WriteString("\n")
			for _, section := range []struct{ label, value string }{
				{"Description", vuln.Description},
				{"Preconditions", vuln.Preconditions},
				{"Reproduction", vuln.ReproSteps},
				{"Evidence", vuln.Evidence},
				{"Impact", vuln.Impact},
				{"Recommendation", vuln.Recommendation},
			} {
				if strings.TrimSpace(section.value) == "" {
					continue
				}
				fmt.Fprintf(&b, "**%s**\n\n%s\n\n", section.label, strings.TrimSpace(section.value))
			}
		}
	}

	b.WriteString("## Confirmed facts\n\n")
	if len(facts) == 0 {
		b.WriteString("No facts were recorded in this run.\n")
	} else {
		for _, node := range facts {
			fmt.Fprintf(&b, "- **%s** — %s\n", firstNonEmptyString(node.Label, node.ID),
				oneLineSummary(node.Content, 240))
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n\n")
	b.WriteString("Authorized testing only. Verify every finding before acting on it.\n")
	return b.String()
}

// buildSARIF renders findings and vulnerabilities as SARIF 2.1.0 so results can
// be consumed by standard security tooling.
func buildSARIF(res Result, snapshot fgs.Snapshot, vulns []*database.Vulnerability) ([]byte, error) {
	type sarifMessage struct {
		Text string `json:"text"`
	}
	type sarifArtifactLocation struct {
		URI string `json:"uri"`
	}
	type sarifPhysicalLocation struct {
		ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	}
	type sarifLocation struct {
		PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
	}
	type sarifRule struct {
		ID               string       `json:"id"`
		Name             string       `json:"name,omitempty"`
		ShortDescription sarifMessage `json:"shortDescription"`
	}
	type sarifResult struct {
		RuleID    string          `json:"ruleId"`
		Level     string          `json:"level"`
		Message   sarifMessage    `json:"message"`
		Locations []sarifLocation `json:"locations,omitempty"`
		Props     map[string]any  `json:"properties,omitempty"`
	}
	type sarifDriver struct {
		Name           string      `json:"name"`
		Version        string      `json:"version,omitempty"`
		InformationURI string      `json:"informationUri,omitempty"`
		Rules          []sarifRule `json:"rules"`
	}
	type sarifTool struct {
		Driver sarifDriver `json:"driver"`
	}
	type sarifRun struct {
		Tool    sarifTool     `json:"tool"`
		Results []sarifResult `json:"results"`
	}
	type sarifLog struct {
		Schema  string     `json:"$schema"`
		Version string     `json:"version"`
		Runs    []sarifRun `json:"runs"`
	}

	rules := make([]sarifRule, 0)
	results := make([]sarifResult, 0)
	seenRule := make(map[string]struct{})

	addRule := func(id, name, description string) {
		if _, ok := seenRule[id]; ok {
			return
		}
		seenRule[id] = struct{}{}
		rules = append(rules, sarifRule{
			ID:               id,
			Name:             name,
			ShortDescription: sarifMessage{Text: description},
		})
	}

	location := func(target string) []sarifLocation {
		if strings.TrimSpace(target) == "" {
			return nil
		}
		return []sarifLocation{{
			PhysicalLocation: sarifPhysicalLocation{
				ArtifactLocation: sarifArtifactLocation{URI: target},
			},
		}}
	}

	for _, node := range findingsOf(snapshot) {
		ruleID := "provena/finding/" + slugify(firstNonEmptyString(node.Label, node.ID))
		addRule(ruleID, node.Label, oneLineSummary(node.Content, 200))
		results = append(results, sarifResult{
			RuleID:    ruleID,
			Level:     "warning",
			Message:   sarifMessage{Text: firstNonEmptyString(node.Content, node.Label, node.ID)},
			Locations: location(res.Target),
			Props: map[string]any{
				"nodeId":   node.ID,
				"status":   string(node.Status),
				"evidence": node.Evidence,
			},
		})
	}

	for _, vuln := range vulns {
		ruleID := "provena/vulnerability/" + slugify(firstNonEmptyString(vuln.Type, vuln.Title, vuln.ID))
		addRule(ruleID, vuln.Title, oneLineSummary(vuln.Description, 200))
		results = append(results, sarifResult{
			RuleID:    ruleID,
			Level:     sarifLevel(vuln.Severity),
			Message:   sarifMessage{Text: firstNonEmptyString(vuln.Description, vuln.Title, vuln.ID)},
			Locations: location(firstNonEmptyString(vuln.Target, res.Target)),
			Props: map[string]any{
				"vulnerabilityId": vuln.ID,
				"severity":        vuln.Severity,
				"status":          vuln.Status,
				"evidence":        vuln.Evidence,
			},
		})
	}

	log := sarifLog{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:           "Provena",
				InformationURI: "https://github.com/chobits02/provena",
				Rules:          rules,
			}},
			Results: results,
		}},
	}
	return json.MarshalIndent(log, "", "  ")
}

// buildJSON dumps the machine-readable run record.
func buildJSON(res Result, snapshot fgs.Snapshot, vulns []*database.Vulnerability) ([]byte, error) {
	payload := map[string]any{
		"run": map[string]any{
			"id":         res.RunID,
			"target":     res.Target,
			"objective":  res.Objective,
			"scope":      res.Scope,
			"status":     res.Status,
			"activities": res.Activities,
			"startedAt":  res.StartedAt,
			"endedAt":    res.EndedAt,
			"graphPath":  res.GraphPath,
			"conversationId": res.ConversationID,
		},
		"graph":      snapshot,
		"findings":   findingsOf(snapshot),
		"facts":      factsOf(snapshot),
		"vulnerabilities": vulns,
	}
	return json.MarshalIndent(payload, "", "  ")
}

func sarifLevel(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical", "high":
		return "error"
	case "medium":
		return "warning"
	case "low", "info":
		return "note"
	default:
		return "warning"
	}
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unnamed"
	}
	if len(out) > 80 {
		out = strings.Trim(out[:80], "-")
	}
	return out
}

func oneLineSummary(value string, max int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	return truncateRunes(value, max)
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
