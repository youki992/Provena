package multiagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/chobits02/provena/internal/config"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// toolCallBudget is shared by all tool endpoints belonging to one agent run.
// Positive values are optional guardrails. A zero value means unlimited for that
// dimension, allowing the model to continue a promising, evidence-backed chain;
// the enclosing agent iteration and request timeout limits remain in force.
type toolCallBudget struct {
	mu            sync.Mutex
	maxTotal      int
	maxPerTool    int
	maxDuplicate  int
	maxToolSearch int
	total         int
	toolSearches  int
	perTool       map[string]int
	bySignature   map[string]int
}

func newToolCallBudget(mw *config.MultiAgentEinoMiddlewareConfig) *toolCallBudget {
	if mw == nil {
		mw = &config.MultiAgentEinoMiddlewareConfig{}
	}
	return &toolCallBudget{
		maxTotal:      mw.MaxToolCallsEffective(),
		maxPerTool:    mw.MaxToolCallsPerToolEffective(),
		maxDuplicate:  mw.MaxDuplicateToolCallsEffective(),
		maxToolSearch: mw.MaxToolSearchCallsEffective(),
		perTool:       make(map[string]int),
		bySignature:   make(map[string]int),
	}
}

func normalizedToolArguments(raw string) string {
	var value any
	if json.Unmarshal([]byte(raw), &value) == nil {
		if normalized, err := json.Marshal(value); err == nil {
			return string(normalized)
		}
	}
	return strings.TrimSpace(raw)
}

func toolSignature(name, arguments string) string {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(name)) + "\x00" + normalizedToolArguments(arguments)))
	return hex.EncodeToString(h[:])
}

func budgetExemptTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "exit", "skill", "write_todos", "taskcreate", "taskget", "taskupdate", "tasklist":
		return true
	default:
		return false
	}
}

func (b *toolCallBudget) admit(name, arguments string) (bool, string) {
	if b == nil {
		return true, ""
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false, "tool call blocked: empty tool name"
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	sig := toolSignature(name, arguments)
	if b.maxDuplicate > 0 && b.bySignature[sig] >= b.maxDuplicate {
		return false, fmt.Sprintf("工具调用已拦截：%s 使用完全相同参数重复执行。请复用已有结果；没有新证据时立即结束并总结。", name)
	}
	if name == "tool_search" {
		if b.maxToolSearch > 0 && b.toolSearches >= b.maxToolSearch {
			return false, "工具调用已拦截：tool_search 本轮发现次数已达上限。请只从已解锁工具中选择一个最相关工具，或直接总结。"
		}
		b.toolSearches++
	} else if !budgetExemptTool(name) {
		if b.maxTotal > 0 && b.total >= b.maxTotal {
			return false, fmt.Sprintf("工具调用已拦截：本轮已达到 %d 次工具执行上限。请停止继续探测，整理当前证据并调用 exit 输出阶段性结论。", b.maxTotal)
		}
		if b.maxPerTool > 0 && b.perTool[name] >= b.maxPerTool {
			return false, fmt.Sprintf("工具调用已拦截：%s 本轮已达到 %d 次上限。请换用有明确新信息的步骤，或结束。", name, b.maxPerTool)
		}
		b.total++
		b.perTool[name]++
	}
	b.bySignature[sig]++
	return true, ""
}

// toolCallBudgetMiddleware turns repeated/over-budget calls into model-visible results,
// so the run can finish with a useful partial report instead of executing more commands.
func toolCallBudgetMiddleware(budget *toolCallBudget) compose.ToolMiddleware {
	check := func(input *compose.ToolInput) (string, bool) {
		if input == nil {
			return "tool call blocked: missing input", true
		}
		allowed, reason := budget.admit(input.Name, input.Arguments)
		return reason, !allowed
	}
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				if reason, blocked := check(input); blocked {
					return &compose.ToolOutput{Result: reason}, nil
				}
				return next(ctx, input)
			}
		},
		Streamable: func(next compose.StreamableToolEndpoint) compose.StreamableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.StreamToolOutput, error) {
				if reason, blocked := check(input); blocked {
					return &compose.StreamToolOutput{Result: schema.StreamReaderFromArray([]string{reason})}, nil
				}
				return next(ctx, input)
			}
		},
	}
}
