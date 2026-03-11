package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"

	"github.com/gorilla/websocket"
)

type Server struct {
	svc *service.Service
}

func New(svc *service.Service) *Server {
	return &Server{svc: svc}
}

func (s *Server) Run(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/projects", s.handleProjects)
	mux.HandleFunc("/projects/detail", s.handleProjectDetail)
	mux.HandleFunc("/events/ws", s.handleEventsWS(ctx))

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == nil || err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	includeHistorical := false
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("historical"))) {
	case "1", "true", "yes", "y":
		includeHistorical = true
	}

	projects, err := s.svc.Store().ListProjects(r.Context(), includeHistorical)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, filterProjectSummariesByName(projects, s.svc.Config().ExcludeProjectPatterns))
}

func (s *Server) handleProjectDetail(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path query param", http.StatusBadRequest)
		return
	}
	detail, err := s.svc.Store().GetProjectDetail(r.Context(), path, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if projectSummaryHidden(detail.Summary, s.svc.Config().ExcludeProjectPatterns) {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	writeJSON(w, detail)
}

func (s *Server) handleEventsWS(ctx context.Context) http.HandlerFunc {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		evtCh, unsub := s.svc.Bus().Subscribe(256)
		defer unsub()

		for {
			select {
			case <-ctx.Done():
				_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "server shutting down"))
				return
			case evt, ok := <-evtCh:
				if !ok {
					return
				}
				if err := conn.WriteJSON(evt); err != nil {
					return
				}
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		http.Error(w, fmt.Sprintf("encode response: %v", err), http.StatusInternalServerError)
	}
}

func filterProjectSummariesByName(projects []model.ProjectSummary, excludeProjectPatterns []string) []model.ProjectSummary {
	if len(projects) == 0 || len(excludeProjectPatterns) == 0 {
		return projects
	}
	filtered := make([]model.ProjectSummary, 0, len(projects))
	for _, project := range projects {
		if projectSummaryHidden(project, excludeProjectPatterns) {
			continue
		}
		filtered = append(filtered, project)
	}
	return filtered
}

func projectSummaryHidden(project model.ProjectSummary, excludeProjectPatterns []string) bool {
	if config.ProjectNameExcluded(project.Name, excludeProjectPatterns) {
		return true
	}
	base := filepath.Base(filepath.Clean(project.Path))
	if strings.EqualFold(strings.TrimSpace(base), strings.TrimSpace(project.Name)) {
		return false
	}
	return config.ProjectNameExcluded(base, excludeProjectPatterns)
}

var _ = events.Event{}
