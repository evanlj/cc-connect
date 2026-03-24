package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	cfgpkg "github.com/chenhg5/cc-connect/config"
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

func TestBuildSquadRuntimePlatforms_SkipFeishuAndFallbackNoop(t *testing.T) {
	in := []cfgpkg.PlatformConfig{
		{
			Type: "feishu",
			Options: map[string]any{
				"app_id":         "app-id",
				"app_secret":     "app-secret",
				"allow_from":     "*",
				"reaction_emoji": "OnIt",
			},
		},
	}
	out := buildSquadRuntimePlatforms(in)
	if len(out) != 1 {
		t.Fatalf("expected fallback noop platform only, got=%d", len(out))
	}
	if !strings.EqualFold(strings.TrimSpace(out[0].Type), "noop") {
		t.Fatalf("expected noop fallback platform, got=%s", out[0].Type)
	}
	if got := strings.TrimSpace(anyToString(out[0].Options["allow_from"])); got != "__squad_internal_only__" {
		t.Fatalf("allow_from mismatch: %q", got)
	}
	if got := strings.TrimSpace(anyToString(out[0].Options["reaction_emoji"])); got != "none" {
		t.Fatalf("reaction_emoji mismatch: %q", got)
	}
}

func TestBuildSquadRuntimePlatforms_KeepNonFeishuAndApplyGate(t *testing.T) {
	in := []cfgpkg.PlatformConfig{
		{
			Type: "feishu",
			Options: map[string]any{
				"app_id":     "app-id",
				"app_secret": "app-secret",
			},
		},
		{
			Type: "telegram",
			Options: map[string]any{
				"token": "123456:abc",
			},
		},
	}
	out := buildSquadRuntimePlatforms(in)
	if len(out) != 1 {
		t.Fatalf("expected one non-feishu platform, got=%d", len(out))
	}
	if got := strings.ToLower(strings.TrimSpace(out[0].Type)); got != "telegram" {
		t.Fatalf("expected telegram, got=%s", out[0].Type)
	}
	if got := anyToString(out[0].Options["token"]); got != "123456:abc" {
		t.Fatalf("token should be preserved, got=%q", got)
	}
	if got := strings.TrimSpace(anyToString(out[0].Options["allow_from"])); got != "__squad_internal_only__" {
		t.Fatalf("allow_from mismatch: %q", got)
	}
	if got := strings.TrimSpace(anyToString(out[0].Options["reaction_emoji"])); got != "none" {
		t.Fatalf("reaction_emoji mismatch: %q", got)
	}
	// Ensure source options map is not mutated.
	if _, ok := in[1].Options["allow_from"]; ok {
		t.Fatalf("source platform options should not be mutated")
	}
}

func anyToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return ""
	}
}
