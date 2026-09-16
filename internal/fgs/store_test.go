package fgs

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreUsesAppendOnlyEventsAndRejectsCycles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.jsonl")
	store, err := Open(path, "完成一个目标")
	if err != nil {
		t.Fatal(err)
	}
	first := store.Snapshot()
	if len(first.Nodes) != 2 || len(first.Edges) != 1 {
		t.Fatalf("initial graph = %+v", first)
	}
	if _, err := store.Apply([]Mutation{
		{Op: "add_node", ID: "step-1", Kind: KindStep, Label: "第一步"},
		{Op: "add_edge", From: "goal", To: "step-1", Relation: "next"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply([]Mutation{{Op: "add_edge", From: "step-1", To: "origin", Relation: "bad"}}); err == nil {
		t.Fatal("expected cycle rejection")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		count++
	}
	if count != 6 {
		t.Fatalf("event count = %d, want 6", count)
	}
}

func TestSubmitFactAddsFactAndProducesEdge(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "graph.jsonl"), "goal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply([]Mutation{{Op: "add_node", ID: "step-1", Kind: KindStep}}); err != nil {
		t.Fatal(err)
	}
	result, err := store.SubmitFact("观察", "得到一个事实", "step-1", []string{"证据"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.AddedIDs) != 1 || len(result.Snapshot.Nodes) != 4 || len(result.Snapshot.Edges) != 2 {
		t.Fatalf("submit result = %+v", result)
	}
}

func TestReplayPreservesNodeCreatedAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.jsonl")
	store, err := Open(path, "goal")
	if err != nil {
		t.Fatal(err)
	}
	created := store.Snapshot().Nodes
	if len(created) != 2 {
		t.Fatalf("initial nodes = %d, want 2", len(created))
	}
	if _, err := store.Apply([]Mutation{{Op: "add_node", ID: "step-1", Kind: KindStep, Label: "观察"}}); err != nil {
		t.Fatal(err)
	}
	want := store.Snapshot()

	// Ensure the test catches a replay implementation that assigns fresh
	// timestamps instead of restoring the event timestamp.
	time.Sleep(2 * time.Millisecond)
	reopened, err := Open(path, "")
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.Snapshot()
	if len(got.Nodes) != len(want.Nodes) {
		t.Fatalf("replayed node count = %d, want %d", len(got.Nodes), len(want.Nodes))
	}
	for i := range want.Nodes {
		if !got.Nodes[i].CreatedAt.Equal(want.Nodes[i].CreatedAt) {
			t.Fatalf("node %q CreatedAt changed on replay: got %s, want %s", got.Nodes[i].ID, got.Nodes[i].CreatedAt, want.Nodes[i].CreatedAt)
		}
	}
}
