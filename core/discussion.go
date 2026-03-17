package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	DefaultDebatePreset    = "tianji-five"
	DefaultSpeakingPolicy  = "host-decide"
	DefaultDebateMaxRounds = 3
	MaxDebateMaxRounds     = 8
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
	RoomID          string       `json:"room_id"`
	Status          string       `json:"status"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
	OwnerSessionKey string       `json:"owner_session_key"`
	GroupChatID     string       `json:"group_chat_id,omitempty"`
	Question        string       `json:"question"`
	Preset          string       `json:"preset"`
	MaxRounds       int          `json:"max_rounds"`
	CurrentRound    int          `json:"current_round"`
	SpeakingPolicy  string       `json:"speaking_policy"`
	Roles           []DebateRole `json:"roles"`
	StopReason      string       `json:"stop_reason,omitempty"`
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

func defaultDebateRoles() []DebateRole {
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
	return &DebateRoom{
		RoomID:          newDebateRoomID(now),
		Status:          DebateStatusRunning,
		CreatedAt:       now,
		UpdatedAt:       now,
		OwnerSessionKey: ownerSessionKey,
		GroupChatID:     extractGroupChatID(ownerSessionKey),
		Question:        strings.TrimSpace(normalized.Question),
		Preset:          normalized.Preset,
		MaxRounds:       normalized.MaxRounds,
		CurrentRound:    0,
		SpeakingPolicy:  normalized.SpeakingPolicy,
		Roles:           defaultDebateRoles(),
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
	if out.MaxRounds <= 0 {
		out.MaxRounds = DefaultDebateMaxRounds
	}
	if out.MaxRounds > MaxDebateMaxRounds {
		out.MaxRounds = MaxDebateMaxRounds
	}
	out.Question = strings.TrimSpace(out.Question)
	return out
}

func ValidateDebateStartOptions(in DebateStartOptions) error {
	if strings.TrimSpace(in.Question) == "" {
		return fmt.Errorf("question is required")
	}
	if in.MaxRounds < 1 || in.MaxRounds > MaxDebateMaxRounds {
		return fmt.Errorf("max_rounds must be in [1,%d]", MaxDebateMaxRounds)
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
