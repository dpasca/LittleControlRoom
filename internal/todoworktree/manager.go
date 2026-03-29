package todoworktree

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"lcroom/internal/events"
	"lcroom/internal/store"
)

type Options struct {
	Client      Suggester
	Workers     int
	Debounce    time.Duration
	StaleAfter  time.Duration
}

type Manager struct {
	store      *store.Store
	bus        *events.Bus
	mu         sync.RWMutex
	client     Suggester
	modelName  string
	workers    int
	debounce   time.Duration
	staleAfter time.Duration
	notifyCh   chan struct{}
	startOnce  sync.Once
}

func NewManager(st *store.Store, bus *events.Bus, opts Options) *Manager {
	workers := opts.Workers
	if workers <= 0 {
		workers = 1
	}
	debounce := opts.Debounce
	if debounce <= 0 {
		debounce = 90 * time.Second
	}
	staleAfter := opts.StaleAfter
	if staleAfter <= 0 {
		staleAfter = 3 * time.Minute
	}
	modelName := ""
	if named, ok := opts.Client.(interface{ ModelName() string }); ok {
		modelName = strings.TrimSpace(named.ModelName())
	}
	return &Manager{
		store:      st,
		bus:        bus,
		client:     opts.Client,
		modelName:  modelName,
		workers:    workers,
		debounce:   debounce,
		staleAfter: staleAfter,
		notifyCh:   make(chan struct{}, 1),
	}
}

func (m *Manager) ConfigureClient(client Suggester) {
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

func (m *Manager) currentClient() (Suggester, string) {
	if m == nil {
		return nil, ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.client, m.modelName
}

func (m *Manager) Enabled() bool {
	client, _ := m.currentClient()
	return client != nil
}

func (m *Manager) QueueTodo(ctx context.Context, todoID int64) (bool, error) {
	if m == nil || m.store == nil || !m.Enabled() {
		return false, nil
	}
	return m.store.QueueTodoWorktreeSuggestion(ctx, todoID)
}

func (m *Manager) Notify() {
	if m == nil {
		return
	}
	select {
	case m.notifyCh <- struct{}{}:
	default:
	}
}

func (m *Manager) Start(ctx context.Context) {
	if m == nil || m.store == nil || !m.Enabled() {
		return
	}
	m.startOnce.Do(func() {
		_, _ = m.store.QueueOpenTodoWorktreeSuggestions(ctx)
		m.Notify()
		for i := 0; i < m.workers; i++ {
			go m.worker(ctx)
		}
	})
}

func (m *Manager) worker(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.notifyCh:
		case <-tick.C:
		}
		for {
			err := m.processOne(ctx)
			if err == nil {
				continue
			}
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			break
		}
	}
}

func (m *Manager) processOne(ctx context.Context) error {
	client, modelName := m.currentClient()
	if client == nil {
		return sql.ErrNoRows
	}
	suggestion, err := m.store.ClaimNextQueuedTodoWorktreeSuggestion(ctx, m.debounce, m.staleAfter)
	if err != nil {
		return err
	}

	detail, err := m.store.GetProjectDetail(ctx, suggestion.ProjectPath, 0)
	if err != nil {
		_, _ = m.store.FailTodoWorktreeSuggestion(ctx, suggestion, err.Error())
		return err
	}
	request := Request{
		ProjectPath: detail.Summary.Path,
		ProjectName: detail.Summary.Name,
		TodoID:      suggestion.TodoID,
		TodoText:    suggestion.TodoText,
	}
	for _, item := range detail.Todos {
		if item.Done || item.ID == suggestion.TodoID {
			continue
		}
		request.OpenSiblingTodos = append(request.OpenSiblingTodos, item.Text)
		if len(request.OpenSiblingTodos) >= defaultOpenSiblingTodoContext {
			break
		}
	}

	result, err := client.Suggest(ctx, request)
	if err != nil {
		_, _ = m.store.FailTodoWorktreeSuggestion(ctx, suggestion, err.Error())
		m.publish(detail.Summary.Path, "todo_worktree_suggestion_failed", suggestion.TodoID, modelName)
		return nil
	}

	suggestion.BranchName = normalizeBranchName(result.BranchName)
	suggestion.WorktreeSuffix = normalizeWorktreeSuffix(result.WorktreeSuffix)
	if suggestion.WorktreeSuffix == "" {
		suggestion.WorktreeSuffix = normalizeWorktreeSuffix(strings.ReplaceAll(suggestion.BranchName, "/", "-"))
	}
	suggestion.Kind = strings.TrimSpace(result.Kind)
	suggestion.Reason = strings.TrimSpace(result.Reason)
	suggestion.Confidence = result.Confidence
	suggestion.Model = strings.TrimSpace(result.Model)

	if suggestion.BranchName == "" || suggestion.WorktreeSuffix == "" {
		_, _ = m.store.FailTodoWorktreeSuggestion(ctx, suggestion, "todo worktree suggester returned unusable names")
		m.publish(detail.Summary.Path, "todo_worktree_suggestion_failed", suggestion.TodoID, modelName)
		return nil
	}

	completed, err := m.store.CompleteTodoWorktreeSuggestion(ctx, suggestion)
	if err != nil {
		return err
	}
	if completed {
		m.publish(detail.Summary.Path, "todo_worktree_suggestion_ready", suggestion.TodoID, suggestion.Model)
	}
	return nil
}

func (m *Manager) publish(projectPath, action string, todoID int64, modelName string) {
	if m == nil || m.bus == nil {
		return
	}
	payload := map[string]string{
		"action": action,
		"todo_id": fmt.Sprintf("%d", todoID),
	}
	if strings.TrimSpace(modelName) != "" {
		payload["model"] = strings.TrimSpace(modelName)
	}
	m.bus.Publish(events.Event{
		Type:        events.ActionApplied,
		At:          time.Now(),
		ProjectPath: strings.TrimSpace(projectPath),
		Payload:     payload,
	})
}

func normalizeBranchName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastSlash := false
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastSlash = false
			lastDash = false
		case r == '/':
			if b.Len() == 0 || lastSlash {
				continue
			}
			b.WriteRune('/')
			lastSlash = true
			lastDash = false
		default:
			if b.Len() == 0 || lastDash || lastSlash {
				continue
			}
			b.WriteRune('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-/")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	for strings.Contains(out, "//") {
		out = strings.ReplaceAll(out, "//", "/")
	}
	return out
}

func normalizeWorktreeSuffix(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if b.Len() == 0 || lastDash {
				continue
			}
			b.WriteRune('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return out
}
