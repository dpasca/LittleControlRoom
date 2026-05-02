package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"lcroom/internal/appfs"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexcli"
	"lcroom/internal/model"
)

const helperPollInterval = 50 * time.Millisecond

type PromptHelperRequest struct {
	Prompt          string
	Model           string
	ReasoningEffort string
}

type PromptHelperResponse struct {
	OutputText string
	Model      string
	Usage      model.LLMUsage
}

type PromptHelper struct {
	mu        sync.Mutex
	session   *appServerSession
	notifyCh  chan struct{}
	workspace string
	closed    bool
}

func NewPromptHelper() (*PromptHelper, error) {
	return NewPromptHelperInDataDir("")
}

func NewPromptHelperInDataDir(dataDir string) (*PromptHelper, error) {
	workspace, err := appfs.CreateInternalWorkspace(dataDir, "lcroom-codex-helper-*")
	if err != nil {
		return nil, fmt.Errorf("create codex helper workspace: %w", err)
	}

	notifyCh := make(chan struct{}, 1)
	sessionAny, err := newAppServerSession(promptHelperLaunchRequest(workspace), func() {
		select {
		case notifyCh <- struct{}{}:
		default:
		}
	})
	if err != nil {
		_ = os.RemoveAll(workspace)
		return nil, err
	}

	session, ok := sessionAny.(*appServerSession)
	if !ok {
		_ = sessionAny.Close()
		_ = os.RemoveAll(workspace)
		return nil, fmt.Errorf("codex helper session had unexpected type %T", sessionAny)
	}

	return &PromptHelper{
		session:   session,
		notifyCh:  notifyCh,
		workspace: workspace,
	}, nil
}

func promptHelperLaunchRequest(workspace string) LaunchRequest {
	return LaunchRequest{
		Provider:    ProviderCodex,
		ProjectPath: workspace,
		ForceNew:    true,
		Preset:      codexcli.PresetSafe,
		PlaywrightPolicy: browserctl.Policy{
			ManagementMode: browserctl.ManagementModeLegacy,
		},
	}
}

func (h *PromptHelper) Run(ctx context.Context, req PromptHelperRequest) (PromptHelperResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h == nil || h.session == nil {
		return PromptHelperResponse{}, fmt.Errorf("codex helper not configured")
	}
	if h.closed {
		return PromptHelperResponse{}, fmt.Errorf("codex helper is closed")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return PromptHelperResponse{}, fmt.Errorf("codex helper prompt required")
	}
	if strings.TrimSpace(req.Model) == "" {
		return PromptHelperResponse{}, fmt.Errorf("codex helper model required")
	}

	drainNotify(h.notifyCh)
	resetHelperSessionState(h.session)

	threadID, err := startHelperThread(ctx, h.session, h.workspace)
	if err != nil {
		h.closeLocked()
		return PromptHelperResponse{}, err
	}
	if err := h.session.startTurnWithInput(
		ctx,
		threadID,
		Submission{Text: strings.TrimSpace(req.Prompt)},
		strings.TrimSpace(req.Model),
		strings.TrimSpace(req.ReasoningEffort),
		strings.TrimSpace(req.Model),
		strings.TrimSpace(req.ReasoningEffort),
	); err != nil {
		h.closeLocked()
		return PromptHelperResponse{}, err
	}

	response, err := waitForHelperResponse(ctx, h.session, h.notifyCh, strings.TrimSpace(req.Model))
	if err != nil {
		h.closeLocked()
		return PromptHelperResponse{}, err
	}
	resetHelperSessionState(h.session)
	return response, nil
}

func (h *PromptHelper) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closeLocked()
}

func (h *PromptHelper) closeLocked() error {
	if h.closed {
		return nil
	}
	h.closed = true
	var err error
	if h.session != nil {
		err = h.session.Close()
	}
	if strings.TrimSpace(h.workspace) != "" {
		_ = os.RemoveAll(h.workspace)
	}
	return err
}

func startHelperThread(ctx context.Context, session *appServerSession, workspace string) (string, error) {
	result, err := session.call(ctx, "thread/start", threadStartParams{
		CWD:            workspace,
		ApprovalPolicy: "never",
		Sandbox:        "read-only",
		ServiceName:    "little-control-room-helper",
	})
	if err != nil {
		return "", err
	}
	var response threadResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return "", err
	}
	threadID := strings.TrimSpace(response.Thread.ID)
	if threadID == "" {
		return "", fmt.Errorf("thread/start returned no thread id")
	}

	session.mu.Lock()
	session.threadID = threadID
	session.started = true
	session.applyThreadConfigLocked(
		response.ApprovalPolicy,
		response.CWD,
		response.Model,
		response.ModelProvider,
		stringValue(response.ReasoningEffort),
		stringValue(response.ServiceTier),
		response.Sandbox,
	)
	session.touchLocked()
	session.mu.Unlock()

	return threadID, nil
}

func resetHelperSessionState(session *appServerSession) {
	if session == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.entries = nil
	session.entryIndex = make(map[string]int)
	session.activeItems = make(map[string]struct{})
	session.activeTurnID = ""
	session.pendingCompletion = nil
	session.busy = false
	session.busyExternal = false
	session.reconciling = false
	session.pendingApproval = nil
	session.pendingToolInput = nil
	session.pendingElicitation = nil
	session.lastError = ""
	session.lastSystemNotice = ""
	session.status = ""
	session.tokenUsage = nil
}

func waitForHelperResponse(ctx context.Context, session *appServerSession, notifyCh <-chan struct{}, fallbackModel string) (PromptHelperResponse, error) {
	for {
		snapshot := session.Snapshot()
		if snapshot.Closed {
			return PromptHelperResponse{}, fmt.Errorf("codex helper session closed")
		}
		if snapshot.PendingApproval != nil {
			return PromptHelperResponse{}, fmt.Errorf("codex helper unexpectedly requested command approval")
		}
		if snapshot.PendingToolInput != nil {
			return PromptHelperResponse{}, fmt.Errorf("codex helper unexpectedly requested structured tool input")
		}
		if snapshot.PendingElicitation != nil {
			return PromptHelperResponse{}, fmt.Errorf("codex helper unexpectedly requested elicitation input")
		}

		output := latestHelperAgentOutput(snapshot.Entries)
		if !snapshot.Busy {
			if strings.TrimSpace(snapshot.LastError) != "" {
				return PromptHelperResponse{}, errors.New(strings.TrimSpace(snapshot.LastError))
			}
			if strings.TrimSpace(output) == "" {
				return PromptHelperResponse{}, fmt.Errorf("codex helper returned no assistant output (status=%s)", strings.TrimSpace(snapshot.Status))
			}
			modelName := strings.TrimSpace(snapshot.Model)
			if modelName == "" {
				modelName = fallbackModel
			}
			return PromptHelperResponse{
				OutputText: strings.TrimSpace(output),
				Model:      modelName,
				Usage:      helperUsageFromSnapshot(snapshot, modelName),
			}, nil
		}

		select {
		case <-ctx.Done():
			return PromptHelperResponse{}, ctx.Err()
		case <-notifyCh:
		case <-time.After(helperPollInterval):
		}
	}
}

func latestHelperAgentOutput(entries []TranscriptEntry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Kind != TranscriptAgent {
			continue
		}
		text := strings.TrimSpace(entries[i].Text)
		if text != "" {
			return text
		}
	}
	return ""
}

func helperUsageFromSnapshot(snapshot Snapshot, modelName string) model.LLMUsage {
	if snapshot.TokenUsage == nil {
		return model.LLMUsage{}
	}
	last := snapshot.TokenUsage.Last
	usage := model.LLMUsage{
		InputTokens:       last.InputTokens,
		OutputTokens:      last.OutputTokens,
		TotalTokens:       last.TotalTokens,
		CachedInputTokens: last.CachedInputTokens,
		ReasoningTokens:   last.ReasoningOutputTokens,
	}
	if usage.TotalTokens == 0 && (usage.InputTokens > 0 || usage.OutputTokens > 0) {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	if estimatedCostUSD, ok := model.EstimateLLMCostUSD(strings.TrimSpace(modelName), usage); ok {
		usage.EstimatedCostUSD = estimatedCostUSD
	}
	return usage
}

func drainNotify(ch <-chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
