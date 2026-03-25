package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type DebateProcessManager struct {
	mu    sync.Mutex
	rooms map[string]map[string]*exec.Cmd // room_id -> role -> cmd
}

func NewDebateProcessManager() *DebateProcessManager {
	return &DebateProcessManager{
		rooms: make(map[string]map[string]*exec.Cmd),
	}
}

func (m *DebateProcessManager) StartRoom(room *DebateRoom, store *DebateStore) ([]DebateRole, map[string]SquadRoleRuntime, error) {
	if room == nil {
		return nil, nil, fmt.Errorf("room is nil")
	}
	if store == nil || !store.Enabled() {
		return nil, nil, fmt.Errorf("debate store is unavailable")
	}
	roomID := strings.TrimSpace(room.RoomID)
	if roomID == "" {
		return nil, nil, fmt.Errorf("room_id is required")
	}
	repoPath := strings.TrimSpace(room.RepoPath)
	if repoPath == "" {
		return nil, nil, fmt.Errorf("repo_path is required for debate runtime")
	}
	if !filepath.IsAbs(repoPath) {
		return nil, nil, fmt.Errorf("repo_path must be an absolute path")
	}
	fi, err := os.Stat(repoPath)
	if err != nil {
		return nil, nil, fmt.Errorf("repo_path is invalid: %w", err)
	}
	if !fi.IsDir() {
		return nil, nil, fmt.Errorf("repo_path must be a directory")
	}
	exePath, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve executable path: %w", err)
	}

	roles := room.Roles
	if len(roles) == 0 {
		roles = defaultDebateRoles()
	}
	if len(roles) == 0 {
		return nil, nil, fmt.Errorf("no roles configured")
	}
	runtimeRoot := resolveDebateRuntimeRoot(roomID, store)
	if runtimeRoot == "" {
		return nil, nil, fmt.Errorf("runtime root is unavailable")
	}
	if err := os.MkdirAll(runtimeRoot, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create runtime root: %w", err)
	}

	started := make(map[string]*exec.Cmd, len(roles))
	runtimeMap := make(map[string]SquadRoleRuntime, len(roles))
	runtimeRoles := make([]DebateRole, 0, len(roles))
	cleanup := func() {
		for role, cmd := range started {
			_ = killProcess(cmd)
			delete(started, role)
		}
	}

	for _, role := range roles {
		roleKey := normalizeRoleToken(role.Role)
		if roleKey == "" {
			continue
		}
		templatePath, displayName, err := resolveRoleTemplateConfig(roleKey)
		if err != nil {
			cleanup()
			return nil, nil, err
		}

		roleDir := filepath.Join(runtimeRoot, roleKey)
		dataDir := filepath.Join(roleDir, "data")
		configPath := filepath.Join(roleDir, "config.toml")
		projectName := fmt.Sprintf("%s-%s-project", roomID, roleKey)
		if err := buildRoleRuntimeConfig(templatePath, configPath, dataDir, repoPath, room.Provider, projectName); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("prepare config for %s: %w", roleKey, err)
		}
		logPath := filepath.Join(roleDir, "cc-connect.log")
		logFile, err := openRuntimeLogFile(logPath)
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("open runtime log for %s: %w", roleKey, err)
		}

		cmd := exec.Command(exePath, "-config", configPath)
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		cmd.Dir = filepath.Dir(exePath)
		if err := cmd.Start(); err != nil {
			_ = logFile.Close()
			cleanup()
			return nil, nil, fmt.Errorf("start role %s: %w", roleKey, err)
		}
		go func(c *exec.Cmd, lf *os.File) {
			_ = c.Wait()
			_ = lf.Close()
		}(cmd, logFile)

		socketPath := filepath.Join(dataDir, "run", "api.sock")
		if len(filepath.ToSlash(socketPath)) > 100 {
			_ = killProcess(cmd)
			cleanup()
			return nil, nil, fmt.Errorf("socket path too long for role %s: %s", roleKey, socketPath)
		}
		if err := waitSocketReady(socketPath, 40*time.Second); err != nil {
			_ = killProcess(cmd)
			cleanup()
			return nil, nil, fmt.Errorf("role %s socket not ready: %w", roleKey, err)
		}

		started[roleKey] = cmd
		runtimeMap[roleKey] = SquadRoleRuntime{
			Role:           roleKey,
			DisplayName:    emptyAs(strings.TrimSpace(role.DisplayName), displayName),
			TemplateConfig: templatePath,
			RuntimeConfig:  configPath,
			DataDir:        dataDir,
			SocketPath:     socketPath,
			ProjectName:    projectName,
			PID:            cmd.Process.Pid,
		}
		runtimeRoles = append(runtimeRoles, DebateRole{
			Role:        roleKey,
			DisplayName: emptyAs(strings.TrimSpace(role.DisplayName), displayName),
			Instance:    role.Instance,
			Project:     projectName,
			SocketPath:  socketPath,
			SpeakMode:   role.SpeakMode,
		})
	}
	if len(runtimeRoles) == 0 {
		cleanup()
		return nil, nil, fmt.Errorf("no valid runtime roles were started")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if old := m.rooms[roomID]; len(old) > 0 {
		for _, cmd := range old {
			_ = killProcess(cmd)
		}
	}
	m.rooms[roomID] = started
	return runtimeRoles, runtimeMap, nil
}

func (m *DebateProcessManager) StopRoom(roomID string) error {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return fmt.Errorf("room_id is required")
	}
	m.mu.Lock()
	cmds := m.rooms[roomID]
	delete(m.rooms, roomID)
	m.mu.Unlock()

	var stopErr error
	for _, cmd := range cmds {
		if err := killProcess(cmd); err != nil && stopErr == nil {
			stopErr = err
		}
	}
	return stopErr
}

func (m *DebateProcessManager) StopAll() {
	m.mu.Lock()
	rooms := m.rooms
	m.rooms = make(map[string]map[string]*exec.Cmd)
	m.mu.Unlock()

	for _, cmds := range rooms {
		for _, cmd := range cmds {
			_ = killProcess(cmd)
		}
	}
}

func resolveDebateRuntimeRoot(roomID string, store *DebateStore) string {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return ""
	}
	base := strings.TrimSpace(os.Getenv("CC_DEBATE_RUNTIME_ROOT"))
	if base == "" {
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
			base = filepath.Join(vol+string(os.PathSeparator), "ccdb")
		} else {
			base = filepath.Join(os.TempDir(), "ccdb")
		}
	}
	if !filepath.IsAbs(base) {
		if abs, err := filepath.Abs(base); err == nil {
			base = abs
		}
	}
	return filepath.Join(base, roomID)
}
