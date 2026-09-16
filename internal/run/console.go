package run

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/chobits02/provena/internal/fgs"
	"github.com/chobits02/provena/internal/piagent"
	"github.com/chobits02/provena/internal/termout"
)

// Graph triggers accepted by console.graph, plus the values of --graph.
const (
	graphOff      = "off"
	graphActivity = "activity"
	graphLive     = "live"

	triggerMutation = "mutation"
	triggerActivity = "activity"
	triggerFinal    = "final"
)

func normalizeGraphMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case graphOff, "none", "false":
		return graphOff
	case graphLive, "always", "all", "true":
		return graphLive
	default:
		return graphActivity
	}
}

// Gutters attribute streamed output to its channel at a glance.
const (
	thinkingGutter = "  · "
	textGutter     = "  │ "
	toolGutter     = "  → "
	resultGutter   = "  ← "
)

// graphMaxNodes keeps a wide graph readable on a terminal.
const graphMaxNodes = 60

// console renders a run to a terminal. It is the CLI face of a run: reasoning,
// text, tool calls and the FGS graph are streamed as they happen, the way a
// coding-agent CLI does. Plain mode reproduces the original line-oriented
// output byte for byte so pipes, logs and CI keep working.
type console struct {
	out       io.Writer
	style     *termout.Style
	rich      bool
	verbose   bool
	thinking  bool
	graphMode string

	atLineStart  bool
	streamGutter string

	// lastModelError dedupes the repeat of message_end/turn_end.
	lastModelError string
}

func newConsole(out io.Writer, opts Options) *console {
	if out == nil {
		out = os.Stdout
	}
	return &console{
		out:          out,
		style:        termout.New(out),
		rich:         !opts.Plain,
		verbose:      opts.Verbose,
		thinking:     opts.ShowThinking,
		graphMode:    normalizeGraphMode(opts.GraphMode),
		atLineStart:  true,
		streamGutter: "",
	}
}

// header introduces the run. Plain mode stays silent so downstream tooling sees
// exactly what it saw before.
func (c *console) header(target, objective string, scope []string, maxActivities int) {
	if c == nil || !c.rich {
		return
	}
	c.style.Println(c.style.Cyan("Provena run") + c.style.Dim("  ·  "+target))
	if strings.TrimSpace(objective) != "" {
		c.style.Println(c.style.Dim("  objective  ") + objective)
	}
	if len(scope) > 0 {
		c.style.Println(c.style.Dim("  scope      ") + strings.Join(scope, ", "))
	}
	c.style.Println(c.style.Dim(fmt.Sprintf("  activities  up to %d", maxActivities)))
}

// writer returns the destination, tolerating a nil console so callers never
// need to guard.
func (c *console) writer() io.Writer {
	if c == nil || c.out == nil {
		return os.Stdout
	}
	return c.out
}

func (c *console) activityStart(activity, total int) {
	if c == nil || !c.rich {
		_, _ = fmt.Fprintf(c.writer(), "activity %d/%d...\n", activity, total)
		return
	}
	c.endStream()
	c.style.Println("")
	c.style.Println(c.style.Cyan("▌") + c.style.Bold(fmt.Sprintf(" activity %d/%d", activity, total)))
}

func (c *console) activityEnd(calls int, elapsed time.Duration, err error) {
	if c == nil {
		return
	}
	c.endStream()
	if !c.rich {
		if err != nil {
			_, _ = fmt.Fprintf(c.writer(), "activity failed: %v\n", err)
		}
		return
	}
	stamp := shortDuration(elapsed) + fmt.Sprintf(" · %d tool call(s)", calls)
	switch {
	case err == nil:
		c.style.Println("  " + c.style.Green("✓") + c.style.Dim(" "+stamp))
		if calls == 0 {
			// A round with no tool calls cannot have produced evidence. It is
			// almost always the model channel, not the target. The nastiest
			// case is a base_url missing its API prefix: an OpenAI-compatible
			// gateway answers the wrong path with an HTML landing page and a
			// 200, so the harness sees a perfectly successful empty turn.
			c.style.Println("    " + c.style.Dim("no tool calls — check the model channel (quota, model id, base_url)"))
			c.style.Println("    " + c.style.Dim("base_url needs the API prefix (e.g. https://host/v1): without it many gateways answer with HTML + 200 and every round comes back empty"))
		}
	case isAborted(err):
		c.style.Println("  " + c.style.Dim("■ aborted by operator · "+stamp))
	default:
		c.style.Println("  " + c.style.Yellow("!") + " " + c.style.Dim(stamp+" · "+oneLine(err.Error())))
	}
}

// chatBanner introduces an interactive session. The session id is the thing a
// user needs to resume the conversation later, so it is printed up front.
func (c *console) chatBanner(target, objective, model, sessionID, sessionDir string, tools int) {
	if c == nil || !c.rich {
		return
	}
	c.style.Println(c.style.Cyan("Provena chat") + c.style.Dim("  ·  interactive session"))
	if strings.TrimSpace(target) != "" {
		c.style.Println(c.style.Dim("  target     ") + target)
	}
	if strings.TrimSpace(objective) != "" {
		c.style.Println(c.style.Dim("  objective  ") + objective)
	}
	c.style.Println(c.style.Dim("  model      ") + model)
	c.style.Println(c.style.Dim("  session    ") + sessionID)
	c.style.Println(c.style.Dim("  state      ") + sessionDir)
	c.style.Println(c.style.Dim(fmt.Sprintf("  tools      %d", tools)))
	c.style.Println(c.style.Dim("  input      /help for commands, Ctrl+C to interrupt, /exit to quit"))
}

// turnStart marks the beginning of one user turn.
func (c *console) turnStart(turn int) {
	if c == nil || !c.rich {
		return
	}
	c.endStream()
	c.style.BlankLine()
	c.style.Println(c.style.Cyan("▌") + c.style.Bold(fmt.Sprintf(" turn %d", turn)))
}

// turnEnd reports how a turn finished. A turn with no tool calls produced no new
// evidence, which is almost always the model channel rather than the target.
func (c *console) turnEnd(calls int, elapsed time.Duration, err error) {
	if c == nil {
		return
	}
	c.endStream()
	if !c.rich {
		if err != nil {
			_, _ = fmt.Fprintf(c.writer(), "turn failed: %v\n", err)
		}
		return
	}
	stamp := shortDuration(elapsed) + fmt.Sprintf(" · %d tool call(s)", calls)
	switch {
	case err == nil:
		c.style.Println("  " + c.style.Green("✓") + c.style.Dim(" "+stamp))
		if calls == 0 {
			c.style.Println("    " + c.style.Dim("no tool calls — if the model answered from memory only, that is expected; otherwise check the model channel"))
		}
	case isAborted(err):
		c.style.Println("  " + c.style.Dim("■ interrupted · "+stamp))
	default:
		c.style.Println("  " + c.style.Yellow("!") + " " + c.style.Dim(stamp+" · "+oneLine(err.Error())))
	}
}

// prompt writes the input marker. It is suppressed in plain mode so piped input
// produces clean, greppable output.
func (c *console) prompt() {
	if c == nil || !c.rich {
		return
	}
	c.endStream()
	c.style.Printf("%s ", c.style.Cyan("›"))
}

// hint prints a local (not model-generated) remark.
func (c *console) hint(text string) {
	if c == nil || strings.TrimSpace(text) == "" {
		return
	}
	c.endStream()
	if !c.rich {
		return
	}
	c.style.Println("  " + c.style.Dim("· "+text))
}

// chatHelp lists the local commands. Keeping them in the renderer means --plain
// can stay silent while rich mode can be helpful.
func (c *console) chatHelp() {
	if c == nil || !c.rich {
		return
	}
	c.endStream()
	rows := [][2]string{
		{"/new", "clear the conversation and start a fresh session (new FGS graph)"},
		{"/graph", "print the current FGS graph"},
		{"/info", "show session id, state directory and message count"},
		{"/help", "show this list"},
		{"/exit", "leave the session (also Ctrl+D)"},
	}
	c.style.BlankLine()
	for _, row := range rows {
		c.style.Println("  " + c.style.Cyan(padRight(row[0], 8)) + c.style.Dim(row[1]))
	}
	c.style.BlankLine()
}

// event routes one Pi event into the console.
func (c *console) event(ev piagent.Event) {
	if c == nil {
		return
	}
	switch ev.Type {
	case "message_update":
		c.messageUpdate(ev.Raw)
	case "message_end":
		c.messageEnd(ev.Raw)
	case "tool_execution_start":
		c.endStream()
		c.toolStart(rawString(ev.Raw, "toolName", "name"), rawMap(ev.Raw, "args"))
	case "tool_execution_end":
		c.toolEnd(rawString(ev.Raw, "toolName", "name"), ev.Raw)
	case "compaction_start":
		c.notice("compaction started")
	case "compaction_end":
		c.notice("compaction finished")
	case "auto_retry_start":
		c.notice("model call failed, retrying")
	case "extension_error":
		detail := rawString(ev.Raw, "message", "error")
		c.notice("extension error: " + oneLine(detail))
	case "agent_end":
		c.endStream()
	}
}

func (c *console) messageUpdate(raw map[string]interface{}) {
	delta := rawMap(raw, "assistantMessageEvent")
	if delta == nil {
		return
	}
	text := rawValueString(delta, "delta")
	switch rawString(delta, "type") {
	case "thinking_delta":
		if c.thinking {
			c.stream(thinkingGutter, text)
		}
	case "text_delta":
		c.stream(textGutter, text)
	case "thinking_end", "text_end":
		c.endStream()
	case "error":
		// An upstream model failure ends the turn quietly: Pi reports it here,
		// and the activity otherwise looks exactly like one that simply had
		// nothing to do. Surfacing it is the difference between "why is the
		// graph empty" and "the account is out of balance".
		reason := rawString(delta, "reason")
		detail := oneLine(rawValueString(delta, "error", "message", "content", "partial"))
		line := "model error"
		if reason != "" {
			line += " (" + reason + ")"
		}
		if detail != "" {
			line += ": " + truncateRunes(detail, 200)
		}
		c.notice(line)
	}
}

// messageEnd surfaces a failed assistant message.
//
// An upstream model failure (quota exhausted, bad model id, expired key) does
// not arrive as a streaming error delta: Pi ends the turn with an empty content
// array plus `stopReason: "error"` and `errorMessage`. Without this the activity
// looks like one that simply had nothing to do, and the real cause is invisible.
func (c *console) messageEnd(raw map[string]interface{}) {
	if c == nil {
		return
	}
	message := rawMap(raw, "message")
	if message == nil {
		return
	}
	if reason := rawString(message, "stopReason"); reason != "" && reason != "error" {
		return
	}
	detail := oneLine(rawString(message, "errorMessage"))
	if detail == "" || detail == c.lastModelError {
		return
	}
	c.lastModelError = detail
	c.notice("model error: " + truncateRunes(detail, 200))
}

// stream writes chunk under the active gutter, re-emitting the gutter after
// every newline so multi-line output stays visually attributed. Chunks are
// batched into a single write: model deltas arrive token by token.
func (c *console) stream(gutter, chunk string) {
	if c == nil || !c.rich || chunk == "" {
		return
	}
	if c.streamGutter != gutter {
		c.endStream()
		c.streamGutter = gutter
	}
	text := strings.ReplaceAll(chunk, "\r\n", "\n")
	prefix := gutter
	if gutter == thinkingGutter {
		prefix = c.style.Dim(gutter)
	}
	var b strings.Builder
	for _, r := range text {
		if c.atLineStart && r != '\n' {
			b.WriteString(prefix)
			c.atLineStart = false
		}
		b.WriteRune(r)
		if r == '\n' {
			c.atLineStart = true
		}
	}
	fmt.Fprint(c.out, b.String())
}

// answer prints the assistant's reply in plain mode, where streamed deltas are
// suppressed. Rich mode has already rendered the text token by token, so this
// only exists so a piped session still shows what the model said.
func (c *console) answer(text string) {
	if c == nil || c.rich || strings.TrimSpace(text) == "" {
		return
	}
	_, _ = fmt.Fprintln(c.writer(), text)
}

// endStream closes an open streamed block so the next output starts on its own
// line.
func (c *console) endStream() {
	if c == nil || c.streamGutter == "" {
		return
	}
	if !c.atLineStart {
		fmt.Fprint(c.out, "\n")
		c.atLineStart = true
	}
	c.streamGutter = ""
}

func (c *console) toolStart(name string, args map[string]interface{}) {
	if c == nil {
		return
	}
	if !c.rich {
		if name != "" {
			_, _ = fmt.Fprintf(c.writer(), "  -> %s\n", name)
		}
		return
	}
	line := toolGutter + c.style.Bold(name)
	if summary := toolArgSummary(args); summary != "" {
		line += c.style.Dim("  " + summary)
	}
	c.style.Println(line)
}

func (c *console) toolEnd(name string, raw map[string]interface{}) {
	if c == nil {
		return
	}
	result := rawValueString(raw, "result", "output", "content")
	isError, _ := raw["isError"].(bool)
	if !c.rich {
		if c.verbose {
			_, _ = fmt.Fprintf(c.writer(), "  <- %s\n", truncateRunes(result, 400))
		}
		return
	}
	if isError {
		c.style.Println(resultGutter + c.style.Red("failed") + c.style.Dim("  "+truncateRunes(oneLine(result), 150)))
		return
	}
	if c.verbose {
		if text := oneLine(result); text != "" {
			c.style.Println(resultGutter + c.style.Dim(truncateRunes(text, 220)))
		}
	}
}

func (c *console) notice(message string) {
	if c == nil || message == "" {
		return
	}
	c.endStream()
	if !c.rich {
		return
	}
	c.style.Println("  " + c.style.Yellow("·") + c.style.Dim(" "+message))
}

// isGraphMutation reports whether a tool can change the graph, which is what
// live graph output keys off.
func isGraphMutation(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "submit_fact", "submit_finding", "fgs_apply":
		return true
	}
	return false
}

// graph prints a readable rendering of the FGS graph. trigger says what caused
// the print so --graph can filter.
func (c *console) graph(snapshot fgs.Snapshot, trigger string) {
	if c == nil {
		return
	}
	switch c.graphMode {
	case graphOff:
		return
	case graphLive:
	case graphActivity:
		if trigger == triggerMutation {
			return
		}
	}
	c.endStream()
	if !c.rich {
		c.graphPlain(snapshot, trigger)
		return
	}

	c.style.Println("")
	c.style.Println(c.style.Cyan("╭─ FGS ") + c.style.Dim(fmt.Sprintf("v%d · %d node(s) · %d edge(s) · %s",
		snapshot.Version, len(snapshot.Nodes), len(snapshot.Edges), trigger)))
	lines := graphLines(snapshot)
	if len(lines) == 0 {
		// A fresh graph only carries its structural origin/goal nodes, which are
		// filtered out. Saying so beats an empty box next to a non-zero count.
		lines = []string{c.style.Dim("(no recorded evidence yet)")}
	}
	for _, line := range lines {
		c.style.Println(c.style.Cyan("│ ") + line)
	}
	c.style.Println(c.style.Cyan("╰─"))
}

// graphPlain keeps a machine-greppable form for pipes and logs.
func (c *console) graphPlain(snapshot fgs.Snapshot, trigger string) {
	out := c.writer()
	_, _ = fmt.Fprintf(out, "graph v%d nodes=%d edges=%d trigger=%s\n", snapshot.Version, len(snapshot.Nodes), len(snapshot.Edges), trigger)
	for _, node := range orderedNodes(snapshot) {
		_, _ = fmt.Fprintf(out, "  [%s] %s %s\n", node.Status, node.ID, oneLine(node.Label))
	}
}

func graphLines(snapshot fgs.Snapshot) []string {
	nodes := orderedNodes(snapshot)
	lines := make([]string, 0, len(nodes)+1)
	shown := nodes
	if len(nodes) > graphMaxNodes {
		shown = nodes[:graphMaxNodes]
	}
	for _, node := range shown {
		id := padRight(truncateRunes(node.ID, 16), 17)
		label := truncateRunes(oneLine(node.Label), 52)
		if label == "" {
			label = truncateRunes(oneLine(node.Content), 52)
		}
		lines = append(lines, fmt.Sprintf("%s %s %s", nodeMarker(node), id, label))
	}
	if len(nodes) > len(shown) {
		lines = append(lines, fmt.Sprintf("… +%d more node(s)", len(nodes)-len(shown)))
	}
	return lines
}

// orderedNodes sorts goal first, then live steps, findings and facts, so the
// interesting rows sit near the top.
func orderedNodes(snapshot fgs.Snapshot) []fgs.Node {
	nodes := make([]fgs.Node, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		switch node.Kind {
		case "origin", "goal", "intent", "hint":
			continue
		default:
			nodes = append(nodes, node)
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		ri, rj := kindRank(nodes[i]), kindRank(nodes[j])
		if ri != rj {
			return ri < rj
		}
		si, sj := statusRank(nodes[i]), statusRank(nodes[j])
		if si != sj {
			return si < sj
		}
		if !nodes[i].CreatedAt.Equal(nodes[j].CreatedAt) {
			return nodes[i].CreatedAt.Before(nodes[j].CreatedAt)
		}
		return nodes[i].ID < nodes[j].ID
	})
	return nodes
}

func kindRank(node fgs.Node) int {
	switch node.Kind {
	case fgs.KindSubGoal:
		return 0
	case fgs.KindStep:
		return 1
	case fgs.KindFinding:
		return 2
	case fgs.KindFact:
		return 3
	default:
		return 4
	}
}

func statusRank(node fgs.Node) int {
	switch node.Status {
	case fgs.StatusActive:
		return 0
	case fgs.StatusBlocked:
		return 1
	case fgs.StatusPending:
		return 2
	case fgs.StatusConfirmed:
		return 3
	case fgs.StatusCompleted:
		return 4
	default:
		return 5
	}
}

func nodeMarker(node fgs.Node) string {
	switch node.Kind {
	case fgs.KindSubGoal:
		return "›"
	case fgs.KindFinding:
		return "⚑"
	case fgs.KindFact:
		return "◆"
	}
	switch node.Status {
	case fgs.StatusActive:
		return "●"
	case fgs.StatusCompleted, fgs.StatusConfirmed:
		return "✓"
	case fgs.StatusBlocked:
		return "!"
	case fgs.StatusAbandoned:
		return "✗"
	default:
		return "○"
	}
}

// toolArgSummary picks the most telling argument for one tool call.
func toolArgSummary(args map[string]interface{}) string {
	if len(args) == 0 {
		return ""
	}
	for _, key := range []string{"url", "command", "target", "script", "path", "query", "id"} {
		value, ok := args[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		return key + "=" + truncateRunes(oneLine(value), 92)
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return truncateRunes(strings.Join(keys, ","), 60)
}

func rawMap(raw map[string]interface{}, key string) map[string]interface{} {
	if raw == nil {
		return nil
	}
	if value, ok := raw[key].(map[string]interface{}); ok {
		return value
	}
	return nil
}

func oneLine(value string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(value), " "))
}

func padRight(value string, width int) string {
	if pad := width - len([]rune(value)); pad > 0 {
		return value + strings.Repeat(" ", pad)
	}
	return value
}

func shortDuration(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}
