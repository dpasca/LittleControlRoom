package boss

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestModelViewRendersBossPanels(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.width = 100
	m.height = 30
	m.stateLoaded = true
	m.snapshot = StateSnapshot{
		TotalProjects:  1,
		ActiveProjects: 1,
		HotProjects: []ProjectBrief{{
			Name:           "Alpha",
			Status:         model.StatusActive,
			AttentionScore: 12,
		}},
	}
	m.syncLayout(true)

	view := m.View()
	for _, want := range []string{"Boss Chat", "Boss Desk", "Watching", "Next", "Alpha"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	for _, legacy := range []string{"Jump", "Situation", "Notes", "Little Room", "On My Desk", "Notebook"} {
		if strings.Contains(view, legacy) {
			t.Fatalf("view still contains themed panel %q:\n%s", legacy, view)
		}
	}
	if strings.Contains(ansi.Strip(view), "Ask what needs attention") {
		t.Fatalf("view should not render the old input placeholder:\n%s", view)
	}
	for _, unwanted := range []string{"Ask what deserves attention", "I will keep a compact view"} {
		if strings.Contains(ansi.Strip(view), unwanted) {
			t.Fatalf("view should not render the default assistant greeting %q:\n%s", unwanted, view)
		}
	}
	stripped := ansi.Strip(view)
	for _, want := range []string{"Alt+Enter newline", "Alt+Up exits"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("view missing boss shortcut %q:\n%s", want, stripped)
		}
	}
	if strings.Contains(stripped, "Ctrl+J newline") {
		t.Fatalf("view should advertise Alt+Enter instead of Ctrl+J:\n%s", stripped)
	}
}

func TestPageKeysScrollChatByEightyPercent(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.chatViewport.Width = 80
	m.chatViewport.Height = 10
	m.chatViewport.SetContent(bossTestViewportLines(40))

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if cmd != nil {
		t.Fatalf("Page Down should not return a command")
	}
	got := updated.(Model)
	if got.chatViewport.YOffset != 8 {
		t.Fatalf("Page Down offset = %d, want 8", got.chatViewport.YOffset)
	}

	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if cmd != nil {
		t.Fatalf("Page Up should not return a command")
	}
	got = updated.(Model)
	if got.chatViewport.YOffset != 0 {
		t.Fatalf("Page Up offset = %d, want 0", got.chatViewport.YOffset)
	}
}

func bossTestViewportLines(count int) string {
	lines := make([]string, count)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	return strings.Join(lines, "\n")
}

func TestModelAttentionRowsUseCompactProjectColumns(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	now := time.Unix(1_800_000_000, 0)
	m.nowFn = func() time.Time { return now }
	m.summaryFlashUntil = map[string]time.Time{"/alpha": now.Add(time.Second)}
	m.snapshot = StateSnapshot{
		HotProjects: []ProjectBrief{{
			Name:                 "Alpha",
			Path:                 "/alpha",
			Status:               model.StatusActive,
			RepoDirty:            true,
			LatestSummary:        "Needs review before the handoff.",
			LatestCategory:       model.SessionCategoryWaitingForUser,
			ClassificationStatus: model.ClassificationCompleted,
		}},
	}

	rendered := m.renderAttentionRows(84, 1)
	stripped := ansi.Strip(rendered)
	for _, want := range []string{"Alt+1", "!", "waiting", "Alpha", "Needs review before the handoff."} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("attention row missing %q:\n%s", want, stripped)
		}
	}
	if !m.summaryFlashActive("/alpha") {
		t.Fatalf("updated project summary should be inside the flash window:\n%s", rendered)
	}
	project := m.snapshot.HotProjects[0]
	if got, want := bossSummaryStyle(project).GetForeground(), bossSummaryTextStyle.GetForeground(); got != want {
		t.Fatalf("summary foreground = %v, want neutral text foreground %v", got, want)
	}
	if got, unwanted := bossSummaryStyle(project).GetForeground(), bossAssessmentWaitingStyle.GetForeground(); got == unwanted {
		t.Fatalf("summary should not inherit the assessment foreground %v", got)
	}
}

func TestModelAttentionRowsIncludeOpenAgentTasks(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.snapshot = StateSnapshot{
		OpenAgentTasks: []AgentTaskBrief{{
			ID:        "agt_demo",
			Title:     "Revoke Cursor GitHub access",
			Status:    model.AgentTaskStatusActive,
			Provider:  model.SessionSourceCodex,
			SessionID: "thread-agent-1",
		}},
		HotProjects: []ProjectBrief{{
			Name:          "Alpha",
			Path:          "/alpha",
			Status:        model.StatusActive,
			LatestSummary: "Needs review before handoff.",
		}},
	}

	rendered := m.renderAttentionRows(90, 2)
	stripped := ansi.Strip(rendered)
	for _, want := range []string{"Alt+1", "T", "working", "Revoke Cursor GitHub", "Alt+2", "Alpha"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("attention rows missing %q:\n%s", want, stripped)
		}
	}
	if got := m.HotAttentionItem(0); got.Kind != AttentionItemAgentTask || got.TaskID != "agt_demo" {
		t.Fatalf("first attention item = %#v, want agent task", got)
	}
	if got := m.HotAttentionItem(1); got.Kind != AttentionItemProject || got.ProjectPath != "/alpha" {
		t.Fatalf("second attention item = %#v, want project", got)
	}
}

func TestModelAttentionRowsShowActiveAgentTaskTimer(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := New(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m.viewContext = ViewContext{
		EngineerActivities: []ViewEngineerActivity{{
			Kind:         "agent_task",
			TaskID:       "agt_demo",
			ProjectPath:  "/tmp/agent-task",
			Title:        "Revoke Cursor GitHub access",
			EngineerName: "Ada",
			Provider:     model.SessionSourceCodex,
			SessionID:    "thread-agent-1",
			Status:       "working",
			Active:       true,
			StartedAt:    now.Add(-37 * time.Second),
		}},
	}
	m.snapshot = StateSnapshot{
		OpenAgentTasks: []AgentTaskBrief{{
			ID:        "agt_demo",
			Title:     "Revoke Cursor GitHub access",
			Status:    model.AgentTaskStatusActive,
			Provider:  model.SessionSourceCodex,
			SessionID: "thread-agent-1",
		}},
	}

	rendered := m.renderAttentionRows(90, 1)
	stripped := ansi.Strip(rendered)
	for _, want := range []string{"Alt+1", "00:37", "working 00:37", "Ada working 00:37", "Revoke Cursor GitHub"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("active agent attention row missing %q:\n%s", want, stripped)
		}
	}
	desk := ansi.Strip(m.deskContent(90, 12))
	for _, want := range []string{"Now", "00:37", "Ada on Revoke Cursor GitHub access"} {
		if !strings.Contains(desk, want) {
			t.Fatalf("active agent desk status missing %q:\n%s", want, desk)
		}
	}
	transcript := ansi.Strip(m.renderTranscript(90))
	if strings.Contains(transcript, "Ada is working on Revoke Cursor GitHub access") || strings.Contains(transcript, "Supervisor") {
		t.Fatalf("active agent status should stay out of transcript:\n%s", transcript)
	}
}

func TestBossTickRefreshesDeskTimer(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := NewEmbeddedWithViewContext(context.Background(), nil, ViewContext{
		Active: true,
		EngineerActivities: []ViewEngineerActivity{{
			Kind:         "agent_task",
			TaskID:       "agt_demo",
			Title:        "Cursor cleanup",
			EngineerName: "Grace",
			Status:       "working",
			Active:       true,
			StartedAt:    now.Add(-3 * time.Second),
		}},
	})
	m.width = 96
	m.height = 24
	m.nowFn = func() time.Time { return now }
	m.syncLayout(true)
	initialDesk := ansi.Strip(m.deskContent(48, 12))
	if !strings.Contains(initialDesk, "00:03") {
		t.Fatalf("initial desk block missing timer:\n%s", initialDesk)
	}

	now = now.Add(time.Second)
	updated, _ := m.Update(TickMsg(now))
	got := updated.(Model)
	rendered := ansi.Strip(got.deskContent(48, 12))
	if !strings.Contains(rendered, "00:04") || strings.Contains(rendered, "00:03") {
		t.Fatalf("desk timer did not refresh on tick:\n%s", rendered)
	}
}

func TestModelSupervisorDoesNotAppendReviewAgentTasksAfterTranscript(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := New(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m.messages = []ChatMessage{
		{Role: "assistant", Content: "Dennis is back from Diff duplicate Codex skills.\n\nCurrent state: there are no longer two live imagegen copies.", At: now.Add(-time.Minute)},
		{Role: "user", Content: "the engineer has no memory?", At: now},
	}
	m.snapshot = StateSnapshot{
		OpenAgentTasks: []AgentTaskBrief{{
			ID:            "agt_diff",
			Title:         "Diff duplicate Codex skills",
			EngineerName:  "Dennis",
			Status:        model.AgentTaskStatusWaiting,
			Summary:       "Current state: there are no longer two live imagegen copies.",
			LastTouchedAt: now.Add(-time.Minute),
		}},
	}

	rendered := ansi.Strip(m.renderTranscript(180))
	for _, want := range []string{"Boss> Dennis is back from Diff duplicate Codex skills.", "You> the engineer has no memory?"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("transcript missing saved chat turn %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Dennis finished Diff duplicate Codex skills") {
		t.Fatalf("review task should not be regenerated as a sticky transcript footer:\n%s", rendered)
	}
	if strings.Contains(rendered, "Should I close it, or send Dennis back in?") {
		t.Fatalf("review decision should not be appended after the saved Boss message:\n%s", rendered)
	}
}

func TestModelSupervisorMarksQuietEngineerActivity(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := New(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m.viewContext = ViewContext{
		EngineerActivities: []ViewEngineerActivity{{
			Kind:         "project",
			ProjectPath:  "/alpha",
			Title:        "Alpha",
			EngineerName: "Tem",
			Provider:     model.SessionSourceCodex,
			Status:       "working",
			Active:       true,
			StartedAt:    now.Add(-30 * time.Minute),
			LastEventAt:  now.Add(-11 * time.Minute),
		}},
	}

	rendered := ansi.Strip(m.deskContent(90, 12))
	for _, want := range []string{"Now", "quiet", "Tem has gone quiet on Alpha for 11:00"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("boss desk quiet block missing %q:\n%s", want, rendered)
		}
	}
}

func TestBossSupervisorStateRefreshCadenceRequiresStore(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "little-control-room.sqlite")
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	svc := service.New(cfg, st, events.NewBus(), nil)

	m := New(context.Background(), svc)
	m.spinnerFrame = bossSupervisorStateRefreshEveryTicks
	if !m.shouldRefreshSupervisorState() {
		t.Fatalf("shouldRefreshSupervisorState() = false, want refresh at cadence")
	}
	m.spinnerFrame = bossSupervisorStateRefreshEveryTicks - 1
	if m.shouldRefreshSupervisorState() {
		t.Fatalf("shouldRefreshSupervisorState() = true before cadence")
	}
	m.svc = nil
	m.spinnerFrame = bossSupervisorStateRefreshEveryTicks
	if m.shouldRefreshSupervisorState() {
		t.Fatalf("shouldRefreshSupervisorState() = true without service")
	}
}

func TestModelAttentionRowsShowActiveProjectEngineerTimer(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := New(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m.viewContext = ViewContext{
		EngineerActivities: []ViewEngineerActivity{{
			Kind:         "project",
			ProjectPath:  "/alpha",
			Title:        "Alpha",
			EngineerName: "Hedy",
			Provider:     model.SessionSourceCodex,
			SessionID:    "thread-project-1",
			Status:       "working",
			Active:       true,
			StartedAt:    now.Add(-2 * time.Minute),
		}},
	}
	m.snapshot = StateSnapshot{
		HotProjects: []ProjectBrief{{
			Name:          "Alpha",
			Path:          "/alpha",
			Status:        model.StatusActive,
			LatestSummary: "Needs review before handoff.",
		}},
	}

	rendered := m.renderAttentionRows(90, 1)
	stripped := ansi.Strip(rendered)
	for _, want := range []string{"Alt+1", "02:00", "working 02:00", "Hedy working 02:00", "Alpha"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("active project attention row missing %q:\n%s", want, stripped)
		}
	}
}

func TestControlResultSummarizesAgentTaskHandoff(t *testing.T) {
	t.Parallel()

	args, err := json.Marshal(control.AgentTaskCreateInput{
		Title:  "Revoke Cursor GitHub access",
		Prompt: "Open GitHub settings and revoke Cursor's OAuth access.",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	content := controlResultContent(ControlInvocationResultMsg{
		Invocation: control.Invocation{
			Capability: control.CapabilityAgentTaskCreate,
			Args:       args,
		},
		Status: "Ok, Ada is working on Revoke Cursor GitHub access.",
	})

	if content != "Ok, Ada is working on Revoke Cursor GitHub access." {
		t.Fatalf("control result = %q", content)
	}
	for _, unwanted := range []string{
		"Sent to the engineer:",
		"Open GitHub settings and revoke Cursor's OAuth access.",
		"The task is linked",
	} {
		if strings.Contains(content, unwanted) {
			t.Fatalf("control result leaked %q:\n%s", unwanted, content)
		}
	}
}

func TestControlResultIsContextNotBossChatTurn(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := NewEmbedded(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m.messages = []ChatMessage{{Role: "user", Content: "what's the situation with the skills?", At: now}}

	updated, cmd := m.Update(ControlInvocationResultMsg{
		Status: "Agent task agt_20260502T230818_4c3c890b46 is now completed",
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("control result without service should not emit a command, got %T", cmd)
	}
	if len(got.messages) != 1 {
		t.Fatalf("control result should not append a boss chat turn, got %#v", got.messages)
	}
	if strings.Contains(got.renderTranscript(120), "Agent task agt_20260502T230818_4c3c890b46") {
		t.Fatalf("control result leaked into transcript:\n%s", got.renderTranscript(120))
	}
	brief := BuildViewContextBrief(got.assistantViewContext(), now)
	if !strings.Contains(brief, "control_completed") || !strings.Contains(brief, "Agent task agt_20260502T230818_4c3c890b46") {
		t.Fatalf("control result should remain available as context:\n%s", brief)
	}
}

func TestControlResultRendersTransientActiveEngineerFeedback(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := NewEmbedded(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m.messages = []ChatMessage{{Role: "user", Content: "just nuke that skill", At: now}}
	activity := ViewEngineerActivity{
		Kind:         "agent_task",
		TaskID:       "agt_skill_cleanup",
		Title:        "Retire projects-control-center skill",
		EngineerName: "Niklaus",
		Provider:     model.SessionSourceCodex,
		SessionID:    "thread-skill-cleanup",
		Status:       "working",
		Active:       true,
		StartedAt:    now.Add(-3 * time.Second),
		LastEventAt:  now.Add(-3 * time.Second),
	}

	updated, _ := m.Update(ControlInvocationResultMsg{
		Status:   "Ok, Niklaus is working on Retire projects-control-center skill.",
		Activity: &activity,
	})
	got := updated.(Model)
	if len(got.messages) != 1 {
		t.Fatalf("control result should not append a boss chat turn, got %#v", got.messages)
	}
	rendered := ansi.Strip(got.renderTranscript(120))
	for _, want := range []string{"You> just nuke that skill"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("transcript missing saved turn %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Niklaus is working on Retire projects-control-center skill") {
		t.Fatalf("transient engineer feedback should stay out of transcript:\n%s", rendered)
	}
	desk := ansi.Strip(got.deskContent(90, 12))
	for _, want := range []string{"Now", "00:03", "Niklaus on Retire projects-control-center skill"} {
		if !strings.Contains(desk, want) {
			t.Fatalf("transient engineer feedback missing from desk %q:\n%s", want, desk)
		}
	}
	for _, want := range []string{"Recent", "Niklaus started Retire projects-control-center skill"} {
		if !strings.Contains(desk, want) {
			t.Fatalf("transient engineer event missing from desk %q:\n%s", want, desk)
		}
	}
}

func TestBossDeskRecentShowsHostAndStateEvents(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := New(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m.appendDeskEvent("host", "update", "Ken finished ChatNext3: fixed SVG serving issue.")
	m.snapshot = StateSnapshot{
		OpenAgentTasks: []AgentTaskBrief{{
			ID:            "agt_diff",
			Title:         "Diff duplicate Codex skills",
			Status:        model.AgentTaskStatusCompleted,
			Summary:       "Canonical copy kept.",
			LastTouchedAt: now.Add(-2 * time.Minute),
		}},
		HotProjects: []ProjectBrief{{
			Name:                 "ChatNext3",
			Path:                 "/tmp/chatnext3",
			Status:               model.StatusActive,
			LastActivity:         now.Add(-5 * time.Minute),
			LatestSummary:        "SVG preview repaired.",
			LatestCategory:       model.SessionCategoryCompleted,
			ClassificationStatus: model.ClassificationCompleted,
		}},
	}

	desk := ansi.Strip(m.deskContent(120, 14))
	for _, want := range []string{
		"Recent",
		"Ken finished ChatNext3: fixed SVG serving issue.",
		"Diff duplicate Codex skills - Canonical copy kept.",
		"ChatNext3 - SVG preview repaired.",
	} {
		if !strings.Contains(desk, want) {
			t.Fatalf("boss desk recent missing %q:\n%s", want, desk)
		}
	}
}

func TestModelSummaryFlashTracksUpdatedProjectSummaries(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := New(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m.snapshot = StateSnapshot{HotProjects: []ProjectBrief{{
		Name:          "Alpha",
		Path:          "/alpha",
		LatestSummary: "old summary",
	}}}

	m.syncSummaryFlashes(StateSnapshot{HotProjects: []ProjectBrief{{
		Name:          "Alpha",
		Path:          "/alpha",
		LatestSummary: "new summary",
	}}})
	if !m.summaryFlashActive("/alpha") {
		t.Fatalf("summary update should start a flash window")
	}

	m.nowFn = func() time.Time { return now.Add(summaryFlashDuration + time.Millisecond) }
	m.pruneSummaryFlashes()
	if m.summaryFlashActive("/alpha") {
		t.Fatalf("summary flash should expire after the flash duration")
	}
}

func TestModelSummaryFlashIgnoresAssessmentMetadataWhenSummaryDoesNotChange(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := New(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m.snapshot = StateSnapshot{HotProjects: []ProjectBrief{{
		Name:                 "Alpha",
		Path:                 "/alpha",
		LatestSummary:        "same summary",
		LatestCategory:       model.SessionCategoryWaitingForUser,
		ClassificationStatus: model.ClassificationCompleted,
	}}}

	m.syncSummaryFlashes(StateSnapshot{HotProjects: []ProjectBrief{{
		Name:                 "Alpha",
		Path:                 "/alpha",
		LatestSummary:        "same summary",
		LatestCategory:       model.SessionCategoryNeedsFollowUp,
		ClassificationStatus: model.ClassificationCompleted,
	}}})
	if m.summaryFlashActive("/alpha") {
		t.Fatalf("summary flash should not start when only assessment metadata changes")
	}
}

func TestAskAssistantRefreshesStateBeforeReply(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/fresh",
		Name:           "Fresh",
		Status:         model.StatusActive,
		AttentionScore: 80,
		LastActivity:   now,
		PresentOnDisk:  true,
		InScope:        true,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	svc := service.New(cfg, st, events.NewBus(), nil)
	runner := &fakeTextRunner{}
	m := New(ctx, svc)
	m.assistant = &Assistant{runner: runner, model: "gpt-test"}
	m.snapshot = StateSnapshot{
		TotalProjects: 1,
		HotProjects: []ProjectBrief{{
			Name: "Old",
		}},
	}

	cmd := m.askAssistantCmd([]ChatMessage{{Role: "user", Content: "any changes?"}}, m.snapshot, ViewContext{})
	msg := cmd()
	reply, ok := msg.(AssistantReplyMsg)
	if !ok {
		t.Fatalf("askAssistantCmd() returned %T, want AssistantReplyMsg", msg)
	}
	if reply.stateErr != nil {
		t.Fatalf("state refresh error = %v", reply.stateErr)
	}
	if !reply.stateRefreshed {
		t.Fatalf("expected assistant command to refresh state before replying")
	}

	prompt := runner.req.Messages[0].Content
	if !strings.Contains(prompt, "Fresh") {
		t.Fatalf("assistant prompt missing refreshed project:\n%s", prompt)
	}
	if strings.Contains(prompt, "Old") {
		t.Fatalf("assistant prompt used stale project snapshot:\n%s", prompt)
	}
}

func TestModelInputAcceptsTypingImmediately(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
	got := updated.(Model)
	if got.input.Value() != "hi" {
		t.Fatalf("input value = %q, want typed text", got.input.Value())
	}
}

func TestModelQTypesIntoInput(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	got := updated.(Model)
	if got.input.Value() != "q" {
		t.Fatalf("input value = %q, want q to type into the chat input", got.input.Value())
	}
}

func TestModelAltEnterInsertsNewline(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line 1")})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("alt+enter should not submit")
	}
	if got.input.Value() != "line 1\n" {
		t.Fatalf("input value = %q, want trailing newline", got.input.Value())
	}
	if len(got.messages) != 0 {
		t.Fatalf("messages len = %d, want no submitted messages", len(got.messages))
	}

	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("ctrl+j compatibility newline should not submit")
	}
	if got.input.Value() != "line 1\n\n" {
		t.Fatalf("input value = %q, want second trailing newline", got.input.Value())
	}
}

func TestModelAltCOpensDialogAndCopiesFullMultilineInput(t *testing.T) {
	var copied string
	previousWriter := clipboardTextWriter
	clipboardTextWriter = func(text string) error {
		copied = text
		return nil
	}
	t.Cleanup(func() {
		clipboardTextWriter = previousWriter
	})

	m := New(context.Background(), nil)
	m.input.SetHeight(3)
	m.input.SetValue("line 1\nline 2\nline 3\nline 4")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}, Alt: true})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("alt+c dialog should not queue a command")
	}
	if got.inputCopyDialog == nil {
		t.Fatalf("alt+c should open the input copy dialog")
	}
	if copied != "" {
		t.Fatalf("alt+c should not copy before a dialog choice, copied %q", copied)
	}

	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("copy all should not queue a command")
	}
	if copied != m.input.Value() {
		t.Fatalf("copied = %q, want full boss input %q", copied, m.input.Value())
	}
	if got.inputCopyDialog != nil {
		t.Fatalf("copy dialog should close after choosing copy all")
	}
	if got.input.Value() != m.input.Value() {
		t.Fatalf("input changed to %q, want %q", got.input.Value(), m.input.Value())
	}
	if got.status != "Copied full boss chat input to clipboard" {
		t.Fatalf("status = %q, want copy confirmation", got.status)
	}
}

func TestModelAltCDialogCanStartInputSelection(t *testing.T) {
	m := New(context.Background(), nil)
	m.input.SetHeight(3)
	m.input.SetValue("line 1\nline 2")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}, Alt: true})
	got := updated.(Model)
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	updated, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("starting selection should not queue a command")
	}
	if got.inputCopyDialog != nil {
		t.Fatalf("copy dialog should close after choosing selection")
	}
	if got.inputSelection == nil {
		t.Fatalf("selection mode should be active")
	}
	if got.status != "Selection mode: move to the start and press Space" {
		t.Fatalf("status = %q, want selection instructions", got.status)
	}
}

func TestModelAltCDialogCanCopyVisibleOutput(t *testing.T) {
	var copied string
	previousWriter := clipboardTextWriter
	clipboardTextWriter = func(text string) error {
		copied = text
		return nil
	}
	t.Cleanup(func() {
		clipboardTextWriter = previousWriter
	})

	m := New(context.Background(), nil)
	m.chatViewport.Width = 80
	m.chatViewport.Height = 2
	m.chatViewport.SetContent("older\nvisible one\nvisible two\nnewer")
	m.chatViewport.SetYOffset(1)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}, Alt: true})
	got := updated.(Model)
	updated, cmd := got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("copy visible output should not queue a command")
	}
	if copied != "visible one\nvisible two" {
		t.Fatalf("copied visible output = %q", copied)
	}
	if got.inputCopyDialog != nil {
		t.Fatalf("copy dialog should close after copying output")
	}
	if got.status != "Copied visible output to clipboard" {
		t.Fatalf("status = %q, want output copy confirmation", got.status)
	}
}

func TestEmbeddedModelConfirmsControlInvocation(t *testing.T) {
	t.Parallel()

	inv := bossControlInvocationForTest(t)
	m := NewEmbedded(context.Background(), nil)

	updated, cmd := m.Update(AssistantReplyMsg{
		response: AssistantResponse{
			Content:           "Send this to OpenCode?",
			ControlInvocation: &inv,
		},
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("assistant reply should only update local confirmation state")
	}
	if got.pendingControl == nil {
		t.Fatalf("pendingControl = nil, want confirmation state")
	}
	if got.status != "Confirm control action with Enter, or Esc to cancel" {
		t.Fatalf("status = %q", got.status)
	}

	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.pendingControl != nil {
		t.Fatalf("pendingControl should clear after confirmation")
	}
	if cmd == nil {
		t.Fatalf("confirmation should emit a host command")
	}
	msg := cmd()
	confirmed, ok := msg.(ControlInvocationConfirmedMsg)
	if !ok {
		t.Fatalf("confirmation command returned %T, want ControlInvocationConfirmedMsg", msg)
	}
	if confirmed.Invocation.Capability != control.CapabilityEngineerSendPrompt {
		t.Fatalf("confirmed capability = %q", confirmed.Invocation.Capability)
	}
}

func TestEmbeddedModelLabelsControlProposalErrors(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)

	updated, _ := m.Update(AssistantReplyMsg{
		err: wrapControlProposalError(fmt.Errorf("project_path or project_name is required")),
	})
	got := updated.(Model)
	if got.status != "Control action proposal failed" {
		t.Fatalf("status = %q, want proposal failure status", got.status)
	}
	if len(got.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(got.messages))
	}
	content := got.messages[0].Content
	if strings.Contains(content, "chat backend") {
		t.Fatalf("content = %q, should not report backend failure", content)
	}
	if !strings.Contains(content, "I could not prepare that control action") ||
		!strings.Contains(content, "project_path or project_name is required") {
		t.Fatalf("content = %q, want proposal failure detail", content)
	}
}

func TestEmbeddedModelRendersControlConfirmationDialog(t *testing.T) {
	t.Parallel()

	inv := bossControlInvocationForTest(t)
	m := NewEmbedded(context.Background(), nil)
	m.width = 96
	m.height = 28
	updated, _ := m.Update(AssistantReplyMsg{
		response: AssistantResponse{
			Content:           "Send this to OpenCode?",
			ControlInvocation: &inv,
		},
	})
	got := updated.(Model)

	rendered := ansi.Strip(got.View())
	if !strings.Contains(rendered, "Confirm Control Action") {
		t.Fatalf("rendered view should show control confirmation dialog, got %q", rendered)
	}
	if !strings.Contains(rendered, "External action") || !strings.Contains(rendered, "Enter") || !strings.Contains(rendered, "Esc") {
		t.Fatalf("rendered dialog should show action framing and keys, got %q", rendered)
	}
	if !strings.Contains(rendered, "Please fix the failing tests.") {
		t.Fatalf("rendered dialog should show prompt, got %q", rendered)
	}
	if len(got.messages) != 0 {
		t.Fatalf("control proposal preview should not be saved as normal chat, got %#v", got.messages)
	}
}

func TestEmbeddedModelCanCancelControlInvocation(t *testing.T) {
	t.Parallel()

	inv := bossControlInvocationForTest(t)
	m := NewEmbedded(context.Background(), nil)
	updated, _ := m.Update(AssistantReplyMsg{
		response: AssistantResponse{
			Content:           "Send this to OpenCode?",
			ControlInvocation: &inv,
		},
	})
	got := updated.(Model)

	updated, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got = updated.(Model)
	if got.pendingControl != nil {
		t.Fatalf("pendingControl should clear after cancel")
	}
	if cmd != nil {
		t.Fatalf("cancel should not emit a host command without persistent sessions")
	}
	if got.status != "Control action canceled" {
		t.Fatalf("status = %q", got.status)
	}
	if len(got.messages) != 0 {
		t.Fatalf("cancel should not append a boss chat turn, got %#v", got.messages)
	}
	if len(got.operationalNotices) != 1 || got.operationalNotices[0].Code != "control_canceled" {
		t.Fatalf("cancel notice = %#v, want one operational cancellation notice", got.operationalNotices)
	}
}

func TestEmbeddedModelAltUpExits(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	if _, ok := updated.(Model); !ok {
		t.Fatalf("Update() returned %T, want boss.Model", updated)
	}
	if cmd == nil {
		t.Fatalf("alt+up should return exit command")
	}
	msg := cmd()
	if _, ok := msg.(ExitMsg); !ok {
		t.Fatalf("alt+up command returned %T, want boss.ExitMsg", msg)
	}
}

func bossControlInvocationForTest(t *testing.T) control.Invocation {
	t.Helper()
	args, err := json.Marshal(control.EngineerSendPromptInput{
		ProjectPath: "/tmp/alpha",
		Provider:    control.ProviderOpenCode,
		SessionMode: control.SessionModeResumeOrNew,
		Prompt:      "Please fix the failing tests.",
		Reveal:      false,
	})
	if err != nil {
		t.Fatalf("marshal control input: %v", err)
	}
	return control.Invocation{
		Capability: control.CapabilityEngineerSendPrompt,
		Args:       args,
	}
}

func TestEmbeddedModelRendersBodyForHostShell(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 160
	m.height = 42
	m.stateLoaded = true
	m.syncLayout(true)

	rendered := ansi.Strip(m.View())
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "╭") {
		t.Fatalf("embedded boss body should start directly with frames for the host shell:\n%s", rendered)
	}
	if lineCount := len(lines); lineCount > m.height {
		layout := m.layout()
		t.Fatalf("embedded boss view rendered %d lines, want at most %d; layout=%+v chat=%d attention=%d",
			lineCount,
			m.height,
			layout,
			renderedLineCount(m.renderChat(layout)),
			renderedLineCount(m.renderAttention(layout.attentionWidth, layout.bottomHeight)))
	}
	layout := m.layout()
	if !strings.HasSuffix(strings.TrimRight(lines[0], " "), "╮") {
		t.Fatalf("top row should end with the right-hand frame, got %q", lines[0])
	}
	if layout.topHeight > 0 && !strings.HasPrefix(lines[layout.topHeight-1], "╰") {
		t.Fatalf("top row should keep its bottom border visible, got %q", lines[layout.topHeight-1])
	}
	bottomStart := layout.topHeight + layout.middleGapHeight
	if layout.bottomHeight > 0 && bottomStart < len(lines) && !strings.HasSuffix(strings.TrimRight(lines[bottomStart], " "), "╮") {
		t.Fatalf("bottom row should end with the right-hand frame, got %q", lines[bottomStart])
	}
	for _, line := range strings.Split(m.View(), "\n") {
		if got := ansi.StringWidth(ansi.Strip(line)); got > m.width {
			t.Fatalf("embedded boss line width = %d, want <= %d: %q", got, m.width, ansi.Strip(line))
		}
	}
	if strings.Contains(m.View(), "\x1b[48;5;0m") {
		t.Fatalf("boss panels should not use ANSI palette black because themed palettes can render it gray")
	}
	if strings.Contains(rendered, "Alt+Enter newline") || strings.Contains(rendered, "Ctrl+R refresh") {
		t.Fatalf("embedded boss body should not repeat footer hotkeys above the input:\n%s", rendered)
	}
}

func TestEmbeddedModelHonorsShortHostHeight(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		width  int
		height int
	}{
		{name: "wide", width: 180, height: 16},
		{name: "wide-very-short", width: 180, height: 11},
		{name: "narrow", width: 70, height: 16},
		{name: "narrow-very-short", width: 70, height: 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := NewEmbedded(context.Background(), nil)
			m.width = tc.width
			m.height = tc.height
			m.stateLoaded = true
			m.syncLayout(true)

			rendered := ansi.Strip(m.View())
			lines := strings.Split(rendered, "\n")
			if len(lines) != m.height {
				t.Fatalf("embedded boss view line count = %d, want %d:\n%s", len(lines), m.height, rendered)
			}
			if !strings.Contains(lines[len(lines)-1], "╰") {
				t.Fatalf("short embedded boss view should keep the bottom panel border visible:\n%s", rendered)
			}
			layout := m.layout()
			if layout.topHeight+layout.middleGapHeight+layout.bottomHeight > layout.height {
				t.Fatalf("short embedded layout heights = top %d + gap %d + bottom %d, want <= %d", layout.topHeight, layout.middleGapHeight, layout.bottomHeight, layout.height)
			}
		})
	}
}

func TestEmbeddedModelGivesSpareHeightToChatOnTallHosts(t *testing.T) {
	t.Parallel()

	previousTopHeight := 0
	for _, height := range []int{30, 44, 61} {
		m := NewEmbedded(context.Background(), nil)
		m.width = 180
		m.height = height
		m.stateLoaded = true
		m.snapshot = StateSnapshot{
			TotalProjects:          148,
			ActiveProjects:         2,
			PossiblyStuckProjects:  2,
			DirtyProjects:          33,
			PendingClassifications: 2,
		}
		m.status = "Boss chat via gpt-5.4-mini"

		layout := m.layout()
		renderedHeight := layout.topHeight + layout.middleGapHeight + layout.bottomHeight
		if renderedHeight != layout.height {
			t.Fatalf("embedded layout should use the full host body height, got rendered height %d terminal height %d", renderedHeight, layout.height)
		}
		if layout.middleGapHeight != 0 {
			t.Fatalf("height %d should not insert a separator row between panel bands, got gap %d", height, layout.middleGapHeight)
		}
		if layout.bottomHeight > bossDeskTargetHeight(layout.height, true) {
			t.Fatalf("height %d bottom desk = %d, want <= %d", height, layout.bottomHeight, bossDeskTargetHeight(layout.height, true))
		}
		if previousTopHeight > 0 && layout.topHeight <= previousTopHeight {
			t.Fatalf("height %d top panel height = %d, want chat row to gain spare terminal height beyond %d", height, layout.topHeight, previousTopHeight)
		}
		previousTopHeight = layout.topHeight
	}
}

func TestEmbeddedModelKeepsBossDeskAtBottom(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 180
	m.height = 42

	layout := m.layout()
	if layout.chatWidth != layout.width {
		t.Fatalf("chat width = %d, want full terminal width %d", layout.chatWidth, layout.width)
	}
	if layout.attentionWidth != layout.width {
		t.Fatalf("boss desk width = %d, want full terminal width %d", layout.attentionWidth, layout.width)
	}
	if layout.bottomHeight < 10 {
		t.Fatalf("boss desk height = %d, want a roomy bottom workbench", layout.bottomHeight)
	}
	if layout.topHeight+layout.middleGapHeight+layout.bottomHeight != layout.height {
		t.Fatalf("layout heights = top %d + gap %d + bottom %d, want %d", layout.topHeight, layout.middleGapHeight, layout.bottomHeight, layout.height)
	}
}

func TestEmbeddedModelKeepsLowerPanelsCompactForLongerConversation(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 180
	m.height = 52
	baseTopHeight := m.layout().topHeight
	baseBottomHeight := m.layout().bottomHeight

	m.messages = append(m.messages,
		ChatMessage{Role: "user", Content: "Give me a compact risk summary for the stuck projects, the active projects, and which dirty repos are probably safe to ignore for now."},
		ChatMessage{Role: "assistant", Content: "The stuck work looks concentrated in a few repos. I would review the highest-attention items first, then separate harmless dirty working trees from projects that are blocking merges or release work."},
		ChatMessage{Role: "user", Content: "Also call out what can safely wait until tomorrow and what needs action before I context switch away from this machine."},
	)

	layout := m.layout()
	if layout.topHeight != baseTopHeight {
		t.Fatalf("longer conversation should scroll within the chat row, got base top %d current %d", baseTopHeight, layout.topHeight)
	}
	if layout.bottomHeight != baseBottomHeight {
		t.Fatalf("longer conversation should not steal height from lower panels, got base bottom %d current %d", baseBottomHeight, layout.bottomHeight)
	}
	if layout.topHeight+layout.middleGapHeight+layout.bottomHeight != layout.height {
		t.Fatalf("longer conversation layout heights = top %d + gap %d + bottom %d, want %d", layout.topHeight, layout.middleGapHeight, layout.bottomHeight, layout.height)
	}
}

func TestEmbeddedModelKeepsMediumWidthLowerPanelsCompact(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 100
	m.height = 44
	m.stateLoaded = true
	m.snapshot = StateSnapshot{
		TotalProjects:         148,
		ActiveProjects:        1,
		PossiblyStuckProjects: 2,
		DirtyProjects:         32,
		HotProjects: []ProjectBrief{
			{Name: "LittleControlRoom", Status: model.StatusActive, AttentionScore: 140, RepoBranch: "master", RepoDirty: true, RepoAheadCount: 10},
			{Name: "social_manager", Status: model.StatusPossiblyStuck, AttentionScore: 92, RepoBranch: "master", RepoDirty: true},
			{Name: "crypto", Status: model.StatusIdle, AttentionScore: 72, RepoBranch: "feature/tui-trader-mvp", RepoDirty: true, RepoAheadCount: 3},
			{Name: "okmain", Status: model.StatusIdle, AttentionScore: 70, RepoBranch: "master_mobnext", RepoAheadCount: 3},
			{Name: "docs_site", Status: model.StatusActive, AttentionScore: 64, RepoBranch: "master"},
			{Name: "runtime_ui", Status: model.StatusIdle, AttentionScore: 58, RepoBranch: "feature/runtime"},
			{Name: "inbox_agent", Status: model.StatusIdle, AttentionScore: 42, RepoBranch: "master"},
			{Name: "release_notes", Status: model.StatusIdle, AttentionScore: 31, RepoBranch: "master"},
		},
	}
	m.status = "Boss chat via gpt-5.4-mini"
	m.syncLayout(true)

	layout := m.layout()
	if layout.middleGapHeight != 0 {
		t.Fatalf("medium-width layout should not use a vertical separator row, got %d", layout.middleGapHeight)
	}
	if layout.bottomHeight < 9 {
		t.Fatalf("medium-width boss desk height = %d, want roomy bottom panel", layout.bottomHeight)
	}
	if layout.chatWidth != layout.width || layout.attentionWidth != layout.width {
		t.Fatalf("medium-width chat/desk should both use full width, got chat %d desk %d terminal %d", layout.chatWidth, layout.attentionWidth, layout.width)
	}
	if layout.topHeight+layout.middleGapHeight+layout.bottomHeight != layout.height {
		t.Fatalf("medium-width panels should fill the host body, got top %d + gap %d + bottom %d terminal %d", layout.topHeight, layout.middleGapHeight, layout.bottomHeight, layout.height)
	}

	rendered := ansi.Strip(m.View())
	if !strings.Contains(rendered, "LittleControlRoom") {
		t.Fatalf("boss desk should still show the highest-attention project:\n%s", rendered)
	}
	lines := strings.Split(rendered, "\n")
	bottomBorderLine := layout.topHeight + layout.middleGapHeight + layout.bottomHeight - 1
	if bottomBorderLine >= len(lines) {
		t.Fatalf("bottom border row %d outside rendered view with %d lines:\n%s", bottomBorderLine, len(lines), rendered)
	}
	if !strings.HasPrefix(lines[bottomBorderLine], "╰") {
		t.Fatalf("bottom desk should keep its bottom border visible, got %q", lines[bottomBorderLine])
	}
	if bottomBorderLine != len(lines)-1 {
		t.Fatalf("bottom desk should finish on the final embedded body row, got border row %d line count %d", bottomBorderLine, len(lines))
	}
}

func TestChatPanelKeepsStyledTranscriptAndInputVisible(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 120
	m.height = 20
	m.stateLoaded = true
	m.messages = []ChatMessage{{
		Role:    "assistant",
		Content: "Use the full chat column for this response so styled terminal output does not get mistaken for visible text.",
	}}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello boss")})
	m = updated.(Model)
	m.syncLayout(true)

	rendered := ansi.Strip(m.renderChat(m.layout()))
	if strings.Contains(rendered, "...") {
		t.Fatalf("chat panel should not append ellipses while fitting styled content:\n%s", rendered)
	}
	if !strings.Contains(rendered, "> hello boss") {
		t.Fatalf("chat input should remain visible while typing:\n%s", rendered)
	}
}

func TestChatPanelRendersCompanionInSpareTranscriptSpace(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 120
	m.height = 20
	m.stateLoaded = true
	m.syncLayout(true)

	rendered := m.renderChat(m.layout())
	if !strings.Contains(rendered, "\x1b[38;2;") {
		t.Fatalf("chat panel should render the truecolor companion when there is spare space:\n%s", ansi.Strip(rendered))
	}
	if !strings.Contains(ansi.Strip(rendered), "\u2588") {
		t.Fatalf("chat panel should render pixel block glyphs for the companion:\n%s", ansi.Strip(rendered))
	}
	if count := strings.Count(rendered, "\x1b[38;2;"); count > 80 {
		t.Fatalf("companion should render as a compact overlay, got %d truecolor fragments", count)
	}
	if strings.Contains(m.renderTranscript(m.layout().chatInnerWidth), "\u2588") {
		t.Fatalf("companion should stay out of the durable transcript")
	}
}

func TestChatPanelHidesCompanionInCrampedTranscript(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 58
	m.height = 14
	m.stateLoaded = true
	m.syncLayout(true)

	rendered := m.renderChat(m.layout())
	if strings.Contains(rendered, "\x1b[38;2;") || strings.Contains(ansi.Strip(rendered), "\u2588") {
		t.Fatalf("chat panel should not force the companion into a cramped transcript:\n%s", ansi.Strip(rendered))
	}
}

func TestChatCompanionDoesNotOverwriteTranscriptText(t *testing.T) {
	t.Parallel()

	width := 60
	height := 8
	sprite := renderBossCompanionSprite(bossCompanionIdle, 0)
	x := width - sprite.width - 1
	y := 0
	collidingRow := strings.Repeat(" ", x+3) + "X" + strings.Repeat(" ", width-x-4)
	lines := []string{collidingRow}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	base := strings.Join(lines, "\n")

	if got := overlayBossCompanion(base, width, height, x, y, sprite); got != base {
		t.Fatalf("companion overlay should leave transcript unchanged when a sprite cell would hit text:\n%s", ansi.Strip(got))
	}
}

func TestChatMouseSelectionCopiesOnlyTranscriptText(t *testing.T) {
	prevWriter := clipboardTextWriter
	var copied string
	clipboardTextWriter = func(text string) error {
		copied = text
		return nil
	}
	defer func() { clipboardTextWriter = prevWriter }()

	m := NewEmbedded(context.Background(), nil)
	m.width = 120
	m.height = 24
	m.stateLoaded = true
	m.snapshot = StateSnapshot{
		TotalProjects: 99,
		DirtyProjects: 42,
	}
	m.messages = []ChatMessage{{
		Role:    "assistant",
		Content: "alpha beta",
	}}
	m.syncLayout(true)

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      bossPanelContentLeft + len("Boss> "),
		Y:      bossChatTranscriptTop,
	})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonLeft,
		X:      bossPanelContentLeft + len("Boss> ") + len("alpha"),
		Y:      bossChatTranscriptTop,
	})
	m = updated.(Model)
	if rendered := m.renderChat(m.layout()); !strings.Contains(rendered, bossSelectionHighlightStart) {
		t.Fatalf("chat selection should render a scoped highlight:\n%s", ansi.Strip(rendered))
	}

	updated, _ = m.Update(tea.MouseMsg{
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
		X:      bossPanelContentLeft + len("Boss> ") + len("alpha"),
		Y:      bossChatTranscriptTop,
	})
	m = updated.(Model)
	if copied != "alpha" {
		t.Fatalf("copied selection = %q, want %q", copied, "alpha")
	}
	for _, unwanted := range []string{"Board:", "Dirty repos", "Projects:"} {
		if strings.Contains(copied, unwanted) {
			t.Fatalf("copied selection should not include side panel text %q: %q", unwanted, copied)
		}
	}
	if m.status != "Copied chat selection to clipboard" {
		t.Fatalf("status = %q, want copy confirmation", m.status)
	}
}

func TestModelTranscriptRendersMarkdown(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.messages = []ChatMessage{{
		Role:    "assistant",
		Content: "## Plan\n- **Ship** the `boss` chat polish",
	}, {
		Role:    "user",
		Content: "Can we use `markdown`?",
	}}

	rendered := ansi.Strip(m.renderTranscript(72))
	for _, want := range []string{"Boss>", "Plan", "Ship", "boss", "You>", "markdown"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered transcript missing %q:\n%s", want, rendered)
		}
	}
	for _, marker := range []string{"Assistant:", "You:", "##", "**", "`"} {
		if strings.Contains(rendered, marker) {
			t.Fatalf("rendered transcript still contains markdown marker %q:\n%s", marker, rendered)
		}
	}
}

func TestModelTranscriptRendersTemporaryStreamingState(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.messages = []ChatMessage{{Role: "user", Content: "Check alpha"}}
	m.sending = true
	m.streamingToolCalls = []string{"tool: project_detail /tmp/alpha", "done: project_detail /tmp/alpha"}
	m.streamingAssistantText = "Alpha is waiting on the rollout decision."

	rendered := ansi.Strip(m.renderTranscript(84))
	for _, want := range []string{"You>", "Check alpha", "Tool calls", "project_detail /tmp/alpha", "Boss>", "Alpha is waiting"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("streaming transcript missing %q:\n%s", want, rendered)
		}
	}
	if len(m.messages) != 1 {
		t.Fatalf("temporary streaming state should not append persisted messages")
	}
}

func TestBossHandoffStyleIsNotMutedToolChrome(t *testing.T) {
	t.Parallel()

	if got, muted := bossHandoffPrefixStyle.GetForeground(), bossToolCallStyle.GetForeground(); got == muted {
		t.Fatalf("handoff prefix foreground = %v, should not reuse muted tool-call color", got)
	}
	if got, want := bossHandoffPrefixStyle.GetForeground(), bossAssistantPrefixStyle.GetForeground(); got != want {
		t.Fatalf("handoff Boss prefix foreground = %v, want normal Boss foreground %v", got, want)
	}
	if got, user := bossHandoffPrefixStyle.GetForeground(), bossUserPrefixStyle.GetForeground(); got == user {
		t.Fatalf("handoff Boss prefix foreground = %v, should not reuse You foreground", got)
	}
}

func TestBossMessagesDoNotIndentContinuationLines(t *testing.T) {
	t.Parallel()

	for name, rendered := range map[string]string{
		"assistant": renderAssistantMessage("Alpha\nBeta", 80),
		"handoff":   renderBossHandoffMessage("Alpha\nBeta", 80),
	} {
		lines := strings.Split(ansi.Strip(rendered), "\n")
		if len(lines) < 2 {
			t.Fatalf("%s rendered %d lines, want continuation:\n%s", name, len(lines), ansi.Strip(rendered))
		}
		if got := strings.TrimRight(lines[0], " "); got != "Boss> Alpha" {
			t.Fatalf("%s first line = %q, want Boss label", name, lines[0])
		}
		if got := strings.TrimRight(lines[1], " "); got != "Beta" {
			t.Fatalf("%s continuation line = %q, want no Boss-label inset", name, lines[1])
		}
	}
}

func TestModelKeepsEngineerActivityOutOfTranscriptWhileBossIsThinking(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := New(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m.messages = []ChatMessage{{Role: "user", Content: "What next?"}}
	m.sending = true
	m.streamingToolCalls = []string{"tool: agent_task_report"}
	m.viewContext = ViewContext{
		EngineerActivities: []ViewEngineerActivity{{
			Kind:         "agent_task",
			TaskID:       "agt_demo",
			Title:        "Diff duplicate Codex skills",
			EngineerName: "Ada",
			Provider:     model.SessionSourceCodex,
			Status:       "working",
			Active:       true,
			StartedAt:    now.Add(-9 * time.Second),
		}},
	}

	rendered := ansi.Strip(m.renderTranscript(90))
	for _, want := range []string{"Tool calls", "agent_task_report"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("thinking transcript missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Ada is working on Diff duplicate Codex skills") || strings.Contains(rendered, "Supervisor") {
		t.Fatalf("thinking transcript should not expose supervisor chrome:\n%s", rendered)
	}
	desk := ansi.Strip(m.deskContent(90, 12))
	for _, want := range []string{"Now", "00:09", "Ada on Diff duplicate Codex skills"} {
		if !strings.Contains(desk, want) {
			t.Fatalf("thinking desk missing %q:\n%s", want, desk)
		}
	}
}

func TestPanelUsesFullAllocatedWidthAndKeepsBottomBorder(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	rendered := ansi.Strip(m.renderPanel("Attention", "alpha beta gamma delta epsilon zeta eta theta", 33, 8))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 8 {
		t.Fatalf("panel rendered %d lines, want 8:\n%s", len(lines), rendered)
	}
	if got := ansi.StringWidth(strings.TrimRight(lines[0], " ")); got != 33 {
		t.Fatalf("panel visible width = %d, want 33: %q", got, lines[0])
	}
	if !strings.HasPrefix(lines[len(lines)-1], "╰") {
		t.Fatalf("panel should keep bottom border visible:\n%s", rendered)
	}
}

func renderedLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(ansi.Strip(s), "\n") + 1
}
