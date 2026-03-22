package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DebateStatusCreated    = "created"
	DebateStatusRunning    = "running"
	DebateStatusSummarize  = "summarizing"
	DebateStatusCompleted  = "completed"
	DebateStatusStopped    = "stopped"
	DebateStatusFailed     = "failed"
	DebateStatusWaiting    = "waiting_input"
	DefaultDebatePreset    = "tianji-five"
	DefaultSpeakingPolicy  = "host-decide"
	DefaultDebateMaxRounds = 3
	DefaultDebateMode      = DebateModeClassic
	MaxDebateMaxRounds     = 8
)

const (
	DebateModeClassic   = "classic"
	DebateModeConsensus = "consensus"
)

type DebateRole struct {
	Role        string `json:"role"`
	DisplayName string `json:"display_name"`
	Instance    string `json:"instance"`
	Project     string `json:"project,omitempty"`
	SocketPath  string `json:"socket_path,omitempty"`
	SpeakMode   string `json:"speak_mode,omitempty"`
}

type DebateRoom struct {
	RoomID                string       `json:"room_id"`
	Status                string       `json:"status"`
	CreatedAt             time.Time    `json:"created_at"`
	UpdatedAt             time.Time    `json:"updated_at"`
	OwnerSessionKey       string       `json:"owner_session_key"`
	GroupChatID           string       `json:"group_chat_id,omitempty"`
	Question              string       `json:"question"`
	TopicDraft            string       `json:"topic_draft,omitempty"`
	RefinedQuestion       string       `json:"refined_question,omitempty"`
	Preset                string       `json:"preset"`
	MaxRounds             int          `json:"max_rounds"`
	CurrentRound          int          `json:"current_round"`
	SpeakingPolicy        string       `json:"speaking_policy"`
	Mode                  string       `json:"mode,omitempty"`
	Phase                 string       `json:"phase,omitempty"`
	Iteration             int          `json:"iteration,omitempty"`
	HostRole              string       `json:"host_role,omitempty"`
	RequestedParticipants []string     `json:"requested_participants,omitempty"`
	ConfirmedParticipants []string     `json:"confirmed_participants,omitempty"`
	UserReviewStatus      string       `json:"user_review_status,omitempty"`
	UserReviewFeedback    string       `json:"user_review_feedback,omitempty"`
	Roles                 []DebateRole `json:"roles"`
	StopReason            string       `json:"stop_reason,omitempty"`
}

type DebateTranscriptEntry struct {
	Round     int       `json:"round"`
	Speaker   string    `json:"speaker"`
	Role      string    `json:"role"`
	PostedBy  string    `json:"posted_by"`
	Content   string    `json:"content"`
	LatencyMS int64     `json:"latency_ms,omitempty"`
	At        time.Time `json:"at"`
}

type DebateStartOptions struct {
	Preset         string
	MaxRounds      int
	SpeakingPolicy string
	Mode           string
	HostRole       string
	Participants   []string
	Question       string
}

type DebateStore struct {
	rootDir string
	mu      sync.Mutex
}

func NewDebateStore(rootDir string) *DebateStore {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return nil
	}
	return &DebateStore{rootDir: rootDir}
}

func (s *DebateStore) Enabled() bool {
	return s != nil && strings.TrimSpace(s.rootDir) != ""
}

func (s *DebateStore) RootDir() string {
	if s == nil {
		return ""
	}
	return s.rootDir
}

func (s *DebateStore) SaveRoom(room *DebateRoom) error {
	if !s.Enabled() {
		return fmt.Errorf("debate store is disabled")
	}
	if room == nil {
		return fmt.Errorf("room is nil")
	}
	if strings.TrimSpace(room.RoomID) == "" {
		return fmt.Errorf("room_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if room.CreatedAt.IsZero() {
		room.CreatedAt = time.Now()
	}
	room.UpdatedAt = time.Now()

	if err := os.MkdirAll(s.roomsDir(), 0o755); err != nil {
		return fmt.Errorf("create rooms dir: %w", err)
	}

	b, err := json.MarshalIndent(room, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal room: %w", err)
	}
	if err := os.WriteFile(s.roomPath(room.RoomID), b, 0o644); err != nil {
		return fmt.Errorf("write room: %w", err)
	}
	return nil
}

func (s *DebateStore) GetRoom(roomID string) (*DebateRoom, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("debate store is disabled")
	}
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return nil, fmt.Errorf("room_id is required")
	}

	b, err := os.ReadFile(s.roomPath(roomID))
	if err != nil {
		return nil, err
	}
	var room DebateRoom
	if err := json.Unmarshal(b, &room); err != nil {
		return nil, fmt.Errorf("decode room: %w", err)
	}
	return &room, nil
}

func (s *DebateStore) ListRooms() ([]*DebateRoom, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("debate store is disabled")
	}
	entries, err := os.ReadDir(s.roomsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []*DebateRoom{}, nil
		}
		return nil, err
	}

	rooms := make([]*DebateRoom, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.roomsDir(), e.Name()))
		if err != nil {
			continue
		}
		var room DebateRoom
		if err := json.Unmarshal(b, &room); err != nil {
			continue
		}
		rooms = append(rooms, &room)
	}

	sort.Slice(rooms, func(i, j int) bool {
		// Newest first.
		return rooms[i].CreatedAt.After(rooms[j].CreatedAt)
	})
	return rooms, nil
}

func (s *DebateStore) AppendTranscript(roomID string, item DebateTranscriptEntry) error {
	if !s.Enabled() {
		return fmt.Errorf("debate store is disabled")
	}
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return fmt.Errorf("room_id is required")
	}
	item.At = time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.transcriptsDir(), 0o755); err != nil {
		return fmt.Errorf("create transcripts dir: %w", err)
	}
	f, err := os.OpenFile(s.transcriptPath(roomID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	b, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("marshal transcript: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write transcript: %w", err)
	}
	return nil
}

func (s *DebateStore) LoadTranscript(roomID string) ([]DebateTranscriptEntry, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("debate store is disabled")
	}
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return nil, fmt.Errorf("room_id is required")
	}
	b, err := os.ReadFile(s.transcriptPath(roomID))
	if err != nil {
		if os.IsNotExist(err) {
			return []DebateTranscriptEntry{}, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	out := make([]DebateTranscriptEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item DebateTranscriptEntry
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *DebateStore) roomsDir() string {
	return filepath.Join(s.rootDir, "rooms")
}

func (s *DebateStore) transcriptsDir() string {
	return filepath.Join(s.rootDir, "transcripts")
}

func (s *DebateStore) roomPath(roomID string) string {
	return filepath.Join(s.roomsDir(), roomID+".json")
}

func (s *DebateStore) transcriptPath(roomID string) string {
	return filepath.Join(s.transcriptsDir(), roomID+".jsonl")
}

func (s *DebateStore) reportsDir() string {
	return filepath.Join(s.rootDir, "reports")
}

func (s *DebateStore) reportPath(roomID string) string {
	return filepath.Join(s.reportsDir(), roomID+"-final.md")
}

func (s *DebateStore) SaveFinalReport(roomID, content string) (string, error) {
	if !s.Enabled() {
		return "", fmt.Errorf("debate store is disabled")
	}
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return "", fmt.Errorf("room_id is required")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("report content is empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.reportsDir(), 0o755); err != nil {
		return "", fmt.Errorf("create reports dir: %w", err)
	}
	path := s.reportPath(roomID)
	if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write report: %w", err)
	}
	return path, nil
}

func defaultDebateRoles() []DebateRole {
	// Preferred mapping for this branch/user setup:
	// D:/ai-github/cc-connect/mutilbot/instance-mutilbot{1..5}
	//
	// If mutilbot directories are not present, fallback to legacy instance-a~e.
	if repoRoot := inferRepoRootFromWorkingDir(); repoRoot != "" {
		mutilbotRoot := filepath.Join(repoRoot, "mutilbot")
		if fi, err := os.Stat(mutilbotRoot); err == nil && fi.IsDir() {
			return []DebateRole{
				{
					Role:        "jarvis",
					DisplayName: "Jarvis",
					Instance:    "instance-mutilbot1",
					Project:     "instance-mutilbot1-project-jarvis",
					SocketPath:  filepath.Join(mutilbotRoot, "instance-mutilbot1", "data", "run", "api.sock"),
					SpeakMode:   "self",
				},
				{
					Role:        "jianzhu",
					DisplayName: "剑主",
					Instance:    "instance-mutilbot2",
					Project:     "instance-mutilbot2-project-jianzhu",
					SocketPath:  filepath.Join(mutilbotRoot, "instance-mutilbot2", "data", "run", "api.sock"),
					SpeakMode:   "self",
				},
				{
					Role:        "wendan",
					DisplayName: "文胆",
					Instance:    "instance-mutilbot3",
					Project:     "instance-mutilbot3-project-wendan",
					SocketPath:  filepath.Join(mutilbotRoot, "instance-mutilbot3", "data", "run", "api.sock"),
					SpeakMode:   "self",
				},
				{
					Role:        "xingzou",
					DisplayName: "行走",
					Instance:    "instance-mutilbot4",
					Project:     "instance-mutilbot4-project-xingzou",
					SocketPath:  filepath.Join(mutilbotRoot, "instance-mutilbot4", "data", "run", "api.sock"),
					SpeakMode:   "self",
				},
				{
					Role:        "zhanggui",
					DisplayName: "掌柜",
					Instance:    "instance-mutilbot5",
					Project:     "instance-mutilbot5-project-zhanggui",
					SocketPath:  filepath.Join(mutilbotRoot, "instance-mutilbot5", "data", "run", "api.sock"),
					SpeakMode:   "self",
				},
			}
		}
	}

	return []DebateRole{
		{Role: "jarvis", DisplayName: "Jarvis", Instance: "instance-a", Project: "instance-a", SpeakMode: "self"},
		{Role: "jianzhu", DisplayName: "剑主", Instance: "instance-b", Project: "instance-b", SpeakMode: "self"},
		{Role: "wendan", DisplayName: "文胆", Instance: "instance-c", Project: "instance-c", SpeakMode: "self"},
		{Role: "xingzou", DisplayName: "行走", Instance: "instance-d", Project: "instance-d", SpeakMode: "self"},
		{Role: "zhanggui", DisplayName: "掌柜", Instance: "instance-e", Project: "instance-e", SpeakMode: "self"},
	}
}

func NewDebateRoom(ownerSessionKey string, opts DebateStartOptions, now time.Time) *DebateRoom {
	normalized := NormalizeDebateStartOptions(opts)
	roles := defaultDebateRoles()
	hostRole := resolveDebateRoleKey(roles, normalized.HostRole)
	if hostRole == "" {
		hostRole = resolveDebateRoleKey(roles, "jarvis")
	}
	if hostRole == "" && len(roles) > 0 {
		hostRole = roles[0].Role
	}
	participants := normalizeDebateRoleList(roles, normalized.Participants, hostRole)
	return &DebateRoom{
		RoomID:                newDebateRoomID(now),
		Status:                DebateStatusRunning,
		CreatedAt:             now,
		UpdatedAt:             now,
		OwnerSessionKey:       ownerSessionKey,
		GroupChatID:           extractGroupChatID(ownerSessionKey),
		Question:              strings.TrimSpace(normalized.Question),
		Preset:                normalized.Preset,
		MaxRounds:             normalized.MaxRounds,
		CurrentRound:          0,
		SpeakingPolicy:        normalized.SpeakingPolicy,
		Mode:                  normalized.Mode,
		Phase:                 "init",
		Iteration:             0,
		HostRole:              hostRole,
		RequestedParticipants: cloneStringSlice(participants),
		ConfirmedParticipants: nil,
		UserReviewStatus:      "pending",
		Roles:                 roles,
	}
}

func NormalizeDebateStartOptions(in DebateStartOptions) DebateStartOptions {
	out := in
	out.Preset = strings.TrimSpace(out.Preset)
	if out.Preset == "" {
		out.Preset = DefaultDebatePreset
	}
	out.SpeakingPolicy = strings.TrimSpace(out.SpeakingPolicy)
	if out.SpeakingPolicy == "" {
		out.SpeakingPolicy = DefaultSpeakingPolicy
	}
	out.Mode = strings.ToLower(strings.TrimSpace(out.Mode))
	if out.Mode == "" {
		out.Mode = DefaultDebateMode
	}
	if out.Mode != DebateModeClassic && out.Mode != DebateModeConsensus {
		out.Mode = DefaultDebateMode
	}
	if out.MaxRounds <= 0 {
		out.MaxRounds = DefaultDebateMaxRounds
	}
	if out.MaxRounds > MaxDebateMaxRounds {
		out.MaxRounds = MaxDebateMaxRounds
	}
	out.HostRole = normalizeRoleToken(out.HostRole)
	out.Participants = normalizeRoleTokens(out.Participants)
	out.Question = normalizeDebateQuestion(out.Question)
	return out
}

func ValidateDebateStartOptions(in DebateStartOptions) error {
	if strings.TrimSpace(in.Question) == "" {
		return fmt.Errorf("question is required")
	}
	if in.MaxRounds < 1 || in.MaxRounds > MaxDebateMaxRounds {
		return fmt.Errorf("max_rounds must be in [1,%d]", MaxDebateMaxRounds)
	}
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = DefaultDebateMode
	}
	if mode != DebateModeClassic && mode != DebateModeConsensus {
		return fmt.Errorf("mode must be one of [%s,%s]", DebateModeClassic, DebateModeConsensus)
	}
	if strings.TrimSpace(in.HostRole) != "" && normalizeRoleToken(in.HostRole) == "" {
		return fmt.Errorf("host_role is invalid")
	}
	return nil
}

func inferDebateStoreRoot(sessionStorePath, projectName string) string {
	if strings.TrimSpace(sessionStorePath) == "" {
		return ""
	}
	dir := filepath.Dir(sessionStorePath)
	if filepath.Base(dir) == "sessions" {
		return filepath.Join(filepath.Dir(dir), "discussion", projectName)
	}
	return filepath.Join(dir, "discussion", projectName)
}

func newDebateRoomID(now time.Time) string {
	// Example: debate_20260317_152601_123456
	return fmt.Sprintf("debate_%s_%06d", now.Format("20060102_150405"), now.Nanosecond()%1_000_000)
}

func extractGroupChatID(sessionKey string) string {
	parts := strings.SplitN(strings.TrimSpace(sessionKey), ":", 3)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

var debateAtTagPattern = regexp.MustCompile(`(?is)<at\b[^>]*>.*?</at>`)

func normalizeDebateQuestion(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// Remove Feishu <at ...>...</at> mentions if they appear in text payload.
	s = debateAtTagPattern.ReplaceAllString(s, " ")

	fields := strings.Fields(s)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if isMentionToken(f) {
			continue
		}
		out = append(out, f)
	}
	return strings.TrimSpace(strings.Join(out, " "))
}

func isMentionToken(token string) bool {
	t := strings.TrimSpace(token)
	if t == "" {
		return false
	}
	t = strings.Trim(t, "，,。.!?;；:：()[]{}<>《》\"'`“”‘’")
	if len(t) <= 1 {
		return false
	}
	return strings.HasPrefix(t, "@")
}

func normalizeRoleToken(raw string) string {
	v := strings.TrimSpace(strings.ToLower(raw))
	v = strings.TrimPrefix(v, "@")
	v = strings.Trim(v, "，,。.!?;；:：()[]{}<>《》\"'`“”‘’")
	return strings.TrimSpace(v)
}

func normalizeRoleTokens(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		token := normalizeRoleToken(raw)
		if token == "" || seen[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	return out
}

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeDebateRoleList(roles []DebateRole, raw []string, hostRole string) []string {
	if len(raw) == 0 {
		return nil
	}
	hostRole = normalizeRoleToken(hostRole)
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, item := range raw {
		roleKey := resolveDebateRoleKey(roles, item)
		if roleKey == "" {
			roleKey = normalizeRoleToken(item)
		}
		if roleKey == "" || roleKey == hostRole || seen[roleKey] {
			continue
		}
		seen[roleKey] = true
		out = append(out, roleKey)
	}
	return out
}

func resolveDebateRoleKey(roles []DebateRole, raw string) string {
	target := normalizeRoleToken(raw)
	if target == "" {
		return ""
	}
	for _, role := range roles {
		if normalizeRoleToken(role.Role) == target {
			return role.Role
		}
	}
	for _, role := range roles {
		if normalizeRoleToken(role.DisplayName) == target {
			return role.Role
		}
	}
	for _, role := range roles {
		if normalizeRoleToken(role.Instance) == target {
			return role.Role
		}
		if normalizeRoleToken(role.Project) == target {
			return role.Role
		}
	}
	return ""
}
