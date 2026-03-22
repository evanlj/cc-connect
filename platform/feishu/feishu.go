package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"

	"github.com/chenhg5/cc-connect/core"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkcb "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

var feishuAtTagPattern = regexp.MustCompile(`(?is)<at\b[^>]*>.*?</at>`)

func init() {
	core.RegisterPlatform("feishu", New)
}

type replyContext struct {
	messageID string
	chatID    string
}

type Platform struct {
	appID         string
	appSecret     string
	reactionEmoji string
	allowFrom     string
	client        *lark.Client
	wsClient      *larkws.Client
	handler       core.MessageHandler
	cancel        context.CancelFunc
}

func New(opts map[string]any) (core.Platform, error) {
	appID, _ := opts["app_id"].(string)
	appSecret, _ := opts["app_secret"].(string)
	if appID == "" || appSecret == "" {
		return nil, fmt.Errorf("feishu: app_id and app_secret are required")
	}
	reactionEmoji, _ := opts["reaction_emoji"].(string)
	if reactionEmoji == "" {
		reactionEmoji = "OnIt"
	}
	if v, ok := opts["reaction_emoji"].(string); ok && v == "none" {
		reactionEmoji = ""
	}
	allowFrom, _ := opts["allow_from"].(string)

	return &Platform{
		appID:         appID,
		appSecret:     appSecret,
		reactionEmoji: reactionEmoji,
		allowFrom:     allowFrom,
		client:        lark.NewClient(appID, appSecret),
	}, nil
}

func (p *Platform) Name() string { return "feishu" }

func (p *Platform) Start(handler core.MessageHandler) error {
	p.handler = handler

	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			slog.Debug("feishu: message received", "app_id", p.appID)
			return p.onMessage(event)
		}).
		OnP2MessageReadV1(func(ctx context.Context, event *larkim.P2MessageReadV1) error {
			return nil // ignore read receipts
		}).
		OnP2ChatAccessEventBotP2pChatEnteredV1(func(ctx context.Context, event *larkim.P2ChatAccessEventBotP2pChatEnteredV1) error {
			slog.Debug("feishu: user opened bot chat", "app_id", p.appID)
			return nil
		}).
		OnP1P2PChatCreatedV1(func(ctx context.Context, event *larkim.P1P2PChatCreatedV1) error {
			slog.Debug("feishu: p2p chat created", "app_id", p.appID)
			return nil
		}).
		OnP2MessageReactionCreatedV1(func(ctx context.Context, event *larkim.P2MessageReactionCreatedV1) error {
			return nil // ignore reaction events (triggered by our own addReaction)
		}).
		OnP2CardActionTrigger(func(ctx context.Context, event *larkcb.CardActionTriggerEvent) (*larkcb.CardActionTriggerResponse, error) {
			return p.onCardAction(ctx, event)
		})

	p.wsClient = larkws.NewClient(p.appID, p.appSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
	)

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	go func() {
		if err := p.wsClient.Start(ctx); err != nil {
			slog.Error("feishu: websocket error", "error", err)
		}
	}()

	return nil
}

func (p *Platform) addReaction(messageID string) {
	if p.reactionEmoji == "" {
		return
	}
	emojiType := p.reactionEmoji
	go func() {
		resp, err := p.client.Im.MessageReaction.Create(context.Background(),
			larkim.NewCreateMessageReactionReqBuilder().
				MessageId(messageID).
				Body(larkim.NewCreateMessageReactionReqBodyBuilder().
					ReactionType(&larkim.Emoji{EmojiType: &emojiType}).
					Build()).
				Build())
		if err != nil {
			slog.Debug("feishu: add reaction failed", "error", err)
			return
		}
		if !resp.Success() {
			slog.Debug("feishu: add reaction failed", "code", resp.Code, "msg", resp.Msg)
			return
		}
	}()
}

func (p *Platform) onMessage(event *larkim.P2MessageReceiveV1) error {
	msg := event.Event.Message
	sender := event.Event.Sender

	msgType := ""
	if msg.MessageType != nil {
		msgType = *msg.MessageType
	}

	chatID := ""
	if msg.ChatId != nil {
		chatID = *msg.ChatId
	}
	userID := ""
	userName := ""
	if sender.SenderId != nil {
		userID = *sender.SenderId.OpenId
	}
	if sender.SenderType != nil {
		userName = *sender.SenderType
	}

	messageID := ""
	if msg.MessageId != nil {
		messageID = *msg.MessageId
	}

	if !core.AllowList(p.allowFrom, userID) {
		slog.Debug("feishu: message from unauthorized user", "user", userID)
		return nil
	}

	if msgType != "" && messageID != "" {
		p.addReaction(messageID)
	}

	sessionKey := fmt.Sprintf("feishu:%s:%s", chatID, userID)
	rctx := replyContext{messageID: messageID, chatID: chatID}

	switch msgType {
	case "text":
		var textBody struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(*msg.Content), &textBody); err != nil {
			slog.Error("feishu: failed to parse text content", "error", err)
			return nil
		}

		// Built-in “menu” shortcut: show an interactive card with buttons.
		// This improves discoverability when users forget keywords/skills.
		if p.shouldShowMenuCard(textBody.Text) {
			if err := p.replyMenuCard(context.Background(), rctx); err != nil {
				slog.Error("feishu: reply menu card failed", "error", err)
			}
			return nil
		}

		p.handler(p, &core.Message{
			SessionKey: sessionKey, Platform: "feishu",
			UserID: userID, UserName: userName,
			Content: textBody.Text, ReplyCtx: rctx,
		})

	case "image":
		var imgBody struct {
			ImageKey string `json:"image_key"`
		}
		if err := json.Unmarshal([]byte(*msg.Content), &imgBody); err != nil {
			slog.Error("feishu: failed to parse image content", "error", err)
			return nil
		}
		imgData, mimeType, err := p.downloadImage(messageID, imgBody.ImageKey)
		if err != nil {
			slog.Error("feishu: download image failed", "error", err)
			return nil
		}
		p.handler(p, &core.Message{
			SessionKey: sessionKey, Platform: "feishu",
			UserID: userID, UserName: userName,
			Images:   []core.ImageAttachment{{MimeType: mimeType, Data: imgData}},
			ReplyCtx: rctx,
		})

	case "audio":
		var audioBody struct {
			FileKey  string `json:"file_key"`
			Duration int    `json:"duration"` // milliseconds
		}
		if err := json.Unmarshal([]byte(*msg.Content), &audioBody); err != nil {
			slog.Error("feishu: failed to parse audio content", "error", err)
			return nil
		}
		slog.Debug("feishu: audio received", "user", userID, "file_key", audioBody.FileKey)
		audioData, err := p.downloadResource(messageID, audioBody.FileKey, "file")
		if err != nil {
			slog.Error("feishu: download audio failed", "error", err)
			return nil
		}
		p.handler(p, &core.Message{
			SessionKey: sessionKey, Platform: "feishu",
			UserID: userID, UserName: userName,
			Audio: &core.AudioAttachment{
				MimeType: "audio/opus",
				Data:     audioData,
				Format:   "ogg",
				Duration: audioBody.Duration / 1000,
			},
			ReplyCtx: rctx,
		})

	default:
		slog.Debug("feishu: ignoring unsupported message type", "type", msgType)
	}

	return nil
}

func (p *Platform) Reply(ctx context.Context, rctx any, content string) error {
	rc, ok := rctx.(replyContext)
	if !ok {
		return fmt.Errorf("feishu: invalid reply context type %T", rctx)
	}

	msgType, msgBody := buildReplyContent(content)

	resp, err := p.client.Im.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().
		MessageId(rc.messageID).
		Body(larkim.NewReplyMessageReqBodyBuilder().
			MsgType(msgType).
			Content(msgBody).
			Build()).
		Build())
	if err != nil {
		return fmt.Errorf("feishu: reply api call: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishu: reply failed code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

func (p *Platform) onCardAction(ctx context.Context, event *larkcb.CardActionTriggerEvent) (*larkcb.CardActionTriggerResponse, error) {
	if event == nil || event.Event == nil || event.Event.Action == nil {
		return nil, nil
	}

	userID := ""
	if event.Event.Operator != nil {
		userID = event.Event.Operator.OpenID
	}

	chatID := ""
	messageID := ""
	if event.Event.Context != nil {
		chatID = event.Event.Context.OpenChatID
		messageID = event.Event.Context.OpenMessageID
	}

	// Hard gate: allowlist.
	if userID != "" && !core.AllowList(p.allowFrom, userID) {
		slog.Debug("feishu: card action from unauthorized user", "user", userID)
		return nil, nil
	}

	val := event.Event.Action.Value
	if val == nil {
		return nil, nil
	}
	formValue := event.Event.Action.FormValue
	ccAction := strings.ToLower(strings.TrimSpace(asStringAny(val["cc_action"])))
	choice := strings.ToLower(strings.TrimSpace(asStringAny(val["choice"])))
	var synthetic string
	var toast string
	switch ccAction {
	case "menu_select":
		switch choice {
		case "tapd":
			synthetic = "选择：TAPD（需求/缺陷/验收/回填）"
			toast = "已选择 TAPD"
		case "openspec":
			synthetic = "选择：OpenSpec 发布到 HTTP（站点 / task-hub / memory）"
			toast = "已选择 OpenSpec 发布"
		case "unity":
			synthetic = "选择：Unity / AGame（boss/场景/材质/shader/相机/AI/脚本报错）"
			toast = "已选择 Unity / AGame"
		case "dev":
			synthetic = "选择：代码实现 / 调试 / 文档"
			toast = "已选择 代码/文档"
		case "debate_start_demo":
			synthetic = "发起讨论模板：/debate start --mode consensus <原始话题>"
			toast = "已发送发起讨论模板（请替换话题）"
		case "debate_status":
			synthetic = "/debate status"
			toast = "正在查询讨论状态"
		case "debate_board":
			synthetic = "/debate board"
			toast = "正在查询讨论黑板"
		case "debate_list":
			synthetic = "/debate list"
			toast = "正在查询讨论房间"
		case "debate_topic_tpl":
			synthetic = "最终议题模板：/debate topic <room_id> <最终议题>"
			toast = "已发送最终议题模板"
		case "debate_participants_tpl":
			synthetic = "选人模板：/debate participants <room_id> 1,2,3"
			toast = "已发送选人模板"
		case "debate_stop_tpl":
			synthetic = "停止模板：/debate stop <room_id>"
			toast = "已发送停止模板"
		case "debate_control_card":
			cardJSON := buildDebateControlCardJSON()
			var sendErr error
			// Prefer replying to the clicked card message (works in both group & p2p
			// even when open_chat_id is absent in callback context).
			if strings.TrimSpace(messageID) != "" {
				sendErr = p.replyInteractiveCardByMessageID(ctx, messageID, cardJSON)
			}
			// Fallback: send a new message to chat when reply path is unavailable.
			if sendErr != nil && strings.TrimSpace(chatID) != "" {
				sendErr = p.sendInteractiveCardToChat(context.Background(), chatID, cardJSON)
			}
			if sendErr != nil {
				slog.Error("feishu: send debate control card failed", "chat_id", chatID, "message_id", messageID, "error", sendErr)
				toast = "打开讨论控制卡失败（请确认群权限/事件配置）"
			} else {
				toast = "已打开讨论控制卡"
			}
		case "squad_control_card":
			sessionKey := fmt.Sprintf("feishu:%s:%s", chatID, userID)
			cardJSON := buildSquadControlCardJSON(core.SquadLatestRunIDForOwnerSession(sessionKey))
			var sendErr error
			// Prefer replying to the clicked card message (works in both group & p2p
			// even when open_chat_id is absent in callback context).
			if strings.TrimSpace(messageID) != "" {
				sendErr = p.replyInteractiveCardByMessageID(ctx, messageID, cardJSON)
			}
			// Fallback: send a new message to chat when reply path is unavailable.
			if sendErr != nil && strings.TrimSpace(chatID) != "" {
				sendErr = p.sendInteractiveCardToChat(context.Background(), chatID, cardJSON)
			}
			if sendErr != nil {
				slog.Error("feishu: send squad control card failed", "chat_id", chatID, "message_id", messageID, "error", sendErr)
				toast = "打开 Squad 控制卡失败（请确认群权限/事件配置）"
			} else {
				toast = "已打开 Squad 控制卡"
			}
		default:
			toast = "未识别的选择"
		}
	case "debate_cmd":
		cmd := strings.ToLower(strings.TrimSpace(asStringAny(val["cmd"])))
		roomID := extractFormString(formValue, "room_id")
		synthetic, toast = buildDebateCommandByAction(cmd, roomID)
	case "debate_tpl":
		tpl := strings.ToLower(strings.TrimSpace(asStringAny(val["tpl"])))
		roomID := extractFormString(formValue, "room_id")
		synthetic, toast = buildDebateTemplateByAction(tpl, roomID)
	case "squad_cmd":
		cmd := strings.ToLower(strings.TrimSpace(asStringAny(val["cmd"])))
		runID := squadFormField(formValue, "run_id")
		reworkNote := squadFormField(formValue, "rework_note")
		synthetic, toast = buildSquadCommandByAction(cmd, runID, reworkNote)
	case "squad_tpl":
		tpl := strings.ToLower(strings.TrimSpace(asStringAny(val["tpl"])))
		runID := squadFormField(formValue, "run_id")
		synthetic, toast = buildSquadTemplateByAction(tpl, runID)
	default:
		// Ignore other cards/actions.
		return nil, nil
	}

	// Feed selection back into the normal message pipeline so the agent can continue.
	if synthetic != "" && p.handler != nil && chatID != "" && userID != "" {
		sessionKey := fmt.Sprintf("feishu:%s:%s", chatID, userID)
		rctx := replyContext{messageID: messageID, chatID: chatID}
		p.handler(p, &core.Message{
			SessionKey: sessionKey, Platform: "feishu",
			UserID: userID, UserName: "feishu_user",
			Content:  synthetic,
			ReplyCtx: rctx,
		})
	}

	if toast == "" {
		return nil, nil
	}

	return &larkcb.CardActionTriggerResponse{
		Toast: &larkcb.Toast{
			Type:    "info",
			Content: toast,
		},
	}, nil
}

func (p *Platform) shouldShowMenuCard(text string) bool {
	t := normalizeMenuTriggerText(text)
	if t == "" {
		return false
	}

	// Normalize trailing punctuation.
	t = strings.Trim(t, "？?!.。")

	// Exact-match triggers only (avoid false positives like “帮助我修复 bug ...”).
	switch strings.ToLower(t) {
	case "菜单", "/menu",
		"help", "/help", "帮助",
		"关键词", "触发词",
		"能做什么", "你能做什么",
		"有哪些功能", "你有哪些功能",
		"有哪些技能", "你有哪些技能",
		"怎么用", "忘了",
		"?", "？",
		"hi", "hello", "你好", "在吗", "...":
		return true
	default:
		return false
	}
}

func normalizeMenuTriggerText(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// Feishu group text often includes <at ...>name</at>; strip it first.
	s = feishuAtTagPattern.ReplaceAllString(s, " ")

	parts := strings.Fields(s)
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = strings.Trim(p, "，,。.!?;；:：()[]{}<>《》\"'`“”‘’")
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "@") {
			continue
		}
		clean = append(clean, p)
	}
	return strings.TrimSpace(strings.Join(clean, " "))
}

func (p *Platform) replyMenuCard(ctx context.Context, rc replyContext) error {
	if rc.messageID == "" {
		return fmt.Errorf("feishu: messageID is empty, cannot reply menu card")
	}

	cardJSON := buildMenuCardJSON()

	resp, err := p.client.Im.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().
		MessageId(rc.messageID).
		Body(larkim.NewReplyMessageReqBodyBuilder().
			MsgType(larkim.MsgTypeInteractive).
			Content(cardJSON).
			Build()).
		Build())
	if err != nil {
		return fmt.Errorf("feishu: reply menu card api call: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishu: reply menu card failed code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// Send sends a new message to the same chat (not a reply to original message)
func (p *Platform) Send(ctx context.Context, rctx any, content string) error {
	rc, ok := rctx.(replyContext)
	if !ok {
		return fmt.Errorf("feishu: invalid reply context type %T", rctx)
	}

	if rc.chatID == "" {
		return fmt.Errorf("feishu: chatID is empty, cannot send new message")
	}

	msgType, msgBody := buildReplyContent(content)

	// Send a new message to the chat (not a reply)
	resp, err := p.client.Im.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.ReceiveIdTypeChatId).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(rc.chatID).
			MsgType(msgType).
			Content(msgBody).
			Build()).
		Build())
	if err != nil {
		return fmt.Errorf("feishu: send api call: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishu: send failed code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// downloadImage fetches an image from Feishu by message_id and image_key.
func (p *Platform) downloadImage(messageID, imageKey string) ([]byte, string, error) {
	resp, err := p.client.Im.MessageResource.Get(context.Background(),
		larkim.NewGetMessageResourceReqBuilder().
			MessageId(messageID).
			FileKey(imageKey).
			Type("image").
			Build())
	if err != nil {
		return nil, "", fmt.Errorf("feishu: image API: %w", err)
	}
	if !resp.Success() {
		return nil, "", fmt.Errorf("feishu: image API code=%d msg=%s", resp.Code, resp.Msg)
	}
	data, err := io.ReadAll(resp.File)
	if err != nil {
		return nil, "", fmt.Errorf("feishu: read image: %w", err)
	}

	mimeType := detectMimeType(data)
	slog.Debug("feishu: downloaded image", "key", imageKey, "size", len(data), "mime", mimeType)
	return data, mimeType, nil
}

// downloadResource fetches a file resource (audio, etc.) from Feishu by message_id and file_key.
func (p *Platform) downloadResource(messageID, fileKey, resType string) ([]byte, error) {
	resp, err := p.client.Im.MessageResource.Get(context.Background(),
		larkim.NewGetMessageResourceReqBuilder().
			MessageId(messageID).
			FileKey(fileKey).
			Type(resType).
			Build())
	if err != nil {
		return nil, fmt.Errorf("feishu: resource API: %w", err)
	}
	if !resp.Success() {
		return nil, fmt.Errorf("feishu: resource API code=%d msg=%s", resp.Code, resp.Msg)
	}
	data, err := io.ReadAll(resp.File)
	if err != nil {
		return nil, fmt.Errorf("feishu: read resource: %w", err)
	}
	slog.Debug("feishu: downloaded resource", "key", fileKey, "type", resType, "size", len(data))
	return data, nil
}

func detectMimeType(data []byte) string {
	if len(data) >= 8 {
		if data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
			return "image/png"
		}
		if data[0] == 0xFF && data[1] == 0xD8 {
			return "image/jpeg"
		}
		if string(data[:4]) == "GIF8" {
			return "image/gif"
		}
		if string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
			return "image/webp"
		}
	}
	return "image/png"
}

// buildReplyContent decides between plain text and interactive card based on content.
func buildReplyContent(content string) (msgType string, body string) {
	if !containsMarkdown(content) {
		b, _ := json.Marshal(map[string]string{"text": content})
		return larkim.MsgTypeText, string(b)
	}
	return larkim.MsgTypeInteractive, buildCardJSON(adaptMarkdown(content))
}

var markdownIndicators = []string{
	"```", "**", "~~", "\n- ", "\n* ", "\n1. ", "\n# ", "---",
}

func containsMarkdown(s string) bool {
	for _, ind := range markdownIndicators {
		if strings.Contains(s, ind) {
			return true
		}
	}
	return false
}

// adaptMarkdown converts standard markdown to Feishu card-compatible markdown.
// Feishu card markdown elements do NOT support # headers or > blockquotes,
// so we convert them to bold text and indented text respectively.
func adaptMarkdown(s string) string {
	lines := strings.Split(s, "\n")
	inCodeBlock := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock {
			continue
		}

		for level := 6; level >= 1; level-- {
			prefix := strings.Repeat("#", level) + " "
			if strings.HasPrefix(line, prefix) {
				lines[i] = "**" + strings.TrimPrefix(line, prefix) + "**"
				break
			}
		}

		if strings.HasPrefix(line, "> ") {
			lines[i] = "  " + strings.TrimPrefix(line, "> ")
		}
	}

	return strings.Join(lines, "\n")
}

func buildCardJSON(content string) string {
	card := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"elements": []any{
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "lark_md",
					"content": content,
				},
			},
		},
	}
	b, _ := json.Marshal(card)
	return string(b)
}

func buildMenuCardJSON() string {
	// Keep this menu short (1 screen). The agent will do the follow-up questions
	// after the user clicks.
	card := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"title": map[string]any{
				"tag":     "plain_text",
				"content": "快捷菜单（业务 + 多Bot讨论）",
			},
			"template": "blue",
		},
		"elements": []any{
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag": "lark_md",
					"content": "忘记关键字时可直接用这个菜单。\n\n" +
						"请选择一个方向：",
				},
			},
			map[string]any{
				"tag": "action",
				"actions": []any{
					map[string]any{
						"tag":  "button",
						"type": "primary",
						"text": map[string]any{"tag": "plain_text", "content": "1) TAPD"},
						"value": map[string]any{
							"cc_action": "menu_select",
							"choice":    "tapd",
						},
					},
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "2) OpenSpec 发布"},
						"value": map[string]any{
							"cc_action": "menu_select",
							"choice":    "openspec",
						},
					},
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "3) Unity / AGame"},
						"value": map[string]any{
							"cc_action": "menu_select",
							"choice":    "unity",
						},
					},
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "4) 代码/文档"},
						"value": map[string]any{
							"cc_action": "menu_select",
							"choice":    "dev",
						},
					},
				},
			},
			map[string]any{
				"tag": "hr",
			},
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "lark_md",
					"content": "**多Bot讨论快捷操作**",
				},
			},
			map[string]any{
				"tag": "action",
				"actions": []any{
					map[string]any{
						"tag":  "button",
						"type": "primary",
						"text": map[string]any{"tag": "plain_text", "content": "发起讨论模板"},
						"value": map[string]any{
							"cc_action": "menu_select",
							"choice":    "debate_start_demo",
						},
					},
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "讨论状态"},
						"value": map[string]any{
							"cc_action": "menu_select",
							"choice":    "debate_status",
						},
					},
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "讨论黑板"},
						"value": map[string]any{
							"cc_action": "menu_select",
							"choice":    "debate_board",
						},
					},
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "讨论列表"},
						"value": map[string]any{
							"cc_action": "menu_select",
							"choice":    "debate_list",
						},
					},
				},
			},
			map[string]any{
				"tag": "action",
				"actions": []any{
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "最终议题模板"},
						"value": map[string]any{
							"cc_action": "menu_select",
							"choice":    "debate_topic_tpl",
						},
					},
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "选人模板"},
						"value": map[string]any{
							"cc_action": "menu_select",
							"choice":    "debate_participants_tpl",
						},
					},
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "停止模板"},
						"value": map[string]any{
							"cc_action": "menu_select",
							"choice":    "debate_stop_tpl",
						},
					},
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "讨论控制卡"},
						"value": map[string]any{
							"cc_action": "menu_select",
							"choice":    "debate_control_card",
						},
					},
				},
			},
			map[string]any{
				"tag": "action",
				"actions": []any{
					map[string]any{
						"tag":  "button",
						"type": "primary",
						"text": map[string]any{"tag": "plain_text", "content": "Squad 审核控制卡"},
						"value": map[string]any{
							"cc_action": "menu_select",
							"choice":    "squad_control_card",
						},
					},
				},
			},
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "lark_md",
					"content": "也可以直接发一句话描述需求（比如：`查 workspace_id=66052431 未完成需求`）。",
				},
			},
		},
	}

	b, _ := json.Marshal(card)
	return string(b)
}

func buildDebateControlCardJSON() string {
	card := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"title": map[string]any{
				"tag":     "plain_text",
				"content": "多Bot讨论控制卡",
			},
			"template": "purple",
		},
		"elements": []any{
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag": "lark_md",
					"content": "在下方输入 room_id（可选），然后点击按钮执行。\n" +
						"- 不填 room_id：默认执行无参命令（如 /debate status）\n" +
						"- 填写 room_id：优先执行带房间命令",
				},
			},
			map[string]any{
				"tag":  "input",
				"name": "room_id",
				"label": map[string]any{
					"tag":     "plain_text",
					"content": "Room ID（可选）",
				},
				"placeholder": map[string]any{
					"tag":     "plain_text",
					"content": "debate_20260320_xxx",
				},
			},
			map[string]any{
				"tag": "action",
				"actions": []any{
					map[string]any{
						"tag":  "button",
						"type": "primary",
						"text": map[string]any{"tag": "plain_text", "content": "状态"},
						"value": map[string]any{
							"cc_action": "debate_cmd",
							"cmd":       "status",
						},
					},
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "黑板"},
						"value": map[string]any{
							"cc_action": "debate_cmd",
							"cmd":       "board",
						},
					},
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "列表"},
						"value": map[string]any{
							"cc_action": "debate_cmd",
							"cmd":       "list",
						},
					},
					map[string]any{
						"tag":  "button",
						"type": "danger",
						"text": map[string]any{"tag": "plain_text", "content": "停止"},
						"value": map[string]any{
							"cc_action": "debate_cmd",
							"cmd":       "stop",
						},
					},
				},
			},
			map[string]any{
				"tag": "hr",
			},
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "lark_md",
					"content": "**一键填充命令模板**（会把 room_id 自动带入，未填则保留 ROOM_ID 占位）",
				},
			},
			map[string]any{
				"tag": "action",
				"actions": []any{
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "最终议题模板"},
						"value": map[string]any{
							"cc_action": "debate_tpl",
							"tpl":       "topic",
						},
					},
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "满意模板"},
						"value": map[string]any{
							"cc_action": "debate_tpl",
							"tpl":       "approve",
						},
					},
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "不满意模板"},
						"value": map[string]any{
							"cc_action": "debate_tpl",
							"tpl":       "reject",
						},
					},
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "选人模板"},
						"value": map[string]any{
							"cc_action": "debate_tpl",
							"tpl":       "participants",
						},
					},
				},
			},
			map[string]any{
				"tag": "action",
				"actions": []any{
					map[string]any{
						"tag":  "button",
						"type": "default",
						"text": map[string]any{"tag": "plain_text", "content": "发起讨论模板"},
						"value": map[string]any{
							"cc_action": "debate_tpl",
							"tpl":       "start",
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(card)
	return string(b)
}

func buildSquadControlCardJSON(defaultRunID string) string {
	defaultRunID = strings.TrimSpace(defaultRunID)
	if len(defaultRunID) > 240 {
		defaultRunID = defaultRunID[:240]
	}

	squadV2Callback := func(val map[string]any) []any {
		return []any{map[string]any{"type": "callback", "value": val}}
	}
	squadV2SubmitBtn := func(name, btnType, label string, val map[string]any) map[string]any {
		return map[string]any{
			"tag":              "button",
			"name":             name,
			"type":             btnType,
			"text":             map[string]any{"tag": "plain_text", "content": label},
			"form_action_type": "submit",
			"behaviors":        squadV2Callback(val),
		}
	}
	squadV2Col := func(width string, elems ...any) map[string]any {
		return map[string]any{
			"tag":            "column",
			"width":          width,
			"vertical_align": "top",
			"elements":       elems,
		}
	}
	squadV2Row := func(cols ...map[string]any) map[string]any {
		return map[string]any{
			"tag":                "column_set",
			"flex_mode":          "flow",
			"background_style":   "default",
			"horizontal_spacing": "medium",
			"columns":            cols,
		}
	}

	intro := "**Squad 审核（JSON 2.0 表单）**\n\n" +
		"在下方填写 **Run ID** 后，点击按钮将**整表提交**（飞书要求带输入的卡片使用表单提交）。\n" +
		"Run ID 已按你在本实例下**最近一次更新的 Squad 运行**自动预填；可改。\n" +
		"裁决返工前请在第二栏填写原因与修改方案。"

	formElements := []any{
		map[string]any{
			"tag":           "input",
			"element_id":    "el_sq_run",
			"name":          "run_id",
			"width":         "fill",
			"label":         map[string]any{"tag": "plain_text", "content": "Run ID"},
			"placeholder":   map[string]any{"tag": "plain_text", "content": "squad_20260321_xxx"},
			"default_value": defaultRunID,
			"required":      false,
			"max_length":    400,
		},
		map[string]any{
			"tag":           "input",
			"element_id":    "el_sq_note",
			"name":          "rework_note",
			"input_type":    "multiline_text",
			"rows":          3,
			"width":         "fill",
			"label":         map[string]any{"tag": "plain_text", "content": "返工或跳过说明"},
			"placeholder":   map[string]any{"tag": "plain_text", "content": "问题与修改方案，或跳过理由"},
			"default_value": "",
			"required":      false,
			"max_length":    1000,
		},
		squadV2Row(
			squadV2Col("auto", squadV2SubmitBtn("sq_btn_list", "default", "运行列表", map[string]any{"cc_action": "squad_cmd", "cmd": "list"})),
			squadV2Col("auto", squadV2SubmitBtn("sq_btn_stat", "default", "状态", map[string]any{"cc_action": "squad_cmd", "cmd": "status"})),
			squadV2Col("auto", squadV2SubmitBtn("sq_btn_plan", "default", "查看计划", map[string]any{"cc_action": "squad_cmd", "cmd": "show_plan"})),
			squadV2Col("auto", squadV2SubmitBtn("sq_btn_task", "default", "查看任务", map[string]any{"cc_action": "squad_cmd", "cmd": "show_task"})),
		),
		squadV2Row(
			squadV2Col("auto", squadV2SubmitBtn("sq_btn_aplan", "primary", "批准计划", map[string]any{"cc_action": "squad_cmd", "cmd": "approve_plan"})),
			squadV2Col("auto", squadV2SubmitBtn("sq_btn_atask", "primary", "批准任务", map[string]any{"cc_action": "squad_cmd", "cmd": "approve_task"})),
			squadV2Col("auto", squadV2SubmitBtn("sq_btn_skip", "default", "跳过任务", map[string]any{"cc_action": "squad_cmd", "cmd": "skip_task"})),
		),
		squadV2Row(
			squadV2Col("auto", squadV2SubmitBtn("sq_btn_jpass", "primary", "裁决通过", map[string]any{"cc_action": "squad_cmd", "cmd": "judge_pass"})),
			squadV2Col("auto", squadV2SubmitBtn("sq_btn_jrework", "danger", "裁决返工", map[string]any{"cc_action": "squad_cmd", "cmd": "judge_rework"})),
		),
		map[string]any{
			"tag":        "hr",
			"element_id": "sq_div_1",
			"margin":     "8px 0",
		},
		map[string]any{
			"tag":         "markdown",
			"element_id":  "sq_tpl_hint",
			"content":     "**一键模板**（提交时带入当前 Run ID；未填则用 RUN_ID 占位）",
			"text_align":  "left",
			"text_size":   "normal",
		},
		squadV2Row(
			squadV2Col("auto", squadV2SubmitBtn("sq_tpl_start", "default", "启动模板", map[string]any{"cc_action": "squad_tpl", "tpl": "start"})),
			squadV2Col("auto", squadV2SubmitBtn("sq_tpl_aplan", "default", "计划确认", map[string]any{"cc_action": "squad_tpl", "tpl": "approve_plan"})),
			squadV2Col("auto", squadV2SubmitBtn("sq_tpl_atask", "default", "任务确认", map[string]any{"cc_action": "squad_tpl", "tpl": "approve_task"})),
		),
		squadV2Row(
			squadV2Col("auto", squadV2SubmitBtn("sq_tpl_skip", "default", "跳过模板", map[string]any{"cc_action": "squad_tpl", "tpl": "skip_task"})),
			squadV2Col("auto", squadV2SubmitBtn("sq_tpl_jrw", "default", "返工模板", map[string]any{"cc_action": "squad_tpl", "tpl": "judge_rework"})),
		),
	}

	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"update_multi": true,
			"width_mode":   "fill",
		},
		"header": map[string]any{
			"template": "green",
			"title": map[string]any{
				"tag":     "plain_text",
				"content": "Squad 审核控制卡",
			},
		},
		"body": map[string]any{
			"elements": []any{
				map[string]any{
					"tag":         "markdown",
					"element_id":  "sq_intro",
					"content":     intro,
					"text_align":  "left",
					"text_size":   "normal",
				},
				map[string]any{
					"tag":        "form",
					"element_id": "sq_form",
					"name":       "squad_main_form",
					"elements":   formElements,
				},
			},
		},
	}
	b, _ := json.Marshal(card)
	return string(b)
}

func (p *Platform) sendInteractiveCardToChat(ctx context.Context, chatID, cardJSON string) error {
	chatID = strings.TrimSpace(chatID)
	cardJSON = strings.TrimSpace(cardJSON)
	if chatID == "" {
		return fmt.Errorf("feishu: chatID is empty, cannot send interactive card")
	}
	if cardJSON == "" {
		return fmt.Errorf("feishu: card content is empty")
	}
	resp, err := p.client.Im.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.ReceiveIdTypeChatId).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType(larkim.MsgTypeInteractive).
			Content(cardJSON).
			Build()).
		Build())
	if err != nil {
		return fmt.Errorf("feishu: send interactive card api call: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishu: send interactive card failed code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

func (p *Platform) replyInteractiveCardByMessageID(ctx context.Context, messageID, cardJSON string) error {
	messageID = strings.TrimSpace(messageID)
	cardJSON = strings.TrimSpace(cardJSON)
	if messageID == "" {
		return fmt.Errorf("feishu: messageID is empty, cannot reply interactive card")
	}
	if cardJSON == "" {
		return fmt.Errorf("feishu: card content is empty")
	}
	resp, err := p.client.Im.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewReplyMessageReqBodyBuilder().
			MsgType(larkim.MsgTypeInteractive).
			Content(cardJSON).
			Build()).
		Build())
	if err != nil {
		return fmt.Errorf("feishu: reply interactive card api call: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishu: reply interactive card failed code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

func asStringAny(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return ""
	}
}

func extractFormString(form map[string]interface{}, key string) string {
	if len(form) == 0 || strings.TrimSpace(key) == "" {
		return ""
	}
	v, ok := form[key]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case map[string]interface{}:
		if inner, ok := x["value"]; ok {
			return strings.TrimSpace(asStringAny(inner))
		}
	case []interface{}:
		if len(x) > 0 {
			return strings.TrimSpace(asStringAny(x[0]))
		}
	}
	return ""
}

// squadFormField reads a named field from card form_value (JSON 1.0 flat map or 2.0 nested maps).
func squadFormField(form map[string]interface{}, key string) string {
	if s := extractFormString(form, key); s != "" {
		return s
	}
	for _, v := range form {
		nested, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if s := extractFormString(nested, key); s != "" {
			return s
		}
	}
	return ""
}

func buildDebateCommandByAction(cmd, roomID string) (string, string) {
	roomID = strings.TrimSpace(roomID)
	switch strings.ToLower(strings.TrimSpace(cmd)) {
	case "status":
		if roomID != "" {
			return "/debate status " + roomID, "正在查询讨论状态"
		}
		return "/debate status", "正在查询讨论状态"
	case "board":
		if roomID != "" {
			return "/debate board " + roomID, "正在查询讨论黑板"
		}
		return "/debate board", "正在查询讨论黑板"
	case "list":
		return "/debate list", "正在查询讨论房间"
	case "stop":
		if roomID != "" {
			return "/debate stop " + roomID, "正在停止讨论"
		}
		return "停止模板：/debate stop <room_id>", "请先填写 room_id 后再停止"
	default:
		return "", "未识别的讨论命令"
	}
}

func buildDebateTemplateByAction(tpl, roomID string) (string, string) {
	roomID = strings.TrimSpace(roomID)
	withRoom := roomID
	if withRoom == "" {
		withRoom = "<room_id>"
	}
	switch strings.ToLower(strings.TrimSpace(tpl)) {
	case "topic":
		return fmt.Sprintf("最终议题模板：/debate topic %s <最终议题>", withRoom), "已发送最终议题模板"
	case "approve":
		return fmt.Sprintf("满意模板：/debate decision %s approve <可选反馈>", withRoom), "已发送满意模板"
	case "reject":
		return fmt.Sprintf("不满意模板：/debate decision %s reject <反馈>", withRoom), "已发送不满意模板"
	case "participants":
		return fmt.Sprintf("选人模板：/debate participants %s 1,2,3", withRoom), "已发送选人模板"
	case "start":
		return "/debate start --mode consensus <原始话题>", "已发送发起讨论模板"
	default:
		return "", "未识别的模板动作"
	}
}

func buildSquadCommandByAction(cmd, runID, reworkNote string) (string, string) {
	runID = strings.TrimSpace(runID)
	reworkNote = strings.TrimSpace(strings.Join(strings.Fields(reworkNote), " "))
	requireRunID := func() (string, string, bool) {
		if runID == "" {
			return "", "请先填写 run_id", false
		}
		return runID, "", true
	}
	switch strings.ToLower(strings.TrimSpace(cmd)) {
	case "list":
		return "/squad list", "正在查询 Squad 运行列表"
	case "status":
		id, tip, ok := requireRunID()
		if !ok {
			return "run_id 示例：squad_20260321_xxx", tip
		}
		return "/squad status " + id, "正在查询 Squad 状态"
	case "show_plan":
		id, tip, ok := requireRunID()
		if !ok {
			return "run_id 示例：squad_20260321_xxx", tip
		}
		return "/squad show-plan " + id, "正在查询 Squad 计划"
	case "show_task":
		id, tip, ok := requireRunID()
		if !ok {
			return "run_id 示例：squad_20260321_xxx", tip
		}
		return "/squad show-task " + id + " current", "正在查询当前任务"
	case "approve_plan":
		id, tip, ok := requireRunID()
		if !ok {
			return "run_id 示例：squad_20260321_xxx", tip
		}
		return "/squad approve-plan " + id, "正在批准计划"
	case "approve_task":
		id, tip, ok := requireRunID()
		if !ok {
			return "run_id 示例：squad_20260321_xxx", tip
		}
		return "/squad approve-task " + id + " current", "正在批准当前任务"
	case "skip_task":
		id, tip, ok := requireRunID()
		if !ok {
			return "run_id 示例：squad_20260321_xxx", tip
		}
		if reworkNote != "" {
			return "/squad skip-task " + id + " " + reworkNote, "已发送跳过任务命令"
		}
		return "/squad skip-task " + id + " 用户手动跳过", "已发送跳过任务命令"
	case "judge_pass":
		id, tip, ok := requireRunID()
		if !ok {
			return "run_id 示例：squad_20260321_xxx", tip
		}
		return "/squad judge-review " + id + " pass", "已发送通过裁决"
	case "judge_rework":
		id, tip, ok := requireRunID()
		if !ok {
			return "run_id 示例：squad_20260321_xxx", tip
		}
		if reworkNote == "" {
			return "请先填写不通过原因与修改方案，然后再点击“裁决返工”", "返工信息为空"
		}
		return "/squad judge-review " + id + " rework " + reworkNote, "已发送返工裁决"
	default:
		return "", "未识别的 Squad 命令"
	}
}

func buildSquadTemplateByAction(tpl, runID string) (string, string) {
	runID = strings.TrimSpace(runID)
	withRun := runID
	if withRun == "" {
		withRun = "RUN_ID"
	}
	switch strings.ToLower(strings.TrimSpace(tpl)) {
	case "start":
		return "启动模板：/squad start --repo D:\\\\your-repo --planner jarvis --executor xingzou --reviewer jianzhu --provider codez <需求>", "已发送启动模板"
	case "approve_plan":
		return "/squad approve-plan " + withRun, "已发送计划确认模板"
	case "approve_task":
		return "/squad approve-task " + withRun + " current", "已发送任务确认模板"
	case "skip_task":
		return "/squad skip-task " + withRun + " 暂不需要该任务，先跳过", "已发送跳过任务模板"
	case "judge_rework":
		return "/squad judge-review " + withRun + " rework 不通过原因：...；修改方案：1)... 2)...", "已发送返工裁决模板"
	default:
		return "", "未识别的 Squad 模板动作"
	}
}

func (p *Platform) ReconstructReplyCtx(sessionKey string) (any, error) {
	// feishu:{chatID}:{userID}
	parts := strings.SplitN(sessionKey, ":", 3)
	if len(parts) < 2 || parts[0] != "feishu" {
		return nil, fmt.Errorf("feishu: invalid session key %q", sessionKey)
	}
	return replyContext{chatID: parts[1]}, nil
}

func (p *Platform) Stop() error {
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}
