package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDebateRuntimeRoot(t *testing.T) {
	t.Run("empty room id", func(t *testing.T) {
		if got := resolveDebateRuntimeRoot("", NewDebateStore(t.TempDir())); got != "" {
			t.Fatalf("expected empty root, got %q", got)
		}
	})

	t.Run("use env override", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("CC_DEBATE_RUNTIME_ROOT", root)
		got := resolveDebateRuntimeRoot("debate_20260325_010101_000001", NewDebateStore(t.TempDir()))
		want := filepath.Join(root, "debate_20260325_010101_000001")
		if filepath.Clean(got) != filepath.Clean(want) {
			t.Fatalf("runtime root mismatch:\n got: %q\nwant: %q", got, want)
		}
	})
}

func TestFormatDebateRuntimeRolesStatus(t *testing.T) {
	readySocket := filepath.Join(t.TempDir(), "api.sock")
	if err := os.WriteFile(readySocket, []byte(""), 0o644); err != nil {
		t.Fatalf("write ready socket: %v", err)
	}
	missingSocket := filepath.Join(t.TempDir(), "missing.sock")

	room := &DebateRoom{
		RoleRuntime: map[string]SquadRoleRuntime{
			"jarvis": {
				Role:        "jarvis",
				DisplayName: "Jarvis",
				ProjectName: "debate_x_jarvis",
				SocketPath:  readySocket,
				PID:         1234,
			},
			"jianzhu": {
				Role:        "jianzhu",
				DisplayName: "剑主",
				ProjectName: "debate_x_jianzhu",
				SocketPath:  missingSocket,
				PID:         5678,
			},
		},
	}

	got := formatDebateRuntimeRolesStatus(room, true)
	if !strings.Contains(got, "socket_state=`ready`") {
		t.Fatalf("expected ready socket state, got: %s", got)
	}
	if !strings.Contains(got, "socket_state=`missing`") {
		t.Fatalf("expected missing socket state, got: %s", got)
	}
}
