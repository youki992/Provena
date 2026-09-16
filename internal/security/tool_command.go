package security

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ResolveToolCommand makes Python-backed tool definitions portable.  Linux
// distributions commonly expose python3 only, while Windows installations and
// some virtual environments expose python (or py) instead.
func ResolveToolCommand(command string) string {
	return resolveToolCommandWithLookup(command, exec.LookPath)
}

func resolveToolCommandWithLookup(command string, lookPath func(string) (string, error)) string {
	trimmed := strings.TrimSpace(command)
	if trimmed != "python" && trimmed != "python3" {
		return command
	}

	candidates := []string{trimmed}
	if trimmed == "python" {
		candidates = append(candidates, "python3", "py")
	} else {
		candidates = append(candidates, "python", "py")
	}
	for _, candidate := range candidates {
		if resolved, err := lookPath(candidate); err == nil && strings.TrimSpace(resolved) != "" {
			return resolved
		}
	}
	return command
}

const windowsPythonInlineScriptLimit = 8000

// PrepareToolCommand avoids Windows CreateProcess failures for tools that
// embed a large Python program in `python -c ...`. The temporary script is
// still executed by the configured interpreter, so tool behavior is unchanged.
func PrepareToolCommand(command string, args []string) (string, []string, func(), error) {
	return prepareToolCommandForOS(command, args, os.PathSeparator == '\\')
}

func prepareToolCommandForOS(command string, args []string, isWindows bool) (string, []string, func(), error) {
	if !isWindows || !isPythonCommand(command) {
		return command, args, func() {}, nil
	}
	prepared := append([]string(nil), args...)
	for i := 0; i+1 < len(prepared); i++ {
		if prepared[i] != "-c" && prepared[i] != "-C" {
			continue
		}
		if len(prepared[i+1]) <= windowsPythonInlineScriptLimit {
			continue
		}
		file, err := os.CreateTemp("", "provena-tool-*.py")
		if err != nil {
			return command, args, func() {}, fmt.Errorf("创建 Python 临时脚本失败: %w", err)
		}
		path := filepath.Clean(file.Name())
		if _, err := file.WriteString(prepared[i+1]); err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return command, args, func() {}, fmt.Errorf("写入 Python 临时脚本失败: %w", err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(path)
			return command, args, func() {}, fmt.Errorf("关闭 Python 临时脚本失败: %w", err)
		}
		prepared = append(prepared[:i], append([]string{path}, prepared[i+2:]...)...)
		return command, prepared, func() { _ = os.Remove(path) }, nil
	}
	return command, prepared, func() {}, nil
}

func isPythonCommand(command string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(command)))
	return base == "python" || base == "python.exe" || base == "python3" || base == "python3.exe" || base == "py" || base == "py.exe"
}
