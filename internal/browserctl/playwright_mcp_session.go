package browserctl

import (
	"bufio"
	"context"
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

var playwrightMCPCommand = "mcp-server-playwright"

type PlaywrightMCPBrowserSession struct {
	cfg    BrowserSessionConfig
	paths  ManagedPlaywrightPaths
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  *bufio.Writer
	pipeIn io.Closer
	respCh map[string]chan playwrightMCPResponse
	seq    uint64
}

func NewPlaywrightMCPBrowserSession(cfg BrowserSessionConfig) (*PlaywrightMCPBrowserSession, error) {
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
	return &PlaywrightMCPBrowserSession{
		cfg:    cfg,
		paths:  paths,
		respCh: map[string]chan playwrightMCPResponse{},
	}, nil
}

func (s *PlaywrightMCPBrowserSession) Navigate(ctx context.Context, url string) (BrowserActionResult, error) {
	text, err := s.callTool(ctx, "browser_navigate", map[string]any{"url": strings.TrimSpace(url)})
	if err != nil {
		return BrowserActionResult{}, err
	}
	return browserActionResultFromMCPText("navigated", text, ""), nil
}

func (s *PlaywrightMCPBrowserSession) Snapshot(ctx context.Context, maxChars int) (BrowserActionResult, error) {
	s.selectNonBlankTabIfCurrentBlank(ctx)
	text, err := s.callTool(ctx, "browser_snapshot", map[string]any{})
	if err != nil {
		return BrowserActionResult{}, err
	}
	if maxChars > 0 && len(text) > maxChars {
		text = text[:maxChars] + "\n[truncated]"
	}
	result := browserActionResultFromMCPText("snapshot", text, "")
	result.Snapshot = strings.TrimSpace(text)
	return result, nil
}

func (s *PlaywrightMCPBrowserSession) Click(ctx context.Context, ref string) (BrowserActionResult, error) {
	text, err := s.callTool(ctx, "browser_click", map[string]any{"element": strings.TrimSpace(ref), "ref": strings.TrimSpace(ref)})
	if err != nil {
		return BrowserActionResult{}, err
	}
	return browserActionResultFromMCPText("clicked", text, ""), nil
}

func (s *PlaywrightMCPBrowserSession) Fill(ctx context.Context, ref, value string) (BrowserActionResult, error) {
	text, err := s.callTool(ctx, "browser_type", map[string]any{
		"element": strings.TrimSpace(ref),
		"ref":     strings.TrimSpace(ref),
		"text":    value,
	})
	if err != nil {
		return BrowserActionResult{}, err
	}
	return browserActionResultFromMCPText("filled", text, ""), nil
}

func (s *PlaywrightMCPBrowserSession) Press(ctx context.Context, key string) (BrowserActionResult, error) {
	text, err := s.callTool(ctx, "browser_press_key", map[string]any{"key": strings.TrimSpace(key)})
	if err != nil {
		return BrowserActionResult{}, err
	}
	return browserActionResultFromMCPText("pressed", text, ""), nil
}

func (s *PlaywrightMCPBrowserSession) FileUpload(ctx context.Context, paths []string) (BrowserActionResult, error) {
	cleaned := make([]string, 0, len(paths))
	for _, path := range paths {
		if path = strings.TrimSpace(path); path != "" {
			cleaned = append(cleaned, path)
		}
	}
	text, err := s.callTool(ctx, "browser_file_upload", map[string]any{"paths": cleaned})
	if err != nil {
		return BrowserActionResult{}, err
	}
	return browserActionResultFromMCPText("file_uploaded", text, ""), nil
}

func (s *PlaywrightMCPBrowserSession) Screenshot(ctx context.Context, path string) (BrowserActionResult, error) {
	args := map[string]any{"fullPage": true}
	requested := strings.TrimSpace(path)
	if requested != "" {
		args["filename"] = requested
	}
	text, err := s.callTool(ctx, "browser_take_screenshot", args)
	if err != nil {
		return BrowserActionResult{}, err
	}
	artifactPath := mcpScreenshotArtifactPath(text, s.paths.OutputDir)
	return browserActionResultFromMCPText("screenshot", text, artifactPath), nil
}

func (s *PlaywrightMCPBrowserSession) CurrentPage(ctx context.Context) (BrowserActionResult, error) {
	s.selectNonBlankTabIfCurrentBlank(ctx)
	text, err := s.callTool(ctx, "browser_snapshot", map[string]any{})
	if err != nil {
		return BrowserActionResult{}, err
	}
	return browserActionResultFromMCPText("current_page", text, ""), nil
}

func (s *PlaywrightMCPBrowserSession) Close() error {
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
			s.markMCPStopped()
			return nil
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			s.markMCPStopped()
			return nil
		}
	}
	s.markMCPStopped()
	return nil
}

func (s *PlaywrightMCPBrowserSession) callTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if err := s.ensureStarted(ctx); err != nil {
		return "", err
	}
	resp, err := s.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return "", err
	}
	if resp.Result.IsError {
		return "", errors.New(resp.Result.Text())
	}
	return resp.Result.Text(), nil
}

func (s *PlaywrightMCPBrowserSession) selectNonBlankTabIfCurrentBlank(ctx context.Context) {
	text, err := s.callTool(ctx, "browser_tabs", map[string]any{"action": "list"})
	if err != nil {
		return
	}
	index, ok := playwrightMCPNonBlankTabToSelect(parsePlaywrightMCPTabs(text))
	if !ok {
		return
	}
	_, _ = s.callTool(ctx, "browser_tabs", map[string]any{"action": "select", "index": index})
}

type playwrightMCPTab struct {
	Index   int
	Current bool
	Title   string
	URL     string
}

func parsePlaywrightMCPTabs(text string) []playwrightMCPTab {
	var tabs []playwrightMCPTab
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(line, "- "))
		indexText, rest, ok := strings.Cut(body, ":")
		if !ok {
			continue
		}
		index, err := strconv.Atoi(strings.TrimSpace(indexText))
		if err != nil {
			continue
		}
		rest = strings.TrimSpace(rest)
		current := false
		if strings.HasPrefix(rest, "(current)") {
			current = true
			rest = strings.TrimSpace(strings.TrimPrefix(rest, "(current)"))
		}
		urlStart := strings.LastIndex(rest, " (")
		if urlStart < 0 || !strings.HasSuffix(rest, ")") {
			continue
		}
		title := strings.TrimSpace(rest[:urlStart])
		title = strings.TrimPrefix(strings.TrimSuffix(title, "]"), "[")
		url := strings.TrimSpace(strings.TrimSuffix(rest[urlStart+2:], ")"))
		tabs = append(tabs, playwrightMCPTab{
			Index:   index,
			Current: current,
			Title:   title,
			URL:     url,
		})
	}
	return tabs
}

func playwrightMCPNonBlankTabToSelect(tabs []playwrightMCPTab) (int, bool) {
	currentIndex := -1
	for i, tab := range tabs {
		if tab.Current {
			currentIndex = i
			break
		}
	}
	if currentIndex >= 0 && !playwrightMCPBlankTabURL(tabs[currentIndex].URL) {
		return 0, false
	}
	if currentIndex < 0 && len(tabs) > 0 && !playwrightMCPBlankTabURL(tabs[0].URL) {
		return 0, false
	}
	for _, tab := range tabs {
		if !playwrightMCPBlankTabURL(tab.URL) {
			return tab.Index, true
		}
	}
	return 0, false
}

func playwrightMCPBlankTabURL(raw string) bool {
	normalized := strings.TrimSpace(strings.ToLower(raw))
	switch normalized {
	case "", "about:blank", "chrome://newtab/", "chrome://new-tab-page/":
		return true
	default:
		return false
	}
}

func (s *PlaywrightMCPBrowserSession) ensureStarted(ctx context.Context) error {
	s.mu.Lock()
	if s.cmd != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	browserPath := s.browserExecutablePath()
	preflight, err := PrepareManagedPlaywrightProfileForLaunch(s.paths, s.browserExecutablePathForCompatibilityCheck(browserPath))
	if err != nil {
		return err
	}
	s.markProfilePreflight(preflight)

	args := []string{"--output-dir", s.paths.OutputDir, "--user-data-dir", s.paths.ProfileDir}
	if s.paths.LaunchMode == ManagedLaunchModeHeadless {
		args = append([]string{"--headless"}, args...)
	}
	if browserPath != "" {
		args = append(args, "--executable-path", browserPath)
	} else if browser := strings.TrimSpace(s.cfg.BrowserChannel); browser != "" {
		args = append(args, "--browser", browser)
	}
	cmd := exec.CommandContext(ctx, playwrightMCPCommand, args...)
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
	s.pipeIn = stdin
	s.stdin = bufio.NewWriter(stdin)
	s.mu.Unlock()

	s.markMCPStarted(cmd.Process.Pid)
	go s.readResponses(stdout)
	go s.monitorMCP(cmd.Process.Pid)
	if _, err := s.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "lcagent", "version": "0"},
	}); err != nil {
		return err
	}
	_, _ = s.notify("notifications/initialized", map[string]any{})
	return nil
}

func (s *PlaywrightMCPBrowserSession) call(ctx context.Context, method string, params map[string]any) (playwrightMCPResponse, error) {
	id := strconv.FormatUint(atomic.AddUint64(&s.seq, 1), 10)
	ch := make(chan playwrightMCPResponse, 1)
	s.mu.Lock()
	s.respCh[id] = ch
	err := json.NewEncoder(s.stdin).Encode(playwrightMCPRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err == nil {
		err = s.stdin.Flush()
	}
	s.mu.Unlock()
	if err != nil {
		s.forget(id)
		return playwrightMCPResponse{}, err
	}
	select {
	case <-ctx.Done():
		s.forget(id)
		return playwrightMCPResponse{}, ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return playwrightMCPResponse{}, fmt.Errorf("playwright MCP server stopped")
		}
		if resp.Error != nil {
			return playwrightMCPResponse{}, errors.New(resp.Error.Message)
		}
		return resp, nil
	}
}

func (s *PlaywrightMCPBrowserSession) notify(method string, params map[string]any) (playwrightMCPResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := json.NewEncoder(s.stdin).Encode(playwrightMCPRequest{JSONRPC: "2.0", Method: method, Params: params})
	if err == nil {
		err = s.stdin.Flush()
	}
	return playwrightMCPResponse{}, err
}

func (s *PlaywrightMCPBrowserSession) readResponses(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var resp playwrightMCPResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			continue
		}
		if strings.TrimSpace(resp.ID) == "" {
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

func (s *PlaywrightMCPBrowserSession) forget(id string) {
	s.mu.Lock()
	delete(s.respCh, id)
	s.mu.Unlock()
}

func (s *PlaywrightMCPBrowserSession) browserExecutablePath() string {
	return managedBrowserExecutablePathForConfig(s.cfg, s.paths.LaunchMode)
}

func (s *PlaywrightMCPBrowserSession) browserExecutablePathForCompatibilityCheck(browserPath string) string {
	if browserPath = strings.TrimSpace(browserPath); browserPath != "" {
		return browserPath
	}
	return managedBrowserExecutablePathForConfigCompatibilityCheck(s.cfg, s.paths.LaunchMode)
}

func (s *PlaywrightMCPBrowserSession) markProfilePreflight(preflight ManagedPlaywrightProfilePreflight) {
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

func (s *PlaywrightMCPBrowserSession) markMCPStarted(pid int) {
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

func (s *PlaywrightMCPBrowserSession) markMCPStopped() {
	_ = WithManagedPlaywrightStateLock(s.paths.DataDir, s.paths.SessionKey, func() error {
		state, err := ReadManagedPlaywrightState(s.paths.DataDir, s.paths.SessionKey)
		if err != nil {
			return nil
		}
		state.MCPPID = 0
		state.BrowserPID = 0
		state.BrowserAppPath = ""
		state.BrowserAppName = ""
		state.BrowserExecutable = ""
		state.RevealSupported = false
		state.UpdatedAt = time.Now().UTC()
		return WriteManagedPlaywrightState(s.paths, state)
	})
}

func (s *PlaywrightMCPBrowserSession) monitorMCP(rootPID int) {
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
		shouldHide := false
		revealed := false
		_ = WithManagedPlaywrightStateLock(s.paths.DataDir, s.paths.SessionKey, func() error {
			state, readErr := ReadManagedPlaywrightState(s.paths.DataDir, s.paths.SessionKey)
			if readErr != nil {
				state = ManagedPlaywrightState{SessionKey: s.paths.SessionKey, ProfileKey: s.paths.ProfileKey, Provider: s.paths.Provider, ProjectPath: s.paths.ProjectPath, LaunchMode: s.paths.LaunchMode, Policy: s.cfg.Policy}
			}
			state.MCPPID = rootPID
			state.BrowserPID = detected.PID
			state.BrowserAppPath = detected.AppPath
			state.BrowserAppName = detected.AppName
			state.BrowserExecutable = detected.ExecutablePath
			state.RevealSupported = detected.PID > 0 || detected.AppPath != "" || detected.AppName != ""
			shouldHide = keepHidden && !hiddenByLCR && !state.Hidden
			revealed = keepHidden && hiddenByLCR && !state.Hidden
			state.UpdatedAt = time.Now().UTC()
			return WriteManagedPlaywrightState(s.paths, state)
		})
		if revealed {
			keepHidden = false
		}
		if shouldHide {
			if hidden, err := HideManagedPlaywrightSession(s.paths.DataDir, s.paths.SessionKey, detected); err == nil && hidden {
				hiddenByLCR = true
			}
		}
	}
}

func browserActionResultFromMCPText(status, text, artifactPath string) BrowserActionResult {
	result := BrowserActionResult{Status: status, Snapshot: strings.TrimSpace(text), ArtifactPath: strings.TrimSpace(artifactPath), Fresh: true}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, "-"))
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "page url", "url":
			result.URL = strings.TrimSpace(value)
		case "page title", "title":
			result.Title = strings.TrimSpace(value)
		}
	}
	return result
}

func mcpScreenshotArtifactPath(text, outputDir string) string {
	for _, line := range strings.Split(text, "\n") {
		for _, field := range strings.Fields(line) {
			cleaned := strings.Trim(field, "`'\".,")
			if strings.HasSuffix(cleaned, ".png") || strings.HasSuffix(cleaned, ".jpg") || strings.HasSuffix(cleaned, ".jpeg") {
				if filepath.IsAbs(cleaned) {
					return cleaned
				}
				return filepath.Join(outputDir, cleaned)
			}
		}
	}
	return ""
}

type playwrightMCPRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      string         `json:"id,omitempty"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type playwrightMCPResponse struct {
	JSONRPC string              `json:"jsonrpc"`
	ID      string              `json:"id"`
	Result  playwrightMCPResult `json:"result"`
	Error   *playwrightMCPError `json:"error,omitempty"`
}

type playwrightMCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type playwrightMCPResult struct {
	Content []playwrightMCPContent `json:"content"`
	IsError bool                   `json:"isError"`
}

func (r playwrightMCPResult) Text() string {
	var lines []string
	for _, content := range r.Content {
		if strings.TrimSpace(content.Text) != "" {
			lines = append(lines, strings.TrimSpace(content.Text))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

type playwrightMCPContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
