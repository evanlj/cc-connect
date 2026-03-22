package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	squadPromptMaxTransportRunes = 12000
	squadPromptRetryRunes        = 1800
)

var squadANSIColorPattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func (e *Engine) startSquadRunner(runID string) error {
	if e.squadStore == nil || !e.squadStore.Enabled() {
		return fmt.Errorf("squad store is disabled")
	}
	if e.squadProc == nil {
		return fmt.Errorf("squad process manager is unavailable")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return fmt.Errorf("run_id is required")
	}

	e.squadMu.Lock()
	if _, ok := e.squadRuns[runID]; ok {
		e.squadMu.Unlock()
		return fmt.Errorf("squad runner already running for %s", runID)
	}
	ctx, cancel := context.WithCancel(e.ctx)
	e.squadRuns[runID] = cancel
	e.squadMu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("squad runner panic", "run_id", runID, "panic", r)
				_ = e.failSquadRun(runID, fmt.Sprintf("runner panic: %v", r))
			}
			e.squadMu.Lock()
			delete(e.squadRuns, runID)
			e.squadMu.Unlock()
		}()
		e.runSquad(ctx, runID)
	}()
	return nil
}

func (e *Engine) stopSquadRunner(runID string) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return
	}
	e.squadMu.Lock()
	cancel := e.squadRuns[runID]
	delete(e.squadRuns, runID)
	e.squadMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *Engine) runSquad(ctx context.Context, runID string) {
	run, err := e.squadStore.GetRun(runID)
	if err != nil {
		slog.Error("squad: load run failed", "run_id", runID, "error", err)
		return
	}

	isStopped := func() bool {
		select {
		case <-ctx.Done():
			return true
		default:
			return false
		}
	}

	if run.Status == SquadStatusStopped || run.Phase == SquadPhaseStopped {
		return
	}
	if run.Status == SquadStatusCompleted || run.Phase == SquadPhaseCompleted {
		return
	}

	// phase 1: start role processes and produce plan.
	if run.Phase == "" || run.Phase == SquadPhaseBooting || run.Status == SquadStatusCreated {
		run.Status = SquadStatusRunning
		run.Phase = SquadPhaseBooting
		_ = e.squadStore.SaveRun(run)
		_ = e.squadStore.AppendEvent(run.RunID, "info", "booting role processes", map[string]any{
			"planner":  run.PlannerRole,
			"executor": run.ExecutorRole,
			"reviewer": run.ReviewerRole,
		})

		runtimeMap, bootErr := e.squadProc.StartRun(run, e.squadStore)
		if bootErr != nil {
			_ = e.failSquadRun(run.RunID, fmt.Sprintf("角色进程启动失败: %v", bootErr))
			return
		}
		run.RoleRuntime = runtimeMap
		run.Status = SquadStatusRunning
		run.Phase = SquadPhasePlanning
		if err := e.squadStore.SaveRun(run); err != nil {
			_ = e.failSquadRun(run.RunID, fmt.Sprintf("保存运行状态失败: %v", err))
			return
		}
		_ = e.squadStore.AppendEvent(run.RunID, "info", "role processes ready", nil)

		if isStopped() {
			return
		}
		if err := e.runSquadPlanning(ctx, run); err != nil {
			_ = e.failSquadRun(run.RunID, err.Error())
			return
		}
		return
	}

	// phase 2: waiting user approval.
	if run.Phase == SquadPhaseWaitPlanApprove && !run.PlanApproved {
		return
	}

	// phase 3: execution-review loop.
	if run.PlanApproved && (run.Phase == SquadPhaseExecuting || run.Phase == SquadPhaseReviewing || run.Phase == SquadPhaseWaitPlanApprove || run.Phase == SquadPhaseWaitTaskApprove || run.Phase == SquadPhaseWaitReviewJudge || run.Phase == SquadPhasePlanning) {
		if err := e.runSquadExecutionLoop(ctx, run); err != nil {
			_ = e.failSquadRun(run.RunID, err.Error())
			return
		}
		return
	}
}

func (e *Engine) runSquadPlanning(ctx context.Context, run *SquadRun) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}
	if strings.TrimSpace(run.TaskPrompt) == "" {
		return fmt.Errorf("task prompt is empty")
	}
	reply, _, err := e.askSquadRole(ctx, run, run.PlannerRole, buildSquadPlannerPrompt(run))
	if err != nil {
		return fmt.Errorf("planner ask failed: %w", err)
	}

	plan, err := parseSquadPlan(reply)
	if err != nil {
		return fmt.Errorf("parse planner output failed: %w", err)
	}
	run.Plan = plan
	run.Status = SquadStatusWaiting
	run.Phase = SquadPhaseWaitPlanApprove
	run.PlanApproved = false
	run.CurrentTask = 0
	run.CurrentRound = 0
	run.ReworkCount = 0
	if err := e.squadStore.SaveRun(run); err != nil {
		return fmt.Errorf("save run after planning: %w", err)
	}

	planMD := renderSquadPlanMarkdown(run)
	planPath, _ := e.squadStore.SavePlanMarkdown(run.RunID, planMD)
	_, _ = e.squadStore.SavePlanTasks(run.RunID, run.Plan.Tasks)
	_ = e.squadStore.AppendEvent(run.RunID, "info", "plan ready, waiting for approval", map[string]any{
		"task_count": len(run.Plan.Tasks),
		"plan_file":  planPath,
	})

	var b strings.Builder
	b.WriteString(fmt.Sprintf("【Squad】方案已生成，等待确认：`%s`\n", run.RunID))
	b.WriteString(fmt.Sprintf("- repo: `%s`\n", run.RepoPath))
	b.WriteString(fmt.Sprintf("- 任务数: `%d`\n", len(run.Plan.Tasks)))
	if strings.TrimSpace(planPath) != "" {
		b.WriteString(fmt.Sprintf("- plan_file: `%s`\n", filepath.ToSlash(planPath)))
	}
	preview := renderSquadPlanApprovalPreview(run, 6)
	if preview != "" {
		b.WriteString("\n\n")
		b.WriteString(preview)
	}
	b.WriteString(fmt.Sprintf("\n\n查看完整计划：`/squad show-plan %s`", run.RunID))
	b.WriteString(fmt.Sprintf("\n确认计划：`/squad approve-plan %s`", run.RunID))
	b.WriteString(fmt.Sprintf("\n说明：计划确认后，每个任务执行前都需要你确认：`/squad approve-task %s <task_id>`", run.RunID))
	_ = e.SendBySessionKey(run.OwnerSessionKey, b.String())
	return nil
}

func (e *Engine) runSquadExecutionLoop(ctx context.Context, run *SquadRun) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}
	if !run.PlanApproved {
		return fmt.Errorf("plan is not approved")
	}
	if len(run.Plan.Tasks) == 0 {
		return fmt.Errorf("plan has no tasks")
	}

	for run.CurrentTask < len(run.Plan.Tasks) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("run canceled")
		default:
		}

		task := run.Plan.Tasks[run.CurrentTask]
		if run.Phase == SquadPhaseWaitReviewJudge {
			if strings.EqualFold(strings.TrimSpace(run.ReviewPendingTask), task.ID) {
				run.Status = SquadStatusWaiting
				if err := e.squadStore.SaveRun(run); err != nil {
					return fmt.Errorf("save run before review judge waiting: %w", err)
				}
				return nil
			}
			// stale pending marker, clear and continue.
			run.ReviewPendingTask = ""
			run.ReviewPendingRound = 0
			run.ReviewPendingCP = ""
		}

		if !strings.EqualFold(strings.TrimSpace(run.TaskApprovedID), task.ID) {
			wasWaiting := run.Status == SquadStatusWaiting &&
				run.Phase == SquadPhaseWaitTaskApprove &&
				strings.EqualFold(strings.TrimSpace(run.TaskPendingID), task.ID)

			run.Status = SquadStatusWaiting
			run.Phase = SquadPhaseWaitTaskApprove
			run.TaskPendingID = task.ID
			if err := e.squadStore.SaveRun(run); err != nil {
				return fmt.Errorf("save run before task approval: %w", err)
			}
			if !wasWaiting {
				_ = e.squadStore.AppendEvent(run.RunID, "info", "task ready, waiting for approval", map[string]any{
					"task_id":    task.ID,
					"task_title": task.Title,
					"task_index": run.CurrentTask + 1,
					"task_total": len(run.Plan.Tasks),
				})
				var notify strings.Builder
				notify.WriteString(fmt.Sprintf("【Squad】等待任务确认：`%s`\n", run.RunID))
				notify.WriteString(renderSquadTaskDetail(run, run.CurrentTask))
				notify.WriteString(fmt.Sprintf("\n\n确认任务：`/squad approve-task %s %s`", run.RunID, task.ID))
				notify.WriteString(fmt.Sprintf("\n跳过任务：`/squad skip-task %s <跳过原因>`", run.RunID))
				notify.WriteString(fmt.Sprintf("\n查看计划：`/squad show-plan %s`", run.RunID))
				notify.WriteString(fmt.Sprintf("\n查看任务详情：`/squad show-task %s %s`", run.RunID, task.ID))
				_ = e.SendBySessionKey(run.OwnerSessionKey, strings.TrimSpace(notify.String()))
			}
			return nil
		}

		run.TaskPendingID = ""
		run.Status = SquadStatusRunning
		run.Phase = SquadPhaseExecuting
		run.CurrentRound++
		if err := e.squadStore.SaveRun(run); err != nil {
			return fmt.Errorf("save run before execute: %w", err)
		}

		execRound := run.ReworkCount + 1
		execReply, _, err := e.askSquadRole(ctx, run, run.ExecutorRole, buildSquadExecutorPrompt(run, task, execRound))
		if err != nil {
			return fmt.Errorf("executor ask failed (task=%s): %w", task.ID, err)
		}
		changedFiles, testResult := parseExecutorMeta(execReply)
		run.UserReworkNote = ""

		run.Phase = SquadPhaseReviewing
		if err := e.squadStore.SaveRun(run); err != nil {
			return fmt.Errorf("save run before review: %w", err)
		}
		reviewReply, _, err := e.askSquadRole(ctx, run, run.ReviewerRole, buildSquadReviewerPrompt(run, task, execReply))
		if err != nil {
			return fmt.Errorf("reviewer ask failed (task=%s): %w", task.ID, err)
		}

		reviewResult, failedReasons, suggestions := parseReviewerFindings(reviewReply)
		cp := &SquadCheckpoint{
			TaskID:        task.ID,
			TaskTitle:     task.Title,
			Round:         execRound,
			ExecutorReply: execReply,
			ReviewerReply: reviewReply,
			ReviewResult:  reviewResult,
			Verdict:       "PENDING_USER",
			Blockers:      failedReasons,
			Suggestions:   suggestions,
			ChangedFiles:  changedFiles,
			TestResult:    testResult,
		}
		cpPath, _ := e.squadStore.SaveCheckpoint(run.RunID, cp)
		_ = e.squadStore.AppendEvent(run.RunID, "info", "task reviewed", map[string]any{
			"task_id":              task.ID,
			"task_title":           task.Title,
			"round":                execRound,
			"review_result":        reviewResult,
			"failed_reasons_count": len(failedReasons),
			"checkpoint":           cpPath,
		})
		run.Status = SquadStatusWaiting
		run.Phase = SquadPhaseWaitReviewJudge
		run.ReviewPendingTask = task.ID
		run.ReviewPendingRound = execRound
		run.ReviewPendingCP = cpPath
		if err := e.squadStore.SaveRun(run); err != nil {
			return fmt.Errorf("save run before review judge waiting: %w", err)
		}

		var notify strings.Builder
		notify.WriteString(fmt.Sprintf("【Squad】任务 `%s` 第 %d 轮审核结果（待你裁决）\n", task.ID, execRound))
		notify.WriteString(fmt.Sprintf("- review_result: %s\n", truncateStr(reviewResult, 200)))
		if len(changedFiles) > 0 {
			notify.WriteString(fmt.Sprintf("- changed_files: %s\n", strings.Join(changedFiles, ", ")))
		}
		if strings.TrimSpace(testResult) != "" {
			notify.WriteString(fmt.Sprintf("- test: %s\n", truncateStr(testResult, 120)))
		}
		if len(failedReasons) > 0 {
			notify.WriteString(fmt.Sprintf("- failed_reasons: %s\n", strings.Join(failedReasons, "；")))
		}
		if len(suggestions) > 0 {
			notify.WriteString(fmt.Sprintf("- suggestions: %s\n", strings.Join(suggestions, "；")))
		}
		notify.WriteString(fmt.Sprintf("\n裁决通过并继续下一步：`/squad judge-review %s pass`", run.RunID))
		notify.WriteString(fmt.Sprintf("\n裁决不通过并给出原因/修改方案：`/squad judge-review %s rework <原因与修改方案>`", run.RunID))
		_ = e.SendBySessionKey(run.OwnerSessionKey, strings.TrimSpace(notify.String()))
		return nil
	}

	run.Status = SquadStatusRunning
	run.Phase = SquadPhaseFinalizing
	if err := e.squadStore.SaveRun(run); err != nil {
		return fmt.Errorf("save run before finalize: %w", err)
	}

	ckpts, _ := e.squadStore.LoadCheckpoints(run.RunID)
	report := renderSquadFinalReport(run, ckpts)
	reportPath, err := e.squadStore.SaveFinalReport(run.RunID, report)
	if err != nil {
		return fmt.Errorf("save final report: %w", err)
	}
	_ = e.squadStore.AppendEvent(run.RunID, "info", "run completed", map[string]any{
		"report": reportPath,
	})

	run.Status = SquadStatusCompleted
	run.Phase = SquadPhaseCompleted
	run.StopReason = ""
	run.ErrorMessage = ""
	if err := e.squadStore.SaveRun(run); err != nil {
		return fmt.Errorf("save completed run: %w", err)
	}

	_ = e.squadProc.StopRun(run.RunID)
	_ = e.SendBySessionKey(run.OwnerSessionKey, fmt.Sprintf("【Squad】任务完成：`%s`\n报告：`%s`", run.RunID, filepath.ToSlash(reportPath)))
	return nil
}

func (e *Engine) failSquadRun(runID, errMsg string) error {
	runID = strings.TrimSpace(runID)
	errMsg = strings.TrimSpace(errMsg)
	if runID == "" {
		return fmt.Errorf("run_id is required")
	}
	run, err := e.squadStore.GetRun(runID)
	if err != nil {
		return err
	}
	run.Status = SquadStatusFailed
	run.Phase = SquadPhaseFailed
	run.ErrorMessage = errMsg
	if saveErr := e.squadStore.SaveRun(run); saveErr != nil {
		return saveErr
	}
	_ = e.squadStore.AppendEvent(run.RunID, "error", "run failed", map[string]any{"error": errMsg})
	_ = e.squadProc.StopRun(run.RunID)
	_ = e.SendBySessionKey(run.OwnerSessionKey, fmt.Sprintf("【Squad】运行失败：`%s`\n原因：%s", run.RunID, errMsg))
	return nil
}

func (e *Engine) askSquadRole(ctx context.Context, run *SquadRun, role, prompt string) (string, int64, error) {
	timeoutSec := DefaultSquadRoleTimeoutSec
	if normalizeRoleToken(role) == normalizeRoleToken(run.PlannerRole) {
		timeoutSec = run.PlannerTimeoutSec
		if timeoutSec <= 0 {
			timeoutSec = DefaultSquadPlannerTimeoutSec
		}
	}
	prompt = sanitizeSquadPromptForTransport(flattenPromptForTransport(prompt))
	reply, latency, err := e.askSquadRoleOnce(ctx, run, role, prompt, timeoutSec)
	if err == nil {
		return reply, latency, nil
	}
	if shouldRetrySquadAskWithCompactPrompt(err) {
		compact := compactSquadPromptForRetry(prompt)
		if compact != "" && compact != prompt {
			_ = e.squadStore.AppendEvent(run.RunID, "warn", "ask retry with compact prompt", map[string]any{
				"role": role,
			})
			return e.askSquadRoleOnce(ctx, run, role, compact, timeoutSec)
		}
	}
	return "", 0, err
}

func (e *Engine) askSquadRoleOnce(ctx context.Context, run *SquadRun, role, prompt string, timeoutSec int) (string, int64, error) {
	if run == nil {
		return "", 0, fmt.Errorf("run is nil")
	}
	role = normalizeRoleToken(role)
	if role == "" {
		return "", 0, fmt.Errorf("role is required")
	}
	rt, ok := run.RoleRuntime[role]
	if !ok {
		return "", 0, fmt.Errorf("role runtime not found: %s", role)
	}
	socketPath := strings.TrimSpace(rt.SocketPath)
	if socketPath == "" {
		return "", 0, fmt.Errorf("socket path is empty for role %s", role)
	}
	if _, err := os.Stat(socketPath); err != nil {
		return "", 0, fmt.Errorf("socket not ready for role %s: %w", role, err)
	}

	req := AskRequest{
		Project:    rt.ProjectName,
		SessionKey: fmt.Sprintf("squad:%s:%s", run.RunID, role),
		Prompt:     prompt,
		TimeoutSec: timeoutSec,
	}
	res, err := e.instanceCli.Ask(ctx, socketPath, req)
	if err != nil {
		return "", 0, err
	}
	return strings.TrimSpace(res.Content), res.LatencyMS, nil
}

func parseExecutorMeta(reply string) ([]string, string) {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return nil, ""
	}
	rawJSON := extractFirstJSONObject(reply)
	if rawJSON == "" {
		return nil, ""
	}
	var payload struct {
		ChangedFiles []string `json:"changed_files"`
		TestResult   string   `json:"test_result"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &payload); err != nil {
		return nil, ""
	}
	return normalizeStringList(payload.ChangedFiles), strings.TrimSpace(payload.TestResult)
}

func sanitizeSquadPromptForTransport(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	prompt = squadANSIColorPattern.ReplaceAllString(prompt, "")
	var b strings.Builder
	b.Grow(len(prompt))
	for _, r := range prompt {
		if r == '\t' {
			b.WriteByte(' ')
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if utf8.RuneCountInString(out) > squadPromptMaxTransportRunes {
		out = truncateStr(out, squadPromptMaxTransportRunes)
	}
	return out
}

func shouldRetrySquadAskWithCompactPrompt(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "filename, directory name") {
		return true
	}
	if strings.Contains(msg, "volume label syntax") {
		return true
	}
	return false
}

func compactSquadPromptForRetry(prompt string) string {
	prompt = sanitizeSquadPromptForTransport(prompt)
	if prompt == "" {
		return ""
	}
	if utf8.RuneCountInString(prompt) <= squadPromptRetryRunes {
		return prompt
	}
	return truncateStr(prompt, squadPromptRetryRunes)
}

func buildSquadPlannerPrompt(run *SquadRun) string {
	var b strings.Builder
	b.WriteString("你是方案师（Planner）。请基于需求输出可执行任务拆解。\n")
	b.WriteString(fmt.Sprintf("仓库路径：%s\n", run.RepoPath))
	b.WriteString(fmt.Sprintf("需求：%s\n\n", run.TaskPrompt))
	b.WriteString("要求：\n")
	b.WriteString("1) 任务必须可执行、可审核；\n")
	b.WriteString("2) 每个任务要有验收标准；\n")
	b.WriteString("3) 若能给测试命令，请填写 test_command；\n")
	b.WriteString("4) 允许你先扫描项目再给计划，但要避免无关信息；\n")
	b.WriteString("5) 任务数量控制在 3~8 项。\n\n")
	b.WriteString("仅输出 JSON（不要额外解释），格式：\n")
	b.WriteString("{\n")
	b.WriteString("  \"title\": \"\",\n")
	b.WriteString("  \"overview\": \"\",\n")
	b.WriteString("  \"tasks\": [\n")
	b.WriteString("    {\n")
	b.WriteString("      \"id\": \"task-1\",\n")
	b.WriteString("      \"title\": \"\",\n")
	b.WriteString("      \"objective\": \"\",\n")
	b.WriteString("      \"acceptance\": \"\",\n")
	b.WriteString("      \"test_command\": \"\",\n")
	b.WriteString("      \"depends_on\": []\n")
	b.WriteString("    }\n")
	b.WriteString("  ]\n")
	b.WriteString("}\n")
	return b.String()
}

func buildSquadExecutorPrompt(run *SquadRun, task SquadTask, round int) string {
	var b strings.Builder
	b.WriteString("你是执行者（Executor）。请按以下任务在代码库中实现。\n")
	b.WriteString(fmt.Sprintf("仓库路径：%s\n", run.RepoPath))
	b.WriteString(fmt.Sprintf("任务ID：%s\n", task.ID))
	b.WriteString(fmt.Sprintf("任务标题：%s\n", task.Title))
	if strings.TrimSpace(task.Objective) != "" {
		b.WriteString(fmt.Sprintf("目标：%s\n", task.Objective))
	}
	if strings.TrimSpace(task.Acceptance) != "" {
		b.WriteString(fmt.Sprintf("验收：%s\n", task.Acceptance))
	}
	if strings.TrimSpace(task.TestCommand) != "" {
		b.WriteString(fmt.Sprintf("建议测试命令：%s\n", task.TestCommand))
	}
	b.WriteString(fmt.Sprintf("当前轮次：第 %d 轮\n\n", round))
	if strings.TrimSpace(run.UserReworkNote) != "" {
		b.WriteString("用户本轮裁决为不通过，并给出了返工方向。请优先按以下要求修改：\n")
		b.WriteString(strings.TrimSpace(run.UserReworkNote))
		b.WriteString("\n\n")
	}
	b.WriteString("输出要求：\n")
	b.WriteString("A) 简述你做了什么、关键修改点、测试结果。\n")
	b.WriteString("B) 附带 JSON：\n")
	b.WriteString("{\"changed_files\":[\"path/a\",\"path/b\"],\"test_result\":\"...\"}\n")
	return b.String()
}

func buildSquadReviewerPrompt(run *SquadRun, task SquadTask, executorReply string) string {
	var b strings.Builder
	b.WriteString("你是代码审核者（Reviewer）。请审核执行者本轮产出，但不要替用户做“通过/不通过”裁决。\n")
	b.WriteString(fmt.Sprintf("仓库路径：%s\n", run.RepoPath))
	b.WriteString(fmt.Sprintf("任务ID：%s\n", task.ID))
	b.WriteString(fmt.Sprintf("任务标题：%s\n", task.Title))
	if strings.TrimSpace(task.Acceptance) != "" {
		b.WriteString(fmt.Sprintf("验收标准：%s\n", task.Acceptance))
	}
	b.WriteString("\n执行者输出如下：\n")
	b.WriteString(truncateStr(executorReply, 4000))
	b.WriteString("\n\n")
	b.WriteString("请只输出 JSON（不要额外解释）：\n")
	b.WriteString("{\"review_result\":\"\",\"failed_reasons\":[\"...\"],\"suggestions\":[\"...\"]}\n")
	b.WriteString("规则：\n")
	b.WriteString("1) 仅基于当前任务目标和验收标准审核，避免无关项；\n")
	b.WriteString("2) 不要输出 PASS/REWORK 或“最终是否通过”的裁决；\n")
	b.WriteString("3) failed_reasons 仅写导致“不通过”的具体问题；若未发现不通过项请返回空数组。")
	return b.String()
}

func renderSquadFinalReport(run *SquadRun, checkpoints []SquadCheckpoint) string {
	var b strings.Builder
	b.WriteString("# Squad 最终交付报告\n\n")
	b.WriteString(fmt.Sprintf("- run_id: `%s`\n", run.RunID))
	b.WriteString(fmt.Sprintf("- repo: `%s`\n", run.RepoPath))
	b.WriteString(fmt.Sprintf("- planner: `%s`\n", run.PlannerRole))
	b.WriteString(fmt.Sprintf("- executor: `%s`\n", run.ExecutorRole))
	b.WriteString(fmt.Sprintf("- reviewer: `%s`\n", run.ReviewerRole))
	b.WriteString(fmt.Sprintf("- completed_at: `%s`\n", time.Now().Format(time.RFC3339)))
	b.WriteString("\n## 原始需求\n\n")
	b.WriteString(strings.TrimSpace(run.TaskPrompt) + "\n\n")
	b.WriteString("## 方案\n\n")
	b.WriteString(fmt.Sprintf("### %s\n\n", emptyAs(run.Plan.Title, "执行方案")))
	b.WriteString(emptyAs(run.Plan.Overview, "（无）") + "\n\n")
	b.WriteString("## 任务执行与审核记录\n\n")
	if len(checkpoints) == 0 {
		b.WriteString("- （无 checkpoint）\n")
		return strings.TrimSpace(b.String())
	}
	sortSquadCheckpoints(checkpoints)
	for _, cp := range checkpoints {
		b.WriteString(fmt.Sprintf("### %s / 第 %d 轮 / 用户裁决 `%s`\n\n", emptyAs(cp.TaskTitle, cp.TaskID), cp.Round, emptyAs(strings.TrimSpace(cp.Verdict), "PENDING_USER")))
		if strings.TrimSpace(cp.ReviewResult) != "" {
			b.WriteString(fmt.Sprintf("- review_result: %s\n", truncateStr(cp.ReviewResult, 220)))
		}
		if len(cp.ChangedFiles) > 0 {
			b.WriteString(fmt.Sprintf("- changed_files: %s\n", strings.Join(cp.ChangedFiles, ", ")))
		}
		if strings.TrimSpace(cp.TestResult) != "" {
			b.WriteString(fmt.Sprintf("- test_result: %s\n", truncateStr(cp.TestResult, 200)))
		}
		if len(cp.Blockers) > 0 {
			b.WriteString(fmt.Sprintf("- blockers: %s\n", strings.Join(cp.Blockers, "；")))
		}
		if len(cp.Suggestions) > 0 {
			b.WriteString(fmt.Sprintf("- suggestions: %s\n", strings.Join(cp.Suggestions, "；")))
		}
		b.WriteString("\n")
	}
	passCount := 0
	for _, cp := range checkpoints {
		if strings.EqualFold(cp.Verdict, "PASS") {
			passCount++
		}
	}
	b.WriteString("## 汇总\n\n")
	b.WriteString(fmt.Sprintf("- checkpoints: `%d`\n", len(checkpoints)))
	b.WriteString(fmt.Sprintf("- pass_count: `%d`\n", passCount))
	return strings.TrimSpace(b.String())
}

func sortSquadCheckpoints(in []SquadCheckpoint) {
	if len(in) <= 1 {
		return
	}
	// Stable by created_at then task_id then round.
	for i := 0; i < len(in)-1; i++ {
		for j := i + 1; j < len(in); j++ {
			swap := false
			if in[i].CreatedAt.After(in[j].CreatedAt) {
				swap = true
			} else if in[i].CreatedAt.Equal(in[j].CreatedAt) {
				if strings.Compare(in[i].TaskID, in[j].TaskID) > 0 {
					swap = true
				} else if in[i].TaskID == in[j].TaskID && in[i].Round > in[j].Round {
					swap = true
				}
			}
			if swap {
				in[i], in[j] = in[j], in[i]
			}
		}
	}
}
