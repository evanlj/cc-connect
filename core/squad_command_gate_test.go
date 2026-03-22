package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdSquadShowPlanAndTask(t *testing.T) {
	platform := &stubPlatform{name: "feishu"}
	engine := NewEngine(
		"squad-cmd-test",
		&stubAgent{session: newStubAgentSession(make(chan Event))},
		[]Platform{platform},
		filepath.Join(t.TempDir(), "sessions", "sessions.json"),
		LangChinese,
	)
	t.Cleanup(func() { _ = engine.Stop() })

	run := &SquadRun{
		RunID:           "squad_test_show",
		Status:          SquadStatusWaiting,
		Phase:           SquadPhaseWaitPlanApprove,
		OwnerSessionKey: "feishu:chat:user",
		RepoPath:        `G:\demo\repo`,
		TaskPrompt:      "示例需求",
		PlannerRole:     "jarvis",
		ExecutorRole:    "xingzou",
		ReviewerRole:    "jianzhu",
		MaxRework:       DefaultSquadMaxRework,
		Plan: SquadPlan{
			Title:    "示例计划",
			Overview: "先实现，再测试",
			Tasks: []SquadTask{
				{
					ID:         "task-1",
					Title:      "实现功能",
					Objective:  "完成核心实现",
					Acceptance: "功能可运行",
				},
				{
					ID:         "task-2",
					Title:      "补充测试",
					Objective:  "补齐回归测试",
					Acceptance: "测试通过",
				},
			},
		},
		RoleRuntime: map[string]SquadRoleRuntime{},
	}
	if err := engine.squadStore.SaveRun(run); err != nil {
		t.Fatalf("save run failed: %v", err)
	}

	msg := &Message{
		SessionKey: "feishu:chat:user",
		Platform:   "feishu",
		UserID:     "user",
		UserName:   "u",
		ReplyCtx:   "ctx",
	}

	engine.cmdSquadShowPlan(platform, msg, []string{run.RunID})
	planReply := lastPlatformReply(platform)
	if !strings.Contains(planReply, "示例计划") || !strings.Contains(planReply, "task-1") {
		t.Fatalf("unexpected show-plan reply: %s", planReply)
	}
	if !strings.Contains(planReply, "/squad approve-plan "+run.RunID) {
		t.Fatalf("show-plan should include approve command: %s", planReply)
	}

	engine.cmdSquadShowTask(platform, msg, []string{run.RunID, "current"})
	taskReply := lastPlatformReply(platform)
	if !strings.Contains(taskReply, "任务：1/2") || !strings.Contains(taskReply, "ID：task-1") {
		t.Fatalf("unexpected show-task reply: %s", taskReply)
	}
	if !strings.Contains(taskReply, "/squad approve-task "+run.RunID+" task-1") {
		t.Fatalf("show-task should include approve-task command: %s", taskReply)
	}
}

func TestCmdSquadApprovePlanThenApproveTask(t *testing.T) {
	platform := &stubPlatform{name: "feishu"}
	engine := NewEngine(
		"squad-cmd-test",
		&stubAgent{session: newStubAgentSession(make(chan Event))},
		[]Platform{platform},
		filepath.Join(t.TempDir(), "sessions", "sessions.json"),
		LangChinese,
	)
	t.Cleanup(func() { _ = engine.Stop() })

	run := &SquadRun{
		RunID:           "squad_test_gate",
		Status:          SquadStatusWaiting,
		Phase:           SquadPhaseWaitPlanApprove,
		OwnerSessionKey: "feishu:chat:user",
		RepoPath:        `G:\demo\repo`,
		TaskPrompt:      "示例需求",
		PlannerRole:     "jarvis",
		ExecutorRole:    "xingzou",
		ReviewerRole:    "jianzhu",
		MaxRework:       DefaultSquadMaxRework,
		CurrentTask:     0,
		Plan: SquadPlan{
			Title: "示例计划",
			Tasks: []SquadTask{
				{ID: "task-1", Title: "实现功能"},
				{ID: "task-2", Title: "补充测试"},
			},
		},
		RoleRuntime: map[string]SquadRoleRuntime{},
	}
	if err := engine.squadStore.SaveRun(run); err != nil {
		t.Fatalf("save run failed: %v", err)
	}

	// Force "already running" branch to avoid starting real subprocess in test.
	engine.squadMu.Lock()
	engine.squadRuns[run.RunID] = func() {}
	engine.squadMu.Unlock()

	msg := &Message{
		SessionKey: "feishu:chat:user",
		Platform:   "feishu",
		UserID:     "user",
		UserName:   "u",
		ReplyCtx:   "ctx",
	}

	engine.cmdSquadApprovePlan(platform, msg, []string{run.RunID})
	updated, err := engine.squadStore.GetRun(run.RunID)
	if err != nil {
		t.Fatalf("load run after approve-plan failed: %v", err)
	}
	if !updated.PlanApproved {
		t.Fatalf("plan should be approved")
	}
	if updated.Phase != SquadPhaseWaitTaskApprove || updated.Status != SquadStatusWaiting {
		t.Fatalf("run should wait for task approval, got status=%s phase=%s", updated.Status, updated.Phase)
	}

	engine.cmdSquadApproveTask(platform, msg, []string{run.RunID, "current"})
	updated2, err := engine.squadStore.GetRun(run.RunID)
	if err != nil {
		t.Fatalf("load run after approve-task failed: %v", err)
	}
	if updated2.TaskApprovedID != "task-1" {
		t.Fatalf("task approved id mismatch: %s", updated2.TaskApprovedID)
	}
	if updated2.Phase != SquadPhaseExecuting || updated2.Status != SquadStatusRunning {
		t.Fatalf("run should move to executing, got status=%s phase=%s", updated2.Status, updated2.Phase)
	}
}

func TestCmdSquadSkipTask_WaitTaskApproval(t *testing.T) {
	platform := &stubPlatform{name: "feishu"}
	engine := NewEngine(
		"squad-cmd-test",
		&stubAgent{session: newStubAgentSession(make(chan Event))},
		[]Platform{platform},
		filepath.Join(t.TempDir(), "sessions", "sessions.json"),
		LangChinese,
	)
	t.Cleanup(func() { _ = engine.Stop() })

	run := &SquadRun{
		RunID:           "squad_test_skip_task",
		Status:          SquadStatusWaiting,
		Phase:           SquadPhaseWaitTaskApprove,
		OwnerSessionKey: "feishu:chat:user",
		RepoPath:        `G:\demo\repo`,
		TaskPrompt:      "示例需求",
		PlannerRole:     "jarvis",
		ExecutorRole:    "xingzou",
		ReviewerRole:    "jianzhu",
		PlanApproved:    true,
		CurrentTask:     0,
		TaskPendingID:   "task-1",
		MaxRework:       DefaultSquadMaxRework,
		Plan: SquadPlan{
			Title: "示例计划",
			Tasks: []SquadTask{
				{ID: "task-1", Title: "实现功能"},
				{ID: "task-2", Title: "补充测试"},
			},
		},
		RoleRuntime: map[string]SquadRoleRuntime{},
	}
	if err := engine.squadStore.SaveRun(run); err != nil {
		t.Fatalf("save run failed: %v", err)
	}
	engine.squadMu.Lock()
	engine.squadRuns[run.RunID] = func() {}
	engine.squadMu.Unlock()

	msg := &Message{
		SessionKey: "feishu:chat:user",
		Platform:   "feishu",
		UserID:     "user",
		UserName:   "u",
		ReplyCtx:   "ctx",
	}

	engine.cmdSquadSkipTask(platform, msg, []string{run.RunID, "该任务不需要执行"})

	updated, err := engine.squadStore.GetRun(run.RunID)
	if err != nil {
		t.Fatalf("load run after skip-task failed: %v", err)
	}
	if updated.CurrentTask != 1 {
		t.Fatalf("current task should move to next after skip, got=%d", updated.CurrentTask)
	}
	if updated.Phase != SquadPhaseExecuting || updated.Status != SquadStatusRunning {
		t.Fatalf("run should resume executing after skip, got status=%s phase=%s", updated.Status, updated.Phase)
	}
	if strings.TrimSpace(updated.TaskPendingID) != "" || strings.TrimSpace(updated.TaskApprovedID) != "" {
		t.Fatalf("task gate markers should be cleared after skip, pending=%s approved=%s", updated.TaskPendingID, updated.TaskApprovedID)
	}

	ckpts, err := engine.squadStore.LoadCheckpoints(run.RunID)
	if err != nil {
		t.Fatalf("load checkpoints failed: %v", err)
	}
	if len(ckpts) == 0 {
		t.Fatalf("expected skip checkpoint to be saved")
	}
	last := ckpts[len(ckpts)-1]
	if last.Verdict != "SKIPPED" {
		t.Fatalf("skip checkpoint verdict mismatch: %s", last.Verdict)
	}
	if len(last.Blockers) == 0 || !strings.Contains(last.Blockers[0], "不需要执行") {
		t.Fatalf("skip reason should be written into checkpoint blockers: %+v", last.Blockers)
	}
}

func TestCmdSquadJudgeReview_ReworkThenPass(t *testing.T) {
	platform := &stubPlatform{name: "feishu"}
	engine := NewEngine(
		"squad-cmd-test",
		&stubAgent{session: newStubAgentSession(make(chan Event))},
		[]Platform{platform},
		filepath.Join(t.TempDir(), "sessions", "sessions.json"),
		LangChinese,
	)
	t.Cleanup(func() { _ = engine.Stop() })

	run := &SquadRun{
		RunID:           "squad_test_review_judge",
		Status:          SquadStatusWaiting,
		Phase:           SquadPhaseWaitReviewJudge,
		OwnerSessionKey: "feishu:chat:user",
		RepoPath:        `G:\demo\repo`,
		TaskPrompt:      "示例需求",
		PlannerRole:     "jarvis",
		ExecutorRole:    "xingzou",
		ReviewerRole:    "jianzhu",
		PlanApproved:    true,
		CurrentTask:     0,
		TaskApprovedID:  "task-1",
		MaxRework:       DefaultSquadMaxRework,
		Plan: SquadPlan{
			Title: "示例计划",
			Tasks: []SquadTask{
				{ID: "task-1", Title: "实现功能"},
				{ID: "task-2", Title: "补充测试"},
			},
		},
		RoleRuntime: map[string]SquadRoleRuntime{},
	}
	cp := &SquadCheckpoint{
		TaskID:        "task-1",
		TaskTitle:     "实现功能",
		Round:         1,
		Verdict:       "PENDING_USER",
		ReviewResult:  "核心逻辑已实现但测试不足",
		Blockers:      []string{"缺少边界测试"},
		Suggestions:   []string{"补齐测试并附命令"},
		ExecutorReply: "done",
		ReviewerReply: "review",
	}
	if err := engine.squadStore.SaveRun(run); err != nil {
		t.Fatalf("save run failed: %v", err)
	}
	cpPath, err := engine.squadStore.SaveCheckpoint(run.RunID, cp)
	if err != nil {
		t.Fatalf("save checkpoint failed: %v", err)
	}
	run.ReviewPendingTask = "task-1"
	run.ReviewPendingRound = 1
	run.ReviewPendingCP = cpPath
	if err := engine.squadStore.SaveRun(run); err != nil {
		t.Fatalf("save run with pending review failed: %v", err)
	}

	// Force "already running" branch to avoid starting real subprocess in test.
	engine.squadMu.Lock()
	engine.squadRuns[run.RunID] = func() {}
	engine.squadMu.Unlock()

	msg := &Message{
		SessionKey: "feishu:chat:user",
		Platform:   "feishu",
		UserID:     "user",
		UserName:   "u",
		ReplyCtx:   "ctx",
	}

	reworkNote := "请补齐边界测试并给出可复现命令"
	engine.cmdSquadJudgeReview(platform, msg, []string{run.RunID, "rework", reworkNote})
	afterRework, err := engine.squadStore.GetRun(run.RunID)
	if err != nil {
		t.Fatalf("load run after rework failed: %v", err)
	}
	if afterRework.Phase != SquadPhaseExecuting || afterRework.Status != SquadStatusRunning {
		t.Fatalf("run should resume executing after rework, got status=%s phase=%s", afterRework.Status, afterRework.Phase)
	}
	if afterRework.TaskApprovedID != "task-1" {
		t.Fatalf("task approved id should stay on current task, got=%s", afterRework.TaskApprovedID)
	}
	if afterRework.ReworkCount != 1 {
		t.Fatalf("rework count should increase, got=%d", afterRework.ReworkCount)
	}
	if !strings.Contains(afterRework.UserReworkNote, "补齐边界测试") {
		t.Fatalf("user rework note should be preserved, got=%s", afterRework.UserReworkNote)
	}
	if strings.TrimSpace(afterRework.ReviewPendingTask) != "" {
		t.Fatalf("review pending task should be cleared after judge")
	}

	var cpAfterRework SquadCheckpoint
	raw, err := os.ReadFile(cpPath)
	if err != nil {
		t.Fatalf("read checkpoint failed: %v", err)
	}
	if err := json.Unmarshal(raw, &cpAfterRework); err != nil {
		t.Fatalf("decode checkpoint failed: %v", err)
	}
	if cpAfterRework.Verdict != "REWORK" {
		t.Fatalf("checkpoint verdict should be REWORK, got=%s", cpAfterRework.Verdict)
	}

	// Simulate next review checkpoint and judge PASS.
	cp2 := &SquadCheckpoint{
		TaskID:       "task-1",
		TaskTitle:    "实现功能",
		Round:        2,
		Verdict:      "PENDING_USER",
		ReviewResult: "实现满足要求",
	}
	cpPath2, err := engine.squadStore.SaveCheckpoint(run.RunID, cp2)
	if err != nil {
		t.Fatalf("save checkpoint2 failed: %v", err)
	}
	afterRework.Phase = SquadPhaseWaitReviewJudge
	afterRework.Status = SquadStatusWaiting
	afterRework.ReviewPendingTask = "task-1"
	afterRework.ReviewPendingRound = 2
	afterRework.ReviewPendingCP = cpPath2
	afterRework.TaskApprovedID = "task-1"
	if err := engine.squadStore.SaveRun(afterRework); err != nil {
		t.Fatalf("save run before pass failed: %v", err)
	}

	engine.cmdSquadJudgeReview(platform, msg, []string{run.RunID, "pass"})
	afterPass, err := engine.squadStore.GetRun(run.RunID)
	if err != nil {
		t.Fatalf("load run after pass failed: %v", err)
	}
	if afterPass.CurrentTask != 1 {
		t.Fatalf("current task should advance to next one, got=%d", afterPass.CurrentTask)
	}
	if afterPass.TaskApprovedID != "" {
		t.Fatalf("task approved id should be cleared after pass, got=%s", afterPass.TaskApprovedID)
	}
	if afterPass.ReworkCount != 0 {
		t.Fatalf("rework count should reset after pass, got=%d", afterPass.ReworkCount)
	}
	if strings.TrimSpace(afterPass.UserReworkNote) != "" {
		t.Fatalf("user rework note should be cleared after pass")
	}

	raw2, err := os.ReadFile(cpPath2)
	if err != nil {
		t.Fatalf("read checkpoint2 failed: %v", err)
	}
	var cpAfterPass SquadCheckpoint
	if err := json.Unmarshal(raw2, &cpAfterPass); err != nil {
		t.Fatalf("decode checkpoint2 failed: %v", err)
	}
	if cpAfterPass.Verdict != "PASS" {
		t.Fatalf("checkpoint2 verdict should be PASS, got=%s", cpAfterPass.Verdict)
	}
}

func lastPlatformReply(p *stubPlatform) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.sent) == 0 {
		return ""
	}
	return p.sent[len(p.sent)-1]
}

// Keep a tiny compile-time guard that test still sees context package
// used by shared stubs when test files are split across files.
var _ = context.Background
