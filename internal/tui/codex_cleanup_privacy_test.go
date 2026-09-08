package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/service"
)

func TestCodexCleanupPrivacyFiltersAuditAndSelection(t *testing.T) {
	for _, privacy := range []bool{false, true} {
		t.Run(fmt.Sprint(privacy), func(t *testing.T) {
			groups := []service.CodexCleanupWorktreeGroup{
				{WorktreePath: "/public", RootThreadCount: 1, RecoverableBytes: 10},
				{WorktreePath: "/secret", RootThreadCount: 2, RecoverableBytes: 20},
				{WorktreePath: "/deleted-secret", RootProjectPath: "/secret", RootThreadCount: 3, RecoverableBytes: 30},
				{WorktreePath: "/linked-secret", RootThreadCount: 4, RecoverableBytes: 40},
			}
			m := Model{privacyMode: privacy, allProjects: []model.ProjectSummary{
				{Path: "/secret", CategoryPrivate: true},
				{Path: "/linked-secret", WorktreeRootPath: "/secret"},
			}, codexCleanup: &codexCleanupDialogState{Loading: true}}
			audit := service.CodexCleanupAudit{Groups: groups, EligibleRootThreads: 10, RecoverableBytes: 100,
				Retained: []service.CodexCleanupRetainedGroup{
					{Name: "public", Path: "/public"},
					{Name: "secret", Path: "/secret"},
					{Name: "deleted-secret", Path: "/deleted-secret", ProjectPath: "/secret"},
				},
			}
			updated, _ := m.applyCodexCleanupAudit(codexCleanupAuditMsg{audit: audit})
			m = updated.(Model)
			wantGroups, wantRetained, wantRoots := 4, 3, 10
			if privacy {
				wantGroups, wantRetained, wantRoots = 1, 1, 1
			}
			d := m.codexCleanup
			if len(d.Audit.Groups) != wantGroups || len(d.Audit.Retained) != wantRetained || d.Audit.EligibleRootThreads != wantRoots {
				t.Fatalf("unexpected visible audit: %+v", d.Audit)
			}
			for _, group := range groups {
				d.Chosen[group.WorktreePath] = true
			}
			if len(selectedCodexCleanupGroups(d)) != wantGroups {
				t.Fatal("hidden group selected for deletion")
			}
			if privacy {
				for _, retained := range []bool{false, true} {
					d.ShowRetained = retained
					text := renderCodexCleanupContent(d, 100, 45, 0, time.Now())
					if strings.Contains(text, "secret") {
						t.Fatalf("private row rendered: %s", text)
					}
				}
				if d.Audit.RecoverableBytes != 10 {
					t.Fatal("recoverable total includes hidden projects")
				}
			}
			if len(audit.Groups) != 4 || audit.Groups[1].WorktreePath != "/secret" {
				t.Fatal("source audit mutated")
			}
		})
	}
}

func TestCodexCleanupPrivacyMasksBackgroundReportWithoutChangingQueue(t *testing.T) {
	group := service.CodexCleanupWorktreeGroup{WorktreePath: "/secret"}
	d := &codexCleanupDialogState{Deleting: true, Queue: []service.CodexCleanupWorktreeGroup{group},
		Audit:  service.CodexCleanupAudit{Groups: []service.CodexCleanupWorktreeGroup{group}},
		Chosen: map[string]bool{"/secret": true},
	}
	m := Model{privacyMode: true, allProjects: []model.ProjectSummary{{Path: "/secret", CategoryPrivate: true}}, codexCleanup: d}
	m.filterCodexCleanupForPrivacy()
	if len(d.Audit.Groups) != 0 || d.Chosen["/secret"] {
		t.Fatal("privacy toggle retained a hidden selection")
	}
	if text := renderCodexCleanupContent(m.codexCleanupPrivacyView(), 100, 45, 0, time.Now()); strings.Contains(text, "secret") {
		t.Fatal(text)
	}
	d.Deleting, d.Finished = false, true
	d.Results = []codexCleanupDeleteResult{{Group: group, Err: fmt.Errorf("failed /secret")}}
	d.ErrorMessage = "failed /secret"
	if text := renderCodexCleanupContent(m.codexCleanupPrivacyView(), 100, 45, 0, time.Now()); strings.Contains(text, "secret") {
		t.Fatal(text)
	}
	if d.Queue[0].WorktreePath != "/secret" || d.Results[0].Group.WorktreePath != "/secret" {
		t.Fatal("rendering changed delete identity")
	}
}
