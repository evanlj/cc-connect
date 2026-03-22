package codex

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/chenhg5/cc-connect/core"
)

const (
	defaultCodexIdleTimeout     = 180 * time.Second
	defaultCodexToolIdleTimeout = 1200 * time.Second // tool/command phase can be quiet for much longer
	defaultCodexMaxStdoutToken  = 32 * 1024 * 1024   // 32MB per line (Codex CLI stdout JSONL)
)

var codexTurnSeq atomic.Uint64

// codexSession manages a multi-turn Codex conversation.
// First Send() uses `codex exec`, subsequent ones use `codex exec resume <threadID>`.
type codexSession struct {
	workDir    string
	model      string
	mode       string
	extraEnv   []string
	configArgs []string
	events     chan core.Event
	threadID   atomic.Value // stores string — Codex thread_id
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	alive      atomic.Bool
}

// turnState holds per-`Send()` state for a single Codex CLI invocation ("one turn").
// It must NOT be stored on codexSession globally, otherwise multiple turns could race
// and old watchdog goroutines could observe new-turn state.
type turnState struct {
	lastOutputAt atomic.Int64
	toolActive   atomic.Bool
	timedOut     atomic.Bool
	completed    atomic.Bool

	traceMu     sync.Mutex
	traceFile   *os.File
	traceWriter *bufio.Writer
	tracePath   string
	traceClosed atomic.Bool

	// done is closed when the Codex subprocess has fully exited (stdout reader ended + Wait returned).
	done chan struct{}
	// turnDone is closed when Codex reports "turn.completed". The subprocess might still be exiting,
	// but we must stop idle detection immediately to avoid falsely triggering auto-resume after
	// a successful turn.
	turnDone chan struct{}
}

func newCodexSession(ctx context.Context, workDir, model, mode, resumeID string, extraEnv []string, configArgs []string) (*codexSession, error) {
	sessionCtx, cancel := context.WithCancel(ctx)

	cs := &codexSession{
		workDir:    workDir,
		model:      model,
		mode:       mode,
		extraEnv:   extraEnv,
		configArgs: configArgs,
		events:     make(chan core.Event, 64),
		ctx:        sessionCtx,
		cancel:     cancel,
	}
	cs.alive.Store(true)

	if resumeID != "" {
		cs.threadID.Store(resumeID)
	}

	return cs, nil
}

func (ts *turnState) traceWriteJSON(v any) {
	if ts == nil {
		return
	}
	ts.traceMu.Lock()
	defer ts.traceMu.Unlock()
	if ts.traceWriter == nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = ts.traceWriter.Write(b)
	_, _ = ts.traceWriter.WriteString("\n")
	_ = ts.traceWriter.Flush()
}

func (ts *turnState) traceWriteRawLine(line string) {
	if ts == nil {
		return
	}
	ts.traceMu.Lock()
	defer ts.traceMu.Unlock()
	if ts.traceWriter == nil {
		return
	}
	_, _ = ts.traceWriter.WriteString(line)
	if !strings.HasSuffix(line, "\n") {
		_, _ = ts.traceWriter.WriteString("\n")
	}
	_ = ts.traceWriter.Flush()
}

func (ts *turnState) closeTrace() {
	if ts == nil {
		return
	}
	if !ts.traceClosed.CompareAndSwap(false, true) {
		return
	}
	ts.traceMu.Lock()
	defer ts.traceMu.Unlock()
	if ts.traceWriter != nil {
		_ = ts.traceWriter.Flush()
	}
	if ts.traceFile != nil {
		_ = ts.traceFile.Close()
	}
	ts.traceWriter = nil
	ts.traceFile = nil
}

func extractEnv(extraEnv []string, key string) string {
	prefix := key + "="
	for _, kv := range extraEnv {
		if strings.HasPrefix(kv, prefix) {
			return strings.TrimPrefix(kv, prefix)
		}
	}
	return ""
}

func promptHash(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	// Short hash is enough for trace indexing.
	return hex.EncodeToString(sum[:8])
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "..."
}

func (cs *codexSession) openTurnTrace(ts *turnState, prompt string, isResume bool, resumeID string) {
	if cs == nil || ts == nil {
		return
	}
	// We keep traces under <work_dir>/data/traces/codex/YYYY-MM-DD/ so that each instance
	// (instance-a/b/c) naturally isolates its trace files.
	traceDir := filepath.Join(cs.workDir, "data", "traces", "codex", time.Now().Format("2006-01-02"))
	if err := os.MkdirAll(traceDir, 0o755); err != nil {
		slog.Warn("codexSession: failed to create trace dir", "dir", traceDir, "error", err)
		return
	}

	seq := codexTurnSeq.Add(1)
	fileName := fmt.Sprintf("%s_turn_%d.jsonl", time.Now().Format("150405.000000000"), seq)
	path := filepath.Join(traceDir, fileName)
	promptLogPath := filepath.Join(traceDir, "prompts-full.jsonl")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		slog.Warn("codexSession: failed to open trace file", "path", path, "error", err)
		return
	}

	ts.traceMu.Lock()
	ts.traceFile = f
	ts.traceWriter = bufio.NewWriterSize(f, 256*1024)
	ts.tracePath = path
	ts.traceMu.Unlock()

	meta := map[string]any{
		"type":             "turn_meta",
		"ts":               time.Now().Format(time.RFC3339Nano),
		"agent":            "codex",
		"work_dir":         cs.workDir,
		"model":            cs.model,
		"mode":             cs.mode,
		"resume":           isResume,
		"resume_thread_id": resumeID,
		"prompt_len":       len(prompt),
		"prompt_sha":       promptHash(prompt),
		"prompt_preview":   truncateRunes(prompt, 8000),
		"cc_project":       extractEnv(cs.extraEnv, "CC_PROJECT"),
		"cc_session_key":   extractEnv(cs.extraEnv, "CC_SESSION_KEY"),
		"trace_path":       path,
		"prompt_log_path":  promptLogPath,
	}
	ts.traceWriteJSON(meta)
	ts.traceWriteJSON(map[string]any{
		"type":       "prompt_full",
		"ts":         time.Now().Format(time.RFC3339Nano),
		"prompt":     prompt,
		"prompt_len": len(prompt),
		"prompt_sha": promptHash(prompt),
	})
	if err := appendPromptFullLog(promptLogPath, map[string]any{
		"type":             "prompt_full",
		"ts":               time.Now().Format(time.RFC3339Nano),
		"agent":            "codex",
		"work_dir":         cs.workDir,
		"model":            cs.model,
		"mode":             cs.mode,
		"resume":           isResume,
		"resume_thread_id": resumeID,
		"prompt":           prompt,
		"prompt_len":       len(prompt),
		"prompt_sha":       promptHash(prompt),
		"trace_path":       path,
		"cc_project":       extractEnv(cs.extraEnv, "CC_PROJECT"),
		"cc_session_key":   extractEnv(cs.extraEnv, "CC_SESSION_KEY"),
	}); err != nil {
		slog.Warn("codexSession: failed to append full prompt log", "path", promptLogPath, "error", err)
	}
	slog.Info("codexSession: turn trace enabled", "path", path)
}

func appendPromptFullLog(path string, payload map[string]any) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("path is empty")
	}
	if payload == nil {
		return fmt.Errorf("payload is nil")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

// Send launches a codex subprocess.
// If a threadID exists (from a prior turn or resume), uses `codex exec resume <id> -`
// and streams prompt from stdin.
// Otherwise uses `codex exec --cd <workDir> -` and streams prompt from stdin.
//
// Why stdin:
// On Windows, npm's codex.cmd shim forwards `%*` through cmd.exe. Complex prompts with
// JSON/quotes/pipes (e.g. PASS|REWORK) can be misparsed as shell operators and fail with
// "The filename, directory name, or volume label syntax is incorrect."
// Passing "-" and writing prompt to stdin avoids command-line parsing pitfalls.
func (cs *codexSession) Send(prompt string, images []core.ImageAttachment) error {
	if len(images) > 0 {
		slog.Warn("codexSession: images not supported by Codex, ignoring")
	}
	if !cs.alive.Load() {
		return fmt.Errorf("session is closed")
	}

	tid := cs.CurrentSessionID()
	isResume := tid != ""

	args := cs.buildSendArgs(isResume, tid)

	slog.Debug("codexSession: launching", "resume", isResume, "args", args)

	// IMPORTANT:
	// - cs.ctx is a long-lived session context.
	// - Each Send() must have its own cancel scope, so that we can abort a single turn
	//   (idle timeout / stalled CLI) without permanently killing the whole session.
	turnCtx, turnCancel := context.WithCancel(cs.ctx)
	cmd := exec.CommandContext(turnCtx, "codex", args...)
	cmd.Dir = cs.workDir
	cmd.Stdin = strings.NewReader(prompt)
	if len(cs.extraEnv) > 0 {
		cmd.Env = append(os.Environ(), cs.extraEnv...)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("codexSession: stdout pipe: %w", err)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("codexSession: start: %w", err)
	}

	ts := &turnState{
		done:     make(chan struct{}),
		turnDone: make(chan struct{}),
	}
	ts.lastOutputAt.Store(time.Now().UnixNano())
	cs.openTurnTrace(ts, prompt, isResume, tid)

	cs.wg.Add(1)
	go cs.readLoop(cmd, stdout, &stderrBuf, ts)

	// Watch for "no output" stalls (cc-connect <-> codex CLI channel).
	cs.wg.Add(1)
	go cs.watchIdle(
		turnCtx, turnCancel,
		ts,
		defaultCodexIdleTimeout,
		defaultCodexToolIdleTimeout,
	)

	return nil
}

func (cs *codexSession) buildSendArgs(isResume bool, tid string) []string {
	var args []string
	if isResume {
		args = []string{"exec", "resume", "--json", "--skip-git-repo-check"}
	} else {
		args = []string{"exec", "--json", "--skip-git-repo-check"}
	}

	switch cs.mode {
	case "auto-edit", "full-auto":
		args = append(args, "--full-auto")
	case "yolo":
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	if len(cs.configArgs) > 0 {
		args = append(args, cs.configArgs...)
	}

	if cs.model != "" {
		args = append(args, "--model", cs.model)
	}

	if isResume {
		args = append(args, tid, "-")
	} else {
		args = append(args, "--cd", cs.workDir, "-")
	}
	return args
}

func (cs *codexSession) readLoop(
	cmd *exec.Cmd,
	stdout io.ReadCloser,
	stderrBuf *bytes.Buffer,
	ts *turnState,
) {
	defer cs.wg.Done()
	defer func() {
		if ts != nil {
			ts.closeTrace()
		}
	}()
	defer func() {
		// Ensure watcher stops after the process is fully waited.
		// (The idle watchdog uses ts.done when it has already decided to abort a turn.)
		if ts != nil && ts.done != nil {
			close(ts.done)
		}
	}()
	defer func() {
		if err := cmd.Wait(); err != nil {
			stderrMsg := strings.TrimSpace(stderrBuf.String())
			if stderrMsg != "" {
				slog.Error("codexSession: process failed", "error", err, "stderr", stderrMsg)
				cs.events <- core.Event{Type: core.EventError, Error: fmt.Errorf("%s", stderrMsg)}
			}
		}
	}()

	scanner := bufio.NewScanner(stdout)
	// Codex CLI streams JSONL. Some events (e.g. command_execution aggregated_output)
	// can be very large and exceed bufio.Scanner's default token limit.
	// We raise the max token size to avoid "token too long" crashes.
	scanner.Buffer(make([]byte, 256*1024), defaultCodexMaxStdoutToken)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		// Any stdout line counts as "activity" for stall detection.
		if ts != nil {
			ts.lastOutputAt.Store(time.Now().UnixNano())
		}

		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			slog.Debug("codexSession: non-JSON line", "line", line)
			if ts != nil {
				ts.traceWriteJSON(map[string]any{
					"type": "stdout_nonjson",
					"ts":   time.Now().Format(time.RFC3339Nano),
					"line": truncateRunes(line, 4000),
				})
			}
			continue
		}
		if ts != nil {
			// Preserve original Codex stdout JSON line for maximal fidelity.
			ts.traceWriteRawLine(line)
		}

		cs.handleEvent(raw, ts)
	}

	if err := scanner.Err(); err != nil {
		// If we aborted this turn due to idle timeout, suppress the follow-up read error
		// to avoid double error events.
		if ts != nil && ts.timedOut.Load() {
			slog.Warn("codexSession: stdout reader stopped after idle timeout", "error", err)
			return
		}
		slog.Error("codexSession: scanner error", "error", err)
		cs.events <- core.Event{Type: core.EventError, Error: fmt.Errorf("read stdout: %w", err)}
	}
}

func (cs *codexSession) handleEvent(raw map[string]any, ts *turnState) {
	eventType, _ := raw["type"].(string)

	switch eventType {
	case "thread.started":
		if tid, ok := raw["thread_id"].(string); ok {
			cs.threadID.Store(tid)
			slog.Debug("codexSession: thread started", "thread_id", tid)
			if ts != nil {
				ts.traceWriteJSON(map[string]any{
					"type":      "thread_id",
					"ts":        time.Now().Format(time.RFC3339Nano),
					"thread_id": tid,
				})
			}
			// Persist session ID as early as possible.
			// Engine will record session.AgentSessionID when it sees EventText with SessionID,
			// even if Content is empty.
			cs.events <- core.Event{
				Type:      core.EventText,
				SessionID: tid,
			}
		}

	case "turn.started":
		slog.Debug("codexSession: turn started")

	case "item.started":
		cs.handleItemStarted(raw, ts)

	case "item.completed":
		cs.handleItemCompleted(raw, ts)

	case "turn.completed":
		if ts != nil && ts.completed.CompareAndSwap(false, true) {
			// Stop idle detection immediately. Even if the Codex subprocess exits slowly,
			// we must NOT auto-resume a successful turn.
			close(ts.turnDone)
			ts.traceWriteJSON(map[string]any{
				"type":   "turn_end",
				"ts":     time.Now().Format(time.RFC3339Nano),
				"status": "completed",
			})
		}
		cs.events <- core.Event{
			Type:      core.EventResult,
			SessionID: cs.CurrentSessionID(),
			Done:      true,
		}

	case "error":
		msg, _ := raw["message"].(string)
		if strings.Contains(msg, "Reconnecting") || strings.Contains(msg, "Falling back") {
			slog.Debug("codexSession: transient error", "message", msg)
		} else {
			slog.Warn("codexSession: error event", "message", msg)
		}
	}
}

func (cs *codexSession) handleItemStarted(raw map[string]any, ts *turnState) {
	item, ok := raw["item"].(map[string]any)
	if !ok {
		return
	}
	itemType, _ := item["type"].(string)

	if itemType == "command_execution" {
		if ts != nil {
			ts.toolActive.Store(true)
		}
		command, _ := item["command"].(string)
		cs.events <- core.Event{
			Type:      core.EventToolUse,
			ToolName:  "Bash",
			ToolInput: truncate(command, 200),
		}
	}
}

func (cs *codexSession) handleItemCompleted(raw map[string]any, ts *turnState) {
	item, ok := raw["item"].(map[string]any)
	if !ok {
		return
	}
	itemType, _ := item["type"].(string)

	switch itemType {
	case "reasoning":
		text, _ := item["text"].(string)
		if text != "" {
			cs.events <- core.Event{
				Type:    core.EventThinking,
				Content: text,
			}
		}

	case "agent_message":
		text, _ := item["text"].(string)
		if text != "" {
			cs.events <- core.Event{
				Type:    core.EventText,
				Content: text,
			}
		}

	case "command_execution":
		if ts != nil {
			ts.toolActive.Store(false)
		}
		command, _ := item["command"].(string)
		status, _ := item["status"].(string)
		output, _ := item["aggregated_output"].(string)
		exitCode, _ := item["exit_code"].(float64)

		slog.Debug("codexSession: command completed",
			"command", truncate(command, 100),
			"status", status,
			"exit_code", int(exitCode),
			"output_len", len(output),
		)

	case "error":
		msg, _ := item["message"].(string)
		if msg != "" && !strings.Contains(msg, "Falling back") {
			slog.Warn("codexSession: item error", "message", msg)
		}
	}
}

func (cs *codexSession) watchIdle(
	turnCtx context.Context,
	turnCancel context.CancelFunc,
	ts *turnState,
	idleTimeout time.Duration,
	toolIdleTimeout time.Duration,
) {
	defer cs.wg.Done()

	if idleTimeout <= 0 && toolIdleTimeout <= 0 {
		return
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ts.done:
			return
		case <-ts.turnDone:
			return
		case <-cs.ctx.Done():
			return
		case <-turnCtx.Done():
			return
		case <-ticker.C:
			if ts.completed.Load() {
				return
			}
			last := time.Unix(0, ts.lastOutputAt.Load())
			effectiveTimeout := idleTimeout
			if ts.toolActive.Load() && toolIdleTimeout > 0 {
				effectiveTimeout = toolIdleTimeout
			}
			if effectiveTimeout <= 0 {
				// If this phase has no timeout configured, never abort.
				continue
			}
			if time.Since(last) < effectiveTimeout {
				continue
			}

			if ts.completed.Load() {
				return
			}
			if !ts.timedOut.CompareAndSwap(false, true) {
				return
			}

			slog.Warn("codexSession: idle timeout reached, aborting turn",
				"idle_timeout", effectiveTimeout,
				"tool_active", ts.toolActive.Load(),
			)
			ts.traceWriteJSON(map[string]any{
				"type":         "turn_end",
				"ts":           time.Now().Format(time.RFC3339Nano),
				"status":       "idle_timeout",
				"idle_timeout": effectiveTimeout.String(),
				"tool_active":  ts.toolActive.Load(),
			})
			// Abort the current turn (kill this codex subprocess).
			turnCancel()

			// Best-effort wait for the current process reader to exit, to reduce event interleaving
			// if the engine immediately auto-resumes.
			select {
			case <-ts.done:
			case <-time.After(5 * time.Second):
			}

			// If the turn actually completed (or was already completed), do NOT send an idle-timeout
			// error event; otherwise it will be consumed by the next user turn and trigger an
			// unexpected auto-resume.
			if ts.completed.Load() {
				return
			}

			// Signal the engine so it can auto-resume if configured.
			cs.events <- core.Event{
				Type:  core.EventError,
				Error: &core.AgentIdleTimeoutError{Agent: "codex", Idle: effectiveTimeout},
			}
			return
		}
	}
}

// RespondPermission is a no-op for Codex — permissions are handled via CLI flags.
func (cs *codexSession) RespondPermission(_ string, _ core.PermissionResult) error {
	return nil
}

func (cs *codexSession) Events() <-chan core.Event {
	return cs.events
}

func (cs *codexSession) CurrentSessionID() string {
	v, _ := cs.threadID.Load().(string)
	return v
}

func (cs *codexSession) Alive() bool {
	return cs.alive.Load()
}

func (cs *codexSession) Close() error {
	cs.alive.Store(false)
	cs.cancel()
	cs.wg.Wait()
	close(cs.events)
	return nil
}

func truncate(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes]) + "..."
}
