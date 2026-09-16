package run

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chobits02/provena/internal/agent"
	"github.com/chobits02/provena/internal/authctx"
	"github.com/chobits02/provena/internal/fgs"
	"github.com/chobits02/provena/internal/piagent"

	"go.uber.org/zap"
)

// bridgeTokenHeader must match the header written into the generated Pi
// extension by internal/piagent.
const bridgeTokenHeader = "X-CyberStrike-Pi-Bridge-Token"

const (
	// maxBridgeBodyBytes bounds a single tool call payload.
	maxBridgeBodyBytes = 8 << 20
	// bridgeShutdownTimeout bounds graceful shutdown of the loopback listener.
	bridgeShutdownTimeout = 5 * time.Second
)

type bridgeCallRequest struct {
	ToolName string                 `json:"toolName"`
	Args     map[string]interface{} `json:"args"`
}

type bridgeCallResponse struct {
	Result      string `json:"result"`
	ExecutionID string `json:"executionId,omitempty"`
	IsError     bool   `json:"isError"`
}

// toolExecutor is the slice of the agent API the bridge drives. It is an
// interface so tests can assert what the bridge hands to the MCP authorizer.
type toolExecutor interface {
	ExecuteMCPToolForConversation(ctx context.Context, conversationID, toolName string, args map[string]interface{}) (*agent.ToolExecutionResult, error)
}

// bridge exposes the selected tools to a Pi activity over loopback HTTP. It is
// the headless equivalent of the console's Pi bridge: identical wire protocol,
// but bound to a run instead of a chat session.
type bridge struct {
	listener  net.Listener
	server    *http.Server
	token     string
	executor  toolExecutor
	convID    string
	principal authctx.Principal
	allowed   map[string]struct{}
	logger    *zap.Logger

	// graph is swappable: an interactive session starts a fresh graph on /new
	// while keeping the same bridge, tools and conversation identity.
	graphMu sync.RWMutex
	graph   *fgs.Store
}

// SetGraph points a long-lived bridge at a different graph.
func (b *bridge) SetGraph(graph *fgs.Store) {
	if b == nil {
		return
	}
	b.graphMu.Lock()
	b.graph = graph
	b.graphMu.Unlock()
}

// graphStore reads the graph currently attached to the bridge.
func (b *bridge) graphStore() *fgs.Store {
	if b == nil {
		return nil
	}
	b.graphMu.RLock()
	defer b.graphMu.RUnlock()
	return b.graph
}

func startBridge(ctx context.Context, graph *fgs.Store, ag toolExecutor, conversationID string, tools []piagent.BridgeTool, logger *zap.Logger) (*bridge, error) {
	// The MCP tool authorizer reads its principal from the context it is handed.
	// The bridge serves its own loopback requests, so that context is not the
	// run context unless the principal is carried across explicitly. Fail loudly
	// rather than serving requests that can never be authorized.
	principal, ok := authctx.PrincipalFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("headless bridge requires a context carrying the run principal")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("cannot bind loopback bridge: %w", err)
	}

	token, err := randomBridgeToken()
	if err != nil {
		_ = listener.Close()
		return nil, err
	}

	allowed := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if name := strings.TrimSpace(tool.Name); name != "" {
			allowed[name] = struct{}{}
		}
	}

	b := &bridge{
		listener:  listener,
		token:     token,
		graph:     graph,
		executor:  ag,
		convID:    conversationID,
		principal: principal,
		allowed:   allowed,
		logger:    logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", b.handleHealth)
	mux.HandleFunc("/call", b.handleCall)
	b.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		if serveErr := b.server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			if logger != nil {
				logger.Debug("headless bridge stopped", zap.Error(serveErr))
			}
		}
	}()

	return b, nil
}

func (b *bridge) URL() string {
	if b == nil || b.listener == nil {
		return ""
	}
	return "http://" + b.listener.Addr().String()
}

func (b *bridge) Token() string {
	if b == nil {
		return ""
	}
	return b.token
}

func (b *bridge) Close() error {
	if b == nil || b.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), bridgeShutdownTimeout)
	defer cancel()
	return b.server.Shutdown(ctx)
}

func (b *bridge) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		b.writeError(w, http.StatusForbidden, "loopback only")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

func (b *bridge) handleCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		b.writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if !isLoopbackRequest(r) {
		b.writeError(w, http.StatusForbidden, "loopback only")
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(bridgeTokenHeader)), []byte(b.token)) != 1 {
		b.writeError(w, http.StatusUnauthorized, "invalid bridge token")
		return
	}

	var req bridgeCallRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBridgeBodyBytes)).Decode(&req); err != nil {
		b.writeError(w, http.StatusBadRequest, "invalid tool call JSON: "+err.Error())
		return
	}
	toolName := strings.TrimSpace(req.ToolName)
	if _, ok := b.allowed[toolName]; !ok {
		b.writeError(w, http.StatusForbidden, "tool is not enabled for this run")
		return
	}
	if req.Args == nil {
		req.Args = map[string]interface{}{}
	}

	// FGS state tools operate on the graph alone and never reach the agent.
	if result, handled, status, err := b.handleFGSTool(toolName, req.Args); handled {
		if err != nil {
			b.writeError(w, status, err.Error())
			return
		}
		writeBridgeResult(w, bridgeCallResponse{Result: result})
		return
	}

	if b.executor == nil {
		b.writeError(w, http.StatusServiceUnavailable, "no tool executor is attached to this run")
		return
	}

	// Mirror the console Pi bridge: re-attach the run principal to the request
	// context, otherwise the MCP authorizer rejects every tool call with
	// "missing authenticated principal".
	callCtx := authctx.WithPrincipal(r.Context(), b.principal)
	result, err := b.executor.ExecuteMCPToolForConversation(callCtx, b.convID, toolName, req.Args)
	if err != nil {
		b.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if result == nil {
		result = &agent.ToolExecutionResult{Result: "(no output)"}
	}
	writeBridgeResult(w, bridgeCallResponse{
		Result:      result.Result,
		ExecutionID: result.ExecutionID,
		IsError:     result.IsError,
	})
}

// handleFGSTool implements the graph state contract. It reports whether the tool
// name belongs to the FGS boundary so the caller can fall through to the agent.
func (b *bridge) handleFGSTool(toolName string, args map[string]interface{}) (string, bool, int, error) {
	graph := b.graphStore()
	if graph == nil {
		return "", false, 0, nil
	}
	switch toolName {
	case "fgs_read":
		data, err := json.Marshal(graph.Snapshot())
		return string(data), true, http.StatusOK, err
	case "fgs_apply":
		mutations, err := mutationsFromArgs(args)
		if err != nil {
			return "", true, http.StatusBadRequest, err
		}
		result, err := graph.Apply(mutations)
		if err != nil {
			return "", true, http.StatusConflict, err
		}
		data, marshalErr := json.Marshal(result)
		return string(data), true, http.StatusOK, marshalErr
	case "submit_fact":
		label := stringArg(args, "label", "title")
		content := stringArg(args, "content", "fact", "summary")
		if label == "" || content == "" {
			return "", true, http.StatusBadRequest, errors.New("submit_fact requires label and content")
		}
		result, err := graph.SubmitFact(label, content,
			stringArg(args, "stepId", "step_id"), stringListArg(args, "evidence"))
		if err != nil {
			return "", true, http.StatusConflict, err
		}
		data, marshalErr := json.Marshal(result)
		return string(data), true, http.StatusOK, marshalErr
	case "submit_finding":
		label := stringArg(args, "label", "title")
		content := stringArg(args, "content", "finding", "summary")
		if label == "" || content == "" {
			return "", true, http.StatusBadRequest, errors.New("submit_finding requires label and content")
		}
		result, err := graph.SubmitFinding(label, content,
			stringArg(args, "stepId", "step_id"), stringListArg(args, "evidence"))
		if err != nil {
			return "", true, http.StatusConflict, err
		}
		data, marshalErr := json.Marshal(result)
		return string(data), true, http.StatusOK, marshalErr
	default:
		return "", false, 0, nil
	}
}

func mutationsFromArgs(args map[string]interface{}) ([]fgs.Mutation, error) {
	var value interface{}
	for _, key := range []string{"mutations", "operations", "changes"} {
		if candidate, ok := args[key]; ok {
			value = candidate
			break
		}
	}
	if value == nil {
		return nil, errors.New("fgs_apply requires a mutations array")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("invalid mutations: %w", err)
	}
	var mutations []fgs.Mutation
	if err := json.Unmarshal(data, &mutations); err != nil {
		return nil, fmt.Errorf("invalid mutations: %w", err)
	}
	if len(mutations) == 0 {
		return nil, errors.New("mutations must not be empty")
	}
	return mutations, nil
}

func writeBridgeResult(w http.ResponseWriter, payload bridgeCallResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (b *bridge) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":   message,
		"message": message,
	})
}

func isLoopbackRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func randomBridgeToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("cannot generate bridge token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
