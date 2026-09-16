package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chobits02/provena/internal/fgs"
	"github.com/chobits02/provena/internal/project"
	"github.com/chobits02/provena/internal/security"

	"github.com/gin-gonic/gin"
)

type fgsRunSummary struct {
	RunID       string    `json:"runId"`
	Version     int       `json:"version"`
	UpdatedAt   time.Time `json:"updatedAt"`
	NodeCount   int       `json:"nodeCount"`
	EdgeCount   int       `json:"edgeCount"`
	GraphStatus string    `json:"graphStatus"`
}

type conversationFGSResponse struct {
	ConversationID string          `json:"conversationId"`
	Available      bool            `json:"available"`
	Active         bool            `json:"active"`
	RunID          string          `json:"runId,omitempty"`
	Snapshot       *fgs.Snapshot   `json:"snapshot,omitempty"`
	Runs           []fgsRunSummary `json:"runs"`
}

// GetConversationFGS returns the newest append-only FGS graph for a conversation.
// The graph is kept in the conversation workspace, so this endpoint deliberately
// exposes only graph data and run metadata, never the server-side file path.
func (h *AgentHandler) GetConversationFGS(c *gin.Context) {
	conversationID := strings.TrimSpace(c.Param("id"))
	if conversationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "conversation id required"})
		return
	}
	if h == nil || h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "agent handler unavailable"})
		return
	}
	if _, err := h.db.GetConversation(conversationID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "对话不存在"})
		return
	}
	session, ok := security.CurrentSession(c)
	if !ok || !h.db.UserCanAccessResource(session.UserID, session.Scope, "conversation", conversationID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权访问该对话的 FGS"})
		return
	}

	workspaceBase := ""
	if h.config != nil {
		workspaceBase = strings.TrimSpace(h.config.PiAgent.WorkingDir)
		if workspaceBase == "" {
			workspaceBase = strings.TrimSpace(h.config.Agent.WorkspaceRootDir)
		}
	}
	conversationRoot, err := filepath.Abs(project.ConversationWorkspaceRootDir(workspaceBase, conversationID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "解析 FGS 工作目录失败"})
		return
	}

	runsRoot := filepath.Join(conversationRoot, ".fgs", "runs")
	runID := strings.TrimSpace(c.Query("run_id"))
	response := conversationFGSResponse{
		ConversationID: conversationID,
		Runs:           []fgsRunSummary{},
		Active:         h.tasks != nil && h.tasks.GetTaskSnapshot(conversationID) != nil,
	}

	entries, err := os.ReadDir(runsRoot)
	if os.IsNotExist(err) {
		c.JSON(http.StatusOK, response)
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取 FGS 运行目录失败"})
		return
	}

	type loadedRun struct {
		id       string
		modified time.Time
		snapshot fgs.Snapshot
	}
	loaded := make([]loadedRun, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.TrimSpace(entry.Name()) == "" {
			continue
		}
		graphPath := filepath.Join(runsRoot, entry.Name(), "graph.jsonl")
		info, statErr := os.Stat(graphPath)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		store, openErr := fgs.Open(graphPath, "")
		if openErr != nil {
			// An active Pi process can be between two append writes. Ignore that
			// snapshot for this poll; the browser will retry shortly.
			continue
		}
		loaded = append(loaded, loadedRun{id: entry.Name(), modified: info.ModTime(), snapshot: store.Snapshot()})
	}
	sort.SliceStable(loaded, func(i, j int) bool {
		return loaded[i].modified.After(loaded[j].modified)
	})
	for _, run := range loaded {
		snapshot := run.snapshot
		response.Runs = append(response.Runs, fgsRunSummary{
			RunID:       run.id,
			Version:     snapshot.Version,
			UpdatedAt:   snapshot.UpdatedAt,
			NodeCount:   len(snapshot.Nodes),
			EdgeCount:   len(snapshot.Edges),
			GraphStatus: fgsGraphStatus(snapshot),
		})
	}

	if runID != "" {
		for _, run := range loaded {
			if run.id != runID {
				continue
			}
			snapshot := run.snapshot
			response.Available = true
			response.RunID = run.id
			response.Snapshot = &snapshot
			c.JSON(http.StatusOK, response)
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "FGS 运行不存在"})
		return
	}
	if len(loaded) > 0 {
		snapshot := loaded[0].snapshot
		response.Available = true
		response.RunID = loaded[0].id
		response.Snapshot = &snapshot
	}
	c.JSON(http.StatusOK, response)
}

func fgsGraphStatus(snapshot fgs.Snapshot) string {
	for _, node := range snapshot.Nodes {
		if node.Kind == fgs.KindGoal {
			switch node.Status {
			case fgs.StatusCompleted:
				return "completed"
			case fgs.StatusBlocked:
				return "blocked"
			case fgs.StatusAbandoned:
				return "abandoned"
			}
		}
	}
	return "in_progress"
}
