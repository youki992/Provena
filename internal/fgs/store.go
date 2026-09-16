// Package fgs implements the external Fact/Intent Graph used by the Pi
// harness. The graph is reconstructed from an append-only event log: no
// activity edits a previous node or replaces the graph wholesale.
package fgs

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type NodeKind string

const (
	KindOrigin  NodeKind = "origin"
	KindGoal    NodeKind = "goal"
	KindFact    NodeKind = "fact"
	KindIntent  NodeKind = "intent"
	KindStep    NodeKind = "step"
	KindSubGoal NodeKind = "sub_goal"
	KindHint    NodeKind = "hint"
	KindFinding NodeKind = "finding"
)

type NodeStatus string

const (
	StatusPending   NodeStatus = "pending"
	StatusActive    NodeStatus = "active"
	StatusConfirmed NodeStatus = "confirmed"
	StatusCompleted NodeStatus = "completed"
	StatusAbandoned NodeStatus = "abandoned"
	StatusBlocked   NodeStatus = "blocked"
)

type Node struct {
	ID        string     `json:"id"`
	Kind      NodeKind   `json:"kind"`
	Label     string     `json:"label,omitempty"`
	Content   string     `json:"content,omitempty"`
	Status    NodeStatus `json:"status"`
	Priority  int        `json:"priority"`
	Evidence  []string   `json:"evidence,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

type Edge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Relation string `json:"relation,omitempty"`
}

// Snapshot is the only graph representation exposed to model activities.
// UpdatedAt and Version allow the harness to detect externalized progress.
type Snapshot struct {
	ID        string    `json:"id"`
	Goal      string    `json:"goal"`
	Version   int       `json:"version"`
	Nodes     []Node    `json:"nodes"`
	Edges     []Edge    `json:"edges"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Mutation is deliberately small. State changes are represented as events;
// callers cannot submit a replacement graph or mutate an existing node body.
type Mutation struct {
	Op       string     `json:"op"`
	ID       string     `json:"id,omitempty"`
	Kind     NodeKind   `json:"kind,omitempty"`
	Label    string     `json:"label,omitempty"`
	Content  string     `json:"content,omitempty"`
	Status   NodeStatus `json:"status,omitempty"`
	Priority int        `json:"priority,omitempty"`
	Evidence []string   `json:"evidence,omitempty"`
	From     string     `json:"from,omitempty"`
	To       string     `json:"to,omitempty"`
	Relation string     `json:"relation,omitempty"`
}

type Event struct {
	Seq      int       `json:"seq"`
	At       time.Time `json:"at"`
	Op       string    `json:"op"`
	GraphID  string    `json:"graphId,omitempty"`
	Goal     string    `json:"goal,omitempty"`
	Mutation Mutation  `json:"mutation,omitempty"`
}

type Store struct {
	mu      sync.RWMutex
	path    string
	graph   Snapshot
	nextSeq int
}

// Open creates or replays an append-only graph log at path.
func Open(path, goal string) (*Store, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return nil, fmt.Errorf("FGS 路径不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建 FGS 目录失败: %w", err)
	}
	s := &Store{path: path}
	if err := s.replay(); err != nil {
		return nil, err
	}
	if s.graph.ID == "" {
		s.graph.ID = "fgs-" + randomID()
		s.graph.Goal = trimText(goal, 4000)
		if strings.TrimSpace(s.graph.Goal) == "" {
			s.graph.Goal = "未命名目标"
		}
		if err := s.appendEvents([]Event{{Op: "graph_created", GraphID: s.graph.ID, Goal: s.graph.Goal}}); err != nil {
			return nil, err
		}
		if _, err := s.Apply([]Mutation{
			{Op: "add_node", ID: "origin", Kind: KindOrigin, Label: "Origin", Content: s.graph.Goal, Status: StatusConfirmed},
			{Op: "add_node", ID: "goal", Kind: KindGoal, Label: "Goal", Content: s.graph.Goal, Status: StatusPending},
			{Op: "add_edge", From: "origin", To: "goal", Relation: "motivates"},
		}); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.path
}

func (s *Store) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSnapshot(s.graph)
}

func (s *Store) Version() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.graph.Version
}

type ApplyResult struct {
	Snapshot Snapshot `json:"snapshot"`
	AddedIDs []string `json:"addedIds,omitempty"`
}

func (s *Store) Apply(mutations []Mutation) (ApplyResult, error) {
	if s == nil {
		return ApplyResult{}, fmt.Errorf("FGS store 未初始化")
	}
	if len(mutations) == 0 {
		return ApplyResult{Snapshot: s.Snapshot()}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := cloneSnapshot(s.graph)
	events := make([]Event, 0, len(mutations))
	added := make([]string, 0)
	at := time.Now().UTC()
	for _, mutation := range mutations {
		mutation = normalizeMutation(mutation)
		if err := applyMutationAt(&candidate, mutation, at); err != nil {
			return ApplyResult{}, err
		}
		events = append(events, Event{At: at, Op: eventOp(mutation.Op), Mutation: mutation})
		if mutation.Op == "add_node" {
			added = append(added, mutation.ID)
		}
	}
	if err := s.appendEventsLocked(events); err != nil {
		return ApplyResult{}, err
	}
	s.graph = candidate
	return ApplyResult{Snapshot: cloneSnapshot(s.graph), AddedIDs: added}, nil
}

func (s *Store) SubmitFact(label, content, stepID string, evidence []string) (ApplyResult, error) {
	return s.submitNode(KindFact, label, content, stepID, evidence)
}

func (s *Store) SubmitFinding(label, content, stepID string, evidence []string) (ApplyResult, error) {
	return s.submitNode(KindFinding, label, content, stepID, evidence)
}

func (s *Store) submitNode(kind NodeKind, label, content, stepID string, evidence []string) (ApplyResult, error) {
	id := "fact-" + randomID()
	if kind == KindFinding {
		id = "finding-" + randomID()
	}
	mutations := []Mutation{{
		Op:       "add_node",
		ID:       id,
		Kind:     kind,
		Label:    label,
		Content:  content,
		Status:   StatusConfirmed,
		Evidence: evidence,
	}}
	if strings.TrimSpace(stepID) != "" {
		mutations = append(mutations, Mutation{Op: "add_edge", From: stepID, To: id, Relation: "produces"})
	}
	return s.Apply(mutations)
}

func (s *Store) replay() error {
	file, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("打开 FGS 日志失败: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return fmt.Errorf("解析 FGS 事件失败: %w", err)
		}
		if event.Seq > s.nextSeq {
			s.nextSeq = event.Seq
		}
		if event.Op == "graph_created" {
			s.graph.ID = event.GraphID
			s.graph.Goal = event.Goal
			if !event.At.IsZero() {
				s.graph.UpdatedAt = event.At
			}
		}
		if event.Mutation.Op != "" {
			at := event.At
			if at.IsZero() {
				// Events written by very old versions may not have a timestamp.
				// Keep replay deterministic for current logs while retaining a
				// sensible fallback for those legacy records.
				at = time.Now().UTC()
			}
			if err := applyMutationAt(&s.graph, normalizeMutation(event.Mutation), at); err != nil {
				return fmt.Errorf("重放 FGS 事件 %d 失败: %w", event.Seq, err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取 FGS 日志失败: %w", err)
	}
	return nil
}

func (s *Store) appendEvents(events []Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendEventsLocked(events)
}

func (s *Store) appendEventsLocked(events []Event) error {
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("打开 FGS 追加日志失败: %w", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for i := range events {
		s.nextSeq++
		events[i].Seq = s.nextSeq
		if events[i].At.IsZero() {
			events[i].At = time.Now().UTC()
		}
		if err := encoder.Encode(events[i]); err != nil {
			return fmt.Errorf("写入 FGS 事件失败: %w", err)
		}
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("同步 FGS 日志失败: %w", err)
	}
	return nil
}

func applyMutation(graph *Snapshot, mutation Mutation) error {
	return applyMutationAt(graph, mutation, time.Now().UTC())
}

func applyMutationAt(graph *Snapshot, mutation Mutation, at time.Time) error {
	if graph == nil {
		return fmt.Errorf("FGS 图为空")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	switch mutation.Op {
	case "add_node":
		if strings.TrimSpace(mutation.ID) == "" {
			return fmt.Errorf("add_node 缺少 id")
		}
		if findNode(graph, mutation.ID) != nil {
			return fmt.Errorf("节点已存在: %s", mutation.ID)
		}
		kind := mutation.Kind
		if kind == "" {
			kind = KindStep
		}
		status := mutation.Status
		if status == "" {
			status = StatusPending
		}
		graph.Nodes = append(graph.Nodes, Node{
			ID: mutation.ID, Kind: kind, Label: trimText(mutation.Label, 300), Content: trimText(mutation.Content, 8000),
			Status: status, Priority: mutation.Priority, Evidence: trimList(mutation.Evidence, 8, 1200), CreatedAt: at,
		})
	case "add_edge":
		if strings.TrimSpace(mutation.From) == "" || strings.TrimSpace(mutation.To) == "" {
			return fmt.Errorf("add_edge 缺少 from 或 to")
		}
		if mutation.From == mutation.To {
			return fmt.Errorf("FGS DAG 不允许自环: %s", mutation.From)
		}
		if findNode(graph, mutation.From) == nil || findNode(graph, mutation.To) == nil {
			return fmt.Errorf("边引用了不存在的节点: %s -> %s", mutation.From, mutation.To)
		}
		for _, edge := range graph.Edges {
			if edge.From == mutation.From && edge.To == mutation.To && edge.Relation == mutation.Relation {
				return fmt.Errorf("边已存在: %s -> %s", mutation.From, mutation.To)
			}
		}
		if pathExists(*graph, mutation.To, mutation.From) {
			return fmt.Errorf("FGS DAG 不允许形成环: %s -> %s", mutation.From, mutation.To)
		}
		graph.Edges = append(graph.Edges, Edge{From: mutation.From, To: mutation.To, Relation: trimText(mutation.Relation, 120)})
	case "set_status":
		node := findNode(graph, mutation.ID)
		if node == nil {
			return fmt.Errorf("set_status 节点不存在: %s", mutation.ID)
		}
		if mutation.Status == "" {
			return fmt.Errorf("set_status 缺少 status")
		}
		node.Status = mutation.Status
	case "set_priority":
		node := findNode(graph, mutation.ID)
		if node == nil {
			return fmt.Errorf("set_priority 节点不存在: %s", mutation.ID)
		}
		if mutation.Priority < -1000 || mutation.Priority > 1000 {
			return fmt.Errorf("priority 超出范围: %d", mutation.Priority)
		}
		node.Priority = mutation.Priority
	case "remove_edge":
		kept := graph.Edges[:0]
		removed := false
		for _, edge := range graph.Edges {
			if edge.From == mutation.From && edge.To == mutation.To && (mutation.Relation == "" || edge.Relation == mutation.Relation) {
				removed = true
				continue
			}
			kept = append(kept, edge)
		}
		if !removed {
			return fmt.Errorf("边不存在: %s -> %s", mutation.From, mutation.To)
		}
		graph.Edges = kept
	case "abandon_node":
		mutation.Op = "set_status"
		mutation.Status = StatusAbandoned
		return applyMutationAt(graph, mutation, at)
	default:
		return fmt.Errorf("不支持的 FGS 操作: %s", mutation.Op)
	}
	graph.Version++
	graph.UpdatedAt = time.Now().UTC()
	return nil
}

func normalizeMutation(m Mutation) Mutation {
	m.Op = strings.ToLower(strings.TrimSpace(m.Op))
	m.ID = strings.TrimSpace(m.ID)
	m.From = strings.TrimSpace(m.From)
	m.To = strings.TrimSpace(m.To)
	m.Label = strings.TrimSpace(m.Label)
	m.Content = strings.TrimSpace(m.Content)
	m.Relation = strings.TrimSpace(m.Relation)
	return m
}

func eventOp(op string) string {
	switch op {
	case "add_node":
		return "node_added"
	case "add_edge":
		return "edge_added"
	case "set_status", "abandon_node":
		return "node_status_changed"
	case "set_priority":
		return "node_priority_changed"
	case "remove_edge":
		return "edge_removed"
	default:
		return op
	}
}

func findNode(graph *Snapshot, id string) *Node {
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == id {
			return &graph.Nodes[i]
		}
	}
	return nil
}

func pathExists(graph Snapshot, from, to string) bool {
	seen := make(map[string]bool)
	var visit func(string) bool
	visit = func(current string) bool {
		if current == to {
			return true
		}
		if seen[current] {
			return false
		}
		seen[current] = true
		for _, edge := range graph.Edges {
			if edge.From == current && visit(edge.To) {
				return true
			}
		}
		return false
	}
	return visit(from)
}

func cloneSnapshot(in Snapshot) Snapshot {
	out := in
	out.Nodes = append([]Node(nil), in.Nodes...)
	out.Edges = append([]Edge(nil), in.Edges...)
	for i := range out.Nodes {
		out.Nodes[i].Evidence = append([]string(nil), in.Nodes[i].Evidence...)
	}
	return out
}

func trimText(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len([]rune(value)) <= max {
		return value
	}
	return string([]rune(value)[:max])
}

func trimList(values []string, maxItems, maxRunes int) []string {
	if len(values) > maxItems {
		values = values[:maxItems]
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = trimText(value, maxRunes); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func randomID() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
