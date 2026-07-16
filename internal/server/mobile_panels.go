package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/codexslash"
	"lcroom/internal/commands"
	"lcroom/internal/model"
	"lcroom/internal/procinspect"
	"lcroom/internal/projectrun"
	"lcroom/internal/service"
	"lcroom/internal/sessionclassify"
	"lcroom/internal/uisurface"
)

const (
	mobileActionMaxBodyBytes = 64 * 1024
	mobileTodoMaxRunes       = 12_000
	mobileCommandMaxRunes    = 2_000
	mobilePanelReadTimeout   = 4 * time.Second
)

type mobileTodoActionRequest struct {
	ProjectPath string `json:"project_path"`
	RequestID   string `json:"request_id"`
	Action      string `json:"action"`
	TodoID      int64  `json:"todo_id,omitempty"`
	Text        string `json:"text,omitempty"`
	Done        *bool  `json:"done,omitempty"`
}

type mobileRuntimeActionRequest struct {
	ProjectPath string `json:"project_path"`
	RequestID   string `json:"request_id"`
	Action      string `json:"action"`
	ProcessID   string `json:"process_id,omitempty"`
	Command     string `json:"command,omitempty"`
}

type mobileCommandSuggestion struct {
	Insert         string `json:"insert"`
	Display        string `json:"display"`
	Summary        string `json:"summary"`
	Source         string `json:"source"`
	Supported      bool   `json:"supported"`
	DisabledReason string `json:"disabled_reason,omitempty"`
	ClientAction   string `json:"client_action,omitempty"`
}

type mobileCommandSuggestionsSurface struct {
	Context     string                    `json:"context"`
	ProjectPath string                    `json:"project_path,omitempty"`
	Suggestions []mobileCommandSuggestion `json:"suggestions"`
}

type mobileCommandExecuteRequest struct {
	ProjectPath string `json:"project_path,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	RequestID   string `json:"request_id"`
	Command     string `json:"command"`
}

type mobileCommandExecuteResponse struct {
	RequestID    string `json:"request_id"`
	Command      string `json:"command"`
	Status       string `json:"status"`
	ClientAction string `json:"client_action,omitempty"`
}

type liveSessionAccess interface {
	LiveSessionSource
	Session(projectPath string) (codexapp.Session, bool)
}

type mobilePermissionSession interface {
	ShowPermissions() error
	SetPermissionLevel(level string) error
}

func (s *Server) handleMobileProjectSidebar(w http.ResponseWriter, r *http.Request) {
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
	snapshot, ok := s.liveSessionSnapshot(projectPath)
	if !ok || snapshot.Closed || snapshot.Phase == codexapp.SessionPhaseClosed {
		http.Error(w, "live engineer session not found", http.StatusNotFound)
		return
	}

	now := time.Now()
	surface := uisurface.BuildSessionSidebar(snapshot, s.mobileProjectItem(detail.Summary, now), now)
	readCtx, cancel := context.WithTimeout(r.Context(), mobilePanelReadTimeout)
	defer cancel()
	surface.Sections = insertPanelSectionBefore(surface.Sections, "summary", s.mobileDiffSection(readCtx, projectPath))
	runtimeSurface := s.buildMobileRuntimeSurface(readCtx, detail, now)
	surface.Sections = insertPanelSectionBefore(surface.Sections, "summary", mobileRuntimeSidebarSection(runtimeSurface))
	writeJSON(w, surface)
}

func (s *Server) handleMobileProjectTodos(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	projectPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if projectPath == "" {
		http.Error(w, "missing path query param", http.StatusBadRequest)
		return
	}
	surface, err := s.mobileTodoSurface(r.Context(), projectPath)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	writeJSON(w, surface)
}

func (s *Server) handleMobileProjectTodoAction(w http.ResponseWriter, r *http.Request) {
	if !requireMobileMutation(s, w, r) {
		return
	}
	var request mobileTodoActionRequest
	if !decodeMobileActionRequest(w, r, &request) {
		return
	}
	request.ProjectPath = strings.TrimSpace(request.ProjectPath)
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.Action = strings.ToLower(strings.TrimSpace(request.Action))
	request.Text = strings.TrimSpace(request.Text)
	if request.ProjectPath == "" || request.RequestID == "" || request.Action == "" {
		http.Error(w, "project, request ID, and action are required", http.StatusBadRequest)
		return
	}
	if len(request.RequestID) > 128 || len([]rune(request.Text)) > mobileTodoMaxRunes {
		http.Error(w, "TODO action request is too large", http.StatusRequestEntityTooLarge)
		return
	}
	clicked, scope, err := s.mobileTodoDetails(r.Context(), request.ProjectPath)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	if !s.claimMobileInputRequest(request.RequestID, time.Now()) {
		http.Error(w, "TODO action request was already handled", http.StatusConflict)
		return
	}
	releaseClaim := true
	defer func() {
		if releaseClaim {
			s.releaseMobileInputRequest(request.RequestID)
		}
	}()

	scopePath := scope.Summary.Path
	switch request.Action {
	case "add":
		if request.Text == "" {
			http.Error(w, "TODO text is required", http.StatusBadRequest)
			return
		}
		_, err = s.svc.AddTodo(r.Context(), scopePath, request.Text)
	case "update":
		if request.TodoID <= 0 || request.Text == "" {
			http.Error(w, "TODO ID and text are required", http.StatusBadRequest)
			return
		}
		if !mobileTodoBelongsToDetail(scope, request.TodoID) {
			http.Error(w, "TODO not found", http.StatusNotFound)
			return
		}
		err = s.svc.UpdateTodo(r.Context(), scopePath, request.TodoID, request.Text)
	case "toggle":
		if request.TodoID <= 0 || request.Done == nil {
			http.Error(w, "TODO ID and done state are required", http.StatusBadRequest)
			return
		}
		if !mobileTodoBelongsToDetail(scope, request.TodoID) {
			http.Error(w, "TODO not found", http.StatusNotFound)
			return
		}
		err = s.svc.ToggleTodoDone(r.Context(), scopePath, request.TodoID, *request.Done)
	case "delete":
		if request.TodoID <= 0 || !mobileTodoBelongsToDetail(scope, request.TodoID) {
			http.Error(w, "TODO not found", http.StatusNotFound)
			return
		}
		err = s.svc.DeleteTodo(r.Context(), scopePath, request.TodoID)
	case "purge_done":
		_, err = s.svc.PurgeDoneTodos(r.Context(), scopePath)
	default:
		http.Error(w, "unsupported TODO action", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	releaseClaim = false
	updated, err := s.visibleMobileProjectDetail(r.Context(), scopePath)
	if err != nil {
		updated = scope
	}
	now := time.Now()
	writeJSON(w, uisurface.BuildTodoSurface(
		s.mobileProjectItem(clicked.Summary, now),
		s.mobileProjectItem(updated.Summary, now),
		updated.Todos,
		true,
	))
}

func (s *Server) handleMobileProjectRuntime(w http.ResponseWriter, r *http.Request) {
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
	readCtx, cancel := context.WithTimeout(r.Context(), mobilePanelReadTimeout)
	defer cancel()
	writeJSON(w, s.buildMobileRuntimeSurface(readCtx, detail, time.Now()))
}

func (s *Server) handleMobileProjectRuntimeAction(w http.ResponseWriter, r *http.Request) {
	if !requireMobileMutation(s, w, r) {
		return
	}
	if s.runtimeManager == nil {
		http.Error(w, "runtime control is unavailable outside the hosted TUI", http.StatusServiceUnavailable)
		return
	}
	var request mobileRuntimeActionRequest
	if !decodeMobileActionRequest(w, r, &request) {
		return
	}
	request.ProjectPath = strings.TrimSpace(request.ProjectPath)
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.Action = strings.ToLower(strings.TrimSpace(request.Action))
	request.ProcessID = strings.TrimSpace(request.ProcessID)
	request.Command = strings.TrimSpace(request.Command)
	if request.ProjectPath == "" || request.RequestID == "" || request.Action == "" {
		http.Error(w, "project, request ID, and action are required", http.StatusBadRequest)
		return
	}
	if len(request.RequestID) > 128 || len([]rune(request.Command)) > mobileCommandMaxRunes {
		http.Error(w, "runtime action request is too large", http.StatusRequestEntityTooLarge)
		return
	}
	detail, err := s.visibleMobileProjectDetail(r.Context(), request.ProjectPath)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	if !s.claimMobileInputRequest(request.RequestID, time.Now()) {
		http.Error(w, "runtime action request was already handled", http.StatusConflict)
		return
	}
	releaseClaim := true
	defer func() {
		if releaseClaim {
			s.releaseMobileInputRequest(request.RequestID)
		}
	}()

	projectPath := detail.Summary.Path
	switch request.Action {
	case "start", "restart":
		command := request.Command
		if command == "" {
			command = strings.TrimSpace(detail.Summary.RunCommand)
		}
		if command == "" {
			http.Error(w, "this project has no run command", http.StatusBadRequest)
			return
		}
		_, err = s.runtimeManager.StartManaged(projectrun.StartRequest{
			ProjectPath:     projectPath,
			Command:         command,
			CWD:             projectPath,
			ReuseMatching:   request.Action == "start",
			ReplaceExisting: request.Action == "restart",
		})
	case "stop":
		if request.ProcessID != "" {
			err = s.runtimeManager.StopProcess(projectPath, request.ProcessID)
		} else {
			err = s.runtimeManager.StopProject(projectPath)
		}
	default:
		http.Error(w, "unsupported runtime action", http.StatusBadRequest)
		return
	}
	if err != nil && !errors.Is(err, projectrun.ErrNotRunning) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	releaseClaim = false
	readCtx, cancel := context.WithTimeout(r.Context(), mobilePanelReadTimeout)
	defer cancel()
	writeJSON(w, s.buildMobileRuntimeSurface(readCtx, detail, time.Now()))
}

func (s *Server) handleMobileCommandSuggestions(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		query = "/"
	}
	if len([]rune(query)) > mobileCommandMaxRunes {
		http.Error(w, "command query is too large", http.StatusRequestEntityTooLarge)
		return
	}
	projectPath := strings.TrimSpace(r.URL.Query().Get("path"))
	contextName := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("context")))
	if contextName != "session" {
		contextName = "dashboard"
	}
	if projectPath != "" {
		if _, err := s.visibleMobileProjectDetail(r.Context(), projectPath); err != nil {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
	}

	suggestions := make([]mobileCommandSuggestion, 0, 24)
	seen := map[string]struct{}{}
	appendSuggestion := func(suggestion mobileCommandSuggestion) {
		key := strings.ToLower(strings.TrimSpace(suggestion.Insert))
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		suggestions = append(suggestions, suggestion)
	}
	for _, suggestion := range mobileNavigationSuggestions(query, projectPath != "") {
		appendSuggestion(suggestion)
	}
	if contextName == "session" {
		for _, suggestion := range codexslash.Suggestions(query) {
			supported, reason := mobileCodexCommandSupport(suggestion.Insert)
			appendSuggestion(mobileCommandSuggestion{
				Insert:         suggestion.Insert,
				Display:        suggestion.Display,
				Summary:        suggestion.Summary,
				Source:         "session",
				Supported:      supported,
				DisabledReason: reason,
			})
		}
	} else {
		for _, suggestion := range commands.Suggestions(query) {
			supported, clientAction, reason := mobileHostCommandSupport(suggestion.Insert, projectPath != "")
			appendSuggestion(mobileCommandSuggestion{
				Insert:         suggestion.Insert,
				Display:        suggestion.Display,
				Summary:        suggestion.Summary,
				Source:         "control room",
				Supported:      supported,
				DisabledReason: reason,
				ClientAction:   clientAction,
			})
		}
	}
	writeJSON(w, mobileCommandSuggestionsSurface{Context: contextName, ProjectPath: projectPath, Suggestions: suggestions})
}

func (s *Server) handleMobileCommandExecute(w http.ResponseWriter, r *http.Request) {
	if !requireMobileMutation(s, w, r) {
		return
	}
	var request mobileCommandExecuteRequest
	if !decodeMobileActionRequest(w, r, &request) {
		return
	}
	request.ProjectPath = strings.TrimSpace(request.ProjectPath)
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.Command = strings.TrimSpace(request.Command)
	if request.RequestID == "" || request.Command == "" || !strings.HasPrefix(request.Command, "/") {
		http.Error(w, "request ID and slash command are required", http.StatusBadRequest)
		return
	}
	if len(request.RequestID) > 128 || len([]rune(request.Command)) > mobileCommandMaxRunes {
		http.Error(w, "command request is too large", http.StatusRequestEntityTooLarge)
		return
	}
	if request.ProjectPath != "" {
		if _, err := s.visibleMobileProjectDetail(r.Context(), request.ProjectPath); err != nil {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
	}
	if !s.claimMobileInputRequest(request.RequestID, time.Now()) {
		http.Error(w, "command request was already handled", http.StatusConflict)
		return
	}
	releaseClaim := true
	defer func() {
		if releaseClaim {
			s.releaseMobileInputRequest(request.RequestID)
		}
	}()

	status, canonical, clientAction, err := s.executeMobileCommand(r.Context(), request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	releaseClaim = false
	writeJSON(w, mobileCommandExecuteResponse{
		RequestID:    request.RequestID,
		Command:      canonical,
		Status:       status,
		ClientAction: clientAction,
	})
}

func (s *Server) mobileTodoSurface(ctx context.Context, projectPath string) (uisurface.TodoSurface, error) {
	clicked, scope, err := s.mobileTodoDetails(ctx, projectPath)
	if err != nil {
		return uisurface.TodoSurface{}, err
	}
	now := time.Now()
	return uisurface.BuildTodoSurface(
		s.mobileProjectItem(clicked.Summary, now),
		s.mobileProjectItem(scope.Summary, now),
		scope.Todos,
		s.svc.Config().MobileInputEnabled,
	), nil
}

func (s *Server) mobileTodoDetails(ctx context.Context, projectPath string) (model.ProjectDetail, model.ProjectDetail, error) {
	clicked, err := s.visibleMobileProjectDetail(ctx, projectPath)
	if err != nil {
		return model.ProjectDetail{}, model.ProjectDetail{}, err
	}
	scope := clicked
	rootPath := strings.TrimSpace(clicked.Summary.WorktreeRootPath)
	if rootPath != "" && !sameCleanPath(rootPath, clicked.Summary.Path) {
		if root, rootErr := s.visibleMobileProjectDetail(ctx, rootPath); rootErr == nil {
			scope = root
		}
	}
	return clicked, scope, nil
}

func mobileTodoBelongsToDetail(detail model.ProjectDetail, todoID int64) bool {
	for _, todo := range detail.Todos {
		if todo.ID == todoID {
			return true
		}
	}
	return false
}

func (s *Server) mobileProjectItem(project model.ProjectSummary, now time.Time) uisurface.ProjectItem {
	cfg := s.svc.Config()
	return uisurface.BuildProjectItem(project, uisurface.BuildOptions{
		Now:            now,
		StuckThreshold: sessionclassify.EffectiveAssessmentStallThreshold(cfg.ActiveThreshold, cfg.StuckThreshold),
	})
}

func (s *Server) buildMobileRuntimeSurface(ctx context.Context, detail model.ProjectDetail, now time.Time) uisurface.RuntimeSurface {
	managed := []projectrun.Snapshot{}
	if s.runtimeManager != nil {
		managed = s.runtimeManager.SnapshotsForProject(detail.Summary.Path)
	}
	report := mobileRuntimeProcessReport(ctx, detail.Summary.Path, managed)
	return uisurface.BuildRuntimeSurface(
		s.mobileProjectItem(detail.Summary, now),
		detail.Summary.RunCommand,
		managed,
		report,
		s.svc.Config().MobileInputEnabled && s.runtimeManager != nil,
		now,
	)
}

func mobileRuntimeProcessReport(ctx context.Context, projectPath string, managed []projectrun.Snapshot) procinspect.ProjectReport {
	managedPIDs := map[int]struct{}{}
	managedPGIDs := map[int]struct{}{}
	managedPIDProjects := map[int]string{}
	managedPGIDProjects := map[int]string{}
	for _, snapshot := range managed {
		if snapshot.PID > 0 {
			managedPIDs[snapshot.PID] = struct{}{}
			managedPIDProjects[snapshot.PID] = projectPath
		}
		if snapshot.PGID > 0 {
			managedPGIDs[snapshot.PGID] = struct{}{}
			managedPGIDProjects[snapshot.PGID] = projectPath
		}
	}
	reports, err := procinspect.ScanProjects(ctx, procinspect.ScanOptions{
		ProjectPaths:        []string{projectPath},
		ManagedPIDs:         managedPIDs,
		ManagedPGIDs:        managedPGIDs,
		ManagedPIDProjects:  managedPIDProjects,
		ManagedPGIDProjects: managedPGIDProjects,
		OwnPID:              os.Getpid(),
	})
	if err != nil || len(reports) == 0 {
		return procinspect.ProjectReport{ProjectPath: projectPath, ScannedAt: time.Now()}
	}
	return reports[0]
}

func (s *Server) mobileDiffSection(ctx context.Context, projectPath string) uisurface.PanelSection {
	section := uisurface.PanelSection{ID: "diff", Title: "Diff summary"}
	preview, err := s.svc.PrepareDiff(ctx, projectPath)
	if err != nil {
		var clean service.NoDiffChangesError
		if errors.As(err, &clean) {
			section.Summary = "Clean"
			section.Blocks = []uisurface.DetailBlock{{Kind: uisurface.DetailBlockText, Text: "No changed files.", Tone: uisurface.TonePositive}}
			if branch := strings.TrimSpace(clean.Branch); branch != "" {
				section.Blocks = append(section.Blocks, uisurface.DetailBlock{Kind: uisurface.DetailBlockField, Label: "Branch", Text: branch, Tone: uisurface.ToneValue})
			}
			return section
		}
		var noGit service.NoGitRepositoryError
		if errors.As(err, &noGit) {
			section.Summary = "Not a git repository"
			section.Blocks = []uisurface.DetailBlock{{Kind: uisurface.DetailBlockText, Text: "No repository diff is available.", Tone: uisurface.ToneMuted}}
			return section
		}
		section.Summary = "Unavailable"
		section.Blocks = []uisurface.DetailBlock{{Kind: uisurface.DetailBlockWrappedField, Label: "Read error", Text: err.Error(), Tone: uisurface.ToneWarning}}
		return section
	}
	section.Summary = strings.TrimSpace(preview.Summary)
	if section.Summary == "" {
		section.Summary = fmt.Sprintf("%d changed file(s)", len(preview.Files))
	}
	section.Blocks = append(section.Blocks, uisurface.DetailBlock{Kind: uisurface.DetailBlockField, Label: "Branch", Text: preview.Branch, Tone: uisurface.ToneValue})
	for _, file := range preview.Files {
		text := strings.TrimSpace(file.Path)
		if summary := strings.TrimSpace(file.Summary); summary != "" {
			text += " — " + summary
		}
		section.Blocks = append(section.Blocks, uisurface.DetailBlock{Kind: uisurface.DetailBlockBullet, Text: text, Tone: uisurface.ToneValue})
	}
	return section
}

func mobileRuntimeSidebarSection(surface uisurface.RuntimeSurface) uisurface.PanelSection {
	running := 0
	blocks := make([]uisurface.DetailBlock, 0, len(surface.Processes)+2)
	for _, process := range surface.Processes {
		if process.Running {
			running++
		}
		text := process.Name + " · " + process.Status.Label
		if process.PID > 0 {
			text += fmt.Sprintf(" · PID %d", process.PID)
		}
		if len(process.Ports) > 0 {
			text += fmt.Sprintf(" · ports %v", process.Ports)
		}
		blocks = append(blocks, uisurface.DetailBlock{Kind: uisurface.DetailBlockBullet, Text: text, Tone: process.Status.Tone})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, uisurface.DetailBlock{Kind: uisurface.DetailBlockText, Text: "No project-local processes detected.", Tone: uisurface.ToneMuted})
	}
	return uisurface.PanelSection{
		ID:      "processes",
		Title:   "Active processes",
		Summary: fmt.Sprintf("%d running", running),
		Blocks:  blocks,
	}
}

func insertPanelSectionBefore(sections []uisurface.PanelSection, beforeID string, section uisurface.PanelSection) []uisurface.PanelSection {
	if strings.TrimSpace(section.ID) == "" {
		return sections
	}
	index := len(sections)
	for i := range sections {
		if sections[i].ID == beforeID {
			index = i
			break
		}
	}
	sections = append(sections, uisurface.PanelSection{})
	copy(sections[index+1:], sections[index:])
	sections[index] = section
	return sections
}

func requireMobileMutation(s *Server, w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if !sameOrigin(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return false
	}
	if s == nil || s.svc == nil || !s.svc.Config().MobileInputEnabled {
		http.Error(w, "phone control is disabled in Mobile settings", http.StatusForbidden)
		return false
	}
	return true
}

func decodeMobileActionRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, mobileActionMaxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || ensureJSONEOF(decoder) != nil {
		http.Error(w, "invalid mobile action request", http.StatusBadRequest)
		return false
	}
	return true
}

func mobileNavigationSuggestions(query string, hasProject bool) []mobileCommandSuggestion {
	all := []mobileCommandSuggestion{
		{Insert: "/session", Display: "/session", Summary: "Return to the current engineer transcript", Source: "mobile", Supported: hasProject, DisabledReason: mobileProjectRequiredReason(hasProject), ClientAction: "session"},
		{Insert: "/sidebar", Display: "/sidebar", Summary: "Open live engineer telemetry", Source: "mobile", Supported: hasProject, DisabledReason: mobileProjectRequiredReason(hasProject), ClientAction: "sidebar"},
		{Insert: "/details", Display: "/details", Summary: "Open the shared project details panel", Source: "mobile", Supported: hasProject, DisabledReason: mobileProjectRequiredReason(hasProject), ClientAction: "details"},
		{Insert: "/todo", Display: "/todo", Summary: "Open the repository-scoped TODO panel", Source: "mobile", Supported: hasProject, DisabledReason: mobileProjectRequiredReason(hasProject), ClientAction: "todo"},
		{Insert: "/runtime", Display: "/runtime", Summary: "Open runtime processes and controls", Source: "mobile", Supported: hasProject, DisabledReason: mobileProjectRequiredReason(hasProject), ClientAction: "runtime"},
	}
	prefix := strings.ToLower(strings.TrimSpace(query))
	if prefix == "" || prefix == "/" {
		return all
	}
	result := make([]mobileCommandSuggestion, 0, len(all))
	for _, suggestion := range all {
		if strings.HasPrefix(strings.ToLower(suggestion.Insert), prefix) {
			result = append(result, suggestion)
		}
	}
	return result
}

func mobileProjectRequiredReason(hasProject bool) string {
	if hasProject {
		return ""
	}
	return "Select a project first"
}

func mobileCodexCommandSupport(raw string) (bool, string) {
	invocation, err := codexslash.Parse(raw)
	if err != nil {
		return false, err.Error()
	}
	switch invocation.Kind {
	case codexslash.KindStatus, codexslash.KindShowStatus, codexslash.KindCompact, codexslash.KindReview, codexslash.KindPermissions, codexslash.KindGoal:
		return true, ""
	default:
		return false, "This command currently requires the desktop TUI"
	}
}

func mobileHostCommandSupport(raw string, hasProject bool) (bool, string, string) {
	invocation, err := commands.Parse(raw)
	if err != nil {
		return false, "", err.Error()
	}
	projectRequired := map[commands.Kind]bool{
		commands.KindPin: true, commands.KindRead: true, commands.KindUnread: true,
		commands.KindSnooze: true, commands.KindClearSnooze: true,
		commands.KindRun: true, commands.KindRestart: true, commands.KindStop: true,
		commands.KindRuntime: true, commands.KindTodo: true,
	}
	if projectRequired[invocation.Kind] && !hasProject {
		return false, "", "Select a project first"
	}
	switch invocation.Kind {
	case commands.KindRefresh, commands.KindPin, commands.KindRead, commands.KindUnread, commands.KindSnooze, commands.KindClearSnooze, commands.KindRun, commands.KindRestart, commands.KindStop:
		return true, "", ""
	case commands.KindTodo:
		return true, "todo", ""
	case commands.KindRuntime:
		return true, "runtime", ""
	default:
		return false, "", "This command currently requires the desktop TUI"
	}
}

func (s *Server) executeMobileCommand(ctx context.Context, request mobileCommandExecuteRequest) (string, string, string, error) {
	if invocation, err := codexslash.Parse(request.Command); err == nil && request.ProjectPath != "" && request.SessionID != "" {
		return s.executeMobileCodexCommand(request, invocation)
	}
	invocation, err := commands.Parse(request.Command)
	if err != nil {
		return "", "", "", err
	}
	canonical := invocation.Canonical
	if canonical == "" {
		canonical = request.Command
	}
	if request.ProjectPath == "" && invocation.Kind != commands.KindRefresh {
		return "", canonical, "", errors.New("select a project before running this command")
	}
	switch invocation.Kind {
	case commands.KindRefresh:
		if request.ProjectPath != "" {
			err = s.svc.RefreshProjectStatus(ctx, request.ProjectPath)
		} else {
			_, err = s.svc.ScanOnce(ctx)
		}
		return "Projects refreshed", canonical, "", err
	case commands.KindPin:
		err = s.svc.TogglePin(ctx, request.ProjectPath)
		return "Pin toggled", canonical, "", err
	case commands.KindRead:
		err = s.svc.MarkProjectSessionSeen(ctx, request.ProjectPath, time.Now())
		return "Project marked read", canonical, "", err
	case commands.KindUnread:
		err = s.svc.MarkProjectSessionUnread(ctx, request.ProjectPath)
		return "Project marked unread", canonical, "", err
	case commands.KindSnooze:
		if invocation.Duration <= 0 {
			err = s.svc.ClearSnooze(ctx, request.ProjectPath)
			return "Snooze cleared", canonical, "", err
		}
		err = s.svc.Snooze(ctx, request.ProjectPath, invocation.Duration)
		return "Project snoozed", canonical, "", err
	case commands.KindClearSnooze:
		err = s.svc.ClearSnooze(ctx, request.ProjectPath)
		return "Snooze cleared", canonical, "", err
	case commands.KindTodo:
		return "TODO panel opened", canonical, "todo", nil
	case commands.KindRuntime:
		return "Runtime panel opened", canonical, "runtime", nil
	case commands.KindRun, commands.KindRestart:
		if s.runtimeManager == nil {
			return "", canonical, "", errors.New("runtime control is unavailable outside the hosted TUI")
		}
		detail, detailErr := s.visibleMobileProjectDetail(ctx, request.ProjectPath)
		if detailErr != nil {
			return "", canonical, "", detailErr
		}
		command := strings.TrimSpace(invocation.Command)
		if command == "" {
			command = strings.TrimSpace(detail.Summary.RunCommand)
		}
		if command == "" {
			return "", canonical, "", errors.New("this project has no run command")
		}
		result, runErr := s.runtimeManager.StartManaged(projectrun.StartRequest{
			ProjectPath:     request.ProjectPath,
			Command:         command,
			CWD:             request.ProjectPath,
			ReuseMatching:   invocation.Kind == commands.KindRun,
			ReplaceExisting: invocation.Kind == commands.KindRestart,
		})
		return "Runtime " + string(result.Disposition), canonical, "runtime", runErr
	case commands.KindStop:
		if s.runtimeManager == nil {
			return "", canonical, "", errors.New("runtime control is unavailable outside the hosted TUI")
		}
		err = s.runtimeManager.StopProject(request.ProjectPath)
		if errors.Is(err, projectrun.ErrNotRunning) {
			err = nil
		}
		return "Runtime stopped", canonical, "runtime", err
	default:
		return "", canonical, "", errors.New("this command currently requires the desktop TUI")
	}
}

func (s *Server) executeMobileCodexCommand(request mobileCommandExecuteRequest, invocation codexslash.Invocation) (string, string, string, error) {
	canonical := invocation.Canonical
	if canonical == "" {
		canonical = request.Command
	}
	snapshot, ok := s.liveSessionSnapshot(request.ProjectPath)
	if !ok {
		return "", canonical, "", errors.New("live engineer session not found")
	}
	item := uisurface.BuildLiveEngineerSession(snapshot, time.Now())
	if item.ID != request.SessionID {
		return "", canonical, "", errors.New("engineer session changed; reopen the current channel")
	}
	access, ok := s.liveSessions.(liveSessionAccess)
	if !ok {
		return "", canonical, "", errors.New("live engineer session commands are unavailable")
	}
	session, ok := access.Session(request.ProjectPath)
	if !ok || session == nil {
		return "", canonical, "", errors.New("live engineer session not found")
	}
	label := snapshot.Provider.Label()
	switch invocation.Kind {
	case codexslash.KindStatus, codexslash.KindShowStatus:
		return "Status added to the transcript", canonical, "session", session.ShowStatus()
	case codexslash.KindCompact:
		return "Conversation compaction started", canonical, "session", session.Compact()
	case codexslash.KindReview:
		return "Review started", canonical, "session", session.Review()
	case codexslash.KindGoal:
		var err error
		switch invocation.GoalAction {
		case codexslash.GoalActionShow:
			err = session.ShowGoal()
		case codexslash.GoalActionSet:
			err = session.SetGoal(invocation.GoalObjective, invocation.GoalTokenBudget)
		case codexslash.GoalActionPause:
			err = session.PauseGoal()
		case codexslash.GoalActionResume:
			err = session.ResumeGoal()
		case codexslash.GoalActionClear:
			err = session.ClearGoal()
		default:
			err = errors.New("unsupported goal action")
		}
		return "Goal command sent", canonical, "session", err
	case codexslash.KindPermissions:
		permissionSession, ok := session.(mobilePermissionSession)
		if !ok {
			return "", canonical, "", fmt.Errorf("%s does not support in-session permission changes", label)
		}
		if invocation.PermissionLevel == "" {
			return "Permissions added to the transcript", canonical, "session", permissionSession.ShowPermissions()
		}
		return "Permission level set to " + invocation.PermissionLevel, canonical, "session", permissionSession.SetPermissionLevel(invocation.PermissionLevel)
	default:
		return "", canonical, "", errors.New("this command currently requires the desktop TUI")
	}
}
