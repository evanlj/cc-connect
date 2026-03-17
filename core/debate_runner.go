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
	if strings.EqualFold(room.Mode, DebateModeConsensus) {
		e.runConsensusDebateRoom(ctx, room)
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
				displayContent := extractDebateDisplayContent(finalContent)
				if strings.TrimSpace(displayContent) == "" {
					displayContent = finalContent
				}

				speakReq := SendRequest{
					Project:    emptyAs(role.Project, role.Instance),
					SessionKey: askReq.SessionKey,
					Message:    fmt.Sprintf("【%s】%s", role.DisplayName, displayContent),
				}
				sendCtx, sendCancel := context.WithTimeout(ctx, 20*time.Second)
				sendErr := e.instanceCli.Send(sendCtx, socketPath, speakReq)
				sendCancel()
				if sendErr != nil {
					_ = e.SendBySessionKey(room.OwnerSessionKey, speakReq.Message)
					_ = e.SendBySessionKey(room.OwnerSessionKey, fmt.Sprintf("【Jarvis】%s 角色发言走降级通道：%s", role.DisplayName, truncateStr(sendErr.Error(), 80)))
				}

				entry.Content = displayContent
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

	e.finalizeDebateSummary(room, transcript, board)

	room.Status = DebateStatusCompleted
	room.StopReason = ""
	room.Phase = "completed"
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

const (
	consensusPhaseInit        = "init"
	consensusPhaseClarify     = "clarify_with_user"
	consensusPhaseHostSeed    = "host_seed"
	consensusPhaseAllDiverge  = "all_diverge"
	consensusPhaseHostCollect = "host_collect"
	consensusPhaseAllResolve  = "all_resolve"
	consensusPhaseHostCheck   = "host_consensus_check"
	consensusPhaseFinalize    = "host_finalize"
)

func (e *Engine) runConsensusDebateRoom(ctx context.Context, room *DebateRoom) {
	if room == nil {
		return
	}
	board, boardErr := e.debateStore.LoadOrInitBlackboard(room)
	if boardErr != nil {
		slog.Warn("debate(consensus): load/init blackboard failed", "room_id", room.RoomID, "error", boardErr)
		board = nil
	}
	blackboardPath := e.debateStore.BlackboardFilePath(room.RoomID)
	if board != nil {
		board.Mode = DebateModeConsensus
		if strings.TrimSpace(room.RefinedQuestion) != "" {
			board.RefinedTopic = strings.TrimSpace(room.RefinedQuestion)
		}
		UpdateBlackboardWorkflow(board, DebateModeConsensus, emptyAs(room.Phase, consensusPhaseInit), room.Iteration)
		_ = e.debateStore.SaveBlackboard(board)
	}

	host, workers := splitConsensusRoles(room.Roles)
	if strings.TrimSpace(host.Role) == "" {
		room.Status = DebateStatusFailed
		room.StopReason = "no_host"
		_ = e.debateStore.SaveRoom(room)
		_ = e.SendBySessionKey(room.OwnerSessionKey, "【Jarvis】讨论失败：未找到主持人角色。")
		return
	}
	if len(workers) == 0 {
		room.Status = DebateStatusFailed
		room.StopReason = "no_workers"
		_ = e.debateStore.SaveRoom(room)
		_ = e.SendBySessionKey(room.OwnerSessionKey, "【Jarvis】讨论失败：未找到可发言角色。")
		return
	}

	if strings.TrimSpace(room.Phase) == "" {
		room.Phase = consensusPhaseInit
	}
	if room.MaxRounds <= 0 {
		room.MaxRounds = DefaultDebateMaxRounds
	}
	if room.Status == DebateStatusCreated {
		room.Status = DebateStatusRunning
	}
	_ = e.debateStore.SaveRoom(room)

	if room.Phase == consensusPhaseInit {
		room.Phase = consensusPhaseClarify
		_ = e.debateStore.SaveRoom(room)
	}

	if room.Phase == consensusPhaseClarify {
		if strings.TrimSpace(room.RefinedQuestion) == "" {
			questions := defaultClarifyQuestions(room.Question)
			if board != nil {
				board.OpenQuestions = mergeUniqueQuestions(board.OpenQuestions, questions, 8)
				UpdateBlackboardWorkflow(board, DebateModeConsensus, consensusPhaseClarify, room.Iteration)
				_ = e.debateStore.SaveBlackboard(board)
			}
			var b strings.Builder
			b.WriteString(fmt.Sprintf("【Jarvis】进入澄清阶段（room=%s）。\n", room.RoomID))
			b.WriteString("为避免讨论跑偏，请先补充以下信息（可一条消息回答）：\n")
			for i, q := range questions {
				b.WriteString(fmt.Sprintf("%d) %s\n", i+1, q))
			}
			b.WriteString(fmt.Sprintf("\n回复方式：`/debate answer %s <你的补充信息>`", room.RoomID))
			_ = e.SendBySessionKey(room.OwnerSessionKey, strings.TrimSpace(b.String()))
			room.Status = DebateStatusWaiting
			room.StopReason = "await_user_clarification"
			_ = e.debateStore.SaveRoom(room)
			return
		}
		room.Phase = consensusPhaseHostSeed
		room.Status = DebateStatusRunning
		room.StopReason = ""
		_ = e.debateStore.SaveRoom(room)
	}

	topic := currentConsensusTopic(room)
	transcript := make([]DebateTranscriptEntry, 0, room.MaxRounds*8)
	if old, err := e.debateStore.LoadTranscript(room.RoomID); err == nil {
		transcript = append(transcript, old...)
	}

	if room.Phase == consensusPhaseHostSeed {
		_ = e.SendBySessionKey(room.OwnerSessionKey, "【Jarvis】阶段1/6：主持人先行立论并同步黑板。")
		reply, latency, err := e.askDebateRole(ctx, room, host, buildConsensusHostSeedPrompt(room, board, topic, blackboardPath))
		if err != nil {
			room.Status = DebateStatusFailed
			room.StopReason = "host_seed_failed"
			_ = e.debateStore.SaveRoom(room)
			_ = e.SendBySessionKey(room.OwnerSessionKey, fmt.Sprintf("【Jarvis】主持人立论失败：%s", truncateStr(err.Error(), 100)))
			return
		}
		display := extractDebateDisplayContent(reply)
		if strings.TrimSpace(display) == "" {
			display = reply
		}
		_ = e.sendDebateRoleSpeech(ctx, room, host, display)
		entry := DebateTranscriptEntry{
			Round:     0,
			Speaker:   host.Instance,
			Role:      host.Role,
			PostedBy:  host.Instance,
			Content:   display,
			LatencyMS: latency,
		}
		_ = e.debateStore.AppendTranscript(room.RoomID, entry)
		transcript = append(transcript, entry)
		ApplyRoleContribution(board, host, 0, reply)
		if board != nil {
			board.RefinedTopic = topic
			UpdateBlackboardWorkflow(board, DebateModeConsensus, consensusPhaseHostSeed, room.Iteration)
			_ = e.debateStore.SaveBlackboard(board)
		}
		room.Phase = consensusPhaseAllDiverge
		_ = e.debateStore.SaveRoom(room)
	}

	if room.Phase == consensusPhaseAllDiverge {
		_ = e.SendBySessionKey(room.OwnerSessionKey, fmt.Sprintf("【Jarvis】阶段2/6：全员发散讨论（%d位）。", len(workers)))
		for _, role := range workers {
			select {
			case <-ctx.Done():
				room.Status = DebateStatusStopped
				room.StopReason = "manual_stop"
				_ = e.debateStore.SaveRoom(room)
				return
			default:
			}
			reply, latency, err := e.askDebateRole(ctx, room, role, buildConsensusWorkerDivergePrompt(room, role, board, topic, blackboardPath))
			if err != nil {
				entry := DebateTranscriptEntry{
					Round:    1,
					Speaker:  role.Instance,
					Role:     role.Role,
					PostedBy: role.Instance,
					Content:  "ERROR: " + err.Error(),
				}
				_ = e.debateStore.AppendTranscript(room.RoomID, entry)
				transcript = append(transcript, entry)
				continue
			}
			display := extractDebateDisplayContent(reply)
			if strings.TrimSpace(display) == "" {
				display = reply
			}
			_ = e.sendDebateRoleSpeech(ctx, room, role, display)
			entry := DebateTranscriptEntry{
				Round:     1,
				Speaker:   role.Instance,
				Role:      role.Role,
				PostedBy:  role.Instance,
				Content:   display,
				LatencyMS: latency,
			}
			_ = e.debateStore.AppendTranscript(room.RoomID, entry)
			transcript = append(transcript, entry)
			ApplyRoleContribution(board, role, 1, reply)
			if board != nil {
				_ = e.debateStore.SaveBlackboard(board)
			}
		}
		room.Phase = consensusPhaseHostCollect
		_ = e.debateStore.SaveRoom(room)
	}

	if room.Phase == consensusPhaseHostCollect {
		_ = e.SendBySessionKey(room.OwnerSessionKey, "【Jarvis】阶段3/6：主持人收集分歧与疑问。")
		reply, latency, err := e.askDebateRole(ctx, room, host, buildConsensusHostCollectPrompt(room, board, topic))
		if err == nil {
			display := extractDebateDisplayContent(reply)
			if strings.TrimSpace(display) == "" {
				display = reply
			}
			_ = e.sendDebateRoleSpeech(ctx, room, host, display)
			entry := DebateTranscriptEntry{
				Round:     2,
				Speaker:   host.Instance,
				Role:      host.Role,
				PostedBy:  host.Instance,
				Content:   display,
				LatencyMS: latency,
			}
			_ = e.debateStore.AppendTranscript(room.RoomID, entry)
			transcript = append(transcript, entry)
			ApplyRoleContribution(board, host, 2, reply)
		}
		if board != nil {
			board.ConsensusPoints, board.Unresolved = summarizeConsensusFromBoard(board, room)
			board.OpenQuestions = mergeUniqueQuestions(board.OpenQuestions, board.Unresolved, 12)
			UpdateBlackboardWorkflow(board, DebateModeConsensus, consensusPhaseHostCollect, room.Iteration)
			_ = e.debateStore.SaveBlackboard(board)
		}
		room.Phase = consensusPhaseAllResolve
		if room.Iteration <= 0 {
			room.Iteration = 1
		}
		_ = e.debateStore.SaveRoom(room)
	}

	for room.Phase == consensusPhaseAllResolve {
		if room.Iteration <= 0 {
			room.Iteration = 1
		}
		if room.Iteration > room.MaxRounds {
			room.Status = DebateStatusWaiting
			room.StopReason = "await_user_decision"
			room.Phase = consensusPhaseClarify
			_ = e.debateStore.SaveRoom(room)
			if board != nil {
				UpdateBlackboardWorkflow(board, DebateModeConsensus, room.Phase, room.Iteration)
				_ = e.debateStore.SaveBlackboard(board)
			}
			unresolved := []string{}
			if board != nil {
				unresolved = board.Unresolved
			}
			_ = e.SendBySessionKey(room.OwnerSessionKey,
				fmt.Sprintf("【Jarvis】已达到最大收敛轮次（%d），仍存在未统一项：%s\n请补充决策后继续：`/debate answer %s <你的决策>`", room.MaxRounds, strings.Join(nonEmptyOrDash(unresolved), "；"), room.RoomID))
			return
		}

		_ = e.SendBySessionKey(room.OwnerSessionKey, fmt.Sprintf("【Jarvis】阶段4/6：第 %d 轮分歧收敛讨论。", room.Iteration))
		for _, role := range workers {
			select {
			case <-ctx.Done():
				room.Status = DebateStatusStopped
				room.StopReason = "manual_stop"
				_ = e.debateStore.SaveRoom(room)
				return
			default:
			}
			reply, latency, err := e.askDebateRole(ctx, room, role, buildConsensusWorkerResolvePrompt(room, role, board, topic, room.Iteration))
			if err != nil {
				entry := DebateTranscriptEntry{
					Round:    2 + room.Iteration,
					Speaker:  role.Instance,
					Role:     role.Role,
					PostedBy: role.Instance,
					Content:  "ERROR: " + err.Error(),
				}
				_ = e.debateStore.AppendTranscript(room.RoomID, entry)
				transcript = append(transcript, entry)
				continue
			}
			display := extractDebateDisplayContent(reply)
			if strings.TrimSpace(display) == "" {
				display = reply
			}
			_ = e.sendDebateRoleSpeech(ctx, room, role, display)
			entry := DebateTranscriptEntry{
				Round:     2 + room.Iteration,
				Speaker:   role.Instance,
				Role:      role.Role,
				PostedBy:  role.Instance,
				Content:   display,
				LatencyMS: latency,
			}
			_ = e.debateStore.AppendTranscript(room.RoomID, entry)
			transcript = append(transcript, entry)
			ApplyRoleContribution(board, role, 2+room.Iteration, reply)
			if board != nil {
				_ = e.debateStore.SaveBlackboard(board)
			}
		}

		room.Phase = consensusPhaseHostCheck
		_ = e.debateStore.SaveRoom(room)
		if board != nil {
			UpdateBlackboardWorkflow(board, DebateModeConsensus, consensusPhaseHostCheck, room.Iteration)
			_ = e.debateStore.SaveBlackboard(board)
		}

		hostReply, latency, err := e.askDebateRole(ctx, room, host, buildConsensusHostCheckPrompt(room, board, topic, room.Iteration))
		if err == nil {
			display := extractDebateDisplayContent(hostReply)
			if strings.TrimSpace(display) == "" {
				display = hostReply
			}
			_ = e.sendDebateRoleSpeech(ctx, room, host, display)
			entry := DebateTranscriptEntry{
				Round:     2 + room.Iteration,
				Speaker:   host.Instance,
				Role:      host.Role,
				PostedBy:  host.Instance,
				Content:   display,
				LatencyMS: latency,
			}
			_ = e.debateStore.AppendTranscript(room.RoomID, entry)
			transcript = append(transcript, entry)
			ApplyRoleContribution(board, host, 2+room.Iteration, hostReply)
		}
		if board != nil {
			board.ConsensusPoints, board.Unresolved = summarizeConsensusFromBoard(board, room)
			board.OpenQuestions = mergeUniqueQuestions(board.OpenQuestions, board.Unresolved, 12)
			_ = e.debateStore.SaveBlackboard(board)
		}
		if board == nil || len(board.Unresolved) == 0 {
			room.Phase = consensusPhaseFinalize
			break
		}
		room.Iteration++
		room.Phase = consensusPhaseAllResolve
		_ = e.debateStore.SaveRoom(room)
		if board != nil {
			UpdateBlackboardWorkflow(board, DebateModeConsensus, consensusPhaseAllResolve, room.Iteration)
			_ = e.debateStore.SaveBlackboard(board)
		}
	}

	room.Phase = consensusPhaseFinalize
	room.Status = DebateStatusSummarize
	room.StopReason = ""
	_ = e.debateStore.SaveRoom(room)
	if board != nil {
		UpdateBlackboardWorkflow(board, DebateModeConsensus, consensusPhaseFinalize, room.Iteration)
		_ = e.debateStore.SaveBlackboard(board)
	}
	if all, err := e.debateStore.LoadTranscript(room.RoomID); err == nil && len(all) > 0 {
		transcript = all
	}
	e.finalizeDebateSummary(room, transcript, board)
	room.Status = DebateStatusCompleted
	room.StopReason = ""
	room.Phase = "completed"
	_ = e.debateStore.SaveRoom(room)
}

func (e *Engine) finalizeDebateSummary(room *DebateRoom, transcript []DebateTranscriptEntry, board *DebateBlackboard) {
	summary := e.buildDebateSummary(room, transcript, board)
	summarySessionKey := buildRoleSessionKey(room.OwnerSessionKey, room.GroupChatID, room.RoomID, "jarvis_summary")
	if askRes, err := e.AskSession(summarySessionKey, summary, 120*time.Second); err == nil {
		summaryContent := strings.TrimSpace(askRes.Content)
		if retryNeeded, issues := debateSummaryNeedsRepair(summaryContent); retryNeeded {
			slog.Warn("debate: summary needs repair", "room_id", room.RoomID, "issues", issues)
			repairPrompt := buildDebateSummaryRepairPrompt(room, transcript, board, summaryContent, issues)
			if retryRes, retryErr := e.AskSession(summarySessionKey, repairPrompt, 120*time.Second); retryErr == nil {
				summaryContent = strings.TrimSpace(retryRes.Content)
			} else {
				slog.Warn("debate: summary repair failed", "room_id", room.RoomID, "error", retryErr)
			}
		}
		if retryNeeded, _ := debateSummaryNeedsRepair(summaryContent); retryNeeded {
			summaryContent = fallbackDebateSummary(room, transcript)
		}
		_ = e.SendBySessionKey(room.OwnerSessionKey, "【Jarvis-总结】\n"+summaryContent)
		return
	}
	_ = e.SendBySessionKey(room.OwnerSessionKey, "【Jarvis-总结】\n"+fallbackDebateSummary(room, transcript))
}

func splitConsensusRoles(roles []DebateRole) (DebateRole, []DebateRole) {
	var host DebateRole
	workers := make([]DebateRole, 0, len(roles))
	for _, r := range roles {
		if strings.EqualFold(r.Role, "jarvis") {
			host = r
			continue
		}
		workers = append(workers, r)
	}
	if strings.TrimSpace(host.Role) == "" && len(roles) > 0 {
		host = roles[0]
		workers = workers[:0]
		for i := 1; i < len(roles); i++ {
			workers = append(workers, roles[i])
		}
	}
	return host, workers
}

func currentConsensusTopic(room *DebateRoom) string {
	if room == nil {
		return ""
	}
	if strings.TrimSpace(room.RefinedQuestion) != "" {
		return strings.TrimSpace(room.RefinedQuestion)
	}
	return strings.TrimSpace(room.Question)
}

func defaultClarifyQuestions(question string) []string {
	qs := []string{
		"本次讨论的目标产物是什么（文档/方案/代码实现计划）？",
		"范围边界是什么（必须包含/明确不做）？",
		"成功标准是什么（如何判定讨论完成）？",
	}
	if strings.Contains(strings.ToLower(question), "unity") {
		qs = append(qs, "是否限定 Unity 版本、项目结构或已有框架约束？")
	}
	return qs
}

func (e *Engine) askDebateRole(ctx context.Context, room *DebateRoom, role DebateRole, prompt string) (string, int64, error) {
	socketPath := e.resolveRoleSocketPath(role)
	if strings.TrimSpace(socketPath) == "" {
		return "", 0, fmt.Errorf("socket path is empty")
	}
	req := AskRequest{
		Project:     emptyAs(role.Project, role.Instance),
		SessionKey:  buildRoleSessionKey(room.OwnerSessionKey, room.GroupChatID, room.RoomID, role.Role),
		Prompt:      flattenPromptForTransport(prompt),
		TimeoutSec:  140,
		Speak:       false,
		SpeakPrefix: "",
	}
	roleCtx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	res, err := e.instanceCli.Ask(roleCtx, socketPath, req)
	if err != nil {
		return "", 0, err
	}
	return strings.TrimSpace(res.Content), res.LatencyMS, nil
}

func (e *Engine) sendDebateRoleSpeech(ctx context.Context, room *DebateRoom, role DebateRole, displayContent string) error {
	socketPath := e.resolveRoleSocketPath(role)
	if strings.TrimSpace(socketPath) == "" {
		return fmt.Errorf("socket path is empty")
	}
	req := SendRequest{
		Project:    emptyAs(role.Project, role.Instance),
		SessionKey: buildRoleSessionKey(room.OwnerSessionKey, room.GroupChatID, room.RoomID, role.Role),
		Message:    fmt.Sprintf("【%s】%s", emptyAs(role.DisplayName, role.Role), strings.TrimSpace(displayContent)),
	}
	sendCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if err := e.instanceCli.Send(sendCtx, socketPath, req); err != nil {
		_ = e.SendBySessionKey(room.OwnerSessionKey, req.Message)
		return err
	}
	return nil
}

func buildConsensusHostSeedPrompt(room *DebateRoom, board *DebateBlackboard, topic, blackboardPath string) string {
	var b strings.Builder
	b.WriteString("你是主持人 Jarvis。当前进入“主持人先立论”阶段。\n")
	b.WriteString("任务：基于用户主题先给出初始观点、边界假设、关键疑问，并写回黑板。\n")
	b.WriteString(fmt.Sprintf("讨论主题：%s\n", topic))
	if p := strings.TrimSpace(blackboardPath); p != "" {
		b.WriteString(fmt.Sprintf("黑板路径：%s\n", filepath.ToSlash(p)))
	}
	if board != nil && len(board.OpenQuestions) > 0 {
		b.WriteString("待确认问题：\n")
		for i := 0; i < minInt(6, len(board.OpenQuestions)); i++ {
			b.WriteString(fmt.Sprintf("- %s\n", truncateStr(board.OpenQuestions[i], 100)))
		}
	}
	b.WriteString("\n输出格式：\n")
	b.WriteString("A) 群内可读内容（精简）：\n【主持人观点】\n【边界假设】\n【关键疑问】\n【建议下一步】\n\n")
	b.WriteString("B) 黑板回填 JSON：\n```json\n")
	b.WriteString("{\"type\":\"blackboard_writeback\",\"role\":\"jarvis\",\"stance\":\"\",\"basis\":\"\",\"risk\":\"\",\"action\":\"\"}\n")
	b.WriteString("```\n")
	return b.String()
}

func buildConsensusWorkerDivergePrompt(room *DebateRoom, role DebateRole, board *DebateBlackboard, topic, blackboardPath string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("你是 %s（%s）。当前进入“全员发散讨论”阶段。\n", role.DisplayName, role.Role))
	b.WriteString("要求：围绕主题和主持人观点提出你的观点与疑问，允许提出异议。\n")
	b.WriteString(fmt.Sprintf("主题：%s\n", topic))
	if p := strings.TrimSpace(blackboardPath); p != "" {
		b.WriteString(fmt.Sprintf("黑板路径：%s（先读取）\n", filepath.ToSlash(p)))
	}
	if board != nil {
		if hostNote, ok := board.RoleNotes["jarvis"]; ok && strings.TrimSpace(hostNote.LatestStance) != "" {
			b.WriteString(fmt.Sprintf("主持人观点：%s\n", truncateStr(hostNote.LatestStance, 200)))
		}
	}
	b.WriteString("\n输出格式：\n")
	b.WriteString("A) 群内可读内容（精简）：\n【我的观点】\n【对主持人观点的认同/异议】\n【我的疑问】\n【建议新增议题】\n\n")
	b.WriteString("B) 黑板回填 JSON：\n```json\n")
	b.WriteString("{\"type\":\"blackboard_writeback\",\"stance\":\"\",\"basis\":\"\",\"risk\":\"\",\"action\":\"\"}\n")
	b.WriteString("```\n")
	return b.String()
}

func buildConsensusHostCollectPrompt(room *DebateRoom, board *DebateBlackboard, topic string) string {
	var b strings.Builder
	b.WriteString("你是主持人 Jarvis。当前进入“汇总发散结果”阶段。\n")
	b.WriteString("请收集所有角色观点，整理为：已一致项、未一致项、待解问题。\n")
	b.WriteString(fmt.Sprintf("主题：%s\n", topic))
	if board != nil && len(board.HistoryDigest) > 0 {
		b.WriteString("近期发言摘要：\n")
		start := 0
		if len(board.HistoryDigest) > 8 {
			start = len(board.HistoryDigest) - 8
		}
		for i := start; i < len(board.HistoryDigest); i++ {
			b.WriteString("- " + truncateStr(board.HistoryDigest[i], 120) + "\n")
		}
	}
	b.WriteString("\n输出格式：\n")
	b.WriteString("【已一致项】\n【未一致项】\n【待解问题】\n【下一轮收敛焦点】\n")
	return b.String()
}

func buildConsensusWorkerResolvePrompt(room *DebateRoom, role DebateRole, board *DebateBlackboard, topic string, iteration int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("你是 %s（%s）。当前进入第 %d 轮“分歧收敛”讨论。\n", role.DisplayName, role.Role, iteration))
	b.WriteString("请重点回应黑板中的未一致项与待解问题，给出可执行解决方案。\n")
	b.WriteString(fmt.Sprintf("主题：%s\n", topic))
	if board != nil && len(board.Unresolved) > 0 {
		b.WriteString("未一致项：\n")
		for i := 0; i < minInt(8, len(board.Unresolved)); i++ {
			b.WriteString(fmt.Sprintf("- %s\n", truncateStr(board.Unresolved[i], 100)))
		}
	}
	if board != nil && len(board.OpenQuestions) > 0 {
		b.WriteString("待解问题：\n")
		for i := 0; i < minInt(8, len(board.OpenQuestions)); i++ {
			b.WriteString(fmt.Sprintf("- %s\n", truncateStr(board.OpenQuestions[i], 100)))
		}
	}
	b.WriteString("\n输出格式：\n")
	b.WriteString("【我支持的方案】\n【我不支持的点及原因】\n【可接受折中】\n【需要用户拍板的点】\n")
	return b.String()
}

func buildConsensusHostCheckPrompt(room *DebateRoom, board *DebateBlackboard, topic string, iteration int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("你是主持人 Jarvis。当前进行第 %d 轮共识判定。\n", iteration))
	b.WriteString("请明确：哪些点已达成一致、哪些点仍未一致、是否需要继续下一轮。\n")
	b.WriteString(fmt.Sprintf("主题：%s\n", topic))
	if board != nil && len(board.HistoryDigest) > 0 {
		b.WriteString("参考摘要：\n")
		start := 0
		if len(board.HistoryDigest) > 10 {
			start = len(board.HistoryDigest) - 10
		}
		for i := start; i < len(board.HistoryDigest); i++ {
			b.WriteString("- " + truncateStr(board.HistoryDigest[i], 120) + "\n")
		}
	}
	b.WriteString("\n输出格式：\n")
	b.WriteString("【已达成一致】\n【仍未一致】\n【需用户拍板】\n【下一步】\n")
	return b.String()
}

func summarizeConsensusFromBoard(board *DebateBlackboard, room *DebateRoom) ([]string, []string) {
	if board == nil {
		return nil, nil
	}
	agreed := make([]string, 0, 8)
	unresolved := make([]string, 0, 8)

	for _, role := range room.Roles {
		if strings.EqualFold(role.Role, "jarvis") {
			continue
		}
		n, ok := board.RoleNotes[role.Role]
		if !ok {
			continue
		}
		st := strings.TrimSpace(n.LatestStance)
		if st == "" {
			continue
		}
		low := strings.ToLower(st)
		if strings.Contains(low, "不支持") || strings.Contains(low, "分歧") || strings.Contains(low, "异议") {
			unresolved = append(unresolved, fmt.Sprintf("%s：%s", emptyAs(role.DisplayName, role.Role), truncateStr(st, 80)))
		} else {
			agreed = append(agreed, fmt.Sprintf("%s：%s", emptyAs(role.DisplayName, role.Role), truncateStr(st, 80)))
		}
	}
	if len(unresolved) == 0 {
		for _, q := range board.OpenQuestions {
			if strings.TrimSpace(q) == "" {
				continue
			}
			if strings.Contains(q, "？") || strings.Contains(q, "?") {
				unresolved = append(unresolved, truncateStr(q, 80))
			}
		}
	}
	agreed = dedupeLines(agreed, 8)
	unresolved = dedupeLines(unresolved, 8)
	return agreed, unresolved
}

func dedupeLines(lines []string, max int) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

func nonEmptyOrDash(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return []string{"（无）"}
	}
	return out
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
	case "all-workers-final", "host-decide-all-final":
		if maxRounds > 0 && round >= maxRounds {
			return workers
		}
		return selectHostDecideSpeakers(round, workers, spoken, maxRounds)
	default: // host-decide
		return selectHostDecideSpeakers(round, workers, spoken, maxRounds)
	}
}

func selectHostDecideSpeakers(round int, workers []DebateRole, spoken map[string]bool, maxRounds int) []DebateRole {
	if len(workers) == 0 {
		return nil
	}
	// Final round should include all workers so everyone can confirm or add dissent.
	if maxRounds > 0 && round >= maxRounds {
		return workers
	}
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

func extractDebateDisplayContent(reply string) string {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return ""
	}
	_, visible := extractWritebackPayload(reply)
	visible = strings.TrimSpace(visible)
	if visible == "" {
		visible = reply
	}
	if loc := debateWritebackSectionStartPattern.FindStringIndex(visible); loc != nil {
		visible = strings.TrimSpace(visible[:loc[0]])
	}
	visible = debateDisplayHeaderPattern.ReplaceAllString(visible, "")
	return strings.TrimSpace(visible)
}

func buildDebateSummaryRepairPrompt(room *DebateRoom, transcript []DebateTranscriptEntry, board *DebateBlackboard, badSummary string, issues []string) string {
	var b strings.Builder
	b.WriteString("你刚才的总结未通过校验。不要索要额外材料，必须基于当前上下文直接输出。\n")
	if len(issues) > 0 {
		b.WriteString("问题：\n")
		for _, issue := range issues {
			if strings.TrimSpace(issue) == "" {
				continue
			}
			b.WriteString("- " + issue + "\n")
		}
	}
	b.WriteString("\n请按以下结构输出（中文）：\n")
	b.WriteString("1) 最终结论（最多3条）\n")
	b.WriteString("2) 主要风险（最多3条）\n")
	b.WriteString("3) 行动项（每条必须含 owner + deadline + 验收标准）\n\n")
	b.WriteString("补充硬约束：行动项里禁止“负责人待定/TBD/待确认”，如暂不确定请指定临时 owner。\n\n")
	b.WriteString(fmt.Sprintf("主题：%s\n", room.Question))
	if board != nil && strings.TrimSpace(board.Goal) != "" {
		b.WriteString(fmt.Sprintf("目标：%s\n", board.Goal))
	}
	b.WriteString("你上一条不合格总结如下（仅供纠偏，勿复述）：\n")
	b.WriteString(truncateStr(strings.TrimSpace(badSummary), 300))
	b.WriteString("\n")
	if len(transcript) > 0 {
		b.WriteString("最近讨论片段（用于你快速聚焦）：\n")
		start := 0
		if len(transcript) > 4 {
			start = len(transcript) - 4
		}
		for i := start; i < len(transcript); i++ {
			t := transcript[i]
			b.WriteString(fmt.Sprintf("- [R%d][%s] %s\n", t.Round, t.Role, truncateStr(t.Content, 140)))
		}
	}
	return b.String()
}

var debateMenuReplyPattern = regexp.MustCompile(`(?is)(回复\s*1\s*/\s*2\s*/\s*3\s*/\s*4|你要选哪一类|先选方向|先确认你要做哪一类|TAPD（需求/缺陷/验收/回填）|OpenSpec 发布到 HTTP|Unity\s*/\s*AGame|代码实现\s*/\s*调试\s*/\s*文档)`)
var debateWritebackSectionStartPattern = regexp.MustCompile(`(?is)\bB\)\s*(黑板回填|writeback|回填)`)
var debateDisplayHeaderPattern = regexp.MustCompile(`(?is)^\s*A\)\s*群内可读内容（精简）[:：]?\s*`)
var debateSummaryRefusalPattern = regexp.MustCompile(`(?is)(贴出来|发我后|你把.*发我|我才能总结|未指定格式|请提供.*记录|需要.*记录)`)
var debateSummaryPendingOwnerPattern = regexp.MustCompile(`(?is)(待定负责人|负责人待定|owner待定|责任人待定|(owner|负责人|责任人)\s*[:：]?\s*(待定|待确认|待指定|未定|tbd|unknown)|AI负责人（待定）|QA（待定）)`)

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

func debateSummaryNeedsRepair(summary string) (bool, []string) {
	summary = strings.TrimSpace(summary)
	issues := make([]string, 0, 4)
	if summary == "" {
		issues = append(issues, "总结内容为空")
		return true, issues
	}
	if debateSummaryRefusalPattern.MatchString(summary) {
		issues = append(issues, "总结内容在索要额外输入材料")
	}
	if !strings.Contains(summary, "结论") && !strings.Contains(summary, "共识") {
		issues = append(issues, "缺少最终结论段")
	}
	if !strings.Contains(summary, "风险") {
		issues = append(issues, "缺少主要风险段")
	}
	if !strings.Contains(summary, "行动项") && !strings.Contains(strings.ToLower(summary), "owner") && !strings.Contains(summary, "负责人") {
		issues = append(issues, "缺少行动项（owner/deadline/验收）")
	}
	if debateSummaryPendingOwnerPattern.MatchString(summary) {
		issues = append(issues, "行动项存在待定负责人")
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
	b.WriteString("4) 输出语言：中文。\n")
	b.WriteString("5) 行动项里禁止“负责人待定/TBD/待确认”，如不确定请指定临时 owner。\n\n")
	topic := room.Question
	if strings.TrimSpace(room.RefinedQuestion) != "" {
		topic = room.RefinedQuestion
	}
	b.WriteString(fmt.Sprintf("讨论主题：%s\n", topic))
	if board != nil {
		if strings.TrimSpace(board.RefinedTopic) != "" {
			b.WriteString(fmt.Sprintf("黑板澄清主题：%s\n", board.RefinedTopic))
		}
		if strings.TrimSpace(board.Phase) != "" {
			b.WriteString(fmt.Sprintf("当前流程阶段：%s（iteration=%d）\n", board.Phase, board.Iteration))
		}
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
	b.WriteString("1) 最终结论\n")
	b.WriteString(fmt.Sprintf("- 主题已收敛：%s\n", room.Question))
	max := minInt(3, len(transcript))
	for i := 0; i < max; i++ {
		t := transcript[len(transcript)-max+i]
		b.WriteString(fmt.Sprintf("- [%s] %s\n", emptyAs(t.Role, "worker"), truncateStr(t.Content, 90)))
	}
	b.WriteString("\n2) 主要风险\n")
	b.WriteString("- 讨论过程中可能存在信息缺口，需在落地前补充边界条件与验收口径。\n")
	b.WriteString("- 若职责不明确，后续执行可能出现串行阻塞或返工。\n")
	b.WriteString("\n3) 行动项\n")
	b.WriteString(fmt.Sprintf("- owner: Jarvis, deadline: %s, 验收标准: 输出可执行任务清单（含负责人、里程碑、验收标准）并完成确认。\n", time.Now().Add(24*time.Hour).Format("2006-01-02")))
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
