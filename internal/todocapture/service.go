package todocapture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
)

const ExternalEventType = "external_todo_capture"
const RuntimeModeSettingKey = "engineer_todo_capture_mode"

const (
	DispositionCreated           = "created"
	DispositionExistingDuplicate = "existing_duplicate"
	DispositionTodosChanged      = "todos_changed"
)

type Action string

const (
	ActionList Action = "list"
	ActionAdd  Action = "add"
)

type Request struct {
	Action Action
	Origin Origin
	Add    AddRequest
}

type Response struct {
	List *ListResult `json:"list,omitempty"`
	Add  *AddResult  `json:"add,omitempty"`
}

type Handler interface {
	HandleTodoCapture(context.Context, Request) (Response, error)
}

type Origin struct {
	ProjectPath string
	Provider    string
	SessionKey  string
	// PublishedInProcess is set only by the owning LCR service after it has
	// committed to publish the mutation directly on its event bus.
	PublishedInProcess bool
}

type ProjectScope struct {
	RequestedPath string `json:"requested_path"`
	ProjectPath   string `json:"project_path"`
	ProjectName   string `json:"project_name"`
	FromWorktree  bool   `json:"from_worktree"`
}

type Todo struct {
	ID        int64  `json:"id"`
	Text      string `json:"text"`
	Position  int    `json:"position"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type ListResult struct {
	Scope          ProjectScope `json:"scope"`
	CaptureMode    CaptureMode  `json:"capture_mode"`
	OpenTodos      []Todo       `json:"open_todos"`
	ReviewRevision string       `json:"review_revision"`
}

type AddRequest struct {
	Text           string      `json:"text"`
	CaptureKind    CaptureKind `json:"capture_kind"`
	ReviewRevision string      `json:"review_revision"`
}

type AddResult struct {
	Scope           ProjectScope `json:"scope"`
	Disposition     string       `json:"disposition"`
	Todo            *Todo        `json:"todo,omitempty"`
	CurrentTodos    []Todo       `json:"current_open_todos,omitempty"`
	CurrentRevision string       `json:"current_review_revision"`
	Warnings        []string     `json:"warnings,omitempty"`
}

type ExternalEvent struct {
	Action             string      `json:"action"`
	Provider           string      `json:"provider"`
	SessionKey         string      `json:"session_key,omitempty"`
	RequestedPath      string      `json:"requested_path"`
	ProjectPath        string      `json:"project_path"`
	CaptureKind        CaptureKind `json:"capture_kind"`
	Disposition        string      `json:"disposition"`
	TodoID             int64       `json:"todo_id,omitempty"`
	PublishedInProcess bool        `json:"published_in_process,omitempty"`
}

type Service struct {
	store              *store.Store
	mode               CaptureMode
	requireRuntimeMode bool
}

func NewService(st *store.Store, mode CaptureMode) *Service {
	return &Service{store: st, mode: NormalizeCaptureMode(mode)}
}

// NewExternalService requires a host-persisted runtime policy on every call.
// This prevents a manually launched or stale external MCP from treating its
// launch-time flag as an enduring authorization grant.
func NewExternalService(st *store.Store, mode CaptureMode) *Service {
	return &Service{store: st, mode: NormalizeCaptureMode(mode), requireRuntimeMode: true}
}

func (s *Service) Mode() CaptureMode {
	if s == nil {
		return ModeOff
	}
	return s.mode
}

func (s *Service) HandleTodoCapture(ctx context.Context, req Request) (Response, error) {
	switch req.Action {
	case ActionList:
		result, err := s.List(ctx, req.Origin)
		if err != nil {
			return Response{}, err
		}
		return Response{List: &result}, nil
	case ActionAdd:
		result, err := s.Add(ctx, req.Origin, req.Add)
		if err != nil {
			return Response{}, err
		}
		return Response{Add: &result}, nil
	default:
		return Response{}, fmt.Errorf("unknown TODO capture action %q", req.Action)
	}
}

func (s *Service) List(ctx context.Context, origin Origin) (ListResult, error) {
	if s == nil || s.store == nil {
		return ListResult{}, fmt.Errorf("TODO capture service is unavailable")
	}
	mode, err := s.effectiveMode(ctx)
	if err != nil {
		return ListResult{}, err
	}
	if !mode.Enabled() {
		return ListResult{}, fmt.Errorf("project TODO capture is disabled")
	}
	scope, err := s.ResolveProject(ctx, origin.ProjectPath)
	if err != nil {
		return ListResult{}, err
	}
	todos, revision, err := s.store.ListOpenTodosForReview(ctx, scope.ProjectPath)
	if err != nil {
		return ListResult{}, fmt.Errorf("list project TODOs: %w", err)
	}
	return ListResult{
		Scope:          scope,
		CaptureMode:    mode,
		OpenTodos:      todoViews(todos),
		ReviewRevision: revision,
	}, nil
}

func (s *Service) Add(ctx context.Context, origin Origin, req AddRequest) (AddResult, error) {
	if s == nil || s.store == nil {
		return AddResult{}, fmt.Errorf("TODO capture service is unavailable")
	}
	mode, err := s.effectiveMode(ctx)
	if err != nil {
		return AddResult{}, err
	}
	if !mode.Enabled() {
		return AddResult{}, fmt.Errorf("project TODO capture is disabled")
	}
	kind, err := ParseCaptureKind(string(req.CaptureKind))
	if err != nil {
		return AddResult{}, err
	}
	if !mode.Allows(kind) {
		return AddResult{}, fmt.Errorf("capture kind %q is not allowed in %s mode", kind, mode)
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return AddResult{}, fmt.Errorf("todo text is required")
	}
	scope, err := s.ResolveProject(ctx, origin.ProjectPath)
	if err != nil {
		return AddResult{}, err
	}
	var reviewed store.ReviewedTodoResult
	if s.requireRuntimeMode {
		allowedModes := []string{string(ModeExplicit), string(ModeExplicitAndClearDeferrals)}
		if kind == CaptureClearDeferral {
			allowedModes = []string{string(ModeExplicitAndClearDeferrals)}
		}
		reviewed, err = s.store.AddTodoReviewedWithRuntimePolicy(ctx, scope.ProjectPath, text, strings.TrimSpace(req.ReviewRevision), RuntimeModeSettingKey, allowedModes)
	} else {
		reviewed, err = s.store.AddTodoReviewed(ctx, scope.ProjectPath, text, strings.TrimSpace(req.ReviewRevision))
	}
	if err != nil {
		return AddResult{}, fmt.Errorf("add reviewed project TODO: %w", err)
	}
	result := AddResult{
		Scope:           scope,
		Disposition:     externalDisposition(reviewed.Disposition),
		CurrentTodos:    todoViews(reviewed.CurrentTodos),
		CurrentRevision: reviewed.CurrentRevision,
	}
	if reviewed.Todo.ID != 0 {
		todo := todoView(reviewed.Todo)
		result.Todo = &todo
	}
	if reviewed.Disposition == store.ReviewedTodoCreated {
		if _, err := s.store.ForceQueueTodoWorktreeSuggestion(ctx, reviewed.Todo.ID); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("TODO was saved, but its worktree suggestion could not be queued: %v", err))
		}
	}
	event := ExternalEvent{
		Action:             "add_todo",
		Provider:           strings.TrimSpace(origin.Provider),
		SessionKey:         strings.TrimSpace(origin.SessionKey),
		RequestedPath:      scope.RequestedPath,
		ProjectPath:        scope.ProjectPath,
		CaptureKind:        kind,
		Disposition:        result.Disposition,
		TodoID:             reviewed.Todo.ID,
		PublishedInProcess: origin.PublishedInProcess,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return AddResult{}, err
	}
	if err := s.store.AddEvent(ctx, model.StoredEvent{
		At:          time.Now(),
		ProjectPath: scope.ProjectPath,
		Type:        ExternalEventType,
		Payload:     string(payload),
	}); err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("TODO result is valid, but capture provenance could not be recorded: %v", err))
	}
	return result, nil
}

func (s *Service) effectiveMode(ctx context.Context) (CaptureMode, error) {
	if !s.requireRuntimeMode {
		return s.mode, nil
	}
	value, found, err := s.store.RuntimeSetting(ctx, RuntimeModeSettingKey)
	if err != nil {
		return ModeOff, fmt.Errorf("read current TODO capture policy: %w", err)
	}
	if !found {
		return ModeOff, fmt.Errorf("current TODO capture policy is unavailable; capture is disabled")
	}
	mode, err := ParseCaptureMode(value)
	if err != nil {
		return ModeOff, fmt.Errorf("read current TODO capture policy: %w", err)
	}
	// Launch-time capability is an upper bound, while the persisted runtime
	// setting can revoke or narrow it immediately. Expanding policy requires a
	// reconnect so the model receives the corresponding tool schema/prompt.
	return mostRestrictiveCaptureMode(s.mode, mode), nil
}

func externalDisposition(disposition store.ReviewedTodoDisposition) string {
	switch disposition {
	case store.ReviewedTodoCreated:
		return DispositionCreated
	case store.ReviewedTodoExisting:
		return DispositionExistingDuplicate
	case store.ReviewedTodoStale:
		return DispositionTodosChanged
	default:
		return string(disposition)
	}
}

// ResolveProject deliberately has no caller-supplied override. The launch cwd
// is resolved to an LCR project, and linked worktrees are redirected to their
// loaded repository root. If that mapping is unavailable or ambiguous, capture
// fails closed instead of writing to a guessed project.
func (s *Service) ResolveProject(ctx context.Context, requestedPath string) (ProjectScope, error) {
	requestedPath = strings.TrimSpace(requestedPath)
	if requestedPath == "" {
		return ProjectScope{}, fmt.Errorf("launch project path is required")
	}
	abs, err := filepath.Abs(requestedPath)
	if err != nil {
		return ProjectScope{}, fmt.Errorf("resolve launch project path: %w", err)
	}
	requestedPath = filepath.Clean(abs)

	info, err := scanner.ReadGitWorktreeInfo(ctx, requestedPath)
	if err == nil {
		repositoryRoot := filepath.Clean(info.RootPath)
		summary, found, err := s.uniqueSymlinkEquivalentProject(ctx, repositoryRoot)
		if err != nil {
			return ProjectScope{}, err
		}
		if !found {
			// EvalSymlinks can fail for a temporarily unavailable path even
			// when Git supplied a durable root spelling. Preserve exact-path
			// resolution in that case, but only after the uniqueness check.
			summary, err = s.store.GetProjectSummary(ctx, repositoryRoot, true)
			if err == nil {
				found = true
			} else if !errors.Is(err, sql.ErrNoRows) {
				return ProjectScope{}, fmt.Errorf("resolve loaded repository root %s: %w", repositoryRoot, err)
			}
		}
		if found {
			return ProjectScope{
				RequestedPath: requestedPath,
				ProjectPath:   summary.Path,
				ProjectName:   summary.Name,
				FromWorktree:  info.Kind == scanner.GitWorktreeKindLinked || requestedPath != summary.Path,
			}, nil
		}
		return ProjectScope{}, fmt.Errorf("repository root %s is not an unambiguous loaded LCR project", repositoryRoot)
	}

	// Non-Git projects and temporarily unavailable repositories use LCR's
	// durable mapping. Live Git metadata always wins when it is available.
	if summary, lookupErr := s.store.GetProjectSummary(ctx, requestedPath, true); lookupErr == nil {
		return s.scopeFromSummary(ctx, requestedPath, summary)
	} else if !errors.Is(lookupErr, sql.ErrNoRows) {
		return ProjectScope{}, fmt.Errorf("resolve loaded project %s: %w", requestedPath, lookupErr)
	}
	return ProjectScope{}, fmt.Errorf("launch path %s does not map to a loaded LCR project (Git inspection failed: %v)", requestedPath, err)
}

func (s *Service) scopeFromSummary(ctx context.Context, requestedPath string, summary model.ProjectSummary) (ProjectScope, error) {
	project := summary
	fromWorktree := summary.WorktreeKind == model.WorktreeKindLinked
	if fromWorktree {
		rootPath := filepath.Clean(strings.TrimSpace(summary.WorktreeRootPath))
		if rootPath == "" || rootPath == "." {
			return ProjectScope{}, fmt.Errorf("linked worktree %s has no repository-root mapping", summary.Path)
		}
		root, err := s.store.GetProjectSummary(ctx, rootPath, true)
		if errors.Is(err, sql.ErrNoRows) {
			var found bool
			root, found, err = s.uniqueSymlinkEquivalentProject(ctx, rootPath)
			if err == nil && !found {
				err = sql.ErrNoRows
			}
		}
		if err != nil {
			return ProjectScope{}, fmt.Errorf("linked worktree repository root %s is not an unambiguous loaded LCR project: %w", rootPath, err)
		}
		project = root
	}
	return ProjectScope{
		RequestedPath: requestedPath,
		ProjectPath:   project.Path,
		ProjectName:   project.Name,
		FromWorktree:  fromWorktree || requestedPath != project.Path,
	}, nil
}

func (s *Service) uniqueSymlinkEquivalentProject(ctx context.Context, candidate string) (model.ProjectSummary, bool, error) {
	resolvedCandidate, err := filepath.EvalSymlinks(filepath.Clean(candidate))
	if err != nil {
		return model.ProjectSummary{}, false, nil
	}
	resolvedCandidate = filepath.Clean(resolvedCandidate)
	projects, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return model.ProjectSummary{}, false, fmt.Errorf("list loaded projects while resolving %s: %w", candidate, err)
	}
	matches := make([]model.ProjectSummary, 0, 1)
	for path, summary := range projects {
		resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
		if err == nil && filepath.Clean(resolved) == resolvedCandidate {
			visible, err := s.store.GetProjectSummary(ctx, summary.Path, true)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return model.ProjectSummary{}, false, fmt.Errorf("validate loaded project %s while resolving %s: %w", summary.Path, candidate, err)
			}
			matches = append(matches, visible)
		}
	}
	switch len(matches) {
	case 0:
		return model.ProjectSummary{}, false, nil
	case 1:
		return matches[0], true, nil
	default:
		return model.ProjectSummary{}, false, fmt.Errorf("launch path %s matches multiple loaded LCR project spellings", candidate)
	}
}

func ParseExternalEvent(payload string) (ExternalEvent, error) {
	var event ExternalEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return ExternalEvent{}, err
	}
	if event.Action != "add_todo" || strings.TrimSpace(event.ProjectPath) == "" {
		return ExternalEvent{}, fmt.Errorf("invalid external TODO capture event")
	}
	return event, nil
}

func todoViews(items []model.TodoItem) []Todo {
	out := make([]Todo, 0, len(items))
	for _, item := range items {
		out = append(out, todoView(item))
	}
	return out
}

func todoView(item model.TodoItem) Todo {
	if item.ID == 0 {
		return Todo{}
	}
	view := Todo{ID: item.ID, Text: item.Text, Position: item.Position}
	if !item.UpdatedAt.IsZero() {
		view.UpdatedAt = item.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return view
}
