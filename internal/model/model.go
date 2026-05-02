package model

import (
	"strings"
	"time"
)

type ProjectStatus string

const (
	StatusActive        ProjectStatus = "active"
	StatusIdle          ProjectStatus = "idle"
	StatusPossiblyStuck ProjectStatus = "possibly_stuck"
)

type ProjectKind string

const (
	ProjectKindProject     ProjectKind = "project"
	ProjectKindScratchTask ProjectKind = "scratch_task"
)

type RepoSyncStatus string

const (
	RepoSyncNoRemote   RepoSyncStatus = "no_remote"
	RepoSyncNoUpstream RepoSyncStatus = "no_upstream"
	RepoSyncSynced     RepoSyncStatus = "synced"
	RepoSyncAhead      RepoSyncStatus = "ahead"
	RepoSyncBehind     RepoSyncStatus = "behind"
	RepoSyncDiverged   RepoSyncStatus = "diverged"
)

type WorktreeKind string

const (
	WorktreeKindNone   WorktreeKind = ""
	WorktreeKindMain   WorktreeKind = "main"
	WorktreeKindLinked WorktreeKind = "linked"
)

type WorktreeMergeStatus string

const (
	WorktreeMergeStatusUnknown         WorktreeMergeStatus = ""
	WorktreeMergeStatusMerged          WorktreeMergeStatus = "merged"
	WorktreeMergeStatusNotMerged       WorktreeMergeStatus = "not_merged"
	WorktreeMergeStatusMergeInProgress WorktreeMergeStatus = "merge_in_progress"
)

type AttentionReason struct {
	Code   string `json:"code"`
	Text   string `json:"text"`
	Weight int    `json:"weight"`
}

type SessionClassificationStatus string

const (
	ClassificationPending   SessionClassificationStatus = "pending"
	ClassificationRunning   SessionClassificationStatus = "running"
	ClassificationCompleted SessionClassificationStatus = "completed"
	ClassificationFailed    SessionClassificationStatus = "failed"
)

type SessionClassificationStage string

const (
	ClassificationStageQueued            SessionClassificationStage = "queued"
	ClassificationStagePreparingSnapshot SessionClassificationStage = "preparing_snapshot"
	ClassificationStageWaitingForModel   SessionClassificationStage = "waiting_for_model"
)

type SessionCategory string

const (
	SessionCategoryCompleted      SessionCategory = "completed"
	SessionCategoryBlocked        SessionCategory = "blocked"
	SessionCategoryWaitingForUser SessionCategory = "waiting_for_user"
	SessionCategoryNeedsFollowUp  SessionCategory = "needs_follow_up"
	SessionCategoryInProgress     SessionCategory = "in_progress"
	SessionCategoryUnknown        SessionCategory = "unknown"
)

type SessionSource string

const (
	SessionSourceUnknown    SessionSource = ""
	SessionSourceCodex      SessionSource = "codex"
	SessionSourceOpenCode   SessionSource = "opencode"
	SessionSourceClaudeCode SessionSource = "claude_code"
)

type AgentTaskKind string

const (
	AgentTaskKindEphemeral AgentTaskKind = "ephemeral"
	AgentTaskKindProject   AgentTaskKind = "project"
	AgentTaskKindScratch   AgentTaskKind = "scratch_task"
	AgentTaskKindSystemOps AgentTaskKind = "system_ops"
)

type AgentTaskStatus string

const (
	AgentTaskStatusActive    AgentTaskStatus = "active"
	AgentTaskStatusWaiting   AgentTaskStatus = "waiting"
	AgentTaskStatusCompleted AgentTaskStatus = "completed"
	AgentTaskStatusArchived  AgentTaskStatus = "archived"
)

type AgentTaskResourceKind string

const (
	AgentTaskResourceProject         AgentTaskResourceKind = "project"
	AgentTaskResourceProcess         AgentTaskResourceKind = "process"
	AgentTaskResourcePort            AgentTaskResourceKind = "port"
	AgentTaskResourceFile            AgentTaskResourceKind = "file"
	AgentTaskResourceEngineerSession AgentTaskResourceKind = "engineer_session"
)

type SessionEvidence struct {
	Source              SessionSource `json:"source"`
	SessionID           string        `json:"session_id"`
	RawSessionID        string        `json:"raw_session_id"`
	ProjectPath         string        `json:"project_path"`
	DetectedProjectPath string        `json:"detected_project_path"`
	SessionFile         string        `json:"session_file"`
	Format              string        `json:"format"`
	SnapshotHash        string        `json:"snapshot_hash"`
	StartedAt           time.Time     `json:"started_at"`
	LastEventAt         time.Time     `json:"last_event_at"`
	ErrorCount          int           `json:"error_count"`
	LatestTurnStartedAt time.Time     `json:"latest_turn_started_at"`
	// Best-effort signal from structured session events (for example task_started/task_complete/turn_aborted).
	LatestTurnStateKnown bool `json:"latest_turn_state_known"`
	LatestTurnCompleted  bool `json:"latest_turn_completed"`
}

type SessionContextSample struct {
	Source                   SessionSource `json:"source"`
	SessionID                string        `json:"session_id"`
	RawSessionID             string        `json:"raw_session_id"`
	ProjectPath              string        `json:"project_path"`
	SessionFile              string        `json:"session_file"`
	SessionFormat            string        `json:"session_format"`
	UpdatedAt                time.Time     `json:"updated_at"`
	ArtifactUpdatedAfterScan bool          `json:"artifact_updated_after_scan"`
	LatestTurnStateKnown     bool          `json:"latest_turn_state_known"`
	LatestTurnCompleted      bool          `json:"latest_turn_completed"`
	Text                     string        `json:"text"`
}

func (sample SessionContextSample) ExternalID() string {
	return ExternalSessionID(sample.Source, sample.SessionFormat, sample.SessionID, sample.RawSessionID)
}

type SessionContextExcerptRequest struct {
	SessionID   string
	Query       string
	BeforeTurns int
	AfterTurns  int
	MaxChars    int
}

type SessionContextTurn struct {
	Index int
	Role  string
	Text  string
}

type SessionContextExcerpt struct {
	Source        SessionSource
	SessionID     string
	RawSessionID  string
	ProjectPath   string
	ProjectName   string
	SessionFile   string
	SessionFormat string
	UpdatedAt     time.Time
	Query         string
	AnchorIndex   int
	AnchorMatched bool
	Turns         []SessionContextTurn
	Truncated     bool
}

func (excerpt SessionContextExcerpt) ExternalID() string {
	return ExternalSessionID(excerpt.Source, excerpt.SessionFormat, excerpt.SessionID, excerpt.RawSessionID)
}

type ProjectGitFingerprint struct {
	ProjectPath  string
	HeadHash     string
	RecentHashes []string
	UpdatedAt    time.Time
}

type AgentTask struct {
	ID            string
	Title         string
	Kind          AgentTaskKind
	Status        AgentTaskStatus
	Summary       string
	Provider      SessionSource
	SessionID     string
	WorkspacePath string
	ExpiresAt     time.Time
	CreatedAt     time.Time
	LastTouchedAt time.Time
	CompletedAt   time.Time
	ArchivedAt    time.Time
	UpdatedAt     time.Time
	Resources     []AgentTaskResource
}

type AgentTaskResource struct {
	ID          int64
	TaskID      string
	Kind        AgentTaskResourceKind
	ProjectPath string
	Path        string
	PID         int
	Port        int
	Provider    SessionSource
	SessionID   string
	Label       string
	CreatedAt   time.Time
}

type CreateAgentTaskInput struct {
	ID            string
	Title         string
	Kind          AgentTaskKind
	Status        AgentTaskStatus
	Summary       string
	Provider      SessionSource
	SessionID     string
	WorkspacePath string
	ExpiresAt     time.Time
	Resources     []AgentTaskResource
}

type UpdateAgentTaskInput struct {
	ID               string
	Title            *string
	Status           *AgentTaskStatus
	Summary          *string
	Provider         *SessionSource
	SessionID        *string
	WorkspacePath    *string
	ExpiresAt        *time.Time
	CompletedAt      *time.Time
	ArchivedAt       *time.Time
	Resources        []AgentTaskResource
	ReplaceResources bool
	Touch            bool
}

type AgentTaskFilter struct {
	Kind            AgentTaskKind
	Statuses        []AgentTaskStatus
	IncludeArchived bool
	Limit           int
}

type PathAlias struct {
	OldPath   string
	NewPath   string
	Reason    string
	UpdatedAt time.Time
}

type ArtifactEvidence struct {
	Path      string    `json:"path"`
	Kind      string    `json:"kind"`
	UpdatedAt time.Time `json:"updated_at"`
	Note      string    `json:"note"`
}

type SessionClassification struct {
	Source            SessionSource               `json:"source"`
	SessionID         string                      `json:"session_id"`
	RawSessionID      string                      `json:"raw_session_id"`
	ProjectPath       string                      `json:"project_path"`
	SessionFile       string                      `json:"session_file"`
	SessionFormat     string                      `json:"session_format"`
	SnapshotHash      string                      `json:"snapshot_hash"`
	Status            SessionClassificationStatus `json:"status"`
	Stage             SessionClassificationStage  `json:"stage"`
	Category          SessionCategory             `json:"category"`
	Summary           string                      `json:"summary"`
	Confidence        float64                     `json:"confidence"`
	Model             string                      `json:"model"`
	ClassifierVersion string                      `json:"classifier_version"`
	LastError         string                      `json:"last_error"`
	SourceUpdatedAt   time.Time                   `json:"source_updated_at"`
	CreatedAt         time.Time                   `json:"created_at"`
	StageStartedAt    time.Time                   `json:"stage_started_at"`
	UpdatedAt         time.Time                   `json:"updated_at"`
	CompletedAt       time.Time                   `json:"completed_at"`
}

type ContextSearchRequest struct {
	Query             string
	ProjectPath       string
	IncludeHistorical bool
	Limit             int
}

type ContextSearchResult struct {
	Source      string
	ProjectPath string
	ProjectName string
	SessionID   string
	Title       string
	Snippet     string
	UpdatedAt   time.Time
	Score       float64
}

type LLMUsage struct {
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	TotalTokens       int64   `json:"total_tokens"`
	CachedInputTokens int64   `json:"cached_input_tokens"`
	ReasoningTokens   int64   `json:"reasoning_tokens"`
	EstimatedCostUSD  float64 `json:"estimated_cost_usd"`
}

type LLMSessionUsage struct {
	Enabled        bool      `json:"enabled"`
	Model          string    `json:"model"`
	Running        int       `json:"running"`
	Started        int       `json:"started"`
	Completed      int       `json:"completed"`
	Failed         int       `json:"failed"`
	LastStartedAt  time.Time `json:"last_started_at"`
	LastFinishedAt time.Time `json:"last_finished_at"`
	Totals         LLMUsage  `json:"totals"`
}

type DetectorProjectActivity struct {
	ProjectPath  string             `json:"project_path"`
	LastActivity time.Time          `json:"last_activity"`
	Sessions     []SessionEvidence  `json:"sessions"`
	Artifacts    []ArtifactEvidence `json:"artifacts"`
	ErrorCount   int                `json:"error_count"`
	Source       string             `json:"source"`
}

type ProjectState struct {
	Path                 string
	Name                 string
	Kind                 ProjectKind
	LastActivity         time.Time
	Status               ProjectStatus
	AttentionScore       int
	PresentOnDisk        bool
	WorktreeRootPath     string
	WorktreeKind         WorktreeKind
	WorktreeParentBranch string
	WorktreeMergeStatus  WorktreeMergeStatus
	WorktreeOriginTodoID int64
	RepoBranch           string
	RepoDirty            bool
	RepoConflict         bool
	RepoSyncStatus       RepoSyncStatus
	RepoAheadCount       int
	RepoBehindCount      int
	Forgotten            bool
	ManuallyAdded        bool
	InScope              bool
	Pinned               bool
	SnoozedUntil         *time.Time
	RunCommand           string
	MovedFromPath        string
	MovedAt              time.Time
	AttentionReason      []AttentionReason
	Sessions             []SessionEvidence
	Artifacts            []ArtifactEvidence
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type ProjectSummary struct {
	Path                                          string
	Name                                          string
	Kind                                          ProjectKind
	LastActivity                                  time.Time
	Status                                        ProjectStatus
	AttentionScore                                int
	PresentOnDisk                                 bool
	WorktreeRootPath                              string
	WorktreeKind                                  WorktreeKind
	WorktreeParentBranch                          string
	WorktreeMergeStatus                           WorktreeMergeStatus
	WorktreeOriginTodoID                          int64
	RepoBranch                                    string
	RepoDirty                                     bool
	RepoConflict                                  bool
	RepoSyncStatus                                RepoSyncStatus
	RepoAheadCount                                int
	RepoBehindCount                               int
	Forgotten                                     bool
	ManuallyAdded                                 bool
	InScope                                       bool
	Pinned                                        bool
	SnoozedUntil                                  *time.Time
	OpenTODOCount                                 int
	TotalTODOCount                                int
	RunCommand                                    string
	MovedFromPath                                 string
	MovedAt                                       time.Time
	LatestSessionSource                           SessionSource
	LatestSessionID                               string
	LatestRawSessionID                            string
	LatestSessionFormat                           string
	LatestSessionDetectedProjectPath              string
	LatestSessionSnapshotHash                     string
	LatestSessionLastEventAt                      time.Time
	LatestTurnStartedAt                           time.Time
	LatestTurnStateKnown                          bool
	LatestTurnCompleted                           bool
	LatestSessionClassification                   SessionClassificationStatus
	LatestSessionClassificationStage              SessionClassificationStage
	LatestSessionClassificationType               SessionCategory
	LatestSessionSummary                          string
	CreatedAt                                     time.Time
	LastSessionSeenAt                             time.Time
	LatestSessionClassificationStageStartedAt     time.Time
	LatestSessionClassificationUpdatedAt          time.Time
	LatestCompletedSessionClassificationType      SessionCategory
	LatestCompletedSessionSummary                 string
	LatestCompletedSessionClassificationUpdatedAt time.Time
}

type TodoItem struct {
	ID                 int64
	ProjectPath        string
	Text               string
	Done               bool
	Position           int
	CreatedAt          time.Time
	UpdatedAt          time.Time
	CompletedAt        time.Time
	WorktreeSuggestion *TodoWorktreeSuggestion
}

func NormalizeSessionSource(source SessionSource) SessionSource {
	switch source {
	case SessionSourceCodex, SessionSourceOpenCode, SessionSourceClaudeCode:
		return source
	default:
		return SessionSourceUnknown
	}
}

func NormalizeProjectKind(kind ProjectKind) ProjectKind {
	switch kind {
	case ProjectKindScratchTask:
		return kind
	default:
		return ProjectKindProject
	}
}

func NormalizeAgentTaskKind(kind AgentTaskKind) AgentTaskKind {
	switch kind {
	case AgentTaskKindProject, AgentTaskKindScratch, AgentTaskKindSystemOps:
		return kind
	default:
		return AgentTaskKindEphemeral
	}
}

func NormalizeAgentTaskStatus(status AgentTaskStatus) AgentTaskStatus {
	switch status {
	case AgentTaskStatusWaiting, AgentTaskStatusCompleted, AgentTaskStatusArchived:
		return status
	default:
		return AgentTaskStatusActive
	}
}

func NormalizeAgentTaskResourceKind(kind AgentTaskResourceKind) AgentTaskResourceKind {
	switch kind {
	case AgentTaskResourceProject, AgentTaskResourceProcess, AgentTaskResourcePort, AgentTaskResourceFile, AgentTaskResourceEngineerSession:
		return kind
	default:
		return ""
	}
}

func SessionSourceFromFormat(format string) SessionSource {
	switch format {
	case "modern", "legacy":
		return SessionSourceCodex
	case "opencode_db":
		return SessionSourceOpenCode
	case "claude_code":
		return SessionSourceClaudeCode
	default:
		return SessionSourceUnknown
	}
}

func BuildCanonicalSessionID(source SessionSource, rawSessionID string) string {
	source = NormalizeSessionSource(source)
	rawSessionID = strings.TrimSpace(rawSessionID)
	if rawSessionID == "" {
		return ""
	}
	if source == SessionSourceUnknown {
		return rawSessionID
	}
	if parsedSource, parsedRaw := ParseCanonicalSessionID(rawSessionID); parsedSource != SessionSourceUnknown && parsedRaw != "" {
		return string(parsedSource) + ":" + parsedRaw
	}
	return string(source) + ":" + rawSessionID
}

func ParseCanonicalSessionID(sessionID string) (SessionSource, string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return SessionSourceUnknown, ""
	}
	parts := strings.SplitN(sessionID, ":", 2)
	if len(parts) != 2 {
		return SessionSourceUnknown, sessionID
	}
	source := NormalizeSessionSource(SessionSource(parts[0]))
	if source == SessionSourceUnknown {
		return SessionSourceUnknown, sessionID
	}
	rawSessionID := strings.TrimSpace(parts[1])
	if rawSessionID == "" {
		return SessionSourceUnknown, sessionID
	}
	return source, rawSessionID
}

func NormalizeSessionIdentity(source SessionSource, format, sessionID, rawSessionID string) (SessionSource, string, string) {
	source = NormalizeSessionSource(source)
	if source == SessionSourceUnknown {
		source = SessionSourceFromFormat(format)
	}
	sessionID = strings.TrimSpace(sessionID)
	rawSessionID = strings.TrimSpace(rawSessionID)

	if rawSessionID == "" && sessionID != "" {
		if parsedSource, parsedRaw := ParseCanonicalSessionID(sessionID); parsedRaw != "" {
			if source == SessionSourceUnknown {
				source = parsedSource
			}
			rawSessionID = parsedRaw
		} else {
			rawSessionID = sessionID
		}
	}
	if sessionID == "" {
		sessionID = BuildCanonicalSessionID(source, rawSessionID)
	}
	if rawSessionID == "" {
		if _, parsedRaw := ParseCanonicalSessionID(sessionID); parsedRaw != "" {
			rawSessionID = parsedRaw
		}
	}
	if source == SessionSourceUnknown {
		if parsedSource, _ := ParseCanonicalSessionID(sessionID); parsedSource != SessionSourceUnknown {
			source = parsedSource
		}
	}
	if source != SessionSourceUnknown && rawSessionID != "" {
		sessionID = BuildCanonicalSessionID(source, rawSessionID)
	}
	return source, sessionID, rawSessionID
}

func NormalizeSessionEvidenceIdentity(session SessionEvidence) SessionEvidence {
	session.Source, session.SessionID, session.RawSessionID = NormalizeSessionIdentity(session.Source, session.Format, session.SessionID, session.RawSessionID)
	return session
}

func (session SessionEvidence) ExternalID() string {
	return ExternalSessionID(session.Source, session.Format, session.SessionID, session.RawSessionID)
}

func NormalizeSessionClassificationIdentity(classification SessionClassification) SessionClassification {
	classification.Source, classification.SessionID, classification.RawSessionID = NormalizeSessionIdentity(classification.Source, classification.SessionFormat, classification.SessionID, classification.RawSessionID)
	return classification
}

func (classification SessionClassification) ExternalID() string {
	return ExternalSessionID(classification.Source, classification.SessionFormat, classification.SessionID, classification.RawSessionID)
}

func ExternalSessionID(source SessionSource, format, sessionID, rawSessionID string) string {
	_, _, rawSessionID = NormalizeSessionIdentity(source, format, sessionID, rawSessionID)
	if rawSessionID != "" {
		return rawSessionID
	}
	return strings.TrimSpace(sessionID)
}

func (summary ProjectSummary) ExternalLatestSessionID() string {
	return ExternalSessionID(summary.LatestSessionSource, summary.LatestSessionFormat, summary.LatestSessionID, summary.LatestRawSessionID)
}

type TodoWorktreeSuggestionStatus string

const (
	TodoWorktreeSuggestionQueued  TodoWorktreeSuggestionStatus = "queued"
	TodoWorktreeSuggestionRunning TodoWorktreeSuggestionStatus = "running"
	TodoWorktreeSuggestionReady   TodoWorktreeSuggestionStatus = "ready"
	TodoWorktreeSuggestionFailed  TodoWorktreeSuggestionStatus = "failed"
)

type TodoWorktreeSuggestion struct {
	TodoID         int64
	ProjectPath    string
	TodoText       string
	Status         TodoWorktreeSuggestionStatus
	TodoTextHash   string
	BranchName     string
	WorktreeSuffix string
	Kind           string
	Reason         string
	Confidence     float64
	Model          string
	LastError      string
	UpdatedAt      time.Time
}

type ProjectDetail struct {
	Summary                     ProjectSummary
	Reasons                     []AttentionReason
	Todos                       []TodoItem
	Sessions                    []SessionEvidence
	Artifacts                   []ArtifactEvidence
	RecentEvents                []StoredEvent
	LatestSessionClassification *SessionClassification
}

type StoredEvent struct {
	ID          int64
	At          time.Time
	ProjectPath string
	Type        string
	Payload     string
}

type IgnoredProjectName struct {
	Name            string
	CreatedAt       time.Time
	MatchedProjects int
}

type ClaudeCodeToolUse struct {
	ID      string
	Name    string
	Summary string
}

type ClaudeCodeTranscriptEntry struct {
	UUID      string
	Kind      string // "user", "assistant"
	Text      string
	Model     string
	Timestamp time.Time
	ToolUses  []ClaudeCodeToolUse
}

func StatusRank(s ProjectStatus) int {
	switch s {
	case StatusPossiblyStuck:
		return 3
	case StatusIdle:
		return 2
	case StatusActive:
		return 1
	default:
		return 0
	}
}
