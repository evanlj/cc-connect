package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func prepareSessionsSocket(t *testing.T, dataDir string) string {
	t.Helper()
	socketPath := filepath.Join(dataDir, "run", "api.sock")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("MkdirAll run dir failed: %v", err)
	}
	if err := os.WriteFile(socketPath, []byte("socket"), 0o644); err != nil {
		t.Fatalf("WriteFile socket marker failed: %v", err)
	}
	return socketPath
}

func TestRunSessionsWithIO_JSONSuccess(t *testing.T) {
	t.Setenv("CC_PROJECT", "")

	dataDir := t.TempDir()
	socketPath := prepareSessionsSocket(t, dataDir)

	originalSessionsGet := sessionsGet
	t.Cleanup(func() { sessionsGet = originalSessionsGet })

	sessionsGet = func(gotSockPath, path string) (*http.Response, error) {
		if gotSockPath != socketPath {
			t.Fatalf("socket path mismatch: got=%s want=%s", gotSockPath, socketPath)
		}
		if path != "/sessions" {
			t.Fatalf("api path mismatch: %s", path)
		}
		body := `[{"project":"proj-a","session_key":"feishu:oc_chat:ou_user","platform":"feishu"}]`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runSessionsWithIO([]string{
		"--json",
		"--data-dir", dataDir,
	}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("runSessionsWithIO exit=%d stderr=%s", exitCode, stderr.String())
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("stderr should be empty: %s", stderr.String())
	}

	var sessions []sessionListItem
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &sessions); err != nil {
		t.Fatalf("stdout should be valid JSON: %v, raw=%s", err, stdout.String())
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions len mismatch: %d", len(sessions))
	}
	if sessions[0].Project != "proj-a" || sessions[0].SessionKey != "feishu:oc_chat:ou_user" || sessions[0].Platform != "feishu" {
		t.Fatalf("unexpected session item: %+v", sessions[0])
	}
}

func TestRunSessionsWithIO_SocketMissing(t *testing.T) {
	t.Setenv("CC_PROJECT", "")

	dataDir := t.TempDir()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runSessionsWithIO([]string{
		"--json",
		"--data-dir", dataDir,
	}, &stdout, &stderr)

	if exitCode == 0 {
		t.Fatalf("expected non-zero exit on missing socket")
	}
	if !strings.Contains(stderr.String(), "socket not found") {
		t.Fatalf("stderr should mention missing socket: %s", stderr.String())
	}
}

func TestRunSessionsWithIO_NotRunningConnectError(t *testing.T) {
	t.Setenv("CC_PROJECT", "")

	dataDir := t.TempDir()
	_ = prepareSessionsSocket(t, dataDir)

	originalSessionsGet := sessionsGet
	t.Cleanup(func() { sessionsGet = originalSessionsGet })

	sessionsGet = func(_, _ string) (*http.Response, error) {
		return nil, errors.New("dial unix /tmp/api.sock: connect: connection refused")
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runSessionsWithIO([]string{
		"--json",
		"--data-dir", dataDir,
	}, &stdout, &stderr)

	if exitCode == 0 {
		t.Fatalf("expected non-zero exit when cc-connect not running")
	}
	if !strings.Contains(stderr.String(), "failed to connect to cc-connect") {
		t.Fatalf("stderr should mention connect failure: %s", stderr.String())
	}
}

func TestRunSessionsWithIO_ProjectNotFound(t *testing.T) {
	t.Setenv("CC_PROJECT", "")

	dataDir := t.TempDir()
	socketPath := prepareSessionsSocket(t, dataDir)

	originalSessionsGet := sessionsGet
	t.Cleanup(func() { sessionsGet = originalSessionsGet })

	sessionsGet = func(gotSockPath, path string) (*http.Response, error) {
		if gotSockPath != socketPath {
			t.Fatalf("socket path mismatch: got=%s want=%s", gotSockPath, socketPath)
		}
		if path != "/sessions?project=ghost" {
			t.Fatalf("api path mismatch: %s", path)
		}
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader(`project "ghost" not found`)),
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runSessionsWithIO([]string{
		"--project", "ghost",
		"--json",
		"--data-dir", dataDir,
	}, &stdout, &stderr)

	if exitCode == 0 {
		t.Fatalf("expected non-zero exit when project not found")
	}
	if !strings.Contains(stderr.String(), `project "ghost" not found`) {
		t.Fatalf("stderr should include project-not-found message: %s", stderr.String())
	}
}
