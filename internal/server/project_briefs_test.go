package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/agentquery"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/projectbrief"
	"lcroom/internal/service"
	"lcroom/internal/store"
)

func TestBriefResponsesUseWriterVisibility(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	root, worktree := filepath.Join(dir, "root"), filepath.Join(dir, "linked")
	fallback := filepath.Join(dir, "fallback")
	db, err := store.Open(filepath.Join(dir, "briefs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, path := range []string{root, worktree, fallback} {
		project := model.ProjectState{Path: path, Name: filepath.Base(path), InScope: true, UpdatedAt: time.Now()}
		if path != root {
			project.WorktreeRootPath, project.WorktreeKind = root, model.WorktreeKindLinked
		}
		if err := db.UpsertProjectState(ctx, project); err != nil {
			t.Fatal(err)
		}
		if path == fallback {
			continue
		}
		if err := db.BindEngineerSession(ctx, path, model.SessionSourceCodex, "key", "thread"); err != nil {
			t.Fatal(err)
		}
		writer := projectbrief.Writer{Store: db, CheckoutPath: path, Provider: model.SessionSourceCodex, SessionKey: "key"}
		_, err := writer.Record(ctx, model.ProjectBriefText{Goal: "Ship it", Now: "Reported by " + project.Name, Next: "Review it"})
		if err != nil {
			t.Fatal(err)
		}
	}
	category, err := db.CreateProjectCategory(ctx, "Private fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetProjectCategoryPrivate(ctx, category.Name, true); err != nil {
		t.Fatal(err)
	}
	if err := db.SetResourceCategory(ctx, model.CategoryResourceProject, worktree, category.ID); err != nil {
		t.Fatal(err)
	}

	for _, privacy := range []bool{false, true} {
		cfg := config.Default()
		cfg.DataDir, cfg.PrivacyMode = dir, privacy
		handler := New(service.New(cfg, db, events.NewBus(), nil)).Handler(ctx)
		for _, endpoint := range []string{"/projects", "/api/mobile/dashboard"} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, endpoint, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("%s: %d %s", endpoint, response.Code, response.Body)
			}
			data := response.Body.Bytes()
			pathKey, summaryKey := "Path", "LatestSessionSummary"
			if endpoint != "/projects" {
				var dashboard struct {
					Projects json.RawMessage `json:"projects"`
				}
				if err := json.Unmarshal(data, &dashboard); err != nil {
					t.Fatal(err)
				}
				data, pathKey, summaryKey = dashboard.Projects, "path", "summary"
			}
			var projects []map[string]json.RawMessage
			if err := json.Unmarshal(data, &projects); err != nil {
				t.Fatal(err)
			}
			wantCount := 3
			if privacy {
				wantCount = 2
			}
			if len(projects) != wantCount || (privacy && strings.Contains(string(data), "Reported by linked")) {
				t.Fatalf("%s with privacy=%v: %s", endpoint, privacy, data)
			}
			for _, project := range projects {
				var got model.ProjectBrief
				if err := json.Unmarshal(project["brief"], &got); err != nil {
					t.Fatal(err)
				}
				var path string
				if err := json.Unmarshal(project[pathKey], &path); err != nil {
					t.Fatal(err)
				}
				wantAuthor := path
				if path == fallback {
					wantAuthor = worktree
					if privacy {
						wantAuthor = root
					}
				}
				if len(project[summaryKey]) == 0 || got.CheckoutPath != wantAuthor ||
					got.Goal != "Ship it" || got.RecordedAt.IsZero() || got.Provider != model.SessionSourceCodex || got.NeedsStatus != "none" {
					t.Fatalf("%s with privacy=%v: %s", endpoint, privacy, data)
				}
			}
		}
	}
}

func TestBriefReadFailuresKeepHTTPAndQueryResults(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "briefs.sqlite")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	valid, corrupt := filepath.Join(dir, "healthy-project"), filepath.Join(dir, "corrupt-project")
	for _, path := range []string{valid, corrupt} {
		if err := db.UpsertProjectState(ctx, model.ProjectState{
			Path: path, Name: filepath.Base(path), InScope: true, UpdatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.BindEngineerSession(ctx, valid, model.SessionSourceCodex, "key", "thread"); err != nil {
		t.Fatal(err)
	}
	writer := projectbrief.Writer{Store: db, CheckoutPath: valid, Provider: model.SessionSourceCodex, SessionKey: "key"}
	const briefText = "Healthy brief still appears"
	if _, err := writer.Record(ctx, model.ProjectBriefText{Goal: "Ship it", Now: briefText, Next: "Review"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertProjectBrief(ctx, store.ProjectBriefRow{
		CheckoutPath: corrupt, RecordedAt: time.Now(), Payload: json.RawMessage(`{"bad payload"`),
	}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir = dir
	handler := New(service.New(cfg, db, events.NewBus(), nil)).Handler(ctx)
	executor, err := agentquery.NewExecutor(agentquery.Options{Reader: db, Scope: agentquery.ScopePortfolio, OriginProjectPath: valid})
	if err != nil {
		t.Fatal(err)
	}
	verify := func(t *testing.T, data []byte, wantBrief bool) {
		t.Helper()
		body := string(data)
		wantCount := 0
		if wantBrief {
			wantCount = 1
		}
		if !strings.Contains(body, "healthy-project") || !strings.Contains(body, "corrupt-project") ||
			strings.Contains(body, briefText) != wantBrief || strings.Contains(body, "bad payload") ||
			strings.Count(body, `"brief":`) != wantCount {
			t.Fatalf("want both project rows and healthy brief=%v, got %s", wantBrief, body)
		}
	}
	check := func(t *testing.T, wantBrief bool) {
		t.Helper()
		for _, endpoint := range []string{"/projects", "/api/mobile/dashboard"} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, endpoint, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("%s: %d %s", endpoint, response.Code, response.Body)
			}
			verify(t, response.Body.Bytes(), wantBrief)
		}
		result, err := executor.Execute(ctx, agentquery.QueryProjectList, nil)
		if err != nil || result["success"] != true {
			t.Fatalf("project.list failed: %v, %v", result, err)
		}
		body, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		verify(t, body, wantBrief)
	}
	t.Run("malformed payload", func(t *testing.T) {
		check(t, true)
	})
	t.Run("briefs table unavailable", func(t *testing.T) {
		sqlDB, err := sql.Open("lcroom-sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer sqlDB.Close()
		if _, err := sqlDB.ExecContext(ctx, "DROP TABLE project_briefs"); err != nil {
			t.Fatal(err)
		}
		check(t, false)
	})
}
