package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/uisurface"
)

const mobileRecordedSessionLimit = 8

const (
	mobileSessionInputMaxBodyBytes = 32 * 1024
	mobileSessionInputMaxRunes     = 12000
	mobileSessionRequestTTL        = 10 * time.Minute
	mobileLiveSnapshotCacheTTL     = 10 * time.Second
	mobileLiveStreamPollInterval   = 150 * time.Millisecond
	mobileLiveStreamKeepalive      = 15 * time.Second
)

type mobileLiveSnapshot struct {
	snapshot   codexapp.Snapshot
	observedAt time.Time
}

type LiveSessionSource interface {
	TrySessionSnapshot(projectPath string) (codexapp.Snapshot, bool)
}

type LiveSessionController interface {
	LiveSessionSource
	SubmitSessionInput(projectPath, expectedThreadID string, input codexapp.Submission) (codexapp.SessionInputResult, error)
}

type mobileSessionInputRequest struct {
	ProjectPath string `json:"project_path"`
	SessionID   string `json:"session_id"`
	RequestID   string `json:"request_id"`
	Text        string `json:"text"`
}

type mobileSessionInputResponse struct {
	RequestID string `json:"request_id"`
	Mode      string `json:"mode"`
	Status    string `json:"status"`
}

type mobileLiveStreamRevision struct {
	threadID           string
	transcriptRevision uint64
	phase              codexapp.SessionPhase
	started            bool
	busy               bool
	busyExternal       bool
	closed             bool
	pendingApproval    bool
	pendingToolInput   bool
	pendingElicitation bool
	status             string
	lastError          string
	lastSystemNotice   string
	lastActivityAt     time.Time
	model              string
	reasoningEffort    string
	permissionLevel    string
	goalStatus         codexapp.ThreadGoalStatus
	goalObjective      string
	goalUpdatedAt      time.Time
	mobileInputEnabled bool
}

func (s *Server) WithLiveSessions(source LiveSessionSource) *Server {
	if s != nil {
		s.mobileLiveMu.Lock()
		s.liveSessions = source
		s.mobileLiveSnapshots = nil
		s.mobileLiveMu.Unlock()
	}
	return s
}

func (s *Server) mobileDashboardLiveSessions(projects []uisurface.ProjectItem, now time.Time) []uisurface.EngineerSessionItem {
	items := make([]uisurface.EngineerSessionItem, 0)
	for _, project := range projects {
		snapshot, ok := s.liveSessionSnapshot(project.Path)
		if !ok || snapshot.Closed || snapshot.Phase == codexapp.SessionPhaseClosed {
			continue
		}
		item := uisurface.BuildLiveEngineerSession(snapshot, now)
		item.ProjectName = project.Name
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		leftRank := mobileLiveSessionMonitorRank(items[i])
		rightRank := mobileLiveSessionMonitorRank(items[j])
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return items[i].LastActivityAt.After(items[j].LastActivityAt)
	})
	return items
}

func mobileLiveSessionMonitorRank(item uisurface.EngineerSessionItem) int {
	switch item.Status.Tone {
	case uisurface.ToneDanger, uisurface.ToneWarning:
		return 0
	case uisurface.TonePositive:
		return 1
	case uisurface.ToneInfo:
		return 2
	default:
		return 3
	}
}

func (s *Server) handleMobileProjectSessions(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	projectPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if projectPath == "" {
		http.Error(w, "missing path query param", http.StatusBadRequest)
		return
	}
	detail, err := s.visibleMobileProjectDetail(r.Context(), projectPath)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	now := time.Now()
	items := make([]uisurface.EngineerSessionItem, 0, min(len(detail.Sessions)+1, mobileRecordedSessionLimit+1))
	seen := make(map[string]struct{})
	if snapshot, ok := s.liveSessionSnapshot(projectPath); ok {
		item := uisurface.BuildLiveEngineerSession(snapshot, now)
		items = append(items, item)
		seen[item.ID] = struct{}{}
	}

	classifications, _ := s.svc.Store().ListSessionClassifications(r.Context(), projectPath, "")
	classificationByID := mobileSessionClassificationsByID(classifications)
	for _, evidence := range detail.Sessions {
		if len(items) >= mobileRecordedSessionLimit {
			break
		}
		evidence = model.NormalizeSessionEvidenceIdentity(evidence)
		if evidence.SessionID == "" {
			continue
		}
		if _, exists := seen[evidence.SessionID]; exists {
			continue
		}
		item := uisurface.BuildRecordedEngineerSession(evidence, mobileSessionClassification(classificationByID, evidence), now)
		items = append(items, item)
		seen[item.ID] = struct{}{}
	}

	writeJSON(w, uisurface.EngineerSessionListSurface{
		ProjectPath: projectPath,
		Sessions:    items,
	})
}

func (s *Server) handleMobileSessionDetail(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	projectPath := strings.TrimSpace(r.URL.Query().Get("path"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if projectPath == "" || sessionID == "" {
		http.Error(w, "missing path or session_id query param", http.StatusBadRequest)
		return
	}
	detail, err := s.visibleMobileProjectDetail(r.Context(), projectPath)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	if snapshot, ok := s.liveSessionSnapshot(projectPath); ok {
		liveItem := uisurface.BuildLiveEngineerSession(snapshot, time.Now())
		if sessionID == liveItem.ID {
			surface := uisurface.BuildLiveEngineerSessionDetail(snapshot, time.Now())
			surface.Input = uisurface.BuildEngineerSessionInput(snapshot, s.svc.Config().MobileInputEnabled)
			writeJSON(w, surface)
			return
		}
	}

	evidence, ok := mobileSessionEvidence(detail.Sessions, sessionID)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	classifications, _ := s.svc.Store().ListSessionClassifications(r.Context(), projectPath, evidence.SessionID)
	classification := mobileSessionClassification(mobileSessionClassificationsByID(classifications), evidence)
	excerpt, excerptErr := s.svc.Store().GetSessionContextExcerpt(r.Context(), model.SessionContextExcerptRequest{
		SessionID:   evidence.SessionID,
		BeforeTurns: 79,
		AfterTurns:  0,
		MaxChars:    12000,
	})
	if excerptErr != nil && errors.Is(excerptErr, context.Canceled) {
		http.Error(w, "request canceled", http.StatusRequestTimeout)
		return
	}
	writeJSON(w, uisurface.BuildRecordedEngineerSessionDetail(evidence, classification, excerpt, time.Now()))
}

func (s *Server) handleMobileSessionStream(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		return
	}
	projectPath := strings.TrimSpace(r.URL.Query().Get("path"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if projectPath == "" || sessionID == "" {
		http.Error(w, "missing path or session_id query param", http.StatusBadRequest)
		return
	}
	if _, err := s.visibleMobileProjectDetail(r.Context(), projectPath); err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	snapshot, ok := s.liveSessionSnapshot(projectPath)
	if !ok {
		http.Error(w, "live engineer session not found", http.StatusNotFound)
		return
	}
	if liveItem := uisurface.BuildLiveEngineerSession(snapshot, time.Now()); liveItem.ID != sessionID {
		http.Error(w, "engineer session changed; reopen the current channel", http.StatusConflict)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "live session streaming is unavailable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, err := fmt.Fprint(w, "retry: 1000\n\n"); err != nil {
		return
	}

	lastRevision := mobileLiveStreamRevision{}
	haveRevision := false
	sendSnapshot := func(snapshot codexapp.Snapshot, now time.Time) error {
		inputEnabled := s.svc.Config().MobileInputEnabled
		revision := buildMobileLiveStreamRevision(snapshot, inputEnabled)
		if haveRevision && revision == lastRevision {
			return nil
		}
		surface := uisurface.BuildLiveEngineerSessionDetail(snapshot, now)
		surface.Input = uisurface.BuildEngineerSessionInput(snapshot, inputEnabled)
		payload, err := json.Marshal(surface)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: session\ndata: %s\n\n", payload); err != nil {
			return err
		}
		lastRevision = revision
		haveRevision = true
		flusher.Flush()
		return nil
	}
	if err := sendSnapshot(snapshot, time.Now()); err != nil {
		return
	}

	poll := time.NewTicker(mobileLiveStreamPollInterval)
	keepalive := time.NewTicker(mobileLiveStreamKeepalive)
	defer poll.Stop()
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case now := <-poll.C:
			snapshot, ok := s.liveSessionSnapshotAt(projectPath, now)
			if !ok {
				_, _ = fmt.Fprint(w, "event: end\ndata: session unavailable\n\n")
				flusher.Flush()
				return
			}
			liveItem := uisurface.BuildLiveEngineerSession(snapshot, now)
			if liveItem.ID != sessionID {
				_, _ = fmt.Fprint(w, "event: replaced\ndata: engineer session changed\n\n")
				flusher.Flush()
				return
			}
			if err := sendSnapshot(snapshot, now); err != nil {
				return
			}
			if snapshot.Closed || snapshot.Phase == codexapp.SessionPhaseClosed {
				_, _ = fmt.Fprint(w, "event: end\ndata: session closed\n\n")
				flusher.Flush()
				return
			}
		}
	}
}

func buildMobileLiveStreamRevision(snapshot codexapp.Snapshot, mobileInputEnabled bool) mobileLiveStreamRevision {
	revision := mobileLiveStreamRevision{
		threadID:           strings.TrimSpace(snapshot.ThreadID),
		transcriptRevision: snapshot.TranscriptRevision,
		phase:              snapshot.Phase,
		started:            snapshot.Started,
		busy:               snapshot.Busy,
		busyExternal:       snapshot.BusyExternal,
		closed:             snapshot.Closed,
		pendingApproval:    snapshot.PendingApproval != nil,
		pendingToolInput:   snapshot.PendingToolInput != nil,
		pendingElicitation: snapshot.PendingElicitation != nil,
		status:             snapshot.Status,
		lastError:          snapshot.LastError,
		lastSystemNotice:   snapshot.LastSystemNotice,
		lastActivityAt:     snapshot.LastActivityAt,
		model:              snapshot.Model,
		reasoningEffort:    snapshot.ReasoningEffort,
		permissionLevel:    snapshot.PermissionLevel,
		mobileInputEnabled: mobileInputEnabled,
	}
	if snapshot.Goal != nil {
		revision.goalStatus = snapshot.Goal.Status
		revision.goalObjective = snapshot.Goal.Objective
		revision.goalUpdatedAt = snapshot.Goal.UpdatedAt
	}
	return revision
}

func (s *Server) handleMobileSessionInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	if s == nil || s.svc == nil || !s.svc.Config().MobileInputEnabled {
		http.Error(w, "session messages are disabled in Mobile settings", http.StatusForbidden)
		return
	}

	var request mobileSessionInputRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, mobileSessionInputMaxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || ensureJSONEOF(decoder) != nil {
		http.Error(w, "invalid session message request", http.StatusBadRequest)
		return
	}
	request.ProjectPath = strings.TrimSpace(request.ProjectPath)
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.Text = strings.TrimSpace(request.Text)
	if request.ProjectPath == "" || request.SessionID == "" || request.RequestID == "" || request.Text == "" {
		http.Error(w, "project, session, request ID, and message are required", http.StatusBadRequest)
		return
	}
	if len(request.RequestID) > 128 || len([]rune(request.Text)) > mobileSessionInputMaxRunes {
		http.Error(w, "session message request is too large", http.StatusRequestEntityTooLarge)
		return
	}
	if _, err := s.visibleMobileProjectDetail(r.Context(), request.ProjectPath); err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	snapshot, ok := s.liveSessionSnapshot(request.ProjectPath)
	if !ok {
		http.Error(w, "live engineer session not found", http.StatusNotFound)
		return
	}
	liveItem := uisurface.BuildLiveEngineerSession(snapshot, time.Now())
	if request.SessionID != liveItem.ID {
		http.Error(w, "engineer session changed; reopen the current channel", http.StatusConflict)
		return
	}
	controller, ok := s.liveSessions.(LiveSessionController)
	if !ok {
		http.Error(w, "live engineer session input is unavailable", http.StatusServiceUnavailable)
		return
	}
	if !s.claimMobileInputRequest(request.RequestID, time.Now()) {
		http.Error(w, "session message request was already handled", http.StatusConflict)
		return
	}
	result, err := controller.SubmitSessionInput(request.ProjectPath, snapshot.ThreadID, codexapp.Submission{Text: request.Text})
	if err != nil {
		s.releaseMobileInputRequest(request.RequestID)
		status := http.StatusConflict
		if !errors.Is(err, codexapp.ErrSessionInputUnavailable) && !errors.Is(err, codexapp.ErrSessionChanged) {
			status = http.StatusBadGateway
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, mobileSessionInputResponse{
		RequestID: request.RequestID,
		Mode:      string(result.Mode),
		Status:    mobileSessionInputStatus(result.Mode),
	})
}

func mobileSessionInputStatus(mode codexapp.SessionInputMode) string {
	switch mode {
	case codexapp.SessionInputSteer:
		return "Steer sent"
	case codexapp.SessionInputQueue:
		return "Message queued"
	default:
		return "Message sent"
	}
}

func (s *Server) claimMobileInputRequest(requestID string, now time.Time) bool {
	s.mobileInputMu.Lock()
	defer s.mobileInputMu.Unlock()
	if s.mobileInputRequests == nil {
		s.mobileInputRequests = make(map[string]time.Time)
	}
	for id, claimedAt := range s.mobileInputRequests {
		if now.Sub(claimedAt) >= mobileSessionRequestTTL {
			delete(s.mobileInputRequests, id)
		}
	}
	if _, exists := s.mobileInputRequests[requestID]; exists {
		return false
	}
	s.mobileInputRequests[requestID] = now
	return true
}

func (s *Server) releaseMobileInputRequest(requestID string) {
	s.mobileInputMu.Lock()
	delete(s.mobileInputRequests, requestID)
	s.mobileInputMu.Unlock()
}

func (s *Server) visibleMobileProjectDetail(ctx context.Context, projectPath string) (model.ProjectDetail, error) {
	detail, err := s.svc.Store().GetProjectDetail(ctx, projectPath, 0)
	if err != nil {
		return model.ProjectDetail{}, err
	}
	cfg := s.svc.Config()
	if projectSummaryHidden(detail.Summary, cfg.ExcludeProjectPatterns) || (cfg.PrivacyMode && detail.Summary.CategoryPrivate) {
		return model.ProjectDetail{}, errors.New("project not found")
	}
	return detail, nil
}

func (s *Server) liveSessionSnapshot(projectPath string) (codexapp.Snapshot, bool) {
	return s.liveSessionSnapshotAt(projectPath, time.Now())
}

func (s *Server) liveSessionSnapshotAt(projectPath string, now time.Time) (codexapp.Snapshot, bool) {
	if s == nil {
		return codexapp.Snapshot{}, false
	}
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return codexapp.Snapshot{}, false
	}
	s.mobileLiveMu.Lock()
	source := s.liveSessions
	s.mobileLiveMu.Unlock()
	if source == nil {
		return codexapp.Snapshot{}, false
	}
	snapshot, ok := source.TrySessionSnapshot(projectPath)
	if ok && sameCleanPath(snapshot.ProjectPath, projectPath) {
		s.storeMobileLiveSnapshot(projectPath, snapshot, now)
		return snapshot, true
	}
	return s.cachedMobileLiveSnapshot(projectPath, now)
}

func (s *Server) storeMobileLiveSnapshot(projectPath string, snapshot codexapp.Snapshot, observedAt time.Time) {
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	s.mobileLiveMu.Lock()
	defer s.mobileLiveMu.Unlock()
	if s.mobileLiveSnapshots == nil {
		s.mobileLiveSnapshots = make(map[string]mobileLiveSnapshot)
	}
	s.mobileLiveSnapshots[filepath.Clean(projectPath)] = mobileLiveSnapshot{
		snapshot:   snapshot,
		observedAt: observedAt,
	}
}

func (s *Server) cachedMobileLiveSnapshot(projectPath string, now time.Time) (codexapp.Snapshot, bool) {
	if now.IsZero() {
		now = time.Now()
	}
	key := filepath.Clean(projectPath)
	s.mobileLiveMu.Lock()
	defer s.mobileLiveMu.Unlock()
	cached, ok := s.mobileLiveSnapshots[key]
	if !ok {
		return codexapp.Snapshot{}, false
	}
	if now.Before(cached.observedAt) || now.Sub(cached.observedAt) > mobileLiveSnapshotCacheTTL {
		delete(s.mobileLiveSnapshots, key)
		return codexapp.Snapshot{}, false
	}
	return cached.snapshot, true
}

func sameCleanPath(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	return left != "" && right != "" && filepath.Clean(left) == filepath.Clean(right)
}

func mobileSessionEvidence(sessions []model.SessionEvidence, sessionID string) (model.SessionEvidence, bool) {
	for _, evidence := range sessions {
		evidence = model.NormalizeSessionEvidenceIdentity(evidence)
		if sessionID == evidence.SessionID || sessionID == evidence.RawSessionID || sessionID == evidence.ExternalID() {
			return evidence, true
		}
	}
	return model.SessionEvidence{}, false
}

func mobileSessionClassificationsByID(classifications []model.SessionClassification) map[string]model.SessionClassification {
	byID := make(map[string]model.SessionClassification, len(classifications)*2)
	for _, classification := range classifications {
		classification = model.NormalizeSessionClassificationIdentity(classification)
		if classification.SessionID != "" {
			byID[classification.SessionID] = classification
		}
		if classification.RawSessionID != "" {
			byID[classification.RawSessionID] = classification
		}
	}
	return byID
}

func mobileSessionClassification(byID map[string]model.SessionClassification, evidence model.SessionEvidence) model.SessionClassification {
	if classification, ok := byID[evidence.SessionID]; ok {
		return classification
	}
	return byID[evidence.RawSessionID]
}
