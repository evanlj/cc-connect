package core

import (
	"fmt"
	"time"
)

// AgentIdleTimeoutError indicates the agent backend produced no output for a long time,
// and cc-connect forcibly aborted the current turn.
//
// Note: This is intentionally in core so both engine and agent implementations can
// share a stable error type without package coupling.
type AgentIdleTimeoutError struct {
	Agent string        // e.g. "codex"
	Idle  time.Duration // idle threshold reached
}

func (e *AgentIdleTimeoutError) Error() string {
	agent := e.Agent
	if agent == "" {
		agent = "agent"
	}
	if e.Idle <= 0 {
		return fmt.Sprintf("%s idle timeout", agent)
	}
	return fmt.Sprintf("%s idle timeout: no output for %s", agent, e.Idle)
}
