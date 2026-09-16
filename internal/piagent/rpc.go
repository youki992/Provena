package piagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// piWaitDelay bounds how long Wait keeps waiting for the pipe readers once the
// Pi process itself has exited. A descendant that survives the process-tree
// kill still holds the inherited write end of stdout, and without this bound
// the activity would not return until that process decided to exit on its own.
// That is what previously made pi_agent.activity_timeout_seconds ineffective
// in wall-clock terms.
const piWaitDelay = 5 * time.Second

// Config contains the small set of Pi CLI options that can be mapped safely
// from Provena configuration. Pi keeps provider-specific model details
// in its own models.json; API key/base URL are passed through environment vars.
type Config struct {
	Command  string
	Provider string
	Protocol string
	Model    string
	Thinking string
	// AppendSystemPrompt is passed through Pi's system-prompt channel instead
	// of being concatenated into the user message. This keeps internal runtime
	// instructions out of conversation history and prevents prompt echoing.
	AppendSystemPrompt string
	// GlobalSystemPrompt is placed before AppendSystemPrompt inside Pi's system
	// prompt channel. It is kept separate so supervisor/FGS activities cannot
	// accidentally drop the application-wide baseline when replacing local text.
	GlobalSystemPrompt string
	ContextWindow      int
	MaxTokens          int
	WorkingDir         string
	SessionDir         string
	NoSession          bool
	// ContinueSession resumes the most recent session in SessionDir at startup.
	// Interactive callers use it to reopen the previous conversation.
	ContinueSession   bool
	Tools             []string
	NoContextFiles    bool
	NoSkills          bool
	NoPromptTemplates bool
	NoExtensions      bool
	NoTools           bool
	APIKey            string
	BaseURL           string
	BridgeURL         string
	BridgeToken       string
	BridgeTools       []BridgeTool
	// GeminiSchemaCompat enables the conservative provider-facing JSON Schema
	// subset required by Gemini. Other providers receive the original MCP
	// schema unchanged.
	GeminiSchemaCompat bool
	SkillPaths         []string
}

// BridgeTool describes a Provena MCP tool exposed to Pi through the
// short-lived loopback bridge created for one task.
type BridgeTool struct {
	Name             string                 `json:"name"`
	Label            string                 `json:"label,omitempty"`
	Description      string                 `json:"description,omitempty"`
	PromptSnippet    string                 `json:"promptSnippet,omitempty"`
	PromptGuidelines []string               `json:"promptGuidelines,omitempty"`
	Parameters       map[string]interface{} `json:"parameters,omitempty"`
}

// Event is one JSON object emitted by `pi --mode rpc`.
type Event struct {
	Type string
	Raw  map[string]interface{}
}

// Run starts one short-lived Pi RPC session, sends one prompt, and forwards
// every JSONL event to onEvent. The process is closed after agent_end; EOF is
// also handled because older Pi versions may not emit agent_end consistently.
func Run(ctx context.Context, cfg Config, prompt string, onEvent func(Event)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	l, err := prepareLaunch(cfg)
	if err != nil {
		return err
	}
	proc, err := startPiProcess(ctx, l)
	if err != nil {
		l.cleanup()
		return err
	}
	defer func() {
		proc.stop()
		l.cleanup()
	}()

	request := map[string]interface{}{"id": "provena", "type": "prompt", "message": prompt}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("编码 Pi prompt 失败: %w", err)
	}
	if err := proc.writeLine(payload); err != nil {
		return fmt.Errorf("发送 Pi prompt 失败: %w", err)
	}

	closeStdin := func() { _ = proc.stdin.Close() }
	ended := false
	completed := false
	var rpcErr error
	// Pi emits JSONL events. A single event can contain a large packet/
	// blackboard payload, so bufio.Scanner's token limit is not suitable here.
	// Reader.ReadBytes handles arbitrarily long lines (bounded only by memory).
	reader := bufio.NewReader(proc.stdout)
	linesRead := 0
	debugPi := os.Getenv("PROVENA_PI_DEBUG") != ""
	census := map[string]int{}
	deltaCensus := map[string]int{}
	// Pi is expected to emit JSONL only. Anything else is kept so a Pi that
	// refuses to start (usage text, an unsupported flag) stays diagnosable
	// instead of looking like an activity that simply produced nothing.
	var unparsed []string
readLoop:
	for {
		lineBytes, readErr := reader.ReadBytes('\n')
		if readErr != nil && len(lineBytes) == 0 {
			// A cancelled context closes the pipes from under this reader. That
			// is an expected shutdown, not a protocol failure, so fall through
			// to the ctx.Err() branch after Wait and report the timeout.
			if errors.Is(readErr, io.EOF) || ctx.Err() != nil {
				break
			}
			proc.terminate()
			return fmt.Errorf("读取 Pi RPC 输出失败: %w", readErr)
		}
		line := strings.TrimSpace(string(lineBytes))
		if line == "" {
			if errors.Is(readErr, io.EOF) {
				break
			}
			continue
		}
		var raw map[string]interface{}
		linesRead++
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			if len(unparsed) < maxUnparsedLines {
				unparsed = append(unparsed, truncateForError(line))
			}
			continue
		}
		e := Event{Raw: raw}
		if v, ok := raw["type"].(string); ok {
			e.Type = v
		}
		if debugPi {
			// A census is what makes a quiet turn diagnosable: it separates
			// "the model never answered" from "it answered and did nothing".
			census[e.Type]++
			if e.Type == "message_update" {
				if delta, ok := raw["assistantMessageEvent"].(map[string]interface{}); ok {
					if kind, ok := delta["type"].(string); ok {
						deltaCensus[kind]++
					}
				}
			}
			// Terminal events carry the model's actual reply, which is where an
			// upstream failure (quota, bad model id) shows up.
			switch e.Type {
			case "message_end", "turn_end", "agent_end":
				if data, marshalErr := json.Marshal(raw); marshalErr == nil {
					fmt.Fprintf(os.Stderr, "[pi-debug] %s %s\n", e.Type, truncateForError(string(data)))
				}
			}
		}
		if e.Type == "response" {
			if success, ok := raw["success"].(bool); ok && !success {
				rpcErr = fmt.Errorf("Pi RPC 请求失败: %s", piErrorText(raw))
				proc.terminate()
				break readLoop
			}
		}
		if onEvent != nil {
			onEvent(e)
		}
		if e.Type == "agent_end" && !ended {
			ended = true
			completed = true
			// Pi keeps the RPC process alive for subsequent commands. This
			// integration uses one process per request, so terminate it after
			// the final event instead of waiting for stdin shutdown. On Windows,
			// closing Node's stdin while its async handles are unwinding can
			// trigger a libuv assertion.
			proc.terminate()
			break readLoop
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	closeStdin()
	waitErr := proc.wait()
	if os.Getenv("PROVENA_PI_DEBUG") != "" {
		fmt.Fprintf(os.Stderr,
			"[pi-debug] command=%s\nexit=%v completed=%v lines=%d unparsed=%d\nstderr=%q\n",
			l.commandLine(), waitErr, completed, linesRead, len(unparsed),
			proc.stderrText())
		fmt.Fprintf(os.Stderr, "[pi-debug] events=%v\n[pi-debug] deltas=%v\n", census, deltaCensus)
	}
	if rpcErr != nil {
		return rpcErr
	}
	if completed {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if waitErr != nil {
		detail := proc.stderrText()
		if detail != "" {
			return fmt.Errorf("Pi 执行失败: %w: %s", waitErr, detail)
		}
		return fmt.Errorf("Pi 执行失败: %w", waitErr)
	}
	// Pi exited without emitting agent_end. Older builds may do that after a
	// successful turn, so stay permissive when it left no diagnostics — but
	// never swallow stderr or stray stdout, otherwise an early exit is
	// indistinguishable from an activity that simply produced nothing.
	detail := proc.stderrText()
	if detail == "" && len(unparsed) > 0 {
		detail = strings.Join(unparsed, " | ")
	}
	if detail != "" {
		return fmt.Errorf("Pi exited before completing the turn: %s", truncateForError(detail))
	}
	return nil
}

// maxUnparsedLines bounds how much non-JSON Pi output is retained.
const maxUnparsedLines = 5

// truncateForError keeps diagnostics readable in a one-line activity failure.
func truncateForError(detail string) string {
	const limit = 600
	runes := []rune(strings.Join(strings.Fields(detail), " "))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

// prepareAppendSystemPrompt avoids Windows CreateProcess command-line limits.
// Pi accepts a file path for --append-system-prompt and reads it as UTF-8.
// The fallback threshold also protects Unix callers from unusually large
// blackboard/history contexts while keeping short prompts easy to inspect.
func prepareAppendSystemPrompt(prompt, agentDir string) (string, func(), error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", func() {}, nil
	}
	if runtime.GOOS != "windows" && len(prompt) < 4096 {
		return prompt, func() {}, nil
	}
	dir := strings.TrimSpace(agentDir)
	cleanup := func() {}
	if dir == "" {
		created, err := os.MkdirTemp("", "provena-pi-prompt-")
		if err != nil {
			return "", nil, fmt.Errorf("创建 Pi system prompt 临时目录失败: %w", err)
		}
		dir = created
		cleanup = func() { _ = os.RemoveAll(created) }
	}
	path := filepath.Join(dir, "append-system-prompt.md")
	if err := os.WriteFile(path, []byte(prompt), 0o600); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("写入 Pi system prompt 文件失败: %w", err)
	}
	return path, cleanup, nil
}

// normalizePiProcessOutput decodes child-process diagnostics before they are
// converted to a Go string. Chinese Windows tools commonly emit GBK/GB18030
// on stderr; decoding after the fact would already have replaced bytes with
// U+FFFD and lose the real error message.
func normalizePiProcessOutput(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if utf8.Valid(raw) {
		return string(raw)
	}
	for _, decoder := range []func([]byte) ([]byte, error){
		func(data []byte) ([]byte, error) {
			out, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), data)
			return out, err
		},
		func(data []byte) ([]byte, error) {
			out, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), data)
			return out, err
		},
	} {
		if decoded, err := decoder(raw); err == nil && utf8.Valid(decoded) {
			return string(decoded)
		}
	}
	return strings.ToValidUTF8(string(raw), "")
}

func prepareAgentDir(cfg Config) (func(), string, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	apiKey := strings.TrimSpace(cfg.APIKey)
	model := strings.TrimSpace(cfg.Model)
	needsRuntimeDir := baseURL != "" && apiKey != "" && model != "" ||
		(strings.TrimSpace(cfg.BridgeURL) != "" && len(cfg.BridgeTools) > 0)
	if !needsRuntimeDir {
		return func() {}, "", nil
	}
	agentDir, err := os.MkdirTemp("", "provena-pi-agent-")
	if err != nil {
		return nil, "", fmt.Errorf("创建 Pi 临时配置目录失败: %w", err)
	}
	if baseURL != "" && apiKey != "" && model != "" {
		protocol := strings.TrimSpace(cfg.Protocol)
		if protocol == "" {
			protocol = "openai-completions"
		}
		models := map[string]interface{}{
			"providers": map[string]interface{}{
				"provena": map[string]interface{}{
					"baseUrl":    strings.TrimRight(baseURL, "/"),
					"api":        protocol,
					"apiKey":     "PROVENA_PI_API_KEY",
					"authHeader": true,
					"models": []interface{}{map[string]interface{}{
						"id":            model,
						"name":          model,
						"reasoning":     true,
						"input":         []string{"text"},
						"contextWindow": effectivePositive(cfg.ContextWindow, 128000),
						"maxTokens":     effectivePositive(cfg.MaxTokens, 32768),
					}},
				},
			},
		}
		data, err := json.MarshalIndent(models, "", "  ")
		if err != nil {
			_ = os.RemoveAll(agentDir)
			return nil, "", fmt.Errorf("编码 Pi models.json 失败: %w", err)
		}
		if err := os.WriteFile(fmt.Sprintf("%s%cmodels.json", agentDir, os.PathSeparator), data, 0o600); err != nil {
			_ = os.RemoveAll(agentDir)
			return nil, "", fmt.Errorf("写入 Pi models.json 失败: %w", err)
		}
	}
	return func() { _ = os.RemoveAll(agentDir) }, agentDir, nil
}

func runtimeModelConfigured(cfg Config) bool {
	return strings.TrimSpace(cfg.BaseURL) != "" &&
		strings.TrimSpace(cfg.APIKey) != "" &&
		strings.TrimSpace(cfg.Model) != ""
}

func runtimeProviderAvailable(agentDir string, cfg Config) bool {
	if strings.TrimSpace(agentDir) == "" || !runtimeModelConfigured(cfg) {
		return false
	}
	info, err := os.Stat(filepath.Join(agentDir, "models.json"))
	return err == nil && info.Mode().IsRegular()
}

func writeBridgeExtension(path, bridgeURL, bridgeToken string, tools []BridgeTool, geminiCompat bool) error {
	providerTools := tools
	// Pi forwards registered tool schemas to the selected model provider. Some
	// MCP servers expose complete JSON Schema documents, but Gemini accepts a
	// smaller Schema subset and rejects e.g. `additionalProperties: true`.
	// Sanitize only this short-lived provider-facing copy: the original MCP
	// schema remains intact for actual tool execution and validation. Other
	// providers receive the original schema unchanged.
	if geminiCompat {
		providerTools = sanitizeBridgeToolsForProvider(tools)
	}
	toolJSON, err := json.Marshal(providerTools)
	if err != nil {
		return fmt.Errorf("编码 Pi Bridge 工具定义失败: %w", err)
	}
	urlJSON, _ := json.Marshal(strings.TrimRight(strings.TrimSpace(bridgeURL), "/"))
	tokenJSON, _ := json.Marshal(bridgeToken)
	script := fmt.Sprintf(`import { Type } from "@mariozechner/pi-ai";

const bridgeURL = %s;
const bridgeToken = %s;
const bridgeTools = %s;

export default function (pi) {
  for (const tool of bridgeTools) {
    pi.registerTool({
      name: tool.name,
      label: tool.label || tool.name,
      description: tool.description || tool.name,
      promptSnippet: tool.promptSnippet || tool.description || tool.name,
      promptGuidelines: tool.promptGuidelines || [],
      // Pi expects a TypeBox schema. Type.Unsafe preserves the JSON Schema
      // received from Provena while making the extension registration
      // explicit and compatible with current Pi releases.
      parameters: Type.Unsafe(tool.parameters || { type: "object", properties: {} }),
      async execute(_toolCallId, params, signal) {
        const response = await fetch(bridgeURL + "/call", {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "X-Provena-Pi-Bridge-Token": bridgeToken,
          },
          body: JSON.stringify({ toolName: tool.name, args: params || {} }),
          signal,
        });
        const raw = await response.text();
        let payload;
        try { payload = JSON.parse(raw); } catch (_) { payload = { result: raw }; }
        if (!response.ok) {
          throw new Error(payload.error || payload.message || raw || ("Bridge HTTP " + response.status));
        }
        const result = typeof payload.result === "string"
          ? payload.result
          : JSON.stringify(payload.result ?? payload);
        return {
          content: [{ type: "text", text: result || "（工具无输出）" }],
          details: {
            toolName: tool.name,
            executionId: payload.executionId || "",
            isError: !!payload.isError,
          },
        };
      },
    });
  }
}
`, string(urlJSON), string(tokenJSON), string(toolJSON))
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		return fmt.Errorf("写入 Pi Bridge 扩展失败: %w", err)
	}
	return nil
}

func isGeminiProvider(provider, model, baseURL string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.ToLower(strings.TrimSpace(model))
	baseURL = strings.ToLower(strings.TrimSpace(baseURL))
	if strings.Contains(model, "gemini") {
		return true
	}
	if strings.Contains(provider, "gemini") || provider == "google" || strings.Contains(provider, "google-") {
		return true
	}
	return strings.Contains(baseURL, "generativelanguage.googleapis.com") ||
		strings.Contains(baseURL, "aiplatform.googleapis.com")
}

// sanitizeBridgeToolsForProvider makes bridge tool schemas portable across
// OpenAI-compatible and Gemini-backed Pi providers. Gemini's Schema protobuf
// does not permit boolean JSON Schema nodes (notably
// additionalProperties: true), nor composition/ref keywords. Keeping the
// compact common subset preserves the useful argument names, types, enums and
// descriptions while preventing one malformed external schema from disabling
// every tool call in a Pi activity.
func sanitizeBridgeToolsForProvider(tools []BridgeTool) []BridgeTool {
	if len(tools) == 0 {
		return nil
	}
	cleaned := make([]BridgeTool, len(tools))
	for i, tool := range tools {
		cleaned[i] = tool
		cleaned[i].Parameters = sanitizeBridgeSchema(tool.Parameters)
	}
	return cleaned
}

func sanitizeBridgeSchema(schema map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	if schema == nil {
		result["type"] = "object"
		result["properties"] = map[string]interface{}{}
		return result
	}

	// JSON Schema unions such as anyOf: [{type: object}, {type: null}] are
	// common in FastMCP output. A function argument is optional unless listed
	// in required, so retaining the first non-null branch is the least lossy
	// provider-neutral representation.
	if branch := bridgeSchemaNonNullBranch(schema); branch != nil {
		result = sanitizeBridgeSchema(branch)
	}

	if value, ok := schema["type"]; ok {
		if typeName := bridgeSchemaType(value); typeName != "" {
			result["type"] = typeName
		}
	}
	if description, ok := schema["description"].(string); ok && strings.TrimSpace(description) != "" {
		result["description"] = description
	}
	if enum := bridgeSchemaEnum(schema["enum"]); len(enum) > 0 {
		result["enum"] = enum
	}
	if properties, ok := schema["properties"].(map[string]interface{}); ok {
		cleanedProperties := make(map[string]interface{}, len(properties))
		for name, value := range properties {
			if property, ok := value.(map[string]interface{}); ok {
				cleanedProperties[name] = sanitizeBridgeSchema(property)
				continue
			}
			// JSON Schema also permits boolean property schemas. Gemini does not;
			// retain a generic object argument instead of passing the boolean on.
			cleanedProperties[name] = map[string]interface{}{"type": "object"}
		}
		result["properties"] = cleanedProperties
		if _, hasType := result["type"]; !hasType {
			result["type"] = "object"
		}
	}
	if required := bridgeSchemaRequired(schema["required"]); len(required) > 0 {
		result["required"] = required
	}
	if items, ok := schema["items"]; ok {
		if itemSchema, ok := bridgeSchemaItems(items); ok {
			result["items"] = itemSchema
			if _, hasType := result["type"]; !hasType {
				result["type"] = "array"
			}
		}
	}

	// A parameter without a declared JSON type (for example a generic REST
	// request body) must still become a valid Gemini Schema node.
	if _, hasType := result["type"]; !hasType {
		result["type"] = "object"
	}
	return result
}

func bridgeSchemaNonNullBranch(schema map[string]interface{}) map[string]interface{} {
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		branches, ok := schema[key].([]interface{})
		if !ok {
			continue
		}
		for _, value := range branches {
			branch, ok := value.(map[string]interface{})
			if !ok || bridgeSchemaType(branch["type"]) == "null" {
				continue
			}
			return branch
		}
	}
	return nil
}

func bridgeSchemaType(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []interface{}:
		for _, item := range typed {
			if typeName := bridgeSchemaType(item); typeName != "" && typeName != "null" {
				return typeName
			}
		}
	}
	return ""
}

func bridgeSchemaEnum(value interface{}) []interface{} {
	cleaned := make([]interface{}, 0)
	switch values := value.(type) {
	case []string:
		for _, item := range values {
			if strings.TrimSpace(item) != "" {
				cleaned = append(cleaned, item)
			}
		}
	case []interface{}:
		for _, item := range values {
			switch item.(type) {
			case string, float64, bool:
				cleaned = append(cleaned, item)
			}
		}
	}
	return cleaned
}

func bridgeSchemaRequired(value interface{}) []string {
	cleaned := make([]string, 0)
	switch values := value.(type) {
	case []string:
		for _, name := range values {
			if strings.TrimSpace(name) != "" {
				cleaned = append(cleaned, name)
			}
		}
	case []interface{}:
		for _, item := range values {
			if name, ok := item.(string); ok && strings.TrimSpace(name) != "" {
				cleaned = append(cleaned, name)
			}
		}
	}
	return cleaned
}

func bridgeSchemaItems(value interface{}) (map[string]interface{}, bool) {
	if schema, ok := value.(map[string]interface{}); ok {
		return sanitizeBridgeSchema(schema), true
	}
	if tuple, ok := value.([]interface{}); ok {
		for _, item := range tuple {
			if schema, ok := item.(map[string]interface{}); ok {
				return sanitizeBridgeSchema(schema), true
			}
		}
	}
	return nil, false
}

func effectivePositive(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func piErrorText(raw map[string]interface{}) string {
	for _, key := range []string{"error", "message"} {
		if s, ok := raw[key].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return "未知错误"
}
