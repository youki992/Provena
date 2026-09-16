//go:build webconsole

package app

// The web console's route table, compiled in only with `-tags webconsole`.
// Without the tag nothing is registered and nothing is served, so a default
// build has no console to reach even by accident.

import (
	"net/http"
	"time"

	"github.com/chobits02/provena/internal/audit"
	"github.com/chobits02/provena/internal/handler"
	"github.com/chobits02/provena/internal/mcp"
	"github.com/chobits02/provena/internal/security"

	"github.com/gin-gonic/gin"
)

// setupRoutes 设置路由
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
	app *App, // 传递 App 实例以便动态获取 knowledgeHandler
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
	// API路由
	api := router.Group("/api")

	// 认证相关路由
	authRoutes := api.Group("/auth")
	loginRL := security.NewRateLimiter(10, 1*time.Minute)
	{
		authRoutes.POST("/login", security.RateLimitMiddleware(loginRL), authHandler.Login)
		authRoutes.POST("/logout", security.AuthMiddleware(authManager), authHandler.Logout)
		authRoutes.POST("/change-password", security.AuthMiddleware(authManager), security.RequirePermission("auth:self"), authHandler.ChangePassword)
		authRoutes.GET("/validate", security.AuthMiddleware(authManager), authHandler.Validate)
		authRoutes.POST("/robot-binding-code", security.AuthMiddleware(authManager), security.RequirePermission("auth:self"), robotHandler.CreateRobotBindingCode)
		authRoutes.GET("/robot-bindings", security.AuthMiddleware(authManager), security.RequirePermission("auth:self"), robotHandler.ListMyRobotBindings)
		authRoutes.DELETE("/robot-bindings/:id", security.AuthMiddleware(authManager), security.RequirePermission("auth:self"), robotHandler.DeleteMyRobotBinding)
	}

	// 机器人回调（无需登录，供企业微信/钉钉/飞书服务器调用）
	// 添加速率限制：每个 IP 每分钟最多 60 次请求，防止滥用
	robotRL := security.NewRateLimiter(60, 1*time.Minute)
	robotGroup := api.Group("/robot")
	robotGroup.Use(security.RateLimitMiddleware(robotRL))
	{
		robotGroup.GET("/wecom", robotHandler.HandleWecomGET)
		robotGroup.POST("/wecom", robotHandler.HandleWecomPOST)
		robotGroup.POST("/dingtalk", robotHandler.HandleDingtalkPOST)
		robotGroup.POST("/lark", robotHandler.HandleLarkPOST)
	}

	protected := api.Group("")
	protected.Use(security.AuthMiddleware(authManager))
	protected.Use(security.RBACMiddlewareWithDenyHook(app.db, func(c *gin.Context, reason, permission string) {
		if auditSvc != nil {
			auditSvc.Record(c, audit.Entry{
				Level: "warn", Category: "rbac", Action: "access_denied", Result: "failure",
				Message: "RBAC 拒绝访问", ResourceType: "route", ResourceID: c.FullPath(),
				Detail: map[string]interface{}{"reason": reason, "permission": permission, "method": c.Request.Method},
			})
		}
	}))
	{
		protected.GET("/rbac/me", rbacHandler.Me)
		protected.GET("/rbac/metadata", rbacHandler.Metadata)
		protected.GET("/rbac/users", rbacHandler.ListUsers)
		protected.POST("/rbac/users", rbacHandler.CreateUser)
		protected.PUT("/rbac/users/:id", rbacHandler.UpdateUser)
		protected.DELETE("/rbac/users/:id", rbacHandler.DeleteUser)
		protected.GET("/rbac/roles", rbacHandler.ListRoles)
		protected.POST("/rbac/roles", rbacHandler.CreateRole)
		protected.PUT("/rbac/roles/:id", rbacHandler.UpdateRole)
		protected.DELETE("/rbac/roles/:id", rbacHandler.DeleteRole)
		protected.GET("/rbac/resource-assignments", rbacHandler.ListResourceAssignments)
		protected.GET("/rbac/resources", rbacHandler.ListAssignableResources)
		protected.POST("/rbac/resource-assignments", rbacHandler.AssignResource)
		protected.DELETE("/rbac/resource-assignments/:id", rbacHandler.DeleteResourceAssignment)

		// 机器人测试（需登录）：POST /api/robot/test，body: {"platform":"dingtalk","user_id":"test","text":"帮助"}，用于验证机器人逻辑
		protected.POST("/robot/test", robotHandler.HandleRobotTest)

		// 微信 iLink 扫码绑定（需登录）
		protected.POST("/robot/wechat/qrcode", wechatRobotHandler.HandleWechatQRCode)
		protected.GET("/robot/wechat/qrcode/status", wechatRobotHandler.HandleWechatQRCodeStatus)
		protected.POST("/robot/wechat/qrcode/verify", wechatRobotHandler.HandleWechatVerifyCode)
		protected.GET("/robot/wechat/status", wechatRobotHandler.HandleWechatStatus)

		// Eino ADK 单代理（ChatModelAgent + Runner；不依赖 multi_agent.enabled）
		protected.POST("/eino-agent", agentHandler.EinoSingleAgentLoop)
		protected.POST("/eino-agent/stream", agentHandler.EinoSingleAgentLoopStream)
		protected.POST("/pi-agent/stream", agentHandler.PiSingleAgentLoopStream)
		protected.GET("/hitl/pending", agentHandler.ListHITLPending)
		protected.GET("/hitl/logs", agentHandler.ListHITLLogs)
		protected.DELETE("/hitl/logs", agentHandler.DeleteHITLLogs)
		protected.GET("/hitl/logs/:id", agentHandler.GetHITLLog)
		protected.POST("/hitl/decision", agentHandler.DecideHITLInterrupt)
		protected.POST("/hitl/dismiss", agentHandler.DismissHITLInterrupt)
		protected.GET("/hitl/config/:conversationId", agentHandler.GetHITLConversationConfig)
		protected.PUT("/hitl/config", agentHandler.UpsertHITLConversationConfig)
		protected.GET("/hitl/tool-whitelist", agentHandler.GetHITLGlobalToolWhitelist)
		protected.PUT("/hitl/tool-whitelist", agentHandler.SetHITLGlobalToolWhitelist)
		protected.POST("/hitl/tool-whitelist", agentHandler.MergeHITLGlobalToolWhitelist)
		protected.GET("/hitl/default-reviewer", agentHandler.GetHITLDefaultReviewer)
		protected.PUT("/hitl/default-reviewer", agentHandler.UpdateHITLDefaultReviewer)
		protected.GET("/hitl/audit-strategy", agentHandler.GetHITLAuditStrategy)
		protected.PUT("/hitl/audit-strategy", agentHandler.UpdateHITLAuditStrategy)
		// Agent Loop 取消与任务列表
		protected.POST("/agent-loop/cancel", agentHandler.CancelAgentLoop)
		protected.GET("/agent-loop/tasks", agentHandler.ListAgentTasks)
		protected.GET("/agent-loop/task-events", agentHandler.SubscribeAgentTaskEvents)
		protected.GET("/agent-loop/tasks/completed", agentHandler.ListCompletedTasks)

		// Eino DeepAgent 多代理（与单 Agent 并存，需 config.multi_agent.enabled）
		// 多代理路由常注册；是否可用由运行时 h.config.MultiAgent.Enabled 决定（应用配置后无需重启）
		protected.POST("/multi-agent", agentHandler.MultiAgentLoop)
		protected.POST("/multi-agent/stream", agentHandler.MultiAgentLoopStream)
		protected.GET("/multi-agent/markdown-agents", markdownAgentsHandler.ListMarkdownAgents)
		protected.GET("/multi-agent/markdown-agents/:filename", markdownAgentsHandler.GetMarkdownAgent)
		protected.POST("/multi-agent/markdown-agents", markdownAgentsHandler.CreateMarkdownAgent)
		protected.PUT("/multi-agent/markdown-agents/:filename", markdownAgentsHandler.UpdateMarkdownAgent)
		protected.DELETE("/multi-agent/markdown-agents/:filename", markdownAgentsHandler.DeleteMarkdownAgent)

		// 信息收集 - FOFA 查询（后端代理）
		protected.POST("/fofa/search", fofaHandler.Search)
		// 信息收集 - 自然语言解析为 FOFA 语法（需人工确认后再查询）
		protected.POST("/fofa/parse", fofaHandler.ParseNaturalLanguage)

		// 资产管理
		protected.GET("/assets", assetHandler.List)
		protected.GET("/assets/selection", assetHandler.Selection)
		protected.GET("/assets/stats", assetHandler.Stats)
		protected.POST("/assets/import", assetHandler.Import)
		protected.POST("/assets/scan-links", assetHandler.RecordScans)
		protected.PUT("/assets/bulk", assetHandler.BulkUpdate)
		protected.PUT("/assets/project-binding", assetHandler.UpdateProjectBinding)
		protected.POST("/assets/batch-delete", assetHandler.BatchDelete)
		protected.POST("/assets/merge", security.RequirePermission("asset:write"), assetHandler.Merge)
		protected.PUT("/assets/:id", assetHandler.Update)
		protected.DELETE("/assets/:id", assetHandler.Delete)

		// 批量任务管理
		protected.POST("/batch-tasks", agentHandler.CreateBatchQueue)
		protected.GET("/batch-tasks", agentHandler.ListBatchQueues)
		protected.GET("/batch-tasks/:queueId", agentHandler.GetBatchQueue)
		protected.POST("/batch-tasks/:queueId/start", agentHandler.StartBatchQueue)
		protected.POST("/batch-tasks/:queueId/rerun", agentHandler.RerunBatchQueue)
		protected.POST("/batch-tasks/:queueId/pause", agentHandler.PauseBatchQueue)
		protected.PUT("/batch-tasks/:queueId/metadata", agentHandler.UpdateBatchQueueMetadata)
		protected.PUT("/batch-tasks/:queueId/schedule", agentHandler.UpdateBatchQueueSchedule)
		protected.PUT("/batch-tasks/:queueId/schedule-enabled", agentHandler.SetBatchQueueScheduleEnabled)
		protected.DELETE("/batch-tasks/:queueId", agentHandler.DeleteBatchQueue)
		protected.PUT("/batch-tasks/:queueId/tasks/:taskId", agentHandler.UpdateBatchTask)
		protected.POST("/batch-tasks/:queueId/tasks/:taskId/run", agentHandler.RunSingleBatchTask)
		protected.POST("/batch-tasks/:queueId/tasks", agentHandler.AddBatchTask)
		protected.DELETE("/batch-tasks/:queueId/tasks/:taskId", agentHandler.DeleteBatchTask)

		// 对话历史
		protected.POST("/conversations", conversationHandler.CreateConversation)
		protected.GET("/conversations", conversationHandler.ListConversations)
		protected.GET("/conversations/:id", conversationHandler.GetConversation)
		protected.GET("/conversations/:id/fgs", agentHandler.GetConversationFGS)
		protected.GET("/messages/:id/process-details", conversationHandler.GetMessageProcessDetails)
		protected.GET("/process-details/:id", conversationHandler.GetProcessDetail)
		protected.PUT("/conversations/:id", conversationHandler.UpdateConversation)
		protected.PUT("/conversations/:id/project", conversationHandler.SetConversationProject)
		protected.DELETE("/conversations/:id", conversationHandler.DeleteConversation)
		protected.POST("/conversations/:id/delete-turn", conversationHandler.DeleteConversationTurn)
		protected.PUT("/conversations/:id/pinned", groupHandler.UpdateConversationPinned)

		// 对话分组
		protected.POST("/groups", groupHandler.CreateGroup)
		protected.GET("/groups", groupHandler.ListGroups)
		protected.GET("/groups/:id", groupHandler.GetGroup)
		protected.PUT("/groups/:id", groupHandler.UpdateGroup)
		protected.DELETE("/groups/:id", groupHandler.DeleteGroup)
		protected.PUT("/groups/:id/pinned", groupHandler.UpdateGroupPinned)
		protected.GET("/groups/:id/conversations", groupHandler.GetGroupConversations)
		protected.GET("/groups/mappings", groupHandler.GetAllMappings)
		protected.POST("/groups/conversations", groupHandler.AddConversationToGroup)
		protected.DELETE("/groups/:id/conversations/:conversationId", groupHandler.RemoveConversationFromGroup)
		protected.PUT("/groups/:id/conversations/:conversationId/pinned", groupHandler.UpdateConversationPinnedInGroup)

		// 监控
		protected.GET("/monitor", monitorHandler.Monitor)
		protected.GET("/monitor/execution/:id", monitorHandler.GetExecution)
		protected.POST("/monitor/execution/:id/cancel", monitorHandler.CancelExecution)
		protected.POST("/monitor/executions/names", monitorHandler.BatchGetToolNames)
		protected.DELETE("/monitor/execution/:id", monitorHandler.DeleteExecution)
		protected.DELETE("/monitor/executions", monitorHandler.DeleteExecutions)
		protected.GET("/monitor/stats", monitorHandler.GetStats)
		protected.GET("/monitor/calls-timeline", monitorHandler.GetCallsTimeline)
		protected.GET("/notifications/summary", notificationHandler.GetSummary)
		protected.POST("/notifications/read", notificationHandler.MarkRead)

		// 配置管理
		protected.GET("/config", configHandler.GetConfig)
		protected.GET("/config/tools", configHandler.GetTools)
		protected.GET("/config/tools/:name/schema", configHandler.GetToolSchema)
		protected.POST("/config/tools/:name/test", configHandler.TestBuiltinTool)
		protected.GET("/local-tools", configHandler.GetLocalTools)
		protected.POST("/local-tools", configHandler.CreateLocalTool)
		protected.PUT("/local-tools/:name", configHandler.UpdateLocalTool)
		protected.DELETE("/local-tools/:name", configHandler.DeleteLocalTool)
		protected.PUT("/config", configHandler.UpdateConfig)
		protected.POST("/config/apply", configHandler.ApplyConfig)
		protected.POST("/config/test-openai", configHandler.TestOpenAI)
		protected.POST("/config/test-vision", configHandler.TestVision)
		protected.POST("/config/list-models", configHandler.ListModels)

		// 本地抓包代理与请求包证据
		if app.packetCaptureHandler != nil {
			protected.GET("/packet-capture/config", app.packetCaptureHandler.Config)
			protected.PUT("/packet-capture/config", app.packetCaptureHandler.Configure)
			protected.GET("/packet-capture/status", app.packetCaptureHandler.Status)
			protected.POST("/packet-capture/start", app.packetCaptureHandler.Start)
			protected.POST("/packet-capture/stop", app.packetCaptureHandler.Stop)
			protected.GET("/packet-capture/ca", app.packetCaptureHandler.CA)
			protected.GET("/packet-capture/ca/download", app.packetCaptureHandler.DownloadCA)
			protected.POST("/packet-capture/ca/install-windows", app.packetCaptureHandler.InstallWindowsCA)
			protected.GET("/packet-groups", app.packetCaptureHandler.Groups)
			protected.GET("/packet-groups/:id/packets", app.packetCaptureHandler.GroupPackets)
			protected.GET("/captured-packets/:id", app.packetCaptureHandler.Packet)
		}

		// 系统设置 - 终端（执行命令，提高运维效率）
		protected.POST("/terminal/run", terminalHandler.RunCommand)
		protected.POST("/terminal/run/stream", terminalHandler.RunCommandStream)
		protected.GET("/terminal/ws", terminalHandler.RunCommandWS)

		// 平台审计日志
		protected.GET("/audit/meta", auditHandler.Meta)
		protected.GET("/audit/summary", auditHandler.Summary)
		protected.GET("/audit/logs", auditHandler.ListLogs)
		protected.GET("/audit/logs/export", auditHandler.ExportLogs)
		protected.GET("/audit/logs/:id", auditHandler.GetLog)

		// 外部MCP管理
		protected.GET("/external-mcp", externalMCPHandler.GetExternalMCPs)
		protected.GET("/external-mcp/stats", externalMCPHandler.GetExternalMCPStats)
		protected.GET("/external-mcp/:name", externalMCPHandler.GetExternalMCP)
		protected.PUT("/external-mcp/:name", externalMCPHandler.AddOrUpdateExternalMCP)
		protected.DELETE("/external-mcp/:name", externalMCPHandler.DeleteExternalMCP)
		protected.POST("/external-mcp/:name/start", externalMCPHandler.StartExternalMCP)
		protected.POST("/external-mcp/:name/stop", externalMCPHandler.StopExternalMCP)
		protected.POST("/external-mcp/:name/validate", externalMCPHandler.ValidateExternalMCP)
		protected.POST("/external-mcp/:name/test", externalMCPHandler.TestExternalMCPTool)

		// 攻击链可视化
		protected.GET("/attack-chain/:conversationId", attackChainHandler.GetAttackChain)
		protected.POST("/attack-chain/:conversationId/regenerate", attackChainHandler.RegenerateAttackChain)

		// 知识库管理（始终注册路由，通过 App 实例动态获取 handler）
		knowledgeRoutes := protected.Group("/knowledge")
		{
			knowledgeRoutes.GET("/categories", func(c *gin.Context) {
				if app.knowledgeHandler == nil {
					c.JSON(http.StatusOK, gin.H{
						"categories": []string{},
						"enabled":    false,
						"message":    "知识库功能未启用，请前往系统设置启用知识检索功能",
					})
					return
				}
				app.knowledgeHandler.GetCategories(c)
			})
			knowledgeRoutes.GET("/items", func(c *gin.Context) {
				if app.knowledgeHandler == nil {
					c.JSON(http.StatusOK, gin.H{
						"items":   []interface{}{},
						"enabled": false,
						"message": "知识库功能未启用，请前往系统设置启用知识检索功能",
					})
					return
				}
				app.knowledgeHandler.GetItems(c)
			})
			knowledgeRoutes.GET("/items/:id", func(c *gin.Context) {
				if app.knowledgeHandler == nil {
					c.JSON(http.StatusOK, gin.H{
						"enabled": false,
						"message": "知识库功能未启用，请前往系统设置启用知识检索功能",
					})
					return
				}
				app.knowledgeHandler.GetItem(c)
			})
			knowledgeRoutes.POST("/items", func(c *gin.Context) {
				if app.knowledgeHandler == nil {
					c.JSON(http.StatusOK, gin.H{
						"enabled": false,
						"error":   "知识库功能未启用，请前往系统设置启用知识检索功能",
					})
					return
				}
				app.knowledgeHandler.CreateItem(c)
			})
			knowledgeRoutes.PUT("/items/:id", func(c *gin.Context) {
				if app.knowledgeHandler == nil {
					c.JSON(http.StatusOK, gin.H{
						"enabled": false,
						"error":   "知识库功能未启用，请前往系统设置启用知识检索功能",
					})
					return
				}
				app.knowledgeHandler.UpdateItem(c)
			})
			knowledgeRoutes.DELETE("/items/:id", func(c *gin.Context) {
				if app.knowledgeHandler == nil {
					c.JSON(http.StatusOK, gin.H{
						"enabled": false,
						"error":   "知识库功能未启用，请前往系统设置启用知识检索功能",
					})
					return
				}
				app.knowledgeHandler.DeleteItem(c)
			})
			knowledgeRoutes.GET("/index-status", func(c *gin.Context) {
				if app.knowledgeHandler == nil {
					c.JSON(http.StatusOK, gin.H{
						"enabled":          false,
						"total_items":      0,
						"indexed_items":    0,
						"progress_percent": 0,
						"is_complete":      false,
						"message":          "知识库功能未启用，请前往系统设置启用知识检索功能",
					})
					return
				}
				app.knowledgeHandler.GetIndexStatus(c)
			})
			knowledgeRoutes.POST("/index", func(c *gin.Context) {
				if app.knowledgeHandler == nil {
					c.JSON(http.StatusOK, gin.H{
						"enabled": false,
						"error":   "知识库功能未启用，请前往系统设置启用知识检索功能",
					})
					return
				}
				app.knowledgeHandler.StartIndex(c)
			})
			knowledgeRoutes.POST("/scan", func(c *gin.Context) {
				if app.knowledgeHandler == nil {
					c.JSON(http.StatusOK, gin.H{
						"enabled": false,
						"error":   "知识库功能未启用，请前往系统设置启用知识检索功能",
					})
					return
				}
				app.knowledgeHandler.ScanKnowledgeBase(c)
			})
			knowledgeRoutes.GET("/retrieval-logs", func(c *gin.Context) {
				if app.knowledgeHandler == nil {
					c.JSON(http.StatusOK, gin.H{
						"logs":    []interface{}{},
						"enabled": false,
						"message": "知识库功能未启用，请前往系统设置启用知识检索功能",
					})
					return
				}
				app.knowledgeHandler.GetRetrievalLogs(c)
			})
			knowledgeRoutes.DELETE("/retrieval-logs/:id", func(c *gin.Context) {
				if app.knowledgeHandler == nil {
					c.JSON(http.StatusOK, gin.H{
						"enabled": false,
						"error":   "知识库功能未启用，请前往系统设置启用知识检索功能",
					})
					return
				}
				app.knowledgeHandler.DeleteRetrievalLog(c)
			})
			knowledgeRoutes.POST("/search", func(c *gin.Context) {
				if app.knowledgeHandler == nil {
					c.JSON(http.StatusOK, gin.H{
						"results": []interface{}{},
						"enabled": false,
						"message": "知识库功能未启用，请前往系统设置启用知识检索功能",
					})
					return
				}
				app.knowledgeHandler.Search(c)
			})
			knowledgeRoutes.GET("/stats", func(c *gin.Context) {
				if app.knowledgeHandler == nil {
					c.JSON(http.StatusOK, gin.H{
						"enabled":          false,
						"total_categories": 0,
						"total_items":      0,
						"message":          "知识库功能未启用，请前往系统设置启用知识检索功能",
					})
					return
				}
				app.knowledgeHandler.GetStats(c)
			})
		}

		// 长期经验库：自动登记只能进入 pending；验证/人工确认后才允许晋级。
		experienceRoutes := protected.Group("/experience")
		{
			experienceRoutes.GET("", app.experienceHandler.List)
			experienceRoutes.GET("/:id", app.experienceHandler.Get)
			experienceRoutes.POST("/:id/verify", app.experienceHandler.Verify)
			experienceRoutes.POST("/:id/promote", app.experienceHandler.Promote)
		}

		// 漏洞管理
		protected.GET("/vulnerabilities", vulnerabilityHandler.ListVulnerabilities)
		protected.GET("/vulnerabilities/export", vulnerabilityHandler.ExportVulnerabilities)
		protected.DELETE("/vulnerabilities/batch", vulnerabilityHandler.BatchDeleteVulnerabilities)
		protected.GET("/vulnerabilities/filter-options", vulnerabilityHandler.GetVulnerabilityFilterOptions)
		protected.GET("/vulnerabilities/stats", vulnerabilityHandler.GetVulnerabilityStats)
		protected.GET("/vulnerability-alerts/subscription", vulnerabilityHandler.GetMyAlertSubscription)
		protected.PUT("/vulnerability-alerts/subscription", vulnerabilityHandler.UpdateMyAlertSubscription)
		protected.GET("/vulnerabilities/:id", vulnerabilityHandler.GetVulnerability)
		protected.POST("/vulnerabilities", vulnerabilityHandler.CreateVulnerability)
		protected.PUT("/vulnerabilities/:id", vulnerabilityHandler.UpdateVulnerability)
		protected.DELETE("/vulnerabilities/:id", vulnerabilityHandler.DeleteVulnerability)

		// 项目管理与事实黑板
		protected.GET("/projects/dashboard-summary", projectHandler.GetDashboardSummary)
		protected.GET("/projects", projectHandler.ListProjects)
		protected.POST("/projects", projectHandler.CreateProject)
		protected.GET("/projects/:id/stats", projectHandler.GetProjectStats)
		protected.GET("/projects/:id/conversations", projectHandler.ListProjectConversations)
		protected.GET("/projects/:id", projectHandler.GetProject)
		protected.PUT("/projects/:id", projectHandler.UpdateProject)
		protected.DELETE("/projects/:id", projectHandler.DeleteProject)
		protected.GET("/projects/:id/fact-graph", projectHandler.GetFactGraph)
		protected.GET("/projects/:id/fact-edges", projectHandler.ListFactEdges)
		protected.POST("/projects/:id/fact-edges", projectHandler.CreateFactEdge)
		protected.DELETE("/projects/:id/fact-edges/:edgeId", projectHandler.DeleteFactEdge)
		protected.POST("/projects/:id/promote-attack-chain/:conversationId", projectHandler.PromoteAttackChain)
		protected.GET("/projects/:id/facts", projectHandler.ListFacts)
		protected.POST("/projects/:id/facts", projectHandler.CreateFact)
		protected.PUT("/projects/:id/facts/:factId", projectHandler.UpdateFact)
		protected.DELETE("/projects/:id/facts/:factId", projectHandler.DeleteFact)
		protected.POST("/projects/:id/facts/deprecate", projectHandler.DeprecateFact)
		protected.POST("/projects/:id/facts/restore", projectHandler.RestoreFact)

		// WebShell 管理（代理执行 + 连接配置存 SQLite）
		protected.GET("/webshell/connections", webshellHandler.ListConnections)
		protected.POST("/webshell/connections", webshellHandler.CreateConnection)
		protected.GET("/webshell/connections/:id/ai-history", webshellHandler.GetAIHistory)
		protected.GET("/webshell/connections/:id/ai-conversations", webshellHandler.ListAIConversations)
		protected.GET("/webshell/connections/:id/state", webshellHandler.GetConnectionState)
		protected.PUT("/webshell/connections/:id", webshellHandler.UpdateConnection)
		protected.PUT("/webshell/connections/:id/state", webshellHandler.SaveConnectionState)
		protected.DELETE("/webshell/connections/:id", webshellHandler.DeleteConnection)
		protected.POST("/webshell/exec", webshellHandler.Exec)
		protected.POST("/webshell/file", webshellHandler.FileOp)

		// C2 管理（未启用时返回 503，避免 Handler 空指针）
		c2Routes := protected.Group("/c2")
		c2Routes.Use(func(c *gin.Context) {
			if app.c2Manager == nil {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
					"error":   "c2_disabled",
					"message": "C2 功能已在系统设置中关闭",
					"enabled": false,
				})
				return
			}
			c.Next()
		})
		c2Routes.GET("/listeners", c2Handler.ListListeners)
		c2Routes.POST("/listeners", c2Handler.CreateListener)
		c2Routes.GET("/listeners/:id", c2Handler.GetListener)
		c2Routes.PUT("/listeners/:id", c2Handler.UpdateListener)
		c2Routes.DELETE("/listeners/:id", c2Handler.DeleteListener)
		c2Routes.POST("/listeners/:id/start", c2Handler.StartListener)
		c2Routes.POST("/listeners/:id/stop", c2Handler.StopListener)
		c2Routes.GET("/sessions", c2Handler.ListSessions)
		c2Routes.DELETE("/sessions", c2Handler.DeleteSessions)
		c2Routes.GET("/sessions/:id", c2Handler.GetSession)
		c2Routes.DELETE("/sessions/:id", c2Handler.DeleteSession)
		c2Routes.PUT("/sessions/:id/sleep", c2Handler.SetSessionSleep)
		c2Routes.PUT("/sessions/:id/note", c2Handler.SetSessionNote)
		c2Routes.GET("/tasks", c2Handler.ListTasks)
		c2Routes.DELETE("/tasks", c2Handler.DeleteTasks)
		c2Routes.GET("/tasks/:id", c2Handler.GetTask)
		c2Routes.POST("/tasks", c2Handler.CreateTask)
		c2Routes.POST("/tasks/:id/cancel", c2Handler.CancelTask)
		c2Routes.GET("/tasks/:id/wait", c2Handler.WaitTask)
		c2Routes.POST("/sessions/:id/tasks", c2Handler.CreateTask)
		c2Routes.POST("/payloads/oneliner", c2Handler.PayloadOneliner)
		c2Routes.POST("/payloads/build", c2Handler.PayloadBuild)
		c2Routes.GET("/payloads/:id/download", c2Handler.PayloadDownload)
		c2Routes.GET("/events", c2Handler.ListEvents)
		c2Routes.DELETE("/events", c2Handler.DeleteEvents)
		c2Routes.GET("/events/stream", c2Handler.EventStream)
		c2Routes.POST("/files/upload", c2Handler.UploadFileForImplant)
		c2Routes.GET("/files", c2Handler.ListFiles)
		c2Routes.GET("/tasks/:id/result-file", c2Handler.DownloadResultFile)
		c2Routes.GET("/profiles", c2Handler.ListProfiles)
		c2Routes.GET("/profiles/:id", c2Handler.GetProfile)
		c2Routes.POST("/profiles", c2Handler.CreateProfile)
		c2Routes.PUT("/profiles/:id", c2Handler.UpdateProfile)
		c2Routes.DELETE("/profiles/:id", c2Handler.DeleteProfile)

		// 对话附件（chat_uploads）管理
		protected.GET("/chat-uploads", chatUploadsHandler.List)
		protected.GET("/chat-uploads/export", chatUploadsHandler.Export)
		protected.GET("/chat-uploads/download", chatUploadsHandler.Download)
		protected.GET("/chat-uploads/path", chatUploadsHandler.ResolvePath)
		protected.GET("/chat-uploads/content", chatUploadsHandler.GetContent)
		protected.POST("/chat-uploads", chatUploadsHandler.Upload)
		protected.POST("/chat-uploads/mkdir", chatUploadsHandler.Mkdir)
		protected.DELETE("/chat-uploads", chatUploadsHandler.Delete)
		protected.PUT("/chat-uploads/rename", chatUploadsHandler.Rename)
		protected.PUT("/chat-uploads/content", chatUploadsHandler.PutContent)

		// 角色管理
		protected.GET("/roles", roleHandler.GetRoles)
		protected.GET("/roles/:name", roleHandler.GetRole)
		protected.POST("/roles", roleHandler.CreateRole)
		protected.PUT("/roles/:name", roleHandler.UpdateRole)
		protected.DELETE("/roles/:name", roleHandler.DeleteRole)

		// 工作流定义（图结构固定，业务字段保存在 graph_json 中）
		protected.GET("/workflows/runs/pending", workflowHandler.ListPendingRuns)
		protected.GET("/workflows/runs/:runId/replay", workflowHandler.ReplayRun)
		protected.GET("/workflows/runs/:runId", workflowHandler.GetRun)
		protected.POST("/workflows/runs/:runId/resume", workflowHandler.ResumeRun)
		protected.POST("/workflows/validate", workflowHandler.Validate)
		protected.POST("/workflows/dry-run", workflowHandler.DryRun)
		protected.POST("/workflows/generate-draft", workflowHandler.GenerateDraft)
		protected.GET("/workflows/:id/package", workflowHandler.ExportPackage)
		protected.POST("/workflow-package-inspections", workflowHandler.CreatePackageInspection)
		protected.GET("/workflow-package-inspections/:inspectionId", workflowHandler.GetPackageInspection)
		protected.POST("/workflow-package-imports", workflowHandler.ApplyPackageImport)
		protected.GET("/workflow-package-imports/:importId", workflowHandler.GetPackageImport)
		protected.GET("/workflows", workflowHandler.List)
		protected.GET("/workflows/:id", workflowHandler.Get)
		protected.POST("/workflows", workflowHandler.Create)
		protected.PUT("/workflows/:id", workflowHandler.Update)
		protected.DELETE("/workflows/:id", workflowHandler.Delete)

		// Skills管理（具体路径需注册在 /skills/:name 之前）
		protected.GET("/skills", skillsHandler.GetSkills)
		protected.GET("/skills/stats", skillsHandler.GetSkillStats)
		protected.DELETE("/skills/stats", skillsHandler.ClearSkillStats)
		protected.GET("/skills/:name/files", skillsHandler.ListSkillPackageFiles)
		protected.GET("/skills/:name/file", skillsHandler.GetSkillPackageFile)
		protected.PUT("/skills/:name/file", skillsHandler.PutSkillPackageFile)
		protected.GET("/skills/:name/bound-roles", skillsHandler.GetSkillBoundRoles)
		protected.POST("/skills", skillsHandler.CreateSkill)
		protected.PUT("/skills/:name", skillsHandler.UpdateSkill)
		protected.DELETE("/skills/:name", skillsHandler.DeleteSkill)
		protected.DELETE("/skills/:name/stats", skillsHandler.ClearSkillStatsByName)
		protected.GET("/skills/:name", skillsHandler.GetSkill)

		// MCP端点
		protected.POST("/mcp", func(c *gin.Context) {
			mcpServer.HandleHTTP(c.Writer, c.Request)
		})

		// OpenAPI结果聚合端点（可选，用于获取对话的完整结果）
		protected.GET("/conversations/:id/results", openAPIHandler.GetConversationResults)
	}

	// OpenAPI规范（需要认证，避免暴露API结构信息）
	protected.GET("/openapi/spec", openAPIHandler.GetOpenAPISpec)

	// API文档页面（公开访问，但需要登录后才能使用API）
	router.GET("/api-docs", func(c *gin.Context) {
		c.HTML(http.StatusOK, "api-docs.html", nil)
	})

	// 静态文件
	router.Static("/static", "./web/static")
	router.LoadHTMLGlob("web/templates/*")

	// 前端页面
	router.GET("/", func(c *gin.Context) {
		version := app.config.Version
		if version == "" {
			version = "v1.0.0"
		}
		c.HTML(http.StatusOK, "index.html", gin.H{"Version": version})
	})
}
