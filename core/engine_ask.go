package core

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type AskResult struct {
	Content   string `json:"content"`
	LatencyMS int64  `json:"latency_ms"`
	ToolCount int    `json:"tool_count,omitempty"`
}

// AskSession sends a prompt to an existing session and waits for the final
// model response. It is intended for orchestration/API use cases where callers
// need a synchronous result instead of streaming platform messages.
func (e *Engine) AskSession(sessionKey, prompt string, timeout time.Duration) (AskResult, error) {
	var out AskResult
	sessionKey = strings.TrimSpace(sessionKey)
	prompt = strings.TrimSpace(prompt)
	if sessionKey == "" {
		return out, fmt.Errorf("session_key is required")
	}
	if prompt == "" {
		return out, fmt.Errorf("prompt is required")
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}

	session := e.sessions.GetOrCreateActive(sessionKey)
	if !session.TryLock() {
		return out, fmt.Errorf("session %q is busy", sessionKey)
	}
	defer session.Unlock()

	startedAt := time.Now()
	session.AddHistory("user", prompt)

	state := e.getOrCreateInteractiveState(sessionKey, nil, nil, session)
	if state == nil || state.agentSession == nil {
		return out, fmt.Errorf("failed to start agent session")
	}

	if err := state.agentSession.Send(prompt, nil); err != nil {
		if !state.agentSession.Alive() {
			e.cleanupInteractiveState(sessionKey)
			state = e.getOrCreateInteractiveState(sessionKey, nil, nil, session)
			if state == nil || state.agentSession == nil {
				return out, fmt.Errorf("failed to restart agent session")
			}
			if err2 := state.agentSession.Send(prompt, nil); err2 != nil {
				return out, err2
			}
		} else {
			return out, err
		}
	}

	var (
		textParts []string
		toolCount int
	)
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			return out, fmt.Errorf("ask timeout after %s", timeout)
		case <-e.ctx.Done():
			return out, fmt.Errorf("engine stopped")
		case event, ok := <-state.agentSession.Events():
			if !ok {
				// Channel closed unexpectedly. Return partial text if we have one.
				if len(textParts) > 0 {
					full := strings.Join(textParts, "")
					session.AddHistory("assistant", full)
					e.sessions.Save()
					out.Content = full
					out.ToolCount = toolCount
					out.LatencyMS = time.Since(startedAt).Milliseconds()
					return out, nil
				}
				return out, fmt.Errorf("agent session closed unexpectedly")
			}

			switch event.Type {
			case EventText:
				if event.Content != "" {
					textParts = append(textParts, event.Content)
				}
				if event.SessionID != "" && session.AgentSessionID == "" {
					session.AgentSessionID = event.SessionID
					e.sessions.Save()
				}

			case EventToolUse:
				toolCount++

			case EventPermissionRequest:
				// /ask is a synchronous API and cannot wait for human approval.
				// Deny by default to avoid hanging indefinitely.
				_ = state.agentSession.RespondPermission(event.RequestID, PermissionResult{
					Behavior: "deny",
					Message:  "permission prompts are not supported in /ask mode",
				})

			case EventResult:
				if event.SessionID != "" {
					session.AgentSessionID = event.SessionID
				}

				full := event.Content
				if full == "" && len(textParts) > 0 {
					full = strings.Join(textParts, "")
				}
				if full == "" {
					full = e.i18n.T(MsgEmptyResponse)
				}

				session.AddHistory("assistant", full)
				e.sessions.Save()

				out.Content = full
				out.ToolCount = toolCount
				out.LatencyMS = time.Since(startedAt).Milliseconds()
				return out, nil

			case EventError:
				if event.Error != nil {
					return out, event.Error
				}
				return out, fmt.Errorf("unknown agent error")
			}
		}
	}
}

// SendBySessionKey proactively sends message content to a session without
// requiring an active interactiveState. This is used by APIs such as /ask?speak=true.
func (e *Engine) SendBySessionKey(sessionKey, content string) error {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return fmt.Errorf("session_key is required")
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("content is required")
	}

	platformName := ""
	if idx := strings.Index(sessionKey, ":"); idx > 0 {
		platformName = sessionKey[:idx]
	}
	if platformName == "" {
		return fmt.Errorf("invalid session key %q", sessionKey)
	}

	var target Platform
	for _, p := range e.platforms {
		if p.Name() == platformName {
			target = p
			break
		}
	}
	if target == nil {
		return fmt.Errorf("platform %q not found for session %q", platformName, sessionKey)
	}

	rc, ok := target.(ReplyContextReconstructor)
	if !ok {
		return errors.New("platform does not support proactive messaging")
	}

	replyCtx, err := rc.ReconstructReplyCtx(sessionKey)
	if err != nil {
		return fmt.Errorf("reconstruct reply context: %w", err)
	}

	for _, chunk := range splitMessage(content, maxPlatformMessageLen) {
		if err := target.Send(e.ctx, replyCtx, chunk); err != nil {
			return err
		}
	}
	return nil
}
