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
	case "status":
		e.cmdDebateStatus(p, msg, args[1:])
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
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(
			"✅ 已创建并启动讨论：`%s`\n- 预设：`%s`\n- 轮次上限：`%d`\n- 发言策略：`%s`\n- 主题：%s\n\n可用命令：`/debate status %s`、`/debate stop %s`",
			room.RoomID, room.Preset, room.MaxRounds, room.SpeakingPolicy, room.Question,
			room.RoomID, room.RoomID,
		))
		return
	}

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(
		"✅ Debate created and started: `%s`\n- preset: `%s`\n- max_rounds: `%d`\n- speaking_policy: `%s`\n- topic: %s\n\nCommands: `/debate status %s`, `/debate stop %s`",
		room.RoomID, room.Preset, room.MaxRounds, room.SpeakingPolicy, room.Question,
		room.RoomID, room.RoomID,
	))
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
		return fmt.Sprintf(
			"🧭 讨论房间状态\n\n- room_id: `%s`\n- status: `%s`\n- preset: `%s`\n- rounds: `%d`\n- current_round: `%d`\n- speaking_policy: `%s`\n- owner_session: `%s`\n- created_at: `%s`\n- updated_at: `%s`\n- topic: %s",
			room.RoomID,
			room.Status,
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
		"🧭 Debate room status\n\n- room_id: `%s`\n- status: `%s`\n- preset: `%s`\n- rounds: `%d`\n- current_round: `%d`\n- speaking_policy: `%s`\n- owner_session: `%s`\n- created_at: `%s`\n- updated_at: `%s`\n- topic: %s",
		room.RoomID,
		room.Status,
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
			"- `/debate start --preset tianji-five --rounds 3 --speaking-policy host-decide <问题>`\n" +
			"- `/debate status [room_id]`\n" +
			"- `/debate stop <room_id>`\n" +
			"- `/debate list`"
	}
	return "Usage:\n" +
		"- `/debate start --preset tianji-five --rounds 3 --speaking-policy host-decide <question>`\n" +
		"- `/debate status [room_id]`\n" +
		"- `/debate stop <room_id>`\n" +
		"- `/debate list`"
}
