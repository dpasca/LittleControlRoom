package boss

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"lcroom/internal/agentcontext"
	"lcroom/internal/bossrun"
	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/pixelart"
	"lcroom/internal/service"
	"lcroom/internal/store"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
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
	for _, want := range []string{"Chat", "Boss Desk", "Boss Log", "Watching", "Next", "Alpha"} {
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
	for _, want := range []string{"Alt+Enter newline", "Alt+Up hides"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("view missing boss shortcut %q:\n%s", want, stripped)
		}
	}
	if strings.Contains(stripped, "Esc hides") {
		t.Fatalf("view should keep Esc as a silent hide alias:\n%s", stripped)
	}
	if strings.Contains(stripped, "Ctrl+J newline") {
		t.Fatalf("view should advertise Alt+Enter instead of Ctrl+J:\n%s", stripped)
	}
}

func TestModelHeaderShowsLastUsage(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.width = 120
	m.height = 30
	m.stateLoaded = true

	updated, _ := m.Update(AssistantReplyMsg{
		response: AssistantResponse{
			Content: "Done.",
			Model:   "gpt-5.5",
			Usage: model.LLMUsage{
				InputTokens:       4321,
				OutputTokens:      210,
				TotalTokens:       4531,
				CachedInputTokens: 800,
				ReasoningTokens:   55,
				EstimatedCostUSD:  0.0123,
			},
		},
	})
	got := updated.(Model)

	rendered := ansi.Strip(got.View())
	if !strings.Contains(rendered, "tok i4.3k o210 c800 r55") {
		t.Fatalf("view missing token usage stats:\n%s", rendered)
	}
	if strings.Contains(rendered, "t4.5k") {
		t.Fatalf("Chat usage should not show total token count:\n%s", rendered)
	}
	if !strings.Contains(rendered, "last $0.012") {
		t.Fatalf("view missing last cost estimate:\n%s", rendered)
	}
}

func TestBossSidebarShowsReadableChatStats(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.lastAssistantModel = "gpt-5.5"
	m.haveLastAssistantUsage = true
	m.lastAssistantUsage = model.LLMUsage{
		InputTokens:       4321,
		OutputTokens:      210,
		TotalTokens:       4531,
		CachedInputTokens: 800,
		ReasoningTokens:   55,
	}
	m.haveLastContextReport = true
	m.lastContextReport = bossContextReport{
		ContextMode:     agentcontext.ContextModeCompacted,
		MessageCount:    25,
		TotalMessages:   25,
		VisibleMessages: 12,
		SummaryMessages: 13,
		ApproxChars:     1800,
	}

	rendered := ansi.Strip(strings.Join(m.bossSidebarLines(54, 20), "\n"))
	for _, want := range []string{
		"Chat",
		"Model",
		"gpt-5.5",
		"Reasoning",
		"high",
		"Context",
		"compacted",
		"12 recent + 13 summarized",
		"~1.8k chars",
		"Tokens",
		"i4.3k o210 c800 r55",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("sidebar missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "ctx compacted") || strings.Contains(rendered, "25t") || strings.Contains(rendered, "1.8kch") {
		t.Fatalf("sidebar should use readable context wording:\n%s", rendered)
	}
}

func TestModelContextTextReportsClippedChatAndFlow(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	for i := 0; i < bossPromptChatHistoryLimit+3; i++ {
		m.messages = append(m.messages, ChatMessage{
			Role:    "user",
			Content: fmt.Sprintf("chat turn %02d", i),
		})
	}
	m.messages = append(m.messages, ChatMessage{
		Role:    "assistant",
		Content: "Work on Alpha is ready for review.",
		Kind:    ChatMessageKindFlow,
	})

	got := m.ContextText()
	for _, want := range []string{
		"ctx clipped",
		fmt.Sprintf("%d/%dt", bossPromptChatHistoryLimit, bossPromptChatHistoryLimit+3),
		"flow1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("ContextText() = %q, missing %q", got, want)
		}
	}
}

func TestModelContextTextReportsCompactedSummary(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.haveLastContextReport = true
	m.lastContextReport = bossContextReport{
		ContextMode:     agentcontext.ContextModeCompacted,
		MessageCount:    25,
		TotalMessages:   25,
		VisibleMessages: 12,
		SummaryMessages: 13,
		ApproxChars:     1800,
	}

	got := m.ContextText()
	for _, want := range []string{
		"ctx compacted",
		"12+13/25t",
		"1.8kch",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("ContextText() = %q, missing %q", got, want)
		}
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
	coloredProjectName := bossProjectIdentityStyle("/alpha", bossProjectNameStyle).Width(21).Render(fitLine("Alpha", 21))
	if !strings.Contains(rendered, coloredProjectName) {
		t.Fatalf("attention row should color the project identity:\n%s", stripped)
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

func TestBossDeskRowsShowStableAltHotkeys(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := New(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m.snapshot = StateSnapshot{
		OpenAgentTasks: []AgentTaskBrief{{
			ID:      "agt_review",
			Title:   "Review Cursor OAuth cleanup",
			Status:  model.AgentTaskStatusWaiting,
			Summary: "Needs your decision.",
		}},
		HotProjects: []ProjectBrief{{
			Name:          "Alpha",
			Path:          "/alpha",
			Status:        model.StatusActive,
			LatestSummary: "Ready for review.",
		}},
	}
	m.syncAttentionHotkeys()

	renderedDesk := m.deskContent(80, 12)
	desk := ansi.Strip(renderedDesk)
	for _, want := range []string{"Alt+1", "Review Cursor OAuth cleanup", "Alt+2", "Alpha"} {
		if !strings.Contains(desk, want) {
			t.Fatalf("boss desk missing hotkey marker %q:\n%s", want, desk)
		}
	}
	if want := bossProjectIdentityStyle("/alpha", bossProjectNameStyle).Render("Alpha"); !strings.Contains(renderedDesk, want) {
		t.Fatalf("boss desk should color the project identity:\n%s", desk)
	}
}

func TestBossDeskTodoRowsQualifyPinnedWorkSessionAgainstLiveActivity(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := New(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m.snapshot = StateSnapshot{
		OpenTodos: []TodoBrief{{
			ID:              42,
			ProjectPath:     "/repo",
			ProjectName:     "repo",
			Text:            "Finish pinned lane",
			WorkState:       model.TodoWorkStateWorking,
			WorkProvider:    model.SessionSourceCodex,
			WorkProjectPath: "/repo--todo-42",
			WorkSessionID:   "codex:thread-42",
		}},
	}

	stale := ansi.Strip(strings.Join(m.bossDeskTodoRows(100, now), "\n"))
	if !strings.Contains(stale, "stale Codex") {
		t.Fatalf("boss desk TODO row = %q, want stale Codex without live activity", stale)
	}

	m.viewContext.EngineerActivities = []ViewEngineerActivity{{
		Provider:    model.SessionSourceCodex,
		ProjectPath: "/repo--todo-42",
		SessionID:   "thread-42",
		Status:      "working",
		Active:      true,
	}}
	live := ansi.Strip(strings.Join(m.bossDeskTodoRows(100, now), "\n"))
	if !strings.Contains(live, "working Codex") || strings.Contains(live, "stale Codex") {
		t.Fatalf("boss desk TODO row = %q, want live working Codex activity", live)
	}
}

func TestBossAttentionHotkeysStayWithIdentityAcrossRefresh(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.snapshot = StateSnapshot{
		OpenAgentTasks: []AgentTaskBrief{
			{ID: "agt_one", Title: "One", Status: model.AgentTaskStatusActive},
			{ID: "agt_two", Title: "Two", Status: model.AgentTaskStatusActive},
		},
	}
	m.syncAttentionHotkeys()

	m.snapshot = StateSnapshot{
		OpenAgentTasks: []AgentTaskBrief{
			{ID: "agt_two", Title: "Two", Status: model.AgentTaskStatusActive},
			{ID: "agt_three", Title: "Three", Status: model.AgentTaskStatusActive},
		},
	}
	m.syncAttentionHotkeys()

	if got := m.attentionHotkeyLabel(AttentionItem{Kind: AttentionItemAgentTask, TaskID: "agt_two"}); got != "Alt+2" {
		t.Fatalf("existing task hotkey = %q, want Alt+2", got)
	}
	if got := m.attentionHotkeyLabel(AttentionItem{Kind: AttentionItemAgentTask, TaskID: "agt_three"}); got != "Alt+1" {
		t.Fatalf("new task hotkey = %q, want the freed Alt+1 slot", got)
	}
}

func TestModelAttentionRowsShowActiveAgentTaskTimer(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := New(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m.viewContext = ViewContext{
		EngineerActivities: []ViewEngineerActivity{{
			Kind:        "agent_task",
			TaskID:      "agt_demo",
			ProjectPath: "/tmp/agent-task",
			Title:       "Revoke Cursor GitHub access",
			Provider:    model.SessionSourceCodex,
			SessionID:   "thread-agent-1",
			Status:      "working",
			Active:      true,
			StartedAt:   now.Add(-37 * time.Second),
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
	for _, want := range []string{"Alt+1", "00:37", "working 00:37", "Revoke Cursor GitHub"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("active agent attention row missing %q:\n%s", want, stripped)
		}
	}
	desk := ansi.Strip(m.deskContent(90, 12))
	for _, want := range []string{"Now", "00:37", "Working on Revoke Cursor GitHub access"} {
		if !strings.Contains(desk, want) {
			t.Fatalf("active agent desk status missing %q:\n%s", want, desk)
		}
	}
	transcript := ansi.Strip(m.renderTranscript(90))
	if strings.Contains(transcript, "Ada is working on Revoke Cursor GitHub access") || strings.Contains(transcript, "Supervisor") {
		t.Fatalf("active agent status should stay out of transcript:\n%s", transcript)
	}
}

func TestModelChatOnlyViewOmitsDeskAndLog(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil).WithChatOnly(true)
	m.width = 112
	m.height = 24
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

	rendered := ansi.Strip(m.View())
	if strings.TrimSpace(rendered) == "" {
		t.Fatalf("chat-only view should render the core chat surface")
	}
	for _, unwanted := range []string{"Chat", "Boss Desk", "Boss Log", "Watching", "Next"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("chat-only view should omit %q:\n%s", unwanted, rendered)
		}
	}
}

func TestEmbeddedHelpUsesSeparateSessionStore(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "little-control-room.sqlite")
	svc := service.New(cfg, nil, events.NewBus(), nil)

	bossModel := NewEmbedded(context.Background(), svc)
	helpModel := NewEmbeddedHelp(context.Background(), svc)
	if bossModel.sessionStore == nil || helpModel.sessionStore == nil {
		t.Fatalf("session stores should be configured")
	}
	if filepath.Base(bossModel.sessionStore.dir) != bossSessionsDirName {
		t.Fatalf("boss session dir = %q, want %q", bossModel.sessionStore.dir, bossSessionsDirName)
	}
	if filepath.Base(helpModel.sessionStore.dir) != helpChatSessionsDirName {
		t.Fatalf("help session dir = %q, want %q", helpModel.sessionStore.dir, helpChatSessionsDirName)
	}
	if bossModel.sessionStore.dir == helpModel.sessionStore.dir {
		t.Fatalf("chat should not share legacy Chat session history: %q", helpModel.sessionStore.dir)
	}
}

func TestEmbeddedHelpDisablesBossSlashAndFlowTabs(t *testing.T) {
	t.Parallel()

	m := NewEmbeddedHelp(context.Background(), nil)
	m.width = 112
	m.height = 24
	m.input.SetValue("/sessions")
	m.syncLayout(true)

	if m.SlashActive() {
		t.Fatalf("chat should treat slash text as normal chat input")
	}
	updated, cmd := m.submit()
	got := updated.(Model)
	if got.sessionPickerVisible {
		t.Fatalf("chat slash-looking input should not open the Boss session picker")
	}
	if len(got.messages) != 1 || got.messages[0].Role != "user" || got.messages[0].Content != "/sessions" {
		t.Fatalf("chat should submit slash-looking text as a user question, got %#v", got.messages)
	}
	if cmd == nil {
		t.Fatalf("chat slash-looking input should still submit to the assistant")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.normalizedTranscriptTab() != bossTranscriptTabChat {
		t.Fatalf("chat Tab should not switch to Flow, got %q", got.normalizedTranscriptTab())
	}
}

func TestEmbeddedHelpViewOmitsBossTranscriptControls(t *testing.T) {
	t.Parallel()

	m := NewEmbeddedHelp(context.Background(), nil)
	m.width = 112
	m.height = 24
	m.stateLoaded = true
	m.messages = []ChatMessage{{Role: "assistant", Content: "Ask me about Little Control Room."}}
	m.syncLayout(true)

	rendered := ansi.Strip(m.View())
	for _, unwanted := range []string{"Chat", "Boss Desk", "Boss Log", "Flow", "Tab switch"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("chat view should omit Boss transcript control %q:\n%s", unwanted, rendered)
		}
	}
	if !strings.Contains(rendered, "Help> Ask me about Little Control Room.") {
		t.Fatalf("chat should use a Help speaker label:\n%s", rendered)
	}
	if strings.Contains(rendered, "Boss>") {
		t.Fatalf("chat should not use Boss speaker labels:\n%s", rendered)
	}

	rawTranscript := m.renderTranscript(112)
	for _, unwanted := range []string{"\x1b[48;2;0;0;0m", "\x1b[48;5;0m"} {
		if strings.Contains(rawTranscript, unwanted) {
			t.Fatalf("chat transcript should not paint old black Boss backgrounds %q:\n%s", unwanted, rendered)
		}
	}
	for label, style := range map[string]lipgloss.Style{
		"assistant": helpChatAssistantMessageStyle,
		"user":      helpChatUserMessageStyle,
	} {
		if got, want := fmt.Sprint(style.GetBackground()), fmt.Sprint(helpChatSurfaceBackground); got != want {
			t.Fatalf("chat %s text background = %s, want help surface %s", label, got, want)
		}
	}
}

func TestEmbeddedHelpRendersHostNoticesInChatWithoutFlow(t *testing.T) {
	t.Parallel()

	m := NewEmbeddedHelp(context.Background(), nil)
	m.width = 100
	m.height = 20
	updated, _ := m.Update(HostNoticeMsg{
		Content:        "Work on Project Task is ready for review.",
		AnnounceInChat: true,
		Handoff:        &HandoffHighlight{ProjectLabel: "Project Task"},
	})
	got := updated.(Model)
	if len(got.messages) != 1 || got.messages[0].Kind != ChatMessageKindChat {
		t.Fatalf("help host notice messages = %#v, want one visible chat message", got.messages)
	}
	got.syncLayout(true)
	rendered := ansi.Strip(got.View())
	if !strings.Contains(rendered, "Help> Work on Project Task is ready for review.") {
		t.Fatalf("help host notice missing from chat:\n%s", rendered)
	}
	if strings.Contains(rendered, "Flow") {
		t.Fatalf("help host notice exposed retired Flow UI:\n%s", rendered)
	}
}

func TestEmbeddedHelpAssistantResponseWordWrapsAtFullWidthAndKeepsBackground(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	defer func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	}()

	m := NewEmbeddedHelp(context.Background(), nil)
	width := 38
	rendered := m.renderAssistantChatMessage(ChatMessage{
		Role:    "assistant",
		Content: "This is a long **wrapped** help response that should continue under the message body instead of snapping back to the left edge.",
	}, width, nil)
	lines := strings.Split(rendered, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected wrapped help response, got:\n%s", ansi.Strip(rendered))
	}
	for i, line := range lines {
		if got := ansi.StringWidth(ansi.Strip(line)); got > width {
			t.Fatalf("chat response line %d width = %d, want <= %d: %q", i, got, width, ansi.Strip(line))
		}
	}
	for i, line := range lines[1:] {
		stripped := strings.TrimRight(ansi.Strip(line), " ")
		if strings.HasPrefix(stripped, " ") {
			t.Fatalf("continuation line %d should start at the left edge:\n%s", i+1, ansi.Strip(rendered))
		}
		if strings.HasPrefix(strings.TrimLeft(stripped, " "), "Help>") {
			t.Fatalf("continuation line %d should not repeat the speaker label:\n%s", i+1, ansi.Strip(rendered))
		}
	}
	plain := strings.Join(strings.Fields(ansi.Strip(rendered)), " ")
	wantPlain := "Help> This is a long wrapped help response that should continue under the message body instead of snapping back to the left edge."
	if plain != wantPlain {
		t.Fatalf("wrapped response changed word boundaries:\n got: %q\nwant: %q", plain, wantPlain)
	}
	usedFullWidth := false
	for _, line := range lines[1:] {
		if ansi.StringWidth(strings.TrimRight(ansi.Strip(line), " ")) > width-len("Help> ") {
			usedFullWidth = true
			break
		}
	}
	if !usedFullWidth {
		t.Fatalf("continuation lines should use more than the old prefix-reduced width:\n%s", ansi.Strip(rendered))
	}
	if got := strings.Count(rendered, "48;5;234"); got == 0 {
		t.Fatalf("chat Markdown response should keep the surface background:\n%q", rendered)
	}
	assertANSI256Background(t, rendered, 234)
}

func TestEmbeddedHelpAssistantKeepsHyphenatedWordsTogetherAtWrapBoundaries(t *testing.T) {
	t.Parallel()

	m := NewEmbeddedHelp(context.Background(), nil)
	content := "Based on attention scores and signals: the session needs follow-up before the next check-in."
	for width := 24; width <= 96; width++ {
		rendered := m.renderAssistantChatMessage(ChatMessage{Role: "assistant", Content: content}, width, nil)
		for _, line := range strings.Split(ansi.Strip(rendered), "\n") {
			if strings.TrimSpace(line) == "-" {
				t.Fatalf("width %d split a hyphen onto its own line:\n%s", width, ansi.Strip(rendered))
			}
		}
	}
}

func TestEmbeddedHelpUsesBossControlConfirmationFlow(t *testing.T) {
	t.Parallel()

	inv := bossControlInvocationForTest(t)
	m := NewEmbeddedHelp(context.Background(), nil)
	updated, cmd := m.Update(AssistantReplyMsg{response: AssistantResponse{
		Content:           "Send this to the engineer?",
		ControlInvocation: &inv,
	}})
	got := updated.(Model)
	if cmd != nil || got.pendingControl == nil || !got.ControlConfirmationActive() {
		t.Fatalf("Chat should enter the shared confirmation flow, cmd=%v pending=%#v", cmd, got.pendingControl)
	}
	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.pendingControl != nil || cmd == nil {
		t.Fatalf("Chat confirmation should emit the host control, cmd=%v pending=%#v", cmd, got.pendingControl)
	}
	if _, ok := cmd().(ControlInvocationConfirmedMsg); !ok {
		t.Fatalf("confirmation command returned the wrong message type")
	}
}

func TestEmbeddedHelpTrackedWorktreeConfirmationCanChooseTodoOnly(t *testing.T) {
	t.Parallel()

	args, err := json.Marshal(control.TodoCreateWorktreeAndStartEngineerInput{
		ProjectPath: "/tmp/alpha",
		ProjectName: "Alpha",
		TodoText:    "Add durable engineer feedback.",
		Prompt:      "Implement durable engineer feedback.",
		Provider:    control.ProviderCodex,
	})
	if err != nil {
		t.Fatalf("marshal invocation: %v", err)
	}
	inv := control.Invocation{Capability: control.CapabilityTodoCreateWorktreeAndStartEngineer, Args: args}
	m := NewEmbeddedHelp(context.Background(), nil)
	updated, _ := m.Update(AssistantReplyMsg{response: AssistantResponse{Content: "Start tracked work?", ControlInvocation: &inv}})
	got := updated.(Model)
	if !got.TodoOnlyConfirmationActive() {
		t.Fatalf("tracked worktree confirmation should expose TODO-only choice")
	}
	rendered := ansi.Strip(got.renderControlConfirmationDialog(100, 32))
	for _, want := range []string{"Tracked Engineer Task", "dedicated worktree", "start in worktree", "TODO only"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("tracked worktree confirmation missing %q:\n%s", want, rendered)
		}
	}
	updated, cmd := got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	got = updated.(Model)
	if got.pendingControl != nil || cmd == nil {
		t.Fatalf("q should confirm a TODO-only action, pending=%#v cmd=%v", got.pendingControl, cmd)
	}
	confirmed, ok := cmd().(ControlInvocationConfirmedMsg)
	if !ok || confirmed.Invocation.Capability != control.CapabilityTodoAdd {
		t.Fatalf("q confirmation = %#v, want todo.add", confirmed)
	}
	var input control.TodoAddInput
	if err := json.Unmarshal(confirmed.Invocation.Args, &input); err != nil {
		t.Fatalf("decode TODO-only input: %v", err)
	}
	if input.ProjectPath != "/tmp/alpha" || input.Text != "Add durable engineer feedback." {
		t.Fatalf("TODO-only input = %#v", input)
	}
}

func TestEmbeddedHelpNewRepositoryConfirmationShowsFilesystemEffects(t *testing.T) {
	t.Parallel()

	inv := validatedControlInvocationForTest(t, control.CapabilityProjectCreateAndStartEngineer, control.ProjectCreateAndStartEngineerInput{
		ParentPath:  "/tmp/repos",
		ProjectName: "KeyMaster",
		TodoText:    "Build the initial KeyMaster repository.",
		Prompt:      "Create the project structure and verify it.",
		Provider:    control.ProviderCodex,
	})
	m := NewEmbeddedHelp(context.Background(), nil)
	updated, _ := m.Update(AssistantReplyMsg{response: AssistantResponse{Content: "Create KeyMaster?", ControlInvocation: &inv}})
	got := updated.(Model)
	if got.TodoOnlyConfirmationActive() {
		t.Fatalf("new repository confirmation must not expose the existing-project TODO-only shortcut")
	}
	rendered := ansi.Strip(got.renderControlConfirmationDialog(100, 36))
	for _, want := range []string{
		"Repository Setup & Work",
		"set up a Git repository and start tracked work",
		"/tmp/repos/KeyMaster",
		"register existing or initialize new",
		"dedicated worktree",
		"Codex",
		"set up and start",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("new repository confirmation missing %q:\n%s", want, rendered)
		}
	}
}

func TestEmbeddedHelpInputUsesSimpleCodexStyle(t *testing.T) {
	t.Parallel()

	m := NewEmbeddedHelp(context.Background(), nil)
	m.width = 60
	m.height = 18
	m.stateLoaded = true
	m.input.SetValue("first line\nsecond line")
	m.syncLayout(true)

	layout := m.layout()
	if layout.inputEditorHeight != 4 {
		t.Fatalf("chat input height = %d, want four rows", layout.inputEditorHeight)
	}
	if got := m.input.Height(); got != 4 {
		t.Fatalf("chat textarea height = %d, want four rows", got)
	}
	if got := helpChatInputShellStyle.GetHorizontalPadding(); got != 2 {
		t.Fatalf("chat input shell horizontal padding = %d, want 2", got)
	}
	if got, want := m.input.Width()+bossInputPromptWidth, layout.chatInnerWidth-helpChatInputShellStyle.GetHorizontalPadding(); got != want {
		t.Fatalf("chat textarea width including prompt = %d, want padded-shell inner width %d", got, want)
	}
	rendered := m.renderCoreInput(layout)
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "> first line") {
		t.Fatalf("chat input should render the first prompt:\n%s", stripped)
	}
	if strings.Contains(stripped, "| second line") {
		t.Fatalf("chat input should not use the old Boss continuation bar:\n%s", stripped)
	}
	if !strings.Contains(stripped, "  second line") {
		t.Fatalf("chat input should render a plain continuation indent:\n%s", stripped)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if got := ansi.StringWidth(ansi.Strip(line)); got > layout.chatInnerWidth {
			t.Fatalf("chat input line width = %d, want <= %d: %q", got, layout.chatInnerWidth, ansi.Strip(line))
		}
	}
}

func assertANSI256Background(t *testing.T, rendered string, want int) {
	t.Helper()

	background := -1
	line := 0
	column := 0
	for i := 0; i < len(rendered); {
		if rendered[i] == '\x1b' && i+1 < len(rendered) && rendered[i+1] == '[' {
			end := i + 2
			for end < len(rendered) && (rendered[end] < '@' || rendered[end] > '~') {
				end++
			}
			if end < len(rendered) {
				if rendered[end] == 'm' {
					background = testANSI256Background(background, rendered[i+2:end])
				}
				i = end + 1
				continue
			}
		}
		if rendered[i] == '\n' {
			line++
			column = 0
			i++
			continue
		}
		if rendered[i] == '\r' {
			i++
			continue
		}
		if background != want {
			t.Fatalf("visible help text cell at line %d column %d has ANSI-256 background %d, want %d; rendered=%q", line, column, background, want, rendered)
		}
		column++
		i++
	}
}

func testANSI256Background(current int, raw string) int {
	if raw == "" {
		return -1
	}
	parts := strings.Split(raw, ";")
	params := make([]int, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		params = append(params, value)
	}
	for i := 0; i < len(params); i++ {
		switch {
		case params[i] == 0 || params[i] == 49:
			current = -1
		case params[i] >= 40 && params[i] <= 47:
			current = params[i] - 40
		case params[i] >= 100 && params[i] <= 107:
			current = params[i] - 100 + 8
		case params[i] == 48 && i+2 < len(params) && params[i+1] == 5:
			current = params[i+2]
			i += 2
		case params[i] == 48 && i+4 < len(params) && params[i+1] == 2:
			current = -2
			i += 4
		}
	}
	return current
}

func TestEmbeddedHelpSlashNewClearsCurrentChat(t *testing.T) {
	t.Parallel()

	svc := newBossSessionTestService(t)
	m := NewEmbeddedHelp(context.Background(), svc)
	loadedMsg := m.loadLatestBossSessionCmd()().(bossSessionLoadedMsg)
	updated, _ := m.Update(loadedMsg)
	m = updated.(Model)
	firstSessionID := m.sessionID
	m.messages = []ChatMessage{
		{Role: "user", Content: "old chat", At: time.Now()},
		{Role: "assistant", Content: "old answer", At: time.Now()},
	}
	m.haveLastAssistantUsage = true
	m.lastAssistantUsage = model.LLMUsage{InputTokens: 10, OutputTokens: 3, TotalTokens: 13}
	m.haveLastAssistantTime = true
	m.lastAssistantTime = 2 * time.Second
	m.input.SetValue("/new")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("/new should create a fresh chat session")
	}
	msg := cmd()
	loaded, ok := msg.(bossSessionLoadedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want bossSessionLoadedMsg", msg)
	}
	updated, _ = got.Update(loaded)
	got = updated.(Model)
	if got.sessionID == "" || got.sessionID == firstSessionID {
		t.Fatalf("session id = %q, want fresh help session different from %q", got.sessionID, firstSessionID)
	}
	if len(got.messages) != 0 {
		t.Fatalf("messages len = %d, want fresh transcript", len(got.messages))
	}
	if got.input.Value() != "" {
		t.Fatalf("input = %q, want cleared", got.input.Value())
	}
	if got.haveLastAssistantUsage || got.haveLastAssistantTime {
		t.Fatalf("profile state should reset on /new")
	}
	if !strings.Contains(got.status, "Chat") {
		t.Fatalf("status = %q, want chat status", got.status)
	}
}

func TestEmbeddedHelpCtrlLClearsCurrentChat(t *testing.T) {
	t.Parallel()

	m := NewEmbeddedHelp(context.Background(), nil)
	m.messages = []ChatMessage{{Role: "user", Content: "old chat", At: time.Now()}}
	m.input.SetValue("draft")
	m.sending = true
	canceled := false
	m.assistantCancel = func() { canceled = true }
	m.assistantStartedAt = time.Now().Add(-time.Second)
	m.haveLastAssistantUsage = true
	m.lastAssistantUsage = model.LLMUsage{InputTokens: 10, OutputTokens: 3, TotalTokens: 13}
	streamID := m.assistantStreamID

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("Ctrl+L without a session store should not need a command")
	}
	if len(got.messages) != 0 || got.input.Value() != "" {
		t.Fatalf("Ctrl+L should clear messages and input, messages=%#v input=%q", got.messages, got.input.Value())
	}
	if !canceled || got.sending || got.assistantCancel != nil || !got.assistantStartedAt.IsZero() || got.haveLastAssistantUsage {
		t.Fatalf("Ctrl+L should clear active/profile state")
	}
	if got.assistantStreamID <= streamID {
		t.Fatalf("Ctrl+L should invalidate active stream id")
	}
}

func TestEmbeddedHelpCtrlCStopsActiveResponseAndKeepsDraft(t *testing.T) {
	t.Parallel()

	m := NewEmbeddedHelp(context.Background(), nil)
	canceled := false
	m.sending = true
	m.assistantCancel = func() { canceled = true }
	m.assistantStartedAt = time.Now().Add(-time.Second)
	m.input.SetValue("actually, use the existing repository")
	streamID := m.assistantStreamID

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("Ctrl+C while Chat is responding should stop the response without closing Chat")
	}
	if !canceled || got.sending || got.assistantCancel != nil {
		t.Fatalf("active response was not canceled cleanly: canceled=%v sending=%v cancel=%v", canceled, got.sending, got.assistantCancel)
	}
	if got.assistantStreamID <= streamID {
		t.Fatalf("stopping should invalidate the active stream id")
	}
	if got.input.Value() != "actually, use the existing repository" {
		t.Fatalf("draft = %q, want correction preserved", got.input.Value())
	}
	if !strings.Contains(got.status, "response stopped") {
		t.Fatalf("status = %q, want stopped receipt", got.status)
	}
}

func TestEmbeddedHelpEnterSteersActiveResponseWithDraft(t *testing.T) {
	t.Parallel()

	m := NewEmbeddedHelp(context.Background(), nil)
	m.messages = []ChatMessage{{Role: "user", Content: "cd /wrong/path", At: time.Now()}}
	canceled := false
	m.sending = true
	m.assistantCancel = func() { canceled = true }
	m.assistantStartedAt = time.Now().Add(-time.Second)
	m.input.SetValue("Ignore that command; register the existing repository instead.")
	streamID := m.assistantStreamID

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	defer got.cancelAssistantRun()
	if cmd == nil {
		t.Fatalf("Enter with a draft during an active response should start the steered request")
	}
	if !canceled || !got.sending || got.assistantCancel == nil {
		t.Fatalf("steer did not replace the active response: canceled=%v sending=%v cancel=%v", canceled, got.sending, got.assistantCancel)
	}
	if got.assistantStreamID <= streamID+1 {
		t.Fatalf("steer stream id = %d, want both old-stream invalidation and a new stream after %d", got.assistantStreamID, streamID)
	}
	if got.input.Value() != "" {
		t.Fatalf("input = %q, want submitted correction cleared", got.input.Value())
	}
	if len(got.messages) != 2 || got.messages[1].Role != "user" || !strings.Contains(got.messages[1].Content, "register the existing repository") {
		t.Fatalf("messages = %#v, want correction appended after mistaken input", got.messages)
	}
}

func TestEmbeddedHelpProfileTextShowsTimingAndTokens(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 7, 9, 13, 0, 0, 0, time.UTC)
	m := NewEmbeddedHelp(context.Background(), nil)
	m.nowFn = func() time.Time { return start.Add(1500 * time.Millisecond) }
	m.sending = true
	m.assistantStartedAt = start
	if got := m.ProfileText(); !strings.Contains(got, "run 1.5s") {
		t.Fatalf("running profile = %q, want elapsed run time", got)
	}

	m.sending = false
	m.assistantStartedAt = time.Time{}
	m.haveLastAssistantTime = true
	m.lastAssistantTime = 2340 * time.Millisecond
	m.haveLastAssistantUsage = true
	m.lastAssistantUsage = model.LLMUsage{
		InputTokens:       4321,
		OutputTokens:      210,
		TotalTokens:       4531,
		CachedInputTokens: 800,
	}
	got := m.ProfileText()
	for _, want := range []string{"last 2.3s", "tok i4.3k o210 c800"} {
		if !strings.Contains(got, want) {
			t.Fatalf("profile = %q, want %q", got, want)
		}
	}
}

func TestBossTickRefreshesDeskTimer(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := NewEmbeddedWithViewContext(context.Background(), nil, ViewContext{
		Active: true,
		EngineerActivities: []ViewEngineerActivity{{
			Kind:      "agent_task",
			TaskID:    "agt_demo",
			Title:     "Cursor cleanup",
			Status:    "working",
			Active:    true,
			StartedAt: now.Add(-3 * time.Second),
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
		{Role: "assistant", Content: "Work on Diff duplicate Codex skills is ready for review.\n\nCurrent state: there are no longer two live imagegen copies.", At: now.Add(-time.Minute)},
		{Role: "user", Content: "the engineer has no memory?", At: now},
	}
	m.snapshot = StateSnapshot{
		OpenAgentTasks: []AgentTaskBrief{{
			ID:            "agt_diff",
			Title:         "Diff duplicate Codex skills",
			Status:        model.AgentTaskStatusWaiting,
			Summary:       "Current state: there are no longer two live imagegen copies.",
			LastTouchedAt: now.Add(-time.Minute),
		}},
	}

	rendered := ansi.Strip(m.renderTranscript(180))
	for _, want := range []string{"Boss> Work on Diff duplicate Codex skills is ready for review.", "You> the engineer has no memory?"} {
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
			Kind:        "project",
			ProjectPath: "/alpha",
			Title:       "Alpha",
			Provider:    model.SessionSourceCodex,
			Status:      "working",
			Active:      true,
			StartedAt:   now.Add(-30 * time.Minute),
			LastEventAt: now.Add(-11 * time.Minute),
		}},
	}

	rendered := ansi.Strip(m.deskContent(90, 12))
	for _, want := range []string{"Now", "quiet", "Work on Alpha has been quiet for 11:00"} {
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
			Kind:        "project",
			ProjectPath: "/alpha",
			Title:       "Alpha",
			Provider:    model.SessionSourceCodex,
			SessionID:   "thread-project-1",
			Status:      "working",
			Active:      true,
			StartedAt:   now.Add(-2 * time.Minute),
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
	for _, want := range []string{"Alt+1", "02:00", "working 02:00", "Alpha"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("active project attention row missing %q:\n%s", want, stripped)
		}
	}
}

func TestModelAttentionRowsPreferActiveEngineerSummary(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := New(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m.viewContext = ViewContext{
		EngineerActivities: []ViewEngineerActivity{{
			Kind:        "project",
			ProjectPath: "/alpha",
			Title:       "Alpha",
			Provider:    model.SessionSourceCodex,
			SessionID:   "thread-project-1",
			Status:      "working",
			Summary:     "Generated the helper for review.",
			Active:      true,
			StartedAt:   now.Add(-2 * time.Minute),
		}},
	}
	m.snapshot = StateSnapshot{
		HotProjects: []ProjectBrief{{
			Name:          "Alpha",
			Path:          "/alpha",
			Status:        model.StatusActive,
			LatestSummary: strings.Repeat("Vec2 pa {}; const auto drawRadius = radius * depthMul; ", 8),
		}},
	}

	rendered := ansi.Strip(m.renderAttentionRows(120, 1))
	if !strings.Contains(rendered, "Generated the helper for review.") {
		t.Fatalf("active project row missing live engineer summary:\n%s", rendered)
	}
	for _, unwanted := range []string{"Vec2 pa", "drawRadius"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("active project row leaked stale code summary %q:\n%s", unwanted, rendered)
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
		t.Fatalf("control result should not append a Chat turn, got %#v", got.messages)
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
		Kind:        "agent_task",
		TaskID:      "agt_skill_cleanup",
		Title:       "Retire projects-control-center skill",
		Provider:    model.SessionSourceCodex,
		SessionID:   "thread-skill-cleanup",
		Status:      "working",
		Active:      true,
		StartedAt:   now.Add(-3 * time.Second),
		LastEventAt: now.Add(-3 * time.Second),
	}

	updated, _ := m.Update(ControlInvocationResultMsg{
		Status:   "Work on Retire projects-control-center skill is underway.",
		Activity: &activity,
	})
	got := updated.(Model)
	if len(got.messages) != 1 {
		t.Fatalf("control result should not append a Chat turn, got %#v", got.messages)
	}
	rendered := ansi.Strip(got.renderTranscript(120))
	for _, want := range []string{"You> just nuke that skill"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("transcript missing saved turn %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Work on Retire projects-control-center skill is underway") {
		t.Fatalf("transient engineer feedback should stay out of transcript:\n%s", rendered)
	}
	desk := ansi.Strip(got.bossSidebarContent(90, 12))
	for _, want := range []string{"Now", "00:03", "Working on Retire projects-control-center skill"} {
		if !strings.Contains(desk, want) {
			t.Fatalf("transient engineer feedback missing from desk %q:\n%s", want, desk)
		}
	}
	log := ansi.Strip(got.bossLogContent(90, 8))
	for _, want := range []string{"Work started on Retire projects-control-center skill"} {
		if !strings.Contains(log, want) {
			t.Fatalf("transient engineer event missing from log %q:\n%s", want, log)
		}
	}
}

func TestControlResultCanAnnounceEngineerStartInChat(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := NewEmbedded(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m.messages = []ChatMessage{{Role: "user", Content: "just nuke that skill", At: now}}
	activity := ViewEngineerActivity{
		Kind:        "agent_task",
		TaskID:      "agt_skill_cleanup",
		Title:       "Retire projects-control-center skill",
		Provider:    model.SessionSourceCodex,
		SessionID:   "thread-skill-cleanup",
		Status:      "working",
		Active:      true,
		StartedAt:   now.Add(-3 * time.Second),
		LastEventAt: now.Add(-3 * time.Second),
	}

	updated, _ := m.Update(ControlInvocationResultMsg{
		Status:         "Work on Retire projects-control-center skill is underway.",
		Activity:       &activity,
		AnnounceInChat: true,
	})
	got := updated.(Model)
	if len(got.messages) != 2 {
		t.Fatalf("control result should append a Chat turn, got %#v", got.messages)
	}
	if got.messages[1].Kind != ChatMessageKindChat {
		t.Fatalf("control result message kind = %q, want chat", got.messages[1].Kind)
	}
	rendered := ansi.Strip(got.renderTranscript(120))
	for _, want := range []string{
		"Boss> Work on Retire projects-control-center skill is underway.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("transcript missing %q:\n%s", want, rendered)
		}
	}
	got.transcriptTab = bossTranscriptTabFlow
	flowRendered := ansi.Strip(got.renderTranscript(120))
	if strings.Contains(flowRendered, "Work on Retire projects-control-center skill is underway") {
		t.Fatalf("flow tab should not include control acknowledgements:\n%s", flowRendered)
	}
}

func TestControlResultEngineerLaunchReceiptPersistsAcrossReload(t *testing.T) {
	t.Parallel()

	svc := newBossSessionTestService(t)
	m := NewEmbeddedHelp(context.Background(), svc)
	loadedMsg := m.loadLatestBossSessionCmd()().(bossSessionLoadedMsg)
	updated, _ := m.Update(loadedMsg)
	m = updated.(Model)
	if m.sessionID == "" {
		t.Fatalf("session id is empty")
	}

	const receipt = "Codex AI engineer launched for Alpha TODO #42 in worktree alpha--help-feedback."
	updated, cmd := m.Update(ControlInvocationResultMsg{Status: receipt, AnnounceInChat: true})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("announced control result should return persistence command")
	}
	runBossTestCmd(cmd)

	_, messages, err := got.sessionStore.loadSession(context.Background(), got.sessionID)
	if err != nil {
		t.Fatalf("loadSession() error = %v", err)
	}
	if len(messages) != 1 || messages[0].Content != receipt || messages[0].Kind != ChatMessageKindChat {
		t.Fatalf("reloaded messages = %#v, want durable engineer receipt", messages)
	}
}

func runBossTestCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return
	}
	for _, nested := range batch {
		runBossTestCmd(nested)
	}
}

func TestControlResultDoesNotDuplicateMatchingErrorStatus(t *testing.T) {
	t.Parallel()

	const detail = "The embedded Codex engineer session is already running, so I did not send the prompt into it. Start a fresh session or open the target session and send manually."
	m := NewEmbedded(context.Background(), nil)

	updated, _ := m.Update(ControlInvocationResultMsg{
		Status:         detail,
		Err:            fmt.Errorf("%s", detail),
		AnnounceInChat: true,
	})
	got := updated.(Model)
	if len(got.messages) != 1 {
		t.Fatalf("messages = %d, want one announced failure", len(got.messages))
	}
	if got.messages[0].Kind != ChatMessageKindChat {
		t.Fatalf("failure message kind = %q, want chat", got.messages[0].Kind)
	}
	content := got.messages[0].Content
	if strings.Count(content, detail) != 1 {
		t.Fatalf("content repeated failure detail %d times: %q", strings.Count(content, detail), content)
	}
}

func TestAssistantViewContextIncludesTransientEngineerActivity(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := NewEmbedded(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m = m.recordTransientEngineerActivity(ViewEngineerActivity{
		Kind:        "project",
		ProjectPath: "/tmp/oyk-aso",
		Title:       "oyk-aso",
		Provider:    model.SessionSourceCodex,
		SessionID:   "thread-oyk",
		Status:      "working",
		Active:      true,
		StartedAt:   now.Add(-15 * time.Second),
		LastEventAt: now.Add(-15 * time.Second),
	})

	brief := BuildViewContextBrief(m.assistantViewContext(), now)
	for _, want := range []string{"active engineer work", "oyk-aso: working", "via codex"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("assistant view context missing %q:\n%s", want, brief)
		}
	}
}

func TestBossLogShowsHostAndStateEvents(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	m := New(context.Background(), nil)
	m.nowFn = func() time.Time { return now }
	m.appendDeskEvent("host", "update", "Ken finished ChatNext3: fixed SVG serving issue.")
	m.appendDeskEvent("project", "update", "ChatNext3 - SVG preview repaired.")
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

	log := ansi.Strip(m.bossLogContent(120, 8))
	for _, want := range []string{
		"Ken finished ChatNext3: fixed SVG serving issue.",
		"ChatNext3 - SVG preview repaired.",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("boss log missing %q:\n%s", want, log)
		}
	}
	desk := ansi.Strip(m.bossSidebarContent(80, 14))
	for _, want := range []string{
		"Needs You",
		"Diff duplicate Codex skills - Canonical copy kept.",
	} {
		if !strings.Contains(desk, want) {
			t.Fatalf("boss desk missing %q:\n%s", want, desk)
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
	if got.status != "Copied full Chat input to clipboard" {
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

func TestModelAltOOpensFilePickerForMarkdownLinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	m := New(context.Background(), nil)
	m.width = 100
	m.height = 28
	m.messages = []ChatMessage{{
		Role:    "assistant",
		Content: "Outputs:\n- [docs](https://example.com/docs)\n- [notes](" + path + ")",
	}}
	m.syncLayout(true)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("alt+o should open the picker without a command")
	}
	got := updated.(Model)
	if got.openTargetPicker == nil {
		t.Fatalf("alt+o should open the file picker")
	}
	if len(got.openTargetPicker.Targets) != 2 {
		t.Fatalf("targets = %#v, want two links", got.openTargetPicker.Targets)
	}
	if !strings.Contains(ansi.Strip(got.View()), "Open Files") {
		t.Fatalf("view should render the file picker:\n%s", got.View())
	}

	oldPathOpener := bossExternalPathOpener
	defer func() { bossExternalPathOpener = oldPathOpener }()
	opened := ""
	bossExternalPathOpener = func(path string) error {
		opened = path
		return nil
	}

	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should return an open command")
	}
	msg := cmd()
	if opened != path {
		t.Fatalf("opened path = %q, want %q", opened, path)
	}
	openMsg, ok := msg.(bossOpenTargetOpenedMsg)
	if !ok {
		t.Fatalf("open command returned %T, want bossOpenTargetOpenedMsg", msg)
	}
	updated, cmd = got.Update(openMsg)
	if cmd != nil {
		t.Fatalf("open result should not queue a command")
	}
	got = updated.(Model)
	if got.status != "Opened file" {
		t.Fatalf("status = %q, want Opened file", got.status)
	}
}

func TestModelAltOOpensPercentEscapedLocalPathsAndFolders(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Family Room", "jun_it_citizenship")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	path := filepath.Join(dir, "Italian B1 certificate.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	encodedPath := strings.ReplaceAll(path, " ", "%20")

	m := New(context.Background(), nil)
	m.width = 100
	m.height = 28
	m.messages = []ChatMessage{{
		Role:    "assistant",
		Content: "June IT citizenship docs:\n- [Italian B1 certificate](" + encodedPath + ")",
	}}
	m.syncLayout(true)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("alt+o should open the picker without a command")
	}
	got := updated.(Model)
	if got.openTargetPicker == nil || len(got.openTargetPicker.Targets) != 1 {
		t.Fatalf("targets = %#v, want one decoded local PDF", got.openTargetPicker)
	}
	target := got.openTargetPicker.Targets[0]
	if target.Kind != "pdf" || target.Path != path {
		t.Fatalf("target = %#v, want kind pdf path %q", target, path)
	}
	if strings.Contains(ansi.Strip(got.View()), "%20") {
		t.Fatalf("file picker should show decoded local paths, view:\n%s", got.View())
	}

	oldPathOpener := bossExternalPathOpener
	oldPathRevealer := bossExternalPathRevealer
	defer func() { bossExternalPathOpener = oldPathOpener }()
	defer func() { bossExternalPathRevealer = oldPathRevealer }()
	opened := ""
	revealed := ""
	bossExternalPathOpener = func(path string) error {
		opened = path
		return nil
	}
	bossExternalPathRevealer = func(path string) error {
		revealed = path
		return nil
	}

	folderModel := got
	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("enter should return an open command")
	}
	msg := cmd()
	if opened != path {
		t.Fatalf("opened path = %q, want decoded file path %q", opened, path)
	}
	openMsg, ok := msg.(bossOpenTargetOpenedMsg)
	if !ok {
		t.Fatalf("open command returned %T, want bossOpenTargetOpenedMsg", msg)
	}
	if openMsg.err != nil || openMsg.status != "Opened file" {
		t.Fatalf("open message = %#v, want successful file open", openMsg)
	}
	_ = updated

	revealed = ""
	updated, cmd = folderModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd == nil {
		t.Fatalf("f should return a containing-folder open command")
	}
	msg = cmd()
	if revealed != path {
		t.Fatalf("revealed path = %q, want decoded file path %q", revealed, path)
	}
	openMsg, ok = msg.(bossOpenTargetOpenedMsg)
	if !ok {
		t.Fatalf("folder open command returned %T, want bossOpenTargetOpenedMsg", msg)
	}
	if openMsg.err != nil || openMsg.status != "Opened containing folder" {
		t.Fatalf("folder open message = %#v, want successful folder open", openMsg)
	}
	_ = updated
}

func TestModelAltOWithoutLinksLeavesPickerClosed(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.messages = []ChatMessage{{Role: "assistant", Content: "No artifacts here."}}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("alt+o without links should not queue a command")
	}
	got := updated.(Model)
	if got.openTargetPicker != nil {
		t.Fatalf("file picker should stay closed")
	}
	if got.status != "No files or links in this Chat" {
		t.Fatalf("status = %q, want no-links notice", got.status)
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
	if got.status != "Ready to send to engineer with Enter, or Esc to cancel" {
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
	if !strings.Contains(rendered, "Engineer Handoff") {
		t.Fatalf("rendered view should show control confirmation dialog, got %q", rendered)
	}
	if !strings.Contains(rendered, "Routine handoff") || !strings.Contains(rendered, "Enter") || !strings.Contains(rendered, "Esc") {
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
		t.Fatalf("cancel should not append a Chat turn, got %#v", got.messages)
	}
	if len(got.operationalNotices) != 1 || got.operationalNotices[0].Code != "control_canceled" {
		t.Fatalf("cancel notice = %#v, want one operational cancellation notice", got.operationalNotices)
	}
}

func TestEmbeddedModelConfirmsGoalRun(t *testing.T) {
	t.Parallel()

	proposal := bossGoalProposalForTest(t)
	m := NewEmbedded(context.Background(), nil)

	updated, cmd := m.Update(AssistantReplyMsg{
		response: AssistantResponse{
			Content:      proposal.Preview,
			GoalProposal: &proposal,
		},
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("assistant goal proposal should only update local confirmation state")
	}
	if got.pendingGoal == nil {
		t.Fatalf("pendingGoal = nil, want confirmation state")
	}
	if got.pendingControl != nil {
		t.Fatalf("pendingControl = %#v, want no single control proposal", got.pendingControl)
	}
	if got.status != "Confirm goal run with Enter, or Esc to cancel" {
		t.Fatalf("status = %q", got.status)
	}

	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.pendingGoal != nil {
		t.Fatalf("pendingGoal should clear after confirmation")
	}
	if cmd == nil {
		t.Fatalf("confirmation should emit a host command")
	}
	msg := cmd()
	confirmed, ok := msg.(GoalRunConfirmedMsg)
	if !ok {
		t.Fatalf("confirmation command returned %T, want GoalRunConfirmedMsg", msg)
	}
	if confirmed.Proposal.Run.Kind != bossrun.GoalKindAgentTaskCleanup {
		t.Fatalf("confirmed goal kind = %q", confirmed.Proposal.Run.Kind)
	}
	if ids := bossrun.AgentTaskResourceIDs(confirmed.Proposal.Authority.Resources); len(ids) != 2 {
		t.Fatalf("confirmed resources = %#v, want two task ids", ids)
	}
}

func TestEmbeddedModelRendersGoalConfirmationDialog(t *testing.T) {
	t.Parallel()

	proposal := bossGoalProposalForTest(t)
	m := NewEmbedded(context.Background(), nil)
	m.width = 96
	m.height = 28
	updated, _ := m.Update(AssistantReplyMsg{
		response: AssistantResponse{
			Content:      proposal.Preview,
			GoalProposal: &proposal,
		},
	})
	got := updated.(Model)

	rendered := ansi.Strip(got.View())
	if !strings.Contains(rendered, "Confirm Goal Run") {
		t.Fatalf("rendered view should show goal confirmation dialog, got %q", rendered)
	}
	if !strings.Contains(rendered, "Scoped goal") || !strings.Contains(rendered, "agt_one") || !strings.Contains(rendered, "agt_two") {
		t.Fatalf("rendered dialog should show goal framing and resources, got %q", rendered)
	}
	if len(got.messages) != 0 {
		t.Fatalf("goal proposal preview should not be saved as normal chat, got %#v", got.messages)
	}
}

func TestEmbeddedModelCanCancelGoalRun(t *testing.T) {
	t.Parallel()

	proposal := bossGoalProposalForTest(t)
	m := NewEmbedded(context.Background(), nil)
	updated, _ := m.Update(AssistantReplyMsg{
		response: AssistantResponse{
			Content:      proposal.Preview,
			GoalProposal: &proposal,
		},
	})
	got := updated.(Model)

	updated, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got = updated.(Model)
	if got.pendingGoal != nil {
		t.Fatalf("pendingGoal should clear after cancel")
	}
	if cmd != nil {
		t.Fatalf("cancel should not emit a host command")
	}
	if got.status != "Goal run canceled" {
		t.Fatalf("status = %q", got.status)
	}
	if len(got.messages) != 0 {
		t.Fatalf("cancel should not append a Chat turn, got %#v", got.messages)
	}
	if len(got.operationalNotices) != 1 || got.operationalNotices[0].Code != "goal_canceled" {
		t.Fatalf("cancel notice = %#v, want one goal cancellation notice", got.operationalNotices)
	}
}

func TestEmbeddedModelAltUpExitsLikeSilentEsc(t *testing.T) {
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
		t.Fatalf("alt+up command returned %T, want ExitMsg", msg)
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
			renderedLineCount(m.renderBossLog(layout.width, layout.bottomHeight)))
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
		m.status = "Chat via gpt-5.4-mini"

		layout := m.layout()
		renderedHeight := layout.topHeight + layout.middleGapHeight + layout.bottomHeight
		if renderedHeight != layout.height {
			t.Fatalf("embedded layout should use the full host body height, got rendered height %d terminal height %d", renderedHeight, layout.height)
		}
		if layout.middleGapHeight != 0 {
			t.Fatalf("height %d should not insert a separator row between panel bands, got gap %d", height, layout.middleGapHeight)
		}
		if layout.bottomHeight > bossLogTargetHeight(layout.height, true) {
			t.Fatalf("height %d bottom log = %d, want <= %d", height, layout.bottomHeight, bossLogTargetHeight(layout.height, true))
		}
		if previousTopHeight > 0 && layout.topHeight <= previousTopHeight {
			t.Fatalf("height %d top panel height = %d, want chat row to gain spare terminal height beyond %d", height, layout.topHeight, previousTopHeight)
		}
		previousTopHeight = layout.topHeight
	}
}

func TestEmbeddedModelUsesSidebarAndBottomLog(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 180
	m.height = 42

	layout := m.layout()
	if layout.sidebarWidth == 0 {
		t.Fatalf("wide embedded layout should allocate a Boss Desk sidebar: %+v", layout)
	}
	if layout.chatWidth+layout.sidebarWidth != layout.width {
		t.Fatalf("top row widths = chat %d + sidebar %d, want terminal width %d", layout.chatWidth, layout.sidebarWidth, layout.width)
	}
	if layout.bottomHeight < 6 {
		t.Fatalf("boss log height = %d, want a compact bottom log", layout.bottomHeight)
	}
	if layout.topHeight+layout.middleGapHeight+layout.bottomHeight != layout.height {
		t.Fatalf("layout heights = top %d + gap %d + bottom %d, want %d", layout.topHeight, layout.middleGapHeight, layout.bottomHeight, layout.height)
	}
}

func TestBossDeskGetsWiderSidebarInEmbeddedLayout(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		width       int
		wantSidebar int
		wantChat    int
	}{
		{width: 96, wantSidebar: 38, wantChat: 58},
		{width: 120, wantSidebar: 48, wantChat: 72},
		{width: 180, wantSidebar: 68, wantChat: 112},
	} {
		m := NewEmbedded(context.Background(), nil)
		m.width = tc.width
		m.height = 24

		layout := m.layout()
		if layout.sidebarWidth != tc.wantSidebar || layout.chatWidth != tc.wantChat {
			t.Fatalf("width %d allocated chat %d sidebar %d, want chat %d sidebar %d", tc.width, layout.chatWidth, layout.sidebarWidth, tc.wantChat, tc.wantSidebar)
		}
	}
}

func TestBossDeskSectionHeadersUseNonCyanBackgroundBand(t *testing.T) {
	t.Parallel()

	header := renderBossDeskSectionHeader("Needs You", 24)
	if got := ansi.StringWidth(ansi.Strip(header)); got != 24 {
		t.Fatalf("header width = %d, want 24: %q", got, ansi.Strip(header))
	}
	if !strings.Contains(ansi.Strip(header), " Needs You") {
		t.Fatalf("header should keep the section title with left padding: %q", ansi.Strip(header))
	}
	if fmt.Sprint(bossDeskSectionStyle.GetForeground()) == fmt.Sprint(bossPanelAccent) {
		t.Fatalf("boss desk section header should not reuse the cyan panel accent")
	}
	if fmt.Sprint(bossDeskSectionStyle.GetBackground()) == fmt.Sprint(bossPanelBackground) {
		t.Fatalf("boss desk section header should have a distinct background band")
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
	m.status = "Chat via gpt-5.4-mini"
	m.syncLayout(true)

	layout := m.layout()
	if layout.middleGapHeight != 0 {
		t.Fatalf("medium-width layout should not use a vertical separator row, got %d", layout.middleGapHeight)
	}
	if layout.bottomHeight < 6 {
		t.Fatalf("medium-width boss log height = %d, want compact bottom panel", layout.bottomHeight)
	}
	if layout.sidebarWidth == 0 || layout.chatWidth+layout.sidebarWidth != layout.width {
		t.Fatalf("medium-width layout should use chat plus sidebar, got chat %d sidebar %d terminal %d", layout.chatWidth, layout.sidebarWidth, layout.width)
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
		t.Fatalf("bottom log should keep its bottom border visible, got %q", lines[bottomBorderLine])
	}
	if bottomBorderLine != len(lines)-1 {
		t.Fatalf("bottom log should finish on the final embedded body row, got border row %d line count %d", bottomBorderLine, len(lines))
	}
}

func TestChatPanelKeepsStyledTranscriptAndInputVisible(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 82
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

func TestChatInputExpandsForMultilineDraftsWithoutSeparator(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 82
	m.height = 20
	m.stateLoaded = true
	m.input.SetValue("line 1\nline 2\nline 3\nline 4")
	m.syncLayout(true)

	layout := m.layout()
	if layout.inputEditorHeight <= bossInputMinEditorHeight {
		t.Fatalf("multiline input editor height = %d, want growth beyond %d", layout.inputEditorHeight, bossInputMinEditorHeight)
	}
	if layout.inputHeight != bossInputBlockHeight(layout.inputEditorHeight) {
		t.Fatalf("input block height = %d, want editor height plus composer chrome", layout.inputHeight)
	}
	if m.input.Height() != layout.inputEditorHeight {
		t.Fatalf("textarea height = %d, want layout editor height %d", m.input.Height(), layout.inputEditorHeight)
	}

	rendered := ansi.Strip(m.renderChat(layout))
	if strings.Contains(rendered, "----") || strings.Contains(rendered, "4 lines") {
		t.Fatalf("multiline input should not render the old separator chrome:\n%s", rendered)
	}
	if !strings.Contains(rendered, "| line 2") {
		t.Fatalf("continuation prompt should make additional lines visible:\n%s", rendered)
	}
}

func TestChatInputCapsGrowthWithoutSeparator(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 82
	m.height = 20
	m.stateLoaded = true
	m.input.SetValue(strings.Join([]string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"line 5",
		"line 6",
		"line 7",
		"line 8",
	}, "\n"))
	m.syncLayout(true)

	layout := m.layout()
	if layout.inputEditorHeight > bossInputMaxEditorHeight {
		t.Fatalf("input editor height = %d, want <= %d", layout.inputEditorHeight, bossInputMaxEditorHeight)
	}
	rendered := ansi.Strip(m.renderChat(layout))
	if strings.Contains(rendered, "----") || strings.Contains(rendered, "8 lines +") {
		t.Fatalf("overflowing multiline input should not render the old separator chrome:\n%s", rendered)
	}
}

func TestChatPanelDoesNotRenderCompanionInTranscriptSpace(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 82
	m.height = 20
	m.stateLoaded = true
	m.syncLayout(true)

	rendered := m.renderChat(m.layout())
	if strings.Contains(rendered, "\x1b[38;2;") || strings.Contains(ansi.Strip(rendered), "\u2580") {
		t.Fatalf("chat panel should not render the companion; Boss Desk owns it now:\n%s", ansi.Strip(rendered))
	}
}

func TestWideChatPanelDoesNotRenderCompanionWithSidebar(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 120
	m.height = 24
	m.stateLoaded = true
	m.syncLayout(true)

	layout := m.layout()
	if layout.sidebarWidth == 0 {
		t.Fatalf("test setup should allocate a sidebar: %+v", layout)
	}
	rendered := m.renderChat(layout)
	if strings.Contains(rendered, "\x1b[38;2;") || strings.Contains(ansi.Strip(rendered), "\u2580") {
		t.Fatalf("wide chat panel should leave the companion to Boss Desk:\n%s", ansi.Strip(rendered))
	}
}

func TestBossSidebarRendersNativeCompanion(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 120
	m.height = 24
	m.stateLoaded = true
	m.syncLayout(true)

	layout := m.layout()
	if layout.sidebarWidth == 0 {
		t.Fatalf("test setup should allocate a sidebar: %+v", layout)
	}
	rendered := m.renderBossSidebar(layout.sidebarWidth, layout.topHeight)
	if !strings.Contains(rendered, "\x1b[38;2;") {
		t.Fatalf("sidebar companion should use truecolor pixels:\n%s", ansi.Strip(rendered))
	}
	if !strings.Contains(rendered, "\x1b[48;2;0;0;0m") {
		t.Fatalf("sidebar companion should paint its own panel background:\n%s", ansi.Strip(rendered))
	}
	if !strings.Contains(ansi.Strip(rendered), "\u2580") {
		t.Fatalf("sidebar companion should render half-block pixels:\n%s", ansi.Strip(rendered))
	}
	if strings.Contains(ansi.Strip(rendered), "\u2588") {
		t.Fatalf("sidebar companion should not render full-block avatar pixels:\n%s", ansi.Strip(rendered))
	}
}

func TestBossSidebarShowsOpenTodos(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.snapshot = StateSnapshot{
		OpenTodos: []TodoBrief{{
			ID:          42,
			ProjectName: "Alpha",
			ProjectPath: "/tmp/alpha",
			Label:       "boss desk todos",
			Text:        "Add Boss Desk TODO visibility.",
		}},
	}

	rendered := strings.Join(m.bossSidebarLines(80, 12), "\n")
	stripped := ansi.Strip(rendered)
	for _, want := range []string{"TODOs", "#42", "Alpha - boss desk todos"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("Boss Desk missing TODO text %q:\n%s", want, stripped)
		}
	}
	if strings.Contains(stripped, "Add Boss Desk TODO visibility.") {
		t.Fatalf("Boss Desk should prefer the short TODO label:\n%s", stripped)
	}
	if strings.Contains(stripped, "todo Alpha") {
		t.Fatalf("Boss Desk should use the TODO id in the label column:\n%s", stripped)
	}
	if want := bossProjectIdentityStyle("/tmp/alpha", bossProjectNameStyle).Render("Alpha"); !strings.Contains(rendered, want) {
		t.Fatalf("Boss Desk should color project names; missing styled project label:\n%s", stripped)
	}
}

func TestBossCompanionUsesSharedStationScene(t *testing.T) {
	t.Parallel()

	sprite := renderBossCompanionSprite(bossCompanionIdle, 0, 36)
	if got, want := sprite.width, 36; got != want {
		t.Fatalf("boss companion width = %d, want panel width %d", got, want)
	}
	if got, want := sprite.height, pixelart.OperatorStationHeight; got != want {
		t.Fatalf("boss companion height = %d, want shared station height %d", got, want)
	}
}

func TestBossCompanionWalksAcrossSharedStationScene(t *testing.T) {
	t.Parallel()

	operator := func(frame int) pixelart.OperatorState {
		return pixelart.OperatorStationOperatorState(bossCompanionStationState(bossCompanionIdle, frame, 36))
	}
	left := operator(0)
	stepOne := operator(6)
	stepTwo := operator(8)
	stepThree := operator(10)
	right := operator(12)
	returnStepThree := operator(22)
	returnStepTwo := operator(24)
	backAtLeft := operator(31)

	if left.Pose != pixelart.OperatorInspect || left.Facing != -1 {
		t.Fatalf("frame 0 = %+v, want inspect pose facing left", left)
	}
	if right.Pose != pixelart.OperatorTypeA || right.Facing != 1 {
		t.Fatalf("frame 12 = %+v, want typing pose facing right", right)
	}
	if !(left.X < stepOne.X && stepOne.X < stepTwo.X && stepTwo.X < stepThree.X && stepThree.X <= right.X) {
		t.Fatalf("boss companion should walk right: left=%d step1=%d step2=%d step3=%d right=%d", left.X, stepOne.X, stepTwo.X, stepThree.X, right.X)
	}
	if !(returnStepThree.X < right.X && returnStepTwo.X < returnStepThree.X && backAtLeft.X <= returnStepTwo.X) {
		t.Fatalf("boss companion should walk left: right=%d step3=%d step2=%d left=%d", right.X, returnStepThree.X, returnStepTwo.X, backAtLeft.X)
	}
}

func TestBossCompanionUsesMinimumSharedStationWidth(t *testing.T) {
	t.Parallel()

	sprite := renderBossCompanionSprite(bossCompanionIdle, 0, 12)
	if got, want := sprite.width, pixelart.OperatorStationWidth; got != want {
		t.Fatalf("boss companion width = %d, want shared station width %d", got, want)
	}
	if got, want := sprite.height, pixelart.OperatorStationHeight; got != want {
		t.Fatalf("boss companion height = %d, want shared station height %d", got, want)
	}
}

func TestBossSidebarCompanionRowsFillPanelWidth(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	rows := m.bossSidebarCompanionLines(36, 20)
	if len(rows) == 0 {
		t.Fatalf("expected sidebar companion rows")
	}
	for i, row := range rows {
		if got, want := ansi.StringWidth(ansi.Strip(row)), 36; got != want {
			t.Fatalf("sidebar companion row %d width = %d, want panel width %d", i, got, want)
		}
	}
}

func TestBossCompanionHalfRowsStayNativeSize(t *testing.T) {
	t.Parallel()

	sprite := renderBossCompanionSprite(bossCompanionIdle, 0, 36)
	rows := sprite.renderHalfRows()
	if len(rows) == 0 {
		t.Fatalf("expected companion rows")
	}
	if got, want := len(rows), (sprite.height+1)/2; got != want {
		t.Fatalf("companion rows = %d, want native half-block height %d", got, want)
	}
	rowWidth := 0
	for _, row := range rows {
		rowWidth = maxInt(rowWidth, ansi.StringWidth(ansi.Strip(row)))
	}
	if rowWidth != sprite.width {
		t.Fatalf("companion width = %d, want native width %d", rowWidth, sprite.width)
	}
	stripped := ansi.Strip(strings.Join(rows, "\n"))
	if !strings.Contains(stripped, "\u2580") || !strings.Contains(stripped, "\u2584") {
		t.Fatalf("companion should use upper and lower half-block pixels:\n%s", stripped)
	}
	if strings.Contains(stripped, "\u2588") {
		t.Fatalf("companion should not use full-block avatar pixels:\n%s", stripped)
	}
}

func TestBossSidebarHidesNativeCompanionWhenHeightIsTooTight(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	if rows := m.bossSidebarCompanionLines(35, 6); len(rows) != 0 {
		t.Fatalf("sidebar companion should hide when native rows do not fit, got %d rows", len(rows))
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
	if strings.Contains(rendered, "\x1b[38;2;") || strings.Contains(ansi.Strip(rendered), "\u2580") {
		t.Fatalf("chat panel should not force the companion into a cramped transcript:\n%s", ansi.Strip(rendered))
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

func TestModelTranscriptSeparatesChatAndFlowMessages(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.messages = []ChatMessage{{
		Role:    "user",
		Content: "Keep this focused on Alpha.",
	}, {
		Role:    "assistant",
		Content: "Work on the noisy flow is ready for review.",
		Kind:    ChatMessageKindFlow,
	}, {
		Role:    "assistant",
		Content: "Alpha is ready for review.",
	}}

	chat := ansi.Strip(m.renderTranscript(120))
	if strings.Contains(chat, "noisy flow") {
		t.Fatalf("chat tab should hide flow notices:\n%s", chat)
	}
	for _, want := range []string{"You> Keep this focused on Alpha.", "Boss> Alpha is ready for review."} {
		if !strings.Contains(chat, want) {
			t.Fatalf("chat tab missing %q:\n%s", want, chat)
		}
	}

	m.transcriptTab = bossTranscriptTabFlow
	flow := ansi.Strip(m.renderTranscript(120))
	if !strings.Contains(flow, "Flow> Work on the noisy flow is ready for review.") {
		t.Fatalf("flow tab missing flow notice:\n%s", flow)
	}
	for _, unwanted := range []string{"Keep this focused on Alpha.", "Alpha is ready for review."} {
		if strings.Contains(flow, unwanted) {
			t.Fatalf("flow tab should hide chat message %q:\n%s", unwanted, flow)
		}
	}
}

func TestModelTranscriptTabSwitchAnnouncesAndUsesProjectTabStyle(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	title := m.renderTranscriptPanelTitle()
	if !strings.Contains(title, bossTranscriptActiveTabStyle.Render("[Chat]")) ||
		!strings.Contains(title, bossTranscriptInactiveTabStyle.Render(" Flow ")) {
		t.Fatalf("chat title should render styled Chat/Flow tabs: %q", title)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if cmd != nil {
		t.Fatalf("tab switch should not return a command")
	}
	got := updated.(Model)
	if got.normalizedTranscriptTab() != bossTranscriptTabFlow {
		t.Fatalf("tab = %q, want flow", got.normalizedTranscriptTab())
	}
	if got.status != "Switched to Boss Flow tab" {
		t.Fatalf("status = %q, want switch announcement", got.status)
	}
	title = got.renderTranscriptPanelTitle()
	if !strings.Contains(title, bossTranscriptInactiveTabStyle.Render(" Chat ")) ||
		!strings.Contains(title, bossTranscriptActiveTabStyle.Render("[Flow]")) {
		t.Fatalf("flow title should render styled Chat/Flow tabs: %q", title)
	}
}

func TestModelTranscriptRendersMarkdownLinksCompactly(t *testing.T) {
	t.Parallel()

	path := "/Users/davide/dev/repos/FractalMech/captures/promo-comparisons/promo-old-vs-new-autoplay-20260505.mp4"
	m := New(context.Background(), nil)
	m.messages = []ChatMessage{{
		Role:    "assistant",
		Content: "Outputs:\n- [side-by-side video](" + path + ")\n- [docs](https://example.com/docs)",
	}}

	rendered := m.renderTranscript(160)
	if !strings.Contains(rendered, ansi.SetHyperlink(path)) {
		t.Fatalf("rendered transcript should keep the local file as a hyperlink target:\n%q", rendered)
	}
	if !strings.Contains(rendered, ansi.SetHyperlink("https://example.com/docs")) {
		t.Fatalf("rendered transcript should keep external URLs clickable:\n%q", rendered)
	}
	if strings.Contains(rendered, "file://") {
		t.Fatalf("rendered transcript should not use file URLs for local paths:\n%q", rendered)
	}

	stripped := ansi.Strip(rendered)
	for _, unwanted := range []string{path, "https://example.com/docs", "[side-by-side video]", "(https://example.com/docs)"} {
		if strings.Contains(stripped, unwanted) {
			t.Fatalf("rendered transcript should hide markdown target %q:\n%s", unwanted, stripped)
		}
	}
	for _, want := range []string{"Boss>", "Outputs:", "side-by-side video", "docs"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("rendered transcript missing %q:\n%s", want, stripped)
		}
	}
}

func TestModelTranscriptColorsProjectNames(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.snapshot = StateSnapshot{HotProjects: []ProjectBrief{{
		Name: "Alpha Project",
		Path: "/alpha",
	}}}
	m.messages = []ChatMessage{{
		Role:    "assistant",
		Content: "Alpha Project is waiting on review.",
	}, {
		Role:    "user",
		Content: "Check Alpha Project before lunch.",
	}}
	m.sending = true
	m.streamingAssistantText = "Alpha Project has one open decision."

	rendered := m.renderTranscript(100)
	coloredProject := bossProjectIdentityStyle("/alpha", bossProjectNameStyle).Render("Alpha Project")
	if got := strings.Count(rendered, coloredProject); got != 3 {
		t.Fatalf("transcript colored Alpha Project %d times, want assistant, user, and streaming mentions:\n%s", got, ansi.Strip(rendered))
	}
}

func TestProjectTextHighlightsDoNotColorPartialWords(t *testing.T) {
	t.Parallel()

	if projectNameMentionBoundary("Alphabet soup", 0, len("Alpha")) {
		t.Fatalf("project mention boundary should reject the start of Alphabet")
	}
	text := "Check Alpha."
	if !projectNameMentionBoundary(text, len("Check "), len("Check Alpha")) {
		t.Fatalf("project mention boundary should accept standalone Alpha")
	}
}

func TestModelTranscriptHighlightsWorkReturnNotice(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	fixed := time.Date(2024, 6, 15, 14, 30, 0, 0, time.UTC)
	m.nowFn = func() time.Time { return fixed }
	updated, _ := m.Update(HostNoticeMsg{
		Content:        "Work on Cursor cleanup is ready for review: Cursor access still needs user-side confirmation.",
		AnnounceInChat: true,
		Handoff:        &HandoffHighlight{ProjectLabel: "Cursor cleanup"},
	})
	got := updated.(Model)
	got.transcriptTab = bossTranscriptTabFlow

	rendered := got.renderTranscript(120)
	stripped := ansi.Strip(rendered)
	want := "14:30:00 Work on Cursor cleanup is ready for review: Cursor access still needs user-side confirmation."
	if !strings.Contains(stripped, want) {
		t.Fatalf("rendered transcript missing compact handoff %q:\n%s", want, stripped)
	}
	if strings.Contains(stripped, "Cursor cleanup.\n\nCursor access") {
		t.Fatalf("engineer return notice should keep output on the first line:\n%s", stripped)
	}
	for label, want := range map[string]string{
		"project": bossProjectIdentityStyle("Cursor cleanup", bossHandoffProjectLabelStyle).Render("Cursor cleanup"),
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered transcript missing %s highlight:\n%s", label, stripped)
		}
	}

	loaded := NewEmbedded(context.Background(), nil)
	loaded.transcriptTab = bossTranscriptTabFlow
	loaded.messages = []ChatMessage{{
		Role:    "assistant",
		Content: "Work on Cursor cleanup is ready for review: Cursor access still needs user-side confirmation.",
		Kind:    ChatMessageKindFlow,
	}}
	loadedRendered := loaded.renderTranscript(120)
	for label, want := range map[string]string{
		"loaded project": bossProjectIdentityStyle("Cursor cleanup", bossHandoffProjectLabelStyle).Render("Cursor cleanup"),
	} {
		if !strings.Contains(loadedRendered, want) {
			t.Fatalf("loaded transcript missing %s highlight:\n%s", label, ansi.Strip(loadedRendered))
		}
	}
}

func TestModelTranscriptFlowDateSeparator(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.transcriptTab = bossTranscriptTabFlow
	m.messages = []ChatMessage{
		{Role: "assistant", Content: "First day message.", Kind: ChatMessageKindFlow, At: time.Date(2024, 6, 14, 23, 59, 0, 0, time.UTC)},
		{Role: "assistant", Content: "Second day message.", Kind: ChatMessageKindFlow, At: time.Date(2024, 6, 15, 0, 1, 0, 0, time.UTC)},
	}

	rendered := ansi.Strip(m.renderTranscript(120))
	if !strings.Contains(rendered, "23:59:00 First day message.") {
		t.Fatalf("missing first message:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Saturday, June 15, 2024") {
		t.Fatalf("missing date separator:\n%s", rendered)
	}
	if !strings.Contains(rendered, "00:01:00 Second day message.") {
		t.Fatalf("missing second message:\n%s", rendered)
	}
}

func TestModelTranscriptKeepsChatBackgroundFlat(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.messages = []ChatMessage{{
		Role:    "assistant",
		Content: "Use `boss` mode.",
	}, {
		Role:    "user",
		Content: "Ok, `commit` it.",
	}}

	rendered := m.renderTranscript(72)
	for _, unwanted := range []string{
		"\x1b[48;2;16;16;16m",
		"\x1b[48;5;236m",
	} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("transcript should not render separate chat text backgrounds %q:\n%s", unwanted, ansi.Strip(rendered))
		}
	}
	if !strings.Contains(ansi.Strip(rendered), "You>") || !strings.Contains(ansi.Strip(rendered), "Boss>") {
		t.Fatalf("transcript lost speaker labels:\n%s", ansi.Strip(rendered))
	}
	for i, line := range strings.Split(ansi.Strip(rendered), "\n") {
		if strings.TrimSpace(line) == "" {
			t.Fatalf("transcript should not insert blank lines between entries; blank line at %d:\n%s", i, ansi.Strip(rendered))
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
			Kind:      "agent_task",
			TaskID:    "agt_demo",
			Title:     "Diff duplicate Codex skills",
			Provider:  model.SessionSourceCodex,
			Status:    "working",
			Active:    true,
			StartedAt: now.Add(-9 * time.Second),
		}},
	}

	rendered := ansi.Strip(m.renderTranscript(90))
	for _, want := range []string{"Tool calls", "agent_task_report"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("thinking transcript missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Work on Diff duplicate Codex skills is underway") || strings.Contains(rendered, "Supervisor") {
		t.Fatalf("thinking transcript should not expose supervisor chrome:\n%s", rendered)
	}
	desk := ansi.Strip(m.deskContent(90, 12))
	for _, want := range []string{"Now", "00:09", "Working on Diff duplicate Codex skills"} {
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

func bossGoalProposalForTest(t *testing.T) bossrun.GoalProposal {
	t.Helper()
	proposal, err := bossrun.NormalizeGoalProposal(bossrun.GoalProposal{
		Run: bossrun.GoalRun{
			Kind:      bossrun.GoalKindAgentTaskCleanup,
			Title:     "Clear stale delegated agents",
			Objective: "Archive stale delegated agent task records that have served their scope.",
		},
		ArchiveResources: []control.ResourceRef{
			{Kind: control.ResourceAgentTask, ID: "agt_one", Label: "old review"},
			{Kind: control.ResourceAgentTask, ID: "agt_two", Label: "old follow-up"},
		},
	})
	if err != nil {
		t.Fatalf("NormalizeGoalProposal() error = %v", err)
	}
	return proposal
}

func renderedLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(ansi.Strip(s), "\n") + 1
}
