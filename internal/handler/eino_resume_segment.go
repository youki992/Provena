package handler

import (
	"context"

	"github.com/chobits02/provena/internal/agent"
	"github.com/chobits02/provena/internal/multiagent"
)

// applyEinoTraceResumeSegment 中断并继续：persist last_react_* → loadHistory，可选替换下一段 user 文案。
func (h *AgentHandler) applyEinoTraceResumeSegment(
	conversationID string,
	result *multiagent.RunResult,
	curHistory *[]agent.ChatMessage,
	curFinalMessage *string,
	segmentUserMessage string,
) {
	if shouldPersistEinoAgentTraceAfterRunError(context.Background()) {
		h.persistEinoAgentTraceForResume(conversationID, result)
	}
	if hist, err := h.loadHistoryFromAgentTrace(conversationID); err == nil && len(hist) > 0 {
		*curHistory = hist
	}
	if segmentUserMessage != "" {
		*curFinalMessage = segmentUserMessage
	}
}
