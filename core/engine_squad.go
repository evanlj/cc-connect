package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) cmdSquad(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese
	if e.squadStore == nil || !e.squadStore.Enabled() {
		if isZh {
			e.reply(p, msg.ReplyCtx, "❌ squad 功能未启用：缺少可用的数据目录。")
		} else {
			e.reply(p, msg.ReplyCtx, "❌ Squad is not enabled: data directory is unavailable.")
		}
		return
	}
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, e.squadUsage(isZh))
		return
	}

	sub := strings.ToLower(strings.TrimSpace(args[0]))
	switch sub {
	case "start":
		e.cmdSquadStart(p, msg, args[1:])
	case "approve-plan":
		e.cmdSquadApprovePlan(p, msg, args[1:])
	case "approve-task":
		e.cmdSquadApproveTask(p, msg, args[1:])
	case "skip-task":
		e.cmdSquadSkipTask(p, msg, args[1:])
	case "judge-review":
		e.cmdSquadJudgeReview(p, msg, args[1:])
	case "show-plan":
		e.cmdSquadShowPlan(p, msg, args[1:])
	case "show-task":
		e.cmdSquadShowTask(p, msg, args[1:])
	case "status":
		e.cmdSquadStatus(p, msg, args[1:])
	case "stop":
		e.cmdSquadStop(p, msg, args[1:])
	case "list":
		e.cmdSquadList(p, msg)
	case "help", "-h", "--help":
		e.reply(p, msg.ReplyCtx, e.squadUsage(isZh))
	default:
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 不支持的子命令：`%s`\n\n%s", sub, e.squadUsage(true)))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Unsupported subcommand: `%s`\n\n%s", sub, e.squadUsage(false)))
		}
	}
}

func (e *Engine) cmdSquadStart(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese
	opts, err := parseSquadStartOptions(args)
	if err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 参数错误：%v\n\n%s", err, e.squadUsage(true)))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Invalid arguments: %v\n\n%s", err, e.squadUsage(false)))
		}
		return
	}

	run := &SquadRun{
		RunID:             newSquadRunID(time.Now()),
		Status:            SquadStatusCreated,
		Phase:             SquadPhaseBooting,
		OwnerSessionKey:   msg.SessionKey,
		RepoPath:          opts.RepoPath,
		TaskPrompt:        opts.TaskPrompt,
		Provider:          opts.Provider,
		PlannerTimeoutSec: opts.PlannerTimeoutSec,
		PlannerRole:       opts.PlannerRole,
		ExecutorRole:      opts.ExecutorRole,
		ReviewerRole:      opts.ReviewerRole,
		MaxRework:         DefaultSquadMaxRework,
		RoleRuntime:       map[string]SquadRoleRuntime{},
	}
	if err := e.squadStore.SaveRun(run); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 创建 squad run 失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to create squad run: %v", err))
		}
		return
	}
	_ = e.squadStore.AppendEvent(run.RunID, "info", "run created", map[string]any{
		"repo":     run.RepoPath,
		"planner":  run.PlannerRole,
		"executor": run.ExecutorRole,
		"reviewer": run.ReviewerRole,
	})

	if err := e.startSquadRunner(run.RunID); err != nil {
		_ = e.failSquadRun(run.RunID, "runner start failed: "+err.Error())
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ run 已创建，但启动失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Run created but failed to start: %v", err))
		}
		return
	}

	if isZh {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(
			"✅ 已创建 squad run：`%s`\n- repo: `%s`\n- planner: `%s`\n- planner_timeout: `%ds`\n- executor: `%s`\n- reviewer: `%s`\n\n正在动态启动三角色并生成方案。你会先看到完整计划并确认（`/squad approve-plan %s`），之后每个任务执行前还要逐项确认（`/squad approve-task %s <task_id>`）。",
			run.RunID, run.RepoPath, run.PlannerRole, run.PlannerTimeoutSec, run.ExecutorRole, run.ReviewerRole, run.RunID, run.RunID,
		))
		return
	}
	e.reply(p, msg.ReplyCtx, fmt.Sprintf(
		"✅ Squad run created: `%s`\n- repo: `%s`\n- planner: `%s`\n- planner_timeout: `%ds`\n- executor: `%s`\n- reviewer: `%s`\n\nBooting role processes and generating plan. You'll review and approve the plan first (`/squad approve-plan %s`), then approve each task before execution (`/squad approve-task %s <task_id>`).",
		run.RunID, run.RepoPath, run.PlannerRole, run.PlannerTimeoutSec, run.ExecutorRole, run.ReviewerRole, run.RunID, run.RunID,
	))
}

func (e *Engine) cmdSquadApprovePlan(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese
	if len(args) < 1 {
		if isZh {
			e.reply(p, msg.ReplyCtx, "用法：`/squad approve-plan <run_id>`")
		} else {
			e.reply(p, msg.ReplyCtx, "Usage: `/squad approve-plan <run_id>`")
		}
		return
	}
	runID := strings.TrimSpace(args[0])
	run, err := e.squadStore.GetRun(runID)
	if err != nil {
		if os.IsNotExist(err) {
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 未找到 run：`%s`", runID))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Run not found: `%s`", runID))
			}
			return
		}
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 读取 run 失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to load run: %v", err))
		}
		return
	}
	if run.Status == SquadStatusCompleted {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("ℹ️ run `%s` 已完成。", runID))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("ℹ️ run `%s` is already completed.", runID))
		}
		return
	}
	if run.Status == SquadStatusStopped {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ run `%s` 已停止，不能再批准。", runID))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ run `%s` is stopped and cannot be approved.", runID))
		}
		return
	}
	if run.Phase != SquadPhaseWaitPlanApprove && !strings.EqualFold(run.Status, SquadStatusWaiting) {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 当前阶段为 `%s`，不在等待方案确认阶段。", run.Phase))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Current phase is `%s`, not waiting for plan approval.", run.Phase))
		}
		return
	}

	run.PlanApproved = true
	run.Status = SquadStatusWaiting
	run.Phase = SquadPhaseWaitTaskApprove
	run.TaskApprovedID = ""
	run.TaskPendingID = ""
	run.StopReason = ""
	run.ErrorMessage = ""
	if err := e.squadStore.SaveRun(run); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 更新 run 失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to update run: %v", err))
		}
		return
	}
	_ = e.squadStore.AppendEvent(run.RunID, "info", "plan approved", nil)
	if err := e.startSquadRunner(run.RunID); err != nil {
		// if already running, that's fine
		if !strings.Contains(strings.ToLower(err.Error()), "already running") {
			_ = e.failSquadRun(run.RunID, err.Error())
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 启动执行失败：%v", err))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to resume run: %v", err))
			}
			return
		}
	}
	if isZh {
		nextTask := ""
		if run.CurrentTask >= 0 && run.CurrentTask < len(run.Plan.Tasks) {
			nextTask = run.Plan.Tasks[run.CurrentTask].ID
		}
		msgText := fmt.Sprintf("✅ 已批准方案：`%s`\n", run.RunID)
		if nextTask != "" {
			msgText += fmt.Sprintf("下一步请确认任务：`/squad approve-task %s %s`\n", run.RunID, nextTask)
		}
		msgText += fmt.Sprintf("你也可先查看：`/squad show-plan %s` / `/squad show-task %s current`", run.RunID, run.RunID)
		e.reply(p, msg.ReplyCtx, strings.TrimSpace(msgText))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ Plan approved: `%s`\nNext, confirm a task with `/squad approve-task %s <task_id>`.", run.RunID, run.RunID))
	}
}

func (e *Engine) cmdSquadApproveTask(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese
	if len(args) < 1 {
		if isZh {
			e.reply(p, msg.ReplyCtx, "用法：`/squad approve-task <run_id> [task_id|task_index|current]`")
		} else {
			e.reply(p, msg.ReplyCtx, "Usage: `/squad approve-task <run_id> [task_id|task_index|current]`")
		}
		return
	}
	runID := strings.TrimSpace(args[0])
	run, err := e.squadStore.GetRun(runID)
	if err != nil {
		if os.IsNotExist(err) {
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 未找到 run：`%s`", runID))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Run not found: `%s`", runID))
			}
			return
		}
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 读取 run 失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to load run: %v", err))
		}
		return
	}
	if !run.PlanApproved {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ run `%s` 的计划尚未确认。先执行 `/squad approve-plan %s`。", run.RunID, run.RunID))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Plan not approved for `%s` yet. Run `/squad approve-plan %s` first.", run.RunID, run.RunID))
		}
		return
	}
	if run.CurrentTask < 0 || run.CurrentTask >= len(run.Plan.Tasks) {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("ℹ️ run `%s` 当前没有待执行任务。", run.RunID))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("ℹ️ run `%s` has no pending task.", run.RunID))
		}
		return
	}

	selection := "current"
	if len(args) > 1 {
		selection = strings.TrimSpace(args[1])
	}
	idx, task, resolveErr := resolveSquadTaskSelection(run, selection)
	if resolveErr != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 无法解析任务：%v", resolveErr))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to resolve task: %v", resolveErr))
		}
		return
	}
	if idx != run.CurrentTask {
		cur := run.Plan.Tasks[run.CurrentTask]
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 只能确认当前待执行任务：`%s`（当前是第 %d/%d 项）。", cur.ID, run.CurrentTask+1, len(run.Plan.Tasks)))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Only current task can be approved now: `%s` (currently %d/%d).", cur.ID, run.CurrentTask+1, len(run.Plan.Tasks)))
		}
		return
	}

	run.TaskApprovedID = task.ID
	run.TaskPendingID = ""
	run.Status = SquadStatusRunning
	run.Phase = SquadPhaseExecuting
	run.StopReason = ""
	run.ErrorMessage = ""
	if err := e.squadStore.SaveRun(run); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 更新 run 失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to update run: %v", err))
		}
		return
	}
	_ = e.squadStore.AppendEvent(run.RunID, "info", "task approved", map[string]any{
		"task_id":    task.ID,
		"task_title": task.Title,
		"task_index": idx + 1,
		"task_total": len(run.Plan.Tasks),
	})
	if err := e.startSquadRunner(run.RunID); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "already running") {
			_ = e.failSquadRun(run.RunID, err.Error())
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 任务确认后启动执行失败：%v", err))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to resume run after task approval: %v", err))
			}
			return
		}
	}
	if isZh {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ 已确认任务 `%s`（%d/%d），开始执行。", task.ID, idx+1, len(run.Plan.Tasks)))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ Task `%s` approved (%d/%d), execution resumed.", task.ID, idx+1, len(run.Plan.Tasks)))
	}
}

func (e *Engine) cmdSquadSkipTask(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese
	if len(args) < 1 {
		if isZh {
			e.reply(p, msg.ReplyCtx, "用法：`/squad skip-task <run_id> [跳过原因]`")
		} else {
			e.reply(p, msg.ReplyCtx, "Usage: `/squad skip-task <run_id> [reason]`")
		}
		return
	}

	runID := strings.TrimSpace(args[0])
	reason := "用户手动跳过该任务"
	if len(args) > 1 {
		if custom := strings.TrimSpace(strings.Join(args[1:], " ")); custom != "" {
			reason = custom
		}
	}

	run, err := e.squadStore.GetRun(runID)
	if err != nil {
		if os.IsNotExist(err) {
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 未找到 run：`%s`", runID))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Run not found: `%s`", runID))
			}
			return
		}
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 读取 run 失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to load run: %v", err))
		}
		return
	}
	if !run.PlanApproved {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ run `%s` 的计划尚未确认。先执行 `/squad approve-plan %s`。", run.RunID, run.RunID))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Plan not approved for `%s` yet. Run `/squad approve-plan %s` first.", run.RunID, run.RunID))
		}
		return
	}
	if run.CurrentTask < 0 || run.CurrentTask >= len(run.Plan.Tasks) {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("ℹ️ run `%s` 当前没有可跳过任务。", run.RunID))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("ℹ️ run `%s` has no skippable task now.", run.RunID))
		}
		return
	}
	if run.Phase == SquadPhaseWaitReviewJudge {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 当前处于审核裁决阶段。请使用 `/squad judge-review %s pass` 推进下一步。", run.RunID))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Run is waiting review decision. Use `/squad judge-review %s pass` to continue.", run.RunID))
		}
		return
	}
	if run.Phase != SquadPhaseWaitTaskApprove {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 仅在等待任务确认阶段可跳过（当前 phase=`%s`）。", run.Phase))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Task can only be skipped in wait_task_approval phase (current=`%s`).", run.Phase))
		}
		return
	}

	taskIndex := run.CurrentTask
	task := run.Plan.Tasks[taskIndex]
	round := run.ReworkCount + 1
	if round <= 0 {
		round = 1
	}
	cp := &SquadCheckpoint{
		TaskID:        task.ID,
		TaskTitle:     task.Title,
		Round:         round,
		ExecutorReply: "[SKIPPED] task skipped before execution by user",
		ReviewerReply: "",
		ReviewResult:  "任务在执行前由用户跳过",
		Verdict:       "SKIPPED",
		Blockers:      []string{reason},
	}
	cpPath, _ := e.squadStore.SaveCheckpoint(run.RunID, cp)

	run.CurrentTask++
	run.CurrentRound++
	run.ReworkCount = 0
	run.TaskApprovedID = ""
	run.TaskPendingID = ""
	run.ReviewPendingTask = ""
	run.ReviewPendingRound = 0
	run.ReviewPendingCP = ""
	run.UserReworkNote = ""
	run.Status = SquadStatusRunning
	run.Phase = SquadPhaseExecuting
	run.StopReason = ""
	run.ErrorMessage = ""
	if err := e.squadStore.SaveRun(run); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 更新 run 失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to update run: %v", err))
		}
		return
	}
	_ = e.squadStore.AppendEvent(run.RunID, "warn", "task skipped by user", map[string]any{
		"task_id":    task.ID,
		"task_title": task.Title,
		"task_index": taskIndex + 1,
		"task_total": len(run.Plan.Tasks),
		"reason":     truncateStr(reason, 220),
		"checkpoint": cpPath,
	})
	if err := e.startSquadRunner(run.RunID); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "already running") {
			_ = e.failSquadRun(run.RunID, err.Error())
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 跳过任务后恢复执行失败：%v", err))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to resume run after skip-task: %v", err))
			}
			return
		}
	}
	if isZh {
		msgText := fmt.Sprintf("⏭️ 已跳过任务 `%s`，原因：%s\n", task.ID, truncateStr(reason, 160))
		if run.CurrentTask < len(run.Plan.Tasks) {
			nextTask := run.Plan.Tasks[run.CurrentTask].ID
			msgText += fmt.Sprintf("下一步请确认任务：`/squad approve-task %s %s`", run.RunID, nextTask)
		} else {
			msgText += "已无剩余任务，系统将进入汇总阶段。"
		}
		e.reply(p, msg.ReplyCtx, strings.TrimSpace(msgText))
		return
	}
	e.reply(p, msg.ReplyCtx, fmt.Sprintf("⏭️ Task `%s` skipped. Reason: %s", task.ID, truncateStr(reason, 120)))
}

func (e *Engine) cmdSquadJudgeReview(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese
	if len(args) < 2 {
		if isZh {
			e.reply(p, msg.ReplyCtx, "用法：`/squad judge-review <run_id> pass|rework [原因与修改方案]`")
		} else {
			e.reply(p, msg.ReplyCtx, "Usage: `/squad judge-review <run_id> pass|rework [reason_and_plan]`")
		}
		return
	}

	runID := strings.TrimSpace(args[0])
	decision := strings.ToLower(strings.TrimSpace(args[1]))
	if decision != "pass" && decision != "rework" {
		if isZh {
			e.reply(p, msg.ReplyCtx, "❌ 裁决仅支持 `pass` 或 `rework`。")
		} else {
			e.reply(p, msg.ReplyCtx, "❌ Decision must be `pass` or `rework`.")
		}
		return
	}
	userNote := ""
	if len(args) > 2 {
		userNote = strings.TrimSpace(strings.Join(args[2:], " "))
	}
	if decision == "rework" && userNote == "" {
		if isZh {
			e.reply(p, msg.ReplyCtx, "❌ 选择 `rework` 时，必须附带“不通过原因与修改方案”。")
		} else {
			e.reply(p, msg.ReplyCtx, "❌ `rework` requires a non-empty reason and modification plan.")
		}
		return
	}

	run, err := e.squadStore.GetRun(runID)
	if err != nil {
		if os.IsNotExist(err) {
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 未找到 run：`%s`", runID))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Run not found: `%s`", runID))
			}
			return
		}
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 读取 run 失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to load run: %v", err))
		}
		return
	}
	if run.Phase != SquadPhaseWaitReviewJudge || strings.TrimSpace(run.ReviewPendingTask) == "" {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ run `%s` 当前不在等待审核裁决阶段（phase=`%s`）。", run.RunID, run.Phase))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ run `%s` is not waiting for review decision (phase=`%s`).", run.RunID, run.Phase))
		}
		return
	}

	pendingTaskID := strings.TrimSpace(run.ReviewPendingTask)
	if idx, ok := findSquadTaskIndex(run, pendingTaskID); ok {
		run.CurrentTask = idx
	}
	decisionUpper := strings.ToUpper(decision)
	if err := updateSquadCheckpointDecision(strings.TrimSpace(run.ReviewPendingCP), decisionUpper, userNote); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 更新 checkpoint 失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to update checkpoint: %v", err))
		}
		return
	}

	fields := map[string]any{
		"task_id":  pendingTaskID,
		"round":    run.ReviewPendingRound,
		"decision": decisionUpper,
	}

	run.ReviewPendingTask = ""
	run.ReviewPendingRound = 0
	run.ReviewPendingCP = ""
	run.StopReason = ""
	run.ErrorMessage = ""
	run.Status = SquadStatusRunning
	run.Phase = SquadPhaseExecuting
	run.TaskPendingID = ""

	switch decision {
	case "pass":
		run.CurrentTask++
		run.ReworkCount = 0
		run.TaskApprovedID = ""
		run.UserReworkNote = ""
	case "rework":
		run.ReworkCount++
		run.TaskApprovedID = pendingTaskID
		run.UserReworkNote = userNote
		fields["user_rework_note"] = truncateStr(userNote, 220)
	}

	if err := e.squadStore.SaveRun(run); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 更新 run 失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to update run: %v", err))
		}
		return
	}
	_ = e.squadStore.AppendEvent(run.RunID, "info", "review judged by user", fields)
	if err := e.startSquadRunner(run.RunID); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "already running") {
			_ = e.failSquadRun(run.RunID, err.Error())
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 裁决后恢复执行失败：%v", err))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to resume run after review decision: %v", err))
			}
			return
		}
	}

	if isZh {
		if decision == "pass" {
			msgText := fmt.Sprintf("✅ 已裁决通过：`%s`\n", pendingTaskID)
			if run.CurrentTask < len(run.Plan.Tasks) {
				nextTask := run.Plan.Tasks[run.CurrentTask].ID
				msgText += fmt.Sprintf("下一步请确认任务：`/squad approve-task %s %s`", run.RunID, nextTask)
			} else {
				msgText += "全部任务已完成，正在进入汇总阶段。"
			}
			e.reply(p, msg.ReplyCtx, strings.TrimSpace(msgText))
			return
		}
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ 已裁决返工：`%s`\n已将你的“不通过原因与修改方案”发送给执行者，执行者将继续修改。", pendingTaskID))
		return
	}
	if decision == "pass" {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ Marked PASS for `%s`. Run continues.", pendingTaskID))
		return
	}
	e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ Marked REWORK for `%s`. Your reason/plan has been sent to executor.", pendingTaskID))
}

func (e *Engine) cmdSquadShowPlan(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese
	if len(args) < 1 {
		if isZh {
			e.reply(p, msg.ReplyCtx, "用法：`/squad show-plan <run_id>`")
		} else {
			e.reply(p, msg.ReplyCtx, "Usage: `/squad show-plan <run_id>`")
		}
		return
	}
	runID := strings.TrimSpace(args[0])
	run, err := e.squadStore.GetRun(runID)
	if err != nil {
		if os.IsNotExist(err) {
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 未找到 run：`%s`", runID))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Run not found: `%s`", runID))
			}
			return
		}
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 读取 run 失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to load run: %v", err))
		}
		return
	}
	var b strings.Builder
	if isZh {
		b.WriteString(fmt.Sprintf("📘 Squad 计划：`%s`\n", run.RunID))
	} else {
		b.WriteString(fmt.Sprintf("📘 Squad plan: `%s`\n", run.RunID))
	}
	b.WriteString(renderSquadPlanMarkdown(run))
	if isZh {
		b.WriteString(fmt.Sprintf("\n\n确认计划：`/squad approve-plan %s`", run.RunID))
		b.WriteString(fmt.Sprintf("\n查看任务：`/squad show-task %s current`", run.RunID))
	} else {
		b.WriteString(fmt.Sprintf("\n\nApprove plan: `/squad approve-plan %s`", run.RunID))
		b.WriteString(fmt.Sprintf("\nShow task: `/squad show-task %s current`", run.RunID))
	}
	e.reply(p, msg.ReplyCtx, strings.TrimSpace(b.String()))
}

func (e *Engine) cmdSquadShowTask(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese
	if len(args) < 1 {
		if isZh {
			e.reply(p, msg.ReplyCtx, "用法：`/squad show-task <run_id> [task_id|task_index|current]`")
		} else {
			e.reply(p, msg.ReplyCtx, "Usage: `/squad show-task <run_id> [task_id|task_index|current]`")
		}
		return
	}
	runID := strings.TrimSpace(args[0])
	selection := "current"
	if len(args) > 1 {
		selection = strings.TrimSpace(args[1])
	}
	run, err := e.squadStore.GetRun(runID)
	if err != nil {
		if os.IsNotExist(err) {
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 未找到 run：`%s`", runID))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Run not found: `%s`", runID))
			}
			return
		}
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 读取 run 失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to load run: %v", err))
		}
		return
	}
	idx, task, resolveErr := resolveSquadTaskSelection(run, selection)
	if resolveErr != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 无法解析任务：%v", resolveErr))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to resolve task: %v", resolveErr))
		}
		return
	}
	var b strings.Builder
	if isZh {
		b.WriteString(fmt.Sprintf("🧩 Squad 任务详情：`%s`\n\n", run.RunID))
	} else {
		b.WriteString(fmt.Sprintf("🧩 Squad task detail: `%s`\n\n", run.RunID))
	}
	b.WriteString(renderSquadTaskDetail(run, idx))
	if idx < run.CurrentTask {
		b.WriteString("\n状态：已完成")
	} else if idx == run.CurrentTask {
		switch {
		case run.Phase == SquadPhaseWaitReviewJudge && strings.EqualFold(strings.TrimSpace(run.ReviewPendingTask), task.ID):
			b.WriteString("\n状态：等待审核裁决")
		case strings.EqualFold(run.TaskApprovedID, task.ID):
			b.WriteString("\n状态：已确认，执行中")
		case strings.EqualFold(run.TaskPendingID, task.ID):
			b.WriteString("\n状态：等待确认")
		default:
			b.WriteString("\n状态：待确认")
		}
		if run.Phase == SquadPhaseWaitReviewJudge && strings.EqualFold(strings.TrimSpace(run.ReviewPendingTask), task.ID) {
			if isZh {
				b.WriteString(fmt.Sprintf("\n裁决通过：`/squad judge-review %s pass`", run.RunID))
				b.WriteString(fmt.Sprintf("\n裁决不通过：`/squad judge-review %s rework <原因与修改方案>`", run.RunID))
			} else {
				b.WriteString(fmt.Sprintf("\nJudge pass: `/squad judge-review %s pass`", run.RunID))
				b.WriteString(fmt.Sprintf("\nJudge rework: `/squad judge-review %s rework <reason_and_plan>`", run.RunID))
			}
		} else if run.Phase == SquadPhaseWaitTaskApprove && strings.EqualFold(strings.TrimSpace(run.TaskPendingID), task.ID) {
			if isZh {
				b.WriteString(fmt.Sprintf("\n确认命令：`/squad approve-task %s %s`", run.RunID, task.ID))
				b.WriteString(fmt.Sprintf("\n跳过命令：`/squad skip-task %s <跳过原因>`", run.RunID))
			} else {
				b.WriteString(fmt.Sprintf("\nApprove command: `/squad approve-task %s %s`", run.RunID, task.ID))
				b.WriteString(fmt.Sprintf("\nSkip command: `/squad skip-task %s <reason>`", run.RunID))
			}
		} else if isZh {
			b.WriteString(fmt.Sprintf("\n确认命令：`/squad approve-task %s %s`", run.RunID, task.ID))
		} else {
			b.WriteString(fmt.Sprintf("\nApprove command: `/squad approve-task %s %s`", run.RunID, task.ID))
		}
	} else {
		b.WriteString("\n状态：未开始")
	}
	e.reply(p, msg.ReplyCtx, strings.TrimSpace(b.String()))
}

func (e *Engine) cmdSquadStatus(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese

	var run *SquadRun
	var err error
	if len(args) > 0 {
		run, err = e.squadStore.GetRun(strings.TrimSpace(args[0]))
	} else {
		runs, listErr := e.squadStore.ListRuns()
		if listErr != nil {
			err = listErr
		} else if len(runs) == 0 {
			if isZh {
				e.reply(p, msg.ReplyCtx, "暂无 squad run。先使用 `/squad start ...`。")
			} else {
				e.reply(p, msg.ReplyCtx, "No squad run found. Use `/squad start ...` first.")
			}
			return
		} else {
			run = runs[0]
		}
	}
	if err != nil {
		if os.IsNotExist(err) {
			if isZh {
				e.reply(p, msg.ReplyCtx, "❌ 未找到指定 run。")
			} else {
				e.reply(p, msg.ReplyCtx, "❌ Run not found.")
			}
			return
		}
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 读取状态失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to load status: %v", err))
		}
		return
	}

	taskProgress := "0/0"
	if len(run.Plan.Tasks) > 0 {
		done := run.CurrentTask
		if done > len(run.Plan.Tasks) {
			done = len(run.Plan.Tasks)
		}
		taskProgress = fmt.Sprintf("%d/%d", done, len(run.Plan.Tasks))
	}
	currentTaskID := ""
	if run.CurrentTask >= 0 && run.CurrentTask < len(run.Plan.Tasks) {
		currentTaskID = run.Plan.Tasks[run.CurrentTask].ID
	}
	var b strings.Builder
	if isZh {
		b.WriteString("🧭 Squad 状态\n\n")
		b.WriteString(fmt.Sprintf("- run_id: `%s`\n", run.RunID))
		b.WriteString(fmt.Sprintf("- status: `%s`\n", run.Status))
		b.WriteString(fmt.Sprintf("- phase: `%s`\n", run.Phase))
		b.WriteString(fmt.Sprintf("- repo: `%s`\n", run.RepoPath))
		b.WriteString(fmt.Sprintf("- planner/executor/reviewer: `%s` / `%s` / `%s`\n", run.PlannerRole, run.ExecutorRole, run.ReviewerRole))
		b.WriteString(fmt.Sprintf("- planner_timeout_sec: `%d`\n", run.PlannerTimeoutSec))
		b.WriteString(fmt.Sprintf("- plan_approved: `%t`\n", run.PlanApproved))
		b.WriteString(fmt.Sprintf("- task_progress: `%s`\n", taskProgress))
		if currentTaskID != "" {
			b.WriteString(fmt.Sprintf("- current_task_id: `%s`\n", currentTaskID))
		}
		if strings.TrimSpace(run.TaskPendingID) != "" {
			b.WriteString(fmt.Sprintf("- task_pending_id: `%s`\n", run.TaskPendingID))
		}
		if strings.TrimSpace(run.TaskApprovedID) != "" {
			b.WriteString(fmt.Sprintf("- task_approved_id: `%s`\n", run.TaskApprovedID))
		}
		if strings.TrimSpace(run.ReviewPendingTask) != "" {
			b.WriteString(fmt.Sprintf("- review_pending_task: `%s`\n", run.ReviewPendingTask))
		}
		if run.ReviewPendingRound > 0 {
			b.WriteString(fmt.Sprintf("- review_pending_round: `%d`\n", run.ReviewPendingRound))
		}
		if run.Phase == SquadPhaseWaitReviewJudge {
			b.WriteString(fmt.Sprintf("- review_judge_cmd(pass): `/squad judge-review %s pass`\n", run.RunID))
			b.WriteString(fmt.Sprintf("- review_judge_cmd(rework): `/squad judge-review %s rework <原因与修改方案>`\n", run.RunID))
		}
		if run.Phase == SquadPhaseWaitTaskApprove {
			b.WriteString(fmt.Sprintf("- skip_task_cmd: `/squad skip-task %s <跳过原因>`\n", run.RunID))
		}
		if strings.TrimSpace(run.UserReworkNote) != "" {
			b.WriteString(fmt.Sprintf("- user_rework_note: %s\n", truncateStr(run.UserReworkNote, 160)))
		}
		b.WriteString(fmt.Sprintf("- rework_count/max: `%d/%d`\n", run.ReworkCount, run.MaxRework))
		if strings.TrimSpace(run.ErrorMessage) != "" {
			b.WriteString(fmt.Sprintf("- error: %s\n", truncateStr(run.ErrorMessage, 200)))
		}
	} else {
		b.WriteString("🧭 Squad status\n\n")
		b.WriteString(fmt.Sprintf("- run_id: `%s`\n", run.RunID))
		b.WriteString(fmt.Sprintf("- status: `%s`\n", run.Status))
		b.WriteString(fmt.Sprintf("- phase: `%s`\n", run.Phase))
		b.WriteString(fmt.Sprintf("- repo: `%s`\n", run.RepoPath))
		b.WriteString(fmt.Sprintf("- planner/executor/reviewer: `%s` / `%s` / `%s`\n", run.PlannerRole, run.ExecutorRole, run.ReviewerRole))
		b.WriteString(fmt.Sprintf("- planner_timeout_sec: `%d`\n", run.PlannerTimeoutSec))
		b.WriteString(fmt.Sprintf("- plan_approved: `%t`\n", run.PlanApproved))
		b.WriteString(fmt.Sprintf("- task_progress: `%s`\n", taskProgress))
		if currentTaskID != "" {
			b.WriteString(fmt.Sprintf("- current_task_id: `%s`\n", currentTaskID))
		}
		if strings.TrimSpace(run.TaskPendingID) != "" {
			b.WriteString(fmt.Sprintf("- task_pending_id: `%s`\n", run.TaskPendingID))
		}
		if strings.TrimSpace(run.TaskApprovedID) != "" {
			b.WriteString(fmt.Sprintf("- task_approved_id: `%s`\n", run.TaskApprovedID))
		}
		if strings.TrimSpace(run.ReviewPendingTask) != "" {
			b.WriteString(fmt.Sprintf("- review_pending_task: `%s`\n", run.ReviewPendingTask))
		}
		if run.ReviewPendingRound > 0 {
			b.WriteString(fmt.Sprintf("- review_pending_round: `%d`\n", run.ReviewPendingRound))
		}
		if run.Phase == SquadPhaseWaitReviewJudge {
			b.WriteString(fmt.Sprintf("- review_judge_cmd(pass): `/squad judge-review %s pass`\n", run.RunID))
			b.WriteString(fmt.Sprintf("- review_judge_cmd(rework): `/squad judge-review %s rework <reason_and_plan>`\n", run.RunID))
		}
		if run.Phase == SquadPhaseWaitTaskApprove {
			b.WriteString(fmt.Sprintf("- skip_task_cmd: `/squad skip-task %s <reason>`\n", run.RunID))
		}
		if strings.TrimSpace(run.UserReworkNote) != "" {
			b.WriteString(fmt.Sprintf("- user_rework_note: %s\n", truncateStr(run.UserReworkNote, 160)))
		}
		b.WriteString(fmt.Sprintf("- rework_count/max: `%d/%d`\n", run.ReworkCount, run.MaxRework))
		if strings.TrimSpace(run.ErrorMessage) != "" {
			b.WriteString(fmt.Sprintf("- error: %s\n", truncateStr(run.ErrorMessage, 200)))
		}
	}
	e.reply(p, msg.ReplyCtx, b.String())
}

func (e *Engine) cmdSquadStop(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese
	if len(args) < 1 {
		if isZh {
			e.reply(p, msg.ReplyCtx, "用法：`/squad stop <run_id>`")
		} else {
			e.reply(p, msg.ReplyCtx, "Usage: `/squad stop <run_id>`")
		}
		return
	}
	runID := strings.TrimSpace(args[0])
	run, err := e.squadStore.GetRun(runID)
	if err != nil {
		if os.IsNotExist(err) {
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 未找到 run：`%s`", runID))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Run not found: `%s`", runID))
			}
			return
		}
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 读取 run 失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to load run: %v", err))
		}
		return
	}

	run.Status = SquadStatusStopped
	run.Phase = SquadPhaseStopped
	run.StopReason = "manual_stop"
	if err := e.squadStore.SaveRun(run); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 更新 run 失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to update run: %v", err))
		}
		return
	}
	_ = e.squadStore.AppendEvent(run.RunID, "warn", "run stopped by user", nil)
	e.stopSquadRunner(run.RunID)
	_ = e.squadProc.StopRun(run.RunID)

	if isZh {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("⏹ 已停止 run：`%s`", run.RunID))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("⏹ Run stopped: `%s`", run.RunID))
	}
}

func (e *Engine) cmdSquadList(p Platform, msg *Message) {
	isZh := e.i18n.CurrentLang() == LangChinese
	runs, err := e.squadStore.ListRuns()
	if err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 列表读取失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to list runs: %v", err))
		}
		return
	}
	if len(runs) == 0 {
		if isZh {
			e.reply(p, msg.ReplyCtx, "暂无 squad run。")
		} else {
			e.reply(p, msg.ReplyCtx, "No squad run found.")
		}
		return
	}
	limit := 10
	if len(runs) < limit {
		limit = len(runs)
	}
	var b strings.Builder
	if isZh {
		b.WriteString("📋 Squad Runs（最新优先）\n\n")
	} else {
		b.WriteString("📋 Squad Runs (newest first)\n\n")
	}
	for i := 0; i < limit; i++ {
		run := runs[i]
		b.WriteString(fmt.Sprintf("- `%s` | `%s/%s` | repo=`%s` | %s\n",
			run.RunID, run.Status, run.Phase, truncateStr(run.RepoPath, 48), run.CreatedAt.Format("2006-01-02 15:04:05")))
	}
	e.reply(p, msg.ReplyCtx, b.String())
}

func (e *Engine) squadUsage(isZh bool) string {
	if isZh {
		return "🛠 Squad（三角色流水线：方案 -> 执行 -> 审核）\n\n" +
			"- `/squad start --repo <绝对路径> [--planner jarvis] [--planner-timeout 3600] [--executor xingzou] [--reviewer jianzhu] [--provider codez] <需求>`\n" +
			"- `/squad show-plan <run_id>`\n" +
			"- `/squad show-task <run_id> [task_id|task_index|current]`\n" +
			"- `/squad approve-plan <run_id>`\n" +
			"- `/squad approve-task <run_id> [task_id|task_index|current]`\n" +
			"- `/squad skip-task <run_id> [跳过原因]`\n" +
			"- `/squad judge-review <run_id> pass|rework [原因与修改方案]`\n" +
			"- `/squad status [run_id]`\n" +
			"- `/squad stop <run_id>`\n" +
			"- `/squad list`"
	}
	return "🛠 Squad (3-role pipeline: plan -> execute -> review)\n\n" +
		"- `/squad start --repo <abs_path> [--planner jarvis] [--planner-timeout 3600] [--executor xingzou] [--reviewer jianzhu] [--provider codez] <task>`\n" +
		"- `/squad show-plan <run_id>`\n" +
		"- `/squad show-task <run_id> [task_id|task_index|current]`\n" +
		"- `/squad approve-plan <run_id>`\n" +
		"- `/squad approve-task <run_id> [task_id|task_index|current]`\n" +
		"- `/squad skip-task <run_id> [reason]`\n" +
		"- `/squad judge-review <run_id> pass|rework [reason_and_plan]`\n" +
		"- `/squad status [run_id]`\n" +
		"- `/squad stop <run_id>`\n" +
		"- `/squad list`"
}

func resolveSquadTaskSelection(run *SquadRun, selection string) (int, SquadTask, error) {
	if run == nil {
		return -1, SquadTask{}, fmt.Errorf("run is nil")
	}
	if len(run.Plan.Tasks) == 0 {
		return -1, SquadTask{}, fmt.Errorf("plan has no tasks")
	}
	selection = strings.TrimSpace(strings.ToLower(selection))
	if selection == "" || selection == "current" {
		if run.CurrentTask < 0 || run.CurrentTask >= len(run.Plan.Tasks) {
			return -1, SquadTask{}, fmt.Errorf("current task index out of range: %d", run.CurrentTask)
		}
		return run.CurrentTask, run.Plan.Tasks[run.CurrentTask], nil
	}
	if n, err := strconv.Atoi(selection); err == nil {
		if n < 1 || n > len(run.Plan.Tasks) {
			return -1, SquadTask{}, fmt.Errorf("task index out of range: %d (valid 1..%d)", n, len(run.Plan.Tasks))
		}
		idx := n - 1
		return idx, run.Plan.Tasks[idx], nil
	}
	for idx, t := range run.Plan.Tasks {
		if strings.EqualFold(strings.TrimSpace(t.ID), selection) {
			return idx, t, nil
		}
	}
	return -1, SquadTask{}, fmt.Errorf("task not found: %s", selection)
}

func findSquadTaskIndex(run *SquadRun, taskID string) (int, bool) {
	if run == nil {
		return -1, false
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return -1, false
	}
	for i, t := range run.Plan.Tasks {
		if strings.EqualFold(strings.TrimSpace(t.ID), taskID) {
			return i, true
		}
	}
	return -1, false
}

func updateSquadCheckpointDecision(checkpointPath, verdict, userNote string) error {
	checkpointPath = strings.TrimSpace(checkpointPath)
	verdict = strings.ToUpper(strings.TrimSpace(verdict))
	userNote = strings.TrimSpace(userNote)
	if checkpointPath == "" {
		return nil
	}
	if verdict != "PASS" && verdict != "REWORK" {
		return fmt.Errorf("invalid verdict: %s", verdict)
	}
	b, err := os.ReadFile(checkpointPath)
	if err != nil {
		return fmt.Errorf("read checkpoint: %w", err)
	}
	var cp SquadCheckpoint
	if err := json.Unmarshal(b, &cp); err != nil {
		return fmt.Errorf("decode checkpoint: %w", err)
	}
	cp.Verdict = verdict
	if verdict == "REWORK" && userNote != "" {
		cp.Blockers = append(cp.Blockers, "用户裁决返工："+userNote)
	}
	out, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("encode checkpoint: %w", err)
	}
	if err := os.WriteFile(checkpointPath, out, 0o644); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}
	return nil
}

func parseSquadStartOptions(args []string) (SquadStartOptions, error) {
	opts := SquadStartOptions{
		PlannerRole:       DefaultSquadPlannerRole,
		ExecutorRole:      DefaultSquadExecutorRole,
		ReviewerRole:      DefaultSquadReviewerRole,
		PlannerTimeoutSec: DefaultSquadPlannerTimeoutSec,
	}
	var taskParts []string
	for i := 0; i < len(args); i++ {
		token := strings.TrimSpace(args[i])
		switch token {
		case "--repo", "--repo-path", "--work-dir", "--workdir":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("%s requires a value", token)
			}
			j := i + 1
			valParts := make([]string, 0, 2)
			for j < len(args) {
				part := strings.TrimSpace(args[j])
				if strings.HasPrefix(part, "--") {
					break
				}
				valParts = append(valParts, part)
				j++
			}
			if len(valParts) == 0 {
				return opts, fmt.Errorf("%s requires a value", token)
			}
			opts.RepoPath = strings.TrimSpace(strings.Join(valParts, " "))
			i = j - 1
		case "--task":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--task requires a value")
			}
			j := i + 1
			valParts := make([]string, 0, 4)
			for j < len(args) {
				part := strings.TrimSpace(args[j])
				if strings.HasPrefix(part, "--") {
					break
				}
				valParts = append(valParts, part)
				j++
			}
			if len(valParts) == 0 {
				return opts, fmt.Errorf("--task requires a value")
			}
			opts.TaskPrompt = strings.TrimSpace(strings.Join(valParts, " "))
			i = j - 1
		case "--planner":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--planner requires a value")
			}
			i++
			opts.PlannerRole = strings.TrimSpace(args[i])
		case "--executor":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--executor requires a value")
			}
			i++
			opts.ExecutorRole = strings.TrimSpace(args[i])
		case "--reviewer":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--reviewer requires a value")
			}
			i++
			opts.ReviewerRole = strings.TrimSpace(args[i])
		case "--provider":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--provider requires a value")
			}
			i++
			opts.Provider = strings.TrimSpace(args[i])
		case "--planner-timeout", "--planner-timeout-sec":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("%s requires a value", token)
			}
			i++
			n, err := strconv.Atoi(strings.TrimSpace(args[i]))
			if err != nil || n < 60 || n > 7200 {
				return opts, fmt.Errorf("%s must be an integer in [60,7200]", token)
			}
			opts.PlannerTimeoutSec = n
		default:
			taskParts = append(taskParts, token)
		}
	}

	if strings.TrimSpace(opts.TaskPrompt) == "" {
		opts.TaskPrompt = strings.TrimSpace(strings.Join(taskParts, " "))
	}
	opts.TaskPrompt = normalizeDebateQuestion(opts.TaskPrompt)
	opts.PlannerRole = normalizeRoleToken(opts.PlannerRole)
	opts.ExecutorRole = normalizeRoleToken(opts.ExecutorRole)
	opts.ReviewerRole = normalizeRoleToken(opts.ReviewerRole)
	if opts.PlannerRole == "" || opts.ExecutorRole == "" || opts.ReviewerRole == "" {
		return opts, fmt.Errorf("planner/executor/reviewer roles are required")
	}
	if opts.PlannerRole == opts.ExecutorRole || opts.PlannerRole == opts.ReviewerRole || opts.ExecutorRole == opts.ReviewerRole {
		return opts, fmt.Errorf("planner/executor/reviewer must be distinct roles")
	}
	if strings.TrimSpace(opts.RepoPath) == "" {
		return opts, fmt.Errorf("repo path is required, use --repo <abs_path>")
	}
	absRepo, err := filepath.Abs(opts.RepoPath)
	if err != nil {
		return opts, fmt.Errorf("resolve repo path: %w", err)
	}
	if fi, err := os.Stat(absRepo); err != nil || !fi.IsDir() {
		return opts, fmt.Errorf("repo path does not exist or is not a directory: %s", absRepo)
	}
	opts.RepoPath = absRepo
	if strings.TrimSpace(opts.TaskPrompt) == "" {
		return opts, fmt.Errorf("task prompt is required")
	}
	if opts.PlannerTimeoutSec <= 0 {
		opts.PlannerTimeoutSec = DefaultSquadPlannerTimeoutSec
	}
	return opts, nil
}
