package core

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeSquadPromptForTransport_RemovesANSIAndControl(t *testing.T) {
	raw := "A\x1b[32;1mB\x1b[0m\tC\r\nD\x7fE"
	got := sanitizeSquadPromptForTransport(raw)
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("ansi escape should be removed: %q", got)
	}
	if strings.ContainsAny(got, "\r\n\t") {
		t.Fatalf("control chars should be removed: %q", got)
	}
	if strings.Contains(got, "[32;1m") {
		t.Fatalf("ansi fragments should be removed: %q", got)
	}
}

func TestSanitizeSquadPromptForTransport_TruncatesTooLongPrompt(t *testing.T) {
	raw := strings.Repeat("x", squadPromptMaxTransportRunes+200)
	got := sanitizeSquadPromptForTransport(raw)
	if utf8.RuneCountInString(got) > squadPromptMaxTransportRunes+3 { // truncateStr appends "..."
		t.Fatalf("prompt should be truncated, got rune count=%d", utf8.RuneCountInString(got))
	}
}

func TestShouldRetrySquadAskWithCompactPrompt(t *testing.T) {
	err := strings.NewReader("not an error")
	_ = err
	if shouldRetrySquadAskWithCompactPrompt(nil) {
		t.Fatalf("nil error should not trigger retry")
	}
	e1 := &simpleErr{s: "The filename, directory name, or volume label syntax is incorrect."}
	if !shouldRetrySquadAskWithCompactPrompt(e1) {
		t.Fatalf("expected retry for windows filename syntax error")
	}
	e2 := &simpleErr{s: "ask timeout after 4m0s"}
	if shouldRetrySquadAskWithCompactPrompt(e2) {
		t.Fatalf("timeout error should not trigger compact-prompt retry")
	}
}

type simpleErr struct {
	s string
}

func (e *simpleErr) Error() string { return e.s }
