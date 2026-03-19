package sessionclassify

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

const (
	DefaultModel      = "gpt-5.4-nano"
	ClassifierVersion = "session-v1"
)

type Result struct {
	Category   model.SessionCategory
	Summary    string
	Confidence float64
	Model      string
	Usage      model.LLMUsage
}

type Classifier interface {
	Classify(ctx context.Context, snapshot SessionSnapshot) (Result, error)
}

type ProjectUpdater func(ctx context.Context, projectPath string) error

type Options struct {
	Client           Classifier
	Workers          int
	RetryAfter       time.Duration
	StaleAfter       time.Duration
	OnProjectUpdated ProjectUpdater
}

type Manager struct {
	store            *store.Store
	bus              *events.Bus
	mu               sync.RWMutex
	client           Classifier
	modelName        string
	workers          int
	retryAfter       time.Duration
	staleAfter       time.Duration
	onProjectUpdated ProjectUpdater
	notifyCh         chan struct{}
	usage            *usageTracker
	startOnce        sync.Once
}

type usageTracker struct {
	mu       sync.Mutex
	snapshot model.LLMSessionUsage
}

func NewManager(st *store.Store, bus *events.Bus, opts Options) *Manager {
	workers := opts.Workers
	if workers <= 0 {
		workers = 3
	}
	retryAfter := opts.RetryAfter
	if retryAfter <= 0 {
		retryAfter = 30 * time.Minute
	}
	staleAfter := opts.StaleAfter
	if staleAfter <= 0 {
		staleAfter = 2 * time.Minute
	}

	modelName := ""
	if named, ok := opts.Client.(interface{ ModelName() string }); ok {
		modelName = strings.TrimSpace(named.ModelName())
	}

	return &Manager{
		store:            st,
		bus:              bus,
		client:           opts.Client,
		modelName:        modelName,
		workers:          workers,
		retryAfter:       retryAfter,
		staleAfter:       staleAfter,
		onProjectUpdated: opts.OnProjectUpdated,
		notifyCh:         make(chan struct{}, 1),
		usage:            newUsageTracker(modelName),
	}
}

func (m *Manager) ConfigureClient(client Classifier) {
	if m == nil {
		return
	}
	modelName := ""
	if named, ok := client.(interface{ ModelName() string }); ok {
		modelName = strings.TrimSpace(named.ModelName())
	}
	m.mu.Lock()
	m.client = client
	m.modelName = modelName
	m.mu.Unlock()
}

func (m *Manager) currentClient() (Classifier, string) {
	if m == nil {
		return nil, ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.client, m.modelName
}

func (m *Manager) classificationHeartbeatInterval() time.Duration {
	if m == nil {
		return 30 * time.Second
	}
	if m.staleAfter > 0 {
		interval := m.staleAfter / 2
		if interval > 0 && interval < 30*time.Second {
			return interval
		}
	}
	return 30 * time.Second
}

func (m *Manager) heartbeatClassification(ctx context.Context, classification *model.SessionClassification) {
	if m == nil || classification == nil {
		return
	}
	interval := m.classificationHeartbeatInterval()
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			touched, err := m.store.TouchSessionClassification(ctx, classification)
			if err != nil || !touched {
				return
			}
		}
	}
}

func (m *Manager) Enabled() bool {
	client, _ := m.currentClient()
	return client != nil
}

func (m *Manager) QueueProject(ctx context.Context, state model.ProjectState) (bool, error) {
	return m.QueueProjectRetry(ctx, state, m.retryAfter)
}

func (m *Manager) QueueProjectRetry(ctx context.Context, state model.ProjectState, retryAfter time.Duration) (bool, error) {
	_, modelName := m.currentClient()
	if !m.Enabled() {
		return false, nil
	}
	prepared := state
	if len(prepared.Sessions) > 0 && strings.TrimSpace(prepared.Sessions[0].SnapshotHash) == "" {
		gitStatus := NewGitStatusSnapshot(prepared.RepoDirty, prepared.RepoSyncStatus, prepared.RepoAheadCount, prepared.RepoBehindCount)
		if hash, err := ComputeSnapshotHash(ctx, prepared.Path, prepared.Sessions[0], gitStatus); err == nil && strings.TrimSpace(hash) != "" {
			prepared.Sessions[0].SnapshotHash = hash
		}
	}
	classification, ok := BuildClassificationRequest(prepared)
	if !ok {
		return false, nil
	}
	if modelName != "" {
		classification.Model = modelName
	}
	if retryAfter < 0 {
		retryAfter = 0
	}
	return m.store.QueueSessionClassification(ctx, classification, retryAfter)
}

func BuildClassificationRequest(state model.ProjectState) (model.SessionClassification, bool) {
	if state.Path == "" || len(state.Sessions) == 0 {
		return model.SessionClassification{}, false
	}
	latest := state.Sessions[0]
	if latest.SessionID == "" || latest.SessionFile == "" {
		return model.SessionClassification{}, false
	}
	snapshotHash := strings.TrimSpace(latest.SnapshotHash)
	if snapshotHash == "" {
		return model.SessionClassification{}, false
	}
	switch latest.Format {
	case "modern", "legacy", "opencode_db":
	default:
		return model.SessionClassification{}, false
	}

	return model.SessionClassification{
		SessionID:         latest.SessionID,
		ProjectPath:       state.Path,
		SessionFile:       latest.SessionFile,
		SessionFormat:     latest.Format,
		SnapshotHash:      snapshotHash,
		Status:            model.ClassificationPending,
		Model:             DefaultModel,
		ClassifierVersion: ClassifierVersion,
		SourceUpdatedAt:   latest.LastEventAt,
	}, true
}

func SnapshotHashForSession(session model.SessionEvidence, projectPath string) string {
	if strings.TrimSpace(session.SnapshotHash) != "" {
		return strings.TrimSpace(session.SnapshotHash)
	}
	return legacySnapshotHashForSession(session, projectPath)
}

func legacySnapshotHashForSession(session model.SessionEvidence, projectPath string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		projectPath,
		session.SessionID,
		session.SessionFile,
		session.Format,
		session.StartedAt.UTC().Format(time.RFC3339Nano),
		session.LastEventAt.UTC().Format(time.RFC3339Nano),
		fmt.Sprintf("%d", session.ErrorCount),
		fmt.Sprintf("%t", session.LatestTurnStateKnown),
		fmt.Sprintf("%t", session.LatestTurnCompleted),
	}, "|")))
	return hex.EncodeToString(sum[:])
}

func ComputeSnapshotHash(ctx context.Context, projectPath string, session model.SessionEvidence, gitStatus GitStatusSnapshot) (string, error) {
	snapshot, err := ExtractSnapshot(ctx, model.SessionClassification{
		SessionID:       session.SessionID,
		ProjectPath:     projectPath,
		SessionFile:     session.SessionFile,
		SessionFormat:   session.Format,
		SourceUpdatedAt: session.LastEventAt,
	}, session, gitStatus)
	if err != nil {
		return "", err
	}
	return SnapshotHashForSnapshot(snapshot), nil
}

func SnapshotHashForSnapshot(snapshot SessionSnapshot) string {
	payload := struct {
		ProjectPath          string            `json:"project_path"`
		SessionID            string            `json:"session_id"`
		SessionFormat        string            `json:"session_format"`
		LatestTurnStateKnown bool              `json:"latest_turn_state_known"`
		LatestTurnCompleted  bool              `json:"latest_turn_completed"`
		GitStatus            GitStatusSnapshot `json:"git_status,omitempty"`
		Transcript           []TranscriptItem  `json:"transcript"`
	}{
		ProjectPath:          snapshot.ProjectPath,
		SessionID:            snapshot.SessionID,
		SessionFormat:        snapshot.SessionFormat,
		LatestTurnStateKnown: snapshot.LatestTurnStateKnown,
		LatestTurnCompleted:  snapshot.LatestTurnCompleted,
		GitStatus:            snapshot.GitStatus,
		Transcript:           snapshot.Transcript,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		sum := sha256.Sum256([]byte(strings.Join([]string{
			snapshot.ProjectPath,
			snapshot.SessionID,
			snapshot.SessionFormat,
		}, "|")))
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (m *Manager) Notify() {
	if !m.Enabled() {
		return
	}
	select {
	case m.notifyCh <- struct{}{}:
	default:
	}
}

func (m *Manager) UsageSnapshot() model.LLMSessionUsage {
	if m == nil {
		return model.LLMSessionUsage{}
	}
	if m.usage == nil {
		_, modelName := m.currentClient()
		return model.LLMSessionUsage{
			Enabled: m.Enabled(),
			Model:   strings.TrimSpace(modelName),
		}
	}
	return m.usage.snapshotFor(m.Enabled())
}

func (m *Manager) Start(ctx context.Context) {
	if m == nil {
		return
	}
	m.startOnce.Do(func() {
		for i := 0; i < m.workers; i++ {
			go m.runWorker(ctx)
		}
	})
	m.Notify()
}

func (m *Manager) runWorker(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		processed, err := m.processOne(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		if processed {
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-m.notifyCh:
		case <-ticker.C:
		}
	}
}

func (m *Manager) processOne(ctx context.Context) (bool, error) {
	client, defaultModelName := m.currentClient()
	if client == nil {
		return false, nil
	}

	classification, err := m.store.ClaimNextPendingSessionClassification(ctx, m.staleAfter)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	gitStatus := GitStatusSnapshot{}
	sessionEvidence := model.SessionEvidence{
		SessionID:   classification.SessionID,
		ProjectPath: classification.ProjectPath,
		SessionFile: classification.SessionFile,
		Format:      classification.SessionFormat,
		LastEventAt: classification.SourceUpdatedAt,
	}
	if detail, detailErr := m.store.GetProjectDetail(ctx, classification.ProjectPath, 1); detailErr == nil {
		gitStatus = NewGitStatusSnapshot(detail.Summary.RepoDirty, detail.Summary.RepoSyncStatus, detail.Summary.RepoAheadCount, detail.Summary.RepoBehindCount)
		for _, session := range detail.Sessions {
			if session.SessionID != classification.SessionID {
				continue
			}
			sessionEvidence = session
			break
		}
	}
	_ = RecoverSessionTurnState(&sessionEvidence)

	snapshot, err := ExtractSnapshot(ctx, classification, sessionEvidence, gitStatus)
	if err != nil {
		m.failClassification(ctx, &classification, err)
		return true, nil
	}
	classification.SnapshotHash = SnapshotHashForSnapshot(snapshot)
	sessionEvidence.SnapshotHash = classification.SnapshotHash
	sessionEvidence.LatestTurnStateKnown = snapshot.LatestTurnStateKnown
	sessionEvidence.LatestTurnCompleted = snapshot.LatestTurnCompleted
	if snapshot.LatestTurnCompleted {
		sessionEvidence.LatestTurnStartedAt = time.Time{}
	}
	if err := m.store.UpdateSessionEvidenceMetadata(ctx, sessionEvidence); err != nil {
		return true, err
	}
	advanced, err := m.store.AdvanceSessionClassificationStage(ctx, &classification, model.ClassificationStageWaitingForModel)
	if err != nil {
		return true, err
	}
	if !advanced {
		return true, nil
	}

	modelName := strings.TrimSpace(classification.Model)
	if modelName == "" {
		modelName = strings.TrimSpace(defaultModelName)
	}
	if m.usage != nil {
		m.usage.start(modelName)
	}

	classifyCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go m.heartbeatClassification(classifyCtx, &classification)

	result, err := client.Classify(ctx, snapshot)
	stopHeartbeat()
	if err != nil {
		if m.usage != nil {
			m.usage.fail(modelName)
		}
		m.failClassification(ctx, &classification, err)
		return true, nil
	}
	if strings.TrimSpace(result.Model) != "" {
		classification.Model = strings.TrimSpace(result.Model)
		modelName = classification.Model
	}
	if m.usage != nil {
		m.usage.complete(modelName, result.Usage)
	}

	classification.Category = result.Category
	classification.Summary = clipForStorage(result.Summary, 280)
	classification.Confidence = clampConfidence(result.Confidence)
	classification.CompletedAt = time.Now()
	completed, err := m.store.CompleteSessionClassificationAttempt(ctx, &classification)
	if err != nil {
		return true, err
	}
	if !completed {
		return true, nil
	}

	m.publishClassificationEvent(ctx, classification, "completed")
	if m.onProjectUpdated != nil {
		_ = m.onProjectUpdated(ctx, classification.ProjectPath)
	}
	return true, nil
}

func (m *Manager) failClassification(ctx context.Context, classification *model.SessionClassification, err error) {
	if classification == nil {
		return
	}
	msg := clipForStorage(err.Error(), 280)
	failed, storeErr := m.store.FailSessionClassificationAttempt(ctx, classification, msg)
	if storeErr != nil || !failed {
		return
	}
	m.publishClassificationEvent(ctx, *classification, "failed")
}

func (m *Manager) publishClassificationEvent(ctx context.Context, classification model.SessionClassification, state string) {
	now := time.Now()
	payload := map[string]string{
		"status":   state,
		"session":  classification.SessionID,
		"category": string(classification.Category),
	}
	if classification.Summary != "" {
		payload["summary"] = classification.Summary
	}
	if m.bus != nil {
		m.bus.Publish(events.Event{
			Type:        events.ClassificationUpdated,
			At:          now,
			ProjectPath: classification.ProjectPath,
			Payload:     payload,
		})
	}

	eventPayload := fmt.Sprintf("classification %s", state)
	if classification.Category != "" {
		eventPayload = fmt.Sprintf("%s category=%s", eventPayload, classification.Category)
	}
	if classification.Summary != "" {
		eventPayload = fmt.Sprintf("%s summary=%s", eventPayload, classification.Summary)
	}
	_ = m.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: classification.ProjectPath,
		Type:        string(events.ClassificationUpdated),
		Payload:     eventPayload,
	})
}

func clampConfidence(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

func newUsageTracker(modelName string) *usageTracker {
	return &usageTracker{
		snapshot: model.LLMSessionUsage{
			Model: strings.TrimSpace(modelName),
		},
	}
}

func (u *usageTracker) start(modelName string) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if trimmed := strings.TrimSpace(modelName); trimmed != "" {
		u.snapshot.Model = trimmed
	}
	u.snapshot.Running++
	u.snapshot.Started++
	u.snapshot.LastStartedAt = time.Now()
}

func (u *usageTracker) complete(modelName string, usage model.LLMUsage) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if trimmed := strings.TrimSpace(modelName); trimmed != "" {
		u.snapshot.Model = trimmed
	}
	if u.snapshot.Running > 0 {
		u.snapshot.Running--
	}
	u.snapshot.Completed++
	u.snapshot.LastFinishedAt = time.Now()
	u.snapshot.Totals.InputTokens += usage.InputTokens
	u.snapshot.Totals.OutputTokens += usage.OutputTokens
	u.snapshot.Totals.TotalTokens += usage.TotalTokens
	u.snapshot.Totals.CachedInputTokens += usage.CachedInputTokens
	u.snapshot.Totals.ReasoningTokens += usage.ReasoningTokens
}

func (u *usageTracker) fail(modelName string) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if trimmed := strings.TrimSpace(modelName); trimmed != "" {
		u.snapshot.Model = trimmed
	}
	if u.snapshot.Running > 0 {
		u.snapshot.Running--
	}
	u.snapshot.Failed++
	u.snapshot.LastFinishedAt = time.Now()
}

func (u *usageTracker) snapshotFor(enabled bool) model.LLMSessionUsage {
	if u == nil {
		return model.LLMSessionUsage{Enabled: enabled}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	snapshot := u.snapshot
	snapshot.Enabled = enabled
	return snapshot
}

func clipForStorage(s string, limit int) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if limit <= 0 || len(s) <= limit {
		return s
	}
	if limit <= 3 {
		return s[:limit]
	}
	return s[:limit-3] + "..."
}
