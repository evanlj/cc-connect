package core

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) cmdDebate(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese

	if e.debateStore == nil || !e.debateStore.Enabled() {
		if isZh {
			e.reply(p, msg.ReplyCtx, "❌ debate 功能未启用：缺少可用的数据目录。")
		} else {
			e.reply(p, msg.ReplyCtx, "❌ Debate is not enabled: data directory is unavailable.")
		}
		return
	}

	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, e.debateUsage(isZh))
		return
	}

	sub := strings.ToLower(strings.TrimSpace(args[0]))
	switch sub {
	case "start":
		e.cmdDebateStart(p, msg, args[1:])
	case "topic":
		e.cmdDebateTopic(p, msg, args[1:])
	case "answer":
		e.cmdDebateAnswer(p, msg, args[1:])
	case "decision":
		e.cmdDebateDecision(p, msg, args[1:])
	case "participants":
		e.cmdDebateParticipants(p, msg, args[1:])
	case "continue":
		e.cmdDebateContinue(p, msg, args[1:])
	case "status":
		e.cmdDebateStatus(p, msg, args[1:])
	case "board":
		e.cmdDebateBoard(p, msg, args[1:])
	case "stop":
		e.cmdDebateStop(p, msg, args[1:])
	case "list":
		e.cmdDebateList(p, msg)
	case "help", "-h", "--help":
		e.reply(p, msg.ReplyCtx, e.debateUsage(isZh))
	default:
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 不支持的子命令：`%s`\n\n%s", sub, e.debateUsage(true)))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Unsupported subcommand: `%s`\n\n%s", sub, e.debateUsage(false)))
		}
	}
}

func (e *Engine) cmdDebateStart(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese

	opts, err := parseDebateStartOptions(args)
	if err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 参数错误：%v\n\n%s", err, e.debateUsage(true)))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Invalid arguments: %v\n\n%s", err, e.debateUsage(false)))
		}
		return
	}
	if err := ValidateDebateStartOptions(opts); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 参数校验失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Validation failed: %v", err))
		}
		return
	}

	room := NewDebateRoom(msg.SessionKey, opts, time.Now())
	if err := e.debateStore.SaveRoom(room); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 创建讨论房间失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to create debate room: %v", err))
		}
		return
	}
	if _, err := e.debateStore.LoadOrInitBlackboard(room); err != nil {
		// Non-fatal: debate can still run without board persistence.
		// Keep this best-effort and continue.
	}

	// M1 skeleton: create room + initial transcript line.
	_ = e.debateStore.AppendTranscript(room.RoomID, DebateTranscriptEntry{
		Round:    0,
		Speaker:  "jarvis",
		Role:     "jarvis",
		PostedBy: "instance-a",
		Content:  "room_created",
	})

	if err := e.startDebateRunner(room.RoomID); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 房间已创建，但启动执行失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Room created but failed to start runner: %v", err))
		}
		return
	}

	if isZh {
		participants := "（未设置）"
		if len(room.RequestedParticipants) > 0 {
			participants = strings.Join(room.RequestedParticipants, "、")
		}
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(
			"✅ 已创建并启动讨论：`%s`\n- 模式：`%s`\n- 主持人：`%s`\n- 预设：`%s`\n- 轮次参数：`%d`（consensus 模式仅作软参考）\n- 发言策略：`%s`\n- 建议参与者：%s\n- 主题：%s\n\n可用命令：`/debate status %s`、`/debate board %s`、`/debate topic %s <最终议题>`、`/debate decision %s approve|reject [反馈]`、`/debate participants %s 1,2,3`、`/debate stop %s`",
			room.RoomID, room.Mode, emptyAs(room.HostRole, "jarvis"), room.Preset, room.MaxRounds, room.SpeakingPolicy, participants, room.Question,
			room.RoomID,
			room.RoomID, room.RoomID, room.RoomID, room.RoomID, room.RoomID,
		))
		return
	}

	participants := "(not set)"
	if len(room.RequestedParticipants) > 0 {
		participants = strings.Join(room.RequestedParticipants, ", ")
	}
	e.reply(p, msg.ReplyCtx, fmt.Sprintf(
		"✅ Debate created and started: `%s`\n- mode: `%s`\n- host_role: `%s`\n- preset: `%s`\n- rounds_param: `%d` (consensus uses unresolved issues for convergence)\n- speaking_policy: `%s`\n- suggested_participants: %s\n- topic: %s\n\nCommands: `/debate status %s`, `/debate board %s`, `/debate topic %s <final topic>`, `/debate decision %s approve|reject [feedback]`, `/debate participants %s 1,2,3`, `/debate stop %s`",
		room.RoomID, room.Mode, emptyAs(room.HostRole, "jarvis"), room.Preset, room.MaxRounds, room.SpeakingPolicy, participants, room.Question,
		room.RoomID,
		room.RoomID, room.RoomID, room.RoomID, room.RoomID, room.RoomID,
	))
}

func (e *Engine) cmdDebateAnswer(p Platform, msg *Message, args []string) {
	// Backward-compatible alias: answer -> topic
	e.cmdDebateTopic(p, msg, args)
}

func (e *Engine) cmdDebateTopic(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese
	if len(args) < 2 {
		if isZh {
			e.reply(p, msg.ReplyCtx, "用法：`/debate topic <room_id> <最终议题>`")
		} else {
			e.reply(p, msg.ReplyCtx, "Usage: `/debate topic <room_id> <final topic>`")
		}
		return
	}

	roomID := strings.TrimSpace(args[0])
	finalTopic := normalizeDebateQuestion(strings.TrimSpace(strings.Join(args[1:], " ")))
	if roomID == "" || finalTopic == "" {
		if isZh {
			e.reply(p, msg.ReplyCtx, "❌ 参数不能为空：room_id 与最终议题都需要。")
		} else {
			e.reply(p, msg.ReplyCtx, "❌ Invalid args: room_id and final topic are required.")
		}
		return
	}

	room, err := e.debateStore.GetRoom(roomID)
	if err != nil {
		if os.IsNotExist(err) {
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 未找到房间：`%s`", roomID))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Room not found: `%s`", roomID))
			}
			return
		}
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 读取房间失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to read room: %v", err))
		}
		return
	}
	if !strings.EqualFold(room.Mode, DebateModeConsensus) {
		if isZh {
			e.reply(p, msg.ReplyCtx, "❌ 当前房间不是 consensus 模式，无需 topic。")
		} else {
			e.reply(p, msg.ReplyCtx, "❌ This room is not in consensus mode; topic command is not required.")
		}
		return
	}

	phase := strings.TrimSpace(room.Phase)
	if phase != consensusPhaseTopicConfirm && room.StopReason != "await_user_final_topic" && phase != consensusPhaseTopicRefine && phase != consensusPhaseInit && phase != "" {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 当前阶段为 `%s`，不在“最终议题确认”流程中。请按阶段使用：评审用 `/debate decision`，选人用 `/debate participants`。", phase))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Current phase is `%s`, not final-topic confirmation. Use `/debate decision` for review and `/debate participants` for participants.", phase))
		}
		return
	}
	room.RefinedQuestion = finalTopic
	room.Status = DebateStatusRunning
	room.Phase = consensusPhaseHostFirstProposal
	room.StopReason = ""
	if strings.TrimSpace(room.UserReviewStatus) == "" {
		room.UserReviewStatus = "pending"
	}
	if err := e.debateStore.SaveRoom(room); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 更新房间失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to update room: %v", err))
		}
		return
	}

	_ = e.debateStore.AppendTranscript(room.RoomID, DebateTranscriptEntry{
		Round:    0,
		Speaker:  "user",
		Role:     "user",
		PostedBy: "user",
		Content:  "final_topic: " + finalTopic,
	})

	if err := e.startDebateRunner(room.RoomID); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 已记录最终议题，但恢复讨论失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Final topic recorded, but failed to resume debate: %v", err))
		}
		return
	}

	if isZh {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ 已记录最终议题并进入主持人首轮回答：`%s`", room.RoomID))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ Final topic saved and host first response started: `%s`", room.RoomID))
	}
}

func (e *Engine) cmdDebateDecision(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese
	if len(args) < 2 {
		if isZh {
			e.reply(p, msg.ReplyCtx, "用法：`/debate decision <room_id> approve|reject [反馈]`")
		} else {
			e.reply(p, msg.ReplyCtx, "Usage: `/debate decision <room_id> approve|reject [feedback]`")
		}
		return
	}

	roomID := strings.TrimSpace(args[0])
	decision := strings.ToLower(strings.TrimSpace(args[1]))
	feedback := ""
	if len(args) > 2 {
		feedback = strings.TrimSpace(strings.Join(args[2:], " "))
	}

	if decision != "approve" && decision != "reject" {
		if isZh {
			e.reply(p, msg.ReplyCtx, "❌ decision 仅支持 approve 或 reject。")
		} else {
			e.reply(p, msg.ReplyCtx, "❌ decision must be approve or reject.")
		}
		return
	}

	room, err := e.debateStore.GetRoom(roomID)
	if err != nil {
		if os.IsNotExist(err) {
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 未找到房间：`%s`", roomID))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Room not found: `%s`", roomID))
			}
			return
		}
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 读取房间失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to read room: %v", err))
		}
		return
	}
	if !strings.EqualFold(room.Mode, DebateModeConsensus) {
		if isZh {
			e.reply(p, msg.ReplyCtx, "❌ 当前房间不是 consensus 模式，无需 decision。")
		} else {
			e.reply(p, msg.ReplyCtx, "❌ This room is not in consensus mode; decision is not required.")
		}
		return
	}
	phase := strings.TrimSpace(room.Phase)
	if phase != consensusPhaseUserReview && room.StopReason != "await_user_review" {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 当前阶段为 `%s`，还未进入“第一轮回答评审”阶段。", phase))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Current phase is `%s`, not in first-proposal review stage.", phase))
		}
		return
	}

	if decision == "approve" {
		room.UserReviewStatus = "approved"
		room.UserReviewFeedback = feedback
		room.Status = DebateStatusRunning
		room.StopReason = ""
		room.Phase = consensusPhaseFinalizeSingle
	} else {
		room.UserReviewStatus = "rejected"
		room.UserReviewFeedback = feedback
		room.Status = DebateStatusRunning
		room.StopReason = ""
		room.RequestedParticipants = nil
		room.ConfirmedParticipants = nil
		room.Phase = consensusPhaseParticipantConfirm
	}

	if err := e.debateStore.SaveRoom(room); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 更新房间失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to update room: %v", err))
		}
		return
	}

	_ = e.debateStore.AppendTranscript(room.RoomID, DebateTranscriptEntry{
		Round:    0,
		Speaker:  "user",
		Role:     "user",
		PostedBy: "user",
		Content:  fmt.Sprintf("proposal_review:%s %s", decision, feedback),
	})

	if err := e.startDebateRunner(room.RoomID); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 已记录 decision，但恢复讨论失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Decision saved, but failed to resume debate: %v", err))
		}
		return
	}

	if isZh {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ 已记录 decision=%s 并恢复讨论：`%s`（下一阶段：%s）", decision, room.RoomID, room.Phase))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ Decision=%s saved and debate resumed: `%s` (next phase: %s)", decision, room.RoomID, room.Phase))
	}
}

func (e *Engine) cmdDebateParticipants(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese
	if len(args) < 2 {
		if isZh {
			e.reply(p, msg.ReplyCtx, "用法：`/debate participants <room_id> 1,2,3`")
		} else {
			e.reply(p, msg.ReplyCtx, "Usage: `/debate participants <room_id> 1,2,3`")
		}
		return
	}

	roomID := strings.TrimSpace(args[0])
	listRaw := strings.TrimSpace(strings.Join(args[1:], " "))
	if roomID == "" || listRaw == "" {
		if isZh {
			e.reply(p, msg.ReplyCtx, "❌ 参数不能为空：room_id 与参与者列表都需要。")
		} else {
			e.reply(p, msg.ReplyCtx, "❌ Invalid args: room_id and participants are required.")
		}
		return
	}

	room, err := e.debateStore.GetRoom(roomID)
	if err != nil {
		if os.IsNotExist(err) {
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 未找到房间：`%s`", roomID))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Room not found: `%s`", roomID))
			}
			return
		}
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 读取房间失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to read room: %v", err))
		}
		return
	}
	if !strings.EqualFold(room.Mode, DebateModeConsensus) {
		if isZh {
			e.reply(p, msg.ReplyCtx, "❌ 当前房间不是 consensus 模式，无需 participants。")
		} else {
			e.reply(p, msg.ReplyCtx, "❌ This room is not in consensus mode; participants command is not required.")
		}
		return
	}
	phase := strings.TrimSpace(room.Phase)
	if phase != consensusPhaseParticipantConfirm && room.StopReason != "await_participants_confirm" {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 当前阶段为 `%s`，暂不需要选择参与角色。", phase))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Current phase is `%s`, participant selection is not required.", phase))
		}
		return
	}

	requested, confirmed, parseErr := parseParticipantSelection(room, listRaw)
	if parseErr != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ %v", parseErr))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ %v", parseErr))
		}
		return
	}
	if len(confirmed) == 0 {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 未识别到有效参与者，请使用编号（可选值：%s）。", strings.Join(listConsensusRoleHints(room), "、")))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ No valid participants found. Use index numbers (available: %s).", strings.Join(listConsensusRoleHints(room), ", ")))
		}
		return
	}

	room.RequestedParticipants = cloneStringSlice(confirmed)
	room.ConfirmedParticipants = cloneStringSlice(confirmed)
	room.Status = DebateStatusRunning
	room.StopReason = ""
	room.Phase = consensusPhaseAllDiverge
	if strings.TrimSpace(room.UserReviewStatus) == "" {
		room.UserReviewStatus = "rejected"
	}
	if err := e.debateStore.SaveRoom(room); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 更新房间失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to update room: %v", err))
		}
		return
	}

	_ = e.debateStore.AppendTranscript(room.RoomID, DebateTranscriptEntry{
		Round:    0,
		Speaker:  "user",
		Role:     "user",
		PostedBy: "user",
		Content:  "participants_confirmed: " + strings.Join(confirmed, ","),
	})

	if err := e.startDebateRunner(room.RoomID); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 已记录参与者，但恢复讨论失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Participants saved, but failed to resume debate: %v", err))
		}
		return
	}

	if isZh {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ 已确认参与角色编号：%s（角色：%s），并恢复讨论：`%s`", strings.Join(requested, ","), strings.Join(confirmed, "、"), room.RoomID))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ Participant indexes confirmed: %s (roles: %s), debate resumed: `%s`", strings.Join(requested, ","), strings.Join(confirmed, ", "), room.RoomID))
	}
}

func (e *Engine) cmdDebateContinue(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese
	if len(args) == 0 {
		if isZh {
			e.reply(p, msg.ReplyCtx, "用法：`/debate continue <room_id>`")
		} else {
			e.reply(p, msg.ReplyCtx, "Usage: `/debate continue <room_id>`")
		}
		return
	}
	roomID := strings.TrimSpace(args[0])
	room, err := e.debateStore.GetRoom(roomID)
	if err != nil {
		if os.IsNotExist(err) {
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 未找到房间：`%s`", roomID))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Room not found: `%s`", roomID))
			}
			return
		}
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 读取房间失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to read room: %v", err))
		}
		return
	}
	if e.isDebateRunnerActive(room.RoomID) {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("ℹ️ 房间 `%s` 已在运行。", room.RoomID))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("ℹ️ Room `%s` is already running.", room.RoomID))
		}
		return
	}
	room.Status = DebateStatusRunning
	room.StopReason = ""
	if err := e.debateStore.SaveRoom(room); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 更新房间失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to update room: %v", err))
		}
		return
	}
	if err := e.startDebateRunner(room.RoomID); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 恢复讨论失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to resume debate: %v", err))
		}
		return
	}
	if isZh {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ 已恢复讨论：`%s`", room.RoomID))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ Debate resumed: `%s`", room.RoomID))
	}
}

func (e *Engine) cmdDebateStatus(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese

	var (
		room *DebateRoom
		err  error
	)
	if len(args) > 0 {
		room, err = e.debateStore.GetRoom(args[0])
		if err != nil {
			if os.IsNotExist(err) {
				if isZh {
					e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 未找到房间：`%s`", args[0]))
				} else {
					e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Room not found: `%s`", args[0]))
				}
				return
			}
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 查询房间失败：%v", err))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to query room: %v", err))
			}
			return
		}
	} else {
		room, err = e.latestRoomByOwner(msg.SessionKey)
		if err != nil {
			if isZh {
				e.reply(p, msg.ReplyCtx, "暂无讨论房间。先用 `/debate start ...` 创建。")
			} else {
				e.reply(p, msg.ReplyCtx, "No debate room found. Create one with `/debate start ...` first.")
			}
			return
		}
	}

	body := formatDebateRoomStatus(room, isZh)
	if e.isDebateRunnerActive(room.RoomID) {
		if isZh {
			body += "\n- runner: `active`"
		} else {
			body += "\n- runner: `active`"
		}
	} else {
		if isZh {
			body += "\n- runner: `inactive`"
		} else {
			body += "\n- runner: `inactive`"
		}
	}
	e.reply(p, msg.ReplyCtx, body)
}

func (e *Engine) cmdDebateStop(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese

	if len(args) == 0 {
		if isZh {
			e.reply(p, msg.ReplyCtx, "用法：`/debate stop <room_id>`")
		} else {
			e.reply(p, msg.ReplyCtx, "Usage: `/debate stop <room_id>`")
		}
		return
	}

	room, err := e.debateStore.GetRoom(args[0])
	if err != nil {
		if os.IsNotExist(err) {
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 未找到房间：`%s`", args[0]))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Room not found: `%s`", args[0]))
			}
			return
		}
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 读取房间失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to read room: %v", err))
		}
		return
	}

	room.Status = DebateStatusStopped
	room.StopReason = "manual_stop"
	room.UpdatedAt = time.Now()
	if err := e.debateStore.SaveRoom(room); err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 停止房间失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to stop room: %v", err))
		}
		return
	}

	_ = e.debateStore.AppendTranscript(room.RoomID, DebateTranscriptEntry{
		Round:    room.CurrentRound,
		Speaker:  "jarvis",
		Role:     "jarvis",
		PostedBy: "instance-a",
		Content:  "room_stopped:manual_stop",
	})
	_ = e.stopDebateRunner(room.RoomID)

	if isZh {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ 讨论房间已停止：`%s`", room.RoomID))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ Debate room stopped: `%s`", room.RoomID))
	}
}

func (e *Engine) cmdDebateBoard(p Platform, msg *Message, args []string) {
	isZh := e.i18n.CurrentLang() == LangChinese

	var (
		room *DebateRoom
		err  error
	)
	if len(args) > 0 {
		room, err = e.debateStore.GetRoom(args[0])
		if err != nil {
			if os.IsNotExist(err) {
				if isZh {
					e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 未找到房间：`%s`", args[0]))
				} else {
					e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Room not found: `%s`", args[0]))
				}
				return
			}
			if isZh {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 读取房间失败：%v", err))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to read room: %v", err))
			}
			return
		}
	} else {
		room, err = e.latestRoomByOwner(msg.SessionKey)
		if err != nil {
			if isZh {
				e.reply(p, msg.ReplyCtx, "暂无讨论房间。先用 `/debate start ...` 创建。")
			} else {
				e.reply(p, msg.ReplyCtx, "No debate room found. Create one with `/debate start ...` first.")
			}
			return
		}
	}

	board, err := e.debateStore.LoadOrInitBlackboard(room)
	if err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 读取黑板失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to load blackboard: %v", err))
		}
		return
	}
	body := formatDebateBlackboard(board, room, isZh)
	if path := strings.TrimSpace(e.debateStore.BlackboardFilePath(room.RoomID)); path != "" {
		if isZh {
			body += fmt.Sprintf("\n\n- 黑板文件: `%s`", path)
		} else {
			body += fmt.Sprintf("\n\n- blackboard_file: `%s`", path)
		}
	}
	e.reply(p, msg.ReplyCtx, body)
}

func (e *Engine) cmdDebateList(p Platform, msg *Message) {
	isZh := e.i18n.CurrentLang() == LangChinese
	rooms, err := e.debateStore.ListRooms()
	if err != nil {
		if isZh {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 获取房间列表失败：%v", err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ Failed to list rooms: %v", err))
		}
		return
	}
	if len(rooms) == 0 {
		if isZh {
			e.reply(p, msg.ReplyCtx, "暂无讨论房间。")
		} else {
			e.reply(p, msg.ReplyCtx, "No debate rooms yet.")
		}
		return
	}

	maxRows := 10
	if len(rooms) < maxRows {
		maxRows = len(rooms)
	}

	var b strings.Builder
	if isZh {
		b.WriteString(fmt.Sprintf("🧭 最近讨论房间（%d/%d）\n\n", maxRows, len(rooms)))
	} else {
		b.WriteString(fmt.Sprintf("🧭 Recent debate rooms (%d/%d)\n\n", maxRows, len(rooms)))
	}

	for i := 0; i < maxRows; i++ {
		r := rooms[i]
		b.WriteString(fmt.Sprintf("%d) `%s` [%s]\n", i+1, r.RoomID, r.Status))
		b.WriteString(fmt.Sprintf("   - preset: `%s`, rounds: `%d`, policy: `%s`\n", r.Preset, r.MaxRounds, r.SpeakingPolicy))
		b.WriteString(fmt.Sprintf("   - topic: %s\n", truncateStr(r.Question, 60)))
	}
	e.reply(p, msg.ReplyCtx, strings.TrimSpace(b.String()))
}

func (e *Engine) latestRoomByOwner(ownerSessionKey string) (*DebateRoom, error) {
	rooms, err := e.debateStore.ListRooms()
	if err != nil {
		return nil, err
	}
	for _, room := range rooms {
		if room.OwnerSessionKey == ownerSessionKey {
			return room, nil
		}
	}
	return nil, fmt.Errorf("room not found")
}

func parseDebateStartOptions(args []string) (DebateStartOptions, error) {
	opts := DebateStartOptions{
		Preset:         DefaultDebatePreset,
		MaxRounds:      DefaultDebateMaxRounds,
		SpeakingPolicy: DefaultSpeakingPolicy,
		Mode:           DefaultDebateMode,
	}

	var questionParts []string
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}

		if !strings.HasPrefix(arg, "--") {
			questionParts = append(questionParts, arg)
			continue
		}

		key := arg
		val := ""
		if idx := strings.Index(arg, "="); idx >= 0 {
			key = strings.TrimSpace(arg[:idx])
			val = strings.TrimSpace(arg[idx+1:])
		} else {
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing value for %s", key)
			}
			i++
			val = strings.TrimSpace(args[i])
		}

		switch strings.ToLower(key) {
		case "--preset":
			opts.Preset = val
		case "--rounds":
			n, err := strconv.Atoi(val)
			if err != nil {
				return opts, fmt.Errorf("--rounds must be integer")
			}
			opts.MaxRounds = n
		case "--speaking-policy":
			opts.SpeakingPolicy = val
		case "--mode":
			opts.Mode = val
		case "--host":
			opts.HostRole = val
		case "--participants":
			opts.Participants = parseRoleCSV(val)
		default:
			return opts, fmt.Errorf("unknown option %s", key)
		}
	}

	opts.Question = strings.TrimSpace(strings.Join(questionParts, " "))
	opts = NormalizeDebateStartOptions(opts)
	return opts, nil
}

func formatDebateRoomStatus(room *DebateRoom, isZh bool) string {
	if room == nil {
		if isZh {
			return "❌ 房间为空"
		}
		return "❌ room is nil"
	}

	if isZh {
		confirmed := strings.Join(nonEmptyOrDash(room.ConfirmedParticipants), "、")
		requested := strings.Join(nonEmptyOrDash(room.RequestedParticipants), "、")
		return fmt.Sprintf(
			"🧭 讨论房间状态\n\n- room_id: `%s`\n- status: `%s`\n- mode: `%s`\n- phase: `%s`\n- iteration: `%d`\n- host_role: `%s`\n- user_review: `%s`\n- requested_participants: %s\n- confirmed_participants: %s\n- topic_draft: %s\n- final_topic: %s\n- preset: `%s`\n- rounds_param: `%d`\n- current_round: `%d`\n- speaking_policy: `%s`\n- owner_session: `%s`\n- created_at: `%s`\n- updated_at: `%s`\n- topic: %s",
			room.RoomID,
			room.Status,
			emptyAs(room.Mode, DebateModeClassic),
			emptyAs(room.Phase, "-"),
			room.Iteration,
			emptyAs(room.HostRole, "jarvis"),
			emptyAs(room.UserReviewStatus, "pending"),
			requested,
			confirmed,
			emptyAs(room.TopicDraft, "-"),
			emptyAs(room.RefinedQuestion, "-"),
			room.Preset,
			room.MaxRounds,
			room.CurrentRound,
			room.SpeakingPolicy,
			room.OwnerSessionKey,
			room.CreatedAt.Format(time.RFC3339),
			room.UpdatedAt.Format(time.RFC3339),
			room.Question,
		)
	}

	return fmt.Sprintf(
		"🧭 Debate room status\n\n- room_id: `%s`\n- status: `%s`\n- mode: `%s`\n- phase: `%s`\n- iteration: `%d`\n- host_role: `%s`\n- user_review: `%s`\n- requested_participants: %s\n- confirmed_participants: %s\n- topic_draft: %s\n- final_topic: %s\n- preset: `%s`\n- rounds_param: `%d`\n- current_round: `%d`\n- speaking_policy: `%s`\n- owner_session: `%s`\n- created_at: `%s`\n- updated_at: `%s`\n- topic: %s",
		room.RoomID,
		room.Status,
		emptyAs(room.Mode, DebateModeClassic),
		emptyAs(room.Phase, "-"),
		room.Iteration,
		emptyAs(room.HostRole, "jarvis"),
		emptyAs(room.UserReviewStatus, "pending"),
		strings.Join(nonEmptyOrDash(room.RequestedParticipants), ", "),
		strings.Join(nonEmptyOrDash(room.ConfirmedParticipants), ", "),
		emptyAs(room.TopicDraft, "-"),
		emptyAs(room.RefinedQuestion, "-"),
		room.Preset,
		room.MaxRounds,
		room.CurrentRound,
		room.SpeakingPolicy,
		room.OwnerSessionKey,
		room.CreatedAt.Format(time.RFC3339),
		room.UpdatedAt.Format(time.RFC3339),
		room.Question,
	)
}

func (e *Engine) debateUsage(isZh bool) string {
	if isZh {
		return "用法：\n" +
			"- `/debate start --mode classic --preset tianji-five --rounds 3 --speaking-policy host-decide <问题>`（host-decide 终轮自动全员发言）\n" +
			"- `/debate start --mode consensus --host jarvis --participants jianzhu,wendan --rounds 4 <问题>`\n" +
			"- `/debate topic <room_id> <最终议题>`（主持人给出增强议题草案后，用户提交最终议题）\n" +
			"- `/debate answer <room_id> <最终议题>`（兼容旧命令，等价于 `/debate topic`）\n" +
			"- `/debate decision <room_id> approve|reject [反馈]`（主持人首轮方案用户评审）\n" +
			"- `/debate participants <room_id> 1,2,3`（按编号确认多人讨论参与者）\n" +
			"- `/debate continue <room_id>`\n" +
			"- `/debate status [room_id]`\n" +
			"- `/debate board [room_id]`\n" +
			"- `/debate stop <room_id>`\n" +
			"- `/debate list`"
	}
	return "Usage:\n" +
		"- `/debate start --mode classic --preset tianji-five --rounds 3 --speaking-policy host-decide <question>` (host-decide auto includes all workers in final round)\n" +
		"- `/debate start --mode consensus --host jarvis --participants jianzhu,wendan --rounds 4 <question>`\n" +
		"- `/debate topic <room_id> <final topic>` (submit final topic after host draft)\n" +
		"- `/debate answer <room_id> <final topic>` (legacy alias for `/debate topic`)\n" +
		"- `/debate decision <room_id> approve|reject [feedback]` (user review for host first proposal)\n" +
		"- `/debate participants <room_id> 1,2,3` (confirm participants by index before multi-bot phase)\n" +
		"- `/debate continue <room_id>`\n" +
		"- `/debate status [room_id]`\n" +
		"- `/debate board [room_id]`\n" +
		"- `/debate stop <room_id>`\n" +
		"- `/debate list`"
}

func buildRefinedQuestion(baseQuestion, answer string) string {
	baseQuestion = strings.TrimSpace(baseQuestion)
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return baseQuestion
	}
	if baseQuestion == "" {
		return answer
	}
	return fmt.Sprintf("%s\n\n用户补充：%s", baseQuestion, answer)
}

func parseRoleCSV(raw string) []string {
	raw = strings.ReplaceAll(raw, "，", ",")
	raw = strings.ReplaceAll(raw, "；", ",")
	raw = strings.ReplaceAll(raw, ";", ",")
	raw = strings.ReplaceAll(raw, "、", ",")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func parseParticipantSelection(room *DebateRoom, listRaw string) ([]string, []string, error) {
	if room == nil {
		return nil, nil, fmt.Errorf("room is nil")
	}
	tokens := parseRoleCSV(listRaw)
	if len(tokens) == 0 {
		return nil, nil, fmt.Errorf("参与者选择为空")
	}
	catalog := consensusWorkerRoleCatalog(room)
	if len(catalog) == 0 {
		return nil, nil, fmt.Errorf("当前没有可选参与角色")
	}

	indexToRole := make(map[int]DebateRole, len(catalog))
	for i, role := range catalog {
		indexToRole[i+1] = role
	}

	requested := make([]string, 0, len(tokens))
	confirmed := make([]string, 0, len(tokens))
	seen := map[string]bool{}
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		requested = append(requested, token)
		if idx, err := strconv.Atoi(token); err == nil {
			role, ok := indexToRole[idx]
			if !ok {
				return nil, nil, fmt.Errorf("编号 `%d` 无效，可选编号：%s", idx, strings.Join(listConsensusRoleHints(room), "、"))
			}
			if !seen[role.Role] {
				seen[role.Role] = true
				confirmed = append(confirmed, role.Role)
			}
			continue
		}
		roleKey := resolveDebateRoleKey(room.Roles, token)
		if roleKey == "" {
			return nil, nil, fmt.Errorf("无法识别角色 `%s`，请使用编号：%s", token, strings.Join(listConsensusRoleHints(room), "、"))
		}
		if !seen[roleKey] {
			seen[roleKey] = true
			confirmed = append(confirmed, roleKey)
		}
	}
	return requested, confirmed, nil
}

func listConsensusRoleHints(room *DebateRoom) []string {
	if room == nil {
		return nil
	}
	catalog := consensusWorkerRoleCatalog(room)
	out := make([]string, 0, len(catalog))
	for i := range catalog {
		out = append(out, strconv.Itoa(i+1))
	}
	return out
}

func formatParticipantCandidateLabels(room *DebateRoom, candidates []string, isZh bool) string {
	if len(candidates) == 0 {
		if isZh {
			return "（无）"
		}
		return "(none)"
	}
	roleByKey := map[string]DebateRole{}
	if room != nil {
		for _, role := range room.Roles {
			roleByKey[role.Role] = role
		}
	}
	parts := make([]string, 0, len(candidates))
	for i, key := range candidates {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		role, ok := roleByKey[key]
		if !ok {
			role = DebateRole{Role: key, DisplayName: key}
		}
		name := emptyAs(strings.TrimSpace(role.DisplayName), key)
		identity := debateRoleIdentity(role, isZh)
		if isZh {
			parts = append(parts, fmt.Sprintf("%d)%s(%s,%s)", i+1, name, key, identity))
		} else {
			parts = append(parts, fmt.Sprintf("%d)%s(%s,%s)", i+1, name, key, identity))
		}
	}
	if len(parts) == 0 {
		if isZh {
			return "（无）"
		}
		return "(none)"
	}
	sep := "；"
	if !isZh {
		sep = "; "
	}
	return strings.Join(parts, sep)
}

func formatDebateBlackboard(board *DebateBlackboard, room *DebateRoom, isZh bool) string {
	if board == nil {
		if isZh {
			return "❌ 黑板为空"
		}
		return "❌ blackboard is nil"
	}

	var b strings.Builder
	if isZh {
		b.WriteString("🧠 讨论黑板\n\n")
		b.WriteString(fmt.Sprintf("- room_id: `%s`\n", board.RoomID))
		b.WriteString(fmt.Sprintf("- revision: `%d`\n", board.Revision))
		b.WriteString(fmt.Sprintf("- 主题: %s\n", emptyAs(board.Topic, room.Question)))
		if strings.TrimSpace(board.TopicDraft) != "" {
			b.WriteString(fmt.Sprintf("- 主持人增强议题草案: %s\n", truncateStr(board.TopicDraft, 140)))
		}
		if strings.TrimSpace(board.RefinedTopic) != "" {
			b.WriteString(fmt.Sprintf("- 用户最终议题: %s\n", board.RefinedTopic))
		}
		b.WriteString(fmt.Sprintf("- 主题锁定: `%t`\n", board.FinalTopicLocked))
		b.WriteString(fmt.Sprintf("- 用户评审: `%s`\n", emptyAs(board.UserReviewStatus, emptyAs(room.UserReviewStatus, "pending"))))
		if strings.TrimSpace(board.UserReviewFeedback) != "" {
			b.WriteString(fmt.Sprintf("- 用户评审反馈: %s\n", truncateStr(board.UserReviewFeedback, 120)))
		}
		if len(board.ParticipantCandidates) > 0 {
			b.WriteString(fmt.Sprintf("- 可选参与者: %s\n", formatParticipantCandidateLabels(room, board.ParticipantCandidates, true)))
		}
		if len(board.ParticipantConfirmed) > 0 {
			b.WriteString(fmt.Sprintf("- 已确认参与者: %s\n", formatParticipantCandidateLabels(room, board.ParticipantConfirmed, true)))
		}
		if strings.TrimSpace(board.HostFirstProposal) != "" {
			b.WriteString(fmt.Sprintf("- 主持人首轮方案: %s\n", truncateStr(board.HostFirstProposal, 120)))
		}
		if strings.TrimSpace(board.Goal) != "" {
			b.WriteString(fmt.Sprintf("- 目标: %s\n", board.Goal))
		}
		b.WriteString(fmt.Sprintf("- 轮次: `%d/%d`\n", board.Round, board.MaxRounds))
		if strings.TrimSpace(board.RoundPlan) != "" {
			b.WriteString(fmt.Sprintf("- 本轮计划: %s\n", board.RoundPlan))
		}
		if strings.TrimSpace(board.RoundFocus) != "" {
			b.WriteString(fmt.Sprintf("- 本轮焦点: %s\n", board.RoundFocus))
		}
		if len(board.OpenQuestions) > 0 {
			b.WriteString("- 待解问题:\n")
			for i := 0; i < minInt(3, len(board.OpenQuestions)); i++ {
				b.WriteString(fmt.Sprintf("  - %s\n", truncateStr(board.OpenQuestions[i], 110)))
			}
		}
		b.WriteString("\n- 角色最新观点:\n")
	} else {
		b.WriteString("🧠 Debate blackboard\n\n")
		b.WriteString(fmt.Sprintf("- room_id: `%s`\n", board.RoomID))
		b.WriteString(fmt.Sprintf("- revision: `%d`\n", board.Revision))
		b.WriteString(fmt.Sprintf("- topic: %s\n", emptyAs(board.Topic, room.Question)))
		if strings.TrimSpace(board.TopicDraft) != "" {
			b.WriteString(fmt.Sprintf("- host_topic_draft: %s\n", truncateStr(board.TopicDraft, 140)))
		}
		if strings.TrimSpace(board.RefinedTopic) != "" {
			b.WriteString(fmt.Sprintf("- final_topic: %s\n", board.RefinedTopic))
		}
		b.WriteString(fmt.Sprintf("- final_topic_locked: `%t`\n", board.FinalTopicLocked))
		b.WriteString(fmt.Sprintf("- user_review_status: `%s`\n", emptyAs(board.UserReviewStatus, emptyAs(room.UserReviewStatus, "pending"))))
		if strings.TrimSpace(board.UserReviewFeedback) != "" {
			b.WriteString(fmt.Sprintf("- user_review_feedback: %s\n", truncateStr(board.UserReviewFeedback, 120)))
		}
		if len(board.ParticipantCandidates) > 0 {
			b.WriteString(fmt.Sprintf("- participant_candidates: %s\n", formatParticipantCandidateLabels(room, board.ParticipantCandidates, false)))
		}
		if len(board.ParticipantConfirmed) > 0 {
			b.WriteString(fmt.Sprintf("- participant_confirmed: %s\n", formatParticipantCandidateLabels(room, board.ParticipantConfirmed, false)))
		}
		if strings.TrimSpace(board.HostFirstProposal) != "" {
			b.WriteString(fmt.Sprintf("- host_first_proposal: %s\n", truncateStr(board.HostFirstProposal, 120)))
		}
		if strings.TrimSpace(board.Goal) != "" {
			b.WriteString(fmt.Sprintf("- goal: %s\n", board.Goal))
		}
		b.WriteString(fmt.Sprintf("- round: `%d/%d`\n", board.Round, board.MaxRounds))
		if strings.TrimSpace(board.RoundPlan) != "" {
			b.WriteString(fmt.Sprintf("- round_plan: %s\n", board.RoundPlan))
		}
		if strings.TrimSpace(board.RoundFocus) != "" {
			b.WriteString(fmt.Sprintf("- round_focus: %s\n", board.RoundFocus))
		}
		if len(board.OpenQuestions) > 0 {
			b.WriteString("- open_questions:\n")
			for i := 0; i < minInt(3, len(board.OpenQuestions)); i++ {
				b.WriteString(fmt.Sprintf("  - %s\n", truncateStr(board.OpenQuestions[i], 110)))
			}
		}
		b.WriteString("\n- latest role notes:\n")
	}

	roleOrder := make([]DebateRole, 0, len(room.Roles))
	for _, r := range room.Roles {
		if strings.EqualFold(r.Role, "jarvis") {
			continue
		}
		roleOrder = append(roleOrder, r)
	}
	for _, r := range roleOrder {
		n, ok := board.RoleNotes[r.Role]
		if !ok {
			b.WriteString(fmt.Sprintf("  - %s: （暂无）\n", emptyAs(r.DisplayName, r.Role)))
			continue
		}
		b.WriteString(fmt.Sprintf("  - %s: %s\n", emptyAs(r.DisplayName, r.Role), truncateStr(emptyAs(n.LatestStance, n.LastMessage), 110)))
		if strings.TrimSpace(n.LatestAction) != "" {
			if isZh {
				b.WriteString(fmt.Sprintf("      action: %s\n", truncateStr(n.LatestAction, 110)))
			} else {
				b.WriteString(fmt.Sprintf("      action: %s\n", truncateStr(n.LatestAction, 110)))
			}
		}
	}

	if len(board.HistoryDigest) > 0 {
		if isZh {
			b.WriteString("\n- 最近沉淀:\n")
		} else {
			b.WriteString("\n- recent digest:\n")
		}
		start := 0
		if len(board.HistoryDigest) > 4 {
			start = len(board.HistoryDigest) - 4
		}
		for i := start; i < len(board.HistoryDigest); i++ {
			b.WriteString(fmt.Sprintf("  - %s\n", truncateStr(board.HistoryDigest[i], 120)))
		}
	}
	return strings.TrimSpace(b.String())
}
