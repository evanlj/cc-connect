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
	SquadStatusCreated   = "created"
	SquadStatusRunning   = "running"
	SquadStatusWaiting   = "waiting_input"
	SquadStatusCompleted = "completed"
	SquadStatusStopped   = "stopped"
	SquadStatusFailed    = "failed"
)

const (
	SquadPhaseBooting         = "role_booting"
	SquadPhasePlanning        = "planning"
	SquadPhaseWaitPlanApprove = "wait_plan_approval"
	SquadPhaseWaitTaskApprove = "wait_task_approval"
	SquadPhaseWaitReviewJudge = "wait_review_judge"
	SquadPhaseExecuting       = "executing_task"
	SquadPhaseReviewing       = "reviewing_task"
	SquadPhaseFinalizing      = "finalizing"
	SquadPhaseCompleted       = "completed"
	SquadPhaseStopped         = "stopped"
	SquadPhaseFailed          = "failed"
)

const (
	DefaultSquadPlannerRole       = "jarvis"
	DefaultSquadExecutorRole      = "xingzou"
	DefaultSquadReviewerRole      = "jianzhu"
	DefaultSquadMaxRework         = 3
	DefaultSquadPlannerTimeoutSec = 3600
	DefaultSquadRoleTimeoutSec    = 3600
)

type SquadStartOptions struct {
	RepoPath          string
	TaskPrompt        string
	PlannerRole       string
	ExecutorRole      string
	ReviewerRole      string
	Provider          string
	PlannerTimeoutSec int
}

type SquadTask struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Objective   string   `json:"objective,omitempty"`
	Acceptance  string   `json:"acceptance,omitempty"`
	TestCommand string   `json:"test_command,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
}

type SquadPlan struct {
	Title    string      `json:"title,omitempty"`
	Overview string      `json:"overview,omitempty"`
	Tasks    []SquadTask `json:"tasks,omitempty"`
	RawReply string      `json:"raw_reply,omitempty"`
}

type SquadCheckpoint struct {
	TaskID        string    `json:"task_id"`
	TaskTitle     string    `json:"task_title,omitempty"`
	Round         int       `json:"round"`
	ExecutorReply string    `json:"executor_reply,omitempty"`
	ReviewerReply string    `json:"reviewer_reply,omitempty"`
	ReviewResult  string    `json:"review_result,omitempty"`
	Verdict       string    `json:"verdict,omitempty"` // PASS | REWORK
	Blockers      []string  `json:"blockers,omitempty"`
	Suggestions   []string  `json:"suggestions,omitempty"`
	ChangedFiles  []string  `json:"changed_files,omitempty"`
	TestResult    string    `json:"test_result,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type SquadRoleRuntime struct {
	Role           string `json:"role"`
	DisplayName    string `json:"display_name,omitempty"`
	TemplateConfig string `json:"template_config,omitempty"`
	RuntimeConfig  string `json:"runtime_config,omitempty"`
	DataDir        string `json:"data_dir,omitempty"`
	SocketPath     string `json:"socket_path,omitempty"`
	ProjectName    string `json:"project_name,omitempty"`
	PID            int    `json:"pid,omitempty"`
}

type SquadRun struct {
	RunID              string                      `json:"run_id"`
	Status             string                      `json:"status"`
	Phase              string                      `json:"phase"`
	CreatedAt          time.Time                   `json:"created_at"`
	UpdatedAt          time.Time                   `json:"updated_at"`
	OwnerSessionKey    string                      `json:"owner_session_key"`
	RepoPath           string                      `json:"repo_path"`
	TaskPrompt         string                      `json:"task_prompt"`
	Provider           string                      `json:"provider,omitempty"`
	PlannerTimeoutSec  int                         `json:"planner_timeout_sec,omitempty"`
	PlannerRole        string                      `json:"planner_role"`
	ExecutorRole       string                      `json:"executor_role"`
	ReviewerRole       string                      `json:"reviewer_role"`
	PlanApproved       bool                        `json:"plan_approved"`
	CurrentTask        int                         `json:"current_task"`
	CurrentRound       int                         `json:"current_round"`
	ReworkCount        int                         `json:"rework_count"`
	MaxRework          int                         `json:"max_rework"`
	TaskApprovedID     string                      `json:"task_approved_id,omitempty"`
	TaskPendingID      string                      `json:"task_pending_id,omitempty"`
	ReviewPendingTask  string                      `json:"review_pending_task,omitempty"`
	ReviewPendingRound int                         `json:"review_pending_round,omitempty"`
	ReviewPendingCP    string                      `json:"review_pending_checkpoint,omitempty"`
	UserReworkNote     string                      `json:"user_rework_note,omitempty"`
	StopReason         string                      `json:"stop_reason,omitempty"`
	ErrorMessage       string                      `json:"error_message,omitempty"`
	Plan               SquadPlan                   `json:"plan,omitempty"`
	RoleRuntime        map[string]SquadRoleRuntime `json:"role_runtime,omitempty"`
}

type squadEvent struct {
	At      time.Time      `json:"at"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

type SquadStore struct {
	rootDir string
	mu      sync.Mutex
}

func NewSquadStore(rootDir string) *SquadStore {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return nil
	}
	return &SquadStore{rootDir: rootDir}
}

func (s *SquadStore) Enabled() bool {
	return s != nil && strings.TrimSpace(s.rootDir) != ""
}

func (s *SquadStore) RootDir() string {
	if s == nil {
		return ""
	}
	return s.rootDir
}

func (s *SquadStore) SaveRun(run *SquadRun) error {
	if !s.Enabled() {
		return fmt.Errorf("squad store is disabled")
	}
	if run == nil {
		return fmt.Errorf("run is nil")
	}
	run.RunID = strings.TrimSpace(run.RunID)
	if run.RunID == "" {
		return fmt.Errorf("run_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now()
	}
	run.UpdatedAt = time.Now()
	if run.MaxRework <= 0 {
		run.MaxRework = DefaultSquadMaxRework
	}
	if run.PlannerTimeoutSec <= 0 {
		run.PlannerTimeoutSec = DefaultSquadPlannerTimeoutSec
	}

	if err := os.MkdirAll(s.runsDir(), 0o755); err != nil {
		return fmt.Errorf("create runs dir: %w", err)
	}
	b, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run: %w", err)
	}
	if err := os.WriteFile(s.runPath(run.RunID), b, 0o644); err != nil {
		return fmt.Errorf("write run: %w", err)
	}
	return nil
}

func (s *SquadStore) GetRun(runID string) (*SquadRun, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("squad store is disabled")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("run_id is required")
	}
	b, err := os.ReadFile(s.runPath(runID))
	if err != nil {
		return nil, err
	}
	var run SquadRun
	if err := json.Unmarshal(b, &run); err != nil {
		return nil, fmt.Errorf("decode run: %w", err)
	}
	if run.MaxRework <= 0 {
		run.MaxRework = DefaultSquadMaxRework
	}
	if run.PlannerTimeoutSec <= 0 {
		run.PlannerTimeoutSec = DefaultSquadPlannerTimeoutSec
	}
	if run.RoleRuntime == nil {
		run.RoleRuntime = map[string]SquadRoleRuntime{}
	}
	return &run, nil
}

func (s *SquadStore) ListRuns() ([]*SquadRun, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("squad store is disabled")
	}
	entries, err := os.ReadDir(s.runsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []*SquadRun{}, nil
		}
		return nil, err
	}
	runs := make([]*SquadRun, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.runsDir(), e.Name()))
		if err != nil {
			continue
		}
		var run SquadRun
		if err := json.Unmarshal(b, &run); err != nil {
			continue
		}
		runs = append(runs, &run)
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
	return runs, nil
}

// LatestRunIDForOwner returns the run_id of the squad run most recently updated
// among those owned by ownerSessionKey (e.g. feishu:chat:user), or empty if none.
func (s *SquadStore) LatestRunIDForOwner(ownerSessionKey string) (string, error) {
	if s == nil || !s.Enabled() {
		return "", nil
	}
	ownerSessionKey = strings.TrimSpace(ownerSessionKey)
	if ownerSessionKey == "" {
		return "", nil
	}
	runs, err := s.ListRuns()
	if err != nil {
		return "", err
	}
	var best *SquadRun
	for _, r := range runs {
		if r == nil {
			continue
		}
		if strings.TrimSpace(r.OwnerSessionKey) != ownerSessionKey {
			continue
		}
		if best == nil || r.UpdatedAt.After(best.UpdatedAt) {
			best = r
		}
	}
	if best == nil {
		return "", nil
	}
	return strings.TrimSpace(best.RunID), nil
}

var squadLatestRunIDForOwner func(ownerSessionKey string) string

// SetSquadLatestRunIDForOwnerLookup registers a resolver used by integrations (e.g. Feishu)
// to pre-fill the latest squad run for a session. Pass nil to disable.
func SetSquadLatestRunIDForOwnerLookup(fn func(ownerSessionKey string) string) {
	squadLatestRunIDForOwner = fn
}

// SquadLatestRunIDForOwnerSession returns the latest squad run_id for the given session key,
// or empty when no lookup is registered or no run exists.
func SquadLatestRunIDForOwnerSession(ownerSessionKey string) string {
	if squadLatestRunIDForOwner == nil {
		return ""
	}
	return strings.TrimSpace(squadLatestRunIDForOwner(ownerSessionKey))
}

func (s *SquadStore) AppendEvent(runID, level, message string, fields map[string]any) error {
	if !s.Enabled() {
		return fmt.Errorf("squad store is disabled")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return fmt.Errorf("run_id is required")
	}
	level = strings.TrimSpace(strings.ToLower(level))
	if level == "" {
		level = "info"
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("event message is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.runDir(runID), 0o755); err != nil {
		return fmt.Errorf("create run dir: %w", err)
	}
	f, err := os.OpenFile(s.eventsPath(runID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open events file: %w", err)
	}
	defer f.Close()

	item := squadEvent{
		At:      time.Now(),
		Level:   level,
		Message: message,
		Fields:  fields,
	}
	b, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	return nil
}

func (s *SquadStore) SavePlanMarkdown(runID, content string) (string, error) {
	if !s.Enabled() {
		return "", fmt.Errorf("squad store is disabled")
	}
	runID = strings.TrimSpace(runID)
	content = strings.TrimSpace(content)
	if runID == "" {
		return "", fmt.Errorf("run_id is required")
	}
	if content == "" {
		return "", fmt.Errorf("plan content is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.runDir(runID), 0o755); err != nil {
		return "", fmt.Errorf("create run dir: %w", err)
	}
	path := s.planPath(runID)
	if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write plan markdown: %w", err)
	}
	return path, nil
}

func (s *SquadStore) SaveFinalReport(runID, content string) (string, error) {
	if !s.Enabled() {
		return "", fmt.Errorf("squad store is disabled")
	}
	runID = strings.TrimSpace(runID)
	content = strings.TrimSpace(content)
	if runID == "" {
		return "", fmt.Errorf("run_id is required")
	}
	if content == "" {
		return "", fmt.Errorf("report content is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.runDir(runID), 0o755); err != nil {
		return "", fmt.Errorf("create run dir: %w", err)
	}
	path := s.reportPath(runID)
	if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write final report: %w", err)
	}
	return path, nil
}

func (s *SquadStore) SaveCheckpoint(runID string, cp *SquadCheckpoint) (string, error) {
	if !s.Enabled() {
		return "", fmt.Errorf("squad store is disabled")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return "", fmt.Errorf("run_id is required")
	}
	if cp == nil {
		return "", fmt.Errorf("checkpoint is nil")
	}
	cp.TaskID = strings.TrimSpace(cp.TaskID)
	if cp.TaskID == "" {
		return "", fmt.Errorf("checkpoint task_id is required")
	}
	if cp.Round <= 0 {
		cp.Round = 1
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.checkpointsDir(runID), 0o755); err != nil {
		return "", fmt.Errorf("create checkpoints dir: %w", err)
	}
	name := fmt.Sprintf("%s-round-%02d.json", sanitizeFileName(cp.TaskID), cp.Round)
	path := filepath.Join(s.checkpointsDir(runID), name)
	b, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal checkpoint: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", fmt.Errorf("write checkpoint: %w", err)
	}
	return path, nil
}

func (s *SquadStore) LoadCheckpoints(runID string) ([]SquadCheckpoint, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("squad store is disabled")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("run_id is required")
	}
	dir := s.checkpointsDir(runID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []SquadCheckpoint{}, nil
		}
		return nil, err
	}
	out := make([]SquadCheckpoint, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var item SquadCheckpoint
		if err := json.Unmarshal(b, &item); err != nil {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			if out[i].TaskID == out[j].TaskID {
				return out[i].Round < out[j].Round
			}
			return out[i].TaskID < out[j].TaskID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *SquadStore) SavePlanTasks(runID string, tasks []SquadTask) (string, error) {
	if !s.Enabled() {
		return "", fmt.Errorf("squad store is disabled")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return "", fmt.Errorf("run_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.runDir(runID), 0o755); err != nil {
		return "", fmt.Errorf("create run dir: %w", err)
	}
	path := s.planTasksPath(runID)
	b, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal plan tasks: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", fmt.Errorf("write plan tasks: %w", err)
	}
	return path, nil
}

func (s *SquadStore) RunDir(runID string) string {
	if !s.Enabled() {
		return ""
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return ""
	}
	return s.runDir(runID)
}

func (s *SquadStore) RuntimeDir(runID string) string {
	runDir := s.RunDir(runID)
	if runDir == "" {
		return ""
	}
	return filepath.Join(runDir, "runtime")
}

func (s *SquadStore) runsDir() string {
	return filepath.Join(s.rootDir, "runs")
}

func (s *SquadStore) runDir(runID string) string {
	return filepath.Join(s.rootDir, "runs", runID)
}

func (s *SquadStore) runPath(runID string) string {
	return filepath.Join(s.runsDir(), runID+".json")
}

func (s *SquadStore) eventsPath(runID string) string {
	return filepath.Join(s.runDir(runID), "events.jsonl")
}

func (s *SquadStore) planPath(runID string) string {
	return filepath.Join(s.runDir(runID), "plan.md")
}

func (s *SquadStore) planTasksPath(runID string) string {
	return filepath.Join(s.runDir(runID), "plan.tasks.json")
}

func (s *SquadStore) reportPath(runID string) string {
	return filepath.Join(s.runDir(runID), "final-report.md")
}

func (s *SquadStore) checkpointsDir(runID string) string {
	return filepath.Join(s.runDir(runID), "checkpoints")
}

func inferSquadStoreRoot(sessionStorePath, projectName string) string {
	if strings.TrimSpace(sessionStorePath) == "" {
		return ""
	}
	dir := filepath.Dir(sessionStorePath)
	if filepath.Base(dir) == "sessions" {
		return filepath.Join(filepath.Dir(dir), "squad", projectName)
	}
	return filepath.Join(dir, "squad", projectName)
}

func newSquadRunID(now time.Time) string {
	return fmt.Sprintf("squad_%s_%06d", now.Format("20060102_150405"), now.Nanosecond()%1_000_000)
}

func sanitizeFileName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "task"
	}
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
		" ", "_",
	)
	out := replacer.Replace(raw)
	out = strings.Trim(out, "._")
	if out == "" {
		return "task"
	}
	return out
}
