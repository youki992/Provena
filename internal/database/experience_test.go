package database

import (
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestExperienceRequiresVerificationBeforePromotion(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "experience.db"), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entry, err := db.CreateExperience(&ExperienceEntry{Title: "ARL asset observation", Category: "arl-observation", Content: "asset evidence", EvidenceRefs: []string{"source=arl"}, SourceFGSPath: "graph.jsonl", SourceNodeIDs: []string{"fact-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if entry.VerificationStatus != "pending" {
		t.Fatalf("status = %q, want pending", entry.VerificationStatus)
	}
	if err := db.MarkExperiencePromoted(entry.ID, "knowledge_base/经验库/example.md"); err == nil {
		t.Fatal("pending experience must not be promoted")
	}
	verified, err := db.SetExperienceVerification(entry.ID, "human_confirmed", "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if verified.VerifiedBy != "reviewer" || verified.VerifiedAt.IsZero() {
		t.Fatalf("missing audit attribution: %+v", verified)
	}
	if err := db.MarkExperiencePromoted(entry.ID, "knowledge_base/经验库/example.md"); err != nil {
		t.Fatal(err)
	}
}
