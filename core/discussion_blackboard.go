package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DebateRoleNote stores the latest structured stance of one role.
type DebateRoleNote struct {
	Role         string    `json:"role"`
	DisplayName  string    `json:"display_name"`
	LatestRound  int       `json:"latest_round"`
	LatestStance string    `json:"latest_stance,omitempty"`
	LatestBasis  string    `json:"latest_basis,omitempty"`
	LatestRisk   string    `json:"latest_risk,omitempty"`
	LatestAction string    `json:"latest_action,omitempty"`
	LastMessage  string    `json:"last_message,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// DebateBlackboard is the shared context board for one debate room.
// Purpose:
// 1) keep discussion goal and constraints as single source of truth;
// 2) accumulate each role's latest structured points;
// 3) provide a stable context snapshot for the next speaker prompt.
type DebateBlackboard struct {
	RoomID          string                    `json:"room_id"`
	Topic           string                    `json:"topic"`
	Goal            string                    `json:"goal"`
	Constraints     []string                  `json:"constraints,omitempty"`
	ExpectedOutput  []string                  `json:"expected_output,omitempty"`
	Round           int                       `json:"round"`
	MaxRounds       int                       `json:"max_rounds"`
	RoundPlan       string                    `json:"round_plan,omitempty"`
	RoundFocus      string                    `json:"round_focus,omitempty"`
	OpenQuestions   []string                  `json:"open_questions,omitempty"`
	HistoryDigest   []string                  `json:"history_digest,omitempty"`
	RoleNotes       map[string]DebateRoleNote `json:"role_notes,omitempty"`
	Revision        int                       `json:"revision"`
	CreatedAt       time.Time                 `json:"created_at"`
	UpdatedAt       time.Time                 `json:"updated_at"`
	LastContributor string                    `json:"last_contributor,omitempty"`
}

// RoleContribution is parsed from one role's reply and written back to the blackboard.
type RoleContribution struct {
	RoomID       string
	Role         string
	Round        int
	BaseRevision int
	Stance       string
	Basis        string
	Risk         string
	Action       string
	Summary      []string
	DisplayReply string
}

func (s *DebateStore) blackboardsDir() string {
	return filepath.Join(s.rootDir, "blackboards")
}

func (s *DebateStore) blackboardPath(roomID string) string {
	return filepath.Join(s.blackboardsDir(), roomID+".json")
}

func (s *DebateStore) BlackboardFilePath(roomID string) string {
	if !s.Enabled() {
		return ""
	}
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return ""
	}
	return s.blackboardPath(roomID)
}

func (s *DebateStore) SaveBlackboard(board *DebateBlackboard) error {
	if !s.Enabled() {
		return fmt.Errorf("debate store is disabled")
	}
	if board == nil {
		return fmt.Errorf("blackboard is nil")
	}
	board.RoomID = strings.TrimSpace(board.RoomID)
	if board.RoomID == "" {
		return fmt.Errorf("room_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if board.CreatedAt.IsZero() {
		board.CreatedAt = time.Now()
	}
	board.UpdatedAt = time.Now()
	if board.RoleNotes == nil {
		board.RoleNotes = map[string]DebateRoleNote{}
	}
	if board.Revision <= 0 {
		board.Revision = 1
	}

	if err := os.MkdirAll(s.blackboardsDir(), 0o755); err != nil {
		return fmt.Errorf("create blackboards dir: %w", err)
	}

	b, err := json.MarshalIndent(board, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal blackboard: %w", err)
	}
	if err := os.WriteFile(s.blackboardPath(board.RoomID), b, 0o644); err != nil {
		return fmt.Errorf("write blackboard: %w", err)
	}
	return nil
}

func (s *DebateStore) LoadBlackboard(roomID string) (*DebateBlackboard, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("debate store is disabled")
	}
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return nil, fmt.Errorf("room_id is required")
	}

	b, err := os.ReadFile(s.blackboardPath(roomID))
	if err != nil {
		return nil, err
	}
	var board DebateBlackboard
	if err := json.Unmarshal(b, &board); err != nil {
		return nil, fmt.Errorf("decode blackboard: %w", err)
	}
	if board.RoleNotes == nil {
		board.RoleNotes = map[string]DebateRoleNote{}
	}
	return &board, nil
}

func (s *DebateStore) LoadOrInitBlackboard(room *DebateRoom) (*DebateBlackboard, error) {
	if room == nil {
		return nil, fmt.Errorf("room is nil")
	}
	if b, err := s.LoadBlackboard(room.RoomID); err == nil {
		return b, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	now := time.Now()
	board := &DebateBlackboard{
		RoomID:      room.RoomID,
		Topic:       strings.TrimSpace(room.Question),
		Goal:        "围绕讨论主题形成可执行结论与行动项（含责任人和验收标准）。",
		Constraints: []string{"多角色协作", "结论可执行", "建议可验证"},
		ExpectedOutput: []string{
			"关键结论（3条内）",
			"主要风险（3条内）",
			"行动项（owner + 验收标准）",
		},
		Round:         room.CurrentRound,
		MaxRounds:     room.MaxRounds,
		RoundPlan:     defaultRoundPlan(room.CurrentRound, room.MaxRounds),
		RoundFocus:    defaultRoundFocus(room.CurrentRound, room.MaxRounds),
		OpenQuestions: []string{},
		HistoryDigest: []string{},
		RoleNotes:     map[string]DebateRoleNote{},
		Revision:      1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.SaveBlackboard(board); err != nil {
		return nil, err
	}
	return board, nil
}

func UpdateBlackboardRound(board *DebateBlackboard, round, maxRounds int) {
	if board == nil {
		return
	}
	board.Round = round
	if maxRounds > 0 {
		board.MaxRounds = maxRounds
	}
	board.RoundPlan = defaultRoundPlan(round, board.MaxRounds)
	board.RoundFocus = defaultRoundFocus(round, board.MaxRounds)
	board.UpdatedAt = time.Now()
}

func ApplyRoleContribution(board *DebateBlackboard, role DebateRole, round int, reply string) RoleContribution {
	if board == nil {
		return RoleContribution{}
	}
	if board.RoleNotes == nil {
		board.RoleNotes = map[string]DebateRoleNote{}
	}

	contrib := ExtractRoleContribution(reply)
	roleKey := strings.TrimSpace(role.Role)
	if roleKey == "" {
		roleKey = "unknown"
	}
	note := board.RoleNotes[roleKey]
	note.Role = roleKey
	note.DisplayName = emptyAs(strings.TrimSpace(role.DisplayName), roleKey)
	note.LatestRound = round
	note.UpdatedAt = time.Now()

	displayReply := strings.TrimSpace(contrib.DisplayReply)
	if displayReply == "" {
		displayReply = strings.TrimSpace(reply)
	}
	fallback := truncateStr(displayReply, 180)
	note.LatestStance = emptyAs(strings.TrimSpace(contrib.Stance), emptyAs(note.LatestStance, fallback))
	if strings.TrimSpace(contrib.Basis) != "" {
		note.LatestBasis = strings.TrimSpace(contrib.Basis)
	}
	if strings.TrimSpace(contrib.Risk) != "" {
		note.LatestRisk = strings.TrimSpace(contrib.Risk)
	}
	if strings.TrimSpace(contrib.Action) != "" {
		note.LatestAction = strings.TrimSpace(contrib.Action)
	}
	note.LastMessage = fallback
	board.RoleNotes[roleKey] = note

	board.LastContributor = roleKey
	board.HistoryDigest = append(board.HistoryDigest, fmt.Sprintf("R%d [%s] %s", round, roleKey, truncateStr(note.LatestStance, 90)))
	if len(board.HistoryDigest) > 24 {
		board.HistoryDigest = board.HistoryDigest[len(board.HistoryDigest)-24:]
	}

	// Objective fields (topic/goal/round_focus/open_questions) are host-controlled.
	// Worker replies only update contribution fields to avoid objective contamination.
	board.Revision++
	board.UpdatedAt = time.Now()
	return contrib
}

func defaultRoundPlan(round, maxRounds int) string {
	if round <= 1 {
		return "对齐讨论目标、边界、术语和衡量标准。"
	}
	if maxRounds > 0 && round >= maxRounds {
		return "收敛最终结论与行动项（owner + deadline + 验收标准）。"
	}
	return "补充证据、对比方案并收敛可执行动作。"
}

func defaultRoundFocus(round, maxRounds int) string {
	if round <= 1 {
		return "先对齐讨论目标、边界和衡量标准。"
	}
	if maxRounds > 0 && round >= maxRounds {
		return "收敛为最终结论与可执行行动项（owner + 验收标准）。"
	}
	return "基于已有观点补充证据，并给出可执行动作。"
}

func mergeUniqueQuestions(base, incoming []string, max int) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(base)+len(incoming))
	for _, q := range base {
		q = strings.TrimSpace(q)
		if q == "" || seen[q] {
			continue
		}
		seen[q] = true
		out = append(out, q)
	}
	for _, q := range incoming {
		q = strings.TrimSpace(q)
		if q == "" || seen[q] {
			continue
		}
		seen[q] = true
		out = append(out, q)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

var questionPattern = regexp.MustCompile(`[^\n\r。！？!?]{4,120}[？?]`)

func extractQuestions(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	raw := questionPattern.FindAllString(text, -1)
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, q := range raw {
		q = strings.TrimSpace(q)
		q = strings.TrimLeft(q, "-•*0123456789.、) ）")
		if q == "" || seen[q] {
			continue
		}
		seen[q] = true
		out = append(out, q)
	}
	return out
}
