package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexcli"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	rpcTimeout                = 15 * time.Second
	compactionWaitTimeout     = 5 * time.Minute
	idleShutdownAfter         = time.Hour
	idleShutdownNotice        = "Closed embedded Codex session after 1 hour of inactivity."
	busyStateReconcileAfter   = time.Minute
	busyStateUnresponsiveFor  = 10 * time.Minute
	busyStateStallAfter       = 2
	busyStateHardStallAfter   = busyStateUnresponsiveFor + time.Duration(busyStateStallAfter)*busyStateReconcileAfter
	codexReconnectSuggestion  = "Embedded Codex session seems stuck or disconnected. Use /reconnect."
	codexHomeCleanupWarning   = "Codex home cleanup warning: could not repair stale rollout paths in state_5.sqlite before startup. Saved-session discovery may still show stale paths until a later cleanup succeeds."
	playwrightMCPReadyTimeout = 12 * time.Second
)

var errBusyTurnLikelyStuck = errors.New("embedded Codex session seems stuck or disconnected. Interrupt the current turn or use /reconnect before sending another prompt")
var generatedImageArtifactRefreshDelay = 300 * time.Millisecond

const codexCompactionStuckSuggestion = "Codex conversation compaction seems stuck or disconnected. Interrupt the current turn or use /reconnect."

type ForceNewSessionReusedError struct {
	Provider Provider
	ThreadID string
}

func (e *ForceNewSessionReusedError) Error() string {
	provider := e.Provider.Normalized()
	label := "embedded session"
	unit := "thread"
	switch provider {
	case ProviderOpenCode:
		label = "embedded OpenCode session"
		unit = "session"
	case ProviderCodex:
		label = "embedded Codex session"
	}
	threadID := strings.TrimSpace(e.ThreadID)
	if threadID == "" {
		return "forced fresh " + label + " open reused an existing " + unit
	}
	return "forced fresh " + label + " open reused existing " + unit + " " + threadID
}

type appServerSession struct {
	projectPath              string
	preset                   codexcli.Preset
	notify                   func()
	rpcCallHook              func(context.Context, string, any) (json.RawMessage, error)
	playwrightPolicy         browserctl.Policy
	playwrightMCPExpected    bool
	managedBrowserSessionKey string
	dataDir                  string
	codexHomeOverlay         string

	cmd   *exec.Cmd
	stdin io.WriteCloser

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan rpcEnvelope
	nextID    int64
	exitCh    chan struct{}
	exitOnce  sync.Once

	mu                      sync.Mutex
	threadID                string
	activeTurnID            string
	activeItems             map[string]struct{}
	activeCompactionItems   map[string]struct{}
	pendingCompletion       *turnCompletionState
	started                 bool
	busy                    bool
	busyExternal            bool
	compacting              bool
	contextCompactionActive bool
	reconciling             bool
	reportedAuth403         bool
	busySince               time.Time
	closed                  bool
	stalled                 bool
	stallCount              int
	status                  string
	lastError               string
	lastSystemNotice        string
	lastActivityAt          time.Time
	lastBusyActivityAt      time.Time
	currentCWD              string
	model                   string
	modelProvider           string
	reasoningEffort         string
	pendingModel            string
	pendingReasoning        string
	serviceTier             string
	approvalPolicy          json.RawMessage
	sandboxPolicy           json.RawMessage
	tokenUsage              *threadTokenUsage
	rateLimits              *rateLimitSnapshot
	rateLimitsByID          map[string]rateLimitSnapshot
	goal                    *ThreadGoal
	pendingApproval         *ApprovalRequest
	pendingToolInput        *ToolInputRequest
	pendingElicitation      *ElicitationRequest
	browserActivity         browserctl.SessionActivity
	currentBrowserPageURL   string
	currentBrowserPageStale bool
	browserToolCalls        map[string]browserToolCall
	mcpServerStartup        map[string]mcpServerStartupState
	playwrightMCPReady      bool
	entries                 []transcriptEntry
	entryIndex              map[string]int
	transcriptRevision      uint64
	transcriptCache         transcriptExportCache
}

type transcriptEntry struct {
	ItemID         string
	Kind           TranscriptKind
	Text           string
	DisplayText    string
	GeneratedImage *GeneratedImageArtifact
}

type browserToolCall struct {
	ServerName string
	ToolName   string
}

type turnCompletionState struct {
	TurnID string
	Status string
}

type mcpServerStartupState string

const (
	mcpServerStartupStateStarting  mcpServerStartupState = "starting"
	mcpServerStartupStateReady     mcpServerStartupState = "ready"
	mcpServerStartupStateFailed    mcpServerStartupState = "failed"
	mcpServerStartupStateCancelled mcpServerStartupState = "cancelled"
)

type rpcEnvelope struct {
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	ID     json.RawMessage `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcRequest struct {
	Method string `json:"method"`
	ID     any    `json:"id"`
	Params any    `json:"params,omitempty"`
}

type rpcNotification struct {
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type rpcResponse struct {
	ID     any `json:"id"`
	Result any `json:"result,omitempty"`
}

type rpcErrorResponse struct {
	ID    any      `json:"id"`
	Error rpcError `json:"error"`
}

type threadResponse struct {
	ApprovalPolicy  json.RawMessage `json:"approvalPolicy"`
	CWD             string          `json:"cwd"`
	Model           string          `json:"model"`
	ModelProvider   string          `json:"modelProvider"`
	ReasoningEffort *string         `json:"reasoningEffort"`
	ServiceTier     *string         `json:"serviceTier"`
	Sandbox         json.RawMessage `json:"sandbox"`
	Thread          struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type resumedThreadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags"`
}

type threadStatusChangedNotification struct {
	ThreadID string              `json:"threadId"`
	Status   resumedThreadStatus `json:"status"`
}

type threadCompactedNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type threadGoalResponse struct {
	Goal *threadGoal `json:"goal"`
}

type threadGoalClearResponse struct {
	Cleared bool `json:"cleared"`
}

type threadGoalSetParams struct {
	ThreadID    string           `json:"threadId"`
	Objective   string           `json:"objective,omitempty"`
	Status      ThreadGoalStatus `json:"status,omitempty"`
	TokenBudget *int64           `json:"tokenBudget,omitempty"`
}

type threadGoalGetParams struct {
	ThreadID string `json:"threadId"`
}

type threadGoalClearParams struct {
	ThreadID string `json:"threadId"`
}

type threadGoal struct {
	ThreadID        string           `json:"threadId"`
	Objective       string           `json:"objective"`
	Status          ThreadGoalStatus `json:"status"`
	TokenBudget     *int64           `json:"tokenBudget"`
	TokensUsed      int64            `json:"tokensUsed"`
	TimeUsedSeconds int64            `json:"timeUsedSeconds"`
	CreatedAt       int64            `json:"createdAt"`
	UpdatedAt       int64            `json:"updatedAt"`
}

type threadGoalUpdatedNotification struct {
	ThreadID string     `json:"threadId"`
	TurnID   *string    `json:"turnId"`
	Goal     threadGoal `json:"goal"`
}

type threadGoalClearedNotification struct {
	ThreadID string `json:"threadId"`
}

type mcpServerStartupStatusUpdatedNotification struct {
	Name   string                `json:"name"`
	Status mcpServerStartupState `json:"status"`
	Error  *string               `json:"error"`
}

type mcpServerStatusListParams struct {
	Detail string `json:"detail,omitempty"`
}

type mcpServerStatusListResponse struct {
	Data []mcpServerStatusEntry `json:"data"`
}

type mcpServerStatusEntry struct {
	Name  string                     `json:"name"`
	Tools map[string]json.RawMessage `json:"tools"`
}

type resumedTurnError struct {
	Message string `json:"message"`
}

type resumedTurn struct {
	ID     string                       `json:"id"`
	Status string                       `json:"status"`
	Error  *resumedTurnError            `json:"error"`
	Items  []map[string]json.RawMessage `json:"items"`
}

type resumedThread struct {
	ID     string              `json:"id"`
	Status resumedThreadStatus `json:"status"`
	Turns  []resumedTurn       `json:"turns"`
}

type threadResumeResponse struct {
	ApprovalPolicy  json.RawMessage `json:"approvalPolicy"`
	CWD             string          `json:"cwd"`
	Model           string          `json:"model"`
	ModelProvider   string          `json:"modelProvider"`
	ReasoningEffort *string         `json:"reasoningEffort"`
	ServiceTier     *string         `json:"serviceTier"`
	Sandbox         json.RawMessage `json:"sandbox"`
	Thread          resumedThread   `json:"thread"`
}

type threadReadResponse struct {
	Thread resumedThread `json:"thread"`
}

type turnStartResponse struct {
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
}

type reviewStartResponse struct {
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
	ReviewThreadID string `json:"reviewThreadId"`
}

type turnSteerResponse struct {
	TurnID string `json:"turnId"`
}

type modelListParams struct {
	Cursor        *string `json:"cursor,omitempty"`
	Limit         *int    `json:"limit,omitempty"`
	IncludeHidden *bool   `json:"includeHidden,omitempty"`
}

type modelListResponse struct {
	Data       []modelListEntry `json:"data"`
	NextCursor *string          `json:"nextCursor"`
}

type modelListEntry struct {
	ID                        string                       `json:"id"`
	Model                     string                       `json:"model"`
	DisplayName               string                       `json:"displayName"`
	Description               string                       `json:"description"`
	Hidden                    bool                         `json:"hidden"`
	SupportedReasoningEfforts []reasoningEffortOptionEntry `json:"supportedReasoningEfforts"`
	DefaultReasoningEffort    string                       `json:"defaultReasoningEffort"`
	SupportsPersonality       bool                         `json:"supportsPersonality"`
	IsDefault                 bool                         `json:"isDefault"`
}

type reasoningEffortOptionEntry struct {
	ReasoningEffort string `json:"reasoningEffort"`
	Description     string `json:"description"`
}

type turnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type turnSteerParams struct {
	ThreadID       string      `json:"threadId"`
	ExpectedTurnID string      `json:"expectedTurnId"`
	Input          []userInput `json:"input"`
}

type turnStartParams struct {
	ThreadID string      `json:"threadId"`
	Input    []userInput `json:"input"`
	Model    string      `json:"model,omitempty"`
	Effort   string      `json:"effort,omitempty"`
}

type threadStartParams struct {
	CWD            string `json:"cwd"`
	ApprovalPolicy string `json:"approvalPolicy"`
	Sandbox        string `json:"sandbox"`
	ServiceName    string `json:"serviceName"`
}

type threadResumeParams struct {
	ThreadID       string `json:"threadId"`
	ApprovalPolicy string `json:"approvalPolicy"`
	Sandbox        string `json:"sandbox"`
}

type threadReadParams struct {
	ThreadID     string `json:"threadId"`
	IncludeTurns bool   `json:"includeTurns,omitempty"`
}

type threadCompactStartParams struct {
	ThreadID string `json:"threadId"`
}

type reviewStartParams struct {
	ThreadID string       `json:"threadId"`
	Target   reviewTarget `json:"target"`
	Delivery string       `json:"delivery,omitempty"`
}

type reviewTarget struct {
	Type string `json:"type"`
}

type userInput struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	Path         string        `json:"path,omitempty"`
	URL          string        `json:"url,omitempty"`
	TextElements []textElement `json:"text_elements,omitempty"`
}

type textElement struct {
	ByteRange   byteRange `json:"byteRange"`
	Placeholder string    `json:"placeholder,omitempty"`
}

type byteRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type threadStartedNotification struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type turnNotification struct {
	ThreadID string `json:"threadId"`
	Turn     struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"turn"`
}

type turnAbortedNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Reason   string `json:"reason"`
	Turn     struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"turn"`
}

type deltaNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

type reasoningSummaryPartAddedNotification struct {
	ThreadID     string `json:"threadId"`
	TurnID       string `json:"turnId"`
	ItemID       string `json:"itemId"`
	SummaryIndex int64  `json:"summaryIndex"`
}

type mcpToolCallProgressNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Message  string `json:"message"`
}

type serverRequestResolvedNotification struct {
	ThreadID  string `json:"threadId"`
	RequestID any    `json:"requestId"`
}

type threadTokenUsageUpdatedNotification struct {
	ThreadID   string           `json:"threadId"`
	TurnID     string           `json:"turnId"`
	TokenUsage threadTokenUsage `json:"tokenUsage"`
}

type threadTokenUsage struct {
	Last               tokenUsageBreakdown `json:"last"`
	Total              tokenUsageBreakdown `json:"total"`
	ModelContextWindow *int64              `json:"modelContextWindow"`
}

type tokenUsageBreakdown struct {
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	InputTokens           int64 `json:"inputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
	TotalTokens           int64 `json:"totalTokens"`
}

type accountRateLimitsUpdatedNotification struct {
	RateLimits rateLimitSnapshot `json:"rateLimits"`
}

type modelReroutedNotification struct {
	ThreadID  string `json:"threadId"`
	FromModel string `json:"fromModel"`
	ToModel   string `json:"toModel"`
	Reason    string `json:"reason"`
}

type accountRateLimitsResponse struct {
	RateLimits     rateLimitSnapshot            `json:"rateLimits"`
	RateLimitsByID map[string]rateLimitSnapshot `json:"rateLimitsByLimitId"`
}

type rateLimitSnapshot struct {
	LimitID   *string           `json:"limitId"`
	LimitName *string           `json:"limitName"`
	PlanType  *string           `json:"planType"`
	Primary   *rateLimitWindow  `json:"primary"`
	Secondary *rateLimitWindow  `json:"secondary"`
	Credits   *rateLimitCredits `json:"credits"`
}

type rateLimitWindow struct {
	UsedPercent        int    `json:"usedPercent"`
	ResetsAt           *int64 `json:"resetsAt"`
	WindowDurationMins *int64 `json:"windowDurationMins"`
}

type rateLimitCredits struct {
	Balance    *string `json:"balance"`
	HasCredits bool    `json:"hasCredits"`
	Unlimited  bool    `json:"unlimited"`
}

type commandApprovalParams struct {
	RequestID string
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	ItemID    string `json:"itemId"`
	Command   string `json:"command"`
	CWD       string `json:"cwd"`
	Reason    string `json:"reason"`
}

type fileChangeApprovalParams struct {
	RequestID string
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	ItemID    string `json:"itemId"`
	GrantRoot string `json:"grantRoot"`
	Reason    string `json:"reason"`
}

type toolRequestUserInputParams struct {
	RequestID string
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	ItemID    string `json:"itemId"`
	Questions []struct {
		Header   string `json:"header"`
		ID       string `json:"id"`
		Question string `json:"question"`
		IsOther  bool   `json:"isOther"`
		IsSecret bool   `json:"isSecret"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

type mcpServerElicitationRequestParams struct {
	RequestID       string
	ServerName      string          `json:"serverName"`
	ThreadID        string          `json:"threadId"`
	TurnID          string          `json:"turnId"`
	Mode            string          `json:"mode"`
	Message         string          `json:"message"`
	URL             string          `json:"url"`
	ElicitationID   string          `json:"elicitationId"`
	RequestedSchema json.RawMessage `json:"requestedSchema"`
}
