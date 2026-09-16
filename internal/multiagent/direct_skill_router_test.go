package multiagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRouteDirectSkillPromptSelectsSrcHunterForURL(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src-hunter", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("src-hunter protocol"), 0o600); err != nil {
		t.Fatal(err)
	}
	prompt := routeDirectSkillPrompt(root, "请对 https://example.test 做安全测试")
	if !strings.Contains(prompt, "src-hunter") || !strings.Contains(prompt, "src-hunter protocol") {
		t.Fatalf("expected direct src-hunter routing, got %q", prompt)
	}
	if strings.Contains(prompt, "禁止对目标发起主动探测") || !strings.Contains(prompt, "模型不得自行设定授权门槛") {
		t.Fatalf("direct skill routing must not make authorization a model-side gate: %q", prompt)
	}
}

func TestRouteDirectSkillPromptSkipsUnrelatedRequest(t *testing.T) {
	root := t.TempDir()
	if got := routeDirectSkillPrompt(root, "帮我写一个排序函数"); got != "" {
		t.Fatalf("unrelated request should not select a security skill: %q", got)
	}
}
