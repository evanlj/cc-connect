package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSquadRuntimeRoot_EnvOverride(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CC_SQUAD_RUNTIME_ROOT", base)
	got := resolveSquadRuntimeRoot("squad_xxx", NewSquadStore(filepath.Join(base, "store")))
	want := filepath.Join(base, "squad_xxx")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("runtime root mismatch: got=%s want=%s", got, want)
	}
}

func TestResolveSquadRuntimeRoot_DefaultHasRunID(t *testing.T) {
	_ = os.Unsetenv("CC_SQUAD_RUNTIME_ROOT")
	store := NewSquadStore(filepath.Join(t.TempDir(), "a", "b", "c", "squad", "p"))
	got := resolveSquadRuntimeRoot("squad_123", store)
	if !strings.HasSuffix(filepath.ToSlash(got), "/squad_123") {
		t.Fatalf("runtime root should end with run id, got=%s", got)
	}
	if !strings.Contains(strings.ToLower(filepath.ToSlash(got)), "ccsq") {
		t.Fatalf("runtime root should contain short base 'ccsq', got=%s", got)
	}
}
