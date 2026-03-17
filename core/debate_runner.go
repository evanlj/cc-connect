package core

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func (e *Engine) startDebateRunner(roomID string) error {
	e.debateMu.Lock()
	defer e.debateMu.Unlock()
	if _, exists := e.debateRuns[roomID]; exists {
		return fmt.Errorf("room %s is already running", roomID)
	}
	ctx, cancel := context.WithCancel(e.ctx)
	e.debateRuns[roomID] = cancel
	go e.runDebateRoom(ctx, roomID)
	return nil
}

func (e *Engine) stopDebateRunner(roomID string) bool {
	e.debateMu.Lock()
	defer e.debateMu.Unlock()
	cancel, ok := e.debateRuns[roomID]
	if !ok {
		return false
	}
	if cancel != nil {
		cancel()
	}
	delete(e.debateRuns, roomID)
	return true
}

func (e *Engine) isDebateRunnerActive(roomID string) bool {
	e.debateMu.Lock()
	defer e.debateMu.Unlock()
	_, ok := e.debateRuns[roomID]
	return ok
}

func (e *Engine) runDebateRoom(ctx context.Context, roomID string) {
	defer func() {
		e.debateMu.Lock()
		delete(e.debateRuns, roomID)
		e.debateMu.Unlock()
	}()

	if e.debateStore == nil {
		return
	}

	room, err := e.debateStore.GetRoom(roomID)
	if err != nil {
		slog.Error("debate: load room failed", "room_id", roomID, "error", err)
		return
	}

	board, boardErr := e.debateStore.LoadOrInitBlackboard(room)
	if boardErr != nil {
		slog.Warn("debate: load/init blackboard failed", "room_id", roomID, "error", boardErr)
		board = nil
	}
	blackboardPath := e.debateStore.BlackboardFilePath(room.RoomID)

	_ = e.SendBySessionKey(room.OwnerSessionKey, fmt.Sprintf("【Jarvis】讨论开始（room=%s），主题：%s", room.RoomID, room.Question))

	workers := make([]DebateRole, 0, len(room.Roles))
	for _, r := range room.Roles {
		if strings.EqualFold(r.Role, "jarvis") {
			continue
		}
		workers = append(workers, r)
	}
	if len(workers) == 0 {
		room.Status = DebateStatusFailed
		room.StopReason = "no_workers"
		_ = e.debateStore.SaveRoom(room)
		_ = e.SendBySessionKey(room.OwnerSessionKey, "【Jarvis】讨论失败：未找到可发言角色。")
		return
	}

	spoken := map[string]bool{}
	transcript := make([]DebateTranscriptEntry, 0, room.MaxRounds*3)

	for round := 1; round <= room.MaxRounds; round++ {
		select {
		case <-ctx.Done():
			room.Status = DebateStatusStopped
			room.StopReason = "manual_stop"
			_ = e.debateStore.SaveRoom(room)
			return
		default:
		}

		room.CurrentRound = round
		room.Status = DebateStatusRunning
		_ = e.debateStore.SaveRoom(room)
		UpdateBlackboardRound(board, round, room.MaxRounds)
		if board != nil {
			_ = e.debateStore.SaveBlackboard(board)
		}

		speakers := selectDebateSpeakers(room.SpeakingPolicy, round, workers, spoken, room.MaxRounds)
		if len(speakers) == 0 {
			speakers = workers[:1]
		}
		_ = e.SendBySessionKey(room.OwnerSessionKey, fmt.Sprintf("【Jarvis】第 %d 轮开始，本轮发言：%s", round, joinDisplayNames(speakers)))

		for _, role := range speakers {
			select {
			case <-ctx.Done():
				room.Status = DebateStatusStopped
				room.StopReason = "manual_stop"
				_ = e.debateStore.SaveRoom(room)
				return
			default:
			}

			entry := DebateTranscriptEntry{
				Round:    round,
				Speaker:  role.Instance,
				Role:     role.Role,
				PostedBy: role.Instance,
			}

			socketPath := e.resolveRoleSocketPath(role)
			if strings.TrimSpace(socketPath) == "" {
				entry.Content = "ERROR: socket path is empty"
				_ = e.debateStore.AppendTranscript(room.RoomID, entry)
				transcript = append(transcript, entry)
				continue
			}

			askReq := AskRequest{
				Project:     emptyAs(role.Project, role.Instance),
				SessionKey:  buildRoleSessionKey(room.OwnerSessionKey, room.GroupChatID, room.RoomID, role.Role),
				Prompt:      flattenPromptForTransport(buildRolePrompt(room, role, round, transcript, board, blackboardPath)),
				TimeoutSec:  120,
				Speak:       false,
				SpeakPrefix: "",
			}

			roleCtx, cancel := context.WithTimeout(ctx, 130*time.Second)
			res, askErr := e.instanceCli.Ask(roleCtx, socketPath, askReq)
			cancel()

			if askErr != nil {
				entry.Content = "ERROR: " + askErr.Error()
				_ = e.SendBySessionKey(room.OwnerSessionKey, fmt.Sprintf("【Jarvis】%s 本轮调用失败：%s", role.DisplayName, truncateStr(askErr.Error(), 80)))
			} else {
				finalContent := strings.TrimSpace(res.Content)
				finalLatency := res.LatencyMS

				repairNeeded, issues := debateReplyNeedsRepair(finalContent)
				if repairNeeded {
					slog.Warn("debate: role reply needs repair", "room_id", room.RoomID, "role", role.Role, "round", round, "issues", issues)
					if repaired, repairedLatency, repairErr := e.retryDebateReply(ctx, socketPath, askReq, room, role, round, board, finalContent, issues); repairErr == nil {
						finalContent = strings.TrimSpace(repaired)
						finalLatency += repairedLatency
					} else {
						slog.Warn("debate: repair attempt failed", "room_id", room.RoomID, "role", role.Role, "round", round, "error", repairErr)
					}
				}

				if strings.TrimSpace(finalContent) == "" {
					finalContent = e.i18n.T(MsgEmptyResponse)
				}

				speakReq := SendRequest{
					Project:    emptyAs(role.Project, role.Instance),
					SessionKey: askReq.SessionKey,
					Message:    fmt.Sprintf("【%s】%s", role.DisplayName, finalContent),
				}
				sendCtx, sendCancel := context.WithTimeout(ctx, 20*time.Second)
				sendErr := e.instanceCli.Send(sendCtx, socketPath, speakReq)
				sendCancel()
				if sendErr != nil {
					_ = e.SendBySessionKey(room.OwnerSessionKey, speakReq.Message)
					_ = e.SendBySessionKey(room.OwnerSessionKey, fmt.Sprintf("【Jarvis】%s 角色发言走降级通道：%s", role.DisplayName, truncateStr(sendErr.Error(), 80)))
				}

				entry.Content = finalContent
				entry.LatencyMS = finalLatency
				spoken[role.Role] = true
				ApplyRoleContribution(board, role, round, finalContent)
				if board != nil {
					_ = e.debateStore.SaveBlackboard(board)
				}
			}
			_ = e.debateStore.AppendTranscript(room.RoomID, entry)
			transcript = append(transcript, entry)
		}
	}

	select {
	case <-ctx.Done():
		room.Status = DebateStatusStopped
		room.StopReason = "manual_stop"
		_ = e.debateStore.SaveRoom(room)
		return
	default:
	}

	room.Status = DebateStatusSummarize
	_ = e.debateStore.SaveRoom(room)

	summary := e.buildDebateSummary(room, transcript, board)
	summarySessionKey := buildRoleSessionKey(room.OwnerSessionKey, room.GroupChatID, room.RoomID, "jarvis_summary")
	if askRes, err := e.AskSession(summarySessionKey, summary, 120*time.Second); err == nil {
		_ = e.SendBySessionKey(room.OwnerSessionKey, "【Jarvis-总结】\n"+askRes.Content)
	} else {
		_ = e.SendBySessionKey(room.OwnerSessionKey, "【Jarvis-总结】\n"+fallbackDebateSummary(room, transcript))
	}

	room.Status = DebateStatusCompleted
	room.StopReason = ""
	_ = e.debateStore.SaveRoom(room)
}

func (e *Engine) retryDebateReply(ctx context.Context, socketPath string, baseReq AskRequest, room *DebateRoom, role DebateRole, round int, board *DebateBlackboard, badReply string, issues []string) (string, int64, error) {
	repairReq := baseReq
	repairReq.Prompt = flattenPromptForTransport(buildDebateRepairPrompt(room, role, round, board, badReply, issues))
	repairReq.Speak = false
	repairReq.SpeakPrefix = ""

	roleCtx, cancel := context.WithTimeout(ctx, 130*time.Second)
	defer cancel()

	res, err := e.instanceCli.Ask(roleCtx, socketPath, repairReq)
	if err != nil {
		return "", 0, err
	}
	reply := strings.TrimSpace(res.Content)
	if needsRepair, _ := debateReplyNeedsRepair(reply); needsRepair {
		return reply, res.LatencyMS, fmt.Errorf("repair reply still invalid")
	}
	return reply, res.LatencyMS, nil
}

func selectDebateSpeakers(policy string, round int, workers []DebateRole, spoken map[string]bool, maxRounds int) []DebateRole {
	if len(workers) == 0 {
		return nil
	}

	policy = strings.ToLower(strings.TrimSpace(policy))
	if policy == "" {
		policy = DefaultSpeakingPolicy
	}

	switch policy {
	case "at-least-2":
		if len(workers) == 1 {
			return workers[:1]
		}
		i := (round - 1) % len(workers)
		j := round % len(workers)
		if i == j {
			return []DebateRole{workers[i]}
		}
		return []DebateRole{workers[i], workers[j]}
	case "cover-all-by-end":
		need := make([]DebateRole, 0, len(workers))
		for _, w := range workers {
			if !spoken[w.Role] {
				need = append(need, w)
			}
		}
		// Ensure uncovered roles are prioritized in final rounds.
		remainingRounds := maxRounds - round + 1
		if len(need) > 0 && remainingRounds <= len(need) {
			return need
		}
		if len(need) >= 2 {
			return need[:2]
		}
		if len(need) == 1 {
			return append(need, workers[(round-1)%len(workers)])
		}
		return workers[:minInt(2, len(workers))]
	default: // host-decide
		// Deterministic "dynamic naming": two speakers per round, rotating.
		if round == 1 && len(workers) >= 2 {
			return []DebateRole{workers[0], workers[1]}
		}
		if round == 2 && len(workers) >= 4 {
			return []DebateRole{workers[2], workers[3]}
		}
		// Fill uncovered first.
		need := make([]DebateRole, 0, len(workers))
		for _, w := range workers {
			if !spoken[w.Role] {
				need = append(need, w)
			}
		}
		if len(need) > 0 {
			return need[:minInt(2, len(need))]
		}
		return workers[:minInt(2, len(workers))]
	}
}

func joinDisplayNames(roles []DebateRole) string {
	if len(roles) == 0 {
		return "(none)"
	}
	names := make([]string, 0, len(roles))
	for _, r := range roles {
		names = append(names, r.DisplayName)
	}
	return strings.Join(names, "、")
}

func buildRoleSessionKey(ownerSessionKey, groupChatID, roomID, role string) string {
	platform := "feishu"
	if idx := strings.Index(ownerSessionKey, ":"); idx > 0 {
		platform = ownerSessionKey[:idx]
	}
	groupChatID = strings.TrimSpace(groupChatID)
	if groupChatID == "" {
		groupChatID = extractGroupChatID(ownerSessionKey)
	}
	if groupChatID == "" {
		groupChatID = "unknown_chat"
	}

	roomID = normalizeSessionKeyPart(roomID, "room")
	role = normalizeSessionKeyPart(role, "role")
	return fmt.Sprintf("%s:%s:debate_%s_%s", platform, groupChatID, roomID, role)
}

func buildRolePrompt(room *DebateRoom, role DebateRole, round int, transcript []DebateTranscriptEntry, board *DebateBlackboard, blackboardPath string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("你是 %s（角色：%s）。\n", role.DisplayName, role.Role))
	b.WriteString("当前在进行多角色讨论。\n")
	b.WriteString("强约束：先读取黑板文件，再基于黑板里的主题与其他角色发言输出观点。\n")
	b.WriteString("禁止输出任务接单菜单（例如“回复1/2/3/4”）。\n\n")

	if p := strings.TrimSpace(blackboardPath); p != "" {
		b.WriteString("[黑板文件]\n")
		b.WriteString(fmt.Sprintf("- 路径（绝对路径）：%s\n", filepath.ToSlash(p)))
		if board != nil {
			b.WriteString(fmt.Sprintf("- 当前 revision：%d\n", board.Revision))
		}
		b.WriteString("- 必须先读取该 JSON，再开始发言。\n")
		b.WriteString("- 读取重点：topic、goal、round_focus、round_plan、role_notes。\n\n")
	}

	if board != nil {
		b.WriteString("[主持人目标（来自黑板 objective）]\n")
		b.WriteString(fmt.Sprintf("- 轮次：第 %d/%d 轮\n", round, room.MaxRounds))
		if strings.TrimSpace(board.RoundPlan) != "" {
			b.WriteString(fmt.Sprintf("- 本轮计划：%s\n", board.RoundPlan))
		}
		if strings.TrimSpace(board.RoundFocus) != "" {
			b.WriteString(fmt.Sprintf("- 本轮焦点：%s\n", board.RoundFocus))
		}
		b.WriteString("- 你需要回应至少一条其他角色观点；若暂无观点，先给初始方案并明确待确认点。\n\n")
	}

	b.WriteString("输出必须分为两段：\n")
	b.WriteString("A) 群内可读内容（精简）：\n")
	b.WriteString("【观点】\n【依据】\n【风险/反例】\n【建议动作】\n\n")
	b.WriteString("B) 黑板回填 JSON（由主持人统一写入黑板，你不要自行写文件）：\n")
	baseRevision := 0
	if board != nil {
		baseRevision = board.Revision
	}
	b.WriteString("```json\n")
	b.WriteString("{\n")
	b.WriteString("  \"type\": \"blackboard_writeback\",\n")
	b.WriteString(fmt.Sprintf("  \"room_id\": \"%s\",\n", room.RoomID))
	b.WriteString(fmt.Sprintf("  \"role\": \"%s\",\n", role.Role))
	b.WriteString(fmt.Sprintf("  \"round\": %d,\n", round))
	b.WriteString(fmt.Sprintf("  \"base_revision\": %d,\n", baseRevision))
	b.WriteString("  \"stance\": \"\",\n")
	b.WriteString("  \"basis\": \"\",\n")
	b.WriteString("  \"risk\": \"\",\n")
	b.WriteString("  \"action\": \"\"\n")
	b.WriteString("}\n")
	b.WriteString("```\n")
	return b.String()
}

func buildDebateRepairPrompt(room *DebateRoom, role DebateRole, round int, board *DebateBlackboard, badReply string, issues []string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("你是 %s（角色：%s）。\n", role.DisplayName, role.Role))
	b.WriteString("你上一条回复未通过主持人校验，必须立即修正。\n")
	b.WriteString("禁止菜单化、禁止让用户继续选方向、禁止重复角色自我确认。\n\n")
	if len(issues) > 0 {
		b.WriteString("未通过原因：\n")
		for _, issue := range issues {
			if strings.TrimSpace(issue) == "" {
				continue
			}
			b.WriteString(fmt.Sprintf("- %s\n", issue))
		}
		b.WriteString("\n")
	}
	b.WriteString("[讨论目标]\n")
	b.WriteString(fmt.Sprintf("- 主题：%s\n", room.Question))
	b.WriteString(fmt.Sprintf("- 当前轮次：第 %d/%d 轮\n", round, room.MaxRounds))
	if board != nil {
		if strings.TrimSpace(board.RoundPlan) != "" {
			b.WriteString(fmt.Sprintf("- 本轮计划：%s\n", board.RoundPlan))
		}
		if strings.TrimSpace(board.RoundFocus) != "" {
			b.WriteString(fmt.Sprintf("- 本轮焦点：%s\n", board.RoundFocus))
		}
	}
	b.WriteString("\n")
	b.WriteString("你上一条不合格回复（仅供参考，不要复述）：\n")
	b.WriteString(truncateStr(strings.TrimSpace(badReply), 400))
	b.WriteString("\n\n")
	b.WriteString("请重新输出，必须严格满足：\n")
	b.WriteString("1) 必须围绕主题给出实质观点；\n")
	b.WriteString("2) 不能出现“回复1/2/3/4”“你要选哪一类”等菜单语句；\n")
	b.WriteString("3) 使用固定四段结构。\n\n")
	b.WriteString("【观点】\n【依据】\n【风险/反例】\n【建议动作】\n")
	return b.String()
}

var debateMenuReplyPattern = regexp.MustCompile(`(?is)(回复\s*1\s*/\s*2\s*/\s*3\s*/\s*4|你要选哪一类|先选方向|先确认你要做哪一类|TAPD（需求/缺陷/验收/回填）|OpenSpec 发布到 HTTP|Unity\s*/\s*AGame|代码实现\s*/\s*调试\s*/\s*文档)`)

func debateReplyNeedsRepair(reply string) (bool, []string) {
	reply = strings.TrimSpace(reply)
	issues := make([]string, 0, 4)
	if reply == "" {
		issues = append(issues, "回复为空")
		return true, issues
	}
	if debateMenuReplyPattern.MatchString(reply) {
		issues = append(issues, "出现菜单化引导语句")
	}
	if !strings.Contains(reply, "【观点】") {
		issues = append(issues, "缺少【观点】段落")
	}
	if !strings.Contains(reply, "【建议动作】") {
		issues = append(issues, "缺少【建议动作】段落")
	}
	return len(issues) > 0, issues
}

func flattenPromptForTransport(prompt string) string {
	prompt = strings.ReplaceAll(prompt, "\r\n", "\n")
	prompt = strings.ReplaceAll(prompt, "\r", "\n")
	lines := strings.Split(prompt, "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		parts = append(parts, trimmed)
	}
	return strings.Join(parts, " | ")
}

var sessionKeySafePattern = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func normalizeSessionKeyPart(raw, fallback string) string {
	v := strings.TrimSpace(raw)
	v = sessionKeySafePattern.ReplaceAllString(v, "_")
	v = strings.Trim(v, "_")
	if v == "" {
		return fallback
	}
	return v
}

func (e *Engine) buildDebateSummary(room *DebateRoom, transcript []DebateTranscriptEntry, board *DebateBlackboard) string {
	var b strings.Builder
	b.WriteString("请基于以下多角色讨论记录，输出结构化总结。\n")
	b.WriteString("要求：\n")
	b.WriteString("1) 先给最终结论（3条内）；\n")
	b.WriteString("2) 给出主要风险（3条内）；\n")
	b.WriteString("3) 给出行动项（owner+deadline+验收标准）。\n")
	b.WriteString("4) 输出语言：中文。\n\n")
	b.WriteString(fmt.Sprintf("讨论主题：%s\n", room.Question))
	if board != nil {
		if strings.TrimSpace(board.Goal) != "" {
			b.WriteString(fmt.Sprintf("黑板目标：%s\n", board.Goal))
		}
		if len(board.OpenQuestions) > 0 {
			b.WriteString("黑板待解问题：\n")
			for i := 0; i < minInt(3, len(board.OpenQuestions)); i++ {
				b.WriteString(fmt.Sprintf("- %s\n", truncateStr(board.OpenQuestions[i], 100)))
			}
		}
		if len(board.RoleNotes) > 0 {
			b.WriteString("黑板角色最新观点：\n")
			for _, rr := range room.Roles {
				n, ok := board.RoleNotes[rr.Role]
				if !ok || strings.TrimSpace(n.LatestStance) == "" {
					continue
				}
				b.WriteString(fmt.Sprintf("- [%s] %s\n", emptyAs(rr.DisplayName, rr.Role), truncateStr(n.LatestStance, 120)))
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("讨论记录：\n")
	for _, t := range transcript {
		b.WriteString(fmt.Sprintf("- [R%d][%s] %s\n", t.Round, t.Role, truncateStr(t.Content, 200)))
	}
	return b.String()
}

func fallbackDebateSummary(room *DebateRoom, transcript []DebateTranscriptEntry) string {
	var b strings.Builder
	b.WriteString("讨论已完成（降级总结）。\n")
	b.WriteString(fmt.Sprintf("主题：%s\n", room.Question))
	b.WriteString("要点回顾：\n")
	max := minInt(5, len(transcript))
	for i := 0; i < max; i++ {
		t := transcript[len(transcript)-max+i]
		b.WriteString(fmt.Sprintf("- [%s] %s\n", t.Role, truncateStr(t.Content, 100)))
	}
	b.WriteString("行动项：\n- [Jarvis] 基于上述要点整理可执行计划并确认优先级。\n")
	return b.String()
}

func (e *Engine) resolveRoleSocketPath(role DebateRole) string {
	if strings.TrimSpace(role.SocketPath) != "" {
		return role.SocketPath
	}
	repoRoot := inferRepoRootFromWorkingDir()
	if repoRoot == "" && e.debateStore != nil {
		repoRoot = inferRepoRootFromInstancesPath(e.debateStore.RootDir())
	}
	if repoRoot == "" {
		return filepath.Join("instances", role.Instance, "data", "run", "api.sock")
	}
	return filepath.Join(repoRoot, "instances", role.Instance, "data", "run", "api.sock")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
