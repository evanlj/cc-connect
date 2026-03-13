package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxPlatformMessageLen = 4000

const (
	defaultThinkingMaxLen = 300
	defaultToolMaxLen     = 500
)

// VersionInfo is set by main at startup so that /version works.
var VersionInfo string

// DisplayCfg controls truncation of intermediate messages.
// A value of -1 means "use default", 0 means "no truncation".
type DisplayCfg struct {
	ThinkingMaxLen int // max runes for thinking preview; 0 = no truncation
	ToolMaxLen     int // max runes for tool use preview; 0 = no truncation
}

// Engine routes messages between platforms and the agent for a single project.
type Engine struct {
	name      string
	agent     Agent
	platforms []Platform
	sessions  *SessionManager
	ctx       context.Context
	cancel    context.CancelFunc
	i18n      *I18n
	speech    SpeechCfg
	display   DisplayCfg
	startedAt time.Time

	providerSaveFunc       func(providerName string) error
	providerAddSaveFunc    func(p ProviderConfig) error
	providerRemoveSaveFunc func(name string) error

	cronScheduler *CronScheduler

	// When true, this engine will not execute normal agent tasks.
	// It will still accept slash commands and background helpers (e.g. trace translation).
	traceTranslateOnly bool
	traceTranslateSvc  *traceTranslateService

	// Interactive agent session management
	interactiveMu     sync.Mutex
	interactiveStates map[string]*interactiveState // key = sessionKey
}

// interactiveState tracks a running interactive agent session and its permission state.
type interactiveState struct {
	agentSession AgentSession
	platform     Platform
	replyCtx     any
	mu           sync.Mutex
	pending      *pendingPermission
	approveAll   bool // when true, auto-approve all permission requests for this session
	quiet        bool // when true, suppress thinking and tool progress messages
	autoResume   int  // auto-resume attempt counter (per session)
}

// pendingPermission represents a permission request waiting for user response.
type pendingPermission struct {
	RequestID    string
	ToolName     string
	ToolInput    map[string]any
	InputPreview string
	Resolved     chan struct{} // closed when user responds
}

func NewEngine(name string, ag Agent, platforms []Platform, sessionStorePath string, lang Language) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{
		name:              name,
		agent:             ag,
		platforms:         platforms,
		sessions:          NewSessionManager(sessionStorePath),
		ctx:               ctx,
		cancel:            cancel,
		i18n:              NewI18n(lang),
		display:           DisplayCfg{ThinkingMaxLen: defaultThinkingMaxLen, ToolMaxLen: defaultToolMaxLen},
		interactiveStates: make(map[string]*interactiveState),
		startedAt:         time.Now(),
	}
}

// SetSpeechConfig configures the speech-to-text subsystem.
func (e *Engine) SetSpeechConfig(cfg SpeechCfg) {
	e.speech = cfg
}

// SetDisplayConfig overrides the default truncation settings.
func (e *Engine) SetDisplayConfig(cfg DisplayCfg) {
	e.display = cfg
}

func (e *Engine) SetLanguageSaveFunc(fn func(Language) error) {
	e.i18n.SetSaveFunc(fn)
}

func (e *Engine) SetProviderSaveFunc(fn func(providerName string) error) {
	e.providerSaveFunc = fn
}

func (e *Engine) SetProviderAddSaveFunc(fn func(ProviderConfig) error) {
	e.providerAddSaveFunc = fn
}

func (e *Engine) SetProviderRemoveSaveFunc(fn func(string) error) {
	e.providerRemoveSaveFunc = fn
}

func (e *Engine) SetCronScheduler(cs *CronScheduler) {
	e.cronScheduler = cs
}

func (e *Engine) ProjectName() string {
	return e.name
}

// ExecuteCronJob runs a cron job by injecting a synthetic message into the engine.
// It finds the platform that owns the session key, reconstructs a reply context,
// and processes the message as if the user sent it.
func (e *Engine) ExecuteCronJob(job *CronJob) error {
	sessionKey := job.SessionKey
	platformName := ""
	if idx := strings.Index(sessionKey, ":"); idx > 0 {
		platformName = sessionKey[:idx]
	}

	var targetPlatform Platform
	for _, p := range e.platforms {
		if p.Name() == platformName {
			targetPlatform = p
			break
		}
	}
	if targetPlatform == nil {
		return fmt.Errorf("platform %q not found for session %q", platformName, sessionKey)
	}

	rc, ok := targetPlatform.(ReplyContextReconstructor)
	if !ok {
		return fmt.Errorf("platform %q does not support proactive messaging (cron)", platformName)
	}

	replyCtx, err := rc.ReconstructReplyCtx(sessionKey)
	if err != nil {
		return fmt.Errorf("reconstruct reply context: %w", err)
	}

	// Notify user that a cron job is executing
	desc := job.Description
	if desc == "" {
		desc = truncateStr(job.Prompt, 40)
	}
	e.send(targetPlatform, replyCtx, fmt.Sprintf("⏰ %s", desc))

	msg := &Message{
		SessionKey: sessionKey,
		Platform:   platformName,
		UserID:     "cron",
		UserName:   "cron",
		Content:    job.Prompt,
		ReplyCtx:   replyCtx,
	}

	session := e.sessions.GetOrCreateActive(sessionKey)
	if !session.TryLock() {
		return fmt.Errorf("session %q is busy", sessionKey)
	}

	e.processInteractiveMessage(targetPlatform, msg, session)
	return nil
}

func (e *Engine) Start() error {
	for _, p := range e.platforms {
		if err := p.Start(e.handleMessage); err != nil {
			return fmt.Errorf("[%s] start platform %s: %w", e.name, p.Name(), err)
		}
		slog.Info("platform started", "project", e.name, "platform", p.Name())
	}
	slog.Info("engine started", "project", e.name, "agent", e.agent.Name(), "platforms", len(e.platforms))
	return nil
}

func (e *Engine) Stop() error {
	e.cancel()

	e.interactiveMu.Lock()
	for key, state := range e.interactiveStates {
		if state.agentSession != nil {
			state.agentSession.Close()
		}
		delete(e.interactiveStates, key)
	}
	e.interactiveMu.Unlock()

	var errs []error
	for _, p := range e.platforms {
		if err := p.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("stop platform %s: %w", p.Name(), err))
		}
	}
	if err := e.agent.Stop(); err != nil {
		errs = append(errs, fmt.Errorf("stop agent %s: %w", e.agent.Name(), err))
	}
	if len(errs) > 0 {
		return fmt.Errorf("engine stop errors: %v", errs)
	}
	return nil
}

func (e *Engine) handleMessage(p Platform, msg *Message) {
	content := strings.TrimSpace(msg.Content)

	// Translation-only mode: ignore all normal messages (including images/voice) to avoid
	// accidentally running agent tasks or creating feedback loops. Only slash commands work.
	// This is intended for a dedicated "trace translate & push" cc-connect process.
	if e.traceTranslateOnly {
		if len(msg.Images) == 0 && msg.Audio == nil && strings.HasPrefix(content, "/") {
			e.handleCommand(p, msg, content)
		}
		return
	}

	// Voice message: transcribe to text first
	if msg.Audio != nil {
		e.handleVoiceMessage(p, msg)
		return
	}

	if content == "" && len(msg.Images) == 0 {
		return
	}

	if len(msg.Images) == 0 && strings.HasPrefix(content, "/") {
		e.handleCommand(p, msg, content)
		return
	}

	// Permission responses bypass the session lock
	if e.handlePendingPermission(p, msg, content) {
		return
	}

	session := e.sessions.GetOrCreateActive(msg.SessionKey)
	if !session.TryLock() {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPreviousProcessing))
		return
	}

	slog.Info("processing message",
		"platform", msg.Platform,
		"user", msg.UserName,
		"session", session.ID,
	)

	go e.processInteractiveMessage(p, msg, session)
}

// ──────────────────────────────────────────────────────────────
// Voice message handling
// ──────────────────────────────────────────────────────────────

func (e *Engine) handleVoiceMessage(p Platform, msg *Message) {
	if !e.speech.Enabled || e.speech.STT == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgVoiceNotEnabled))
		return
	}

	audio := msg.Audio
	if NeedsConversion(audio.Format) && !HasFFmpeg() {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgVoiceNoFFmpeg))
		return
	}

	slog.Info("transcribing voice message",
		"platform", msg.Platform, "user", msg.UserName,
		"format", audio.Format, "size", len(audio.Data),
	)
	e.send(p, msg.ReplyCtx, e.i18n.T(MsgVoiceTranscribing))

	text, err := TranscribeAudio(e.ctx, e.speech.STT, audio, e.speech.Language)
	if err != nil {
		slog.Error("speech transcription failed", "error", err)
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgVoiceTranscribeFailed), err))
		return
	}

	text = strings.TrimSpace(text)
	if text == "" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgVoiceEmpty))
		return
	}

	slog.Info("voice transcribed", "text_len", len(text))
	e.send(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgVoiceTranscribed), text))

	// Replace audio with transcribed text and re-dispatch
	msg.Audio = nil
	msg.Content = text
	e.handleMessage(p, msg)
}

// ──────────────────────────────────────────────────────────────
// Permission handling
// ──────────────────────────────────────────────────────────────

func (e *Engine) handlePendingPermission(p Platform, msg *Message, content string) bool {
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates[msg.SessionKey]
	e.interactiveMu.Unlock()
	if !ok || state == nil {
		return false
	}

	state.mu.Lock()
	pending := state.pending
	state.mu.Unlock()
	if pending == nil {
		return false
	}

	lower := strings.ToLower(strings.TrimSpace(content))

	if isApproveAllResponse(lower) {
		state.mu.Lock()
		state.approveAll = true
		state.mu.Unlock()

		if err := state.agentSession.RespondPermission(pending.RequestID, PermissionResult{
			Behavior:     "allow",
			UpdatedInput: pending.ToolInput,
		}); err != nil {
			slog.Error("failed to send permission response", "error", err)
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgError), err))
		} else {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPermissionApproveAll))
		}
	} else if isAllowResponse(lower) {
		if err := state.agentSession.RespondPermission(pending.RequestID, PermissionResult{
			Behavior:     "allow",
			UpdatedInput: pending.ToolInput,
		}); err != nil {
			slog.Error("failed to send permission response", "error", err)
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgError), err))
		} else {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPermissionAllowed))
		}
	} else if isDenyResponse(lower) {
		if err := state.agentSession.RespondPermission(pending.RequestID, PermissionResult{
			Behavior: "deny",
			Message:  "User denied this tool use.",
		}); err != nil {
			slog.Error("failed to send deny response", "error", err)
		}
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPermissionDenied))
	} else {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPermissionHint))
		return true
	}

	state.mu.Lock()
	state.pending = nil
	state.mu.Unlock()
	close(pending.Resolved)

	return true
}

func isApproveAllResponse(s string) bool {
	for _, w := range []string{
		"allow all", "allowall", "approve all", "yes all",
		"允许所有", "允许全部", "全部允许", "所有允许", "都允许", "全部同意",
	} {
		if s == w {
			return true
		}
	}
	return false
}

func isAllowResponse(s string) bool {
	for _, w := range []string{"allow", "yes", "y", "ok", "允许", "同意", "可以", "好", "好的", "是", "确认", "approve"} {
		if s == w {
			return true
		}
	}
	return false
}

func isDenyResponse(s string) bool {
	for _, w := range []string{"deny", "no", "n", "reject", "拒绝", "不允许", "不行", "不", "否", "取消", "cancel"} {
		if s == w {
			return true
		}
	}
	return false
}

// ──────────────────────────────────────────────────────────────
// Interactive agent processing
// ──────────────────────────────────────────────────────────────

func (e *Engine) processInteractiveMessage(p Platform, msg *Message, session *Session) {
	defer session.Unlock()

	e.i18n.DetectAndSet(msg.Content)
	session.AddHistory("user", msg.Content)

	state := e.getOrCreateInteractiveState(msg.SessionKey, p, msg.ReplyCtx, session)

	// Update reply context for this turn
	state.mu.Lock()
	state.platform = p
	state.replyCtx = msg.ReplyCtx
	// Reset auto-resume budget for each NEW user message.
	// (We still cap auto-resume inside the same message to avoid infinite loops.)
	state.autoResume = 0
	state.mu.Unlock()

	if state.agentSession == nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgError), "failed to start agent session"))
		return
	}

	if err := state.agentSession.Send(msg.Content, msg.Images); err != nil {
		slog.Error("failed to send prompt", "error", err)

		if !state.agentSession.Alive() {
			e.cleanupInteractiveState(msg.SessionKey)
			e.send(p, msg.ReplyCtx, e.i18n.T(MsgSessionRestarting))

			state = e.getOrCreateInteractiveState(msg.SessionKey, p, msg.ReplyCtx, session)
			if state.agentSession == nil {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgError), "failed to restart agent session"))
				return
			}
			if err := state.agentSession.Send(msg.Content, msg.Images); err != nil {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgError), err))
				return
			}
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgError), err))
			return
		}
	}

	// Quiet mode suppresses intermediate messages. If the platform supports a
	// sending a visible "executing" message.
	state.mu.Lock()
	quiet := state.quiet
	state.mu.Unlock()
	if quiet {
		// In quiet mode we suppress intermediate thinking/tool messages, which can make
		// long-running turns feel "stuck" from the user's perspective. Send a single
		// lightweight acknowledgement so users know execution has started.
		e.send(p, msg.ReplyCtx, e.i18n.T(MsgQuietAck))
	}

	e.processInteractiveEvents(state, session, msg.SessionKey, msg.Content)
}

func (e *Engine) getOrCreateInteractiveState(sessionKey string, p Platform, replyCtx any, session *Session) *interactiveState {
	e.interactiveMu.Lock()
	defer e.interactiveMu.Unlock()

	state, ok := e.interactiveStates[sessionKey]
	if ok && state.agentSession != nil && state.agentSession.Alive() {
		return state
	}

	// Inject per-session env vars so the agent subprocess can call `cc-connect cron add` etc.
	if inj, ok := e.agent.(SessionEnvInjector); ok {
		envVars := []string{
			"CC_PROJECT=" + e.name,
			"CC_SESSION_KEY=" + sessionKey,
		}
		if exePath, err := os.Executable(); err == nil {
			binDir := filepath.Dir(exePath)
			if curPath := os.Getenv("PATH"); curPath != "" {
				envVars = append(envVars, "PATH="+binDir+string(filepath.ListSeparator)+curPath)
			} else {
				envVars = append(envVars, "PATH="+binDir)
			}
		}
		inj.SetSessionEnv(envVars)
	}

	agentSession, err := e.agent.StartSession(e.ctx, session.AgentSessionID)
	if err != nil {
		slog.Error("failed to start interactive session", "error", err)
		if !ok || state == nil {
			state = &interactiveState{platform: p, replyCtx: replyCtx}
			e.interactiveStates[sessionKey] = state
			return state
		}
		// Preserve existing flags (quiet/approveAll) even if session start fails.
		state.platform = p
		state.replyCtx = replyCtx
		state.agentSession = nil
		return state
	}

	// Preserve per-session flags (quiet, approveAll, etc.) across restarts.
	if ok && state != nil {
		state.agentSession = agentSession
		state.platform = p
		state.replyCtx = replyCtx
	} else {
		state = &interactiveState{
			agentSession: agentSession,
			platform:     p,
			replyCtx:     replyCtx,
		}
		e.interactiveStates[sessionKey] = state
	}

	slog.Info("interactive session started", "session_key", sessionKey, "agent_session", session.AgentSessionID)
	return state
}

func (e *Engine) cleanupInteractiveState(sessionKey string) {
	e.interactiveMu.Lock()
	defer e.interactiveMu.Unlock()

	state, ok := e.interactiveStates[sessionKey]
	if ok && state.agentSession != nil {
		state.agentSession.Close()
	}
	delete(e.interactiveStates, sessionKey)
}

func (e *Engine) processInteractiveEvents(state *interactiveState, session *Session, sessionKey string, originalPrompt string) {
	var textParts []string
	toolCount := 0

	for event := range state.agentSession.Events() {
		if e.ctx.Err() != nil {
			return
		}

		state.mu.Lock()
		p := state.platform
		replyCtx := state.replyCtx
		state.mu.Unlock()

		switch event.Type {
		case EventThinking:
			if !state.quiet && event.Content != "" {
				preview := truncateIf(event.Content, e.display.ThinkingMaxLen)
				e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgThinking), preview))
			}

		case EventToolUse:
			toolCount++
			if !state.quiet {
				inputPreview := truncateIf(event.ToolInput, e.display.ToolMaxLen)
				e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgTool), toolCount, event.ToolName, inputPreview))
			}

		case EventText:
			if event.Content != "" {
				textParts = append(textParts, event.Content)
			}
			if event.SessionID != "" && session.AgentSessionID == "" {
				session.AgentSessionID = event.SessionID
				e.sessions.Save()
			}

		case EventPermissionRequest:
			state.mu.Lock()
			autoApprove := state.approveAll
			state.mu.Unlock()

			if autoApprove {
				slog.Debug("auto-approving (approve-all)", "request_id", event.RequestID, "tool", event.ToolName)
				_ = state.agentSession.RespondPermission(event.RequestID, PermissionResult{
					Behavior:     "allow",
					UpdatedInput: event.ToolInputRaw,
				})
				continue
			}

			slog.Info("permission request",
				"request_id", event.RequestID,
				"tool", event.ToolName,
			)

			permLimit := e.display.ToolMaxLen
			if permLimit > 0 {
				permLimit = permLimit * 8 / 5 // permission prompts get ~1.6x more room
			}
			prompt := fmt.Sprintf(e.i18n.T(MsgPermissionPrompt), event.ToolName, truncateIf(event.ToolInput, permLimit))
			e.send(p, replyCtx, prompt)

			pending := &pendingPermission{
				RequestID:    event.RequestID,
				ToolName:     event.ToolName,
				ToolInput:    event.ToolInputRaw,
				InputPreview: event.ToolInput,
				Resolved:     make(chan struct{}),
			}
			state.mu.Lock()
			state.pending = pending
			state.mu.Unlock()

			<-pending.Resolved
			slog.Info("permission resolved", "request_id", event.RequestID)

		case EventResult:
			if event.SessionID != "" {
				session.AgentSessionID = event.SessionID
			}

			fullResponse := event.Content
			if fullResponse == "" && len(textParts) > 0 {
				fullResponse = strings.Join(textParts, "")
			}
			if fullResponse == "" {
				fullResponse = e.i18n.T(MsgEmptyResponse)
			}

			session.AddHistory("assistant", fullResponse)
			e.sessions.Save()

			slog.Debug("turn complete",
				"session", session.ID,
				"agent_session", session.AgentSessionID,
				"tools", toolCount,
				"response_len", len(fullResponse),
			)

			for _, chunk := range splitMessage(fullResponse, maxPlatformMessageLen) {
				if err := p.Send(e.ctx, replyCtx, chunk); err != nil {
					slog.Error("failed to send reply", "error", err)
					return
				}
			}
			return

		case EventError:
			if event.Error != nil {
				var idleErr *AgentIdleTimeoutError
				if errors.As(event.Error, &idleErr) && idleErr != nil && strings.EqualFold(idleErr.Agent, "codex") {
					state.mu.Lock()
					canAutoResume := state.autoResume < 1
					quiet := state.quiet
					if canAutoResume {
						state.autoResume++
						state.pending = nil
					}
					state.mu.Unlock()

					if canAutoResume {
						// Reset aggregation for the resumed turn.
						textParts = nil
						toolCount = 0

						if !quiet {
							e.send(p, replyCtx, fmt.Sprintf("⏳ AI 长时间无输出（%ds），已自动中止并尝试继续执行。", int(idleErr.Idle.Seconds())))
						}

						resumePrompt := fmt.Sprintf(
							"上一轮执行疑似卡住（连续 %ds 无输出）已中止。请在同一会话继续完成用户任务。\n\n用户任务：%s\n\n要求：\n1) 先检查当前工作区状态（已改文件/已生成产物/已执行到哪一步）。\n2) 用 5-10 行总结你已完成与未完成的部分。\n3) 只继续未完成部分，避免重复执行可能产生副作用的步骤。\n4) 若无法判断进度，先提出 1 个最关键的问题让我确认后再继续。",
							int(idleErr.Idle.Seconds()),
							originalPrompt,
						)

						if err := state.agentSession.Send(resumePrompt, nil); err != nil {
							slog.Error("auto-resume send failed", "error", err)
							e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgError), err))
							return
						}
						// Continue consuming events from the same AgentSession (new turn will stream into the same channel).
						continue
					}
				}

				slog.Error("agent error", "error", event.Error)
				e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgError), event.Error))
			}
			return
		}
	}

	// Channel closed - process exited unexpectedly
	slog.Warn("agent process exited", "session_key", sessionKey)
	e.cleanupInteractiveState(sessionKey)

	if len(textParts) > 0 {
		state.mu.Lock()
		p := state.platform
		replyCtx := state.replyCtx
		state.mu.Unlock()

		fullResponse := strings.Join(textParts, "")
		session.AddHistory("assistant", fullResponse)
		for _, chunk := range splitMessage(fullResponse, maxPlatformMessageLen) {
			e.send(p, replyCtx, chunk)
		}
	}
}

// ──────────────────────────────────────────────────────────────
// Command handling
// ──────────────────────────────────────────────────────────────

func (e *Engine) handleCommand(p Platform, msg *Message, raw string) {
	parts := strings.Fields(raw)
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "/new":
		e.cmdNew(p, msg, args)
	case "/list", "/sessions":
		e.cmdList(p, msg)
	case "/switch":
		e.cmdSwitch(p, msg, args)
	case "/current":
		e.cmdCurrent(p, msg)
	case "/sessionkey", "/session-key", "/session_key":
		e.cmdSessionKey(p, msg)
	case "/trace", "/watch", "/listen":
		e.cmdTrace(p, msg, args)
	case "/status":
		e.cmdStatus(p, msg)
	case "/history":
		e.cmdHistory(p, msg, args)
	case "/allow":
		e.cmdAllow(p, msg, args)
	case "/model":
		e.cmdModel(p, msg, args)
	case "/mode":
		e.cmdMode(p, msg, args)
	case "/lang":
		e.cmdLang(p, msg, args)
	case "/quiet":
		e.cmdQuiet(p, msg)
	case "/provider":
		e.cmdProvider(p, msg, args)
	case "/memory":
		e.cmdMemory(p, msg, args)
	case "/cron":
		e.cmdCron(p, msg, args)
	case "/compress", "/compact":
		e.cmdCompress(p, msg)
	case "/stop":
		e.cmdStop(p, msg)
	case "/help":
		e.cmdHelp(p, msg)
	case "/version":
		e.reply(p, msg.ReplyCtx, VersionInfo)
	default:
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("Unknown command: %s\nType /help for available commands.", cmd))
	}
}

func (e *Engine) cmdSessionKey(p Platform, msg *Message) {
	// Useful for configuring trace_translate_target_session_key.
	e.reply(p, msg.ReplyCtx, fmt.Sprintf("SessionKey: `%s`", msg.SessionKey))
}

func (e *Engine) cmdTrace(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese
	if e.traceTranslateSvc == nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, "当前项目未启用 trace 同步/翻译（trace_translate_enabled=false）。")
		} else {
			e.reply(p, msg.ReplyCtx, "Trace translate/forward is not enabled for this project (trace_translate_enabled=false).")
		}
		return
	}

	// No args: show status and usage
	if len(args) == 0 {
		snap := e.traceTranslateSvc.snapshot()
		var b strings.Builder
		if isZh {
			if snap.ForwardOnly {
				b.WriteString("📡 Trace 同步（直通，不翻译）\n\n")
			} else {
				b.WriteString("📡 Trace 翻译/同步\n\n")
			}
			b.WriteString(fmt.Sprintf("- 当前监听: `%s`\n", snap.WatchPath))
			b.WriteString(fmt.Sprintf("- follow_latest: `%v`\n", snap.FollowLatest))
			b.WriteString(fmt.Sprintf("- 目标窗口: `%s`\n", emptyAs(snap.TargetSessionKey, "(自动回发)")))
			b.WriteString("\n用法：\n")
			b.WriteString("- `/trace a` 监听 instance-a\n")
			b.WriteString("- `/trace b` 监听 instance-b\n")
			b.WriteString("- `/trace c` 监听 instance-c\n")
			b.WriteString("- `/trace instance-a` 同上\n")
			b.WriteString("- `/trace path D:/.../data/traces/codex` 监听指定目录/文件\n")
		} else {
			if snap.ForwardOnly {
				b.WriteString("📡 Trace forward (no LLM)\n\n")
			} else {
				b.WriteString("📡 Trace translate/forward\n\n")
			}
			b.WriteString(fmt.Sprintf("- watch: `%s`\n", snap.WatchPath))
			b.WriteString(fmt.Sprintf("- follow_latest: `%v`\n", snap.FollowLatest))
			b.WriteString(fmt.Sprintf("- target_session: `%s`\n", emptyAs(snap.TargetSessionKey, "(auto-reply)")))
			b.WriteString("\nUsage:\n")
			b.WriteString("- `/trace a` watch instance-a\n")
			b.WriteString("- `/trace b` watch instance-b\n")
			b.WriteString("- `/trace c` watch instance-c\n")
			b.WriteString("- `/trace instance-a` same\n")
			b.WriteString("- `/trace path D:/.../data/traces/codex` watch custom dir/file\n")
		}
		e.reply(p, msg.ReplyCtx, b.String())
		return
	}

	// Support: /trace watch <x>  OR /trace <x>
	target := strings.TrimSpace(args[0])
	if (target == "watch" || target == "switch" || target == "follow" || target == "to") && len(args) >= 2 {
		target = strings.TrimSpace(args[1])
	}

	// Support: /trace path <abs>
	if target == "path" && len(args) >= 2 {
		target = strings.TrimSpace(strings.Join(args[1:], " "))
		if err := e.traceTranslateSvc.SwitchWatchPath(target); err != nil {
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 切换监听失败：%v", err))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to switch watch path: %v", err))
			}
			return
		}
		snap := e.traceTranslateSvc.snapshot()
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ 已切换监听：`%s`", snap.WatchPath))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ Watch path switched to: `%s`", snap.WatchPath))
		}
		return
	}

	inst := normalizeInstanceArg(target)
	if inst == "" {
		if isZh {
			e.reply(p, msg.ReplyCtx, "❌ 参数不支持。用法：`/trace a|b|c` 或 `/trace path <目录>`")
		} else {
			e.reply(p, msg.ReplyCtx, "❌ Unsupported argument. Usage: `/trace a|b|c` or `/trace path <dir>`")
		}
		return
	}

	// Build watch path based on current watch root (infer repo root from .../instances/...).
	current := e.traceTranslateSvc.snapshot().WatchPath
	repoRoot := inferRepoRootFromInstancesPath(current)
	if repoRoot == "" {
		repoRoot = inferRepoRootFromWorkingDir()
		if repoRoot == "" {
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 无法从当前路径推断 repo 根目录：%s", current))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Cannot infer repo root from current watch path: %s", current))
			}
			return
		}
	}

	newWatch := filepath.Join(repoRoot, "instances", inst, "data", "traces", "codex")
	if err := e.traceTranslateSvc.SwitchWatchPath(newWatch); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 切换监听失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to switch watch path: %v", err))
		}
		return
	}

	snap := e.traceTranslateSvc.snapshot()
	if isZh {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ 已切换监听：`%s`", snap.WatchPath))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ Now watching: `%s`", snap.WatchPath))
	}
}

func (e *Engine) cmdNew(p Platform, msg *Message, args []string) {
	e.cleanupInteractiveState(msg.SessionKey)
	name := "session"
	if len(args) > 0 {
		name = strings.Join(args, " ")
	}
	s := e.sessions.NewSession(msg.SessionKey, name)
	e.reply(p, msg.ReplyCtx,
		fmt.Sprintf("✅ New session created: %s (id: %s)", s.Name, s.ID))
}

func (e *Engine) cmdList(p Platform, msg *Message) {
	agentSessions, err := e.agent.ListSessions(e.ctx)
	if err != nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgListError), err))
		return
	}
	if len(agentSessions) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgListEmpty))
		return
	}

	agentName := e.agent.Name()
	activeSession := e.sessions.GetOrCreateActive(msg.SessionKey)
	activeAgentID := activeSession.AgentSessionID

	limit := 20
	if len(agentSessions) < limit {
		limit = len(agentSessions)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(e.i18n.T(MsgListTitle), agentName, len(agentSessions)))
	for i := 0; i < limit; i++ {
		s := agentSessions[i]
		marker := "◻"
		if s.ID == activeAgentID {
			marker = "▶"
		}
		shortID := s.ID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		summary := s.Summary
		if summary == "" {
			summary = "(empty)"
		}
		sb.WriteString(fmt.Sprintf("%s `%s` · %s · **%d** msgs · %s\n",
			marker, shortID, summary, s.MessageCount, s.ModifiedAt.Format("01-02 15:04")))
	}
	if len(agentSessions) > limit {
		sb.WriteString(fmt.Sprintf(e.i18n.T(MsgListMore), len(agentSessions)-limit))
	}
	sb.WriteString(e.i18n.T(MsgListSwitchHint))
	e.reply(p, msg.ReplyCtx, sb.String())
}

func (e *Engine) cmdSwitch(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, "Usage: /switch <session_id_prefix>")
		return
	}
	prefix := strings.TrimSpace(args[0])

	agentSessions, err := e.agent.ListSessions(e.ctx)
	if err != nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ %v", err))
		return
	}

	var matched *AgentSessionInfo
	for i := range agentSessions {
		if strings.HasPrefix(agentSessions[i].ID, prefix) {
			matched = &agentSessions[i]
			break
		}
	}
	if matched == nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ No session matching prefix %q", prefix))
		return
	}

	e.cleanupInteractiveState(msg.SessionKey)

	session := e.sessions.GetOrCreateActive(msg.SessionKey)
	session.AgentSessionID = matched.ID
	session.Name = matched.Summary
	session.ClearHistory()
	e.sessions.Save()

	shortID := matched.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	e.reply(p, msg.ReplyCtx,
		fmt.Sprintf("✅ Switched to: %s (%s, %d msgs)", matched.Summary, shortID, matched.MessageCount))
}

func (e *Engine) cmdCurrent(p Platform, msg *Message) {
	s := e.sessions.GetOrCreateActive(msg.SessionKey)
	agentID := s.AgentSessionID
	if agentID == "" {
		agentID = "(new — not yet started)"
	}
	e.reply(p, msg.ReplyCtx, fmt.Sprintf(
		"📌 Current session\nName: %s\nClaude Session: %s\nLocal messages: %d",
		s.Name, agentID, len(s.History)))
}

func (e *Engine) cmdStatus(p Platform, msg *Message) {
	isZh := e.i18n.CurrentLang() == LangChinese

	// Platforms
	platNames := make([]string, len(e.platforms))
	for i, pl := range e.platforms {
		platNames[i] = pl.Name()
	}
	platformStr := strings.Join(platNames, ", ")
	if len(platNames) == 0 {
		platformStr = "-"
	}

	// Uptime
	uptime := time.Since(e.startedAt)
	var uptimeStr string
	if isZh {
		uptimeStr = formatDurationZh(uptime)
	} else {
		uptimeStr = formatDuration(uptime)
	}

	// Language
	langStr := string(e.i18n.CurrentLang())
	switch e.i18n.CurrentLang() {
	case LangChinese:
		langStr = "zh (中文)"
	case LangEnglish:
		langStr = "en (English)"
	}

	// Mode (optional)
	var modeStr string
	if ms, ok := e.agent.(ModeSwitcher); ok {
		mode := ms.GetMode()
		if mode != "" {
			if isZh {
				modeStr = fmt.Sprintf("权限模式: %s\n", mode)
			} else {
				modeStr = fmt.Sprintf("Mode: %s\n", mode)
			}
		}
	}

	// Session info
	var sessionStr string
	s := e.sessions.GetOrCreateActive(msg.SessionKey)
	if isZh {
		sessionStr = fmt.Sprintf("当前会话: %s (消息: %d)\n", s.Name, len(s.History))
	} else {
		sessionStr = fmt.Sprintf("Session: %s (messages: %d)\n", s.Name, len(s.History))
	}

	// Cron jobs
	var cronStr string
	if e.cronScheduler != nil {
		jobs := e.cronScheduler.Store().ListBySessionKey(msg.SessionKey)
		if len(jobs) > 0 {
			enabledCount := 0
			for _, j := range jobs {
				if j.Enabled {
					enabledCount++
				}
			}
			if isZh {
				cronStr = fmt.Sprintf("定时任务: %d (启用: %d)\n", len(jobs), enabledCount)
			} else {
				cronStr = fmt.Sprintf("Cron jobs: %d (enabled: %d)\n", len(jobs), enabledCount)
			}
		}
	}

	// Trace translation status (optional)
	if e.traceTranslateSvc != nil {
		snap := e.traceTranslateSvc.snapshot()
		var traceStr string
		if isZh {
			title := "Trace 翻译: 启用"
			if snap.ForwardOnly {
				title = "Trace 同步: 启用（直通，不翻译）"
			}
			traceStr = fmt.Sprintf("%s\n- watch: %s\n- follow_latest: %v\n- target_session: %s\n- provider: %s (%s)\n",
				title,
				snap.WatchPath,
				snap.FollowLatest,
				emptyAs(snap.TargetSessionKey, "(自动回发)"),
				emptyAs(snap.ProviderName, "(unknown)"),
				emptyAs(snap.ProviderModel, "(default)"),
			)
			if !snap.ForwardOnly && snap.FailCount > 0 {
				traceStr += fmt.Sprintf("- 连续失败: %d\n- 最近错误: %s\n", snap.FailCount, emptyAs(snap.LastFailErr, "(unknown)"))
			}
		} else {
			title := "Trace translate: enabled"
			if snap.ForwardOnly {
				title = "Trace forward: enabled (no LLM)"
			}
			traceStr = fmt.Sprintf("%s\n- watch: %s\n- follow_latest: %v\n- target_session: %s\n- provider: %s (%s)\n",
				title,
				snap.WatchPath,
				snap.FollowLatest,
				emptyAs(snap.TargetSessionKey, "(auto-reply)"),
				emptyAs(snap.ProviderName, "(unknown)"),
				emptyAs(snap.ProviderModel, "(default)"),
			)
			if !snap.ForwardOnly && snap.FailCount > 0 {
				traceStr += fmt.Sprintf("- consecutive failures: %d\n- last error: %s\n", snap.FailCount, emptyAs(snap.LastFailErr, "(unknown)"))
			}
		}
		if cronStr == "" {
			cronStr = traceStr
		} else {
			cronStr += traceStr
		}
	}

	e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgStatusTitle,
		e.name,
		e.agent.Name(),
		platformStr,
		uptimeStr,
		langStr,
		modeStr,
		sessionStr,
		cronStr,
	))
}

func cronTimeFormat(t, now time.Time) string {
	if t.Year() != now.Year() {
		return "2006-01-02 15:04"
	}
	return "01-02 15:04"
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func formatDurationZh(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%d天 %d小时 %d分钟", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%d小时 %d分钟", hours, minutes)
	}
	return fmt.Sprintf("%d分钟", minutes)
}

func emptyAs(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func normalizeInstanceArg(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "instances/")
	s = strings.TrimPrefix(s, "instances\\")
	s = strings.Trim(s, "/\\")

	switch s {
	case "a", "inst-a", "instance-a":
		return "instance-a"
	case "b", "inst-b", "instance-b":
		return "instance-b"
	case "c", "inst-c", "instance-c":
		return "instance-c"
	}

	if strings.HasPrefix(s, "instance-") {
		return s
	}
	return ""
}

func inferRepoRootFromInstancesPath(p string) string {
	p = filepath.Clean(filepath.FromSlash(strings.TrimSpace(p)))
	if p == "" {
		return ""
	}
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '\\' || r == '/' })
	if len(parts) < 3 {
		return ""
	}
	for i := 0; i < len(parts); i++ {
		if strings.EqualFold(parts[i], "instances") && i > 0 {
			return filepath.Join(parts[:i]...)
		}
	}
	return ""
}

func inferRepoRootFromWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	wd = filepath.Clean(wd)
	if wd == "" {
		return ""
	}
	// If "<wd>/instances" exists, treat wd as repo root.
	if fi, err := os.Stat(filepath.Join(wd, "instances")); err == nil && fi.IsDir() {
		return wd
	}
	return ""
}

func (e *Engine) cmdHistory(p Platform, msg *Message, args []string) {
	s := e.sessions.GetOrCreateActive(msg.SessionKey)
	n := 10
	if len(args) > 0 {
		if v, err := strconv.Atoi(args[0]); err == nil && v > 0 {
			n = v
		}
	}

	entries := s.GetHistory(n)

	// Fallback: load from agent backend if in-memory history is empty
	if len(entries) == 0 && s.AgentSessionID != "" {
		if hp, ok := e.agent.(HistoryProvider); ok {
			if agentEntries, err := hp.GetSessionHistory(e.ctx, s.AgentSessionID, n); err == nil {
				entries = agentEntries
			}
		}
	}

	if len(entries) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgHistoryEmpty))
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📜 History (last %d):\n\n", len(entries)))
	for _, h := range entries {
		icon := "👤"
		if h.Role == "assistant" {
			icon = "🤖"
		}
		content := h.Content
		if len([]rune(content)) > 200 {
			content = string([]rune(content)[:200]) + "..."
		}
		sb.WriteString(fmt.Sprintf("%s [%s]\n%s\n\n", icon, h.Timestamp.Format("15:04:05"), content))
	}
	e.reply(p, msg.ReplyCtx, sb.String())
}

func (e *Engine) cmdLang(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		cur := e.i18n.CurrentLang()
		name := langDisplayName(cur)
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgLangCurrent, name))
		return
	}

	target := strings.ToLower(strings.TrimSpace(args[0]))
	var lang Language
	switch target {
	case "en", "english":
		lang = LangEnglish
	case "zh", "cn", "chinese", "中文":
		lang = LangChinese
	case "auto":
		lang = LangAuto
	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgLangInvalid))
		return
	}

	e.i18n.SetLang(lang)
	name := langDisplayName(lang)
	e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgLangChanged, name))
}

func langDisplayName(lang Language) string {
	switch lang {
	case LangEnglish:
		return "English"
	case LangChinese:
		return "中文"
	default:
		return "Auto"
	}
}

func (e *Engine) cmdHelp(p Platform, msg *Message) {
	e.reply(p, msg.ReplyCtx, e.i18n.T(MsgHelp))
}

func (e *Engine) cmdModel(p Platform, msg *Message, args []string) {
	switcher, ok := e.agent.(ModelSwitcher)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgModelNotSupported))
		return
	}

	fetchCtx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()
	models := switcher.AvailableModels(fetchCtx)
	isZh := e.i18n.CurrentLang() == LangChinese

	if len(args) == 0 {
		var sb strings.Builder
		current := switcher.GetModel()
		if current == "" {
			if isZh {
				sb.WriteString("当前模型: (未设置，使用 Agent 默认值)\n")
			} else {
				sb.WriteString("Current model: (not set, using agent default)\n")
			}
		} else {
			sb.WriteString(e.i18n.Tf(MsgModelCurrent, current))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
		if isZh {
			sb.WriteString("可用模型:\n")
		} else {
			sb.WriteString("Available models:\n")
		}
		for i, m := range models {
			marker := "  "
			if m.Name == current {
				marker = "> "
			}
			desc := m.Desc
			if desc != "" {
				desc = " — " + desc
			}
			sb.WriteString(fmt.Sprintf("%s%d. %s%s\n", marker, i+1, m.Name, desc))
		}
		sb.WriteString("\n")
		if isZh {
			sb.WriteString("用法: `/model <序号>` 或 `/model <模型名>`")
		} else {
			sb.WriteString("Usage: `/model <number>` or `/model <model_name>`")
		}
		e.reply(p, msg.ReplyCtx, sb.String())
		return
	}

	target := args[0]
	if idx, err := strconv.Atoi(target); err == nil && idx >= 1 && idx <= len(models) {
		target = models[idx-1].Name
	}

	switcher.SetModel(target)
	e.cleanupInteractiveState(msg.SessionKey)

	s := e.sessions.GetOrCreateActive(msg.SessionKey)
	s.AgentSessionID = ""
	s.ClearHistory()
	e.sessions.Save()

	e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgModelChanged, target))
}

func (e *Engine) cmdMode(p Platform, msg *Message, args []string) {
	switcher, ok := e.agent.(ModeSwitcher)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgModeNotSupported))
		return
	}

	if len(args) == 0 {
		current := switcher.GetMode()
		modes := switcher.PermissionModes()
		var sb strings.Builder
		isZh := e.i18n.CurrentLang() == LangChinese
		for _, m := range modes {
			marker := "  "
			if m.Key == current {
				marker = "▶ "
			}
			if isZh {
				sb.WriteString(fmt.Sprintf("%s**%s** — %s\n", marker, m.NameZh, m.DescZh))
			} else {
				sb.WriteString(fmt.Sprintf("%s**%s** — %s\n", marker, m.Name, m.Desc))
			}
		}
		if isZh {
			sb.WriteString("\n使用 `/mode <名称>` 切换模式\n可用值: `default` / `edit` / `plan` / `yolo`")
		} else {
			sb.WriteString("\nUse `/mode <name>` to switch.\nAvailable: `default` / `edit` / `plan` / `yolo`")
		}
		e.reply(p, msg.ReplyCtx, sb.String())
		return
	}

	target := strings.ToLower(args[0])
	switcher.SetMode(target)
	newMode := switcher.GetMode()

	e.cleanupInteractiveState(msg.SessionKey)

	modes := switcher.PermissionModes()
	displayName := newMode
	isZh := e.i18n.CurrentLang() == LangChinese
	for _, m := range modes {
		if m.Key == newMode {
			if isZh {
				displayName = m.NameZh
			} else {
				displayName = m.Name
			}
			break
		}
	}
	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgModeChanged), displayName))
}

func (e *Engine) cmdQuiet(p Platform, msg *Message) {
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates[msg.SessionKey]
	e.interactiveMu.Unlock()

	if !ok || state == nil {
		// No state yet, create one so the flag persists
		state = &interactiveState{platform: p, replyCtx: msg.ReplyCtx, quiet: true}
		e.interactiveMu.Lock()
		e.interactiveStates[msg.SessionKey] = state
		e.interactiveMu.Unlock()
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgQuietOn))
		return
	}

	state.mu.Lock()
	state.quiet = !state.quiet
	quiet := state.quiet
	state.mu.Unlock()

	if quiet {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgQuietOn))
	} else {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgQuietOff))
	}
}

func (e *Engine) cmdStop(p Platform, msg *Message) {
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates[msg.SessionKey]
	e.interactiveMu.Unlock()

	if !ok || state == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNoExecution))
		return
	}

	// Cancel pending permission if any
	state.mu.Lock()
	pending := state.pending
	if pending != nil {
		state.pending = nil
	}
	state.mu.Unlock()
	if pending != nil {
		close(pending.Resolved)
	}

	e.cleanupInteractiveState(msg.SessionKey)
	e.reply(p, msg.ReplyCtx, e.i18n.T(MsgExecutionStopped))
}

func (e *Engine) cmdCompress(p Platform, msg *Message) {
	compressor, ok := e.agent.(ContextCompressor)
	if !ok || compressor.CompressCommand() == "" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCompressNotSupported))
		return
	}

	// Check for an active interactive session
	e.interactiveMu.Lock()
	state, hasState := e.interactiveStates[msg.SessionKey]
	e.interactiveMu.Unlock()

	if !hasState || state == nil || state.agentSession == nil || !state.agentSession.Alive() {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCompressNoSession))
		return
	}

	session := e.sessions.GetOrCreateActive(msg.SessionKey)
	if !session.TryLock() {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPreviousProcessing))
		return
	}

	e.send(p, msg.ReplyCtx, e.i18n.T(MsgCompressing))

	msg.Content = compressor.CompressCommand()
	go e.processInteractiveMessage(p, msg, session)
}

func (e *Engine) cmdAllow(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		if auth, ok := e.agent.(ToolAuthorizer); ok {
			tools := auth.GetAllowedTools()
			if len(tools) == 0 {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNoToolsAllowed))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCurrentTools), strings.Join(tools, ", ")))
			}
		} else {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgToolAuthNotSupported))
		}
		return
	}

	toolName := strings.TrimSpace(args[0])
	if auth, ok := e.agent.(ToolAuthorizer); ok {
		if err := auth.AddAllowedTools(toolName); err != nil {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgToolAllowFailed), err))
			return
		}
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgToolAllowedNew), toolName))
	} else {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgToolAuthNotSupported))
	}
}

func (e *Engine) cmdProvider(p Platform, msg *Message, args []string) {
	switcher, ok := e.agent.(ProviderSwitcher)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgProviderNotSupported))
		return
	}

	if len(args) == 0 {
		current := switcher.GetActiveProvider()
		if current == nil {
			providers := switcher.ListProviders()
			if len(providers) == 0 {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgProviderNone))
			} else {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgProviderNone))
			}
			return
		}
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgProviderCurrent), current.Name))
		return
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "list":
		providers := switcher.ListProviders()
		if len(providers) == 0 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgProviderListEmpty))
			return
		}
		current := switcher.GetActiveProvider()
		var sb strings.Builder
		sb.WriteString(e.i18n.T(MsgProviderListTitle))
		for _, prov := range providers {
			marker := "  "
			if current != nil && prov.Name == current.Name {
				marker = "▶ "
			}
			detail := prov.Name
			if prov.BaseURL != "" {
				detail += " (" + prov.BaseURL + ")"
			}
			if prov.Model != "" {
				detail += " [" + prov.Model + "]"
			}
			sb.WriteString(fmt.Sprintf("%s**%s**\n", marker, detail))
		}
		sb.WriteString("\n" + e.i18n.T(MsgProviderSwitchHint))
		e.reply(p, msg.ReplyCtx, sb.String())

	case "add":
		e.cmdProviderAdd(p, msg, switcher, args[1:])

	case "remove", "rm", "delete":
		e.cmdProviderRemove(p, msg, switcher, args[1:])

	case "switch":
		if len(args) < 2 {
			e.reply(p, msg.ReplyCtx, "Usage: /provider switch <name>")
			return
		}
		e.switchProvider(p, msg, switcher, args[1])

	case "current":
		current := switcher.GetActiveProvider()
		if current == nil {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgProviderNone))
			return
		}
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgProviderCurrent), current.Name))

	default:
		e.switchProvider(p, msg, switcher, args[0])
	}
}

func (e *Engine) cmdProviderAdd(p Platform, msg *Message, switcher ProviderSwitcher, args []string) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgProviderAddUsage))
		return
	}

	var prov ProviderConfig

	// Join args back; detect JSON (starts with '{') vs positional
	raw := strings.Join(args, " ")
	raw = strings.TrimSpace(raw)

	if strings.HasPrefix(raw, "{") {
		// JSON format: /provider add {"name":"relay","api_key":"sk-xxx",...}
		var jp struct {
			Name    string            `json:"name"`
			APIKey  string            `json:"api_key"`
			BaseURL string            `json:"base_url"`
			Model   string            `json:"model"`
			Env     map[string]string `json:"env"`
		}
		if err := json.Unmarshal([]byte(raw), &jp); err != nil {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgProviderAddFailed), "invalid JSON: "+err.Error()))
			return
		}
		if jp.Name == "" {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgProviderAddFailed), "\"name\" is required"))
			return
		}
		prov = ProviderConfig{Name: jp.Name, APIKey: jp.APIKey, BaseURL: jp.BaseURL, Model: jp.Model, Env: jp.Env}
	} else {
		// Positional: /provider add <name> <api_key> [base_url] [model]
		if len(args) < 2 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgProviderAddUsage))
			return
		}
		prov.Name = args[0]
		prov.APIKey = args[1]
		if len(args) > 2 {
			prov.BaseURL = args[2]
		}
		if len(args) > 3 {
			prov.Model = args[3]
		}
	}

	// Check for duplicates
	for _, existing := range switcher.ListProviders() {
		if existing.Name == prov.Name {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgProviderAddFailed), fmt.Sprintf("provider %q already exists", prov.Name)))
			return
		}
	}

	// Add to runtime
	updated := append(switcher.ListProviders(), prov)
	switcher.SetProviders(updated)

	// Persist to config
	if e.providerAddSaveFunc != nil {
		if err := e.providerAddSaveFunc(prov); err != nil {
			slog.Error("failed to persist provider", "error", err)
		}
	}

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgProviderAdded), prov.Name, prov.Name))
}

func (e *Engine) cmdProviderRemove(p Platform, msg *Message, switcher ProviderSwitcher, args []string) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, "Usage: /provider remove <name>")
		return
	}
	name := args[0]

	providers := switcher.ListProviders()
	found := false
	var remaining []ProviderConfig
	for _, prov := range providers {
		if prov.Name == name {
			found = true
		} else {
			remaining = append(remaining, prov)
		}
	}

	if !found {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgProviderNotFound), name))
		return
	}

	// If removing the active provider, clear it
	active := switcher.GetActiveProvider()
	switcher.SetProviders(remaining)
	if active != nil && active.Name == name {
		// No active provider after removal
		slog.Info("removed active provider, clearing selection", "name", name)
	}

	// Persist
	if e.providerRemoveSaveFunc != nil {
		if err := e.providerRemoveSaveFunc(name); err != nil {
			slog.Error("failed to persist provider removal", "error", err)
		}
	}

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgProviderRemoved), name))
}

func (e *Engine) switchProvider(p Platform, msg *Message, switcher ProviderSwitcher, name string) {
	if !switcher.SetActiveProvider(name) {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgProviderNotFound), name))
		return
	}

	// Force next user turn to start a fresh upstream thread so provider switch
	// takes effect deterministically (instead of resuming prior thread context).
	s := e.sessions.GetOrCreateActive(msg.SessionKey)
	s.AgentSessionID = ""
	e.sessions.Save()

	e.cleanupInteractiveState(msg.SessionKey)

	if e.providerSaveFunc != nil {
		if err := e.providerSaveFunc(name); err != nil {
			slog.Error("failed to save provider", "error", err)
		}
	}

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgProviderSwitched), name))
}

// ──────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────

// SendToSession sends a message to an active session from an external caller (API/CLI).
// If sessionKey is empty, it picks the first active session.
func (e *Engine) SendToSession(sessionKey, message string) error {
	e.interactiveMu.Lock()
	defer e.interactiveMu.Unlock()

	var state *interactiveState
	if sessionKey != "" {
		state = e.interactiveStates[sessionKey]
	} else {
		// Pick the first active session
		for _, s := range e.interactiveStates {
			state = s
			break
		}
	}

	if state == nil || state.platform == nil {
		return fmt.Errorf("no active session found (key=%q)", sessionKey)
	}

	state.mu.Lock()
	p := state.platform
	replyCtx := state.replyCtx
	state.mu.Unlock()

	return p.Send(e.ctx, replyCtx, message)
}

// send wraps p.Send with error logging.
func (e *Engine) send(p Platform, replyCtx any, content string) {
	if err := p.Send(e.ctx, replyCtx, content); err != nil {
		slog.Error("platform send failed", "platform", p.Name(), "error", err, "content_len", len(content))
	}
}

// reply wraps p.Reply with error logging.
func (e *Engine) reply(p Platform, replyCtx any, content string) {
	if err := p.Reply(e.ctx, replyCtx, content); err != nil {
		slog.Error("platform reply failed", "platform", p.Name(), "error", err, "content_len", len(content))
	}
}

// ──────────────────────────────────────────────────────────────
// /memory command
// ──────────────────────────────────────────────────────────────

func (e *Engine) cmdMemory(p Platform, msg *Message, args []string) {
	mp, ok := e.agent.(MemoryFileProvider)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMemoryNotSupported))
		return
	}

	if len(args) == 0 {
		// /memory — show project memory
		e.showMemoryFile(p, msg, mp.ProjectMemoryFile(), false)
		return
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "add":
		text := strings.TrimSpace(strings.Join(args[1:], " "))
		if text == "" {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMemoryAddUsage))
			return
		}
		e.appendMemoryFile(p, msg, mp.ProjectMemoryFile(), text)

	case "global":
		if len(args) == 1 {
			// /memory global — show global memory
			e.showMemoryFile(p, msg, mp.GlobalMemoryFile(), true)
			return
		}
		if strings.ToLower(args[1]) == "add" {
			text := strings.TrimSpace(strings.Join(args[2:], " "))
			if text == "" {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMemoryAddUsage))
				return
			}
			e.appendMemoryFile(p, msg, mp.GlobalMemoryFile(), text)
		} else {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMemoryAddUsage))
		}

	case "show":
		e.showMemoryFile(p, msg, mp.ProjectMemoryFile(), false)

	case "help", "--help", "-h":
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMemoryAddUsage))

	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMemoryAddUsage))
	}
}

func (e *Engine) showMemoryFile(p Platform, msg *Message, filePath string, isGlobal bool) {
	if filePath == "" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMemoryNotSupported))
		return
	}

	data, err := os.ReadFile(filePath)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgMemoryEmpty), filePath))
		return
	}

	content := string(data)
	if len([]rune(content)) > 2000 {
		content = string([]rune(content)[:2000]) + "\n\n... (truncated)"
	}

	if isGlobal {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgMemoryShowGlobal), filePath, content))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgMemoryShowProject), filePath, content))
	}
}

func (e *Engine) appendMemoryFile(p Platform, msg *Message, filePath, text string) {
	if filePath == "" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMemoryNotSupported))
		return
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgMemoryAddFailed), err))
		return
	}

	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgMemoryAddFailed), err))
		return
	}
	defer f.Close()

	entry := "\n- " + text + "\n"
	if _, err := f.WriteString(entry); err != nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgMemoryAddFailed), err))
		return
	}

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgMemoryAdded), filePath))
}

// ──────────────────────────────────────────────────────────────
// /cron command
// ──────────────────────────────────────────────────────────────

func (e *Engine) cmdCron(p Platform, msg *Message, args []string) {
	if e.cronScheduler == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronNotAvailable))
		return
	}

	if len(args) == 0 {
		e.cmdCronList(p, msg)
		return
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "add":
		e.cmdCronAdd(p, msg, args[1:])
	case "list":
		e.cmdCronList(p, msg)
	case "del", "delete", "rm", "remove":
		e.cmdCronDel(p, msg, args[1:])
	case "enable":
		e.cmdCronToggle(p, msg, args[1:], true)
	case "disable":
		e.cmdCronToggle(p, msg, args[1:], false)
	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronUsage))
	}
}

func (e *Engine) cmdCronAdd(p Platform, msg *Message, args []string) {
	// /cron add <min> <hour> <day> <month> <weekday> <prompt...>
	if len(args) < 6 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronAddUsage))
		return
	}

	cronExpr := strings.Join(args[:5], " ")
	prompt := strings.Join(args[5:], " ")

	job := &CronJob{
		ID:         GenerateCronID(),
		Project:    e.name,
		SessionKey: msg.SessionKey,
		CronExpr:   cronExpr,
		Prompt:     prompt,
		Enabled:    true,
		CreatedAt:  time.Now(),
	}

	if err := e.cronScheduler.AddJob(job); err != nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ %v", err))
		return
	}

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronAdded), job.ID, cronExpr, truncateStr(prompt, 60)))
}

func (e *Engine) cmdCronList(p Platform, msg *Message) {
	jobs := e.cronScheduler.Store().ListBySessionKey(msg.SessionKey)
	if len(jobs) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronEmpty))
		return
	}

	isZh := e.i18n.CurrentLang() == LangChinese
	now := time.Now()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(e.i18n.T(MsgCronListTitle), len(jobs)))
	sb.WriteString("\n")
	sb.WriteString("\n")

	for i, j := range jobs {
		if i > 0 {
			sb.WriteString("\n")
		}

		status := "✅"
		if !j.Enabled {
			status = "⏸"
		}
		desc := j.Description
		if desc == "" {
			desc = truncateStr(j.Prompt, 60)
		}
		sb.WriteString(fmt.Sprintf("%s %s\n", status, desc))

		sb.WriteString(fmt.Sprintf("ID: %s\n", j.ID))

		human := CronExprToHuman(j.CronExpr, isZh)
		if isZh {
			sb.WriteString(fmt.Sprintf("调度: %s (%s)\n", human, j.CronExpr))
		} else {
			sb.WriteString(fmt.Sprintf("Schedule: %s (%s)\n", human, j.CronExpr))
		}

		nextRun := e.cronScheduler.NextRun(j.ID)
		if !nextRun.IsZero() {
			fmtStr := cronTimeFormat(nextRun, now)
			if isZh {
				sb.WriteString(fmt.Sprintf("下次执行: %s\n", nextRun.Format(fmtStr)))
			} else {
				sb.WriteString(fmt.Sprintf("Next run: %s\n", nextRun.Format(fmtStr)))
			}
		}

		if !j.LastRun.IsZero() {
			fmtStr := cronTimeFormat(j.LastRun, now)
			if isZh {
				sb.WriteString(fmt.Sprintf("上次执行: %s", j.LastRun.Format(fmtStr)))
			} else {
				sb.WriteString(fmt.Sprintf("Last run: %s", j.LastRun.Format(fmtStr)))
			}
			if j.LastError != "" {
				sb.WriteString(fmt.Sprintf(" (failed: %s)", truncateStr(j.LastError, 40)))
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString(fmt.Sprintf("\n%s", e.i18n.T(MsgCronListFooter)))
	e.reply(p, msg.ReplyCtx, sb.String())
}

func (e *Engine) cmdCronDel(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronDelUsage))
		return
	}
	id := args[0]
	if e.cronScheduler.RemoveJob(id) {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronDeleted), id))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronNotFound), id))
	}
}

func (e *Engine) cmdCronToggle(p Platform, msg *Message, args []string, enable bool) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronDelUsage))
		return
	}
	id := args[0]
	var err error
	if enable {
		err = e.cronScheduler.EnableJob(id)
	} else {
		err = e.cronScheduler.DisableJob(id)
	}
	if err != nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ %v", err))
		return
	}
	if enable {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronEnabled), id))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronDisabled), id))
	}
}

// truncateIf truncates s to maxLen runes. 0 means no truncation.
func truncateIf(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	return string([]rune(s)[:maxLen]) + "..."
}

func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(text) > 0 {
		end := maxLen
		if end > len(text) {
			end = len(text)
		}
		if end < len(text) {
			if idx := strings.LastIndex(text[:end], "\n"); idx > 0 {
				end = idx + 1
			}
		}
		chunks = append(chunks, text[:end])
		text = text[end:]
	}
	return chunks
}
