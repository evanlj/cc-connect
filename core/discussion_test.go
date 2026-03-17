package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseDebateStartOptions(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		opts, err := parseDebateStartOptions([]string{
			"--mode", "consensus",
			"--preset", "tianji-five",
			"--rounds", "4",
			"--speaking-policy", "host-decide",
			"如何",
			"实现",
			"单机多机器人讨论",
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if opts.Preset != "tianji-five" {
			t.Fatalf("preset mismatch: %q", opts.Preset)
		}
		if opts.Mode != "consensus" {
			t.Fatalf("mode mismatch: %q", opts.Mode)
		}
		if opts.MaxRounds != 4 {
			t.Fatalf("max rounds mismatch: %d", opts.MaxRounds)
		}
		if opts.SpeakingPolicy != "host-decide" {
			t.Fatalf("policy mismatch: %q", opts.SpeakingPolicy)
		}
		if opts.Question != "如何 实现 单机多机器人讨论" {
			t.Fatalf("question mismatch: %q", opts.Question)
		}
		if err := ValidateDebateStartOptions(opts); err != nil {
			t.Fatalf("validate should pass: %v", err)
		}
	})

	t.Run("unknown flag", func(t *testing.T) {
		_, err := parseDebateStartOptions([]string{"--unknown", "x", "topic"})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("missing question", func(t *testing.T) {
		opts, err := parseDebateStartOptions([]string{"--rounds", "3"})
		if err != nil {
			t.Fatalf("parse should pass: %v", err)
		}
		if err := ValidateDebateStartOptions(opts); err == nil {
			t.Fatal("expected validation error, got nil")
		}
	})

	t.Run("invalid mode", func(t *testing.T) {
		opts := DebateStartOptions{
			Mode:      "xxx",
			Question:  "q",
			MaxRounds: 3,
		}
		if err := ValidateDebateStartOptions(opts); err == nil {
			t.Fatal("expected invalid mode error, got nil")
		}
	})
}

func TestDebateStoreSaveGetListAndTranscript(t *testing.T) {
	root := t.TempDir()
	store := NewDebateStore(root)
	if store == nil || !store.Enabled() {
		t.Fatal("store should be enabled")
	}

	now := time.Date(2026, 3, 17, 12, 0, 0, 123456000, time.UTC)
	room := NewDebateRoom("feishu:oc_chat_xxx:ou_user_xxx", DebateStartOptions{
		Preset:         "tianji-five",
		MaxRounds:      3,
		SpeakingPolicy: "host-decide",
		Question:       "讨论命令如何设计",
	}, now)

	if err := store.SaveRoom(room); err != nil {
		t.Fatalf("save room: %v", err)
	}

	got, err := store.GetRoom(room.RoomID)
	if err != nil {
		t.Fatalf("get room: %v", err)
	}
	if got.RoomID != room.RoomID {
		t.Fatalf("room id mismatch: got %q want %q", got.RoomID, room.RoomID)
	}
	if got.Question != room.Question {
		t.Fatalf("question mismatch: got %q want %q", got.Question, room.Question)
	}
	if got.GroupChatID != "oc_chat_xxx" {
		t.Fatalf("group chat mismatch: %q", got.GroupChatID)
	}

	list, err := store.ListRooms()
	if err != nil {
		t.Fatalf("list rooms: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len mismatch: %d", len(list))
	}

	if err := store.AppendTranscript(room.RoomID, DebateTranscriptEntry{
		Round:    1,
		Speaker:  "instance-b",
		Role:     "jianzhu",
		PostedBy: "instance-b",
		Content:  "这里是发言",
	}); err != nil {
		t.Fatalf("append transcript: %v", err)
	}

	transcriptPath := filepath.Join(root, "transcripts", room.RoomID+".jsonl")
	b, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("transcript file should not be empty")
	}

	loaded, err := store.LoadTranscript(room.RoomID)
	if err != nil {
		t.Fatalf("load transcript: %v", err)
	}
	if len(loaded) == 0 {
		t.Fatal("loaded transcript should not be empty")
	}
	if loaded[0].Role != "jianzhu" {
		t.Fatalf("loaded transcript role mismatch: %q", loaded[0].Role)
	}
}

func TestDefaultDebateRolesPreferMutilbotLayout(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mutilbot"), 0o755); err != nil {
		t.Fatalf("mkdir mutilbot: %v", err)
	}

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir temp root: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	roles := defaultDebateRoles()
	if len(roles) != 5 {
		t.Fatalf("roles len mismatch: %d", len(roles))
	}
	if roles[0].Instance != "instance-mutilbot1" {
		t.Fatalf("expected mutilbot mapping, got %q", roles[0].Instance)
	}
	socketPath := filepath.ToSlash(roles[0].SocketPath)
	if !strings.Contains(socketPath, "/mutilbot/instance-mutilbot1/data/run/api.sock") {
		t.Fatalf("unexpected socket path: %q", roles[0].SocketPath)
	}
}

func TestDefaultDebateRolesFallbackLegacyLayout(t *testing.T) {
	root := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir temp root: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	roles := defaultDebateRoles()
	if len(roles) != 5 {
		t.Fatalf("roles len mismatch: %d", len(roles))
	}
	if roles[0].Instance != "instance-a" {
		t.Fatalf("expected legacy mapping, got %q", roles[0].Instance)
	}
}

func TestInferRepoRootFromWorkingDirWithMutilbotSubDirs(t *testing.T) {
	root := t.TempDir()
	mutilbotRoot := filepath.Join(root, "mutilbot")
	instanceDir := filepath.Join(mutilbotRoot, "instance-mutilbot1")
	if err := os.MkdirAll(instanceDir, 0o755); err != nil {
		t.Fatalf("mkdir instance dir: %v", err)
	}

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	if err := os.Chdir(mutilbotRoot); err != nil {
		t.Fatalf("chdir mutilbot root: %v", err)
	}
	if got := inferRepoRootFromWorkingDir(); filepath.Clean(got) != filepath.Clean(root) {
		t.Fatalf("repo root mismatch from mutilbot root: got %q want %q", got, root)
	}

	if err := os.Chdir(instanceDir); err != nil {
		t.Fatalf("chdir mutilbot instance: %v", err)
	}
	if got := inferRepoRootFromWorkingDir(); filepath.Clean(got) != filepath.Clean(root) {
		t.Fatalf("repo root mismatch from mutilbot instance: got %q want %q", got, root)
	}
}

func TestNormalizeDebateQuestionStripMentions(t *testing.T) {
	in := "请讨论：如何把需求拆成可并行执行的任务 @Jarvis @_user_1"
	got := normalizeDebateQuestion(in)
	want := "请讨论：如何把需求拆成可并行执行的任务"
	if got != want {
		t.Fatalf("normalize question mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestNormalizeDebateQuestionStripFeishuAtTag(t *testing.T) {
	in := "请讨论：如何把需求拆成可并行执行的任务 <at user_id=\"ou_xxx\">Jarvis</at>"
	got := normalizeDebateQuestion(in)
	want := "请讨论：如何把需求拆成可并行执行的任务"
	if got != want {
		t.Fatalf("normalize feishu at mismatch:\n got: %q\nwant: %q", got, want)
	}
}
