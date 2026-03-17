package core

import (
	"strings"
	"testing"
)

func TestSelectDebateSpeakers(t *testing.T) {
	workers := []DebateRole{
		{Role: "jianzhu", DisplayName: "剑主"},
		{Role: "wendan", DisplayName: "文胆"},
		{Role: "xingzou", DisplayName: "行走"},
		{Role: "zhanggui", DisplayName: "掌柜"},
	}

	got1 := selectDebateSpeakers("host-decide", 1, workers, map[string]bool{}, 3)
	if len(got1) != 2 || got1[0].Role != "jianzhu" || got1[1].Role != "wendan" {
		t.Fatalf("round1 host-decide unexpected: %+v", got1)
	}

	spoken := map[string]bool{"jianzhu": true, "wendan": true}
	got2 := selectDebateSpeakers("cover-all-by-end", 3, workers, spoken, 3)
	if len(got2) < 2 {
		t.Fatalf("cover-all-by-end should prioritize uncovered roles: %+v", got2)
	}
}

func TestBuildRoleSessionKey(t *testing.T) {
	key := buildRoleSessionKey("feishu:oc_chat_xxx:ou_user", "oc_chat_xxx", "debate_20260317_152601_123456", "jianzhu")
	want := "feishu:oc_chat_xxx:debate_debate_20260317_152601_123456_jianzhu"
	if key != want {
		t.Fatalf("session key mismatch: got %q want %q", key, want)
	}
}

func TestBuildRolePromptGuardrails(t *testing.T) {
	room := &DebateRoom{
		Question:  "如何把需求拆成可并行执行的任务",
		MaxRounds: 3,
		RoomID:    "debate_20260317_190001_123456",
	}
	role := DebateRole{Role: "jianzhu", DisplayName: "剑主"}

	board := &DebateBlackboard{
		Topic:      room.Question,
		Goal:       "形成可执行的并行任务拆分方案",
		RoundPlan:  "补充证据、对比方案并收敛可执行动作。",
		RoundFocus: "先对齐拆分原则，再给职责边界",
		Revision:   5,
		RoleNotes: map[string]DebateRoleNote{
			"wendan": {LatestStance: "先定义统一模板再拆任务。"},
		},
	}

	got := buildRolePrompt(room, role, 1, nil, board, `D:\ai-github\cc-connect\mutilbot\instance-mutilbot1\data\discussion\p\blackboards\debate_x.json`)
	if got == "" {
		t.Fatal("prompt should not be empty")
	}
	if !containsAll(got,
		"[黑板文件]",
		"必须先读取该 JSON",
		"本轮计划：补充证据、对比方案并收敛可执行动作。",
		"\"type\": \"blackboard_writeback\"",
		"\"base_revision\": 5",
	) {
		t.Fatalf("prompt blackboard context missing:\n%s", got)
	}
}

func TestFlattenPromptForTransport(t *testing.T) {
	raw := "第一行\n\n第二行  \r\n  第三行"
	got := flattenPromptForTransport(raw)
	if strings.Contains(got, "\n") || strings.Contains(got, "\r") {
		t.Fatalf("flattened prompt should not contain new lines: %q", got)
	}
	if !containsAll(got, "第一行", "第二行", "第三行") {
		t.Fatalf("flattened prompt missing content: %q", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
