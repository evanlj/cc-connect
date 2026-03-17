package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseDebateStartOptions(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		opts, err := parseDebateStartOptions([]string{
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
}
