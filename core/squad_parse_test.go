package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSquadStartOptions_Success(t *testing.T) {
	repo := t.TempDir()
	args := []string{
		"--repo", repo,
		"--planner", "jarvis",
		"--executor", "xingzou",
		"--reviewer", "jianzhu",
		"--provider", "codez",
		"请重构", "支付模块并补充测试",
	}

	opts, err := parseSquadStartOptions(args)
	if err != nil {
		t.Fatalf("parseSquadStartOptions failed: %v", err)
	}
	if filepath.Clean(opts.RepoPath) != filepath.Clean(repo) {
		t.Fatalf("repo mismatch: got=%s want=%s", opts.RepoPath, repo)
	}
	if opts.PlannerRole != "jarvis" || opts.ExecutorRole != "xingzou" || opts.ReviewerRole != "jianzhu" {
		t.Fatalf("role mismatch: %+v", opts)
	}
	if opts.Provider != "codez" {
		t.Fatalf("provider mismatch: %s", opts.Provider)
	}
	if opts.TaskPrompt == "" {
		t.Fatalf("task prompt should not be empty")
	}
	if opts.PlannerTimeoutSec != DefaultSquadPlannerTimeoutSec {
		t.Fatalf("planner timeout mismatch: got=%d want=%d", opts.PlannerTimeoutSec, DefaultSquadPlannerTimeoutSec)
	}
}

func TestParseSquadStartOptions_TaskFlagMultiWord(t *testing.T) {
	repo := t.TempDir()
	args := []string{
		"--repo", repo,
		"--task", "请重构", "用户模块", "并补充测试",
	}
	opts, err := parseSquadStartOptions(args)
	if err != nil {
		t.Fatalf("parseSquadStartOptions failed: %v", err)
	}
	if got := opts.TaskPrompt; got == "" || !strings.Contains(got, "用户模块") {
		t.Fatalf("unexpected task prompt: %q", got)
	}
}

func TestParseSquadStartOptions_PlannerTimeoutFlag(t *testing.T) {
	repo := t.TempDir()
	args := []string{
		"--repo", repo,
		"--planner-timeout", "1500",
		"重构任务",
	}
	opts, err := parseSquadStartOptions(args)
	if err != nil {
		t.Fatalf("parseSquadStartOptions failed: %v", err)
	}
	if opts.PlannerTimeoutSec != 1500 {
		t.Fatalf("planner timeout mismatch: got=%d want=1500", opts.PlannerTimeoutSec)
	}
}

func TestParseSquadStartOptions_PlannerTimeoutAlias(t *testing.T) {
	repo := t.TempDir()
	args := []string{
		"--repo", repo,
		"--planner-timeout-sec", "1200",
		"重构任务",
	}
	opts, err := parseSquadStartOptions(args)
	if err != nil {
		t.Fatalf("parseSquadStartOptions failed: %v", err)
	}
	if opts.PlannerTimeoutSec != 1200 {
		t.Fatalf("planner timeout mismatch: got=%d want=1200", opts.PlannerTimeoutSec)
	}
}

func TestParseSquadStartOptions_PlannerTimeoutInvalid(t *testing.T) {
	repo := t.TempDir()
	args := []string{
		"--repo", repo,
		"--planner-timeout", "30",
		"重构任务",
	}
	if _, err := parseSquadStartOptions(args); err == nil {
		t.Fatalf("expected planner-timeout range validation error")
	}
}

func TestParseSquadStartOptions_DuplicateRole(t *testing.T) {
	repo := t.TempDir()
	args := []string{
		"--repo", repo,
		"--planner", "jarvis",
		"--executor", "jarvis",
		"--reviewer", "jianzhu",
		"task",
	}
	if _, err := parseSquadStartOptions(args); err == nil {
		t.Fatalf("expected duplicate role error")
	}
}

func TestParseSquadPlan_JSON(t *testing.T) {
	reply := "```json\n{\"title\":\"重构计划\",\"overview\":\"先拆分模块\",\"tasks\":[{\"id\":\"task-1\",\"title\":\"拆接口\",\"objective\":\"解耦\",\"acceptance\":\"接口可独立测试\"}]}\n```"
	plan, err := parseSquadPlan(reply)
	if err != nil {
		t.Fatalf("parseSquadPlan failed: %v", err)
	}
	if plan.Title != "重构计划" {
		t.Fatalf("title mismatch: %s", plan.Title)
	}
	if len(plan.Tasks) != 1 || plan.Tasks[0].ID != "task-1" {
		t.Fatalf("tasks mismatch: %+v", plan.Tasks)
	}
}

func TestParseSquadPlan_Fallback(t *testing.T) {
	plan, err := parseSquadPlan("这是一个非 JSON 回复")
	if err != nil {
		t.Fatalf("fallback should not error: %v", err)
	}
	if len(plan.Tasks) == 0 {
		t.Fatalf("fallback should produce at least one task")
	}
}

func TestParseReviewerVerdict_JSON(t *testing.T) {
	reply := "{\"verdict\":\"REWORK\",\"blockers\":[\"缺少单元测试\"],\"suggestions\":[\"补充边界测试\"]}"
	verdict, blockers, suggestions := parseReviewerVerdict(reply)
	if verdict != "REWORK" {
		t.Fatalf("verdict mismatch: %s", verdict)
	}
	if len(blockers) != 1 || blockers[0] == "" {
		t.Fatalf("blockers mismatch: %+v", blockers)
	}
	if len(suggestions) != 1 || suggestions[0] == "" {
		t.Fatalf("suggestions mismatch: %+v", suggestions)
	}
}

func TestParseReviewerVerdict_TextFallback(t *testing.T) {
	verdict, blockers, _ := parseReviewerVerdict("结论：PASS。实现符合要求。")
	if verdict != "PASS" {
		t.Fatalf("expected PASS from text fallback, got %s", verdict)
	}
	if len(blockers) != 0 {
		t.Fatalf("unexpected blockers: %+v", blockers)
	}
}

func TestParseReviewerFindings_JSON(t *testing.T) {
	reply := "{\"review_result\":\"核心逻辑已实现，但边界场景缺失\",\"failed_reasons\":[\"缺少空输入测试\"],\"suggestions\":[\"补充单测并附命令\"]}"
	result, reasons, suggestions := parseReviewerFindings(reply)
	if !strings.Contains(result, "边界场景") {
		t.Fatalf("unexpected review_result: %s", result)
	}
	if len(reasons) != 1 || reasons[0] != "缺少空输入测试" {
		t.Fatalf("unexpected failed reasons: %+v", reasons)
	}
	if len(suggestions) != 1 || suggestions[0] != "补充单测并附命令" {
		t.Fatalf("unexpected suggestions: %+v", suggestions)
	}
}

func TestParseReviewerFindings_LegacyVerdictJSON(t *testing.T) {
	reply := "{\"verdict\":\"REWORK\",\"blockers\":[\"测试命令缺失\"],\"suggestions\":[\"补充可复现命令\"]}"
	result, reasons, suggestions := parseReviewerFindings(reply)
	if !strings.Contains(result, "审核发现问题") {
		t.Fatalf("unexpected review_result for legacy payload: %s", result)
	}
	if len(reasons) != 1 || reasons[0] != "测试命令缺失" {
		t.Fatalf("unexpected failed reasons: %+v", reasons)
	}
	if len(suggestions) != 1 || suggestions[0] != "补充可复现命令" {
		t.Fatalf("unexpected suggestions: %+v", suggestions)
	}
}

func TestParseReviewerFindings_TextFallback(t *testing.T) {
	result, reasons, _ := parseReviewerFindings("结论：当前实现有问题，需要补充回归测试。")
	if strings.TrimSpace(result) == "" {
		t.Fatalf("review_result should not be empty")
	}
	if len(reasons) == 0 {
		t.Fatalf("text fallback should provide at least one failed reason when text implies rework")
	}
}

func TestParseSquadStartOptions_InvalidRepo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-exist")
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("expect missing path in test")
	}
	args := []string{
		"--repo", missing,
		"task",
	}
	if _, err := parseSquadStartOptions(args); err == nil {
		t.Fatalf("expected error for missing repo path")
	}
}

func TestResolveSquadTaskSelection(t *testing.T) {
	run := &SquadRun{
		CurrentTask: 1,
		Plan: SquadPlan{
			Tasks: []SquadTask{
				{ID: "task-1", Title: "one"},
				{ID: "task-2", Title: "two"},
				{ID: "task-3", Title: "three"},
			},
		},
	}

	idx, task, err := resolveSquadTaskSelection(run, "current")
	if err != nil {
		t.Fatalf("resolve current failed: %v", err)
	}
	if idx != 1 || task.ID != "task-2" {
		t.Fatalf("unexpected current task: idx=%d task=%+v", idx, task)
	}

	idx, task, err = resolveSquadTaskSelection(run, "3")
	if err != nil {
		t.Fatalf("resolve by index failed: %v", err)
	}
	if idx != 2 || task.ID != "task-3" {
		t.Fatalf("unexpected index task: idx=%d task=%+v", idx, task)
	}

	idx, task, err = resolveSquadTaskSelection(run, "task-1")
	if err != nil {
		t.Fatalf("resolve by id failed: %v", err)
	}
	if idx != 0 || task.ID != "task-1" {
		t.Fatalf("unexpected id task: idx=%d task=%+v", idx, task)
	}
}

func TestRenderSquadPlanApprovalPreview(t *testing.T) {
	run := &SquadRun{
		RunID: "squad_xxx",
		Plan: SquadPlan{
			Title:    "测试计划",
			Overview: "这是一个计划摘要",
			Tasks: []SquadTask{
				{ID: "task-1", Title: "任务一", Objective: "目标一", Acceptance: "验收一"},
				{ID: "task-2", Title: "任务二", Objective: "目标二", Acceptance: "验收二"},
			},
		},
	}
	out := renderSquadPlanApprovalPreview(run, 6)
	if !strings.Contains(out, "测试计划") {
		t.Fatalf("preview should include title: %s", out)
	}
	if !strings.Contains(out, "[task-1] 任务一") || !strings.Contains(out, "[task-2] 任务二") {
		t.Fatalf("preview should include tasks: %s", out)
	}
}
