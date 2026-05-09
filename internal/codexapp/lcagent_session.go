package codexapp

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
	"strings"
	"sync"
	"time"

	"lcroom/internal/appfs"
	"lcroom/internal/lcagent/modeladapter"
)

const (
	lcagentDefaultAuto = "low"
)

type lcagentSession struct {
	projectPath string
	dataDir     string
	execPath    string
	envFile     string
	auto        string
	notify      func()

	mu                 sync.Mutex
	cmd                *exec.Cmd
	cancel             context.CancelFunc
	threadID           string
	started            bool
	busy               bool
	closed             bool
	busySince          time.Time
	lastBusyActivityAt time.Time
	lastActivityAt     time.Time
	status             string
	lastError          string
	model              string
	modelProvider      string
	reasoningEffort    string
	replayLoaded       bool
	entries            []TranscriptEntry
	revision           uint64
	cache              transcriptExportCache
}

func newLCAgentSession(req LaunchRequest, notify func()) (Session, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	dataDir := strings.TrimSpace(req.AppDataDir)
	if dataDir == "" {
		dataDir = appfs.DefaultDataDir()
	}
	model := strings.TrimSpace(req.PendingModel)
	if model == "" {
		model = modeladapter.DefaultOpenRouterModel
	}
	session := &lcagentSession{
		projectPath:   strings.TrimSpace(req.ProjectPath),
		dataDir:       dataDir,
		execPath:      strings.TrimSpace(req.LCAgentPath),
		envFile:       strings.TrimSpace(req.LCAgentEnvFile),
		auto:          strings.TrimSpace(req.LCAgentAuto),
		notify:        notify,
		model:         model,
		modelProvider: "openrouter",
		status:        "Ready",
	}
	if replay, err := loadLCAgentReplay(req); err != nil {
		return nil, err
	} else if replay != nil {
		session.applyReplay(replay)
	} else if prompt := strings.TrimSpace(req.Prompt); prompt != "" {
		if err := session.Submit(prompt); err != nil {
			return nil, err
		}
	} else {
		session.appendEntryLocked(TranscriptStatus, "LCAgent is ready. Send a prompt to start a one-shot run.")
	}
	return session, nil
}

func (s *lcagentSession) ProjectPath() string {
	if s == nil {
		return ""
	}
	return s.projectPath
}

func (s *lcagentSession) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *lcagentSession) TrySnapshot() (Snapshot, bool) {
	if s == nil || !s.mu.TryLock() {
		return Snapshot{}, false
	}
	defer s.mu.Unlock()
	return s.snapshotLocked(), true
}

func (s *lcagentSession) Submit(prompt string) error {
	return s.SubmitInput(Submission{Text: prompt})
}

func (s *lcagentSession) SubmitInput(input Submission) error {
	if input.Empty() {
		return nil
	}
	return s.startRun(input.TranscriptText(), input.TranscriptDisplayText())
}

func (s *lcagentSession) ShowStatus() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := strings.TrimSpace(s.status)
	if status == "" {
		status = "LCAgent status unavailable"
	}
	s.appendEntryLocked(TranscriptStatus, status)
	return nil
}

func (s *lcagentSession) ShowGoal() error {
	return s.unsupportedNotice("LCAgent goal state is not wired yet.")
}

func (s *lcagentSession) SetGoal(string, *int64) error {
	return s.unsupportedNotice("LCAgent goals are not wired yet.")
}

func (s *lcagentSession) ClearGoal() error {
	return s.unsupportedNotice("LCAgent goals are not wired yet.")
}

func (s *lcagentSession) Compact() error {
	return fmt.Errorf("LCAgent compact is not supported yet")
}

func (s *lcagentSession) Review() error {
	return fmt.Errorf("LCAgent review is not supported yet")
}

func (s *lcagentSession) ListModels() ([]ModelOption, error) {
	return []ModelOption{{
		ID:          modeladapter.DefaultOpenRouterModel,
		Model:       modeladapter.DefaultOpenRouterModel,
		DisplayName: "DeepSeek V4 Pro via OpenRouter",
		Description: "OpenAI-compatible tool-calling model used by the LCAgent spike.",
		IsDefault:   true,
	}}, nil
}

func (s *lcagentSession) StageModelOverride(model, reasoningEffort string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(model) != "" {
		s.model = strings.TrimSpace(model)
	}
	s.reasoningEffort = strings.TrimSpace(reasoningEffort)
	return nil
}

func (s *lcagentSession) Interrupt() error {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (s *lcagentSession) RespondApproval(ApprovalDecision) error {
	return fmt.Errorf("LCAgent approvals are not supported yet")
}

func (s *lcagentSession) RespondToolInput(map[string][]string) error {
	return fmt.Errorf("LCAgent structured input is not supported yet")
}

func (s *lcagentSession) RespondElicitation(ElicitationDecision, json.RawMessage) error {
	return fmt.Errorf("LCAgent elicitation is not supported yet")
}

func (s *lcagentSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.status = "Closed"
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if s.notify != nil {
		s.notify()
	}
	return nil
}

func (s *lcagentSession) startRun(prompt, displayPrompt string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("LCAgent session is closed")
	}
	if s.busy {
		s.mu.Unlock()
		return fmt.Errorf("LCAgent is already running")
	}
	now := time.Now()
	s.started = true
	s.busy = true
	s.busySince = now
	s.lastBusyActivityAt = now
	s.lastActivityAt = now
	s.status = "Starting LCAgent"
	s.lastError = ""
	if s.replayLoaded && len(s.entries) > 0 {
		s.appendEntryLocked(TranscriptStatus, "Starting a new one-shot LCAgent run. Loaded history is display-only and is not sent as model context.")
		s.replayLoaded = false
	}
	s.appendEntryLocked(TranscriptUser, firstNonEmpty(displayPrompt, prompt))
	model := firstNonEmpty(s.model, modeladapter.DefaultOpenRouterModel)
	s.mu.Unlock()

	spec, err := lcagentCommandSpec(s.execPath)
	if err != nil {
		s.finishRun("", false, err)
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	args := append([]string{}, spec.Args...)
	args = append(args,
		"exec",
		"--cwd", s.projectPath,
		"--data-dir", s.dataDir,
		"--auto", lcagentAutoLevel(s.auto),
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", model,
	)
	envFile := firstNonEmpty(s.envFile, os.Getenv("LCROOM_LCAGENT_ENV_FILE"))
	if envFile != "" {
		args = append(args, "--env-file", envFile)
	}
	args = append(args, prompt)
	cmd := exec.CommandContext(ctx, spec.Command, args...)
	cmd.Dir = spec.Dir
	cmd.Env = os.Environ()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		s.finishRun("", false, err)
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		s.finishRun("", false, err)
		return err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		s.finishRun("", false, err)
		return err
	}
	s.mu.Lock()
	s.cmd = cmd
	s.cancel = cancel
	s.status = "LCAgent running"
	s.mu.Unlock()
	if s.notify != nil {
		s.notify()
	}

	go s.readStream(stdout)
	go s.readStderr(stderr)
	go func() {
		err := cmd.Wait()
		cancel()
		if ctx.Err() == context.Canceled && err != nil {
			err = errors.New("LCAgent run interrupted")
		}
		processState := ""
		if cmd.ProcessState != nil {
			processState = cmd.ProcessState.String()
		}
		s.finishRun(processState, err == nil, err)
	}()
	return nil
}

func (s *lcagentSession) readStream(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		s.handleEvent(scanner.Bytes())
	}
	if err := scanner.Err(); err != nil {
		s.appendAsync(TranscriptError, "LCAgent stream error: "+err.Error())
	}
}

func (s *lcagentSession) readStderr(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text != "" {
			s.appendAsync(TranscriptError, text)
		}
	}
}

func (s *lcagentSession) handleEvent(line []byte) {
	var event map[string]json.RawMessage
	if err := json.Unmarshal(line, &event); err != nil {
		s.appendAsync(TranscriptOther, string(line))
		return
	}
	eventType := rawJSONString(event["type"])
	switch eventType {
	case "session_meta":
		id := rawJSONString(event["id"])
		s.mu.Lock()
		if id != "" {
			s.threadID = id
		}
		s.status = "LCAgent session " + firstNonEmpty(id, "started")
		s.touchLocked()
		s.mu.Unlock()
	case "tool_call":
		tool := rawJSONString(event["tool"])
		s.appendAsync(TranscriptTool, "Tool call: "+firstNonEmpty(tool, "unknown"))
	case "tool_result":
		tool := rawJSONString(event["tool"])
		text := lcagentToolResultText(tool, event["result"])
		s.appendAsync(TranscriptTool, text)
	case "plan_update":
		s.appendAsync(TranscriptPlan, lcagentPlanText(event["items"]))
	case "assistant_message":
		if text := rawJSONString(event["message"]); text != "" {
			s.appendAsync(TranscriptAgent, text)
		}
	case "files_touched":
		s.appendAsync(TranscriptFileChange, lcagentFilesTouchedText(event["files"]))
	case "turn_complete":
		s.mu.Lock()
		s.status = "LCAgent run complete"
		s.touchLocked()
		s.mu.Unlock()
	case "turn_aborted":
		reason := rawJSONString(event["reason"])
		if reason == "" {
			reason = "LCAgent run aborted"
		}
		s.mu.Lock()
		s.lastError = reason
		s.status = s.lastError
		s.touchLocked()
		s.mu.Unlock()
		s.appendAsync(TranscriptError, reason)
	}
	if s.notify != nil {
		s.notify()
	}
}

func (s *lcagentSession) finishRun(processState string, ok bool, err error) {
	s.mu.Lock()
	s.busy = false
	s.cancel = nil
	s.cmd = nil
	s.lastBusyActivityAt = time.Now()
	s.lastActivityAt = s.lastBusyActivityAt
	if err != nil {
		s.lastError = err.Error()
		s.status = "LCAgent failed: " + err.Error()
		s.appendEntryLocked(TranscriptError, s.status)
	} else if ok {
		s.status = "LCAgent run complete"
	} else {
		s.status = firstNonEmpty(processState, "LCAgent stopped")
	}
	s.mu.Unlock()
	if s.notify != nil {
		s.notify()
	}
}

func (s *lcagentSession) unsupportedNotice(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendEntryLocked(TranscriptStatus, text)
	return nil
}

func (s *lcagentSession) appendAsync(kind TranscriptKind, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.mu.Lock()
	s.appendEntryLocked(kind, text)
	s.mu.Unlock()
	if s.notify != nil {
		s.notify()
	}
}

func (s *lcagentSession) appendEntryLocked(kind TranscriptKind, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.entries = append(s.entries, TranscriptEntry{
		ItemID: fmt.Sprintf("lcagent-%d", len(s.entries)+1),
		Kind:   kind,
		Text:   text,
	})
	s.touchLocked()
}

func (s *lcagentSession) applyReplay(replay *lcagentReplay) {
	if s == nil || replay == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threadID = strings.TrimSpace(replay.sessionID)
	s.started = true
	s.busy = false
	s.closed = false
	s.busySince = time.Time{}
	s.lastBusyActivityAt = time.Time{}
	s.lastActivityAt = replay.lastActivityAt
	if s.lastActivityAt.IsZero() {
		s.lastActivityAt = replay.startedAt
	}
	if s.lastActivityAt.IsZero() {
		s.lastActivityAt = time.Now()
	}
	s.lastError = strings.TrimSpace(replay.lastError)
	if model := strings.TrimSpace(replay.model); model != "" {
		s.model = model
	}
	if provider := strings.TrimSpace(replay.modelProvider); provider != "" {
		s.modelProvider = provider
	}
	label := firstNonEmpty(s.threadID, "history")
	s.status = "Loaded LCAgent session " + label + " from disk"
	s.replayLoaded = true
	s.entries = nil
	s.revision = 0
	s.cache.ready = false
	replayActivityAt := s.lastActivityAt
	s.appendEntryLocked(TranscriptStatus, "Loaded LCAgent session "+label+" from disk. History is read-only; sending a prompt starts a new one-shot run.")
	for _, entry := range replay.entries {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		s.entries = append(s.entries, TranscriptEntry{
			ItemID:      firstNonEmpty(strings.TrimSpace(entry.ItemID), fmt.Sprintf("lcagent-replay-%d", len(s.entries)+1)),
			Kind:        entry.Kind,
			Text:        text,
			DisplayText: strings.TrimSpace(entry.DisplayText),
		})
	}
	s.lastActivityAt = replayActivityAt
	s.cache.invalidate(&s.revision)
}

func (s *lcagentSession) touchLocked() {
	s.lastActivityAt = time.Now()
	s.revision++
	s.cache.invalidate(nil)
}

func (s *lcagentSession) snapshotLocked() Snapshot {
	entries := cloneTranscriptEntries(s.entries)
	transcript := ""
	if s.cache.ready && s.cache.revision == s.revision {
		transcript = s.cache.transcript
	} else {
		transcript = buildTranscriptText(ProviderLCAgent, entries, "\n", false)
		s.cache.ready = true
		s.cache.revision = s.revision
		s.cache.entries = entries
		s.cache.transcript = transcript
	}
	phase := SessionPhaseIdle
	if s.closed {
		phase = SessionPhaseClosed
	} else if s.busy {
		phase = SessionPhaseRunning
	}
	return Snapshot{
		Provider:           ProviderLCAgent,
		ProjectPath:        s.projectPath,
		ThreadID:           s.threadID,
		TranscriptRevision: s.revision,
		Phase:              phase,
		Started:            s.started,
		Busy:               s.busy,
		BusySince:          s.busySince,
		LastBusyActivityAt: s.lastBusyActivityAt,
		Closed:             s.closed,
		Entries:            cloneTranscriptEntries(entries),
		Transcript:         transcript,
		Status:             s.status,
		LastError:          s.lastError,
		LastActivityAt:     s.lastActivityAt,
		CurrentCWD:         s.projectPath,
		Model:              firstNonEmpty(s.model, modeladapter.DefaultOpenRouterModel),
		ModelProvider:      firstNonEmpty(s.modelProvider, "openrouter"),
		ReasoningEffort:    s.reasoningEffort,
	}
}

type lcagentCommand struct {
	Command string
	Args    []string
	Dir     string
}

func lcagentCommandSpec(configuredPath string) (lcagentCommand, error) {
	if configured := firstNonEmpty(configuredPath, os.Getenv("LCROOM_LCAGENT_PATH")); configured != "" {
		return lcagentCommand{Command: configured}, nil
	}
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), "lcagent")
		if info, statErr := os.Stat(sibling); statErr == nil && !info.IsDir() {
			return lcagentCommand{Command: sibling}, nil
		}
	}
	if path, err := exec.LookPath("lcagent"); err == nil {
		return lcagentCommand{Command: path}, nil
	}
	if wd, err := os.Getwd(); err == nil {
		if _, statErr := os.Stat(filepath.Join(wd, "cmd", "lcagent", "main.go")); statErr == nil {
			return lcagentCommand{Command: "go", Args: []string{"run", "./cmd/lcagent"}, Dir: wd}, nil
		}
	}
	return lcagentCommand{}, fmt.Errorf("lcagent executable not found; set LCROOM_LCAGENT_PATH")
}

func lcagentAutoLevel(configured string) string {
	raw := firstNonEmpty(configured, os.Getenv("LCROOM_LCAGENT_AUTO"))
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "off", "low", "medium":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return lcagentDefaultAuto
	}
}

func rawJSONString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return strings.TrimSpace(value)
}

func lcagentToolResultText(tool string, raw json.RawMessage) string {
	var result struct {
		Success bool   `json:"success"`
		Output  string `json:"output"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "Tool result: " + firstNonEmpty(tool, "unknown")
	}
	label := "Tool result"
	if strings.TrimSpace(tool) != "" {
		label += ": " + strings.TrimSpace(tool)
	}
	if result.Output != "" {
		return label + "\n" + strings.TrimSpace(result.Output)
	}
	if result.Error != "" {
		return label + "\n" + strings.TrimSpace(result.Error)
	}
	return label
}

func lcagentPlanText(raw json.RawMessage) string {
	var items []struct {
		Step   string `json:"step"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		return "Plan updated"
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		step := strings.TrimSpace(item.Step)
		if step == "" {
			continue
		}
		status := strings.TrimSpace(item.Status)
		if status != "" {
			lines = append(lines, status+": "+step)
		} else {
			lines = append(lines, step)
		}
	}
	if len(lines) == 0 {
		return "Plan updated"
	}
	return strings.Join(lines, "\n")
}

func lcagentFilesTouchedText(raw json.RawMessage) string {
	var files []string
	if err := json.Unmarshal(raw, &files); err != nil || len(files) == 0 {
		return "Files touched"
	}
	return "Files touched:\n" + strings.Join(files, "\n")
}
