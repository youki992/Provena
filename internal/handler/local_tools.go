package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/chobits02/provena/internal/config"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

type localToolRequest struct {
	Name        string   `json:"name"`
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	Description string   `json:"description"`
	Enabled     *bool    `json:"enabled"`
}

func (h *ConfigHandler) GetLocalTools(c *gin.Context) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	tools := make([]localToolResponse, 0)
	for _, tool := range h.config.Security.Tools {
		if tool.Local {
			tools = append(tools, localToolResponse{
				Name: tool.Name, Command: tool.Command, Args: tool.Args,
				Description: tool.Description, Enabled: tool.Enabled,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"tools": tools})
}

type localToolResponse struct {
	Name        string   `json:"name"`
	Command     string   `json:"command"`
	Args        []string `json:"args,omitempty"`
	Description string   `json:"description"`
	Enabled     bool     `json:"enabled"`
}

func (h *ConfigHandler) CreateLocalTool(c *gin.Context) {
	h.saveLocalTool(c, "")
}

func (h *ConfigHandler) UpdateLocalTool(c *gin.Context) {
	h.saveLocalTool(c, c.Param("name"))
}

func (h *ConfigHandler) saveLocalTool(c *gin.Context, oldName string) {
	var req localToolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数: " + err.Error()})
		return
	}

	name := sanitizeLocalToolName(req.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "工具名称只能包含字母、数字、下划线、短横线和点"})
		return
	}
	if oldName != "" && name != oldName {
		c.JSON(http.StatusBadRequest, gin.H{"error": "暂不支持修改工具名称"})
		return
	}
	command := strings.TrimSpace(req.Command)
	description := strings.TrimSpace(req.Description)
	if command == "" || description == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "工具路径/命令和用途说明不能为空"})
		return
	}

	args := cleanLocalToolArgs(req.Args)
	command, args = normalizeLocalToolCommand(command, args)
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	tool := config.ToolConfig{
		Name:                   name,
		Command:                command,
		Args:                   args,
		Description:            description,
		Enabled:                enabled,
		Local:                  true,
		NoOutputTimeoutSeconds: -1,
		Parameters: []config.ParameterConfig{{
			Name:        "additional_args",
			Type:        "string",
			Description: "要传给该命令的参数，按命令行格式填写；例如 sqlmap 可填写 -u https://example.com?id=1 --batch。",
			Format:      "positional",
		}},
	}
	tool.ShortDescription = firstLocalToolLine(description)

	toolsDir := h.localToolsDir()
	if toolsDir == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "未配置 security.tools_dir"})
		return
	}
	if err := os.MkdirAll(toolsDir, 0700); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建工具目录失败: " + err.Error()})
		return
	}
	toolPath := filepath.Join(toolsDir, name+".yaml")
	if oldName == "" {
		if localToolConfigFileExists(toolsDir, name) {
			c.JSON(http.StatusConflict, gin.H{"error": "同名工具已存在"})
			return
		}
	} else {
		if !h.isLocalToolConfigured(oldName) {
			c.JSON(http.StatusNotFound, gin.H{"error": "本地工具不存在"})
			return
		}
		// 兼容手工创建的 .yml 配置，更新时沿用原文件名，避免同名配置重复加载。
		if existingPath, ok := localToolConfigPath(toolsDir, oldName); ok {
			toolPath = existingPath
		}
	}

	data, err := yaml.Marshal(tool)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成工具配置失败: " + err.Error()})
		return
	}
	if err := os.WriteFile(toolPath, data, 0600); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存工具配置失败: " + err.Error()})
		return
	}
	if oldName != "" && oldName != name {
		_ = os.Remove(filepath.Join(toolsDir, oldName+".yaml"))
	}

	// Reuse the normal apply path so built-in tools are re-registered together
	// with the newly created local tool.
	h.ApplyConfig(c)
}

func (h *ConfigHandler) DeleteLocalTool(c *gin.Context) {
	name := sanitizeLocalToolName(c.Param("name"))
	toolsDir := h.localToolsDir()
	if toolsDir == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "未配置 security.tools_dir"})
		return
	}
	if name == "" || !h.isLocalToolConfigured(name) {
		c.JSON(http.StatusNotFound, gin.H{"error": "本地工具不存在"})
		return
	}
	path, ok := localToolConfigPath(toolsDir, name)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "本地工具配置文件不存在"})
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除工具配置失败: " + err.Error()})
		return
	}
	h.ApplyConfig(c)
}

func (h *ConfigHandler) localToolsDir() string {
	h.mu.RLock()
	dir := strings.TrimSpace(h.config.Security.ToolsDir)
	configPath := h.configPath
	h.mu.RUnlock()
	if dir == "" {
		return ""
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(filepath.Dir(configPath), dir)
	}
	return filepath.Clean(dir)
}

func (h *ConfigHandler) isLocalToolConfigured(name string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, tool := range h.config.Security.Tools {
		if tool.Local && tool.Name == name {
			return true
		}
	}
	return false
}

func localToolConfigFileExists(dir, name string) bool {
	_, ok := localToolConfigPath(dir, name)
	return ok
}

func localToolConfigPath(dir, name string) (string, bool) {
	for _, ext := range []string{".yaml", ".yml"} {
		path := filepath.Join(dir, name+ext)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, true
		}
	}
	return "", false
}

func sanitizeLocalToolName(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	name := strings.Trim(b.String(), "._-")
	if name == "." || name == ".." {
		return ""
	}
	return name
}

func cleanLocalToolArgs(args []string) []string {
	cleaned := make([]string, 0, len(args))
	for _, arg := range args {
		if value := strings.TrimSpace(arg); value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return cleaned
}

func normalizeLocalToolCommand(command string, args []string) (string, []string) {
	command = strings.Trim(strings.TrimSpace(command), "\"'")
	if strings.EqualFold(filepath.Ext(command), ".py") {
		return "python", append([]string{command}, args...)
	}
	if strings.EqualFold(filepath.Ext(command), ".ps1") {
		return "powershell", append([]string{"-ExecutionPolicy", "Bypass", "-File", command}, args...)
	}
	return command, args
}

func firstLocalToolLine(value string) string {
	value = strings.TrimSpace(value)
	if idx := strings.IndexAny(value, "\r\n"); idx >= 0 {
		return strings.TrimSpace(value[:idx])
	}
	return value
}
