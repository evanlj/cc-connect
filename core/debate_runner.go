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
	consensusPhaseInit               = "init"
	consensusPhaseTopicRefine        = "topic_refine_host"
	consensusPhaseTopicConfirm       = "topic_confirm_user"
	consensusPhaseHostFirstProposal  = "host_first_proposal"
	consensusPhaseUserReview         = "user_proposal_review"
	consensusPhaseParticipantConfirm = "participant_confirm"
	consensusPhaseAllDiverge         = "all_diverge"
	consensusPhaseHostCollect        = "host_collect"
	consensusPhaseAllResolve         = "all_resolve"
	consensusPhaseHostCheck          = "host_consensus_check"
	consensusPhaseFinalizeSingle     = "finalize_single"
	consensusPhaseFinalize           = "host_finalize"
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
		syncBlackboardWithRoom(board, room)
		board.Mode = DebateModeConsensus
		UpdateBlackboardWorkflow(board, DebateModeConsensus, emptyAs(room.Phase, consensusPhaseInit), room.Iteration)
		_ = e.debateStore.SaveBlackboard(board)
	}

	host, _ := splitConsensusRolesForRoom(room, false)
	if strings.TrimSpace(host.Role) == "" {
		room.Status = DebateStatusFailed
		room.StopReason = "no_host"
		_ = e.debateStore.SaveRoom(room)
		_ = e.SendBySessionKey(room.OwnerSessionKey, "【Jarvis】讨论失败：未找到主持人角色。")
		return
	}

	if strings.TrimSpace(room.Phase) == "" {
		room.Phase = consensusPhaseInit
	}
	if room.MaxRounds <= 0 {
		room.MaxRounds = DefaultDebateMaxRounds
	}
	if strings.TrimSpace(room.UserReviewStatus) == "" {
		room.UserReviewStatus = "pending"
	}
	if room.Status == DebateStatusCreated {
		room.Status = DebateStatusRunning
	}
	_ = e.debateStore.SaveRoom(room)

	transcript := make([]DebateTranscriptEntry, 0, room.MaxRounds*8)
	if old, err := e.debateStore.LoadTranscript(room.RoomID); err == nil {
		transcript = append(transcript, old...)
	}

	appendEntry := func(entry DebateTranscriptEntry) {
		_ = e.debateStore.AppendTranscript(room.RoomID, entry)
		transcript = append(transcript, entry)
	}

	waitAndExit := func(stopReason, message string) {
		room.Status = DebateStatusWaiting
		room.StopReason = stopReason
		_ = e.debateStore.SaveRoom(room)
		if board != nil {
			syncBlackboardWithRoom(board, room)
			UpdateBlackboardWorkflow(board, DebateModeConsensus, room.Phase, room.Iteration)
			_ = e.debateStore.SaveBlackboard(board)
		}
		if strings.TrimSpace(message) != "" {
			_ = e.SendBySessionKey(room.OwnerSessionKey, message)
		}
	}

	for {
		select {
		case <-ctx.Done():
			room.Status = DebateStatusStopped
			room.StopReason = "manual_stop"
			_ = e.debateStore.SaveRoom(room)
			return
		default:
		}

		if board != nil {
			syncBlackboardWithRoom(board, room)
			UpdateBlackboardWorkflow(board, DebateModeConsensus, room.Phase, room.Iteration)
			_ = e.debateStore.SaveBlackboard(board)
		}

		topic := currentConsensusTopic(room)

		switch room.Phase {
		case consensusPhaseInit:
			room.Phase = consensusPhaseTopicRefine
			room.Status = DebateStatusRunning
			room.StopReason = ""
			_ = e.debateStore.SaveRoom(room)
			continue

		case consensusPhaseTopicRefine:
			_ = e.SendBySessionKey(room.OwnerSessionKey, "【Jarvis】阶段1/8：主持人先自动完善你的议题。")
			reply, latency, err := e.askDebateRole(ctx, room, host, buildConsensusTopicRefinePrompt(room, board, topic))
			if err != nil {
				fallback := buildConsensusTopicDraftFallback(topic)
				_ = e.SendBySessionKey(room.OwnerSessionKey, fmt.Sprintf("【Jarvis】议题完善超时，已启用兜底草案：%s", truncateStr(err.Error(), 120)))
				reply = fallback
				latency = 0
			}
			display := extractDebateDisplayContent(reply)
			if strings.TrimSpace(display) == "" {
				display = reply
			}
			_ = e.sendDebateRoleSpeech(ctx, room, host, display)
			appendEntry(DebateTranscriptEntry{
				Round:     0,
				Speaker:   host.Instance,
				Role:      host.Role,
				PostedBy:  host.Instance,
				Content:   display,
				LatencyMS: latency,
			})
			contrib := ApplyRoleContribution(board, host, 0, reply)
			topicDraft := normalizeTopicDraft(firstNonEmpty(
				extractTopicDraftFromReply(reply),
				contrib.Stance,
				display,
				topic,
			))
			if topicDraft == "" {
				topicDraft = normalizeTopicDraft(topic)
			}
			room.TopicDraft = topicDraft
			room.RefinedQuestion = ""
			room.UserReviewStatus = "pending"
			room.UserReviewFeedback = ""
			room.Status = DebateStatusRunning
			room.StopReason = ""
			room.Phase = consensusPhaseTopicConfirm
			_ = e.debateStore.SaveRoom(room)

			if board != nil {
				board.TopicDraft = topicDraft
				board.RefinedTopic = ""
				board.FinalTopicLocked = false
				board.UserReviewStatus = "pending"
				board.UserReviewFeedback = ""
				board.ParticipantCandidates = defaultConsensusParticipantCandidates(room)
				board.OpenQuestions = mergeUniqueQuestions(board.OpenQuestions, extractQuestions(reply), 12)
				UpdateBlackboardWorkflow(board, DebateModeConsensus, consensusPhaseTopicConfirm, room.Iteration)
				_ = e.debateStore.SaveBlackboard(board)
			}
			waitAndExit("await_user_final_topic",
				fmt.Sprintf("【Jarvis】我已基于你的原始话题整理出“增强议题草案”：\n%s\n\n请你在此基础上修改并提交最终议题：`/debate topic %s <最终议题>`", topicDraft, room.RoomID))
			return

		case consensusPhaseTopicConfirm:
			waitAndExit("await_user_final_topic",
				fmt.Sprintf("【Jarvis】等待你提交最终议题：`/debate topic %s <最终议题>`", room.RoomID))
			return

		case consensusPhaseHostFirstProposal:
			_ = e.SendBySessionKey(room.OwnerSessionKey, "【Jarvis】阶段3/8：基于最终议题，主持人给出第一轮回答。")
			reply, latency, err := e.askDebateRole(ctx, room, host, buildConsensusHostSeedPrompt(room, board, topic, blackboardPath))
			finalReply := reply
			finalDisplay := extractDebateDisplayContent(reply)
			if err != nil {
				fallback := buildConsensusHostSeedFallback(topic, board)
				finalReply = fallback
				finalDisplay = fallback
				_ = e.SendBySessionKey(room.OwnerSessionKey, fmt.Sprintf("【Jarvis】主持人首轮立论超时，已启用兜底方案继续流程：%s", truncateStr(err.Error(), 120)))
			}
			if strings.TrimSpace(finalDisplay) == "" {
				finalDisplay = finalReply
			}
			_ = e.sendDebateRoleSpeech(ctx, room, host, finalDisplay)
			entry := DebateTranscriptEntry{
				Round:    0,
				Speaker:  host.Instance,
				Role:     host.Role,
				PostedBy: host.Instance,
				Content:  finalDisplay,
			}
			if err == nil {
				entry.LatencyMS = latency
			}
			appendEntry(entry)
			contrib := ApplyRoleContribution(board, host, 0, finalReply)
			if board != nil {
				board.RefinedTopic = topic
				board.FinalTopicLocked = strings.TrimSpace(topic) != ""
				board.UserReviewStatus = "pending"
				board.UserReviewFeedback = ""
				board.HostFirstProposal = truncateStr(strings.TrimSpace(contrib.Stance), 500)
				riskLines := extractQuestions(contrib.Risk)
				if len(riskLines) == 0 && strings.TrimSpace(contrib.Risk) != "" {
					riskLines = []string{truncateStr(strings.TrimSpace(contrib.Risk), 120)}
				}
				board.HostFirstProposalRisks = dedupeLines(riskLines, 6)
				UpdateBlackboardWorkflow(board, DebateModeConsensus, consensusPhaseHostFirstProposal, room.Iteration)
				_ = e.debateStore.SaveBlackboard(board)
			}
			room.Phase = consensusPhaseUserReview
			room.UserReviewStatus = "pending"
			room.UserReviewFeedback = ""
			room.Status = DebateStatusRunning
			room.StopReason = ""
			_ = e.debateStore.SaveRoom(room)
			waitAndExit("await_user_review", fmt.Sprintf("【Jarvis】请评审主持人首轮方案：\n- 满意：`/debate decision %s approve [可选反馈]`\n- 不满意：`/debate decision %s reject <反馈>`", room.RoomID, room.RoomID))
			return

		case consensusPhaseUserReview:
			switch strings.ToLower(strings.TrimSpace(room.UserReviewStatus)) {
			case "approved":
				room.Phase = consensusPhaseFinalizeSingle
				room.Status = DebateStatusRunning
				room.StopReason = ""
				_ = e.debateStore.SaveRoom(room)
				continue
			case "rejected":
				room.Phase = consensusPhaseParticipantConfirm
				room.Status = DebateStatusRunning
				room.StopReason = ""
				_ = e.debateStore.SaveRoom(room)
				continue
			default:
				waitAndExit("await_user_review", fmt.Sprintf("【Jarvis】等待用户评审主持人首轮方案：`/debate decision %s approve|reject [反馈]`", room.RoomID))
				return
			}

		case consensusPhaseParticipantConfirm:
			_, workers := splitConsensusRolesForRoom(room, true)
			if len(workers) == 0 {
				catalog := consensusWorkerRoleCatalog(room)
				candidates := make([]string, 0, len(catalog))
				for _, role := range catalog {
					candidates = append(candidates, role.Role)
				}
				if board != nil {
					board.ParticipantCandidates = cloneStringSlice(candidates)
					board.ParticipantConfirmed = cloneStringSlice(room.ConfirmedParticipants)
					board.UserReviewStatus = emptyAs(strings.TrimSpace(room.UserReviewStatus), "rejected")
					board.UserReviewFeedback = strings.TrimSpace(room.UserReviewFeedback)
					UpdateBlackboardWorkflow(board, DebateModeConsensus, consensusPhaseParticipantConfirm, room.Iteration)
					_ = e.debateStore.SaveBlackboard(board)
				}
				waitAndExit("await_participants_confirm",
					fmt.Sprintf("【Jarvis】第一轮方案未满足预期，进入多人讨论前请先选择参与角色（用编号）：\n%s\n\n选择命令：`/debate participants %s 1,2,3`",
						formatConsensusRoleCatalog(catalog, true), room.RoomID))
				return
			}
			room.Phase = consensusPhaseAllDiverge
			room.Status = DebateStatusRunning
			room.StopReason = ""
			_ = e.debateStore.SaveRoom(room)
			continue

		case consensusPhaseAllDiverge:
			_, workers := splitConsensusRolesForRoom(room, true)
			if len(workers) == 0 {
				catalog := consensusWorkerRoleCatalog(room)
				waitAndExit("await_participants_confirm",
					fmt.Sprintf("【Jarvis】未找到有效参与者，请按编号重新确认：\n%s\n\n`/debate participants %s 1,2,3`",
						formatConsensusRoleCatalog(catalog, true), room.RoomID))
				return
			}
			_ = e.SendBySessionKey(room.OwnerSessionKey, fmt.Sprintf("【Jarvis】阶段5/8：全员发散讨论（%d位）。", len(workers)))
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
					appendEntry(entry)
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
				appendEntry(entry)
				ApplyRoleContribution(board, role, 1, reply)
				if board != nil {
					_ = e.debateStore.SaveBlackboard(board)
				}
			}
			room.Phase = consensusPhaseHostCollect
			room.Status = DebateStatusRunning
			room.StopReason = ""
			_ = e.debateStore.SaveRoom(room)
			continue

		case consensusPhaseHostCollect:
			_ = e.SendBySessionKey(room.OwnerSessionKey, "【Jarvis】阶段6/8：主持人收集分歧与疑问。")
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
				appendEntry(entry)
				ApplyRoleContribution(board, host, 2, reply)
			}
			if board != nil {
				board.ConsensusPoints, board.Unresolved = summarizeConsensusFromBoard(board, room)
				board.OpenQuestions = mergeUniqueQuestions(board.OpenQuestions, board.Unresolved, 16)
				UpdateBlackboardWorkflow(board, DebateModeConsensus, consensusPhaseHostCollect, room.Iteration)
				_ = e.debateStore.SaveBlackboard(board)
			}
			room.Phase = consensusPhaseAllResolve
			if room.Iteration <= 0 {
				room.Iteration = 1
			}
			room.Status = DebateStatusRunning
			room.StopReason = ""
			_ = e.debateStore.SaveRoom(room)
			continue

		case consensusPhaseAllResolve:
			_, workers := splitConsensusRolesForRoom(room, true)
			if len(workers) == 0 {
				catalog := consensusWorkerRoleCatalog(room)
				waitAndExit("await_participants_confirm",
					fmt.Sprintf("【Jarvis】参与者为空，请按编号重新确认：\n%s\n\n`/debate participants %s 1,2,3`",
						formatConsensusRoleCatalog(catalog, true), room.RoomID))
				return
			}
			if room.Iteration <= 0 {
				room.Iteration = 1
			}
			_ = e.SendBySessionKey(room.OwnerSessionKey, fmt.Sprintf("【Jarvis】阶段7/8：第 %d 轮分歧收敛讨论。", room.Iteration))
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
					appendEntry(entry)
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
				appendEntry(entry)
				ApplyRoleContribution(board, role, 2+room.Iteration, reply)
				if board != nil {
					_ = e.debateStore.SaveBlackboard(board)
				}
			}
			room.Phase = consensusPhaseHostCheck
			room.Status = DebateStatusRunning
			room.StopReason = ""
			_ = e.debateStore.SaveRoom(room)
			continue

		case consensusPhaseHostCheck:
			if board != nil {
				UpdateBlackboardWorkflow(board, DebateModeConsensus, consensusPhaseHostCheck, room.Iteration)
				_ = e.debateStore.SaveBlackboard(board)
			}
			_ = e.SendBySessionKey(room.OwnerSessionKey, fmt.Sprintf("【Jarvis】阶段8/8：第 %d 轮共识判定（主持人收敛）。", room.Iteration))
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
				appendEntry(entry)
				ApplyRoleContribution(board, host, 2+room.Iteration, hostReply)
			}
			if board != nil {
				board.ConsensusPoints, board.Unresolved = summarizeConsensusFromBoard(board, room)
				board.OpenQuestions = mergeUniqueQuestions(board.OpenQuestions, board.Unresolved, 16)
				_ = e.debateStore.SaveBlackboard(board)
			}
			if board == nil || len(board.Unresolved) == 0 {
				room.Phase = consensusPhaseFinalize
				room.Status = DebateStatusRunning
				room.StopReason = ""
				_ = e.debateStore.SaveRoom(room)
				continue
			}
			room.Iteration++
			room.Phase = consensusPhaseAllResolve
			room.Status = DebateStatusRunning
			room.StopReason = ""
			_ = e.debateStore.SaveRoom(room)
			continue

		case consensusPhaseFinalizeSingle, consensusPhaseFinalize:
			room.Status = DebateStatusSummarize
			room.StopReason = ""
			_ = e.debateStore.SaveRoom(room)
			if board != nil {
				UpdateBlackboardWorkflow(board, DebateModeConsensus, room.Phase, room.Iteration)
				_ = e.debateStore.SaveBlackboard(board)
			}
			if all, err := e.debateStore.LoadTranscript(room.RoomID); err == nil && len(all) > 0 {
				transcript = all
			}
			summary := e.finalizeDebateSummary(room, transcript, board)
			reportContent := renderDebateFinalReport(room, board, transcript, summary)
			reportPath, reportErr := e.debateStore.SaveFinalReport(room.RoomID, reportContent)
			if reportErr != nil {
				slog.Warn("debate(consensus): save final report failed", "room_id", room.RoomID, "error", reportErr)
			}
			room.Status = DebateStatusCompleted
			room.StopReason = ""
			room.Phase = "completed"
			_ = e.debateStore.SaveRoom(room)
			if strings.TrimSpace(reportPath) != "" {
				_ = e.SendBySessionKey(room.OwnerSessionKey, fmt.Sprintf("【Jarvis】讨论已结束，成果文档已生成：`%s`", reportPath))
			}
			return

		default:
			room.Phase = consensusPhaseTopicRefine
			room.Status = DebateStatusRunning
			room.StopReason = ""
			_ = e.debateStore.SaveRoom(room)
			continue
		}
	}
}

func (e *Engine) finalizeDebateSummary(room *DebateRoom, transcript []DebateTranscriptEntry, board *DebateBlackboard) string {
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
		return summaryContent
	}
	fallback := fallbackDebateSummary(room, transcript)
	_ = e.SendBySessionKey(room.OwnerSessionKey, "【Jarvis-总结】\n"+fallback)
	return fallback
}

func splitConsensusRoles(roles []DebateRole) (DebateRole, []DebateRole) {
	room := &DebateRoom{
		Roles:    roles,
		HostRole: "jarvis",
	}
	return splitConsensusRolesForRoom(room, false)
}

func splitConsensusRolesForRoom(room *DebateRoom, requireConfirmedWorkers bool) (DebateRole, []DebateRole) {
	if room == nil {
		return DebateRole{}, nil
	}
	roles := room.Roles
	var host DebateRole
	hostKey := resolveDebateRoleKey(roles, room.HostRole)
	if hostKey == "" {
		hostKey = resolveDebateRoleKey(roles, "jarvis")
	}
	if hostKey == "" && len(roles) > 0 {
		hostKey = roles[0].Role
	}
	workersAll := make([]DebateRole, 0, len(roles))
	for _, r := range roles {
		if strings.EqualFold(r.Role, hostKey) {
			host = r
			continue
		}
		workersAll = append(workersAll, r)
	}
	if strings.TrimSpace(host.Role) == "" && len(roles) > 0 {
		host = roles[0]
		workersAll = workersAll[:0]
		for i := 1; i < len(roles); i++ {
			workersAll = append(workersAll, roles[i])
		}
	}
	if !requireConfirmedWorkers || len(room.ConfirmedParticipants) == 0 {
		return host, workersAll
	}
	confirmSet := map[string]bool{}
	for _, key := range normalizeDebateRoleList(roles, room.ConfirmedParticipants, host.Role) {
		confirmSet[key] = true
	}
	filtered := make([]DebateRole, 0, len(workersAll))
	for _, role := range workersAll {
		if confirmSet[role.Role] {
			filtered = append(filtered, role)
		}
	}
	return host, filtered
}

func currentConsensusTopic(room *DebateRoom) string {
	if room == nil {
		return ""
	}
	if strings.TrimSpace(room.RefinedQuestion) != "" {
		return strings.TrimSpace(room.RefinedQuestion)
	}
	if strings.TrimSpace(room.TopicDraft) != "" {
		return strings.TrimSpace(room.TopicDraft)
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

func buildConsensusTopicRefinePrompt(room *DebateRoom, board *DebateBlackboard, topic string) string {
	var b strings.Builder
	b.WriteString("你是主持人 Jarvis。当前任务：在不改变用户意图的前提下，自动完善用户议题。\n")
	b.WriteString("要求：\n")
	b.WriteString("1) 输出一版“增强议题草案”，必须可直接用于后续方案讨论；\n")
	b.WriteString("2) 补齐目标、范围边界、交付物、验收口径；\n")
	b.WriteString("3) 不要开始给解决方案，只做议题澄清与结构化；\n")
	b.WriteString("4) 用中文，简洁。\n\n")
	b.WriteString(fmt.Sprintf("用户原始议题：%s\n", topic))
	if room != nil && strings.TrimSpace(room.Question) != "" && strings.TrimSpace(room.Question) != strings.TrimSpace(topic) {
		b.WriteString(fmt.Sprintf("原始文本：%s\n", room.Question))
	}
	if board != nil && len(board.OpenQuestions) > 0 {
		b.WriteString("历史待确认点：\n")
		for i := 0; i < minInt(6, len(board.OpenQuestions)); i++ {
			b.WriteString(fmt.Sprintf("- %s\n", truncateStr(board.OpenQuestions[i], 100)))
		}
	}
	b.WriteString("\n输出格式：\n")
	b.WriteString("【增强议题草案】\n")
	b.WriteString("【补充假设】\n")
	b.WriteString("【边界与不做项】\n")
	b.WriteString("【验收标准】\n")
	return b.String()
}

func buildConsensusTopicDraftFallback(topic string) string {
	var b strings.Builder
	b.WriteString("【增强议题草案】\n")
	b.WriteString(fmt.Sprintf("围绕“%s”形成可执行讨论议题：明确目标产物、范围边界、关键依赖、验收标准，并输出可落地方案。\n", truncateStr(topic, 120)))
	b.WriteString("【补充假设】\n")
	b.WriteString("- 目标产物至少包含方案结构、执行步骤与验收口径。\n")
	b.WriteString("【边界与不做项】\n")
	b.WriteString("- 本轮仅讨论方案，不直接改代码或上线。\n")
	b.WriteString("【验收标准】\n")
	b.WriteString("- 结论可执行，且行动项包含 owner、deadline、验收标准。\n")
	return strings.TrimSpace(b.String())
}

func extractTopicDraftFromReply(reply string) string {
	visible := extractDebateDisplayContent(reply)
	if strings.TrimSpace(visible) == "" {
		visible = strings.TrimSpace(reply)
	}
	lines := strings.Split(strings.ReplaceAll(visible, "\r\n", "\n"), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.Contains(line, "增强议题草案") || strings.Contains(line, "议题草案") || strings.Contains(line, "最终议题建议") {
			line = strings.TrimPrefix(line, "【增强议题草案】")
			line = strings.TrimPrefix(line, "【议题草案】")
			line = strings.TrimPrefix(line, "【最终议题建议】")
			line = strings.TrimPrefix(line, "增强议题草案：")
			line = strings.TrimPrefix(line, "增强议题草案:")
			line = strings.TrimPrefix(line, "议题草案：")
			line = strings.TrimPrefix(line, "议题草案:")
			line = strings.TrimSpace(line)
			if line != "" {
				return line
			}
		}
	}
	sections := splitStructuredSections(visible)
	if v := strings.TrimSpace(sections["stance"]); v != "" {
		return firstMeaningfulLine(v)
	}
	return firstMeaningfulLine(visible)
}

func normalizeTopicDraft(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if raw == "" {
		return ""
	}
	lines := strings.Split(raw, "\n")
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "-•*0123456789.、) ）")
		if line == "" {
			continue
		}
		clean = append(clean, line)
	}
	if len(clean) == 0 {
		return ""
	}
	return truncateStr(strings.Join(clean, "；"), 500)
}

func consensusWorkerRoleCatalog(room *DebateRoom) []DebateRole {
	if room == nil {
		return nil
	}
	_, workers := splitConsensusRolesForRoom(room, false)
	return workers
}

func formatConsensusRoleCatalog(roles []DebateRole, isZh bool) string {
	if len(roles) == 0 {
		if isZh {
			return "（无可选角色）"
		}
		return "(no available roles)"
	}
	var b strings.Builder
	for i, role := range roles {
		idx := i + 1
		name := emptyAs(strings.TrimSpace(role.DisplayName), role.Role)
		identity := debateRoleIdentity(role, isZh)
		if isZh {
			b.WriteString(fmt.Sprintf("%d) %s（%s）- %s\n", idx, name, role.Role, identity))
		} else {
			b.WriteString(fmt.Sprintf("%d) %s (%s) - %s\n", idx, name, role.Role, identity))
		}
	}
	return strings.TrimSpace(b.String())
}

func debateRoleIdentity(role DebateRole, isZh bool) string {
	key := strings.ToLower(strings.TrimSpace(role.Role))
	if isZh {
		switch key {
		case "jarvis":
			return "主持人 / 收敛官"
		case "jianzhu":
			return "架构与技术拆解"
		case "wendan":
			return "表达与方案文档"
		case "xingzou":
			return "执行路径与落地推进"
		case "zhanggui":
			return "资源评估与风险控制"
		default:
			return "专项顾问"
		}
	}
	switch key {
	case "jarvis":
		return "host / convergence lead"
	case "jianzhu":
		return "architecture & technical decomposition"
	case "wendan":
		return "narrative & documentation design"
	case "xingzou":
		return "execution path & delivery"
	case "zhanggui":
		return "resource, cost and risk control"
	default:
		return "specialized advisor"
	}
}

func summarizeClarificationAnswers(transcript []DebateTranscriptEntry) string {
	if len(transcript) == 0 {
		return ""
	}
	lines := make([]string, 0, 8)
	for _, item := range transcript {
		if !strings.EqualFold(item.Role, "user") {
			continue
		}
		content := strings.TrimSpace(item.Content)
		if !strings.HasPrefix(content, "clarification_answer:") {
			continue
		}
		content = strings.TrimSpace(strings.TrimPrefix(content, "clarification_answer:"))
		if content == "" {
			continue
		}
		lines = append(lines, truncateStr(content, 120))
	}
	if len(lines) == 0 {
		return ""
	}
	start := 0
	if len(lines) > 4 {
		start = len(lines) - 4
	}
	return strings.Join(lines[start:], "；")
}

func buildConsensusClarifyPrompt(room *DebateRoom, board *DebateBlackboard, topic string, transcript []DebateTranscriptEntry) string {
	var b strings.Builder
	b.WriteString("你是主持人 Jarvis。当前处于“议题澄清”阶段。\n")
	b.WriteString("目标：先理解用户议题并通过一问一答补齐信息；信息充分后再进入首轮方案回答。\n")
	b.WriteString("规则：\n")
	b.WriteString("1) 每轮最多只问 1 个最关键的问题；\n")
	b.WriteString("2) 若信息已足够，必须明确标记无需继续澄清；\n")
	b.WriteString("3) 输出不能菜单化，不能让用户选 1/2/3/4。\n\n")
	b.WriteString(fmt.Sprintf("原始议题：%s\n", topic))
	if room != nil && strings.TrimSpace(room.RefinedQuestion) != "" {
		b.WriteString(fmt.Sprintf("当前议题草案：%s\n", room.RefinedQuestion))
	}
	if board != nil && len(board.OpenQuestions) > 0 {
		b.WriteString("当前待确认问题：\n")
		for i := 0; i < minInt(6, len(board.OpenQuestions)); i++ {
			b.WriteString(fmt.Sprintf("- %s\n", truncateStr(board.OpenQuestions[i], 100)))
		}
	}
	if len(transcript) > 0 {
		b.WriteString("最近澄清对话：\n")
		snippets := make([]string, 0, 10)
		for i := len(transcript) - 1; i >= 0 && len(snippets) < 10; i-- {
			item := transcript[i]
			role := strings.ToLower(strings.TrimSpace(item.Role))
			if role != "user" && role != "jarvis" {
				continue
			}
			content := strings.TrimSpace(item.Content)
			if role == "user" && strings.HasPrefix(content, "clarification_answer:") {
				content = strings.TrimSpace(strings.TrimPrefix(content, "clarification_answer:"))
				if content == "" {
					continue
				}
				snippets = append(snippets, fmt.Sprintf("- [用户] %s", truncateStr(content, 140)))
				continue
			}
			if role == "jarvis" {
				snippets = append(snippets, fmt.Sprintf("- [主持人] %s", truncateStr(content, 140)))
			}
		}
		for i := len(snippets) - 1; i >= 0; i-- {
			b.WriteString(snippets[i] + "\n")
		}
	}

	b.WriteString("\n输出格式（必须严格遵守）：\n")
	b.WriteString("A) 给用户的澄清回复：\n")
	b.WriteString("- 若 need_more=true：只提 1 个问题；\n")
	b.WriteString("- 若 need_more=false：明确告知“澄清完成”，并给出最终详细议题。\n\n")
	b.WriteString("B) 决策 JSON：\n```json\n")
	b.WriteString("{\n")
	b.WriteString("  \"type\": \"clarify_decision\",\n")
	b.WriteString("  \"need_more\": true,\n")
	b.WriteString("  \"question\": \"\",\n")
	b.WriteString("  \"refined_topic\": \"\",\n")
	b.WriteString("  \"missing\": [],\n")
	b.WriteString("  \"summary\": \"\"\n")
	b.WriteString("}\n")
	b.WriteString("```\n")
	return b.String()
}

func (e *Engine) askDebateRole(ctx context.Context, room *DebateRoom, role DebateRole, prompt string) (string, int64, error) {
	socketPath := e.resolveRoleSocketPath(role)
	if strings.TrimSpace(socketPath) == "" {
		return "", 0, fmt.Errorf("socket path is empty")
	}
	timeoutSec := consensusAskTimeoutSecDefault
	retry := consensusAskRetryDefault
	if strings.EqualFold(role.Role, "jarvis") {
		timeoutSec = consensusAskTimeoutSecHost
		retry = consensusAskRetryHost
	}

	req := AskRequest{
		Project:    emptyAs(role.Project, role.Instance),
		SessionKey: buildRoleSessionKey(room.OwnerSessionKey, room.GroupChatID, room.RoomID, role.Role),
		Speak:      false,
	}

	flatPrompt := flattenPromptForTransport(prompt)
	lastErr := error(nil)
	for attempt := 0; attempt <= retry; attempt++ {
		req.Prompt = flatPrompt
		req.TimeoutSec = timeoutSec
		req.SpeakPrefix = ""

		roleCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec+consensusAskContextExtraSec)*time.Second)
		res, err := e.instanceCli.Ask(roleCtx, socketPath, req)
		cancel()
		if err == nil {
			return strings.TrimSpace(res.Content), res.LatencyMS, nil
		}
		lastErr = err
		if !isAskTimeoutErr(err) || attempt >= retry {
			break
		}
		slog.Warn("debate: ask timeout, retrying",
			"room_id", room.RoomID,
			"role", role.Role,
			"attempt", attempt+1,
			"timeout_sec", timeoutSec,
			"error", err)

		// retry with condensed rescue prompt to reduce token and latency pressure.
		flatPrompt = flattenPromptForTransport(buildTimeoutRescuePrompt(prompt))
		timeoutSec += consensusAskRetryExtraTimeoutSec
	}
	return "", 0, lastErr
}

const (
	consensusAskTimeoutSecDefault    = 180
	consensusAskTimeoutSecHost       = 240
	consensusAskRetryDefault         = 1
	consensusAskRetryHost            = 1
	consensusAskRetryExtraTimeoutSec = 60
	consensusAskContextExtraSec      = 20
)

func isAskTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "context deadline exceeded")
}

func buildTimeoutRescuePrompt(original string) string {
	base := strings.TrimSpace(original)
	if base == "" {
		base = "请直接给出核心结论与行动项。"
	}
	var b strings.Builder
	b.WriteString("你上一轮超时。请基于同一主题立即输出“可执行最小答案”，不要铺垫。\n")
	b.WriteString("要求：\n")
	b.WriteString("1) 直接给核心观点（2-4条）；\n")
	b.WriteString("2) 给关键疑问（最多3条）；\n")
	b.WriteString("3) 给下一步动作（owner+deadline+验收标准）。\n")
	b.WriteString("4) 禁止菜单化语句。\n\n")
	b.WriteString("原始任务：\n")
	b.WriteString(truncateStr(base, 1200))
	return b.String()
}

func buildConsensusHostSeedFallback(topic string, board *DebateBlackboard) string {
	questions := []string{
		"目标产物是否明确（方案/计划/代码）？",
		"范围与非目标是否已锁定？",
		"验收标准是否可量化？",
	}
	if board != nil && len(board.OpenQuestions) > 0 {
		questions = board.OpenQuestions
	}
	var b strings.Builder
	b.WriteString("【观点】\n")
	b.WriteString(fmt.Sprintf("- 本轮先围绕主题“%s”建立可执行讨论框架：先对齐目标、范围、验收，再进入全员发散与收敛。\n", truncateStr(topic, 120)))
	b.WriteString("- 讨论产出必须可落地：所有结论最终沉淀为行动项（owner + deadline + 验收标准）。\n")
	b.WriteString("【依据】\n")
	b.WriteString("- 先统一边界可显著减少后续分歧与返工。\n")
	b.WriteString("- 先发散再收敛能提高方案覆盖面并保证最终一致性。\n")
	b.WriteString("【风险/反例】\n")
	b.WriteString("- 若边界未锁定，后续各角色会基于不同前提给出冲突建议。\n")
	b.WriteString("- 若行动项缺 owner 与验收标准，讨论结论难执行。\n")
	b.WriteString("【建议动作】\n")
	b.WriteString("- 进入全员发散阶段：每位角色基于该主题提出观点、异议与疑问。\n")
	b.WriteString("- 主持人下一步汇总已一致项与未一致项，并发起收敛轮。\n")
	if len(questions) > 0 {
		b.WriteString("- 待优先确认问题：")
		for i := 0; i < minInt(3, len(questions)); i++ {
			if i > 0 {
				b.WriteString("；")
			}
			b.WriteString(truncateStr(strings.TrimSpace(questions[i]), 50))
		}
		b.WriteString("。\n")
	}
	return strings.TrimSpace(b.String())
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

	_, roles := splitConsensusRolesForRoom(room, true)
	if len(roles) == 0 {
		_, roles = splitConsensusRolesForRoom(room, false)
	}
	for _, role := range roles {
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
	} else if strings.TrimSpace(room.TopicDraft) != "" {
		topic = room.TopicDraft
	}
	b.WriteString(fmt.Sprintf("讨论主题：%s\n", topic))
	if board != nil {
		if strings.TrimSpace(board.TopicDraft) != "" {
			b.WriteString(fmt.Sprintf("主持人增强议题草案：%s\n", board.TopicDraft))
		}
		if strings.TrimSpace(board.RefinedTopic) != "" {
			b.WriteString(fmt.Sprintf("黑板最终议题：%s\n", board.RefinedTopic))
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
	b.WriteString(fmt.Sprintf("- 主题已收敛：%s\n", currentConsensusTopic(room)))
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

func renderDebateFinalReport(room *DebateRoom, board *DebateBlackboard, transcript []DebateTranscriptEntry, summary string) string {
	var b strings.Builder
	now := time.Now().Format("2006-01-02 15:04:05")
	b.WriteString("# 多Bot讨论成果汇总\n\n")
	b.WriteString(fmt.Sprintf("- 生成时间：%s\n", now))
	if room != nil {
		b.WriteString(fmt.Sprintf("- 房间ID：`%s`\n", room.RoomID))
		b.WriteString(fmt.Sprintf("- 模式：`%s`\n", emptyAs(room.Mode, DebateModeClassic)))
		b.WriteString(fmt.Sprintf("- 主持人：`%s`\n", emptyAs(room.HostRole, "jarvis")))
		b.WriteString(fmt.Sprintf("- 参与者（已确认）：%s\n", strings.Join(nonEmptyOrDash(room.ConfirmedParticipants), "、")))
		b.WriteString(fmt.Sprintf("- 会话状态：`%s`\n", room.Status))
	}
	rawTopic := ""
	if room != nil {
		rawTopic = strings.TrimSpace(room.Question)
	}
	draftTopic := ""
	if board != nil && strings.TrimSpace(board.TopicDraft) != "" {
		draftTopic = strings.TrimSpace(board.TopicDraft)
	} else if room != nil {
		draftTopic = strings.TrimSpace(room.TopicDraft)
	}
	finalTopic := ""
	if board != nil && strings.TrimSpace(board.RefinedTopic) != "" {
		finalTopic = strings.TrimSpace(board.RefinedTopic)
	} else if room != nil {
		finalTopic = strings.TrimSpace(room.RefinedQuestion)
	}
	if strings.TrimSpace(finalTopic) == "" && room != nil {
		finalTopic = currentConsensusTopic(room)
	}
	b.WriteString(fmt.Sprintf("- 最终讨论主题：%s\n\n", emptyAs(finalTopic, "-")))

	b.WriteString("## 议题演进\n\n")
	b.WriteString(fmt.Sprintf("- 用户原始议题：%s\n", emptyAs(rawTopic, "-")))
	b.WriteString(fmt.Sprintf("- 主持人增强议题草案：%s\n", emptyAs(draftTopic, "-")))
	b.WriteString(fmt.Sprintf("- 用户最终议题：%s\n\n", emptyAs(finalTopic, "-")))

	if board != nil && strings.TrimSpace(board.HostFirstProposal) != "" {
		b.WriteString("## 主持人首轮方案\n\n")
		b.WriteString(board.HostFirstProposal + "\n\n")
		if len(board.HostFirstProposalRisks) > 0 {
			b.WriteString("### 首轮识别风险\n")
			for _, line := range board.HostFirstProposalRisks {
				if strings.TrimSpace(line) == "" {
					continue
				}
				b.WriteString(fmt.Sprintf("- %s\n", line))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## 用户评审\n\n")
	if room != nil {
		b.WriteString(fmt.Sprintf("- 评审结论：`%s`\n", emptyAs(room.UserReviewStatus, "pending")))
		if strings.TrimSpace(room.UserReviewFeedback) != "" {
			b.WriteString(fmt.Sprintf("- 评审反馈：%s\n", room.UserReviewFeedback))
		}
	}
	b.WriteString("\n")

	b.WriteString("## 最终共识总结\n\n")
	summary = strings.TrimSpace(summary)
	if summary == "" {
		summary = "（无模型总结，已使用兜底流程）"
	}
	b.WriteString(summary + "\n\n")

	if board != nil {
		if len(board.ConsensusPoints) > 0 {
			b.WriteString("## 已达成一致\n\n")
			for _, item := range board.ConsensusPoints {
				if strings.TrimSpace(item) == "" {
					continue
				}
				b.WriteString(fmt.Sprintf("- %s\n", item))
			}
			b.WriteString("\n")
		}
		if len(board.Unresolved) > 0 {
			b.WriteString("## 未达成一致（供后续跟进）\n\n")
			for _, item := range board.Unresolved {
				if strings.TrimSpace(item) == "" {
					continue
				}
				b.WriteString(fmt.Sprintf("- %s\n", item))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## 讨论轨迹（最近 12 条）\n\n")
	start := 0
	if len(transcript) > 12 {
		start = len(transcript) - 12
	}
	for i := start; i < len(transcript); i++ {
		item := transcript[i]
		b.WriteString(fmt.Sprintf("- [R%d][%s] %s\n", item.Round, emptyAs(item.Role, item.Speaker), truncateStr(item.Content, 160)))
	}
	return strings.TrimSpace(b.String()) + "\n"
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
