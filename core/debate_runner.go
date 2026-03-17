package core

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
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
				SessionKey:  buildRoleSessionKey(room.OwnerSessionKey, room.GroupChatID, role.Role),
				Prompt:      buildRolePrompt(room, role, round, transcript),
				TimeoutSec:  120,
				Speak:       true,
				SpeakPrefix: fmt.Sprintf("【%s】", role.DisplayName),
			}

			roleCtx, cancel := context.WithTimeout(ctx, 130*time.Second)
			res, askErr := e.instanceCli.Ask(roleCtx, socketPath, askReq)
			cancel()

			if askErr != nil {
				entry.Content = "ERROR: " + askErr.Error()
				_ = e.SendBySessionKey(room.OwnerSessionKey, fmt.Sprintf("【Jarvis】%s 本轮调用失败：%s", role.DisplayName, truncateStr(askErr.Error(), 80)))
			} else {
				entry.Content = res.Content
				entry.LatencyMS = res.LatencyMS
				spoken[role.Role] = true
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

	summary := e.buildDebateSummary(room, transcript)
	summarySessionKey := buildRoleSessionKey(room.OwnerSessionKey, room.GroupChatID, "jarvis_summary")
	if askRes, err := e.AskSession(summarySessionKey, summary, 120*time.Second); err == nil {
		_ = e.SendBySessionKey(room.OwnerSessionKey, "【Jarvis-总结】\n"+askRes.Content)
	} else {
		_ = e.SendBySessionKey(room.OwnerSessionKey, "【Jarvis-总结】\n"+fallbackDebateSummary(room, transcript))
	}

	room.Status = DebateStatusCompleted
	room.StopReason = ""
	_ = e.debateStore.SaveRoom(room)
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

func buildRoleSessionKey(ownerSessionKey, groupChatID, role string) string {
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
	return fmt.Sprintf("%s:%s:debate_%s", platform, groupChatID, role)
}

func buildRolePrompt(room *DebateRoom, role DebateRole, round int, transcript []DebateTranscriptEntry) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("你是 %s（角色：%s）。\n", role.DisplayName, role.Role))
	b.WriteString("当前在进行多角色讨论，请基于角色职责给出简明观点。\n\n")
	b.WriteString(fmt.Sprintf("讨论主题：%s\n", room.Question))
	b.WriteString(fmt.Sprintf("当前轮次：第 %d 轮（总上限 %d 轮）\n\n", round, room.MaxRounds))

	if len(transcript) > 0 {
		b.WriteString("最近发言摘要：\n")
		start := 0
		if len(transcript) > 4 {
			start = len(transcript) - 4
		}
		for i := start; i < len(transcript); i++ {
			t := transcript[i]
			b.WriteString(fmt.Sprintf("- [%s] %s\n", t.Role, truncateStr(t.Content, 120)))
		}
		b.WriteString("\n")
	}

	b.WriteString("请用以下结构输出（每段尽量精简）：\n")
	b.WriteString("【观点】\n【依据】\n【风险/反例】\n【建议动作】\n")
	return b.String()
}

func (e *Engine) buildDebateSummary(room *DebateRoom, transcript []DebateTranscriptEntry) string {
	var b strings.Builder
	b.WriteString("请基于以下多角色讨论记录，输出结构化总结。\n")
	b.WriteString("要求：\n")
	b.WriteString("1) 先给最终结论（3条内）；\n")
	b.WriteString("2) 给出主要风险（3条内）；\n")
	b.WriteString("3) 给出行动项（owner+deadline+验收标准）。\n")
	b.WriteString("4) 输出语言：中文。\n\n")
	b.WriteString(fmt.Sprintf("讨论主题：%s\n", room.Question))
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
