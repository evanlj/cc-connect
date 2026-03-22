package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	cfgpkg "github.com/chenhg5/cc-connect/config"
)

type SquadProcessManager struct {
	mu   sync.Mutex
	runs map[string]map[string]*exec.Cmd // run_id -> role -> cmd
}

func NewSquadProcessManager() *SquadProcessManager {
	return &SquadProcessManager{
		runs: make(map[string]map[string]*exec.Cmd),
	}
}

func (m *SquadProcessManager) StartRun(run *SquadRun, store *SquadStore) (map[string]SquadRoleRuntime, error) {
	if run == nil {
		return nil, fmt.Errorf("run is nil")
	}
	if store == nil || !store.Enabled() {
		return nil, fmt.Errorf("squad store is unavailable")
	}
	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable path: %w", err)
	}

	roles := uniqueSquadRolesInOrder(run.PlannerRole, run.ExecutorRole, run.ReviewerRole)
	if len(roles) == 0 {
		return nil, fmt.Errorf("no roles configured")
	}

	runtimeRoot := resolveSquadRuntimeRoot(run.RunID, store)
	if runtimeRoot == "" {
		return nil, fmt.Errorf("runtime dir is unavailable")
	}
	if err := os.MkdirAll(runtimeRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create runtime dir: %w", err)
	}

	started := make(map[string]*exec.Cmd, len(roles))
	runtimeMap := make(map[string]SquadRoleRuntime, len(roles))
	cleanup := func() {
		for role, cmd := range started {
			_ = killProcess(cmd)
			delete(started, role)
		}
	}

	for _, role := range roles {
		templatePath, displayName, err := resolveRoleTemplateConfig(role)
		if err != nil {
			cleanup()
			return nil, err
		}
		roleDir := filepath.Join(runtimeRoot, role)
		dataDir := filepath.Join(roleDir, "data")
		configPath := filepath.Join(roleDir, "config.toml")
		projectName := fmt.Sprintf("%s-%s-project", run.RunID, role)

		if err := buildRoleRuntimeConfig(templatePath, configPath, dataDir, run.RepoPath, run.Provider, projectName); err != nil {
			cleanup()
			return nil, fmt.Errorf("prepare config for %s: %w", role, err)
		}
		logPath := filepath.Join(roleDir, "cc-connect.log")
		logFile, err := openRuntimeLogFile(logPath)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("open runtime log for %s: %w", role, err)
		}

		cmd := exec.Command(exePath, "-config", configPath)
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		cmd.Dir = filepath.Dir(exePath)
		if err := cmd.Start(); err != nil {
			_ = logFile.Close()
			cleanup()
			return nil, fmt.Errorf("start role %s: %w", role, err)
		}
		go func(c *exec.Cmd, lf *os.File) {
			_ = c.Wait()
			_ = lf.Close()
		}(cmd, logFile)

		socketPath := filepath.Join(dataDir, "run", "api.sock")
		// Unix socket paths have strict length limits on multiple OSes (typically ~108 bytes).
		// Fail fast with a clear message if path is still too long.
		if len(filepath.ToSlash(socketPath)) > 100 {
			_ = killProcess(cmd)
			cleanup()
			return nil, fmt.Errorf("socket path too long for role %s: %s", role, socketPath)
		}
		if err := waitSocketReady(socketPath, 40*time.Second); err != nil {
			_ = killProcess(cmd)
			cleanup()
			return nil, fmt.Errorf("role %s socket not ready: %w", role, err)
		}

		started[role] = cmd
		runtimeMap[role] = SquadRoleRuntime{
			Role:           role,
			DisplayName:    displayName,
			TemplateConfig: templatePath,
			RuntimeConfig:  configPath,
			DataDir:        dataDir,
			SocketPath:     socketPath,
			ProjectName:    projectName,
			PID:            cmd.Process.Pid,
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if old := m.runs[run.RunID]; len(old) > 0 {
		for _, cmd := range old {
			_ = killProcess(cmd)
		}
	}
	m.runs[run.RunID] = started
	return runtimeMap, nil
}

func resolveSquadRuntimeRoot(runID string, store *SquadStore) string {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return ""
	}

	base := strings.TrimSpace(os.Getenv("CC_SQUAD_RUNTIME_ROOT"))
	if base == "" {
		// Prefer a short root on the same drive to avoid AF_UNIX path length overflow.
		storeRoot := ""
		if store != nil {
			storeRoot = strings.TrimSpace(store.RootDir())
		}
		vol := filepath.VolumeName(storeRoot)
		if vol == "" {
			if repoRoot := inferRepoRootFromWorkingDir(); repoRoot != "" {
				vol = filepath.VolumeName(repoRoot)
			}
		}
		if vol != "" {
			base = filepath.Join(vol+string(os.PathSeparator), "ccsq")
		} else {
			base = filepath.Join(os.TempDir(), "ccsq")
		}
	}
	if !filepath.IsAbs(base) {
		if abs, err := filepath.Abs(base); err == nil {
			base = abs
		}
	}
	return filepath.Join(base, runID)
}

func (m *SquadProcessManager) StopRun(runID string) error {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return fmt.Errorf("run_id is required")
	}

	m.mu.Lock()
	cmds := m.runs[runID]
	delete(m.runs, runID)
	m.mu.Unlock()

	var stopErr error
	for _, cmd := range cmds {
		if err := killProcess(cmd); err != nil && stopErr == nil {
			stopErr = err
		}
	}
	return stopErr
}

func (m *SquadProcessManager) StopAll() {
	m.mu.Lock()
	runs := m.runs
	m.runs = make(map[string]map[string]*exec.Cmd)
	m.mu.Unlock()

	for _, cmds := range runs {
		for _, cmd := range cmds {
			_ = killProcess(cmd)
		}
	}
}

func openRuntimeLogFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

func waitSocketReady(socketPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if fi, err := os.Stat(socketPath); err == nil && !fi.IsDir() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait timeout (%s): %s", timeout, socketPath)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func killProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !strings.Contains(strings.ToLower(err.Error()), "process already finished") {
		return err
	}
	return nil
}

func uniqueSquadRolesInOrder(roles ...string) []string {
	out := make([]string, 0, len(roles))
	seen := map[string]bool{}
	for _, role := range roles {
		role = normalizeRoleToken(role)
		if role == "" || seen[role] {
			continue
		}
		seen[role] = true
		out = append(out, role)
	}
	return out
}

func resolveRoleTemplateConfig(role string) (configPath, displayName string, err error) {
	role = normalizeRoleToken(role)
	if role == "" {
		return "", "", fmt.Errorf("role is empty")
	}
	repoRoot := inferRepoRootFromWorkingDir()
	if repoRoot == "" {
		return "", "", fmt.Errorf("cannot infer repository root")
	}

	for _, r := range defaultDebateRoles() {
		if normalizeRoleToken(r.Role) != role {
			continue
		}
		displayName = emptyAs(strings.TrimSpace(r.DisplayName), r.Role)
		candidates := buildTemplateCandidates(repoRoot, r.Instance)
		for _, p := range candidates {
			if fileExists(p) {
				return p, displayName, nil
			}
		}
		return "", "", fmt.Errorf("template config for role %s not found (tried %s)", role, strings.Join(candidates, ", "))
	}
	return "", "", fmt.Errorf("role %s is not recognized", role)
}

func buildTemplateCandidates(repoRoot, instance string) []string {
	repoRoot = strings.TrimSpace(repoRoot)
	instance = strings.TrimSpace(instance)
	out := make([]string, 0, 3)
	if repoRoot == "" || instance == "" {
		return out
	}
	if strings.HasPrefix(instance, "instance-mutilbot") {
		out = append(out, filepath.Join(repoRoot, "mutilbot", instance, "config.toml"))
	}
	out = append(out, filepath.Join(repoRoot, "instances", instance, "config.toml"))
	return out
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func buildRoleRuntimeConfig(templatePath, outPath, dataDir, repoPath, provider, projectName string) error {
	templatePath = strings.TrimSpace(templatePath)
	outPath = strings.TrimSpace(outPath)
	dataDir = strings.TrimSpace(dataDir)
	repoPath = strings.TrimSpace(repoPath)
	provider = strings.TrimSpace(provider)
	projectName = strings.TrimSpace(projectName)
	if templatePath == "" || outPath == "" || dataDir == "" || repoPath == "" || projectName == "" {
		return fmt.Errorf("template/out/data_dir/repo/project_name are required")
	}

	cfg, err := cfgpkg.Load(templatePath)
	if err != nil {
		return fmt.Errorf("load template config: %w", err)
	}
	if len(cfg.Projects) == 0 {
		return fmt.Errorf("template has no projects")
	}
	baseProject := cfg.Projects[0]

	agentOptions := cloneMapAny(baseProject.Agent.Options)
	agentOptions["work_dir"] = filepath.ToSlash(repoPath)
	if provider != "" {
		agentOptions["provider"] = provider
	}

	platforms := make([]cfgpkg.PlatformConfig, 0, len(baseProject.Platforms))
	for _, pf := range baseProject.Platforms {
		platforms = append(platforms, cfgpkg.PlatformConfig{
			Type:    pf.Type,
			Options: cloneMapAny(pf.Options),
		})
	}
	// Prevent runtime worker processes from handling normal inbound chat messages.
	for i := range platforms {
		if platforms[i].Options == nil {
			platforms[i].Options = make(map[string]any)
		}
		platforms[i].Options["allow_from"] = "__squad_internal_only__"
		platforms[i].Options["reaction_emoji"] = "none"
	}

	outCfg := cfgpkg.Config{
		DataDir:  filepath.ToSlash(dataDir),
		Language: cfg.Language,
		Log:      cfg.Log,
		Speech:   cfg.Speech,
		Display:  cfg.Display,
		Projects: []cfgpkg.ProjectConfig{
			{
				Name: projectName,
				Agent: cfgpkg.AgentConfig{
					Type:      baseProject.Agent.Type,
					Options:   agentOptions,
					Providers: cloneProviders(baseProject.Agent.Providers),
				},
				Platforms: platforms,
			},
		},
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("create runtime config dir: %w", err)
	}
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create runtime config: %w", err)
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(outCfg); err != nil {
		return fmt.Errorf("encode runtime config: %w", err)
	}
	return nil
}

func cloneMapAny(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneProviders(in []cfgpkg.ProviderConfig) []cfgpkg.ProviderConfig {
	if len(in) == 0 {
		return nil
	}
	out := make([]cfgpkg.ProviderConfig, 0, len(in))
	for _, p := range in {
		item := cfgpkg.ProviderConfig{
			Name:    p.Name,
			APIKey:  p.APIKey,
			BaseURL: p.BaseURL,
			Model:   p.Model,
		}
		if len(p.Env) > 0 {
			item.Env = make(map[string]string, len(p.Env))
			for k, v := range p.Env {
				item.Env[k] = v
			}
		}
		out = append(out, item)
	}
	return out
}
