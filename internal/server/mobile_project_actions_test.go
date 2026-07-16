package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"
	"lcroom/internal/uisurface"
)

func TestMobileProjectTODOActionsUseRepositoryScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "little-control-room.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now()
	rootPath := filepath.Join(dataDir, "repo")
	childPath := filepath.Join(dataDir, "repo--feature")
	otherPath := filepath.Join(dataDir, "other")
	for _, state := range []model.ProjectState{
		{Path: rootPath, Name: "Repository", PresentOnDisk: true, InScope: true, WorktreeRootPath: rootPath, WorktreeKind: model.WorktreeKindMain, UpdatedAt: now},
		{Path: childPath, Name: "Feature", PresentOnDisk: true, InScope: true, WorktreeRootPath: rootPath, WorktreeKind: model.WorktreeKindLinked, UpdatedAt: now},
		{Path: otherPath, Name: "Other", PresentOnDisk: true, InScope: true, UpdatedAt: now},
	} {
		if err := st.UpsertProjectState(ctx, state); err != nil {
			t.Fatalf("upsert %s: %v", state.Path, err)
		}
	}
	rootTodo, err := st.AddTodo(ctx, rootPath, "Review the mobile sheet")
	if err != nil {
		t.Fatalf("add root TODO: %v", err)
	}
	foreignTodo, err := st.AddTodo(ctx, otherPath, "Unrelated task")
	if err != nil {
		t.Fatalf("add foreign TODO: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.DBPath = dbPath
	cfg.MobileInputEnabled = true
	svc := service.New(cfg, st, events.NewBus(), nil)
	handler := New(svc).Handler(ctx)

	getTodos := httptest.NewRequest(http.MethodGet, "/api/mobile/projects/todos?path="+url.QueryEscape(childPath), nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getTodos)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET TODOs status = %d, body = %s", getResponse.Code, getResponse.Body.String())
	}
	var initial uisurface.TodoSurface
	if err := json.Unmarshal(getResponse.Body.Bytes(), &initial); err != nil {
		t.Fatalf("decode TODO surface: %v", err)
	}
	if initial.ScopeProject.Path != rootPath || initial.ScopeLabel != "Repository TODOs" {
		t.Fatalf("TODO scope = %#v, want repository root %q", initial, rootPath)
	}
	if len(initial.Todos) != 1 || initial.Todos[0].ID != rootTodo.ID {
		t.Fatalf("initial TODOs = %#v, want only root TODO", initial.Todos)
	}

	postAction := func(payload mobileTodoActionRequest) *httptest.ResponseRecorder {
		raw, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			t.Fatalf("marshal action: %v", marshalErr)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/mobile/projects/todos/action", bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	addResponse := postAction(mobileTodoActionRequest{
		ProjectPath: childPath,
		RequestID:   "todo-add-1",
		Action:      "add",
		Text:        "Exercise the embossed controls",
	})
	if addResponse.Code != http.StatusOK {
		t.Fatalf("add TODO status = %d, body = %s", addResponse.Code, addResponse.Body.String())
	}
	var added uisurface.TodoSurface
	if err := json.Unmarshal(addResponse.Body.Bytes(), &added); err != nil {
		t.Fatalf("decode added TODO surface: %v", err)
	}
	if added.ScopeProject.Path != rootPath || added.OpenCount != 2 {
		t.Fatalf("added TODO surface = %#v", added)
	}

	done := true
	toggleResponse := postAction(mobileTodoActionRequest{
		ProjectPath: childPath,
		RequestID:   "todo-toggle-1",
		Action:      "toggle",
		TodoID:      rootTodo.ID,
		Done:        &done,
	})
	if toggleResponse.Code != http.StatusOK {
		t.Fatalf("toggle TODO status = %d, body = %s", toggleResponse.Code, toggleResponse.Body.String())
	}

	foreignResponse := postAction(mobileTodoActionRequest{
		ProjectPath: childPath,
		RequestID:   "todo-foreign-1",
		Action:      "toggle",
		TodoID:      foreignTodo.ID,
		Done:        &done,
	})
	if foreignResponse.Code != http.StatusNotFound {
		t.Fatalf("foreign TODO status = %d, want 404; body = %s", foreignResponse.Code, foreignResponse.Body.String())
	}

	suggestionRequest := httptest.NewRequest(http.MethodGet, "/api/mobile/commands/suggestions?q=%2F&context=dashboard&path="+url.QueryEscape(childPath), nil)
	suggestionResponse := httptest.NewRecorder()
	handler.ServeHTTP(suggestionResponse, suggestionRequest)
	if suggestionResponse.Code != http.StatusOK {
		t.Fatalf("GET command suggestions status = %d, body = %s", suggestionResponse.Code, suggestionResponse.Body.String())
	}
	var suggestions mobileCommandSuggestionsSurface
	if err := json.Unmarshal(suggestionResponse.Body.Bytes(), &suggestions); err != nil {
		t.Fatalf("decode command suggestions: %v", err)
	}
	foundSidebar := false
	for _, suggestion := range suggestions.Suggestions {
		if suggestion.Insert == "/sidebar" && suggestion.Supported && suggestion.ClientAction == "sidebar" {
			foundSidebar = true
			break
		}
	}
	if !foundSidebar {
		t.Fatalf("command suggestions missing supported /sidebar action: %#v", suggestions.Suggestions)
	}
}

func TestMobileProjectMutationsRequirePhoneControl(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "little-control-room.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	projectPath := filepath.Join(dataDir, "project")
	if err := st.UpsertProjectState(ctx, model.ProjectState{Path: projectPath, Name: "Project", PresentOnDisk: true, InScope: true}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.DBPath = dbPath
	svc := service.New(cfg, st, events.NewBus(), nil)
	handler := New(svc).Handler(ctx)
	raw, err := json.Marshal(mobileTodoActionRequest{ProjectPath: projectPath, RequestID: "disabled-1", Action: "add", Text: "Should not save"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/mobile/projects/todos/action", bytes.NewReader(raw))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("disabled phone-control status = %d, want 403; body = %s", response.Code, response.Body.String())
	}
}
