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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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

func TestBossViewContextIncludesActiveAgentTaskEngineerActivity(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	task := model.AgentTask{
		ID:            "agt_demo",
		Title:         "Revoke Cursor GitHub access",
		Status:        model.AgentTaskStatusActive,
		Provider:      model.SessionSourceCodex,
		SessionID:     "thread-agent-1",
		WorkspacePath: "/tmp/agent-task",
	}
	m := Model{
		openAgentTasks: []model.AgentTask{task},
		codexSnapshots: map[string]codexapp.Snapshot{
			task.WorkspacePath: {
				Provider:           codexapp.ProviderCodex,
				ProjectPath:        task.WorkspacePath,
				ThreadID:           task.SessionID,
				Started:            true,
				Busy:               true,
				BusySince:          now.Add(-37 * time.Second),
				ActiveTurnID:       "turn-live",
				LastBusyActivityAt: now.Add(-5 * time.Second),
				LastActivityAt:     now.Add(-5 * time.Second),
			},
		},
	}

	view := m.bossViewContext()
	if len(view.EngineerActivities) != 1 {
		t.Fatalf("EngineerActivities len = %d, want 1: %#v", len(view.EngineerActivities), view.EngineerActivities)
	}
	activity := view.EngineerActivities[0]
	if activity.Kind != "agent_task" || activity.TaskID != task.ID || activity.ProjectPath != task.WorkspacePath || activity.Title != task.Title || activity.Provider != model.SessionSourceCodex || activity.SessionID != task.SessionID {
		t.Fatalf("activity identity = %#v, want task/session identity", activity)
	}
	if !activity.Active || activity.Status != "working" || !activity.StartedAt.Equal(now.Add(-37*time.Second)) {
		t.Fatalf("activity state = %#v, want active working with started time", activity)
	}
	if activity.EngineerName != bossui.EngineerNameForKey("agent_task", task.ID) {
		t.Fatalf("activity engineer name = %q, want deterministic task name", activity.EngineerName)
	}
}

func TestBossViewContextIncludesActiveProjectEngineerActivity(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	project := model.ProjectSummary{Path: "/tmp/project-task", Name: "Project Task"}
	m := Model{
		allProjects: []model.ProjectSummary{project},
		projects:    []model.ProjectSummary{project},
		codexSnapshots: map[string]codexapp.Snapshot{
			project.Path: {
				Provider:           codexapp.ProviderCodex,
				ProjectPath:        project.Path,
				ThreadID:           "thread-project-1",
				Started:            true,
				Busy:               true,
				BusySince:          now.Add(-2 * time.Minute),
				ActiveTurnID:       "turn-live",
				LastBusyActivityAt: now.Add(-5 * time.Second),
				LastActivityAt:     now.Add(-5 * time.Second),
			},
		},
	}

	view := m.bossViewContext()
	if len(view.EngineerActivities) != 1 {
		t.Fatalf("EngineerActivities len = %d, want 1: %#v", len(view.EngineerActivities), view.EngineerActivities)
	}
	activity := view.EngineerActivities[0]
	if activity.Kind != "project" || activity.ProjectPath != project.Path || activity.Title != "Project Task" || activity.Provider != model.SessionSourceCodex || activity.SessionID != "thread-project-1" {
		t.Fatalf("activity identity = %#v, want project session identity", activity)
	}
	if !activity.Active || activity.Status != "working" || !activity.StartedAt.Equal(now.Add(-2*time.Minute)) {
		t.Fatalf("activity state = %#v, want active working with started time", activity)
	}
	if activity.EngineerName != bossui.EngineerNameForKey("project", project.Path, "thread-project-1") {
		t.Fatalf("activity engineer name = %q, want deterministic project session name", activity.EngineerName)
	}
}

func TestBossChatNoticesEngineerTurnCompletion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	projectPath := "/tmp/project-task"
	idleSnapshot := codexapp.Snapshot{
		Provider:       codexapp.ProviderCodex,
		ProjectPath:    projectPath,
		ThreadID:       "thread-project-1",
		Started:        true,
		Status:         "Codex turn completed",
		LastActivityAt: now,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "Killed the stale dev server on port 5173 and left the project clean.\n\nPID 12345 was unrelated diagnostic noise.",
		}},
	}
	session := &fakeCodexSession{projectPath: projectPath, snapshot: idleSnapshot}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: projectPath, Provider: codexapp.ProviderCodex}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	prevSnapshot := idleSnapshot
	prevSnapshot.Busy = true
	prevSnapshot.BusySince = now.Add(-2 * time.Minute)
	prevSnapshot.ActiveTurnID = "turn-live"
	prevSnapshot.Phase = codexapp.SessionPhaseRunning
	m := Model{
		ctx:          ctx,
		bossMode:     true,
		bossModel:    bossui.NewEmbedded(ctx, nil),
		codexManager: manager,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: prevSnapshot,
		},
		allProjects: []model.ProjectSummary{{Path: projectPath, Name: "Project Task"}},
		projects:    []model.ProjectSummary{{Path: projectPath, Name: "Project Task"}},
	}
	m.bossModel = m.bossModel.WithViewContext(m.bossViewContext())

	updated, _ := m.update(codexUpdateMsg{projectPath: projectPath})
	got := updated.(Model)
	view := bossChatOnlyText(got.bossModel)
	noticeText := bossOperationalNoticeText(got.bossModel)
	engineerName := bossui.EngineerNameForKey("project", projectPath, idleSnapshot.ThreadID)
	for _, want := range []string{
		engineerName + " is back from Project Task:",
		"Killed the stale dev server on port 5173",
	} {
		if !bossTextContains(view, want) {
			t.Fatalf("boss chat transcript missing engineer outcome %q:\n%s", want, view)
		}
	}
	for _, want := range []string{
		engineerName + " is back from Project Task: Killed the stale dev server on port 5173",
	} {
		if !strings.Contains(noticeText, want) {
			t.Fatalf("operational notice missing %q:\n%s", want, noticeText)
		}
	}
	if strings.Contains(noticeText, "PID 12345") {
		t.Fatalf("operational notice should keep only the top-level paragraph:\n%s", noticeText)
	}
}

func TestBossChatFetchesFreshEngineerReportBeforeNotice(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	projectPath := "/tmp/chatnext3"
	staleIdleSnapshot := codexapp.Snapshot{
		Provider:       codexapp.ProviderCodex,
		ProjectPath:    projectPath,
		ThreadID:       "thread-project-1",
		Started:        true,
		Status:         "Codex turn completed",
		LastActivityAt: now,
	}
	freshIdleSnapshot := staleIdleSnapshot
	freshIdleSnapshot.Entries = []codexapp.TranscriptEntry{{
		Kind: codexapp.TranscriptAgent,
		Text: "The broken preview is caused by the SVG being written as an HTML error page. I regenerated the SVG and verified the preview opens cleanly.",
	}}
	session := &fakeCodexSession{
		projectPath: projectPath,
		snapshot:    freshIdleSnapshot,
		trySnapshotFn: func(_ *fakeCodexSession) (codexapp.Snapshot, bool) {
			return staleIdleSnapshot, true
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: projectPath, Provider: codexapp.ProviderCodex}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	prevSnapshot := staleIdleSnapshot
	prevSnapshot.Busy = true
	prevSnapshot.BusySince = now.Add(-2 * time.Minute)
	prevSnapshot.ActiveTurnID = "turn-live"
	prevSnapshot.Phase = codexapp.SessionPhaseRunning
	m := Model{
		ctx:          ctx,
		bossMode:     true,
		bossModel:    bossui.NewEmbedded(ctx, nil),
		codexManager: manager,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: prevSnapshot,
		},
		allProjects: []model.ProjectSummary{{Path: projectPath, Name: "ChatNext3"}},
		projects:    []model.ProjectSummary{{Path: projectPath, Name: "ChatNext3"}},
	}
	m.bossModel = m.bossModel.WithViewContext(m.bossViewContext())

	updated, cmd := m.update(codexUpdateMsg{projectPath: projectPath})
	got := updated.(Model)
	for _, msg := range collectCmdMsgs(cmd) {
		updated, _ = got.Update(msg)
		got = updated.(Model)
	}

	view := bossChatOnlyText(got.bossModel)
	noticeText := bossOperationalNoticeText(got.bossModel)
	engineerName := bossui.EngineerNameForKey("project", projectPath, staleIdleSnapshot.ThreadID)
	for _, want := range []string{
		engineerName + " is back from ChatNext3:",
		"The broken preview is caused by the SVG",
	} {
		if !bossTextContains(view, want) {
			t.Fatalf("boss chat transcript missing engineer outcome %q:\n%s", want, view)
		}
	}
	for _, want := range []string{
		engineerName + " is back from ChatNext3: The broken preview is caused by the SVG",
	} {
		if !strings.Contains(noticeText, want) {
			t.Fatalf("fresh operational notice missing %q:\n%s", want, noticeText)
		}
	}
}

func TestLatestEngineerTranscriptOutputDropsCodeBlocks(t *testing.T) {
	t.Parallel()

	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "No stale roguellm dev server is running now. Checked:\n```text\n/Users/davide/dev/repos/roguellm cwd processes: none\nport 8127: no listener\n```\nTerminated: nothing this pass. The server is fully stopped.",
		}},
	}

	got := latestEngineerTranscriptOutput(snapshot)
	if got != "No stale roguellm dev server is running now." {
		t.Fatalf("latestEngineerTranscriptOutput() = %q", got)
	}
	for _, unwanted := range []string{"`", "```", "``text", "/Users/davide", "port 8127", "Terminated:"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("summary leaked %q: %q", unwanted, got)
		}
	}
}

func TestLatestEngineerTranscriptOutputDropsMalformedInlineFence(t *testing.T) {
	t.Parallel()

	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "No stale roguellm dev server is running now. Checked:\n``text /Users/davide/dev/repos/roguellm cwd processes: none port 8127: no listener ``\nThe server is fully stopped.",
		}},
	}

	got := latestEngineerTranscriptOutput(snapshot)
	if got != "No stale roguellm dev server is running now." {
		t.Fatalf("latestEngineerTranscriptOutput() = %q", got)
	}
	if strings.Contains(got, "``") || strings.Contains(got, "/Users/davide") {
		t.Fatalf("summary leaked malformed fence content: %q", got)
	}
}

func TestLatestEngineerTranscriptOutputCleansDanglingColon(t *testing.T) {
	t.Parallel()

	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "Confirmed: the removed imagegen copy was the user-local directory:",
		}},
	}

	got := latestEngineerTranscriptOutput(snapshot)
	if got != "Confirmed: the removed imagegen copy was the user-local directory." {
		t.Fatalf("latestEngineerTranscriptOutput() = %q", got)
	}
}

func TestBossEngineerCompletionLeavesAgentTaskWaitingForDecision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	svc := newControlTestService(t)
	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title: "Kill stale roguellm dev server",
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
	idleSnapshot := codexapp.Snapshot{
		Provider:       codexapp.ProviderCodex,
		ProjectPath:    task.WorkspacePath,
		ThreadID:       "thread-agent-1",
		Started:        true,
		Status:         "Codex turn completed",
		LastActivityAt: now,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "No stale roguellm dev server is running now. Checked:\n```text\nport 8127: no listener\n```\nThe server is fully stopped.",
		}},
	}
	session := &fakeCodexSession{projectPath: task.WorkspacePath, snapshot: idleSnapshot}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: task.WorkspacePath, Provider: codexapp.ProviderCodex}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	prevSnapshot := idleSnapshot
	prevSnapshot.Busy = true
	prevSnapshot.BusySince = now.Add(-2 * time.Minute)
	prevSnapshot.ActiveTurnID = "turn-live"
	prevSnapshot.Phase = codexapp.SessionPhaseRunning
	m := Model{
		ctx:          ctx,
		svc:          svc,
		bossMode:     true,
		bossModel:    bossui.NewEmbedded(ctx, nil),
		codexManager: manager,
		codexSnapshots: map[string]codexapp.Snapshot{
			task.WorkspacePath: prevSnapshot,
		},
		openAgentTasks: []model.AgentTask{task},
	}
	m.bossModel = m.bossModel.WithViewContext(m.bossViewContext())

	updated, cmd := m.update(codexUpdateMsg{projectPath: task.WorkspacePath})
	got := updated.(Model)
	for _, msg := range collectCmdMsgs(cmd) {
		updated, _ = got.Update(msg)
		got = updated.(Model)
	}

	completed, err := svc.GetAgentTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetAgentTask() after completion error = %v", err)
	}
	if completed.Status != model.AgentTaskStatusWaiting {
		t.Fatalf("agent task status = %s, want waiting", completed.Status)
	}
	if _, ok := got.agentTaskForProjectPath(task.WorkspacePath); !ok {
		t.Fatalf("returned agent task should stay open for a close-or-continue decision: %#v", got.openAgentTasks)
	}
	view := bossChatOnlyText(got.bossModel)
	noticeText := bossOperationalNoticeText(got.bossModel)
	engineerName := bossui.EngineerNameForKey("agent_task", task.ID)
	for _, want := range []string{
		engineerName + " is back from Kill stale roguellm dev server:",
		"No stale roguellm dev server is running now.",
	} {
		if !bossTextContains(view, want) {
			t.Fatalf("boss transcript missing engineer outcome %q:\n%s", want, view)
		}
	}
	for _, want := range []string{
		engineerName + " is back from Kill stale roguellm dev server: No stale roguellm dev server is running now.",
	} {
		if !strings.Contains(noticeText, want) {
			t.Fatalf("operational notice missing %q:\n%s", want, noticeText)
		}
	}
	if strings.Contains(noticeText, "Should I close it") {
		t.Fatalf("boss review notice should not append a repeated close-or-continue question:\n%s", noticeText)
	}
	if strings.Contains(noticeText, "port 8127") || strings.Contains(noticeText, "```") {
		t.Fatalf("boss operational notice leaked raw output:\n%s", noticeText)
	}
	if strings.Contains(view, "port 8127") || strings.Contains(view, "```") {
		t.Fatalf("boss transcript leaked raw output:\n%s", view)
	}
}

func TestLatestEngineerTranscriptOutputKeepsConcreteReviewDetails(t *testing.T) {
	t.Parallel()

	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "Compared the user-local imagegen copy with the .system imagegen copy. Kept the .system copy because it had the current metadata and prompt flow. Discarded the user-local copy because it was the stale duplicate.",
		}},
	}

	got := latestEngineerTranscriptReviewOutput(snapshot)
	for _, want := range []string{
		"Compared the user-local imagegen copy",
		"Kept the .system copy",
		"Discarded the user-local copy",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("latestEngineerTranscriptOutput() missing %q:\n%s", want, got)
		}
	}
}

func TestLatestEngineerTranscriptOutputDropsLowInformationDone(t *testing.T) {
	t.Parallel()

	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "Done.",
		}},
	}

	if got := latestEngineerTranscriptOutput(snapshot); got != "" {
		t.Fatalf("latestEngineerTranscriptOutput() = %q, want empty low-information summary", got)
	}
}

func TestLatestEngineerTranscriptOutputSkipsLowInformationIntroAndKeepsArtifactLinks(t *testing.T) {
	t.Parallel()

	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "Done. I kept the existing promo untouched and built the new autoplay version under `captures/promo-new-autoplay`.\n\nBest comparison artifact:\n[side-by-side video](/Users/davide/dev/repos/FractalMech/captures/promo-comparisons/promo-old-vs-new-autoplay-20260505.mp4)\n\nQuick scan:\n[contact sheet](/Users/davide/dev/repos/FractalMech/captures/promo-comparisons/promo-old-vs-new-autoplay-20260505-contact-sheet.jpg)",
		}},
	}

	got := latestEngineerTranscriptOutput(snapshot)
	for _, want := range []string{
		"I kept the existing promo untouched",
		"Outputs:",
		"- side-by-side video: /Users/davide/dev/repos/FractalMech/captures/promo-comparisons/promo-old-vs-new-autoplay-20260505.mp4",
		"- contact sheet: /Users/davide/dev/repos/FractalMech/captures/promo-comparisons/promo-old-vs-new-autoplay-20260505-contact-sheet.jpg",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("latestEngineerTranscriptOutput() missing %q:\n%s", want, got)
		}
	}
	if strings.HasPrefix(got, "Done.") {
		t.Fatalf("latestEngineerTranscriptOutput() should skip low-information opener:\n%s", got)
	}
}

func TestBossHostNoticeQueuedWhileClosedAppearsOnOpen(t *testing.T) {
	t.Parallel()

	m := Model{
		ctx:    context.Background(),
		width:  100,
		height: 30,
	}
	updated, cmd := m.updateBossHostNotice("Ada is back from Cursor cleanup.\n\nCursor access still needs user-side confirmation.")
	if cmd != nil {
		t.Fatalf("updateBossHostNotice() cmd = %T, want nil while Boss Chat is closed", cmd)
	}
	m = updated
	if len(m.pendingBossHostNotices) != 1 {
		t.Fatalf("pending notices = %#v, want one queued notice", m.pendingBossHostNotices)
	}

	reopened, _ := m.openBossMode()
	got := reopened.(Model)
	if len(got.pendingBossHostNotices) != 0 {
		t.Fatalf("pending notices after open = %#v, want drained", got.pendingBossHostNotices)
	}
	view := bossChatOnlyText(got.bossModel)
	noticeText := bossOperationalNoticeText(got.bossModel)
	for _, want := range []string{
		"Ada is back from Cursor cleanup.",
		"Cursor access still needs user-side confirmation.",
	} {
		if strings.Contains(view, want) {
			t.Fatalf("reopened Boss Chat transcript should not contain queued notice %q:\n%s", want, view)
		}
		if !strings.Contains(noticeText, want) {
			t.Fatalf("queued operational notice missing %q:\n%s", want, noticeText)
		}
	}
}

func TestBossHostChatNoticeQueuedWhileClosedAppearsInTranscriptOnOpen(t *testing.T) {
	t.Parallel()

	m := Model{
		ctx:    context.Background(),
		width:  100,
		height: 30,
	}
	updated, cmd := m.updateBossHostChatNotice("Ken is back from ChatNext3.\n\nNo migration needed; DB/schema stayed untouched.")
	if cmd != nil {
		t.Fatalf("updateBossHostChatNotice() cmd = %T, want nil while Boss Chat is closed", cmd)
	}
	m = updated
	if len(m.pendingBossHostNotices) != 1 {
		t.Fatalf("pending notices = %#v, want one queued notice", m.pendingBossHostNotices)
	}

	reopened, _ := m.openBossMode()
	got := reopened.(Model)
	view := bossChatOnlyText(got.bossModel)
	noticeText := bossOperationalNoticeText(got.bossModel)
	for _, want := range []string{
		"Ken is back from ChatNext3.",
		"No migration needed; DB/schema stayed untouched.",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("reopened Boss Chat transcript missing queued chat notice %q:\n%s", want, view)
		}
		if !strings.Contains(noticeText, want) {
			t.Fatalf("queued operational notice missing %q:\n%s", want, noticeText)
		}
	}
}

func TestBossChatReplyContinuesAfterBossModeHidden(t *testing.T) {
	t.Parallel()

	svc := newControlTestService(t)
	m := Model{
		ctx:    context.Background(),
		svc:    svc,
		width:  100,
		height: 30,
	}
	opened, initCmd := m.openBossMode()
	got := opened.(Model)
	for _, msg := range collectCmdMsgs(initCmd) {
		if _, ok := msg.(bossui.TickMsg); ok {
			continue
		}
		updated, _ := got.Update(msg)
		got = updated.(Model)
	}
	if !got.bossModelActive {
		t.Fatalf("boss model should be active after opening")
	}

	updated, _ := got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("answer this while hidden")})
	got = updated.(Model)
	updated, chatCmd := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if chatCmd == nil {
		t.Fatalf("submitting boss chat should start async work")
	}

	updated, exitCmd := got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got = updated.(Model)
	if exitCmd == nil {
		t.Fatalf("Esc should hide boss mode through an exit message")
	}
	for _, msg := range collectCmdMsgs(exitCmd) {
		updated, _ = got.Update(msg)
		got = updated.(Model)
	}
	if got.bossMode {
		t.Fatalf("boss mode should be hidden")
	}
	if !got.bossModelActive {
		t.Fatalf("hiding boss mode should keep the boss model alive")
	}

	got = drainCmdMsgs(got, chatCmd)
	if got.bossMode {
		t.Fatalf("background boss reply should not reopen boss mode")
	}

	got.bossModel = bossui.Model{}
	got.bossModelActive = false
	reopened, initCmd := got.openBossMode()
	got = reopened.(Model)
	for _, msg := range collectCmdMsgs(initCmd) {
		if _, ok := msg.(bossui.TickMsg); ok {
			continue
		}
		updated, _ := got.Update(msg)
		got = updated.(Model)
	}
	view := bossChatOnlyText(got.bossModel)
	for _, want := range []string{
		"answer this while hidden",
		"I could not reach my chat backend yet",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("hidden boss reply did not finish with %q:\n%s", want, view)
		}
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

func TestBossViewContextIncludesCPUHotSystemNotice(t *testing.T) {
	t.Parallel()

	m := Model{
		cpuSnapshot: procinspect.CPUSnapshot{
			TotalCPU:  155.2,
			ScannedAt: time.Date(2026, 4, 3, 11, 0, 0, 0, time.UTC),
			Processes: []procinspect.CPUProcess{{
				Process: procinspect.Process{PID: 42, CPU: 91.4, Command: "/usr/local/bin/node dev.js"},
			}},
		},
	}

	view := m.bossViewContext()
	if len(view.SystemNotices) != 1 {
		t.Fatalf("SystemNotices len = %d, want 1", len(view.SystemNotices))
	}
	notice := view.SystemNotices[0]
	if notice.Code != "cpu_hot" || notice.Severity != "warning" || notice.Count != 1 {
		t.Fatalf("notice = %#v, want CPU warning", notice)
	}
	if !strings.Contains(notice.Summary, "CPU monitor") || !strings.Contains(notice.Summary, "PID 42") || !strings.Contains(notice.Summary, "/cpu") {
		t.Fatalf("notice summary = %q, want CPU details and /cpu guidance", notice.Summary)
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

func TestBossBrowserOpenResultIsRecordedAsOperationalNotice(t *testing.T) {
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
	view := bossChatOnlyText(got.bossModel)
	noticeText := bossOperationalNoticeText(got.bossModel)
	if strings.Contains(view, "Browser handoff") || strings.Contains(view, "Finish the browser flow there.") {
		t.Fatalf("boss chat transcript should not echo browser handoff:\n%s", view)
	}
	if !strings.Contains(noticeText, "Browser handoff") || !strings.Contains(noticeText, "Finish the browser flow there.") {
		t.Fatalf("operational notice did not capture browser handoff:\n%s", noticeText)
	}
}

func bossChatPanelText(view string) string {
	cutAt := len(view)
	for _, marker := range []string{"Boss Desk", "Boss Log"} {
		if index := strings.Index(view, marker); index >= 0 && index < cutAt {
			cutAt = index
		}
	}
	if cutAt < len(view) {
		return view[:cutAt]
	}
	return view
}

func bossChatOnlyText(model bossui.Model) string {
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 70, Height: 28})
	return bossChatPanelText(normalizeBossModel(updated).View())
}

func bossOperationalNoticeText(model bossui.Model) string {
	notices := model.OperationalNotices()
	parts := make([]string, 0, len(notices))
	for _, notice := range notices {
		parts = append(parts, notice.Summary)
	}
	return strings.Join(parts, "\n")
}

func bossTextContains(haystack, needle string) bool {
	normalize := func(text string) string {
		text = ansi.Strip(text)
		text = strings.NewReplacer("│", " ", "╭", " ", "╮", " ", "╰", " ", "╯", " ", "─", " ").Replace(text)
		return strings.Join(strings.Fields(text), " ")
	}
	normalizedHaystack := normalize(haystack)
	normalizedNeedle := normalize(needle)
	return strings.Contains(normalizedHaystack, normalizedNeedle)
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
