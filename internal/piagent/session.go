package piagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// ErrSessionClosed is returned when the underlying Pi process is gone.
var ErrSessionClosed = errors.New("pi session is closed")

// Session is a long-lived `pi --mode rpc` process that keeps one conversation in
// memory across prompts. That is what separates an interactive chat from the
// one-shot Run: Run starts a process per prompt, a Session keeps the same
// process — and therefore the same message history — until Close.
//
// A single reader goroutine owns stdout and feeds events over a channel, so a
// turn can be abandoned (ctx cancellation) without killing the conversation, and
// Abort can be issued from another goroutine while a turn is streaming.
type Session struct {
	proc   *piProcess
	events chan Event
	ctx    context.Context

	writeMu sync.Mutex
	seq     int

	// readErr is written by the reader goroutine before it closes events, so
	// reading it is only valid after the channel reports closed.
	readErr error
}

// OpenSession launches Pi and keeps it alive for as many prompts as the caller
// needs.
func OpenSession(ctx context.Context, cfg Config) (*Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	l, err := prepareLaunch(cfg)
	if err != nil {
		return nil, err
	}
	proc, err := startPiProcess(ctx, l)
	if err != nil {
		l.cleanup()
		return nil, err
	}
	s := &Session{
		proc:   proc,
		events: make(chan Event, 256),
		ctx:    ctx,
	}
	go s.readLoop(l)
	return s, nil
}

func (s *Session) readLoop(l *launch) {
	defer close(s.events)
	debugPi := os.Getenv("PROVENA_PI_DEBUG") != ""
	census := map[string]int{}
	deltaCensus := map[string]int{}
	unparsed := 0
	reader := bufio.NewReader(s.proc.stdout)
	for {
		lineBytes, err := reader.ReadBytes('\n')
		if err != nil && len(lineBytes) == 0 {
			if !errors.Is(err, io.EOF) && !s.proc.isAbort(err) {
				s.readErr = fmt.Errorf("读取 Pi RPC 输出失败: %w", err)
			}
			break
		}
		line := strings.TrimSpace(string(lineBytes))
		if line == "" {
			if err != nil {
				break
			}
			continue
		}
		var raw map[string]interface{}
		if jsonErr := json.Unmarshal([]byte(line), &raw); jsonErr != nil {
			unparsed++
			if debugPi {
				fmt.Fprintf(os.Stderr, "[pi-debug] unparsed: %s\n", truncateForError(line))
			}
			continue
		}
		ev := Event{Raw: raw}
		if v, ok := raw["type"].(string); ok {
			ev.Type = v
		}
		if debugPi {
			census[ev.Type]++
			if ev.Type == "message_update" {
				if delta, ok := raw["assistantMessageEvent"].(map[string]interface{}); ok {
					if kind, ok := delta["type"].(string); ok {
						deltaCensus[kind]++
					}
				}
			}
		}
		select {
		case s.events <- ev:
		case <-s.ctx.Done():
			return
		}
		if err != nil {
			break
		}
	}
	if debugPi {
		fmt.Fprintf(os.Stderr, "[pi-debug] session ended command=%s\nevents=%v\ndeltas=%v unparsed=%d\nstderr=%q\n",
			l.commandLine(), census, deltaCensus, unparsed, s.proc.stderrText())
	}
}

func (s *Session) nextID() string {
	s.seq++
	return fmt.Sprintf("provena-%d", s.seq)
}

func (s *Session) send(payload map[string]interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.proc.writeLine(data)
}

// Prompt sends one user message and streams events until the turn finishes.
// Cancelling ctx abandons the wait without killing Pi; use Abort to actually
// stop the model.
func (s *Session) Prompt(ctx context.Context, message string, onEvent func(Event)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	id := s.nextID()
	if err := s.send(map[string]interface{}{"id": id, "type": "prompt", "message": message}); err != nil {
		return fmt.Errorf("发送 Pi prompt 失败: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-s.events:
			if !ok {
				return s.closedErr()
			}
			if ev.Type == "response" && matchesResponse(ev.Raw, "prompt", id) {
				if success, isBool := ev.Raw["success"].(bool); isBool && !success {
					return fmt.Errorf("Pi 拒绝了该 prompt: %s", piErrorText(ev.Raw))
				}
			}
			if onEvent != nil {
				onEvent(ev)
			}
			if ev.Type == "agent_end" {
				// agent_end is emitted last, but a couple of bookkeeping events
				// can already be queued behind it. Forward them so a caller's
				// rendering stays complete, without waiting for more.
				s.drain(onEvent)
				return nil
			}
		}
	}
}

// drain forwards everything already queued and returns immediately.
func (s *Session) drain(onEvent func(Event)) {
	for {
		select {
		case ev, ok := <-s.events:
			if !ok {
				return
			}
			if onEvent != nil {
				onEvent(ev)
			}
		default:
			return
		}
	}
}

// Abort stops the in-flight turn. The conversation itself survives, which is the
// whole point of interrupting instead of restarting.
func (s *Session) Abort() error {
	if err := s.send(map[string]interface{}{"id": s.nextID(), "type": "abort"}); err != nil {
		return fmt.Errorf("中止当前轮失败: %w", err)
	}
	return nil
}

// NewSession clears the conversation in place: same process, same tools, empty
// history.
func (s *Session) NewSession(ctx context.Context) error {
	_, err := s.request(ctx, "new_session", nil, nil)
	return err
}

// State returns Pi's session state (session file, id, message count, model).
func (s *Session) State(ctx context.Context) (map[string]interface{}, error) {
	return s.request(ctx, "get_state", nil, nil)
}

// request sends a command and waits for its matching response.
func (s *Session) request(ctx context.Context, command string, extra map[string]interface{}, onEvent func(Event)) (map[string]interface{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	id := s.nextID()
	payload := map[string]interface{}{"id": id, "type": command}
	for key, value := range extra {
		payload[key] = value
	}
	if err := s.send(payload); err != nil {
		return nil, fmt.Errorf("向 Pi 发送 %s 失败: %w", command, err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case ev, ok := <-s.events:
			if !ok {
				return nil, s.closedErr()
			}
			if ev.Type == "response" && matchesResponse(ev.Raw, command, id) {
				data, _ := ev.Raw["data"].(map[string]interface{})
				if success, isBool := ev.Raw["success"].(bool); isBool && !success {
					return data, fmt.Errorf("Pi 命令 %s 失败: %s", command, piErrorText(ev.Raw))
				}
				return data, nil
			}
			if onEvent != nil {
				onEvent(ev)
			}
		}
	}
}

func (s *Session) closedErr() error {
	if s.readErr != nil {
		return s.readErr
	}
	if detail := s.proc.stderrText(); detail != "" {
		return fmt.Errorf("%w: %s", ErrSessionClosed, truncateForError(detail))
	}
	return ErrSessionClosed
}

// Close reaps the process tree and releases the temporary agent directory.
func (s *Session) Close() {
	if s == nil || s.proc == nil {
		return
	}
	s.proc.stop()
	if s.proc.launch != nil {
		s.proc.launch.cleanup()
	}
}

// matchesResponse accepts either an id match or, when Pi omits the id, a command
// match. Both are used so a response is never mistaken for another command's.
func matchesResponse(raw map[string]interface{}, command, id string) bool {
	if got, _ := raw["command"].(string); got != command {
		return false
	}
	if got, ok := raw["id"].(string); ok && got != "" && id != "" {
		return got == id
	}
	return true
}
