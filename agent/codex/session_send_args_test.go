package codex

import "testing"

func TestBuildSendArgs_NonResumeUsesStdinPrompt(t *testing.T) {
	cs := &codexSession{
		workDir:    "G:/AgeAction/AgeActionExample",
		model:      "gpt-5.2",
		mode:       "yolo",
		configArgs: []string{"-c", "foo=bar"},
	}

	args := cs.buildSendArgs(false, "")
	if len(args) < 3 {
		t.Fatalf("args too short: %#v", args)
	}
	if args[len(args)-1] != "-" {
		t.Fatalf("expected last arg '-', got %#v", args[len(args)-1])
	}
	foundCd := false
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--cd" && i+1 < len(args) && args[i+1] == cs.workDir {
			foundCd = true
			break
		}
	}
	if !foundCd {
		t.Fatalf("expected --cd %q in args: %#v", cs.workDir, args)
	}
}

func TestBuildSendArgs_ResumeUsesSessionAndStdinPrompt(t *testing.T) {
	cs := &codexSession{
		workDir: "G:/AgeAction/AgeActionExample",
		model:   "gpt-5.2",
		mode:    "auto-edit",
	}

	tid := "thread-123"
	args := cs.buildSendArgs(true, tid)
	if len(args) < 3 {
		t.Fatalf("args too short: %#v", args)
	}
	if args[0] != "exec" || args[1] != "resume" {
		t.Fatalf("expected exec resume prefix, got %#v", args)
	}
	if args[len(args)-2] != tid {
		t.Fatalf("expected session id %q near tail, got %#v", tid, args)
	}
	if args[len(args)-1] != "-" {
		t.Fatalf("expected last arg '-', got %#v", args[len(args)-1])
	}
	for _, a := range args {
		if a == "--cd" {
			t.Fatalf("resume args should not include --cd: %#v", args)
		}
	}
}
