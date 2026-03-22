package core

import (
	"testing"
	"time"
)

func TestResolveAskHTTPTimeout_Default(t *testing.T) {
	got := resolveAskHTTPTimeout(0)
	if got != defaultAskHTTPTimeout {
		t.Fatalf("timeout mismatch: got=%s want=%s", got, defaultAskHTTPTimeout)
	}
}

func TestResolveAskHTTPTimeout_ExtendedForLongPlanner(t *testing.T) {
	got := resolveAskHTTPTimeout(1800)
	want := 1800*time.Second + askHTTPTimeoutBuffer
	if got != want {
		t.Fatalf("timeout mismatch: got=%s want=%s", got, want)
	}
}

func TestResolveAskHTTPTimeout_KeepDefaultForShortRequests(t *testing.T) {
	got := resolveAskHTTPTimeout(240)
	if got != defaultAskHTTPTimeout {
		t.Fatalf("timeout mismatch: got=%s want=%s", got, defaultAskHTTPTimeout)
	}
}
