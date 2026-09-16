//go:build !webconsole

package app

// The default build has no web console. The parameter list mirrors the real
// setupRoutes so the caller in New compiles unchanged; nothing is registered and
// nothing is served.

import (
	"github.com/chobits02/provena/internal/audit"
	"github.com/chobits02/provena/internal/handler"
	"github.com/chobits02/provena/internal/mcp"
	"github.com/chobits02/provena/internal/security"

	"github.com/gin-gonic/gin"
)

func setupRoutes(
	router *gin.Engine,
	authHandler *handler.AuthHandler,
	agentHandler *handler.AgentHandler,
	monitorHandler *handler.MonitorHandler,
	notificationHandler *handler.NotificationHandler,
	conversationHandler *handler.ConversationHandler,
	robotHandler *handler.RobotHandler,
	wechatRobotHandler *handler.WechatRobotHandler,
	groupHandler *handler.GroupHandler,
	configHandler *handler.ConfigHandler,
	externalMCPHandler *handler.ExternalMCPHandler,
	attackChainHandler *handler.AttackChainHandler,
	app *App,
	vulnerabilityHandler *handler.VulnerabilityHandler,
	assetHandler *handler.AssetHandler,
	projectHandler *handler.ProjectHandler,
	workflowHandler *handler.WorkflowHandler,
	webshellHandler *handler.WebShellHandler,
	chatUploadsHandler *handler.ChatUploadsHandler,
	roleHandler *handler.RoleHandler,
	skillsHandler *handler.SkillsHandler,
	markdownAgentsHandler *handler.MarkdownAgentsHandler,
	fofaHandler *handler.FofaHandler,
	terminalHandler *handler.TerminalHandler,
	c2Handler *handler.C2Handler,
	auditHandler *handler.AuditHandler,
	auditSvc *audit.Service,
	rbacHandler *handler.RBACHandler,
	mcpServer *mcp.Server,
	authManager *security.AuthManager,
	openAPIHandler *handler.OpenAPIHandler,
) {
}
