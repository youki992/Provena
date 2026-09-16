package handler

import (
	"fmt"
	"strings"

	"github.com/chobits02/provena/internal/authctx"
	"github.com/chobits02/provena/internal/database"
	"github.com/chobits02/provena/internal/project"
	"go.uber.org/zap"
)

// agentSessionContextBlock 注入会话工作目录与项目黑板（用于 system prompt 追加块）。
// 用户输入由 message history 承载；压缩后由 summarization 摘要指令保留关键约束。
func (h *AgentHandler) agentSessionContextBlock(conversationID string) string {
	var parts []string
	if ws := h.buildWorkspaceBlock(conversationID); ws != "" {
		parts = append(parts, ws)
	}
	if bb := h.projectBlackboardBlock(conversationID); bb != "" {
		parts = append(parts, bb)
	}
	return strings.Join(parts, "\n\n")
}

// agentSessionContextBlockForPrincipal is the Pi-specific context path. It
// adds project assets and vulnerability summaries only after the authenticated
// principal has been checked; the legacy context path above remains compatible
// with existing agents that do not carry a principal into prompt assembly.
func (h *AgentHandler) agentSessionContextBlockForPrincipal(conversationID string, principal authctx.Principal) string {
	parts := []string{}
	if ws := h.buildWorkspaceBlock(conversationID); ws != "" {
		parts = append(parts, ws)
	}
	if strings.TrimSpace(principal.UserID) == "" {
		return strings.Join(parts, "\n\n")
	}
	if h.config != nil && h.config.Project.Enabled && principal.HasPermission("project:read") {
		if bb := h.projectBlackboardBlock(conversationID); bb != "" {
			parts = append(parts, bb)
		}
	}
	if knowledge := h.projectKnowledgeBlock(conversationID, principal); knowledge != "" {
		parts = append(parts, knowledge)
	}
	return strings.Join(parts, "\n\n")
}

// projectKnowledgeBlock is a bounded planning index, not a replacement for
// the MCP detail tools. It contains enough data to choose the next step while
// keeping evidence, cookies and large response bodies out of the system prompt.
func (h *AgentHandler) projectKnowledgeBlock(conversationID string, principal authctx.Principal) string {
	if h == nil || h.db == nil || strings.TrimSpace(conversationID) == "" {
		return ""
	}
	projectID := h.conversationProjectID(conversationID)
	access := database.RBACListAccess{UserID: principal.UserID}
	var assets []*database.Asset
	var vulnerabilities []*database.Vulnerability

	if principal.HasPermission("asset:read") {
		access.Scope = principal.ScopeFor("asset:read")
		filter := database.AssetListFilter{}
		if projectID != "" {
			filter.ProjectID = projectID
		}
		items, _, err := h.db.ListAssets(100, 0, filter, access)
		if err == nil {
			if projectID != "" {
				assets = items
			} else {
				// Assets are project-scoped when possible. For an unbound
				// conversation, only retain assets whose latest scan belongs to
				// this conversation.
				for _, item := range items {
					if item != nil && strings.TrimSpace(item.LastScanConversationID) == strings.TrimSpace(conversationID) {
						assets = append(assets, item)
					}
				}
			}
		}
	}

	if principal.HasPermission("vulnerability:read") {
		vulnAccess := database.RBACListAccess{UserID: principal.UserID, Scope: principal.ScopeFor("vulnerability:read")}
		filter := database.VulnerabilityListFilter{ConversationID: conversationID}
		if projectID != "" {
			filter = database.VulnerabilityListFilter{ProjectID: projectID}
		}
		items, err := h.db.ListVulnerabilitiesForAccess(50, 0, filter, vulnAccess)
		if err == nil {
			vulnerabilities = items
		}
	}

	if len(assets) == 0 && len(vulnerabilities) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 可复用项目数据（用于规划下一步；详情按需调用 MCP 工具）\n")
	if projectID != "" {
		b.WriteString("项目范围：")
		b.WriteString(projectID)
		b.WriteByte('\n')
	}
	if len(assets) > 0 {
		b.WriteString("\n### 已有资产\n")
		for i, item := range assets {
			if i >= 20 {
				break
			}
			if item == nil {
				continue
			}
			target := firstNonEmptyProjectContext(item.Domain, item.IP, item.Host)
			if item.Port > 0 {
				target += ":" + formatProjectContextPort(item.Port)
			}
			fmtProjectContextLine(&b, "asset", item.ID, target, item.Protocol, item.RiskLevel, item.VulnerabilityCount)
		}
	}
	if len(vulnerabilities) > 0 {
		b.WriteString("\n### 已有漏洞\n")
		for i, item := range vulnerabilities {
			if i >= 20 {
				break
			}
			if item == nil {
				continue
			}
			fmt.Fprintf(&b, "- vulnerability_id=%s | %s | %s | %s | target=%s\n", item.ID, item.Severity, item.Status, truncateProjectContext(item.Title, 160), truncateProjectContext(item.Target, 240))
		}
	}
	return strings.TrimSpace(b.String())
}

func firstNonEmptyProjectContext(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "-"
}

func formatProjectContextPort(port int) string {
	return fmt.Sprintf("%d", port)
}

func truncateProjectContext(value string, max int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}

func fmtProjectContextLine(b *strings.Builder, kind, id, target, protocol, risk string, vulnerabilities int) {
	fmt.Fprintf(b, "- %s_id=%s | target=%s | protocol=%s | risk=%s | vulnerabilities=%d\n", kind, id, truncateProjectContext(target, 240), truncateProjectContext(protocol, 40), truncateProjectContext(risk, 40), vulnerabilities)
}

func (h *AgentHandler) buildWorkspaceBlock(conversationID string) string {
	if h == nil || h.config == nil {
		return ""
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ""
	}
	rel := project.ConversationWorkspaceRootDir(h.config.Agent.WorkspaceRootDir, conversationID)
	abs, err := project.EnsureWorkspace(rel)
	if err != nil {
		if h.logger != nil {
			h.logger.Warn("创建会话工作目录失败",
				zap.String("conversationId", conversationID),
				zap.String("path", rel),
				zap.Error(err))
		}
		return ""
	}
	return project.BuildWorkspaceBlock(abs)
}

// projectBlackboardBlock 根据对话 ID 构建项目事实索引块（用于注入 system prompt）。
func (h *AgentHandler) projectBlackboardBlock(conversationID string) string {
	if h == nil || h.db == nil || h.config == nil {
		return ""
	}
	if !h.config.Project.Enabled {
		return ""
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ""
	}
	projectID, err := h.db.GetConversationProjectID(conversationID)
	if err != nil || projectID == "" {
		return ""
	}
	block, err := project.BuildProjectBlackboardBlock(h.db, projectID, h.config.Project)
	if err != nil {
		h.logger.Warn("构建项目黑板索引失败", zap.String("conversationId", conversationID), zap.Error(err))
		return ""
	}
	return strings.TrimSpace(block)
}

// conversationProjectID 返回对话绑定的项目 ID；未绑定或查询失败时返回空字符串。
func (h *AgentHandler) conversationProjectID(conversationID string) string {
	if h == nil || h.db == nil {
		return ""
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ""
	}
	projectID, err := h.db.GetConversationProjectID(conversationID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(projectID)
}
