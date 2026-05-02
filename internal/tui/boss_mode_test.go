package tui

import (
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/procinspect"
)

func TestBossViewContextCapturesClassicTUIStateWithoutSelection(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	project := model.ProjectSummary{
		Path:                 "/tmp/alpha",
		Name:                 "Alpha",
		Status:               model.StatusPossiblyStuck,
		AttentionScore:       31,
		LastActivity:         now.Add(-time.Hour),
		RepoBranch:           "feature/boss",
		RepoDirty:            true,
		OpenTODOCount:        2,
		LatestSessionSummary: "Waiting for product direction.",
	}
	m := Model{
		allProjects: []model.ProjectSummary{
			project,
			{Path: "/tmp/beta", Name: "Beta"},
		},
		projects:    []model.ProjectSummary{project},
		selected:    0,
		sortMode:    sortByAttention,
		visibility:  visibilityAllFolders,
		focusedPane: focusDetail,
		status:      "Detail focused",
		privacyMode: true,
		privacyPatterns: []string{
			"*private*",
		},
	}

	view := m.bossViewContext()
	if !view.Active || !view.Embedded {
		t.Fatalf("view should be active embedded context: %#v", view)
	}
	if view.VisibleProjectCount != 1 || view.AllProjectCount != 2 {
		t.Fatalf("project counts = visible %d all %d", view.VisibleProjectCount, view.AllProjectCount)
	}
	if view.FocusedPane != "detail" || view.SortMode != "attention" || view.Visibility != "all_folders" {
		t.Fatalf("view controls = %#v", view)
	}
	if !view.PrivacyMode || len(view.PrivacyPatterns) != 1 || view.PrivacyPatterns[0] != "*private*" {
		t.Fatalf("privacy state = %#v, want privacy mode and patterns", view)
	}
}

func TestBossViewContextIncludesProcessSystemNotice(t *testing.T) {
	t.Parallel()

	project := model.ProjectSummary{Path: "/tmp/alpha", Name: "Alpha"}
	m := Model{
		allProjects: []model.ProjectSummary{project},
		projects:    []model.ProjectSummary{project},
		processReports: map[string]procinspect.ProjectReport{
			project.Path: {
				ProjectPath: project.Path,
				Findings: []procinspect.Finding{{
					Process:     procinspect.Process{PID: 49995, PPID: 1, CPU: 99, Ports: []int{9229}},
					ProjectPath: project.Path,
				}},
			},
		},
	}

	view := m.bossViewContext()
	if len(view.SystemNotices) != 1 {
		t.Fatalf("SystemNotices len = %d, want 1", len(view.SystemNotices))
	}
	notice := view.SystemNotices[0]
	if notice.Code != "process_suspicious" || notice.Severity != "warning" || notice.Count != 1 {
		t.Fatalf("notice = %#v, want process warning", notice)
	}
	if !strings.Contains(notice.Summary, "Processes: 1 suspicious") || !strings.Contains(notice.Summary, "process_report") {
		t.Fatalf("notice summary = %q, want process report guidance", notice.Summary)
	}
}

func TestBossViewContextProcessSystemNoticeRespectsPrivacy(t *testing.T) {
	t.Parallel()

	secret := model.ProjectSummary{Path: "/tmp/secret", Name: "SecretClient"}
	m := Model{
		allProjects:     []model.ProjectSummary{secret},
		privacyMode:     true,
		privacyPatterns: []string{"*Secret*"},
		processReports: map[string]procinspect.ProjectReport{
			secret.Path: {
				ProjectPath: secret.Path,
				Findings: []procinspect.Finding{{
					Process:     procinspect.Process{PID: 49995, PPID: 1, CPU: 99},
					ProjectPath: secret.Path,
				}},
			},
		},
	}

	view := m.bossViewContext()
	if len(view.SystemNotices) != 0 {
		t.Fatalf("privacy mode should hide process notices for private projects, got %#v", view.SystemNotices)
	}
}
