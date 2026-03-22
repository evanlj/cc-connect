package core

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSelectDebateSpeakers(t *testing.T) {
	workers := []DebateRole{
		{Role: "jianzhu", DisplayName: "剑主"},
		{Role: "wendan", DisplayName: "文胆"},
		{Role: "xingzou", DisplayName: "行走"},
		{Role: "zhanggui", DisplayName: "掌柜"},
	}

	got1 := selectDebateSpeakers("host-decide", 1, workers, map[string]bool{}, 3)
	if len(got1) != 2 || got1[0].Role != "jianzhu" || got1[1].Role != "wendan" {
		t.Fatalf("round1 host-decide unexpected: %+v", got1)
	}

	spoken := map[string]bool{"jianzhu": true, "wendan": true}
	got2 := selectDebateSpeakers("cover-all-by-end", 3, workers, spoken, 3)
	if len(got2) < 2 {
		t.Fatalf("cover-all-by-end should prioritize uncovered roles: %+v", got2)
	}

	gotFinal := selectDebateSpeakers("host-decide", 3, workers, spoken, 3)
	if len(gotFinal) != len(workers) {
		t.Fatalf("final round should include all workers, got=%d want=%d", len(gotFinal), len(workers))
	}
}

func TestBuildRoleSessionKey(t *testing.T) {
	key := buildRoleSessionKey("feishu:oc_chat_xxx:ou_user", "oc_chat_xxx", "debate_20260317_152601_123456", "jianzhu")
	want := "feishu:oc_chat_xxx:debate_debate_20260317_152601_123456_jianzhu"
	if key != want {
		t.Fatalf("session key mismatch: got %q want %q", key, want)
	}
}

func TestBuildRolePromptGuardrails(t *testing.T) {
	room := &DebateRoom{
		Question:  "如何把需求拆成可并行执行的任务",
		MaxRounds: 3,
		RoomID:    "debate_20260317_190001_123456",
	}
	role := DebateRole{Role: "jianzhu", DisplayName: "剑主"}

	board := &DebateBlackboard{
		Topic:      room.Question,
		Goal:       "形成可执行的并行任务拆分方案",
		RoundPlan:  "补充证据、对比方案并收敛可执行动作。",
		RoundFocus: "先对齐拆分原则，再给职责边界",
		Revision:   5,
		RoleNotes: map[string]DebateRoleNote{
			"wendan": {LatestStance: "先定义统一模板再拆任务。"},
		},
	}

	got := buildRolePrompt(room, role, 1, nil, board, `D:\ai-github\cc-connect\mutilbot\instance-mutilbot1\data\discussion\p\blackboards\debate_x.json`)
	if got == "" {
		t.Fatal("prompt should not be empty")
	}
	if !containsAll(got,
		"[黑板文件]",
		"必须先读取该 JSON",
		"本轮计划：补充证据、对比方案并收敛可执行动作。",
		"\"type\": \"blackboard_writeback\"",
		"\"base_revision\": 5",
	) {
		t.Fatalf("prompt blackboard context missing:\n%s", got)
	}
}

func TestFlattenPromptForTransport(t *testing.T) {
	raw := "第一行\n\n第二行  \r\n  第三行"
	got := flattenPromptForTransport(raw)
	if strings.Contains(got, "\n") || strings.Contains(got, "\r") {
		t.Fatalf("flattened prompt should not contain new lines: %q", got)
	}
	if !containsAll(got, "第一行", "第二行", "第三行") {
		t.Fatalf("flattened prompt missing content: %q", got)
	}
}

func TestDebateReplyNeedsRepairMenuized(t *testing.T) {
	reply := "【观点】先确认方向。\n【依据】信息不足。\n【风险/反例】可能跑偏。\n【建议动作】你要选哪一类？回复 1/2/3/4。"
	need, issues := debateReplyNeedsRepair(reply)
	if !need {
		t.Fatalf("menuized reply should require repair, issues=%v", issues)
	}
	if len(issues) == 0 {
		t.Fatal("issues should not be empty")
	}
}

func TestDebateReplyNeedsRepairGoodReply(t *testing.T) {
	reply := "【观点】先按能力边界把需求拆成并行工作流。\n【依据】并行切片可降低串行等待。\n【风险/反例】切片过细会增加沟通成本。\n【建议动作】先定义 4 条并行泳道并约定验收口径。"
	need, issues := debateReplyNeedsRepair(reply)
	if need {
		t.Fatalf("good reply should not require repair, issues=%v", issues)
	}
}

func TestExtractDebateDisplayContentDropsWritebackSection(t *testing.T) {
	raw := "A) 群内可读内容（精简）：\n【观点】先定边界。\n【依据】避免返工。\n【风险/反例】不定边界会分叉。\n【建议动作】先出边界表。\n\nB) 黑板回填 JSON：\n```json\n{\"type\":\"blackboard_writeback\",\"room_id\":\"r1\",\"role\":\"jianzhu\"}\n```"
	got := extractDebateDisplayContent(raw)
	if got == "" {
		t.Fatal("display content should not be empty")
	}
	if strings.Contains(got, "黑板回填") || strings.Contains(got, "blackboard_writeback") {
		t.Fatalf("display content should drop writeback section, got: %q", got)
	}
	if !containsAll(got, "【观点】", "【建议动作】") {
		t.Fatalf("display content missing core sections: %q", got)
	}
}

func TestDebateSummaryNeedsRepairRefusal(t *testing.T) {
	bad := "把讨论记录贴出来我才能总结。你把原文发我后我再输出。"
	need, issues := debateSummaryNeedsRepair(bad)
	if !need {
		t.Fatalf("refusal summary should require repair, issues=%v", issues)
	}
	if len(issues) == 0 {
		t.Fatal("issues should not be empty")
	}
}

func TestDebateSummaryNeedsRepairGoodSummary(t *testing.T) {
	okSummary := "最终结论：采用组合式+数据驱动。\n主要风险：模块粒度过碎、指标过理想。\n行动项：\n- owner: jianzhu, deadline: 2026-03-18, 验收标准: 提交边界决策表。"
	need, issues := debateSummaryNeedsRepair(okSummary)
	if need {
		t.Fatalf("good summary should not require repair, issues=%v", issues)
	}
}

func TestDebateSummaryNeedsRepairPendingOwner(t *testing.T) {
	bad := "最终结论：先做MVP。\n主要风险：范围失控。\n行动项：\n- owner: 待定, deadline: 2026-03-19, 验收标准: 输出RFC。"
	need, issues := debateSummaryNeedsRepair(bad)
	if !need {
		t.Fatalf("pending owner summary should require repair, issues=%v", issues)
	}
	if len(issues) == 0 {
		t.Fatal("issues should not be empty")
	}
}

func TestIsAskTimeoutErr(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{err: nil, want: false},
		{err: errors.New("ask status=500: ask timeout after 2m20s"), want: true},
		{err: errors.New("context deadline exceeded"), want: true},
		{err: errors.New("network unreachable"), want: false},
	}
	for _, tc := range cases {
		got := isAskTimeoutErr(tc.err)
		if got != tc.want {
			t.Fatalf("isAskTimeoutErr(%v)=%v want=%v", tc.err, got, tc.want)
		}
	}
}

func TestSplitConsensusRolesForRoomWithConfirmedParticipants(t *testing.T) {
	room := &DebateRoom{
		HostRole: "jarvis",
		Roles: []DebateRole{
			{Role: "jarvis", DisplayName: "Jarvis"},
			{Role: "jianzhu", DisplayName: "剑主"},
			{Role: "wendan", DisplayName: "文胆"},
			{Role: "xingzou", DisplayName: "行走"},
		},
		ConfirmedParticipants: []string{"jianzhu", "xingzou"},
	}
	host, workers := splitConsensusRolesForRoom(room, true)
	if host.Role != "jarvis" {
		t.Fatalf("host mismatch: %q", host.Role)
	}
	if len(workers) != 2 {
		t.Fatalf("workers len mismatch: %d", len(workers))
	}
	if workers[0].Role != "jianzhu" || workers[1].Role != "xingzou" {
		t.Fatalf("workers mismatch: %+v", workers)
	}
}

func TestRenderDebateFinalReport(t *testing.T) {
	room := &DebateRoom{
		RoomID:                "debate_20260319_100000_000001",
		Mode:                  DebateModeConsensus,
		HostRole:              "jarvis",
		Status:                DebateStatusCompleted,
		Question:              "如何把需求拆成可并行执行的任务",
		RefinedQuestion:       "如何把需求拆成可并行执行任务并定义验收标准",
		UserReviewStatus:      "rejected",
		UserReviewFeedback:    "首轮方案还需要更细的角色分工",
		ConfirmedParticipants: []string{"jianzhu", "wendan"},
		CreatedAt:             time.Now().Add(-20 * time.Minute),
	}
	board := &DebateBlackboard{
		RefinedTopic:      room.RefinedQuestion,
		HostFirstProposal: "先定义拆分维度：业务模块、依赖、风险、验收。",
		ConsensusPoints:   []string{"先锁定目标产物", "按依赖拓扑拆分"},
		Unresolved:        []string{"跨团队接口变更节奏"},
	}
	transcript := []DebateTranscriptEntry{
		{Round: 1, Role: "jianzhu", Content: "建议先明确边界。"},
		{Round: 2, Role: "wendan", Content: "建议给每项任务加验收标准。"},
	}
	got := renderDebateFinalReport(room, board, transcript, "最终结论：先完成任务拓扑图，再并行执行。")
	if got == "" {
		t.Fatal("report should not be empty")
	}
	if !containsAll(got, "多Bot讨论成果汇总", "主持人首轮方案", "用户评审", "最终共识总结", "讨论轨迹") {
		t.Fatalf("report missing expected sections:\n%s", got)
	}
}

func TestExtractClarifyDecisionNeedMore(t *testing.T) {
	raw := "A) 请先补充：你希望最终产出是规划文档还是可执行任务清单？\n\n```json\n{\n  \"type\": \"clarify_decision\",\n  \"need_more\": true,\n  \"question\": \"你希望最终产出是规划文档还是可执行任务清单？\",\n  \"refined_topic\": \"\",\n  \"missing\": [\"目标产物\"],\n  \"summary\": \"缺少目标产物定义\"\n}\n```"
	got, visible := extractClarifyDecision(raw)
	if !got.NeedMore {
		t.Fatalf("need_more mismatch: %+v", got)
	}
	if got.Question == "" {
		t.Fatalf("question should not be empty: %+v", got)
	}
	if strings.Contains(visible, "\"clarify_decision\"") {
		t.Fatalf("visible should strip json section, got=%q", visible)
	}
}

func TestExtractClarifyDecisionDone(t *testing.T) {
	raw := "【澄清结论】信息已足够，进入首轮方案。\n【已锁定议题】围绕“需求拆分并行化”，输出含 owner/依赖/验收标准的执行计划。\n\n```json\n{\"type\":\"clarify_decision\",\"need_more\":false,\"question\":\"\",\"refined_topic\":\"围绕需求拆分并行化，输出含 owner/依赖/验收标准的执行计划。\",\"missing\":[],\"summary\":\"澄清完成\"}\n```"
	got, _ := extractClarifyDecision(raw)
	if got.NeedMore {
		t.Fatalf("need_more should be false: %+v", got)
	}
	if got.RefinedTopic == "" {
		t.Fatalf("refined topic should not be empty: %+v", got)
	}
}

func TestCurrentConsensusTopicPrefersFinalThenDraft(t *testing.T) {
	room := &DebateRoom{
		Question:        "原始议题",
		TopicDraft:      "增强议题草案",
		RefinedQuestion: "用户最终议题",
	}
	if got := currentConsensusTopic(room); got != "用户最终议题" {
		t.Fatalf("final topic should be preferred, got=%q", got)
	}
	room.RefinedQuestion = ""
	if got := currentConsensusTopic(room); got != "增强议题草案" {
		t.Fatalf("topic draft should be second preferred, got=%q", got)
	}
	room.TopicDraft = ""
	if got := currentConsensusTopic(room); got != "原始议题" {
		t.Fatalf("original topic should be fallback, got=%q", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
