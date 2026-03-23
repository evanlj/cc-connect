package core

import (
	"strings"
	"testing"
)

func TestBuildSquadReviewerPrompt_ContainsScopeGuards(t *testing.T) {
	run := &SquadRun{
		RepoPath: "G:/AgeAction/AgeActionExample",
	}
	task := SquadTask{
		ID:         "task-5",
		Title:      "交付回归与证据固化",
		Acceptance: "文档补证 + 变更面收口",
	}
	prompt := buildSquadReviewerPrompt(
		run,
		task,
		"执行者输出",
		[]string{
			"Assets/AGE/Action/Events/Tick/SetParent.cs",
			"Doc/新增动作结点-SetParent-父子挂接.md",
		},
		"EditMode Passed 11/11",
	)

	mustContains := []string{
		"审核权限硬门禁（A规则，必须遵守）",
		"仅审核“代码实现 + Doc 文档”是否符合当前任务计划",
		"`[ACC:验收点] [FILE:文件路径] 问题描述`",
		"Assets/AGE/Action/Events/Tick/SetParent.cs",
		"Doc/新增动作结点-SetParent-父子挂接.md",
		"执行者测试结论：EditMode Passed 11/11",
	}
	for _, item := range mustContains {
		if !strings.Contains(prompt, item) {
			t.Fatalf("prompt should contain %q, got: %s", item, prompt)
		}
	}
}

func TestFilterReviewerFindingsByScope_DropsNoiseAndOutOfScope(t *testing.T) {
	task := SquadTask{
		ID:         "task-5",
		Title:      "交付回归",
		Acceptance: "文档补证 + 变更面收口",
	}
	changedFiles := []string{
		"Assets/AGE/Action/Events/Tick/SetParent.cs",
		"Doc/新增动作结点-SetParent-父子挂接.md",
	}
	reviewResult := "审核发现问题（建议返工，由用户最终裁决）"
	failedReasons := []string{
		"[ACC:日志检查] [FILE:Logs/editmode-tests.log] Android SDK command-line tools component is not found",
		"[ACC:文档补证] 缺少测试结果路径",
		"[ACC:文档补证] [FILE:Doc/新增动作结点-SetParent-父子挂接.md] 缺少 task-5 最终回归证据小节",
		"[ACC:代码实现] [FILE:Assets/Other.cs] 不是当前任务变更文件",
	}
	suggestions := []string{
		"排查 Android SDK command-line tools",
		"补齐文档 9.3 小节并说明证据路径",
	}

	gotResult, gotReasons, gotSuggestions, stats := filterReviewerFindingsByScope(task, changedFiles, reviewResult, failedReasons, suggestions)
	if gotResult == "" {
		t.Fatalf("review_result should not be empty")
	}
	if len(gotReasons) != 1 {
		t.Fatalf("expected 1 in-scope reason, got %d: %+v", len(gotReasons), gotReasons)
	}
	if !strings.Contains(gotReasons[0], "[FILE:Doc/新增动作结点-SetParent-父子挂接.md]") {
		t.Fatalf("unexpected in-scope reason: %s", gotReasons[0])
	}
	if len(gotSuggestions) != 1 || !strings.Contains(gotSuggestions[0], "补齐文档") {
		t.Fatalf("unexpected suggestions after filter: %+v", gotSuggestions)
	}
	if stats.DroppedTotal != 3 || stats.DroppedNoise != 1 || stats.DroppedUnanchored != 1 || stats.DroppedOutOfScope != 1 {
		t.Fatalf("unexpected filter stats: %+v", stats)
	}
}

func TestFilterReviewerFindingsByScope_AllDroppedFallbackToNonBlocking(t *testing.T) {
	task := SquadTask{ID: "task-5", Acceptance: "仅审计划一致性"}
	changedFiles := []string{"Doc/实现说明.md"}
	reviewResult := "审核发现问题（建议返工，由用户最终裁决）"
	failedReasons := []string{
		"[ACC:日志] [FILE:Logs/editmode-tests.log] Licensing::Module Error: Access token is unavailable",
		"[ACC:文档] 缺少文件锚点",
	}

	gotResult, gotReasons, _, stats := filterReviewerFindingsByScope(task, changedFiles, reviewResult, failedReasons, nil)
	if len(gotReasons) != 0 {
		t.Fatalf("all failed reasons should be filtered out, got %+v", gotReasons)
	}
	if !strings.Contains(gotResult, "未发现可阻塞的计划偏差") {
		t.Fatalf("review_result should downgrade to non-blocking fallback, got: %s", gotResult)
	}
	if stats.DroppedTotal != 2 {
		t.Fatalf("expected dropped_total=2, got %+v", stats)
	}
}
