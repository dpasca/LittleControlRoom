package uisurface

import (
	"reflect"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/procinspect"
	"lcroom/internal/projectrun"
)

func TestBuildSessionSidebarUsesStableTUISectionOrder(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 16, 10, 0, 0, 0, time.UTC)
	surface := BuildSessionSidebar(codexapp.Snapshot{
		Provider:               codexapp.ProviderCodex,
		ProjectPath:            "/tmp/mobile",
		ThreadID:               "thread-1",
		Started:                true,
		Busy:                   true,
		Phase:                  codexapp.SessionPhaseRunning,
		CurrentCWD:             "/tmp/mobile",
		QualityPlanUpdates:     2,
		QualityPlanPhases:      1,
		QualityPlanNeedsRepair: 1,
		ImageAnalyses:          2,
		LastActivityAt:         now,
	}, ProjectItem{
		Path:       "/tmp/mobile",
		Name:       "Mobile",
		Summary:    "Review the action deck.",
		Assessment: Status{Label: "Follow up", Tone: ToneWarning},
	}, now)

	ids := make([]string, 0, len(surface.Sections))
	for _, section := range surface.Sections {
		ids = append(ids, section.ID)
	}
	want := []string{"session", "quality", "vision", "browser", "mcps", "summary"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("section order = %#v, want %#v", ids, want)
	}
	if got, want := surface.Sections[1].Summary, "1 need repair"; got != want {
		t.Fatalf("quality summary = %q, want %q", got, want)
	}
}

func TestBuildTodoSurfaceUsesRepositoryScopeAndOpenFirst(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 16, 10, 0, 0, 0, time.UTC)
	surface := BuildTodoSurface(
		ProjectItem{Path: "/tmp/repo--feature", Name: "Feature"},
		ProjectItem{Path: "/tmp/repo", Name: "Repository"},
		[]model.TodoItem{
			{ID: 1, Text: "Resolved", Done: true, Position: 0, UpdatedAt: now},
			{ID: 2, Text: "Second open", Position: 2, UpdatedAt: now},
			{ID: 3, Text: "First open", Position: 1, UpdatedAt: now},
		},
		true,
	)

	if got, want := surface.ScopeLabel, "Repository TODOs"; got != want {
		t.Fatalf("scope label = %q, want %q", got, want)
	}
	if surface.OpenCount != 2 || surface.DoneCount != 1 || !surface.WriteEnabled {
		t.Fatalf("TODO counts/control = %#v", surface)
	}
	if got := []int64{surface.Todos[0].ID, surface.Todos[1].ID, surface.Todos[2].ID}; !reflect.DeepEqual(got, []int64{3, 2, 1}) {
		t.Fatalf("TODO order = %#v, want open-by-position then resolved", got)
	}
}

func TestBuildRuntimeSurfaceKeepsExternalProcessesReadOnly(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 16, 10, 0, 0, 0, time.UTC)
	surface := BuildRuntimeSurface(
		ProjectItem{Path: "/tmp/mobile", Name: "Mobile"},
		"make dev",
		[]projectrun.Snapshot{{
			ID:          "managed-1",
			Name:        "Web preview",
			ProjectPath: "/tmp/mobile",
			Command:     "make dev",
			PID:         120,
			Running:     true,
			Ports:       []int{7777},
		}},
		procinspect.ProjectReport{Instances: []procinspect.ProjectInstance{{Process: procinspect.Process{
			PID:     220,
			Command: "node external.js",
			CWD:     "/tmp/mobile",
			Ports:   []int{3000},
		}}}},
		true,
		now,
	)

	if got, want := len(surface.Processes), 2; got != want {
		t.Fatalf("process count = %d, want %d", got, want)
	}
	managed, external := surface.Processes[0], surface.Processes[1]
	if !managed.Managed || !managed.CanStop || !managed.CanRestart {
		t.Fatalf("managed runtime actions = %#v", managed)
	}
	if external.Managed || external.CanStop || external.CanRestart || external.Status.Label != "External" {
		t.Fatalf("external runtime should be read only: %#v", external)
	}
	if !reflect.DeepEqual(managed.URLs, []string{"http://127.0.0.1:7777"}) {
		t.Fatalf("managed URLs = %#v", managed.URLs)
	}
}
