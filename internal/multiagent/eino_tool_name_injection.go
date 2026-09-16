package multiagent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/components/tool"
)

// injectToolNamesOnlyInstruction prepends a compact tool directory into the
// system instruction so the model can choose tools by purpose, not by scene.
// toolSearchMiddlewareActive must be true when prependEinoMiddlewares mounted toolsearch (dynamic tools); do not infer this
// by scanning tool names — tool_search is injected by middleware and is usually absent from the pre-split tools list.
func injectToolNamesOnlyInstruction(ctx context.Context, instruction string, tools []tool.BaseTool, toolSearchMiddlewareActive bool) string {
	descriptors := collectToolDescriptors(ctx, tools)
	if len(descriptors) == 0 {
		return strings.TrimSpace(instruction)
	}
	hasToolSearch := toolSearchMiddlewareActive
	if !hasToolSearch {
		for _, descriptor := range descriptors {
			if strings.EqualFold(strings.TrimSpace(descriptor.Name), "tool_search") {
				hasToolSearch = true
				break
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("以下是当前会话可用工具目录。工具按用途说明选择，不存在固定场景到固定工具的映射。\n")
	sb.WriteString("说明：用途说明用于判断何时/为什么调用；真实参数必须以当前请求下发的完整 schema 为准。\n")
	for _, descriptor := range descriptors {
		sb.WriteString("- ")
		sb.WriteString(descriptor.Name)
		if descriptor.Description != "" {
			sb.WriteString(": ")
			sb.WriteString(descriptor.Description)
		}
		sb.WriteByte('\n')
	}
	sb.WriteString("\n使用规则：\n")
	sb.WriteString("1) 先根据用途、目标和已有证据选择最相关工具，再阅读 schema 并填写参数；不要因为关键词命中就强行调用。\n")
	if hasToolSearch {
		sb.WriteString("2) 本会话启用了 tool_search；当目标工具未出现在当前完整 schema 中时，先按用途搜索并等待 schema 解锁，再调用。\n")
		sb.WriteString("3) tool_search 的 regex_pattern 按工具名匹配，例如子串 nuclei 或 ^exact_tool_name$；不要臆造不存在的工具名。\n\n")
	} else {
		sb.WriteString("2) 调用具体工具前，请先确认该工具的参数要求（以当前请求中的工具定义为准）；不确定时先澄清再调用。\n")
		sb.WriteString("3) 不要臆造不存在的工具名。\n\n")
	}
	names := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		names = append(names, descriptor.Name)
	}
	if s := strings.TrimSpace(injectShellToolGuidance("", names)); s != "" {
		sb.WriteString(s)
		sb.WriteString("\n\n")
	}
	if s := strings.TrimSpace(instruction); s != "" {
		sb.WriteString(s)
	}
	return sb.String()
}

type toolDescriptor struct {
	Name        string
	Description string
}

func collectToolDescriptors(ctx context.Context, tools []tool.BaseTool) []toolDescriptor {
	if len(tools) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tools))
	out := make([]toolDescriptor, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		info, err := t.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		name := strings.TrimSpace(info.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		description := strings.TrimSpace(info.Desc)
		out = append(out, toolDescriptor{Name: name, Description: description})
	}
	return out
}

func collectToolNames(ctx context.Context, tools []tool.BaseTool) []string {
	descriptors := collectToolDescriptors(ctx, tools)
	out := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		out = append(out, descriptor.Name)
	}
	return out
}
