package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	bossui "lcroom/internal/boss"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
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
	if !strings.Contains(notice.Summary, "1 suspicious project-local PID") || !strings.Contains(notice.Summary, "process_report") {
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

func TestBossViewContextIncludesBrowserAndQuestionNotices(t *testing.T) {
	t.Parallel()

	m := Model{
		browserAttention: &browserAttentionNotification{
			ProjectPath:              "/tmp/browser-task",
			ProjectName:              "browser-task",
			Provider:                 codexapp.ProviderCodex,
			ManagedBrowserSessionKey: "managed-session",
			Activity: browserctl.SessionActivity{
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
				ToolName:   "browser_navigate",
			},
		},
		questionNotify: &questionNotification{
			ProjectPath: "/tmp/question-task",
			ProjectName: "question-task",
			Provider:    codexapp.ProviderCodex,
			Summary:     "Codex is waiting for a choice.",
		},
	}

	view := m.bossViewContext()
	if len(view.SystemNotices) != 2 {
		t.Fatalf("SystemNotices len = %d, want 2: %#v", len(view.SystemNotices), view.SystemNotices)
	}
	if view.SystemNotices[0].Code != "browser_waiting" || !strings.Contains(view.SystemNotices[0].Summary, "browser-task") {
		t.Fatalf("browser notice = %#v", view.SystemNotices[0])
	}
	if view.SystemNotices[1].Code != "engineer_input_waiting" || !strings.Contains(view.SystemNotices[1].Summary, "Codex is waiting") {
		t.Fatalf("question notice = %#v", view.SystemNotices[1])
	}
}

func TestBossBrowserOpenResultIsEchoedIntoBossChat(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	m := Model{
		ctx:       ctx,
		bossMode:  true,
		bossModel: bossui.NewEmbedded(ctx, nil),
	}
	m.bossModel = m.bossModel.WithViewContext(m.bossViewContext())
	updated, cmd := m.update(browserOpenMsg{status: "Managed browser window is ready. Finish the browser flow there."})
	if cmd != nil {
		for _, msg := range collectCmdMsgs(cmd) {
			updated, _ = updated.Update(msg)
		}
	}
	got := updated.(Model)
	view := got.bossModel.View()
	if !strings.Contains(view, "Browser handoff") || !strings.Contains(view, "Finish the browser flow there.") {
		t.Fatalf("boss chat did not echo browser handoff:\n%s", view)
	}
}

func TestBossAttentionAgentTaskJumpOpensTrackedSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newControlTestService(t)
	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title: "Revoke Cursor GitHub access",
		Kind:  model.AgentTaskKindAgent,
	})
	if err != nil {
		t.Fatalf("CreateAgentTask() error = %v", err)
	}
	if _, err := svc.AttachAgentTaskEngineerSession(ctx, task.ID, model.SessionSourceCodex, "thread-agent-1"); err != nil {
		t.Fatalf("AttachAgentTaskEngineerSession() error = %v", err)
	}
	task, err = svc.GetAgentTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetAgentTask() error = %v", err)
	}
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "thread-agent-1",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{
		ctx:          ctx,
		svc:          svc,
		bossMode:     true,
		bossModel:    bossui.NewEmbedded(ctx, svc),
		codexManager: manager,
	}

	updated, cmd := m.openBossAttentionAgentTask(0, task.ID)
	got := updated.(Model)
	if got.bossMode {
		t.Fatalf("bossMode should close when jumping to an agent task")
	}
	if cmd == nil {
		t.Fatalf("openBossAttentionAgentTask() cmd = nil, want launch command")
	}
	_ = collectCmdMsgs(cmd)
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if requests[0].ProjectPath != task.WorkspacePath {
		t.Fatalf("request ProjectPath = %q, want %q", requests[0].ProjectPath, task.WorkspacePath)
	}
	if requests[0].ResumeID != "thread-agent-1" {
		t.Fatalf("request ResumeID = %q, want tracked session", requests[0].ResumeID)
	}
}
