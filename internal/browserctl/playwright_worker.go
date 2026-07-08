package browserctl

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed playwright_worker.js
var playwrightWorkerSource string

var (
	playwrightWorkerCommand = "node"
	playwrightWorkerArgs    = func(workerPath string) []string { return []string{workerPath} }
)

type PlaywrightBrowserSession struct {
	cfg    BrowserSessionConfig
	paths  ManagedPlaywrightPaths
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  *bufio.Writer
	pipeIn *os.File
	respCh map[string]chan playwrightWorkerResponse
	done   chan struct{}
	seq    uint64
}

func NewPlaywrightBrowserSession(cfg BrowserSessionConfig) (*PlaywrightBrowserSession, error) {
	cfg.Provider = strings.TrimSpace(strings.ToLower(cfg.Provider))
	if cfg.Provider == "" {
		cfg.Provider = "lcagent"
	}
	cfg.LaunchMode = cfg.LaunchMode.Normalize()
	if cfg.Policy.ManagementMode == "" {
		cfg.Policy = DefaultPolicy()
	} else {
		cfg.Policy = cfg.Policy.Normalize()
	}
	paths, err := ManagedPlaywrightPathsFor(cfg.DataDir, cfg.Provider, cfg.ProjectPath, cfg.SessionKey, cfg.ProfileKey, cfg.LaunchMode)
	if err != nil {
		return nil, err
	}
	state := ManagedPlaywrightState{
		SessionKey:  paths.SessionKey,
		ProfileKey:  paths.ProfileKey,
		Provider:    paths.Provider,
		ProjectPath: paths.ProjectPath,
		LaunchMode:  paths.LaunchMode,
		Policy:      cfg.Policy,
		UpdatedAt:   time.Now().UTC(),
	}
	if err := WriteManagedPlaywrightState(paths, state); err != nil {
		return nil, err
	}
	return &PlaywrightBrowserSession{
		cfg:    cfg,
		paths:  paths,
		respCh: map[string]chan playwrightWorkerResponse{},
		done:   make(chan struct{}),
	}, nil
}

func (s *PlaywrightBrowserSession) Navigate(ctx context.Context, url string) (BrowserActionResult, error) {
	return s.call(ctx, "navigate", map[string]any{"url": strings.TrimSpace(url)})
}

func (s *PlaywrightBrowserSession) Snapshot(ctx context.Context, maxChars int) (BrowserActionResult, error) {
	return s.call(ctx, "snapshot", map[string]any{"max_chars": maxChars})
}

func (s *PlaywrightBrowserSession) Click(ctx context.Context, ref string) (BrowserActionResult, error) {
	return s.call(ctx, "click", map[string]any{"ref": strings.TrimSpace(ref)})
}

func (s *PlaywrightBrowserSession) Fill(ctx context.Context, ref, value string) (BrowserActionResult, error) {
	return s.call(ctx, "fill", map[string]any{"ref": strings.TrimSpace(ref), "value": value})
}

func (s *PlaywrightBrowserSession) Press(ctx context.Context, key string) (BrowserActionResult, error) {
	return s.call(ctx, "press", map[string]any{"key": strings.TrimSpace(key)})
}

func (s *PlaywrightBrowserSession) FileUpload(ctx context.Context, paths []string) (BrowserActionResult, error) {
	return BrowserActionResult{}, fmt.Errorf("file upload is only supported by Playwright MCP browser sessions")
}

func (s *PlaywrightBrowserSession) Screenshot(ctx context.Context, path string) (BrowserActionResult, error) {
	return s.call(ctx, "screenshot", map[string]any{"path": strings.TrimSpace(path)})
}

func (s *PlaywrightBrowserSession) CurrentPage(ctx context.Context) (BrowserActionResult, error) {
	return s.call(ctx, "current_page", map[string]any{})
}

func (s *PlaywrightBrowserSession) SearchGoogle(ctx context.Context, query string, maxResults int, site string, recencyDays int) (BrowserActionResult, error) {
	return s.call(ctx, "search_google", map[string]any{
		"query":        strings.TrimSpace(query),
		"max_results":  maxResults,
		"site":         strings.TrimSpace(site),
		"recency_days": recencyDays,
	})
}

func (s *PlaywrightBrowserSession) Close() error {
	s.mu.Lock()
	cmd := s.cmd
	pipeIn := s.pipeIn
	s.cmd = nil
	s.pipeIn = nil
	s.stdin = nil
	s.mu.Unlock()
	if pipeIn != nil {
		_ = pipeIn.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
			s.markWorkerStopped()
			return nil
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			s.markWorkerStopped()
			return nil
		}
	}
	s.markWorkerStopped()
	return nil
}

func (s *PlaywrightBrowserSession) call(ctx context.Context, method string, params map[string]any) (BrowserActionResult, error) {
	if err := s.ensureStarted(ctx); err != nil {
		return BrowserActionResult{}, err
	}
	id := strconv.FormatUint(atomic.AddUint64(&s.seq, 1), 10)
	ch := make(chan playwrightWorkerResponse, 1)
	s.mu.Lock()
	s.respCh[id] = ch
	err := json.NewEncoder(s.stdin).Encode(playwrightWorkerRequest{ID: id, Method: method, Params: params})
	if err == nil {
		err = s.stdin.Flush()
	}
	s.mu.Unlock()
	if err != nil {
		s.forget(id)
		return BrowserActionResult{}, err
	}
	select {
	case <-ctx.Done():
		s.forget(id)
		return BrowserActionResult{}, ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return BrowserActionResult{}, fmt.Errorf("browser worker stopped")
		}
		if !resp.OK {
			return BrowserActionResult{}, errors.New(resp.Error)
		}
		return resp.Result, nil
	}
}

func (s *PlaywrightBrowserSession) ensureStarted(ctx context.Context) error {
	s.mu.Lock()
	if s.cmd != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	workerPath, err := writePlaywrightWorkerFile()
	if err != nil {
		return err
	}
	browserChannel := s.browserChannel()
	preflight, err := PrepareManagedPlaywrightProfileForLaunch(s.paths, s.browserExecutablePathForCompatibilityCheck(browserChannel))
	if err != nil {
		return err
	}
	s.markProfilePreflight(preflight)

	config := map[string]any{
		"profileDir":     s.paths.ProfileDir,
		"outputDir":      s.paths.OutputDir,
		"launchMode":     string(s.paths.LaunchMode),
		"browserChannel": browserChannel,
	}
	configRaw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, playwrightWorkerCommand, playwrightWorkerArgs(workerPath)...)
	cmd.Env = appendBrowserWorkerEnv(os.Environ(), string(configRaw), s.paths.ProjectPath)
	if info, statErr := os.Stat(s.paths.ProjectPath); statErr == nil && info.IsDir() {
		cmd.Dir = s.paths.ProjectPath
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	s.mu.Lock()
	if s.cmd != nil {
		s.mu.Unlock()
		_ = cmd.Process.Kill()
		return nil
	}
	s.cmd = cmd
	if file, ok := stdin.(*os.File); ok {
		s.pipeIn = file
	}
	s.stdin = bufio.NewWriter(stdin)
	s.mu.Unlock()

	s.markWorkerStarted(cmd.Process.Pid)
	go s.readResponses(stdout)
	go s.monitorWorker(cmd.Process.Pid)
	return nil
}

func (s *PlaywrightBrowserSession) browserChannel() string {
	if channel := strings.TrimSpace(os.Getenv("LCR_PLAYWRIGHT_BROWSER_CHANNEL")); channel != "" {
		return channel
	}
	if channel := strings.TrimSpace(s.cfg.BrowserChannel); channel != "" {
		return channel
	}
	switch s.paths.LaunchMode.Normalize() {
	case ManagedLaunchModeHeaded, ManagedLaunchModeBackground:
		return "chrome"
	default:
		return ""
	}
}

func (s *PlaywrightBrowserSession) browserExecutablePathForCompatibilityCheck(browserChannel string) string {
	if strings.TrimSpace(browserChannel) != "" {
		return ""
	}
	if s.paths.LaunchMode.Normalize() == ManagedLaunchModeHeadless {
		return installedPlaywrightChromiumExecutable()
	}
	return ""
}

func appendBrowserWorkerEnv(base []string, configRaw, projectPath string) []string {
	env := append([]string(nil), base...)
	env = append(env, "LCR_BROWSER_WORKER_CONFIG="+configRaw)
	paths := []string{}
	if existing := strings.TrimSpace(os.Getenv("NODE_PATH")); existing != "" {
		paths = append(paths, filepath.SplitList(existing)...)
	}
	if strings.TrimSpace(projectPath) != "" {
		paths = append(paths, filepath.Join(projectPath, "node_modules"))
	}
	if globalRoot, err := exec.Command("npm", "root", "-g").Output(); err == nil {
		root := strings.TrimSpace(string(globalRoot))
		if root != "" {
			paths = append(paths, root, filepath.Join(root, "@playwright", "mcp", "node_modules"))
		}
	}
	if mcpPath, err := exec.LookPath("mcp-server-playwright"); err == nil {
		if realPath, evalErr := filepath.EvalSymlinks(mcpPath); evalErr == nil {
			dir := filepath.Dir(realPath)
			paths = append(paths, filepath.Join(dir, "node_modules"))
		}
	}
	if len(paths) > 0 {
		env = append(env, "NODE_PATH="+strings.Join(uniqueCleanPaths(paths), string(filepath.ListSeparator)))
	}
	return env
}

func uniqueCleanPaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, value := range paths {
		path := strings.TrimSpace(value)
		if path == "" {
			continue
		}
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func writePlaywrightWorkerFile() (string, error) {
	dir := filepath.Join(os.TempDir(), "lcroom-browserctl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "playwright_worker.js")
	if err := os.WriteFile(path, []byte(playwrightWorkerSource), 0o755); err != nil {
		return "", err
	}
	return path, nil
}

func (s *PlaywrightBrowserSession) readResponses(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var resp playwrightWorkerResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			continue
		}
		s.mu.Lock()
		ch := s.respCh[resp.ID]
		delete(s.respCh, resp.ID)
		s.mu.Unlock()
		if ch != nil {
			ch <- resp
			close(ch)
		}
	}
	s.mu.Lock()
	for id, ch := range s.respCh {
		delete(s.respCh, id)
		close(ch)
	}
	s.mu.Unlock()
}

func (s *PlaywrightBrowserSession) forget(id string) {
	s.mu.Lock()
	delete(s.respCh, id)
	s.mu.Unlock()
}

func (s *PlaywrightBrowserSession) markWorkerStarted(pid int) {
	_ = WithManagedPlaywrightStateLock(s.paths.DataDir, s.paths.SessionKey, func() error {
		state, err := ReadManagedPlaywrightState(s.paths.DataDir, s.paths.SessionKey)
		if err != nil {
			state = ManagedPlaywrightState{SessionKey: s.paths.SessionKey, ProfileKey: s.paths.ProfileKey, Provider: s.paths.Provider, ProjectPath: s.paths.ProjectPath, LaunchMode: s.paths.LaunchMode, Policy: s.cfg.Policy}
		}
		state.MCPPID = pid
		state.UpdatedAt = time.Now().UTC()
		return WriteManagedPlaywrightState(s.paths, state)
	})
}

func (s *PlaywrightBrowserSession) markProfilePreflight(preflight ManagedPlaywrightProfilePreflight) {
	if preflight.ProfileBackupPath == "" && preflight.RecoveryReason() == "" {
		return
	}
	_ = WithManagedPlaywrightStateLock(s.paths.DataDir, s.paths.SessionKey, func() error {
		state, err := ReadManagedPlaywrightState(s.paths.DataDir, s.paths.SessionKey)
		if err != nil {
			state = ManagedPlaywrightState{SessionKey: s.paths.SessionKey, ProfileKey: s.paths.ProfileKey, Provider: s.paths.Provider, ProjectPath: s.paths.ProjectPath, LaunchMode: s.paths.LaunchMode, Policy: s.cfg.Policy}
		}
		state.ProfileBackupPath = preflight.ProfileBackupPath
		state.ProfileRecoveryReason = preflight.RecoveryReason()
		state.UpdatedAt = time.Now().UTC()
		return WriteManagedPlaywrightState(s.paths, state)
	})
}

func (s *PlaywrightBrowserSession) markWorkerStopped() {
	_ = WithManagedPlaywrightStateLock(s.paths.DataDir, s.paths.SessionKey, func() error {
		state, err := ReadManagedPlaywrightState(s.paths.DataDir, s.paths.SessionKey)
		if err != nil {
			return nil
		}
		state.MCPPID = 0
		state.UpdatedAt = time.Now().UTC()
		return WriteManagedPlaywrightState(s.paths, state)
	})
}

func (s *PlaywrightBrowserSession) monitorWorker(rootPID int) {
	if rootPID <= 0 {
		return
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	keepHidden := s.paths.LaunchMode == ManagedLaunchModeBackground
	hiddenByLCR := false
	for range ticker.C {
		s.mu.Lock()
		running := s.cmd != nil
		s.mu.Unlock()
		if !running {
			return
		}
		detected, ok, err := DetectManagedBrowserProcess(rootPID)
		if err != nil || !ok {
			continue
		}
		_ = WithManagedPlaywrightStateLock(s.paths.DataDir, s.paths.SessionKey, func() error {
			state, readErr := ReadManagedPlaywrightState(s.paths.DataDir, s.paths.SessionKey)
			if readErr != nil {
				state = ManagedPlaywrightState{SessionKey: s.paths.SessionKey, ProfileKey: s.paths.ProfileKey, Provider: s.paths.Provider, ProjectPath: s.paths.ProjectPath, LaunchMode: s.paths.LaunchMode, Policy: s.cfg.Policy}
			}
			state.BrowserPID = detected.PID
			state.BrowserAppPath = detected.AppPath
			state.BrowserAppName = detected.AppName
			state.BrowserExecutable = detected.ExecutablePath
			state.RevealSupported = detected.PID > 0 || detected.AppPath != "" || detected.AppName != ""
			if keepHidden && !hiddenByLCR && !state.Hidden {
				if err := HideManagedBrowserProcess(detected.PID); err == nil {
					state.Hidden = true
					hiddenByLCR = true
				}
			}
			if keepHidden && hiddenByLCR && !state.Hidden {
				keepHidden = false
			}
			state.UpdatedAt = time.Now().UTC()
			return WriteManagedPlaywrightState(s.paths, state)
		})
	}
}

type playwrightWorkerRequest struct {
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type playwrightWorkerResponse struct {
	ID     string              `json:"id"`
	OK     bool                `json:"ok"`
	Result BrowserActionResult `json:"result,omitempty"`
	Error  string              `json:"error,omitempty"`
}
