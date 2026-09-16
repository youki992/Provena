package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// DefaultGlobalSystemPromptFilename is the default application-wide prompt
// file name. It is resolved relative to the config file directory.
const DefaultGlobalSystemPromptFilename = "GLOBAL_SYSTEM_PROMPT.md"

// ResolveGlobalSystemPromptPath resolves the configured global prompt path.
// An empty configured path intentionally falls back to the default file so a
// deployment can opt in simply by placing GLOBAL_SYSTEM_PROMPT.md beside its
// config.yaml.
func ResolveGlobalSystemPromptPath(configPath, configuredPath string) string {
	p := strings.TrimSpace(configuredPath)
	if p == "" {
		p = DefaultGlobalSystemPromptFilename
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}

	base := strings.TrimSpace(configPath)
	if base == "" {
		base = "."
	} else {
		base = filepath.Dir(base)
	}
	return filepath.Clean(filepath.Join(base, p))
}

// LoadGlobalSystemPrompt loads the global prompt at startup. A missing file
// is not an error: this keeps older deployments compatible while allowing the
// file to be added later. The returned path is useful for diagnostics.
func LoadGlobalSystemPrompt(configPath, configuredPath string, logger *zap.Logger) (string, string, error) {
	path := ResolveGlobalSystemPromptPath(configPath, configuredPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if logger != nil {
				logger.Debug("全局系统提示词文件不存在，使用内置提示词", zap.String("path", path))
			}
			return "", path, nil
		}
		return "", path, fmt.Errorf("读取全局系统提示词失败（%s）: %w", path, err)
	}
	return strings.TrimSpace(string(data)), path, nil
}

// PrependSystemPrompt places global before local. Keeping this operation in a
// single helper makes the ordering explicit at every prompt boundary.
func PrependSystemPrompt(global, local string) string {
	global = strings.TrimSpace(global)
	local = strings.TrimSpace(local)
	if global == "" {
		return local
	}
	if local == "" {
		return global
	}
	return global + "\n\n" + local
}
