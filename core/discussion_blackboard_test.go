package core

import (
	"strings"
	"testing"
	"time"
)

func TestDebateStoreLoadOrInitBlackboard(t *testing.T) {
	store := NewDebateStore(t.TempDir())
	room := NewDebateRoom("feishu:oc_chat_xxx:ou_user_xxx", DebateStartOptions{
		Question:  "如何进行每日总结",
		MaxRounds: 3,
	}, time.Now())

	board, err := store.LoadOrInitBlackboard(room)
	if err != nil {
		t.Fatalf("LoadOrInitBlackboard err: %v", err)
	}
	if board.RoomID != room.RoomID {
		t.Fatalf("room_id mismatch: got %q want %q", board.RoomID, room.RoomID)
	}
	if board.Topic != room.Question {
		t.Fatalf("topic mismatch: got %q want %q", board.Topic, room.Question)
	}
	if board.Revision <= 0 {
		t.Fatalf("revision should be > 0, got %d", board.Revision)
	}

	board.Round = 2
	board.RoundPlan = "补充证据、对比方案并收敛可执行动作。"
	board.RoundFocus = "收敛模板与执行节奏"
	if err := store.SaveBlackboard(board); err != nil {
		t.Fatalf("SaveBlackboard err: %v", err)
	}

	got, err := store.LoadBlackboard(room.RoomID)
	if err != nil {
		t.Fatalf("LoadBlackboard err: %v", err)
	}
	if got.Round != 2 {
		t.Fatalf("round mismatch: got %d want 2", got.Round)
	}
	if got.RoundFocus != "收敛模板与执行节奏" {
		t.Fatalf("round focus mismatch: %q", got.RoundFocus)
	}
	if got.RoundPlan != "补充证据、对比方案并收敛可执行动作。" {
		t.Fatalf("round plan mismatch: %q", got.RoundPlan)
	}
}

func TestApplyRoleContributionToBlackboard(t *testing.T) {
	board := &DebateBlackboard{
		RoomID:    "debate_xxx",
		Topic:     "如何进行每日总结",
		MaxRounds: 3,
		RoleNotes: map[string]DebateRoleNote{},
		Revision:  1,
	}
	role := DebateRole{Role: "jianzhu", DisplayName: "剑主"}
	reply := "【观点】每日总结要固定时间窗口。\n【依据】便于跨角色汇总。\n【风险/反例】若无固定时点会拖延？\n【建议动作】设定18:00前提交。"

	c := ApplyRoleContribution(board, role, 1, reply)
	if c.Stance == "" || c.Action == "" {
		t.Fatalf("contribution parsed unexpectedly: %+v", c)
	}
	note, ok := board.RoleNotes["jianzhu"]
	if !ok {
		t.Fatal("role note not written")
	}
	if note.LatestRound != 1 {
		t.Fatalf("latest round mismatch: %d", note.LatestRound)
	}
	if note.LatestStance == "" {
		t.Fatal("latest stance should not be empty")
	}
	if len(board.OpenQuestions) != 0 {
		t.Fatalf("open questions should be host-controlled, got %d", len(board.OpenQuestions))
	}
	if board.Revision <= 1 {
		t.Fatalf("revision should increase, got %d", board.Revision)
	}
}

func TestDebateStoreBlackboardFilePath(t *testing.T) {
	store := NewDebateStore(t.TempDir())
	got := store.BlackboardFilePath("debate_20260317_190001_000001")
	if got == "" {
		t.Fatal("blackboard path should not be empty")
	}
	if filepathExt := len(got) >= 5 && got[len(got)-5:] == ".json"; !filepathExt {
		t.Fatalf("blackboard path should end with .json, got %q", got)
	}
}

func TestExtractRoleContribution(t *testing.T) {
	reply := "1) 【观点】先统一日报结构。\n2) 【依据】便于比较趋势。\n3) 【风险/反例】字段太多会增加填报成本。\n4) 【建议动作】先跑一周轻量模板。"
	c := ExtractRoleContribution(reply)
	if c.Stance == "" {
		t.Fatal("stance should not be empty")
	}
	if c.Basis == "" {
		t.Fatal("basis should not be empty")
	}
	if c.Action == "" {
		t.Fatal("action should not be empty")
	}
}

func TestExtractRoleContributionWithWritebackJSON(t *testing.T) {
	reply := "【观点】先统一日报模板。\n【依据】便于跨角色对齐。\n【风险/反例】字段太多会降低填写意愿。\n【建议动作】先试运行一周。\n\n```json\n{\n  \"type\": \"blackboard_writeback\",\n  \"room_id\": \"debate_20260317_190001_000001\",\n  \"role\": \"jianzhu\",\n  \"round\": 2,\n  \"base_revision\": 7,\n  \"stance\": \"统一日报模板，先轻量后扩展。\",\n  \"basis\": \"先确保填写率，再逐步增加字段。\",\n  \"risk\": \"一次性字段过多导致执行失败。\",\n  \"action\": \"第一周只收集3个核心字段。\"\n}\n```"

	c := ExtractRoleContribution(reply)
	if c.RoomID != "debate_20260317_190001_000001" {
		t.Fatalf("room_id mismatch: %q", c.RoomID)
	}
	if c.Role != "jianzhu" {
		t.Fatalf("role mismatch: %q", c.Role)
	}
	if c.BaseRevision != 7 {
		t.Fatalf("base revision mismatch: %d", c.BaseRevision)
	}
	if c.Stance != "统一日报模板，先轻量后扩展。" {
		t.Fatalf("stance should prefer writeback json, got %q", c.Stance)
	}
	if strings.Contains(c.DisplayReply, "\"blackboard_writeback\"") {
		t.Fatalf("display reply should strip writeback json block, got %q", c.DisplayReply)
	}
}

func TestTruncateStrKeepsUTF8Boundary(t *testing.T) {
	in := "这是一个用于验证UTF-8截断安全性的测试字符串"
	got := truncateStr(in, 8)
	if strings.Contains(got, "\uFFFD") {
		t.Fatalf("truncate should not produce replacement rune, got=%q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncate should append ellipsis, got=%q", got)
	}
}
