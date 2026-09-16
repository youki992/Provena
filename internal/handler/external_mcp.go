package handler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chobits02/provena/internal/audit"
	"github.com/chobits02/provena/internal/config"
	"github.com/chobits02/provena/internal/mcp"
	"github.com/chobits02/provena/internal/security"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// ExternalMCPHandler 外部MCP处理器
type ExternalMCPHandler struct {
	manager    *mcp.ExternalMCPManager
	config     *config.Config
	configPath string
	logger     *zap.Logger
	audit      *audit.Service
	mu         sync.RWMutex
}

// SetAudit wires platform audit logging.
func (h *ExternalMCPHandler) SetAudit(s *audit.Service) {
	h.audit = s
}

// NewExternalMCPHandler 创建外部MCP处理器
func NewExternalMCPHandler(manager *mcp.ExternalMCPManager, cfg *config.Config, configPath string, logger *zap.Logger) *ExternalMCPHandler {
	return &ExternalMCPHandler{
		manager:    manager,
		config:     cfg,
		configPath: configPath,
		logger:     logger,
	}
}

// GetExternalMCPs 获取所有外部MCP配置
func (h *ExternalMCPHandler) GetExternalMCPs(c *gin.Context) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	configs := h.manager.GetConfigs()

	// 获取所有外部MCP的工具数量
	toolCounts := h.manager.GetToolCounts()

	// 转换为响应格式
	result := make(map[string]ExternalMCPResponse)
	for name, cfg := range configs {
		client, exists := h.manager.GetClient(name)
		status := "disconnected"
		configError := ""
		if err := h.validateConfig(cfg); err != nil {
			status = "error"
			configError = "配置错误: " + err.Error()
		} else if exists {
			status = client.GetStatus()
		} else if h.isEnabled(cfg) {
			status = "disconnected"
		} else {
			status = "disabled"
		}

		toolCount := toolCounts[name]
		errorMsg := externalMCPStatusError(h.manager, name, status)
		if configError != "" {
			errorMsg = configError
		}

		result[name] = ExternalMCPResponse{
			Config:    externalMCPConfigForResponse(c, cfg),
			Status:    status,
			ToolCount: toolCount,
			Error:     errorMsg,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"servers": result,
		"stats":   h.manager.GetStats(),
	})
}

// GetExternalMCP 获取单个外部MCP配置
func (h *ExternalMCPHandler) GetExternalMCP(c *gin.Context) {
	name := c.Param("name")

	h.mu.RLock()
	defer h.mu.RUnlock()

	configs := h.manager.GetConfigs()
	cfg, exists := configs[name]
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "外部MCP配置不存在"})
		return
	}

	client, clientExists := h.manager.GetClient(name)
	status := "disconnected"
	configError := ""
	if err := h.validateConfig(cfg); err != nil {
		status = "error"
		configError = "配置错误: " + err.Error()
	} else if clientExists {
		status = client.GetStatus()
	} else if h.isEnabled(cfg) {
		status = "disconnected"
	} else {
		status = "disabled"
	}

	// 获取工具数量
	toolCount := 0
	if clientExists && client.IsConnected() {
		if count, err := h.manager.GetToolCount(name); err == nil {
			toolCount = count
		}
	}

	errorMsg := externalMCPStatusError(h.manager, name, status)
	if configError != "" {
		errorMsg = configError
	}
	c.JSON(http.StatusOK, ExternalMCPResponse{
		Config:    externalMCPConfigForResponse(c, cfg),
		Status:    status,
		ToolCount: toolCount,
		Error:     errorMsg,
	})
}

func externalMCPConfigForResponse(c *gin.Context, cfg config.ExternalMCPServerConfig) config.ExternalMCPServerConfig {
	if security.SessionHasPermission(c, "mcp:write") {
		return cfg
	}
	copyCfg := cfg
	if len(cfg.Env) > 0 {
		copyCfg.Env = make(map[string]string, len(cfg.Env))
		for key := range cfg.Env {
			copyCfg.Env[key] = "***"
		}
	}
	if len(cfg.Headers) > 0 {
		copyCfg.Headers = make(map[string]string, len(cfg.Headers))
		for key := range cfg.Headers {
			copyCfg.Headers[key] = "***"
		}
	}
	return copyCfg
}

// externalMCPStatusError 在 error/disconnected 状态下返回最近错误（含断连原因）。
func externalMCPStatusError(manager *mcp.ExternalMCPManager, name, status string) string {
	if status != "error" && status != "disconnected" {
		return ""
	}
	return manager.GetError(name)
}

// AddOrUpdateExternalMCP 添加或更新外部MCP配置
func (h *ExternalMCPHandler) AddOrUpdateExternalMCP(c *gin.Context) {
	var req AddOrUpdateExternalMCPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数: " + err.Error()})
		return
	}

	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "名称不能为空"})
		return
	}

	// 先把官方字段映射到平台内部字段，并展开环境变量。管理器必须拿到
	// 与 h.config 相同的最终配置，否则前端保存后可能显示为“已保存”，
	// 但管理器仍认为服务是禁用状态，也不会自动建立连接。
	cfg := req.Config
	config.NormalizeExternalMCPServerCommand(&cfg)
	if cfg.Disabled {
		cfg.ExternalMCPEnable = false
	} else if !cfg.ExternalMCPEnable {
		// 外部 MCP 配置默认启用；停止操作会显式写入 false。
		cfg.ExternalMCPEnable = true
	}
	config.ExpandConfigEnv(&cfg)
	if err := h.validateConfig(cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// 添加或更新配置
	if err := h.manager.AddOrUpdateConfig(name, cfg); err != nil {
		h.logger.Error("添加或更新外部MCP配置失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "添加或更新配置失败: " + err.Error()})
		return
	}

	// 更新内存中的配置
	if h.config.ExternalMCP.Servers == nil {
		h.config.ExternalMCP.Servers = make(map[string]config.ExternalMCPServerConfig)
	}

	h.config.ExternalMCP.Servers[name] = cfg

	// 保存到配置文件
	if err := h.saveConfig(); err != nil {
		h.logger.Error("保存配置失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存配置失败: " + err.Error()})
		return
	}

	h.logger.Info("外部MCP配置已更新", zap.String("name", name))
	if h.audit != nil {
		h.audit.Record(c, audit.Entry{
			Category:     "external_mcp",
			Action:       "upsert",
			Result:       "success",
			ResourceType: "external_mcp",
			ResourceID:   name,
			Message:      "更新外部 MCP 配置",
		})
	}
	c.JSON(http.StatusOK, gin.H{"message": "配置已更新"})
}

// DeleteExternalMCP 删除外部MCP配置
func (h *ExternalMCPHandler) DeleteExternalMCP(c *gin.Context) {
	name := c.Param("name")

	h.mu.Lock()
	defer h.mu.Unlock()

	// 移除配置
	if err := h.manager.RemoveConfig(name); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "配置不存在"})
		return
	}

	// 从内存配置中删除
	if h.config.ExternalMCP.Servers != nil {
		delete(h.config.ExternalMCP.Servers, name)
	}

	// 保存到配置文件
	if err := h.saveConfig(); err != nil {
		h.logger.Error("保存配置失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存配置失败: " + err.Error()})
		return
	}

	h.logger.Info("外部MCP配置已删除", zap.String("name", name))
	if h.audit != nil {
		h.audit.Record(c, audit.Entry{
			Category:     "external_mcp",
			Action:       "delete",
			Result:       "success",
			ResourceType: "external_mcp",
			ResourceID:   name,
			Message:      "删除外部 MCP 配置",
		})
	}
	c.JSON(http.StatusOK, gin.H{"message": "配置已删除"})
}

// StartExternalMCP 启动外部MCP
func (h *ExternalMCPHandler) StartExternalMCP(c *gin.Context) {
	name := c.Param("name")

	h.mu.Lock()
	defer h.mu.Unlock()

	// 更新配置为启用
	if h.config.ExternalMCP.Servers == nil {
		h.config.ExternalMCP.Servers = make(map[string]config.ExternalMCPServerConfig)
	}
	cfg := h.config.ExternalMCP.Servers[name]
	cfg.ExternalMCPEnable = true
	h.config.ExternalMCP.Servers[name] = cfg

	// 保存到配置文件
	if err := h.saveConfig(); err != nil {
		h.logger.Error("保存配置失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存配置失败: " + err.Error()})
		return
	}

	// 启动客户端（立即创建客户端并设置状态为connecting，实际连接在后台进行）
	h.logger.Info("开始启动外部MCP", zap.String("name", name))
	if err := h.manager.StartClient(name); err != nil {
		h.logger.Error("启动外部MCP失败", zap.String("name", name), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  err.Error(),
			"status": "error",
		})
		return
	}

	// 获取客户端状态（应该是connecting）
	client, exists := h.manager.GetClient(name)
	status := "connecting"
	if exists {
		status = client.GetStatus()
	}

	// 立即返回，不等待连接完成
	// 客户端会在后台异步连接，用户可以通过状态查询接口查看连接状态
	c.JSON(http.StatusOK, gin.H{
		"message": "外部MCP启动请求已提交，正在后台连接中",
		"status":  status,
	})
}

// StopExternalMCP 停止外部MCP
func (h *ExternalMCPHandler) StopExternalMCP(c *gin.Context) {
	name := c.Param("name")

	h.mu.Lock()
	defer h.mu.Unlock()

	// 停止客户端
	if err := h.manager.StopClient(name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 更新配置
	if h.config.ExternalMCP.Servers == nil {
		h.config.ExternalMCP.Servers = make(map[string]config.ExternalMCPServerConfig)
	}
	cfg := h.config.ExternalMCP.Servers[name]
	cfg.ExternalMCPEnable = false
	h.config.ExternalMCP.Servers[name] = cfg

	// 保存到配置文件
	if err := h.saveConfig(); err != nil {
		h.logger.Error("保存配置失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存配置失败: " + err.Error()})
		return
	}

	h.logger.Info("外部MCP已停止", zap.String("name", name))
	c.JSON(http.StatusOK, gin.H{"message": "外部MCP已停止"})
}

// GetExternalMCPStats 获取统计信息
func (h *ExternalMCPHandler) GetExternalMCPStats(c *gin.Context) {
	stats := h.manager.GetStats()
	c.JSON(http.StatusOK, stats)
}

// ValidateExternalMCP performs a real MCP handshake followed by tools/list.
// It is separate from Start so the UI can give an immediate, actionable
// diagnosis for a bad command, endpoint, credential, or non-MCP executable.
func (h *ExternalMCPHandler) ValidateExternalMCP(c *gin.Context) {
	name := c.Param("name")
	h.mu.RLock()
	cfg, exists := h.manager.GetConfigs()[name]
	h.mu.RUnlock()
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "外部MCP配置不存在"})
		return
	}
	if err := h.validateConfig(cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "status": "invalid", "error": err.Error()})
		return
	}

	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	started := time.Now()
	tools, err := h.manager.ValidateClient(ctx, name)
	if err != nil {
		status := "error"
		if client, ok := h.manager.GetClient(name); ok && client != nil {
			status = client.GetStatus()
		}
		c.JSON(http.StatusBadGateway, gin.H{
			"ok":          false,
			"name":        name,
			"status":      status,
			"tool_count":  0,
			"duration_ms": time.Since(started).Milliseconds(),
			"error":       err.Error(),
		})
		return
	}

	toolItems := make([]gin.H, 0, len(tools))
	for _, tool := range tools {
		toolItems = append(toolItems, gin.H{
			"name":             tool.Name,
			"description":      tool.Description,
			"shortDescription": tool.ShortDescription,
			"input_schema":     tool.InputSchema,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"name":        name,
		"status":      "connected",
		"tool_count":  len(toolItems),
		"tools":       toolItems,
		"duration_ms": time.Since(started).Milliseconds(),
	})
}

type externalMCPToolTestRequest struct {
	Tool      string                 `json:"tool"`
	Arguments map[string]interface{} `json:"arguments"`
}

// TestExternalMCPTool invokes one discovered tool with caller-supplied JSON.
// The normal external-MCP authorization and execution audit path is reused.
func (h *ExternalMCPHandler) TestExternalMCPTool(c *gin.Context) {
	name := strings.TrimSpace(c.Param("name"))
	var req externalMCPToolTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "工具测试参数必须是有效 JSON: " + err.Error()})
		return
	}
	toolName := strings.TrimSpace(req.Tool)
	if toolName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "tool 不能为空"})
		return
	}
	if strings.Contains(toolName, "::") {
		parts := strings.SplitN(toolName, "::", 2)
		if parts[0] != name {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "工具不属于当前 MCP 服务"})
			return
		}
		toolName = strings.TrimSpace(parts[1])
	}
	if toolName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "工具名称不能为空"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	tools, err := h.manager.ValidateClient(ctx, name)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"ok":    false,
			"tool":  toolName,
			"error": "调用前验证 MCP 工具列表失败: " + err.Error(),
		})
		return
	}
	found := false
	for _, tool := range tools {
		if tool.Name == toolName {
			found = true
			break
		}
	}
	if !found {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"tool":  toolName,
			"error": "工具不在该 MCP 当前 tools/list 中，请重新验证连接并选择已发现的工具",
		})
		return
	}
	if req.Arguments == nil {
		req.Arguments = map[string]interface{}{}
	}
	result, executionID, err := h.manager.CallTool(ctx, name+"::"+toolName, req.Arguments)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"ok":           false,
			"tool":         toolName,
			"execution_id": executionID,
			"error":        err.Error(),
		})
		return
	}
	if result == nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"ok":           false,
			"tool":         toolName,
			"execution_id": executionID,
			"error":        "工具未返回结果",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":           !result.IsError,
		"tool":         toolName,
		"execution_id": executionID,
		"is_error":     result.IsError,
		"result":       result,
	})
}

// validateConfig 验证配置（同时支持官方 type 字段和旧版 transport 字段）
func (h *ExternalMCPHandler) validateConfig(cfg config.ExternalMCPServerConfig) error {
	transport := cfg.GetTransportType()
	if transport == "" {
		return fmt.Errorf("需要指定 command（stdio模式）或 url + type（http/sse模式）")
	}
	if cfg.Timeout < 0 || cfg.MaxRetries < 0 || cfg.TerminateDuration < 0 || cfg.KeepAlive < 0 {
		return fmt.Errorf("timeout、max_retries、terminate_duration、keep_alive 不能为负数")
	}

	switch transport {
	case "http":
		if cfg.URL == "" {
			return fmt.Errorf("HTTP模式需要 url")
		}
		if !hasHTTPURLScheme(cfg.URL) {
			return fmt.Errorf("HTTP模式的 url 必须以 http:// 或 https:// 开头")
		}
	case "stdio":
		if cfg.Command == "" {
			return fmt.Errorf("stdio模式需要 command")
		}
	case "sse":
		if cfg.URL == "" {
			return fmt.Errorf("SSE模式需要 url")
		}
		if !hasHTTPURLScheme(cfg.URL) {
			return fmt.Errorf("SSE模式的 url 必须以 http:// 或 https:// 开头")
		}
	default:
		return fmt.Errorf("不支持的传输模式: %s，支持的模式: http, stdio, sse", transport)
	}

	return nil
}

func hasHTTPURLScheme(rawURL string) bool {
	value := strings.ToLower(strings.TrimSpace(rawURL))
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

// isEnabled 检查是否启用
func (h *ExternalMCPHandler) isEnabled(cfg config.ExternalMCPServerConfig) bool {
	return cfg.ExternalMCPEnable
}

// saveConfig 保存配置到文件
func (h *ExternalMCPHandler) saveConfig() error {
	data, err := os.ReadFile(h.configPath)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	if err := os.WriteFile(h.configPath+".backup", data, 0644); err != nil {
		h.logger.Warn("创建配置备份失败", zap.Error(err))
	}

	root, err := loadYAMLDocument(h.configPath)
	if err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}

	updateExternalMCPConfig(root, h.config.ExternalMCP)

	if err := writeYAMLDocument(h.configPath, root); err != nil {
		return fmt.Errorf("保存配置文件失败: %w", err)
	}

	h.logger.Info("配置已保存", zap.String("path", h.configPath))
	return nil
}

// updateExternalMCPConfig 更新外部MCP配置
func updateExternalMCPConfig(doc *yaml.Node, cfg config.ExternalMCPConfig) {
	root := doc.Content[0]
	externalMCPNode := ensureMap(root, "external_mcp")
	serversNode := ensureMap(externalMCPNode, "servers")

	// 清空现有服务器配置
	serversNode.Content = nil

	// 添加新的服务器配置
	for name, serverCfg := range cfg.Servers {
		nameNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}
		serverNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		serversNode.Content = append(serversNode.Content, nameNode, serverNode)

		// type（官方 MCP 传输类型）
		effectiveType := serverCfg.GetTransportType()
		if effectiveType != "" && effectiveType != "stdio" {
			// stdio 可省略（有 command 时自动推断）
			setStringInMap(serverNode, "type", effectiveType)
		}
		if serverCfg.Command != "" {
			setStringInMap(serverNode, "command", serverCfg.Command)
		}
		if len(serverCfg.Args) > 0 {
			setStringArrayInMap(serverNode, "args", serverCfg.Args)
		}
		if serverCfg.Env != nil && len(serverCfg.Env) > 0 {
			envNode := ensureMap(serverNode, "env")
			for envKey, envValue := range serverCfg.Env {
				setStringInMap(envNode, envKey, envValue)
			}
		}
		if serverCfg.URL != "" {
			setStringInMap(serverNode, "url", serverCfg.URL)
		}
		if serverCfg.Headers != nil && len(serverCfg.Headers) > 0 {
			headersNode := ensureMap(serverNode, "headers")
			for k, v := range serverCfg.Headers {
				setStringInMap(headersNode, k, v)
			}
		}
		if serverCfg.Description != "" {
			setStringInMap(serverNode, "description", serverCfg.Description)
		}
		if serverCfg.Timeout > 0 {
			setIntInMap(serverNode, "timeout", serverCfg.Timeout)
		}
		// 官方标准字段
		if serverCfg.Disabled {
			setBoolInMap(serverNode, "disabled", true)
		}
		if len(serverCfg.AutoApprove) > 0 {
			setStringArrayInMap(serverNode, "autoApprove", serverCfg.AutoApprove)
		}

		// SDK 高级配置
		if serverCfg.MaxRetries > 0 {
			setIntInMap(serverNode, "max_retries", serverCfg.MaxRetries)
		}
		if serverCfg.TerminateDuration > 0 {
			setIntInMap(serverNode, "terminate_duration", serverCfg.TerminateDuration)
		}
		if serverCfg.KeepAlive > 0 {
			setIntInMap(serverNode, "keep_alive", serverCfg.KeepAlive)
		}

		setBoolInMap(serverNode, "external_mcp_enable", serverCfg.ExternalMCPEnable)
		if serverCfg.ToolEnabled != nil && len(serverCfg.ToolEnabled) > 0 {
			toolEnabledNode := ensureMap(serverNode, "tool_enabled")
			for toolName, enabled := range serverCfg.ToolEnabled {
				setBoolInMap(toolEnabledNode, toolName, enabled)
			}
		}
	}
}

// setStringArrayInMap 设置字符串数组
func setStringArrayInMap(mapNode *yaml.Node, key string, values []string) {
	_, valueNode := ensureKeyValue(mapNode, key)
	valueNode.Kind = yaml.SequenceNode
	valueNode.Tag = "!!seq"
	valueNode.Content = nil
	for _, v := range values {
		itemNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
		valueNode.Content = append(valueNode.Content, itemNode)
	}
}

// AddOrUpdateExternalMCPRequest 添加或更新外部MCP请求
type AddOrUpdateExternalMCPRequest struct {
	Config config.ExternalMCPServerConfig `json:"config"`
}

// ExternalMCPResponse 外部MCP响应
type ExternalMCPResponse struct {
	Config    config.ExternalMCPServerConfig `json:"config"`
	Status    string                         `json:"status"`          // "connected", "disconnected", "disabled", "error", "connecting"
	ToolCount int                            `json:"tool_count"`      // 工具数量
	Error     string                         `json:"error,omitempty"` // 错误信息（仅在status为error时存在）
}
