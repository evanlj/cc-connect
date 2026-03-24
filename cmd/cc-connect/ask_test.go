package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAskWithIO_JSONOutput(t *testing.T) {
	dataDir := t.TempDir()
	socketPath := filepath.Join(dataDir, "run", "api.sock")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("MkdirAll run dir failed: %v", err)
	}
	if err := os.WriteFile(socketPath, []byte("socket"), 0o644); err != nil {
		t.Fatalf("WriteFile socket marker failed: %v", err)
	}

	originalAskPost := askPost
	t.Cleanup(func() { askPost = originalAskPost })

	askPost = func(gotSockPath, path string, payload []byte) (*http.Response, error) {
		if gotSockPath != socketPath {
			t.Fatalf("socket path mismatch: got=%s want=%s", gotSockPath, socketPath)
		}
		if path != "/ask" {
			t.Fatalf("path mismatch: got=%s", path)
		}

		var req map[string]any
		if err := json.Unmarshal(payload, &req); err != nil {
			t.Fatalf("invalid payload JSON: %v", err)
		}
		if got, _ := req["session_key"].(string); got != "feishu:oc_chat:ou_user" {
			t.Fatalf("session_key mismatch: %q", got)
		}
		if got, _ := req["prompt"].(string); got != "give me answer" {
			t.Fatalf("prompt mismatch: %q", got)
		}

		body := `{"status":"ok","session_key":"feishu:oc_chat:ou_user","content":"final answer","latency_ms":88,"tool_count":1,"timeline":[{"type":"thinking","at_ms":4,"content":"plan"},{"type":"tool","at_ms":15,"tool_name":"read_file","tool_input":"core/engine_ask.go"},{"type":"text","at_ms":23,"content":"final answer"}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runAskWithIO([]string{
		"--session", "feishu:oc_chat:ou_user",
		"--data-dir", dataDir,
		"--json",
		"give", "me", "answer",
	}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("runAskWithIO exit code=%d stderr=%s", exitCode, stderr.String())
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("stderr should be empty, got: %s", stderr.String())
	}

	var got askResponse
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %v, raw=%s", err, stdout.String())
	}
	if got.Status != "ok" {
		t.Fatalf("status mismatch: %q", got.Status)
	}
	if got.SessionKey != "feishu:oc_chat:ou_user" {
		t.Fatalf("session key mismatch: %q", got.SessionKey)
	}
	if got.Content != "final answer" {
		t.Fatalf("content mismatch: %q", got.Content)
	}
	if got.LatencyMS != 88 {
		t.Fatalf("latency mismatch: %d", got.LatencyMS)
	}
	if got.ToolCount != 1 {
		t.Fatalf("tool_count mismatch: %d", got.ToolCount)
	}
	if len(got.Timeline) != 3 {
		t.Fatalf("timeline length mismatch: %d", len(got.Timeline))
	}
	if got.Timeline[0].Type != "thinking" || got.Timeline[1].Type != "tool" || got.Timeline[2].Type != "text" {
		t.Fatalf("timeline order mismatch: %+v", got.Timeline)
	}
}

func TestRunAskWithIO_TextOutputCompatible(t *testing.T) {
	dataDir := t.TempDir()
	socketPath := filepath.Join(dataDir, "run", "api.sock")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("MkdirAll run dir failed: %v", err)
	}
	if err := os.WriteFile(socketPath, []byte("socket"), 0o644); err != nil {
		t.Fatalf("WriteFile socket marker failed: %v", err)
	}

	originalAskPost := askPost
	t.Cleanup(func() { askPost = originalAskPost })

	askPost = func(_, _ string, _ []byte) (*http.Response, error) {
		body := `{"status":"ok","session_key":"feishu:oc_chat:ou_user","content":"plain answer","latency_ms":99,"tool_count":2,"timeline":[{"type":"text","at_ms":10,"content":"plain answer"}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runAskWithIO([]string{
		"--session", "feishu:oc_chat:ou_user",
		"--data-dir", dataDir,
		"plain", "answer",
	}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("runAskWithIO exit code=%d stderr=%s", exitCode, stderr.String())
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("stderr should be empty, got: %s", stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "Session: feishu:oc_chat:ou_user") {
		t.Fatalf("missing session line: %s", out)
	}
	if !strings.Contains(out, "Latency: 99 ms") {
		t.Fatalf("missing latency line: %s", out)
	}
	if !strings.Contains(out, "Tools: 2") {
		t.Fatalf("missing tool count line: %s", out)
	}
	if !strings.Contains(out, "----\nplain answer") {
		t.Fatalf("missing answer body: %s", out)
	}
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("text mode should not emit JSON: %s", out)
	}
}
