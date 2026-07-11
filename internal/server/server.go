package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/sessionclassify"
	"lcroom/internal/uisurface"

	"github.com/gorilla/websocket"
)

type Server struct {
	svc          *service.Service
	liveSessions LiveSessionSource
	mobileAuth   *MobileAuth
}

type RunningServer struct {
	httpServer *http.Server
	listener   net.Listener
	address    string
	done       chan struct{}

	mu  sync.RWMutex
	err error
}

const DefaultListenAddress = "127.0.0.1:7777"

//go:embed web
var mobileWebFiles embed.FS

func New(svc *service.Service) *Server {
	return &Server{svc: svc}
}

func (s *Server) WithMobileAuth(auth *MobileAuth) *Server {
	if s != nil {
		s.mobileAuth = auth
	}
	return s
}

func ValidateListenAddress(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return fmt.Errorf("listen address is required")
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("listen address must use host:port form: %w", err)
	}
	return nil
}

func ListenAddressIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	host = strings.TrimSpace(host)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) Start(ctx context.Context, addr string) (*RunningServer, error) {
	if err := ValidateListenAddress(addr); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	listener, err := net.Listen("tcp", strings.TrimSpace(addr))
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}

	httpServer := &http.Server{
		Addr:              listener.Addr().String(),
		Handler:           s.Handler(ctx),
		ReadHeaderTimeout: 5 * time.Second,
	}
	running := &RunningServer{
		httpServer: httpServer,
		listener:   listener,
		address:    listener.Addr().String(),
		done:       make(chan struct{}),
	}

	go func() {
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		running.mu.Lock()
		running.err = err
		running.mu.Unlock()
		close(running.done)
	}()

	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := running.Shutdown(shutdownCtx); err != nil {
				_ = running.Close()
			}
		case <-running.done:
		}
	}()

	return running, nil
}

func (s *Server) Run(ctx context.Context, addr string) error {
	running, err := s.Start(ctx, addr)
	if err != nil {
		return err
	}
	return running.Wait()
}

func (s *RunningServer) Address() string {
	if s == nil {
		return ""
	}
	return s.address
}

func (s *RunningServer) URL() string {
	if s == nil || strings.TrimSpace(s.address) == "" {
		return ""
	}
	return "http://" + s.address
}

func (s *RunningServer) Done() <-chan struct{} {
	if s == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return s.done
}

func (s *RunningServer) Wait() error {
	if s == nil {
		return nil
	}
	<-s.done
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.err
}

func (s *RunningServer) Shutdown(ctx context.Context) error {
	if s == nil || s.httpServer == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.httpServer.Shutdown(ctx)
}

func (s *RunningServer) Close() error {
	if s == nil || s.httpServer == nil {
		return nil
	}
	return s.httpServer.Close()
}

func (s *Server) Handler(ctx context.Context) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/mobile/auth/status", s.handleMobileAuthStatus)
	mux.HandleFunc("/api/mobile/auth/pair", s.handleMobileAuthPair)
	mux.HandleFunc("/api/mobile/auth/logout", s.handleMobileAuthLogout)
	mux.Handle("/projects", s.protectMobile(http.HandlerFunc(s.handleProjects)))
	mux.Handle("/projects/detail", s.protectMobile(http.HandlerFunc(s.handleProjectDetail)))
	mux.Handle("/api/mobile/dashboard", s.protectMobile(http.HandlerFunc(s.handleMobileDashboard)))
	mux.Handle("/api/mobile/projects/detail", s.protectMobile(http.HandlerFunc(s.handleMobileProjectDetail)))
	mux.Handle("/api/mobile/projects/sessions", s.protectMobile(http.HandlerFunc(s.handleMobileProjectSessions)))
	mux.Handle("/api/mobile/sessions/detail", s.protectMobile(http.HandlerFunc(s.handleMobileSessionDetail)))
	mux.HandleFunc("/assets/operator-station.png", handleOperatorStationAsset)
	mux.Handle("/events/ws", s.protectMobile(s.handleEventsWS(ctx)))

	webRoot, err := fs.Sub(mobileWebFiles, "web")
	if err != nil {
		panic(fmt.Sprintf("mobile web assets: %v", err))
	}
	webHandler := http.FileServer(http.FS(webRoot))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		webHandler.ServeHTTP(w, r)
	}))
	return securityHeaders(mux)
}

func (s *Server) protectMobile(next http.Handler) http.Handler {
	if s == nil || s.mobileAuth == nil {
		return next
	}
	return s.mobileAuth.Protect(next)
}

func (s *Server) handleMobileAuthStatus(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	writeJSON(w, s.mobileAuth.status(r))
}

func (s *Server) handleMobileAuthPair(w http.ResponseWriter, r *http.Request) {
	s.mobileAuth.pair(w, r)
}

func (s *Server) handleMobileAuthLogout(w http.ResponseWriter, r *http.Request) {
	s.mobileAuth.logout(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
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
	cfg := s.svc.Config()
	projects = filterProjectSummariesByName(projects, cfg.ExcludeProjectPatterns)
	if cfg.PrivacyMode {
		projects = filterPrivateProjectSummaries(projects)
	}
	writeJSON(w, projects)
}

func (s *Server) handleProjectDetail(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
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
	cfg := s.svc.Config()
	if projectSummaryHidden(detail.Summary, cfg.ExcludeProjectPatterns) || (cfg.PrivacyMode && detail.Summary.CategoryPrivate) {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	writeJSON(w, detail)
}

func (s *Server) handleMobileDashboard(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	projects, err := s.svc.Store().ListProjects(r.Context(), false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	categories, err := s.svc.Store().ListProjectCategories(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cfg := s.svc.Config()
	projects = filterProjectSummariesByName(projects, cfg.ExcludeProjectPatterns)
	writeJSON(w, uisurface.BuildDashboard(projects, categories, uisurface.BuildOptions{
		Now:            time.Now(),
		StuckThreshold: sessionclassify.EffectiveAssessmentStallThreshold(cfg.ActiveThreshold, cfg.StuckThreshold),
		HidePrivate:    cfg.PrivacyMode,
	}))
}

func (s *Server) handleMobileProjectDetail(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		http.Error(w, "missing path query param", http.StatusBadRequest)
		return
	}
	detail, err := s.svc.Store().GetProjectDetail(r.Context(), path, 0)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	cfg := s.svc.Config()
	if projectSummaryHidden(detail.Summary, cfg.ExcludeProjectPatterns) || (cfg.PrivacyMode && detail.Summary.CategoryPrivate) {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	writeJSON(w, uisurface.BuildProjectDetail(detail, uisurface.BuildOptions{
		Now:            time.Now(),
		StuckThreshold: sessionclassify.EffectiveAssessmentStallThreshold(cfg.ActiveThreshold, cfg.StuckThreshold),
	}))
}

func (s *Server) handleEventsWS(ctx context.Context) http.HandlerFunc {
	upgrader := websocket.Upgrader{
		CheckOrigin: sameOrigin,
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
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		http.Error(w, fmt.Sprintf("encode response: %v", err), http.StatusInternalServerError)
	}
}

func requireGET(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", http.MethodGet)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self' ws: wss:; img-src 'self' data:; style-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
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

func filterPrivateProjectSummaries(projects []model.ProjectSummary) []model.ProjectSummary {
	filtered := make([]model.ProjectSummary, 0, len(projects))
	for _, project := range projects {
		if !project.CategoryPrivate {
			filtered = append(filtered, project)
		}
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
