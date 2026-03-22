package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendPromptFullLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "data", "traces", "codex", "2026-03-21", "prompts-full.jsonl")
	entry := map[string]any{
		"type":   "prompt_full",
		"prompt": "hello world",
	}
	if err := appendPromptFullLog(logPath, entry); err != nil {
		t.Fatalf("appendPromptFullLog failed: %v", err)
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read prompt log failed: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, "\"prompt\":\"hello world\"") {
		t.Fatalf("unexpected prompt log content: %s", got)
	}
}
