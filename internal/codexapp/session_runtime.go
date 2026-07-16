package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/browserctl"
	"lcroom/internal/codexcli"
)

const codexResumeInitialTurnLimit = 64

func (s *appServerSession) start(req LaunchRequest) error {
	cmd := exec.Command("codex", "app-server")
	cmd.Dir = req.ProjectPath
	applyCodexMCPOverrides(cmd, req)
	configureAppServerCommand(cmd)
	applyPlaywrightPolicyEnvironment(cmd, ProviderCodex, req.PlaywrightPolicy)
	sourceHome, err := effectiveCodexHome(req.CodexHome)
	if err != nil {
		return err
	}
	if _, err := sanitizeCodexStateRolloutPathsOnce(sourceHome); err != nil {
		s.appendCodexHomeCleanupWarning(sourceHome, err)
	}
	codexHomeOverlay, err := prepareCodexHomeOverlayForLaunch(
		req.AppDataDir,
		sourceHome,
		shouldShadowPlaywrightSkill(req.PlaywrightPolicy),
		shouldShadowRuntimeSkill(req),
	)
	if err != nil {
		return err
	}
	s.codexHomeOverlay = codexHomeOverlay
	cmd.Env = withEnvOverride(cmd.Env, "CODEX_HOME", codexHomeOverlay)
	applyCodexDirectRMGuardEnvironment(cmd, codexHomeOverlay)
	compatibility := codexcli.ApplyCodeModeHostCompatibility(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	s.cmd = cmd
	s.stdin = stdin

	if err := cmd.Start(); err != nil {
		return err
	}

	go s.readStdout(stdout)
	go s.readStderr(stderr)
	go s.waitForExit()

	// Resuming a thread initializes its configured MCP services before the
	// app-server answers. That startup path is materially slower than an
	// ordinary RPC, especially when several sessions are restored after an LCR
	// restart, so it gets its own timeout budget.
	ctx, cancel := context.WithTimeout(context.Background(), appServerStartupTimeout)
	defer cancel()

	if _, err := s.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "little_control_room",
			"title":   "Little Control Room",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}); err != nil {
		return err
	}
	if err := s.send(rpcNotification{Method: "initialized", Params: map[string]any{}}); err != nil {
		return err
	}

	var threadID string
	if !req.ForceNew && strings.TrimSpace(req.ResumeID) != "" {
		threadID, err = s.resumeThread(ctx, req.ResumeID)
		if err != nil {
			s.appendSystemNotice("Resume failed, starting a new Codex thread.")
		}
	}
	if threadID == "" {
		threadID, err = s.startThread(ctx)
		if err != nil {
			return err
		}
		if req.ForceNew {
			if err := s.ensureFreshThread(ctx, threadID); err != nil {
				return err
			}
		}
	}

	s.mu.Lock()
	s.threadID = threadID
	s.started = true
	s.pendingModel, s.pendingReasoning = stagedModelOverride(s.model, s.reasoningEffort, req.PendingModel, req.PendingReasoning)
	s.status = ""
	s.mu.Unlock()
	s.notify()
	if compatibility.CodeModeHostDisabled {
		s.appendSystemNotice(codexCodeModeHostFallback)
	}

	if err := s.refreshGoalState(ctx, threadID); err != nil {
		s.appendSystemNotice("Embedded Codex goal status could not refresh: " + err.Error())
	}

	if initialInput := launchRequestInitialInput(req); !initialInput.Empty() {
		if req.ContinueInterruptedTurn {
			return s.continueInterruptedTurn(req.InterruptedTurnID, initialInput)
		}
		if snapshot := s.Snapshot(); snapshot.BusyExternal {
			s.appendSystemNotice("This Codex session is already active in another process. The embedded prompt was not sent; use /codex-new for a separate session.")
			return nil
		}
		return s.SubmitInput(initialInput)
	}
	return nil
}

func (s *appServerSession) appendCodexHomeCleanupWarning(codexHome string, err error) {
	if err == nil {
		return
	}
	log.Printf("WARN codexapp: %s codex_home=%q err=%v", codexHomeCleanupWarning, strings.TrimSpace(codexHome), err)
	s.appendSystemNotice(codexHomeCleanupWarning)
}

func (s *appServerSession) ensureFreshThread(ctx context.Context, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	thread, err := s.readThreadState(ctx, threadID)
	if err != nil {
		if isFreshThreadUnmaterializedError(err) {
			return nil
		}
		return err
	}
	if threadHasRetainedHistory(thread) {
		return &ForceNewSessionReusedError{Provider: ProviderCodex, ThreadID: threadID}
	}
	return nil
}

func (s *appServerSession) startThread(ctx context.Context) (string, error) {
	result, err := s.call(ctx, "thread/start", threadStartParams{
		CWD:            s.projectPath,
		ApprovalPolicy: approvalPolicyForPreset(s.preset),
		Sandbox:        sandboxModeForPreset(s.preset),
		ServiceName:    "little-control-room",
	})
	if err != nil {
		return "", err
	}
	var response threadResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.Thread.ID) == "" {
		return "", fmt.Errorf("thread/start returned no thread id")
	}
	s.mu.Lock()
	s.applyThreadConfigLocked(response.ApprovalPolicy, response.CWD, response.Model, response.ModelProvider, stringValue(response.ReasoningEffort), stringValue(response.ServiceTier), response.Sandbox)
	s.mu.Unlock()
	s.appendSystemNotice("Started a new embedded Codex session " + shortID(response.Thread.ID) + ".")
	return response.Thread.ID, nil
}

func (s *appServerSession) resumeThread(ctx context.Context, threadID string) (string, error) {
	requestedThreadID := strings.TrimSpace(threadID)
	response, err := s.resumeThreadResponse(ctx, requestedThreadID)
	if err != nil {
		return "", err
	}
	rootThreadID := strings.TrimSpace(response.Thread.SessionID)
	if rootThreadID != "" && rootThreadID != strings.TrimSpace(response.Thread.ID) {
		response, err = s.resumeThreadResponse(ctx, rootThreadID)
		if err != nil {
			return "", fmt.Errorf("resume root Codex thread %s for sub-agent thread %s: %w", shortID(rootThreadID), shortID(requestedThreadID), err)
		}
		s.mu.Lock()
		if strings.TrimSpace(s.reconnectThreadID) == requestedThreadID {
			s.reconnectThreadID = rootThreadID
		}
		s.mu.Unlock()
	}
	if strings.TrimSpace(response.Thread.ID) == "" {
		return "", fmt.Errorf("thread/resume returned no thread id")
	}
	s.mu.Lock()
	s.applyThreadConfigLocked(response.ApprovalPolicy, response.CWD, response.Model, response.ModelProvider, stringValue(response.ReasoningEffort), stringValue(response.ServiceTier), response.Sandbox)
	s.mu.Unlock()
	s.initializeHistoryPagination(response.Thread)
	s.hydrateResumedThread(response.Thread)
	if snapshot := s.Snapshot(); snapshot.BusyExternal {
		s.appendSystemNotice("Resumed embedded Codex session " + shortID(response.Thread.ID) + ". It is already active in another Codex process, so embedded controls are read-only until it finishes.")
	} else {
		s.appendSystemNotice("Resumed embedded Codex session " + shortID(response.Thread.ID) + ".")
	}
	return response.Thread.ID, nil
}

func (s *appServerSession) resumeThreadResponse(ctx context.Context, threadID string) (threadResumeResponse, error) {
	params := threadResumeParams{
		ThreadID:       threadID,
		ApprovalPolicy: approvalPolicyForPreset(s.preset),
		Sandbox:        sandboxModeForPreset(s.preset),
		ExcludeTurns:   true,
		InitialTurnsPage: &threadResumeInitialTurnsPageParams{
			Limit:         codexResumeInitialTurnLimit,
			SortDirection: "desc",
			ItemsView:     "summary",
		},
	}
	result, err := s.call(ctx, "thread/resume", params)
	if err != nil {
		// Older app-server versions may reject the bounded-history fields. Keep
		// the legacy request as a compatibility fallback instead of turning a
		// valid saved session into a fresh thread.
		params.ExcludeTurns = false
		params.InitialTurnsPage = nil
		result, err = s.call(ctx, "thread/resume", params)
		if err != nil {
			return threadResumeResponse{}, err
		}
	}
	var response threadResumeResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return threadResumeResponse{}, err
	}
	applyInitialTurnsPage(&response)
	return response, nil
}

func applyInitialTurnsPage(response *threadResumeResponse) {
	if response == nil || response.InitialTurnsPage == nil {
		return
	}
	page := response.InitialTurnsPage
	turns := make([]resumedTurn, len(page.Data))
	for i := range page.Data {
		// The bootstrap page is requested newest-first so app-server can stop
		// after a bounded tail. Transcript hydration expects chronological order.
		turns[len(page.Data)-1-i] = page.Data[i]
	}
	response.Thread.Turns = turns
	response.Thread.HistoryTruncated = page.NextCursor != nil && strings.TrimSpace(*page.NextCursor) != ""
	response.Thread.HistorySummaryOnly = len(turns) > 0 || response.Thread.HistoryTruncated
	if page.NextCursor != nil {
		response.Thread.HistoryNextCursor = strings.TrimSpace(*page.NextCursor)
	}
}

func (s *appServerSession) RefreshBusyElsewhere() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	if !s.busyExternal {
		s.mu.Unlock()
		return nil
	}
	threadID := strings.TrimSpace(s.threadID)
	s.touchLocked()
	s.mu.Unlock()

	if threadID == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()

	response, err := s.resumeThreadResponse(ctx, threadID)
	if err != nil {
		s.appendSystemError(err)
		return err
	}

	s.mu.Lock()
	wasBusyExternal := s.busyExternal
	s.applyThreadConfigLocked(response.ApprovalPolicy, response.CWD, response.Model, response.ModelProvider, stringValue(response.ReasoningEffort), stringValue(response.ServiceTier), response.Sandbox)
	s.hydrateResumedThreadLocked(response.Thread)
	noticeID := shortID(firstNonEmpty(response.Thread.ID, threadID))
	switch {
	case wasBusyExternal && !s.busyExternal:
		message := "Embedded Codex session " + noticeID + " is no longer active in another Codex process. Embedded controls are live again."
		s.appendEntryLocked("", TranscriptSystem, message)
		s.lastSystemNotice = message
		s.status = message
	case s.busyExternal:
		message := "Embedded Codex session " + noticeID + " is already active in another Codex process, so embedded controls are read-only until it finishes."
		s.lastSystemNotice = message
		s.status = message
	default:
		s.status = "Codex session ready"
	}
	s.mu.Unlock()
	return nil
}

func (s *appServerSession) readThreadState(ctx context.Context, threadID string) (resumedThread, error) {
	result, err := s.call(ctx, "thread/read", threadReadParams{
		ThreadID:     strings.TrimSpace(threadID),
		IncludeTurns: true,
	})
	if err != nil {
		return resumedThread{}, err
	}
	var response threadReadResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return resumedThread{}, err
	}
	if strings.TrimSpace(response.Thread.ID) == "" {
		return resumedThread{}, fmt.Errorf("thread/read returned no thread id")
	}
	return response.Thread, nil
}

func (s *appServerSession) readThreadStatus(ctx context.Context, threadID string) (resumedThreadStatus, error) {
	result, err := s.call(ctx, "thread/read", threadReadParams{
		ThreadID: strings.TrimSpace(threadID),
	})
	if err != nil {
		return resumedThreadStatus{}, err
	}
	var response threadReadResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return resumedThreadStatus{}, err
	}
	if strings.TrimSpace(response.Thread.ID) == "" {
		return resumedThreadStatus{}, fmt.Errorf("thread/read returned no thread id")
	}
	status := response.Thread.Status
	if strings.TrimSpace(status.Type) == "" {
		status = effectiveThreadStatus(response.Thread)
	}
	return status, nil
}

func (s *appServerSession) canRefreshThreadStateLocked() bool {
	return !s.closed && (s.rpcCallHook != nil || s.stdin != nil)
}

func (s *appServerSession) scheduleGeneratedImageArtifactRefresh(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	delay := generatedImageArtifactRefreshDelay
	exitCh := s.exitCh
	go func() {
		if delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-exitCh:
				return
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		if err := s.mergeThreadArtifacts(ctx, threadID); err != nil {
			log.Printf("WARN codexapp: refresh generated image artifacts thread_id=%q err=%v", threadID, err)
			return
		}
		s.notify()
	}()
}

func (s *appServerSession) mergeThreadArtifacts(ctx context.Context, threadID string) error {
	thread, err := s.readThreadState(ctx, threadID)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	currentThreadID := strings.TrimSpace(s.threadID)
	readThreadID := strings.TrimSpace(thread.ID)
	if currentThreadID != "" && readThreadID != "" && currentThreadID != readThreadID {
		return nil
	}
	s.mergeResumedThreadItemsLocked(thread)
	return nil
}

func (s *appServerSession) ReconcileBusyState() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	if s.busyExternal || !s.busy || s.pendingApproval != nil || s.pendingToolInput != nil || s.pendingElicitation != nil || s.reconciling {
		s.mu.Unlock()
		return nil
	}
	threadID := strings.TrimSpace(s.threadID)
	if threadID == "" {
		s.mu.Unlock()
		return nil
	}
	s.reconciling = true
	s.mu.Unlock()
	s.notify()

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()

	status, err := s.readThreadStatus(ctx, threadID)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.reconciling = false
	if err == nil {
		activeTurnID := strings.TrimSpace(s.activeTurnID)
		if strings.TrimSpace(status.Type) == "active" && s.activeTurnLooksStuckLocked(activeTurnID, time.Now()) {
			s.noteSuspectedBusyStallLocked()
			if !s.stalled {
				s.status = "Codex is working..."
			}
		} else {
			s.stalled = false
			s.stallCount = 0
			if strings.TrimSpace(status.Type) == "active" && s.busy {
				s.status = "Codex is working..."
				if s.lastSystemNotice == codexReconnectSuggestion {
					s.lastSystemNotice = ""
				}
			}
		}
		s.syncThreadStatusLocked(threadID, status, true)
	} else {
		s.stallCount++
		s.lastError = err.Error()
		if s.stallCount >= busyStateStallAfter {
			if !s.stalled {
				s.appendEntryLocked("", TranscriptSystem, codexReconnectSuggestion)
			}
			s.stalled = true
			s.status = codexReconnectSuggestion
			s.lastSystemNotice = codexReconnectSuggestion
		}
	}
	s.mu.Unlock()
	s.notify()
	return err
}

func (s *appServerSession) readStdout(r io.Reader) {
	err := readLines(r, func(line []byte) {
		var env rpcEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			s.appendSystemError(fmt.Errorf("invalid app-server message: %w", err))
			return
		}
		s.routeEnvelope(env)
	})
	if err != nil {
		s.handleTransportFailure(fmt.Errorf("app-server stream error: %w", err))
	}
}

func (s *appServerSession) readStderr(r io.Reader) {
	err := readLines(r, func(raw []byte) {
		line := strings.TrimSpace(string(raw))
		if line == "" {
			return
		}
		s.appendSystemNotice("codex stderr: " + line)
		s.maybeAppendCodeModeHostDiagnosis(line)
		s.maybeAppendAuth403Diagnosis(line)
	})
	if err != nil {
		s.appendSystemNotice("codex stderr stream error: " + err.Error())
	}
}

func (s *appServerSession) waitForExit() {
	if s.cmd == nil {
		s.closeExitCh()
		return
	}
	err := s.cmd.Wait()
	s.closeExitCh()
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.clearActiveStateLocked()
		if err != nil {
			s.lastError = err.Error()
			s.status = "Codex app-server exited with error"
			s.lastSystemNotice = "Codex app-server exited with error"
		} else {
			s.status = "Codex app-server exited"
			s.lastSystemNotice = "Codex app-server exited"
		}
	}
	s.mu.Unlock()
	s.failPending("session exited")
	s.notify()
}

func (s *appServerSession) closeExitCh() {
	if s == nil {
		return
	}
	s.exitOnce.Do(func() {
		if s.exitCh != nil {
			close(s.exitCh)
		}
	})
}

func (s *appServerSession) routeEnvelope(env rpcEnvelope) {
	if env.Method != "" && len(env.ID) > 0 {
		s.handleServerRequest(env)
		return
	}
	if env.Method != "" {
		s.handleNotification(env.Method, env.Params)
		return
	}
	if len(env.ID) == 0 {
		return
	}
	s.pendingMu.Lock()
	ch, ok := s.pending[idKey(env.ID)]
	if ok {
		delete(s.pending, idKey(env.ID))
	}
	s.pendingMu.Unlock()
	if ok {
		ch <- env
	}
}

func (s *appServerSession) handleServerRequest(env rpcEnvelope) {
	switch env.Method {
	case "item/commandExecution/requestApproval":
		var params commandApprovalParams
		if err := json.Unmarshal(env.Params, &params); err != nil {
			s.appendSystemError(err)
			return
		}
		params.RequestID = idKey(env.ID)
		s.mu.Lock()
		s.touchLocked()
		s.pendingApproval = &ApprovalRequest{
			ID:       params.RequestID,
			Kind:     ApprovalCommandExecution,
			ThreadID: params.ThreadID,
			TurnID:   params.TurnID,
			ItemID:   params.ItemID,
			Command:  params.Command,
			CWD:      params.CWD,
			Reason:   params.Reason,
		}
		s.status = "Waiting for command approval"
		s.lastSystemNotice = "Codex requested command approval"
		s.mu.Unlock()
		s.notify()
	case "item/fileChange/requestApproval":
		var params fileChangeApprovalParams
		if err := json.Unmarshal(env.Params, &params); err != nil {
			s.appendSystemError(err)
			return
		}
		params.RequestID = idKey(env.ID)
		s.mu.Lock()
		s.touchLocked()
		s.pendingApproval = &ApprovalRequest{
			ID:        params.RequestID,
			Kind:      ApprovalFileChange,
			ThreadID:  params.ThreadID,
			TurnID:    params.TurnID,
			ItemID:    params.ItemID,
			Reason:    params.Reason,
			GrantRoot: params.GrantRoot,
		}
		s.status = "Waiting for file change approval"
		s.lastSystemNotice = "Codex requested file change approval"
		s.mu.Unlock()
		s.notify()
	case "item/tool/requestUserInput":
		var params toolRequestUserInputParams
		if err := json.Unmarshal(env.Params, &params); err != nil {
			s.appendSystemError(err)
			_ = s.respondRequestError(env.ID, -32602, "invalid tool input request")
			return
		}
		params.RequestID = idKey(env.ID)
		request := &ToolInputRequest{
			ID:       params.RequestID,
			ThreadID: params.ThreadID,
			TurnID:   params.TurnID,
			ItemID:   params.ItemID,
		}
		for _, question := range params.Questions {
			options := make([]ToolInputOption, 0, len(question.Options))
			for _, option := range question.Options {
				options = append(options, ToolInputOption{
					Label:       option.Label,
					Description: option.Description,
				})
			}
			request.Questions = append(request.Questions, ToolInputQuestion{
				Header:   question.Header,
				ID:       question.ID,
				Question: question.Question,
				IsOther:  question.IsOther,
				IsSecret: question.IsSecret,
				Options:  options,
			})
		}
		s.mu.Lock()
		s.touchLocked()
		s.pendingToolInput = request
		s.status = "Waiting for structured user input"
		s.lastSystemNotice = "Codex requested structured user input"
		s.mu.Unlock()
		s.notify()
	case "mcpServer/elicitation/request":
		var params mcpServerElicitationRequestParams
		if err := json.Unmarshal(env.Params, &params); err != nil {
			s.appendSystemError(err)
			_ = s.respondRequestError(env.ID, -32602, "invalid MCP elicitation request")
			return
		}
		params.RequestID = idKey(env.ID)
		request := &ElicitationRequest{
			ID:              params.RequestID,
			ServerName:      params.ServerName,
			ThreadID:        params.ThreadID,
			TurnID:          params.TurnID,
			Mode:            ElicitationMode(params.Mode),
			Message:         params.Message,
			URL:             params.URL,
			ElicitationID:   params.ElicitationID,
			RequestedSchema: params.RequestedSchema,
		}
		s.mu.Lock()
		s.touchLocked()
		s.pendingElicitation = request
		s.refreshBrowserActivityLocked(time.Now())
		if browserctl.IsPlaywrightToolCall(request.ServerName, "") {
			s.status = "Browser needs attention"
			s.lastSystemNotice = "Playwright requested browser input"
		} else {
			s.status = "Waiting for MCP input"
			s.lastSystemNotice = "MCP server requested input"
		}
		s.mu.Unlock()
		s.notify()
	default:
		s.appendSystemNotice("Unsupported app-server request: " + env.Method)
		_ = s.respondRequestError(env.ID, -32601, "unsupported request: "+env.Method)
	}
}

func (s *appServerSession) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "thread/status/changed":
		var msg threadStatusChangedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		s.touchLocked()
		s.syncThreadStatusLocked(msg.ThreadID, msg.Status, false)
		s.mu.Unlock()
		s.notify()
	case "thread/compacted":
		var msg threadCompactedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		s.touchLocked()
		if strings.TrimSpace(s.threadID) != "" && strings.TrimSpace(msg.ThreadID) != "" && strings.TrimSpace(s.threadID) != strings.TrimSpace(msg.ThreadID) {
			s.mu.Unlock()
			return
		}
		hadCompactionState := s.compacting || s.contextCompactionActive
		s.compacting = false
		s.contextCompactionActive = false
		if len(s.activeCompactionItems) > 0 {
			for itemID := range s.activeCompactionItems {
				delete(s.activeItems, itemID)
			}
			s.activeCompactionItems = nil
		} else if hadCompactionState {
			s.activeItems = nil
		}
		if hadCompactionState && len(s.activeItems) == 0 {
			s.clearBusyLocked("")
		}
		s.status = "Conversation history compacted"
		s.lastSystemNotice = "Conversation history compacted"
		s.mu.Unlock()
		s.notify()
	case "thread/goal/updated":
		var msg threadGoalUpdatedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		s.touchLocked()
		if current := strings.TrimSpace(s.threadID); current == "" || strings.TrimSpace(msg.ThreadID) == "" || current == strings.TrimSpace(msg.ThreadID) {
			s.goal = exportedThreadGoal(&msg.Goal)
			if s.goal != nil {
				switch s.goal.Status {
				case ThreadGoalStatusPaused:
					s.lastSystemNotice = "Embedded Codex goal paused"
				case ThreadGoalStatusBlocked:
					s.lastSystemNotice = "Embedded Codex goal blocked"
				case ThreadGoalStatusBudgetLimited:
					s.lastSystemNotice = "Embedded Codex goal reached its token budget"
				case ThreadGoalStatusComplete:
					s.lastSystemNotice = "Embedded Codex goal marked complete"
				default:
					s.lastSystemNotice = "Embedded Codex goal updated"
				}
			}
		}
		s.mu.Unlock()
		s.notify()
	case "thread/goal/cleared":
		var msg threadGoalClearedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		s.touchLocked()
		if current := strings.TrimSpace(s.threadID); current == "" || strings.TrimSpace(msg.ThreadID) == "" || current == strings.TrimSpace(msg.ThreadID) {
			s.goal = nil
			s.lastSystemNotice = "Embedded Codex goal cleared"
		}
		s.mu.Unlock()
		s.notify()
	case "thread/started":
		var msg threadStartedNotification
		if err := json.Unmarshal(params, &msg); err == nil && msg.Thread.ID != "" {
			s.mu.Lock()
			if !s.shouldTrackStartedThreadLocked(msg) {
				s.mu.Unlock()
				return
			}
			s.touchLocked()
			s.threadID = msg.Thread.ID
			s.started = true
			s.mu.Unlock()
			s.notify()
		}
	case "turn/started":
		var msg turnNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		if !s.notificationMatchesThreadLocked(msg.ThreadID) {
			s.mu.Unlock()
			return
		}
		s.touchLocked()
		turnID := strings.TrimSpace(msg.Turn.ID)
		if s.busy || strings.TrimSpace(s.activeTurnID) != "" {
			s.setBusyLocked(turnID, false)
			s.status = "Codex is working..."
		} else if turnID != "" {
			// Some control-only Codex turns briefly emit turn/started before any
			// user-visible work exists. Keep the turn ID for later correlation,
			// but do not surface a fresh busy timer until real activity arrives.
			s.activeTurnID = turnID
			s.pendingCompletion = nil
			s.reconciling = false
		}
		s.mu.Unlock()
		s.notify()
	case "turn/completed":
		var msg turnNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		if !s.notificationMatchesThreadLocked(msg.ThreadID) {
			s.mu.Unlock()
			return
		}
		s.touchBusyLocked()
		status := formatTurnCompletionStatus(msg.Turn.Status, s.busySince, time.Now())
		s.queueTurnCompletionLocked(msg.Turn.ID, status)
		s.mu.Unlock()
		s.notify()
	case "turn/aborted":
		var msg turnAbortedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		if !s.notificationMatchesThreadLocked(msg.ThreadID) {
			s.mu.Unlock()
			return
		}
		s.touchBusyLocked()
		status := formatTurnCompletionStatus(firstNonEmpty(msg.Turn.Status, msg.Reason, "interrupted"), s.busySince, time.Now())
		s.queueTurnCompletionLocked(firstNonEmpty(msg.Turn.ID, msg.TurnID), status)
		s.mu.Unlock()
		s.notify()
	case "item/started":
		s.handleItemStarted(params)
	case "item/completed":
		s.handleItemCompleted(params)
	case "item/agentMessage/delta":
		s.handleItemDelta(params, TranscriptAgent)
	case "item/plan/delta":
		s.handleItemDelta(params, TranscriptPlan)
	case "item/commandExecution/outputDelta":
		s.handleItemDelta(params, TranscriptCommand)
	case "item/fileChange/outputDelta":
		s.handleItemDelta(params, TranscriptFileChange)
	case "item/reasoning/summaryTextDelta":
		s.handleItemDelta(params, TranscriptReasoning)
	case "item/reasoning/textDelta":
		s.handleItemDelta(params, TranscriptReasoning)
	case "item/reasoning/summaryPartAdded":
		var msg reasoningSummaryPartAddedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		if !s.notificationMatchesThreadLocked(msg.ThreadID) {
			s.mu.Unlock()
			return
		}
		s.touchBusyLocked()
		s.markItemActiveLocked(msg.TurnID, msg.ItemID)
		if msg.SummaryIndex > 0 {
			s.appendDeltaToItemLocked(msg.ItemID, TranscriptReasoning, "\n\n")
		} else {
			s.ensureItemEntryLocked(msg.ItemID, TranscriptReasoning, "")
		}
		s.mu.Unlock()
		s.notify()
	case "item/mcpToolCall/progress":
		var msg mcpToolCallProgressNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		if !s.notificationMatchesThreadLocked(msg.ThreadID) {
			s.mu.Unlock()
			return
		}
		s.touchBusyLocked()
		s.markItemActiveLocked(msg.TurnID, msg.ItemID)
		if _, ok := s.browserToolCalls[msg.ItemID]; ok {
			s.refreshBrowserActivityLocked(time.Now())
		}
		progress := strings.TrimSpace(msg.Message)
		if progress == "" {
			s.ensureItemEntryLocked(msg.ItemID, TranscriptTool, "")
		} else if index, ok := s.entryIndex[msg.ItemID]; ok && strings.TrimSpace(s.entries[index].Text) != "" {
			// Check if this is a spinner update of the last progress line
			// (same text, different spinner frame). If so, replace rather than append.
			existing := s.entries[index].Text
			lastNL := strings.LastIndex(existing, "\n")
			var lastLine string
			if lastNL >= 0 {
				lastLine = strings.TrimSpace(existing[lastNL+1:])
			} else {
				lastLine = strings.TrimSpace(existing)
			}
			strippedNew := normalizeProgressLine(progress)
			strippedOld := normalizeProgressLine(lastLine)
			if strippedNew != "" && strippedNew == strippedOld {
				// Replace the last line in-place
				s.invalidateTranscriptCacheLocked()
				if lastNL >= 0 {
					s.entries[index].Text = existing[:lastNL+1] + progress
				} else {
					s.entries[index].Text = progress
				}
			} else {
				s.appendDeltaToItemLocked(msg.ItemID, TranscriptTool, "\n"+progress)
			}
		} else {
			s.appendDeltaToItemLocked(msg.ItemID, TranscriptTool, progress)
		}
		s.mu.Unlock()
		s.notify()
	case "serverRequest/resolved":
		var msg serverRequestResolvedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		requestID := normalizeRequestID(msg.RequestID)
		s.mu.Lock()
		s.touchLocked()
		if s.pendingApproval != nil && s.pendingApproval.ID == requestID {
			s.pendingApproval = nil
		}
		if s.pendingToolInput != nil && s.pendingToolInput.ID == requestID {
			s.pendingToolInput = nil
		}
		if s.pendingElicitation != nil && s.pendingElicitation.ID == requestID {
			s.pendingElicitation = nil
		}
		s.refreshBrowserActivityLocked(time.Now())
		if s.busy {
			s.status = "Codex is working..."
		} else {
			s.status = ""
		}
		s.mu.Unlock()
		s.notify()
	case "thread/tokenUsage/updated":
		var msg threadTokenUsageUpdatedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		if !s.notificationMatchesThreadLocked(msg.ThreadID) {
			s.mu.Unlock()
			return
		}
		s.touchLocked()
		s.tokenUsage = cloneThreadTokenUsage(&msg.TokenUsage)
		s.mu.Unlock()
		s.notify()
	case "mcpServer/startupStatus/updated":
		var msg mcpServerStartupStatusUpdatedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		s.touchLocked()
		name := strings.TrimSpace(msg.Name)
		if name != "" {
			if s.mcpServerStartup == nil {
				s.mcpServerStartup = make(map[string]mcpServerStartupState)
			}
			s.mcpServerStartup[name] = msg.Status
			switch name {
			case "playwright":
				s.playwrightMCPReady = msg.Status == mcpServerStartupStateReady
			case "lcr_runtime":
				s.runtimeMCPReady = msg.Status == mcpServerStartupStateReady
			}
		}
		s.mu.Unlock()
		s.notify()
	case "account/rateLimits/updated":
		var msg accountRateLimitsUpdatedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		s.touchLocked()
		s.rateLimits = cloneRateLimitSnapshot(&msg.RateLimits)
		s.mu.Unlock()
		s.notify()
	case "model/rerouted":
		var msg modelReroutedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		if !s.notificationMatchesThreadLocked(msg.ThreadID) {
			s.mu.Unlock()
			return
		}
		s.touchLocked()
		if toModel := strings.TrimSpace(msg.ToModel); toModel != "" {
			s.model = toModel
			if strings.EqualFold(strings.TrimSpace(s.pendingModel), toModel) {
				s.pendingModel = ""
			}
		}
		s.mu.Unlock()
		s.notify()
	case "error":
		var msg struct {
			ThreadID string `json:"threadId"`
			Error    struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(params, &msg); err == nil && strings.TrimSpace(msg.Error.Message) != "" {
			s.mu.Lock()
			matches := s.notificationMatchesThreadLocked(msg.ThreadID)
			s.mu.Unlock()
			if !matches {
				return
			}
			s.appendSystemError(errors.New(msg.Error.Message))
		}
	}
}

func (s *appServerSession) shouldTrackStartedThreadLocked(msg threadStartedNotification) bool {
	threadID := strings.TrimSpace(msg.Thread.ID)
	if threadID == "" {
		return false
	}
	if current := strings.TrimSpace(s.threadID); current != "" {
		return current == threadID
	}
	// A resume response establishes the authoritative thread after app-server
	// has returned its session-tree metadata. Ignore interim thread/started
	// notifications so a resumed sub-agent cannot become the direct-input target.
	if strings.TrimSpace(s.reconnectThreadID) != "" {
		return false
	}
	if parentID := stringValue(msg.Thread.ParentThreadID); parentID != "" {
		return false
	}
	if sessionID := strings.TrimSpace(msg.Thread.SessionID); sessionID != "" && sessionID != threadID {
		return false
	}
	return true
}

func (s *appServerSession) notificationMatchesThreadLocked(threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if current := strings.TrimSpace(s.threadID); current != "" {
		return threadID == "" || threadID == current
	}
	// During resume, hydrate from the response before accepting any streamed
	// thread events. The requested ID may itself be a sub-agent that resolves
	// to a different root thread.
	return strings.TrimSpace(s.reconnectThreadID) == ""
}

func (s *appServerSession) handleItemStarted(params json.RawMessage) {
	var msg struct {
		ThreadID string                     `json:"threadId"`
		TurnID   string                     `json:"turnId"`
		Item     map[string]json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(params, &msg); err != nil {
		return
	}
	itemType := decodeRawString(msg.Item["type"])
	itemID := decodeRawString(msg.Item["id"])

	s.mu.Lock()
	if !s.notificationMatchesThreadLocked(msg.ThreadID) {
		s.mu.Unlock()
		return
	}
	s.touchBusyLocked()
	if itemType == "contextCompaction" {
		s.contextCompactionActive = true
		s.status = "Compacting conversation history..."
		s.lastSystemNotice = "Compacting conversation history..."
		if itemID != "" {
			if s.activeCompactionItems == nil {
				s.activeCompactionItems = make(map[string]struct{})
			}
			s.activeCompactionItems[itemID] = struct{}{}
		}
	}
	if tracksBusyItemLifecycle(itemType) {
		s.markItemActiveLocked(msg.TurnID, itemID)
	}
	if itemType == "mcpToolCall" {
		s.recordCodexMCPToolUsageLocked(itemID, msg.Item)
		if call, ok := s.browserToolCallForItem(msg.Item); ok {
			if s.browserToolCalls == nil {
				s.browserToolCalls = make(map[string]browserToolCall)
			}
			s.browserToolCalls[itemID] = call
			s.refreshBrowserActivityLocked(time.Now())
		}
	}
	switch itemType {
	case "agentMessage":
		s.ensureItemEntryLocked(itemID, TranscriptAgent, "")
	default:
		itemID, kind, text, image := s.renderThreadItemForTurn("inProgress", msg.Item)
		if strings.TrimSpace(text) != "" && itemType == "commandExecution" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if strings.TrimSpace(text) != "" {
			s.upsertRenderedItemEntryLocked(itemID, kind, text, image)
		}
	}
	s.mu.Unlock()
	s.notify()
}

func (s *appServerSession) handleItemDelta(params json.RawMessage, kind TranscriptKind) {
	var msg deltaNotification
	if err := json.Unmarshal(params, &msg); err != nil {
		return
	}
	s.mu.Lock()
	if !s.notificationMatchesThreadLocked(msg.ThreadID) {
		s.mu.Unlock()
		return
	}
	s.touchBusyLocked()
	s.markItemActiveLocked(msg.TurnID, msg.ItemID)
	s.appendDeltaToItemLocked(msg.ItemID, kind, msg.Delta)
	s.mu.Unlock()
	s.notify()
}

func (s *appServerSession) handleItemCompleted(params json.RawMessage) {
	var msg struct {
		ThreadID string                     `json:"threadId"`
		TurnID   string                     `json:"turnId"`
		Item     map[string]json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(params, &msg); err != nil {
		return
	}
	itemType := decodeRawString(msg.Item["type"])
	itemID := decodeRawString(msg.Item["id"])
	refreshGeneratedImageThreadID := ""

	s.mu.Lock()
	if !s.notificationMatchesThreadLocked(msg.ThreadID) {
		s.mu.Unlock()
		return
	}
	s.touchBusyLocked()
	if itemType == "contextCompaction" {
		s.contextCompactionActive = false
		delete(s.activeCompactionItems, itemID)
		if len(s.activeCompactionItems) == 0 {
			s.activeCompactionItems = nil
		}
		if s.busy && s.pendingCompletion == nil {
			s.status = "Codex is working..."
		}
	}
	if itemType == "mcpToolCall" {
		s.recordCodexMCPToolUsageLocked(itemID, msg.Item)
		if isManagedBrowserAttentionToolCall(msg.Item) {
			s.browserHandoffPending = true
			s.browserHandoffAt = time.Now()
			s.browserHandoffMessage = managedBrowserAttentionMessage(msg.Item)
			s.status = "Browser needs attention"
			s.lastSystemNotice = "Codex requested browser input"
			s.refreshBrowserActivityLocked(s.browserHandoffAt)
		}
		if call, ok := s.browserToolCallForItem(msg.Item); ok {
			if s.browserToolCalls == nil {
				s.browserToolCalls = make(map[string]browserToolCall)
			}
			s.browserToolCalls[itemID] = call
			s.updateBrowserPageURLLocked(call, msg.Item)
		}
	}
	if _, ok := s.browserToolCalls[itemID]; ok {
		delete(s.browserToolCalls, itemID)
		s.refreshBrowserActivityLocked(time.Now())
	}
	switch itemType {
	case "commandExecution":
		s.finalizeCommandItemLocked(itemID, msg.Item)
	case "fileChange":
		s.finalizeFileChangeItemLocked(itemID, msg.Item)
	default:
		itemID, kind, text, image := s.renderThreadItemForTurn("completed", msg.Item)
		if strings.TrimSpace(text) != "" {
			s.upsertRenderedItemEntryLocked(itemID, kind, text, image)
		}
		if itemType == "imageGeneration" && image == nil && s.canRefreshThreadStateLocked() {
			refreshGeneratedImageThreadID = firstNonEmpty(msg.ThreadID, s.threadID)
		}
	}
	s.markItemCompletedLocked(itemID)
	s.mu.Unlock()
	s.notify()
	if refreshGeneratedImageThreadID != "" {
		s.scheduleGeneratedImageArtifactRefresh(refreshGeneratedImageThreadID)
	}
}

func (s *appServerSession) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if s.rpcCallHook != nil {
		return s.rpcCallHook(ctx, method, params)
	}
	id := s.nextRequestID()
	key := idKey(id)
	ch := make(chan rpcEnvelope, 1)

	s.pendingMu.Lock()
	s.pending[key] = ch
	s.pendingMu.Unlock()

	if err := s.send(rpcRequest{Method: method, ID: decodeRequestID(key), Params: params}); err != nil {
		s.pendingMu.Lock()
		delete(s.pending, key)
		s.pendingMu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		s.pendingMu.Lock()
		delete(s.pending, key)
		s.pendingMu.Unlock()
		return nil, ctx.Err()
	case env := <-ch:
		if env.Error != nil {
			return nil, errors.New(env.Error.Message)
		}
		return env.Result, nil
	}
}

func (s *appServerSession) send(v any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.stdin == nil {
		return fmt.Errorf("codex app-server stdin unavailable")
	}
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := s.stdin.Write(append(payload, '\n')); err != nil {
		return err
	}
	return nil
}

func (s *appServerSession) respondRequestError(id json.RawMessage, code int, message string) error {
	return s.send(rpcErrorResponse{
		ID: decodeRequestID(idKey(id)),
		Error: rpcError{
			Code:    code,
			Message: message,
		},
	})
}

func (s *appServerSession) nextRequestID() json.RawMessage {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	s.nextID++
	return json.RawMessage(strconv.AppendInt(nil, s.nextID, 10))
}

func (s *appServerSession) failPending(message string) {
	s.pendingMu.Lock()
	pending := s.pending
	s.pending = make(map[string]chan rpcEnvelope)
	s.pendingMu.Unlock()
	for _, ch := range pending {
		ch <- rpcEnvelope{Error: &rpcError{Message: message}}
	}
}
