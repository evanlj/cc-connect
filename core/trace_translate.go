package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

// TraceTranslateCfg controls the built-in "watch Codex trace → translate → send to chat" loop.
//
// It is intentionally driven by Agent.Options (map[string]any) so we don't need to evolve the
// top-level config schema for small automation features.
type TraceTranslateCfg struct {
	Enabled bool

	// WatchPath can be a single *.jsonl trace file, or a directory.
	// If it's a directory, we scan recursively for *.jsonl and tail all "active" files.
	WatchPath string

	// FollowLatest makes the watcher follow only the newest trace file under WatchPath.
	// This is usually what you want if WatchPath points to the Codex trace root:
	//   .../data/traces/codex/YYYY-MM-DD/*.jsonl
	//
	// When enabled (default), scan is lightweight: pick latest date dir, then latest *.jsonl.
	// When disabled, we will walk the directory and tail multiple files (heavier).
	FollowLatest bool

	// TargetSessionKey overrides the destination chat.
	//
	// When empty (default), translations are sent back to the originating session derived from
	// trace metadata (turn_meta.cc_session_key).
	//
	// When set (e.g. "feishu:oc_xxx:ou_xxx"), ALL translations will be pushed to that session
	// instead (useful for a dedicated "CN translations" chat window).
	TargetSessionKey string

	// Follow from file beginning (replay). Default is tail-from-end.
	FromStart bool

	// How often to read newly appended bytes.
	PollInterval time.Duration
	// How often to rescan WatchPath directory for new *.jsonl files.
	ScanInterval time.Duration

	Reasoning    bool // translate item.type=reasoning
	AgentMessage bool // translate item.type=agent_message (final answer)

	// ForwardOnly disables LLM translation and simply forwards the raw text to chat.
	// This is the rollback / safe mode when translation providers are unstable.
	ForwardOnly bool

	ShowOriginal bool   // include original text in the pushed message
	Prefix       string // optional message prefix (e.g. "【思考翻译】\n")

	// When a file has no updates for this duration, we stop tailing it to save work.
	// 0 means use default (15 minutes).
	PurgeAfter time.Duration

	// Soft limit to split outgoing messages (per chunk). 0 means default (3500).
	MaxSendChars int

	// OnlyMode disables normal agent task execution for this engine.
	// When true, the engine only responds to slash commands (e.g. /sessionkey) and runs trace translation.
	OnlyMode bool
}

// StartTraceTranslate starts a background service that watches Codex trace JSONL files, translates
// selected items to Chinese, and proactively sends them back to the originating chat session
// (derived from cc_session_key in trace metadata).
//
// Enable via Agent.Options, for example:
//
//   trace_translate_enabled = true
//   trace_translate_path = "D:/ai-github/cc-connect/instances/instance-a/data/traces/codex"
//
// If trace_translate_path is omitted, it defaults to: <work_dir>/data/traces/codex
func (e *Engine) StartTraceTranslate(agentOptions map[string]any) {
	cfg := parseTraceTranslateCfg(agentOptions)
	if !cfg.Enabled {
		return
	}
	if cfg.WatchPath == "" {
		return
	}

	// If enabled, optionally put this engine into "translation-only" mode so it won't run tasks.
	if cfg.OnlyMode {
		e.traceTranslateOnly = true
	}

	var provider *ProviderConfig
	if !cfg.ForwardOnly {
		provider = activeProviderFromAgent(e.agent)
		if provider == nil || provider.APIKey == "" {
			// Preferred fallback: infer provider from the watched instance config (e.g. watchPath contains /instances/instance-c/).
			provider = providerFromWatchedInstanceConfig(cfg.WatchPath)
		}
		if provider == nil || provider.APIKey == "" {
			// Last fallback: environment variables.
			provider = providerFromEnv()
		}
		if provider == nil || provider.APIKey == "" {
			slog.Warn("trace-translate: no api_key available (provider/env); will forward original text only",
				"project", e.name,
			)
		}
	}

	svc := newTraceTranslateService(e.ctx, e.platforms, provider, cfg)
	svc.start()
	e.traceTranslateSvc = svc

	providerName := ""
	providerBaseURL := ""
	providerModel := ""
	if provider != nil {
		providerName = provider.Name
		providerBaseURL = provider.BaseURL
		providerModel = provider.Model
	}

	slog.Info("trace-translate: started",
		"project", e.name,
		"watch", cfg.WatchPath,
		"follow_latest", cfg.FollowLatest,
		"target_session", cfg.TargetSessionKey,
		"only_mode", cfg.OnlyMode,
		"forward_only", cfg.ForwardOnly,
		"provider", providerName,
		"provider_base_url", providerBaseURL,
		"provider_model", providerModel,
		"poll_ms", cfg.PollInterval.Milliseconds(),
		"scan_ms", cfg.ScanInterval.Milliseconds(),
		"reasoning", cfg.Reasoning,
		"agent_message", cfg.AgentMessage,
	)
}

func parseTraceTranslateCfg(opts map[string]any) TraceTranslateCfg {
	getBool := func(key string, def bool) bool {
		if opts == nil {
			return def
		}
		v, ok := opts[key]
		if !ok || v == nil {
			return def
		}
		if b, ok := v.(bool); ok {
			return b
		}
		// TOML sometimes decodes "true"/"false" as string in some setups
		if s, ok := v.(string); ok {
			s = strings.ToLower(strings.TrimSpace(s))
			if s == "true" || s == "1" || s == "yes" {
				return true
			}
			if s == "false" || s == "0" || s == "no" {
				return false
			}
		}
		return def
	}
	getInt := func(key string, def int) int {
		if opts == nil {
			return def
		}
		v, ok := opts[key]
		if !ok || v == nil {
			return def
		}
		switch n := v.(type) {
		case int:
			return n
		case int64:
			return int(n)
		case float64:
			return int(n)
		case string:
			// best-effort parse
			n = strings.TrimSpace(n)
			if n == "" {
				return def
			}
			var out int
			_, _ = fmt.Sscanf(n, "%d", &out)
			if out == 0 {
				return def
			}
			return out
		default:
			return def
		}
	}
	getString := func(key string, def string) string {
		if opts == nil {
			return def
		}
		v, ok := opts[key]
		if !ok || v == nil {
			return def
		}
		if s, ok := v.(string); ok {
			s = strings.TrimSpace(s)
			if s == "" {
				return def
			}
			return s
		}
		return def
	}

	enabled := getBool("trace_translate_enabled", false)

	watchPath := getString("trace_translate_path", "")
	if watchPath == "" {
		workDir := getString("work_dir", "")
		if workDir != "" {
			// Allow forward slashes in TOML on Windows.
			workDir = filepath.FromSlash(workDir)
			watchPath = filepath.Join(workDir, "data", "traces", "codex")
		}
	} else {
		watchPath = filepath.FromSlash(watchPath)
	}

	pollMs := getInt("trace_translate_poll_ms", 500)
	if pollMs < 50 {
		pollMs = 50
	}
	scanMs := getInt("trace_translate_scan_ms", 2000)
	if scanMs < 200 {
		scanMs = 200
	}

	purgeMin := getInt("trace_translate_purge_min", 15)
	if purgeMin <= 0 {
		purgeMin = 15
	}

	maxSendChars := getInt("trace_translate_max_send_chars", 3500)
	if maxSendChars < 500 {
		maxSendChars = 500
	}

	return TraceTranslateCfg{
		Enabled:      enabled,
		WatchPath:    watchPath,
		FollowLatest: getBool("trace_translate_follow_latest", true),
		TargetSessionKey: strings.TrimSpace(getString("trace_translate_target_session_key", "")),
		FromStart:    getBool("trace_translate_from_start", false),
		PollInterval: time.Duration(pollMs) * time.Millisecond,
		ScanInterval: time.Duration(scanMs) * time.Millisecond,
		Reasoning:    getBool("trace_translate_reasoning", true),
		AgentMessage: getBool("trace_translate_agent_message", false),
		ForwardOnly:  getBool("trace_translate_forward_only", false),
		ShowOriginal: getBool("trace_translate_show_original", false),
		Prefix:       getString("trace_translate_prefix", ""),
		PurgeAfter:   time.Duration(purgeMin) * time.Minute,
		MaxSendChars: maxSendChars,
		OnlyMode:     getBool("trace_translate_only", false),
	}
}

func activeProviderFromAgent(agent Agent) *ProviderConfig {
	ps, ok := agent.(ProviderSwitcher)
	if !ok {
		return nil
	}
	p := ps.GetActiveProvider()
	return p
}

func providerFromEnv() *ProviderConfig {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return nil
	}
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
	return &ProviderConfig{
		Name:    "env",
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   model,
	}
}

func providerFromWatchedInstanceConfig(watchPath string) *ProviderConfig {
	// WatchPath often looks like:
	//   D:\...\cc-connect\instances\instance-c\data\traces\codex
	// or a date folder under it. We infer:
	//   <repo>\instances\<inst>\config.toml
	p := filepath.Clean(filepath.FromSlash(strings.TrimSpace(watchPath)))
	if p == "" {
		return nil
	}

	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '\\' || r == '/' })
	if len(parts) < 3 {
		return nil
	}

	inst := ""
	rootParts := []string{}
	for i := 0; i < len(parts); i++ {
		if strings.EqualFold(parts[i], "instances") && i+1 < len(parts) {
			inst = parts[i+1]
			rootParts = parts[:i]
			break
		}
	}
	if inst == "" || len(rootParts) == 0 {
		return nil
	}

	repoRoot := filepath.Join(rootParts...)
	cfgPath := filepath.Join(repoRoot, "instances", inst, "config.toml")
	if _, err := os.Stat(cfgPath); err != nil {
		return nil
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil
	}

	// Minimal TOML decode: only extract provider + providers.
	var raw struct {
		Projects []struct {
			Name string `toml:"name"`
			Agent struct {
				Options   map[string]any `toml:"options"`
				Providers []struct {
					Name    string `toml:"name"`
					APIKey  string `toml:"api_key"`
					BaseURL string `toml:"base_url"`
					Model   string `toml:"model"`
				} `toml:"providers"`
			} `toml:"agent"`
		} `toml:"projects"`
	}
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil
	}
	if len(raw.Projects) == 0 {
		return nil
	}
	proj := raw.Projects[0]
	activeName, _ := proj.Agent.Options["provider"].(string)
	activeName = strings.TrimSpace(activeName)

	choose := func() *ProviderConfig {
		if activeName != "" {
			for _, p := range proj.Agent.Providers {
				if p.Name == activeName {
					return &ProviderConfig{Name: p.Name, APIKey: p.APIKey, BaseURL: p.BaseURL, Model: p.Model}
				}
			}
		}
		if len(proj.Agent.Providers) > 0 {
			p := proj.Agent.Providers[0]
			return &ProviderConfig{Name: p.Name, APIKey: p.APIKey, BaseURL: p.BaseURL, Model: p.Model}
		}
		return nil
	}
	return choose()
}

type traceTranslateService struct {
	ctx       context.Context
	platforms map[string]Platform // by platform name
	provider  *ProviderConfig
	cfg       TraceTranslateCfg

	startedAt time.Time

	client *http.Client

	jobs chan traceTranslateJob

	mu    sync.Mutex
	files map[string]*traceFileState

	// Failure tracking for auto-diagnosis (so users can locate issues without digging logs).
	failCount          int
	lastFailAt         time.Time
	lastFailErr        string
	lastFailReportedAt time.Time
}

type traceTranslateJob struct {
	sessionKey string
	itemType   string // "reasoning" | "agent_message"
	itemID     string
	text       string
}

type traceFileState struct {
	path       string
	pos        int64
	buf        string
	sessionKey string
	seen       map[string]struct{}
	lastUpdate time.Time
}

type traceTranslateSnapshot struct {
	WatchPath        string
	FollowLatest     bool
	TargetSessionKey string
	ForwardOnly      bool

	ProviderName    string
	ProviderBaseURL string
	ProviderModel   string

	FailCount   int
	LastFailAt  time.Time
	LastFailErr string
}

func newTraceTranslateService(ctx context.Context, platforms []Platform, provider *ProviderConfig, cfg TraceTranslateCfg) *traceTranslateService {
	pm := make(map[string]Platform, len(platforms))
	for _, p := range platforms {
		pm[p.Name()] = p
	}
	if cfg.PurgeAfter <= 0 {
		cfg.PurgeAfter = 15 * time.Minute
	}
	if cfg.MaxSendChars <= 0 {
		cfg.MaxSendChars = 3500
	}

	return &traceTranslateService{
		ctx:       ctx,
		platforms: pm,
		provider:  provider,
		cfg:       cfg,
		startedAt: time.Now(),
		client:    &http.Client{Timeout: 60 * time.Second},
		jobs:      make(chan traceTranslateJob, 128),
		files:     make(map[string]*traceFileState),
	}
}

func (s *traceTranslateService) snapshot() traceTranslateSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := traceTranslateSnapshot{
		WatchPath:        s.cfg.WatchPath,
		FollowLatest:     s.cfg.FollowLatest,
		TargetSessionKey: strings.TrimSpace(s.cfg.TargetSessionKey),
		ForwardOnly:      s.cfg.ForwardOnly,
		FailCount:        s.failCount,
		LastFailAt:       s.lastFailAt,
		LastFailErr:      s.lastFailErr,
	}
	if s.provider != nil {
		snap.ProviderName = s.provider.Name
		snap.ProviderBaseURL = s.provider.BaseURL
		snap.ProviderModel = s.provider.Model
	}
	return snap
}

func (s *traceTranslateService) SwitchWatchPath(newPath string) error {
	newPath = filepath.Clean(filepath.FromSlash(strings.TrimSpace(newPath)))
	if newPath == "" {
		return fmt.Errorf("watch path is empty")
	}
	if _, err := os.Stat(newPath); err != nil {
		return err
	}

	// Treat switch time as a new baseline so we won't accidentally replay an old active file.
	now := time.Now()

	s.mu.Lock()
	s.cfg.WatchPath = newPath
	s.startedAt = now
	s.files = make(map[string]*traceFileState)
	s.failCount = 0
	s.lastFailAt = time.Time{}
	s.lastFailErr = ""
	s.lastFailReportedAt = time.Time{}
	s.mu.Unlock()

	// Attach immediately (don't wait for next scan tick).
	s.scanFiles()
	return nil
}

func (s *traceTranslateService) start() {
	go s.workerLoop()
	go s.watchLoop()
}

func (s *traceTranslateService) watchLoop() {
	tPoll := time.NewTicker(s.cfg.PollInterval)
	defer tPoll.Stop()
	tScan := time.NewTicker(s.cfg.ScanInterval)
	defer tScan.Stop()

	// Initial scan (so we can attach quickly).
	s.scanFiles()

	for {
		select {
		case <-s.ctx.Done():
			close(s.jobs)
			return
		case <-tScan.C:
			s.scanFiles()
			s.purgeFiles()
		case <-tPoll.C:
			s.pollFiles()
		}
	}
}

func isDateDirName(name string) bool {
	if len(name) != 10 {
		return false
	}
	// YYYY-MM-DD
	for i := 0; i < 10; i++ {
		ch := name[i]
		switch i {
		case 4, 7:
			if ch != '-' {
				return false
			}
		default:
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}

func latestDateDir(root string) string {
	ents, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	best := ""
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !isDateDirName(name) {
			continue
		}
		// Lexicographical order matches date order for YYYY-MM-DD.
		if best == "" || name > best {
			best = name
		}
	}
	if best == "" {
		return ""
	}
	return filepath.Join(root, best)
}

func latestJsonlInDir(dir string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var bestPath string
	var bestTime time.Time
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if bestPath == "" || info.ModTime().After(bestTime) {
			bestTime = info.ModTime()
			bestPath = filepath.Join(dir, e.Name())
		}
	}
	return bestPath
}

// findLatestTraceFile tries to locate the newest *.jsonl under the given trace root.
// It handles the common structure: <root>/YYYY-MM-DD/*.jsonl.
func findLatestTraceFile(root string) string {
	// Prefer latest date dir if present.
	if d := latestDateDir(root); d != "" {
		if f := latestJsonlInDir(d); f != "" {
			return f
		}
	}
	// Fallback: scan current dir (e.g. when root itself is a date dir).
	return latestJsonlInDir(root)
}

func (s *traceTranslateService) scanFiles() {
	s.mu.Lock()
	root := s.cfg.WatchPath
	followLatest := s.cfg.FollowLatest
	fromStart := s.cfg.FromStart
	startedAt := s.startedAt
	s.mu.Unlock()

	if root == "" {
		return
	}

	info, err := os.Stat(root)
	if err != nil {
		return
	}

	paths := make([]string, 0, 8)
	if !info.IsDir() {
		if strings.HasSuffix(strings.ToLower(root), ".jsonl") {
			paths = append(paths, root)
		}
	} else {
		if followLatest {
			if f := findLatestTraceFile(root); f != "" {
				paths = append(paths, f)
			}
		} else {
			_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d == nil {
					return nil
				}
				if d.IsDir() {
					return nil
				}
				if strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
					paths = append(paths, path)
				}
				return nil
			})
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// If we only follow the latest file, keep the state map small (single file).
	if followLatest && info.IsDir() {
		keep := map[string]bool{}
		for _, p := range paths {
			keep[p] = true
		}
		for existing := range s.files {
			if !keep[existing] {
				delete(s.files, existing)
			}
		}
	}

	for _, p := range paths {
		if _, ok := s.files[p]; ok {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}

		st := &traceFileState{
			path:       p,
			seen:       make(map[string]struct{}),
			lastUpdate: time.Now(),
		}

		// If the file is old (pre-existing when we started), tail from end by default.
		// For files created after we started, read from beginning to avoid missing early lines.
		if fromStart || fi.ModTime().After(startedAt) {
			st.pos = 0
		} else {
			st.pos = fi.Size()
		}

		st.sessionKey = readSessionKeyFromTrace(p)
		s.files[p] = st
	}
}

func (s *traceTranslateService) purgeFiles() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for path, st := range s.files {
		if now.Sub(st.lastUpdate) > s.cfg.PurgeAfter {
			delete(s.files, path)
		}
	}
}

func (s *traceTranslateService) pollFiles() {
	s.mu.Lock()
	paths := make([]string, 0, len(s.files))
	for p := range s.files {
		paths = append(paths, p)
	}
	s.mu.Unlock()

	for _, p := range paths {
		s.pollOne(p)
	}
}

func (s *traceTranslateService) pollOne(path string) {
	s.mu.Lock()
	st := s.files[path]
	s.mu.Unlock()
	if st == nil {
		return
	}

	fi, err := os.Stat(path)
	if err != nil {
		s.mu.Lock()
		delete(s.files, path)
		s.mu.Unlock()
		return
	}

	// Handle truncation / rewrite.
	if fi.Size() < st.pos {
		st.pos = 0
		st.buf = ""
		st.seen = make(map[string]struct{})
		st.sessionKey = readSessionKeyFromTrace(path)
	}

	if fi.Size() == st.pos {
		return
	}

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	if _, err := f.Seek(st.pos, io.SeekStart); err != nil {
		return
	}
	data, err := io.ReadAll(f)
	if err != nil || len(data) == 0 {
		return
	}
	st.pos += int64(len(data))
	st.lastUpdate = time.Now()

	st.buf += string(data)
	if !strings.Contains(st.buf, "\n") {
		return
	}

	parts := strings.Split(st.buf, "\n")
	for i := 0; i < len(parts)-1; i++ {
		line := strings.TrimRight(parts[i], "\r")
		s.processLine(st, line)
	}
	st.buf = parts[len(parts)-1]
}

func (s *traceTranslateService) processLine(st *traceFileState, line string) {
	if strings.TrimSpace(line) == "" {
		return
	}

	var raw struct {
		Type         string `json:"type"`
		CCSessionKey string `json:"cc_session_key"`
		Item         *struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return
	}

	if raw.Type == "turn_meta" && raw.CCSessionKey != "" {
		st.sessionKey = raw.CCSessionKey
		return
	}

	if raw.Type != "item.completed" || raw.Item == nil {
		return
	}

	itemType := raw.Item.Type
	if itemType == "reasoning" && !s.cfg.Reasoning {
		return
	}
	if itemType == "agent_message" && !s.cfg.AgentMessage {
		return
	}
	if itemType != "reasoning" && itemType != "agent_message" {
		return
	}

	if st.sessionKey == "" {
		// No session key means we don't know where to push.
		return
	}

	id := raw.Item.ID
	seenKey := itemType + "|" + id
	if id != "" {
		if _, ok := st.seen[seenKey]; ok {
			return
		}
		st.seen[seenKey] = struct{}{}
	}

	text := raw.Item.Text
	if strings.TrimSpace(text) == "" {
		return
	}

	job := traceTranslateJob{
		sessionKey: st.sessionKey,
		itemType:   itemType,
		itemID:     id,
		text:       text,
	}
	if strings.TrimSpace(s.cfg.TargetSessionKey) != "" {
		job.sessionKey = strings.TrimSpace(s.cfg.TargetSessionKey)
	}

	select {
	case s.jobs <- job:
	default:
		// If translation is too slow, drop intermediate jobs to avoid unbounded lag.
		slog.Warn("trace-translate: job queue full, dropping item",
			"session", st.sessionKey,
			"type", itemType,
		)
	}
}

func readSessionKeyFromTrace(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	// Only need the header region.
	b, err := io.ReadAll(io.LimitReader(f, 256*1024))
	if err != nil {
		return ""
	}

	// Parse JSONL quickly line-by-line.
	for _, line := range bytes.Split(b, []byte("\n")) {
		line = bytes.TrimSpace(bytes.TrimRight(line, "\r"))
		if len(line) == 0 {
			continue
		}
		var meta struct {
			Type         string `json:"type"`
			CCSessionKey string `json:"cc_session_key"`
		}
		if json.Unmarshal(line, &meta) == nil && meta.Type == "turn_meta" && meta.CCSessionKey != "" {
			return meta.CCSessionKey
		}
	}
	return ""
}

func (s *traceTranslateService) workerLoop() {
	for job := range s.jobs {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		msg := s.translateJob(job)
		if strings.TrimSpace(msg) == "" {
			continue
		}
		_ = s.sendToSession(job.sessionKey, msg)
	}
}

func (s *traceTranslateService) translateJob(job traceTranslateJob) string {
	original := job.text
	text := strings.TrimSpace(original)
	if text == "" {
		return ""
	}

	// Rollback mode: do not call LLM, just forward the raw text.
	if s.cfg.ForwardOnly {
		// Ignore ShowOriginal to avoid duplication; prefix still applies.
		return s.formatMsg("", original)
	}

	// If translation is unavailable, still forward original.
	if s.provider == nil || s.provider.APIKey == "" {
		s.onTranslateFail(job.sessionKey, fmt.Errorf("no api_key (provider/env missing)"))
		if s.cfg.ShowOriginal {
			return s.formatMsg(original, original)
		}
		return s.formatMsg("", original)
	}

	// If already Chinese (and basically no English), skip translation to save cost.
	if (strings.IndexFunc(text, func(r rune) bool { return r >= 0x4e00 && r <= 0x9fff }) >= 0) &&
		(strings.IndexFunc(text, func(r rune) bool { return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') }) < 0) {
		if s.cfg.ShowOriginal {
			return s.formatMsg(original, original)
		}
		return s.formatMsg("", original)
	}

	zh, err := translateToChinese(s.ctx, s.client, *s.provider, text)
	if err != nil || strings.TrimSpace(zh) == "" {
		if err == nil {
			err = fmt.Errorf("empty translation output")
		}
		slog.Warn("trace-translate: translate failed; forwarding original",
			"error", err,
		)
		s.onTranslateFail(job.sessionKey, err)
		if s.cfg.ShowOriginal {
			return s.formatMsg(original, original)
		}
		return s.formatMsg("", original)
	}

	s.onTranslateSuccess()
	if s.cfg.ShowOriginal {
		return s.formatMsg(original, zh)
	}
	return s.formatMsg("", zh)
}

func (s *traceTranslateService) onTranslateSuccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCount = 0
	s.lastFailErr = ""
}

func (s *traceTranslateService) onTranslateFail(sessionKey string, err error) {
	if err == nil {
		err = fmt.Errorf("unknown error")
	}
	errStr := sanitizeSecrets(err.Error())
	if len(errStr) > 600 {
		errStr = errStr[:600] + "…"
	}

	s.mu.Lock()
	s.failCount++
	s.lastFailAt = time.Now()
	s.lastFailErr = errStr
	shouldReport := s.failCount >= 3 && time.Since(s.lastFailReportedAt) > time.Minute
	if shouldReport {
		s.lastFailReportedAt = time.Now()
	}
	s.mu.Unlock()

	if !shouldReport {
		return
	}

	// Proactively push one diagnostic message (rate-limited) so the user can see why it stays English.
	diag := s.buildDiagMessage(errStr)
	if strings.TrimSpace(diag) == "" {
		return
	}
	_ = s.sendToSession(sessionKey, diag)
}

func (s *traceTranslateService) buildDiagMessage(errStr string) string {
	var b strings.Builder
	b.WriteString("【思考翻译诊断】\n")
	b.WriteString("检测到连续翻译失败（已自动转发原文，所以你看到仍是英文）。\n\n")

	if strings.TrimSpace(errStr) != "" {
		b.WriteString("错误：\n")
		b.WriteString(errStr)
		b.WriteString("\n\n")
	}

	if s.provider != nil {
		b.WriteString("当前翻译 Provider：\n")
		if strings.TrimSpace(s.provider.Name) != "" {
			b.WriteString("- name: " + s.provider.Name + "\n")
		}
		if strings.TrimSpace(s.provider.BaseURL) != "" {
			b.WriteString("- base_url: " + s.provider.BaseURL + "\n")
		}
		if strings.TrimSpace(s.provider.Model) != "" {
			b.WriteString("- model: " + s.provider.Model + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("排查建议：\n")
	b.WriteString("- 先看这个错误是否为 401/403（Key 无效或额度/权限问题）、404（网关不支持该接口）、400（模型名/参数不支持或内容过长）。\n")
	b.WriteString("- 若 instance-c 能正常跑任务但这里失败：通常是翻译请求走的接口/返回格式与网关不兼容（请把本条诊断消息发我）。\n")
	return strings.TrimSpace(b.String())
}

func (s *traceTranslateService) formatMsg(original, zh string) string {
	var b strings.Builder
	if s.cfg.Prefix != "" {
		b.WriteString(s.cfg.Prefix)
	}
	if strings.TrimSpace(original) != "" {
		b.WriteString("【原文】\n")
		b.WriteString(original)
		b.WriteString("\n\n")
	}
	b.WriteString(zh)
	return strings.TrimSpace(b.String())
}

func (s *traceTranslateService) sendToSession(sessionKey, content string) error {
	platformName := ""
	if idx := strings.Index(sessionKey, ":"); idx > 0 {
		platformName = sessionKey[:idx]
	}
	p := s.platforms[platformName]
	if p == nil {
		return fmt.Errorf("platform %q not found for session %q", platformName, sessionKey)
	}

	rc, ok := p.(ReplyContextReconstructor)
	if !ok {
		return fmt.Errorf("platform %q does not support proactive messaging", platformName)
	}

	replyCtx, err := rc.ReconstructReplyCtx(sessionKey)
	if err != nil {
		return err
	}

	for _, chunk := range splitByRunes(content, s.cfg.MaxSendChars) {
		if err := p.Send(s.ctx, replyCtx, chunk); err != nil {
			return err
		}
	}
	return nil
}

func splitByRunes(text string, maxRunes int) []string {
	if maxRunes <= 0 || utf8.RuneCountInString(text) <= maxRunes {
		return []string{text}
	}
	r := []rune(text)
	var out []string
	for len(r) > 0 {
		n := maxRunes
		if n > len(r) {
			n = len(r)
		}
		out = append(out, string(r[:n]))
		r = r[n:]
	}
	return out
}

func translateToChinese(ctx context.Context, client *http.Client, p ProviderConfig, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	if p.APIKey == "" {
		return "", fmt.Errorf("no api_key")
	}

	baseURL := strings.TrimRight(p.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	// Normalize base URL to .../v1 (avoid double /v1)
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL = baseURL + "/v1"
	}

	model := p.Model
	if model == "" {
		model = "gpt-4o-mini"
	}

	prompt := "请把下面内容翻译成中文，保留 Markdown/换行格式，仅输出中文译文：\n\n" + text

	// 1) Try Responses API
	if out, err := callResponses(ctx, client, baseURL, p.APIKey, model, prompt); err == nil && strings.TrimSpace(out) != "" {
		return out, nil
	}

	// 2) Fallback to Chat Completions
	return callChatCompletions(ctx, client, baseURL, p.APIKey, model, prompt)
}

func callResponses(ctx context.Context, client *http.Client, baseURL, apiKey, model, prompt string) (string, error) {
	url := baseURL + "/responses"
	body := map[string]any{
		"model":       model,
		"input":       prompt,
		"temperature": 0.2,
	}
	b, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("responses API %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	out := extractOpenAIText(raw)
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("responses API %d: empty output (raw=%s)", resp.StatusCode, shortenForLog(raw, 500))
	}
	return out, nil
}

func callChatCompletions(ctx context.Context, client *http.Client, baseURL, apiKey, model, prompt string) (string, error) {
	url := baseURL + "/chat/completions"
	body := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.2,
	}
	b, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("chat.completions API %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	out := extractChatCompletionText(raw)
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("chat.completions API %d: empty output (raw=%s)", resp.StatusCode, shortenForLog(raw, 500))
	}
	return out, nil
}

func shortenForLog(raw []byte, max int) string {
	s := strings.TrimSpace(string(raw))
	s = sanitizeSecrets(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func sanitizeSecrets(s string) string {
	// Best-effort: hide API keys if a gateway echoes them (shouldn't, but don't trust).
	// Replace patterns like: sk-xxxxxxxxxxxxxxxx...
	reSk := regexp.MustCompile(`sk-[A-Za-z0-9]{8,}`)
	s = reSk.ReplaceAllString(s, "sk-***")

	// Hide Bearer tokens.
	reBearer := regexp.MustCompile(`(?i)Bearer\\s+[A-Za-z0-9._\\-]+`)
	s = reBearer.ReplaceAllString(s, "Bearer ***")

	return s
}

func extractOpenAIText(raw []byte) string {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}

	// Some gateways provide "output_text"
	if v, ok := obj["output_text"].(string); ok && strings.TrimSpace(v) != "" {
		return v
	}

	// Standard Responses API: output[].content[].text
	out, ok := obj["output"].([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, o := range out {
		m, _ := o.(map[string]any)
		if m == nil {
			continue
		}
		content, _ := m["content"].([]any)
		for _, c := range content {
			cm, _ := c.(map[string]any)
			if cm == nil {
				continue
			}
			if t, ok := cm["text"].(string); ok {
				b.WriteString(t)
			}
		}
	}
	return b.String()
}

func extractChatCompletionText(raw []byte) string {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	// Some gateways still return Responses-like shape even for /chat/completions.
	if v, ok := obj["output_text"].(string); ok && strings.TrimSpace(v) != "" {
		return v
	}

	choices, ok := obj["choices"].([]any)
	if !ok || len(choices) == 0 {
		return ""
	}
	first, _ := choices[0].(map[string]any)
	if first == nil {
		return ""
	}

	// Standard Chat Completions: choices[0].message.content
	if msgAny, ok := first["message"]; ok {
		if msg, ok := msgAny.(map[string]any); ok && msg != nil {
			if c, ok := msg["content"].(string); ok && strings.TrimSpace(c) != "" {
				return c
			}
			// Some OpenAI-compatible providers return "content" as an array of segments:
			//   [{"type":"text","text":"..."}]
			if segs, ok := msg["content"].([]any); ok {
				if out := joinContentSegments(segs); strings.TrimSpace(out) != "" {
					return out
				}
			}
			// Rare: content is an object
			if m, ok := msg["content"].(map[string]any); ok && m != nil {
				if t, ok := m["text"].(string); ok && strings.TrimSpace(t) != "" {
					return t
				}
				if t, ok := m["content"].(string); ok && strings.TrimSpace(t) != "" {
					return t
				}
			}
		}
	}

	// Fallback: streaming-like gateways might return delta.
	if deltaAny, ok := first["delta"]; ok {
		if delta, ok := deltaAny.(map[string]any); ok && delta != nil {
			if c, ok := delta["content"].(string); ok && strings.TrimSpace(c) != "" {
				return c
			}
			if segs, ok := delta["content"].([]any); ok {
				if out := joinContentSegments(segs); strings.TrimSpace(out) != "" {
					return out
				}
			}
		}
	}

	// Legacy: choices[0].text
	if t, ok := first["text"].(string); ok && strings.TrimSpace(t) != "" {
		return t
	}

	return ""
}

func joinContentSegments(segs []any) string {
	var b strings.Builder
	for _, s := range segs {
		switch v := s.(type) {
		case string:
			b.WriteString(v)
		case map[string]any:
			// Common: {"type":"text","text":"..."}
			if t, ok := v["text"].(string); ok {
				b.WriteString(t)
				continue
			}
			// Some gateways use "content"
			if t, ok := v["content"].(string); ok {
				b.WriteString(t)
				continue
			}
			// Rare: {"value":"..."}
			if t, ok := v["value"].(string); ok {
				b.WriteString(t)
				continue
			}
		}
	}
	return b.String()
}
