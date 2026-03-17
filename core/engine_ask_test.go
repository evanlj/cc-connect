package core

import (
	"context"
	"sync"
	"testing"
	"time"
)

type stubAgent struct {
	session *stubAgentSession
}

func (a *stubAgent) Name() string { return "stub-agent" }

func (a *stubAgent) StartSession(_ context.Context, _ string) (AgentSession, error) {
	return a.session, nil
}

func (a *stubAgent) ListSessions(_ context.Context) ([]AgentSessionInfo, error) {
	return nil, nil
}

func (a *stubAgent) Stop() error { return nil }

type stubAgentSession struct {
	mu sync.Mutex

	events <-chan Event
	sendFn func(prompt string, images []ImageAttachment) error

	alive      bool
	sent       []string
	permission []PermissionResult
}

func newStubAgentSession(events <-chan Event) *stubAgentSession {
	return &stubAgentSession{
		events: events,
		alive:  true,
	}
}

func (s *stubAgentSession) Send(prompt string, images []ImageAttachment) error {
	s.mu.Lock()
	s.sent = append(s.sent, prompt)
	fn := s.sendFn
	s.mu.Unlock()
	if fn != nil {
		return fn(prompt, images)
	}
	return nil
}

func (s *stubAgentSession) RespondPermission(_ string, result PermissionResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.permission = append(s.permission, result)
	return nil
}

func (s *stubAgentSession) Events() <-chan Event {
	return s.events
}

func (s *stubAgentSession) CurrentSessionID() string {
	return "stub-session-id"
}

func (s *stubAgentSession) Alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alive
}

func (s *stubAgentSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alive = false
	return nil
}

type stubPlatform struct {
	name string

	mu   sync.Mutex
	sent []string
}

func (p *stubPlatform) Name() string { return p.name }

func (p *stubPlatform) Start(_ MessageHandler) error { return nil }

func (p *stubPlatform) Reply(_ context.Context, _ any, content string) error {
	return p.Send(context.Background(), nil, content)
}

func (p *stubPlatform) Send(_ context.Context, _ any, content string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, content)
	return nil
}

func (p *stubPlatform) Stop() error { return nil }

func (p *stubPlatform) ReconstructReplyCtx(_ string) (any, error) { return "ctx", nil }

func TestEngineAskSessionSuccess(t *testing.T) {
	events := make(chan Event, 4)
	events <- Event{Type: EventText, Content: "partial "}
	events <- Event{Type: EventResult, Content: "final answer", SessionID: "agent-session-x"}
	session := newStubAgentSession(events)
	agent := &stubAgent{session: session}

	engine := NewEngine("ask-test", agent, nil, "", LangEnglish)
	res, err := engine.AskSession("feishu:oc_chat:ou_user", "give me answer", 5*time.Second)
	if err != nil {
		t.Fatalf("AskSession unexpected err: %v", err)
	}
	if res.Content != "final answer" {
		t.Fatalf("content mismatch: got %q", res.Content)
	}
	if res.LatencyMS < 0 {
		t.Fatalf("latency should be >= 0, got %d", res.LatencyMS)
	}
}

func TestEngineAskSessionPermissionAutoDeny(t *testing.T) {
	events := make(chan Event, 4)
	events <- Event{Type: EventPermissionRequest, RequestID: "req-1", ToolName: "shell"}
	events <- Event{Type: EventResult, Content: "done"}
	session := newStubAgentSession(events)
	agent := &stubAgent{session: session}

	engine := NewEngine("ask-test", agent, nil, "", LangEnglish)
	_, err := engine.AskSession("feishu:oc_chat:ou_user", "check", 5*time.Second)
	if err != nil {
		t.Fatalf("AskSession unexpected err: %v", err)
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if len(session.permission) == 0 {
		t.Fatalf("expected permission response, got none")
	}
	if session.permission[0].Behavior != "deny" {
		t.Fatalf("permission behavior mismatch: %q", session.permission[0].Behavior)
	}
}

func TestEngineSendBySessionKey(t *testing.T) {
	platform := &stubPlatform{name: "feishu"}
	engine := NewEngine("send-test", &stubAgent{session: newStubAgentSession(make(chan Event))}, []Platform{platform}, "", LangEnglish)

	if err := engine.SendBySessionKey("feishu:oc_chat:ou_user", "hello"); err != nil {
		t.Fatalf("SendBySessionKey err: %v", err)
	}

	platform.mu.Lock()
	defer platform.mu.Unlock()
	if len(platform.sent) != 1 {
		t.Fatalf("sent count mismatch: %d", len(platform.sent))
	}
	if platform.sent[0] != "hello" {
		t.Fatalf("sent content mismatch: %q", platform.sent[0])
	}
}

func TestInteractiveStateDefaultQuietOnStart(t *testing.T) {
	platform := &stubPlatform{name: "feishu"}
	engine := NewEngine("quiet-default-test", &stubAgent{session: newStubAgentSession(make(chan Event))}, []Platform{platform}, "", LangEnglish)
	s := engine.sessions.GetOrCreateActive("feishu:oc_chat:ou_user")

	state := engine.getOrCreateInteractiveState("feishu:oc_chat:ou_user", platform, "ctx", s)
	if state == nil {
		t.Fatal("state should not be nil")
	}

	state.mu.Lock()
	quiet := state.quiet
	state.mu.Unlock()

	if !quiet {
		t.Fatal("expected quiet mode ON by default for new interactive session")
	}
}
