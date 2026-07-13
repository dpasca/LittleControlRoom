package server

import (
	"context"
	"errors"
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

type LiveSessionSource interface {
	TrySessionSnapshot(projectPath string) (codexapp.Snapshot, bool)
}

func (s *Server) WithLiveSessions(source LiveSessionSource) *Server {
	if s != nil {
		s.liveSessions = source
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
			writeJSON(w, uisurface.BuildLiveEngineerSessionDetail(snapshot, time.Now()))
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
	if s == nil || s.liveSessions == nil {
		return codexapp.Snapshot{}, false
	}
	snapshot, ok := s.liveSessions.TrySessionSnapshot(projectPath)
	if !ok || !sameCleanPath(snapshot.ProjectPath, projectPath) {
		return codexapp.Snapshot{}, false
	}
	return snapshot, true
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
