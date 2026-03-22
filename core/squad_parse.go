package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var squadJSONCodeFencePattern = regexp.MustCompile("(?is)```(?:json)?\\s*(\\{[\\s\\S]*?\\})\\s*```")

func parseSquadPlan(reply string) (SquadPlan, error) {
	plan := SquadPlan{RawReply: strings.TrimSpace(reply)}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return plan, fmt.Errorf("empty planner reply")
	}

	rawJSON := extractFirstJSONObject(reply)
	if rawJSON == "" {
		// Fallback: treat full text as one task so flow can continue.
		plan.Title = "重构执行方案（降级提取）"
		plan.Overview = truncateStr(reply, 260)
		plan.Tasks = []SquadTask{
			{
				ID:         "task-1",
				Title:      "执行整体重构任务",
				Objective:  truncateStr(reply, 320),
				Acceptance: "完成主要重构并给出验证结果",
			},
		}
		return plan, nil
	}

	var payload struct {
		Title    string      `json:"title"`
		Overview string      `json:"overview"`
		Tasks    []SquadTask `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &payload); err != nil {
		return plan, fmt.Errorf("decode plan json: %w", err)
	}
	plan.Title = strings.TrimSpace(payload.Title)
	plan.Overview = strings.TrimSpace(payload.Overview)
	plan.Tasks = normalizeSquadTasks(payload.Tasks)
	if len(plan.Tasks) == 0 {
		return plan, fmt.Errorf("plan has no tasks")
	}
	if plan.Title == "" {
		plan.Title = "重构执行方案"
	}
	if plan.Overview == "" {
		plan.Overview = "按任务拆解推进实现与审核。"
	}
	return plan, nil
}

func parseReviewerFindings(reply string) (reviewResult string, failedReasons []string, suggestions []string) {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return "审核回复为空", []string{"审核回复为空"}, nil
	}

	rawJSON := extractFirstJSONObject(reply)
	if rawJSON != "" {
		var payload struct {
			ReviewResult  string   `json:"review_result"`
			FailedReasons []string `json:"failed_reasons"`
			Suggestions   []string `json:"suggestions"`
			Blockers      []string `json:"blockers"` // backward compatibility
			Verdict       string   `json:"verdict"`  // backward compatibility
		}
		if err := json.Unmarshal([]byte(rawJSON), &payload); err == nil {
			reviewResult = strings.TrimSpace(payload.ReviewResult)
			failedReasons = normalizeStringList(payload.FailedReasons)
			if len(failedReasons) == 0 {
				failedReasons = normalizeStringList(payload.Blockers)
			}
			suggestions = normalizeStringList(payload.Suggestions)
			if reviewResult == "" {
				verdict := strings.ToUpper(strings.TrimSpace(payload.Verdict))
				switch verdict {
				case "PASS":
					reviewResult = "审核通过（建议由用户最终裁决）"
				case "REWORK":
					reviewResult = "审核发现问题（建议返工，由用户最终裁决）"
				}
			}
			if reviewResult == "" {
				if len(failedReasons) > 0 {
					reviewResult = "发现未通过项（由用户最终裁决）"
				} else {
					reviewResult = "审核未发现阻塞问题（由用户最终裁决）"
				}
			}
			return reviewResult, failedReasons, suggestions
		}
	}

	reviewResult = truncateStr(reply, 220)
	if inferVerdictFromText(reply) == "REWORK" {
		failedReasons = []string{truncateStr(reply, 180)}
	}
	return reviewResult, failedReasons, nil
}

// parseReviewerVerdict is kept for backward compatibility.
func parseReviewerVerdict(reply string) (verdict string, blockers []string, suggestions []string) {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return "REWORK", []string{"审核回复为空"}, nil
	}

	rawJSON := extractFirstJSONObject(reply)
	if rawJSON != "" {
		var payload struct {
			Verdict     string   `json:"verdict"`
			Blockers    []string `json:"blockers"`
			Suggestions []string `json:"suggestions"`
		}
		if err := json.Unmarshal([]byte(rawJSON), &payload); err == nil {
			verdict = strings.ToUpper(strings.TrimSpace(payload.Verdict))
			if verdict == "" {
				verdict = inferVerdictFromText(reply)
			}
			blockers = normalizeStringList(payload.Blockers)
			suggestions = normalizeStringList(payload.Suggestions)
			if verdict != "PASS" && verdict != "REWORK" {
				verdict = inferVerdictFromText(reply)
			}
			if verdict == "REWORK" && len(blockers) == 0 {
				blockers = []string{"审核要求返工，但未给出 blocker，已按保守策略阻塞"}
			}
			return verdict, blockers, suggestions
		}
	}

	verdict = inferVerdictFromText(reply)
	if verdict == "REWORK" {
		blockers = []string{truncateStr(reply, 160)}
	}
	return verdict, blockers, nil
}

func inferVerdictFromText(reply string) string {
	upper := strings.ToUpper(reply)
	if strings.Contains(upper, "VERDICT: PASS") || strings.Contains(upper, "VERDICT=PASS") || strings.Contains(upper, " PASS") {
		return "PASS"
	}
	if strings.Contains(upper, "PASS") && !strings.Contains(upper, "REWORK") {
		return "PASS"
	}
	return "REWORK"
}

func normalizeSquadTasks(in []SquadTask) []SquadTask {
	out := make([]SquadTask, 0, len(in))
	for i, t := range in {
		id := strings.TrimSpace(t.ID)
		if id == "" {
			id = fmt.Sprintf("task-%d", i+1)
		}
		title := strings.TrimSpace(t.Title)
		if title == "" {
			title = fmt.Sprintf("任务 %d", i+1)
		}
		item := SquadTask{
			ID:          id,
			Title:       title,
			Objective:   strings.TrimSpace(t.Objective),
			Acceptance:  strings.TrimSpace(t.Acceptance),
			TestCommand: strings.TrimSpace(t.TestCommand),
			DependsOn:   normalizeStringList(t.DependsOn),
		}
		out = append(out, item)
	}
	return out
}

func normalizeStringList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func extractFirstJSONObject(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if matches := squadJSONCodeFencePattern.FindAllStringSubmatch(text, -1); len(matches) > 0 {
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			candidate := strings.TrimSpace(m[1])
			if json.Valid([]byte(candidate)) {
				return candidate
			}
		}
	}

	decoder := json.NewDecoder(strings.NewReader(text))
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err == nil && json.Valid(raw) {
		return strings.TrimSpace(string(raw))
	}

	// Last resort: try first '{' ... last '}'.
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		candidate := strings.TrimSpace(text[start : end+1])
		if json.Valid([]byte(candidate)) {
			return candidate
		}
	}
	return ""
}

func renderSquadPlanMarkdown(run *SquadRun) string {
	if run == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Squad 方案\n\n")
	b.WriteString(fmt.Sprintf("- run_id: `%s`\n", run.RunID))
	b.WriteString(fmt.Sprintf("- repo: `%s`\n", run.RepoPath))
	b.WriteString(fmt.Sprintf("- planner: `%s`\n", run.PlannerRole))
	b.WriteString(fmt.Sprintf("- executor: `%s`\n", run.ExecutorRole))
	b.WriteString(fmt.Sprintf("- reviewer: `%s`\n", run.ReviewerRole))
	if strings.TrimSpace(run.Provider) != "" {
		b.WriteString(fmt.Sprintf("- provider: `%s`\n", run.Provider))
	}
	b.WriteString("\n## 需求\n\n")
	b.WriteString(strings.TrimSpace(run.TaskPrompt) + "\n\n")
	b.WriteString("## 方案摘要\n\n")
	b.WriteString(fmt.Sprintf("### %s\n\n", emptyAs(run.Plan.Title, "执行方案")))
	b.WriteString(emptyAs(run.Plan.Overview, "（无）") + "\n\n")
	b.WriteString("## 任务拆解\n\n")
	if len(run.Plan.Tasks) == 0 {
		b.WriteString("- （无任务）\n")
		return b.String()
	}
	for i, t := range run.Plan.Tasks {
		b.WriteString(fmt.Sprintf("### %d. %s (%s)\n\n", i+1, t.Title, t.ID))
		if strings.TrimSpace(t.Objective) != "" {
			b.WriteString(fmt.Sprintf("- 目标: %s\n", t.Objective))
		}
		if strings.TrimSpace(t.Acceptance) != "" {
			b.WriteString(fmt.Sprintf("- 验收: %s\n", t.Acceptance))
		}
		if strings.TrimSpace(t.TestCommand) != "" {
			b.WriteString(fmt.Sprintf("- 测试命令: `%s`\n", t.TestCommand))
		}
		if len(t.DependsOn) > 0 {
			b.WriteString(fmt.Sprintf("- 依赖: %s\n", strings.Join(t.DependsOn, "、")))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func renderSquadPlanApprovalPreview(run *SquadRun, maxTasks int) string {
	if run == nil {
		return ""
	}
	if maxTasks <= 0 {
		maxTasks = 8
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("方案标题：%s\n", emptyAs(strings.TrimSpace(run.Plan.Title), "执行方案")))
	if strings.TrimSpace(run.Plan.Overview) != "" {
		b.WriteString(fmt.Sprintf("方案摘要：%s\n", truncateStr(strings.TrimSpace(run.Plan.Overview), 240)))
	}
	if len(run.Plan.Tasks) == 0 {
		b.WriteString("- （无任务）")
		return strings.TrimSpace(b.String())
	}
	b.WriteString(fmt.Sprintf("任务列表（共 %d 项）：\n", len(run.Plan.Tasks)))
	limit := len(run.Plan.Tasks)
	if limit > maxTasks {
		limit = maxTasks
	}
	for i := 0; i < limit; i++ {
		t := run.Plan.Tasks[i]
		b.WriteString(fmt.Sprintf("%d) [%s] %s\n", i+1, t.ID, t.Title))
		if strings.TrimSpace(t.Objective) != "" {
			b.WriteString(fmt.Sprintf("   - 目标：%s\n", truncateStr(t.Objective, 180)))
		}
		if strings.TrimSpace(t.Acceptance) != "" {
			b.WriteString(fmt.Sprintf("   - 验收：%s\n", truncateStr(t.Acceptance, 220)))
		}
	}
	if len(run.Plan.Tasks) > limit {
		b.WriteString(fmt.Sprintf("... 其余 %d 项请用 `/squad show-plan %s` 查看。\n", len(run.Plan.Tasks)-limit, run.RunID))
	}
	return strings.TrimSpace(b.String())
}

func renderSquadTaskDetail(run *SquadRun, taskIndex int) string {
	if run == nil || taskIndex < 0 || taskIndex >= len(run.Plan.Tasks) {
		return ""
	}
	task := run.Plan.Tasks[taskIndex]
	var b strings.Builder
	b.WriteString(fmt.Sprintf("任务：%d/%d\n", taskIndex+1, len(run.Plan.Tasks)))
	b.WriteString(fmt.Sprintf("ID：%s\n", task.ID))
	b.WriteString(fmt.Sprintf("标题：%s\n", emptyAs(task.Title, "（无）")))
	if strings.TrimSpace(task.Objective) != "" {
		b.WriteString(fmt.Sprintf("目标：%s\n", task.Objective))
	}
	if strings.TrimSpace(task.Acceptance) != "" {
		b.WriteString(fmt.Sprintf("验收：%s\n", task.Acceptance))
	}
	if strings.TrimSpace(task.TestCommand) != "" {
		b.WriteString(fmt.Sprintf("测试命令：`%s`\n", task.TestCommand))
	}
	if len(task.DependsOn) > 0 {
		b.WriteString(fmt.Sprintf("依赖：%s\n", strings.Join(task.DependsOn, "、")))
	}
	return strings.TrimSpace(b.String())
}
