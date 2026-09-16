package handler

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/chobits02/provena/internal/agent"
	"github.com/chobits02/provena/internal/authctx"
	"github.com/chobits02/provena/internal/fgs"
	"github.com/chobits02/provena/internal/mcp/builtin"
	"github.com/chobits02/provena/internal/piagent"
	"go.uber.org/zap"
)

type piBridge struct {
	server         *http.Server
	listener       net.Listener
	url            string
	token          string
	tools          map[string]struct{}
	principal      authctx.Principal
	baseCtx        context.Context
	agent          *agent.Agent
	conversationID string
	assetScope     piAssetScope
	executionGate  *piExecutionGate
	fgs            *fgs.Store
	playbookRoot   string
	workerStepID   string
	logger         *zap.Logger
}

type piBridgeCallRequest struct {
	ToolName string                 `json:"toolName"`
	Args     map[string]interface{} `json:"args"`
}

type piBridgeCallResponse struct {
	Result      string `json:"result"`
	ExecutionID string `json:"executionId,omitempty"`
	IsError     bool   `json:"isError"`
}

// startPiBridge exposes only the tools selected for this Pi conversation on a
// random loopback port. It never exposes MCP credentials or a public route.
func (h *AgentHandler) startPiBridge(ctx context.Context, principal authctx.Principal, conversationID, request string, tools []piagent.BridgeTool) (*piBridge, error) {
	return h.startPiBridgeWithFGS(ctx, principal, conversationID, request, nil, tools)
}

func (h *AgentHandler) startPiBridgeWithFGS(ctx context.Context, principal authctx.Principal, conversationID, request string, graph *fgs.Store, tools []piagent.BridgeTool) (*piBridge, error) {
	if h == nil || h.agent == nil {
		return nil, fmt.Errorf("Pi Bridge 初始化失败：Agent 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if len(tools) == 0 {
		return nil, fmt.Errorf("Pi Bridge 初始化失败：当前没有可用工具")
	}
	secret, err := randomPiBridgeToken()
	if err != nil {
		return nil, err
	}
	listener, err := listenPiBridge(ctx, net.Listen)
	if err != nil {
		return nil, fmt.Errorf("Pi Bridge 监听失败: %w", err)
	}
	allowed := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name != "" {
			allowed[name] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		_ = listener.Close()
		return nil, fmt.Errorf("Pi Bridge 初始化失败：工具名称为空")
	}
	b := &piBridge{
		listener:       listener,
		url:            "http://" + listener.Addr().String(),
		token:          secret,
		tools:          allowed,
		principal:      principal,
		baseCtx:        ctx,
		agent:          h.agent,
		conversationID: strings.TrimSpace(conversationID),
		assetScope:     piAssetScopeFromRequest(request),
		fgs:            graph,
		playbookRoot:   h.piPlaybookRoot(),
		logger:         h.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", b.handleHealth)
	mux.HandleFunc("/call", b.handleCall)
	b.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 * 1024}
	go func() {
		_ = b.server.Serve(listener)
	}()
	return b, nil
}

type piBridgeListenFunc func(network, address string) (net.Listener, error)

// listenPiBridge absorbs transient Windows Winsock pressure. FGS creates
// short-lived loopback bridges for clean Decide/Execute processes, and a
// concurrent worker burst can occasionally make an ephemeral bind return
// WSAENOBUFS even though the port range is not permanently exhausted.
// Retrying here prevents one transient local resource error from terminating
// the whole task. Cancellation remains immediate and the final error is kept.
func listenPiBridge(ctx context.Context, listen piBridgeListenFunc) (net.Listener, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if listen == nil {
		listen = net.Listen
	}
	delays := []time.Duration{0, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second}
	var lastErr error
	for attempt, delay := range delays {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return nil, context.Cause(ctx)
			case <-timer.C:
			}
		}
		listener, err := listen("tcp4", "127.0.0.1:0")
		if err == nil {
			return listener, nil
		}
		lastErr = err
		if attempt == len(delays)-1 || !isTransientPiBridgeListenError(err) {
			break
		}
	}
	return nil, lastErr
}

func isTransientPiBridgeListenError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "insufficient buffer space") ||
		strings.Contains(message, "queue was full") ||
		strings.Contains(message, "no buffer space available") ||
		strings.Contains(message, "wsaenobufs") ||
		strings.Contains(message, "address already in use")
}

func (b *piBridge) URL() string {
	if b == nil {
		return ""
	}
	return b.url
}

func (b *piBridge) Token() string {
	if b == nil {
		return ""
	}
	return b.token
}

// SetExecutionGate attaches the per-conversation workflow guard after the
// bridge is created. The bridge starts before Pi so that the RPC extension can
// be fully configured, while the gate is ready before the first tool call.
func (b *piBridge) SetExecutionGate(gate *piExecutionGate) {
	if b == nil {
		return
	}
	b.executionGate = gate
}

func (b *piBridge) Close() error {
	if b == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if b.server != nil {
		return b.server.Shutdown(ctx)
	}
	if b.listener != nil {
		return b.listener.Close()
	}
	return nil
}

func (b *piBridge) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

func (b *piBridge) handleCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		b.writeError(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	if !isLoopbackRequest(r) {
		b.writeError(w, http.StatusForbidden, "仅允许本机调用")
		return
	}
	provided := r.Header.Get("X-CyberStrike-Pi-Bridge-Token")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(b.token)) != 1 {
		b.writeError(w, http.StatusUnauthorized, "Bridge token 无效")
		return
	}
	var req piBridgeCallRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	if err := decoder.Decode(&req); err != nil {
		b.writeError(w, http.StatusBadRequest, "工具参数不是有效 JSON: "+err.Error())
		return
	}
	toolName := strings.TrimSpace(req.ToolName)
	if _, ok := b.tools[toolName]; !ok {
		b.writeError(w, http.StatusForbidden, "工具不在当前 Pi 会话允许列表中")
		return
	}
	if req.Args == nil {
		req.Args = map[string]interface{}{}
	}
	if toolName == builtin.ToolCreateAsset && !piAssetTargetInScope(piAssetTargetFromArgs(req.Args), b.assetScope) {
		b.writeError(w, http.StatusForbidden, "资产目标不在当前任务目标范围内，已阻止写入")
		return
	}
	if b.executionGate != nil {
		if allowed, reason := b.executionGate.allowBridgeCall(toolName, req.Args); !allowed {
			b.writeError(w, http.StatusConflict, reason)
			return
		}
	}
	select {
	case <-b.baseCtx.Done():
		b.writeError(w, http.StatusRequestTimeout, "Pi 任务已结束")
		return
	default:
	}
	callCtx := authctx.WithPrincipal(r.Context(), b.principal)
	callCtx, cancel := context.WithCancel(callCtx)
	defer cancel()
	go func() {
		select {
		case <-b.baseCtx.Done():
			cancel()
		case <-callCtx.Done():
		}
	}()
	if b.fgs != nil {
		if result, ok, status, err := b.handleFGSTool(callCtx, toolName, req.Args); ok {
			if err != nil {
				b.writeError(w, status, err.Error())
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(piBridgeCallResponse{Result: result})
			return
		}
	}
	result, err := b.callAgent(callCtx, toolName, req.Args)
	if err != nil {
		// A transport/executor failure is still an ARL boundary observation.
		// Preserve it in FGS before returning HTTP failure so a resumed Decide
		// can distinguish "ARL failed" from "ARL was never attempted".
		if b.fgs != nil && isARLBridgeTool(toolName) {
			failed := &agent.ToolExecutionResult{Result: "ARL MCP 调用失败：" + err.Error(), IsError: true}
			if recordErr := recordARLResultToFGS(b.fgs, b.workerStepID, toolName, req.Args, failed); recordErr != nil && b.logger != nil {
				b.logger.Warn("ARL 失败结果写入 FGS 失败", zap.String("tool", toolName), zap.Error(recordErr))
			}
		}
		b.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if result == nil {
		result = &agent.ToolExecutionResult{Result: "（工具无输出）"}
	}
	// ARL is the authoritative reconnaissance backend in v3. Every returned
	// observation (including an error response) is captured as a durable FGS
	// Fact by the controller before Pi receives the result. This keeps planning
	// evidence-driven even when Pi forgets to call submit_fact itself.
	if b.fgs != nil && isARLBridgeTool(toolName) {
		if recordErr := recordARLResultToFGS(b.fgs, b.workerStepID, toolName, req.Args, result); recordErr != nil && b.logger != nil {
			b.logger.Warn("ARL 结果写入 FGS 失败", zap.String("tool", toolName), zap.Error(recordErr))
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(piBridgeCallResponse{Result: result.Result, ExecutionID: result.ExecutionID, IsError: result.IsError})
}

// SetWorkerStepID associates subsequent bridge calls with the Step currently
// assigned to this short-lived Execute Worker.
func (b *piBridge) SetWorkerStepID(stepID string) {
	if b != nil {
		b.workerStepID = strings.TrimSpace(stepID)
	}
}

func (b *piBridge) handleFGSTool(_ context.Context, toolName string, args map[string]interface{}) (string, bool, int, error) {
	if b == nil || b.fgs == nil {
		return "", false, 0, nil
	}
	switch strings.TrimSpace(toolName) {
	case "fgs_read":
		data, err := json.Marshal(b.fgs.Snapshot())
		return string(data), true, http.StatusOK, err
	case "fgs_apply":
		mutations, err := fgsMutationsFromArgs(args)
		if err != nil {
			return "", true, http.StatusBadRequest, err
		}
		result, err := b.fgs.Apply(mutations)
		if err != nil {
			return "", true, http.StatusConflict, err
		}
		data, marshalErr := json.Marshal(result)
		return string(data), true, http.StatusOK, marshalErr
	case "fgs_playbook":
		result, err := readPiFGSPlaybook(b.fgs, b.playbookRoot, args)
		return result, true, http.StatusOK, err
	case "submit_fact":
		label := stringArg(args, "label", "title")
		content := stringArg(args, "content", "fact", "summary")
		stepID := stringArg(args, "stepId", "step_id", "sourceStepId", "source_step_id")
		evidence := stringListArg(args, "evidence", "evidenceItems", "evidence_items")
		if label == "" || content == "" {
			return "", true, http.StatusBadRequest, fmt.Errorf("submit_fact 需要 label 和 content")
		}
		result, err := b.fgs.SubmitFact(label, content, stepID, evidence)
		if err != nil {
			return "", true, http.StatusConflict, err
		}
		data, marshalErr := json.Marshal(result)
		return string(data), true, http.StatusOK, marshalErr
	case "submit_finding":
		label := stringArg(args, "label", "title")
		content := stringArg(args, "content", "finding", "summary")
		stepID := stringArg(args, "stepId", "step_id", "sourceStepId", "source_step_id")
		evidence := stringListArg(args, "evidence", "evidenceItems", "evidence_items")
		if label == "" || content == "" {
			return "", true, http.StatusBadRequest, fmt.Errorf("submit_finding 需要 label 和 content")
		}
		result, err := b.fgs.SubmitFinding(label, content, stepID, evidence)
		if err != nil {
			return "", true, http.StatusConflict, err
		}
		data, marshalErr := json.Marshal(result)
		return string(data), true, http.StatusOK, marshalErr
	default:
		return "", false, 0, nil
	}
}

func fgsMutationsFromArgs(args map[string]interface{}) ([]fgs.Mutation, error) {
	var value interface{}
	for _, key := range []string{"mutations", "operations", "changes"} {
		if candidate, ok := args[key]; ok {
			value = candidate
			break
		}
	}
	if value == nil {
		return nil, fmt.Errorf("fgs_apply 需要 mutations 数组")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("fgs_apply mutations 无效: %w", err)
	}
	var mutations []fgs.Mutation
	if err := json.Unmarshal(data, &mutations); err != nil {
		return nil, fmt.Errorf("fgs_apply mutations 无效: %w", err)
	}
	if len(mutations) == 0 {
		return nil, fmt.Errorf("fgs_apply mutations 不能为空")
	}
	return mutations, nil
}

func stringArg(args map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringListArg(args map[string]interface{}, keys ...string) []string {
	for _, key := range keys {
		value, ok := args[key]
		if !ok {
			continue
		}
		if items, ok := value.([]interface{}); ok {
			out := make([]string, 0, len(items))
			for _, item := range items {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					out = append(out, strings.TrimSpace(text))
				}
			}
			return out
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			return []string{strings.TrimSpace(text)}
		}
	}
	return nil
}

func piAssetTargetFromArgs(args map[string]interface{}) string {
	if args == nil {
		return ""
	}
	var value string
	var valueKey string
	for _, key := range []string{"target", "url", "domain", "host", "ip"} {
		candidate, ok := args[key].(string)
		if ok && strings.TrimSpace(candidate) != "" {
			valueKey = key
			value = strings.TrimSpace(candidate)
			break
		}
	}
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		return value
	}
	protocol := ""
	if value, ok := args["protocol"].(string); ok {
		protocol = strings.TrimSpace(value)
	}
	if protocol == "" {
		if valueKey == "ip" {
			protocol = "tcp"
		} else {
			protocol = "https"
		}
	}
	return protocol + "://" + value
}

func (b *piBridge) callAgent(ctx context.Context, toolName string, args map[string]interface{}) (*agent.ToolExecutionResult, error) {
	// The Agent method retains the existing MCP routing, authorization, HITL,
	// conversation binding and execution-monitor persistence behavior.
	if b == nil || b.agent == nil {
		return nil, fmt.Errorf("Pi Bridge 未绑定 Agent")
	}
	return b.agent.ExecuteMCPToolForConversation(ctx, b.conversationID, toolName, args)
}

func randomPiBridgeToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成 Pi Bridge token 失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (b *piBridge) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
