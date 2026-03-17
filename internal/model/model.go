package model

import "time"

type ProjectStatus string

const (
	StatusActive        ProjectStatus = "active"
	StatusIdle          ProjectStatus = "idle"
	StatusPossiblyStuck ProjectStatus = "possibly_stuck"
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

type SessionEvidence struct {
	SessionID           string    `json:"session_id"`
	ProjectPath         string    `json:"project_path"`
	DetectedProjectPath string    `json:"detected_project_path"`
	SessionFile         string    `json:"session_file"`
	Format              string    `json:"format"`
	SnapshotHash        string    `json:"snapshot_hash"`
	StartedAt           time.Time `json:"started_at"`
	LastEventAt         time.Time `json:"last_event_at"`
	ErrorCount          int       `json:"error_count"`
	LatestTurnStartedAt time.Time `json:"latest_turn_started_at"`
	// Best-effort signal from structured session events (task_started/task_complete).
	LatestTurnStateKnown bool `json:"latest_turn_state_known"`
	LatestTurnCompleted  bool `json:"latest_turn_completed"`
}

type ProjectGitFingerprint struct {
	ProjectPath  string
	HeadHash     string
	RecentHashes []string
	UpdatedAt    time.Time
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
	SessionID         string                      `json:"session_id"`
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

type LLMUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	ReasoningTokens   int64 `json:"reasoning_tokens"`
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
	Path            string
	Name            string
	LastActivity    time.Time
	Status          ProjectStatus
	AttentionScore  int
	PresentOnDisk   bool
	RepoDirty       bool
	RepoSyncStatus  RepoSyncStatus
	RepoAheadCount  int
	RepoBehindCount int
	Forgotten       bool
	ManuallyAdded   bool
	InScope         bool
	Pinned          bool
	SnoozedUntil    *time.Time
	Note            string
	RunCommand      string
	MovedFromPath   string
	MovedAt         time.Time
	AttentionReason []AttentionReason
	Sessions        []SessionEvidence
	Artifacts       []ArtifactEvidence
	UpdatedAt       time.Time
}

type ProjectSummary struct {
	Path                                          string
	Name                                          string
	LastActivity                                  time.Time
	Status                                        ProjectStatus
	AttentionScore                                int
	PresentOnDisk                                 bool
	RepoDirty                                     bool
	RepoSyncStatus                                RepoSyncStatus
	RepoAheadCount                                int
	RepoBehindCount                               int
	Forgotten                                     bool
	ManuallyAdded                                 bool
	InScope                                       bool
	Pinned                                        bool
	SnoozedUntil                                  *time.Time
	Note                                          string
	RunCommand                                    string
	MovedFromPath                                 string
	MovedAt                                       time.Time
	LatestSessionID                               string
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
	LatestSessionClassificationStageStartedAt     time.Time
	LatestSessionClassificationUpdatedAt          time.Time
	LatestCompletedSessionClassificationType      SessionCategory
	LatestCompletedSessionSummary                 string
	LatestCompletedSessionClassificationUpdatedAt time.Time
}

type ProjectDetail struct {
	Summary                     ProjectSummary
	Reasons                     []AttentionReason
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
