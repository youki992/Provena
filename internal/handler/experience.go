package handler

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/chobits02/provena/internal/database"
	"github.com/chobits02/provena/internal/fgs"
	"github.com/chobits02/provena/internal/security"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ExperienceHandler exposes the review gate for the long-term experience
// library. Listing is read-only; verification and promotion are explicit write
// actions so model output can never silently become durable guidance.
type ExperienceHandler struct {
	db       *database.DB
	basePath string
	logger   *zap.Logger
}

func NewExperienceHandler(db *database.DB, basePath string, logger *zap.Logger) *ExperienceHandler {
	if strings.TrimSpace(basePath) == "" {
		basePath = "knowledge_base/经验库"
	}
	return &ExperienceHandler{db: db, basePath: basePath, logger: logger}
}

func (h *ExperienceHandler) List(c *gin.Context) {
	status, projectID := c.Query("status"), c.Query("project_id")
	items, err := h.db.ListExperiences(status, projectID, 100, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []*database.ExperienceEntry{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *ExperienceHandler) Get(c *gin.Context) {
	e, err := h.db.GetExperience(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, e)
}

type experienceVerificationRequest struct {
	Status string `json:"status" binding:"required"`
}

func (h *ExperienceHandler) Verify(c *gin.Context) {
	var req experienceVerificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// `verified` is reserved for the deterministic evidence controller. A
	// person can confirm, reject, or return an item to pending, but cannot
	// make an unverified assertion look like controller-checked evidence.
	if req.Status != "human_confirmed" && req.Status != "rejected" && req.Status != "pending" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "人工操作 status 仅允许 human_confirmed、rejected 或 pending；verified 由外部证据控制器专用"})
		return
	}
	session, _ := security.CurrentSession(c)
	by := session.UserID
	e, err := h.db.SetExperienceVerification(c.Param("id"), req.Status, by)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, e)
}

func (h *ExperienceHandler) Promote(c *gin.Context) {
	e, err := h.db.GetExperience(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if e.VerificationStatus != "verified" && e.VerificationStatus != "human_confirmed" {
		c.JSON(http.StatusConflict, gin.H{"error": "经验必须先通过证据验证或人工确认"})
		return
	}
	if err := os.MkdirAll(h.basePath, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	name := safeExperienceFilename(e.Title)
	if name == "" {
		name = uuid.New().String()
	}
	path := filepath.Join(h.basePath, name+"-"+e.ID[:8]+".md")
	content := fmt.Sprintf("# %s\n\n- 验证状态：%s\n- 验证人：%s\n- 项目：%s\n- 来源 FGS：%s\n- 来源节点：%s\n\n## 经验\n\n%s\n\n## 证据引用\n\n%s\n", e.Title, e.VerificationStatus, e.VerifiedBy, e.ProjectID, e.SourceFGSPath, strings.Join(e.SourceNodeIDs, ", "), e.Content, strings.Join(e.EvidenceRefs, "\n"))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.db.MarkExperiencePromoted(e.ID, path); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"item": e, "path": path, "message": "已晋级到长期经验库；如启用向量知识库，请随后执行索引"})
}

var experienceFilenamePattern = regexp.MustCompile(`[^\p{L}\p{N}._-]+`)

func safeExperienceFilename(s string) string {
	s = strings.TrimSpace(s)
	s = experienceFilenamePattern.ReplaceAllString(s, "-")
	s = strings.Trim(s, ".-")
	if len([]rune(s)) > 80 {
		s = string([]rune(s)[:80])
	}
	return s
}

// capturePendingExperiences links ARL observations and evidence-bearing
// findings to the review queue. It is idempotent per FGS path/node pair.
func (h *AgentHandler) capturePendingExperiences(graph *fgs.Store, conversationID string) {
	if h == nil || h.db == nil || graph == nil {
		return
	}
	snapshot := graph.Snapshot()
	projectID := h.conversationProjectID(conversationID)
	for _, node := range snapshot.Nodes {
		if node.Kind != fgs.KindFact && node.Kind != fgs.KindFinding {
			continue
		}
		if len(node.Evidence) == 0 || strings.TrimSpace(node.Content) == "" {
			continue
		}
		isARL := false
		for _, ref := range node.Evidence {
			if strings.HasPrefix(strings.ToLower(ref), "source=arl") {
				isARL = true
				break
			}
		}
		if !isARL && node.Kind != fgs.KindFinding {
			continue
		}
		// Tool failures must remain in FGS for retry planning, but they are not
		// reusable experience candidates and can never be auto-verified.
		if isARL && fgsNodeHasFailedARLResult(node) {
			continue
		}
		exists, err := h.db.ExperienceExistsBySource(graph.Path(), node.ID)
		if err != nil || exists {
			continue
		}
		category := "fgs-finding"
		if isARL {
			category = "arl-observation"
		}
		_, err = h.db.CreateExperience(&database.ExperienceEntry{Title: node.Label, Category: category, Content: node.Content, EvidenceRefs: node.Evidence, ProjectID: projectID, SourceConversationID: conversationID, SourceFGSPath: graph.Path(), SourceNodeIDs: []string{node.ID}})
		if err != nil && h.logger != nil {
			h.logger.Warn("登记待验证经验失败", zap.Error(err), zap.String("nodeId", node.ID))
		}
	}
}

// syncARLResultsToProjectBlackboard makes current ARL observations visible to
// other conversations in the same project. They remain tentative by design:
// an MCP response is a lead, not confirmation of an asset or vulnerability.
func (h *AgentHandler) syncARLResultsToProjectBlackboard(graph *fgs.Store, conversationID string) {
	if h == nil || h.db == nil || h.config == nil || !h.config.Project.Enabled || graph == nil {
		return
	}
	projectID := h.conversationProjectID(conversationID)
	if projectID == "" {
		return
	}
	for _, node := range graph.Snapshot().Nodes {
		if node.Kind != fgs.KindFact || strings.TrimSpace(node.Content) == "" {
			continue
		}
		var hash string
		for _, ref := range node.Evidence {
			if strings.HasPrefix(ref, "arl_result_hash:") {
				hash = strings.TrimPrefix(ref, "arl_result_hash:")
				break
			}
		}
		if hash == "" {
			continue
		}
		key := "arl/" + hash
		_, err := h.db.UpsertProjectFact(&database.ProjectFact{
			ProjectID: projectID, FactKey: key, Category: "arl_observation", Summary: node.Label,
			Body:       node.Content + "\n\nFGS 节点：" + node.ID + "\nFGS 路径：" + graph.Path(),
			Confidence: "tentative", SourceConversationID: conversationID,
		})
		if err != nil && h.logger != nil {
			h.logger.Warn("同步 ARL 观察到项目黑板失败", zap.Error(err), zap.String("nodeId", node.ID))
		}
	}
}
