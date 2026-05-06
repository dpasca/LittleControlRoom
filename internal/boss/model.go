package boss

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/bossrun"
	"lcroom/internal/inputcomposer"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/terminalmd"
	"lcroom/internal/viewportnav"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	defaultBossWidth             = 112
	defaultBossHeight            = 32
	summaryFlashDuration         = time.Second
	maxOperationalNotices        = 8
	transientEngineerActivityTTL = 2 * time.Minute
)

type Model struct {
	ctx       context.Context
	svc       *service.Service
	assistant *Assistant
	embedded  bool

	width  int
	height int

	input             textarea.Model
	inputCopyDialog   *inputcomposer.CopyDialogState
	inputSelection    *inputcomposer.SelectionState
	chatViewport      viewport.Model
	chatSelection     bossTextSelection
	messages          []ChatMessage
	bossSlashSelected int
	pendingControl    *ControlProposal
	pendingGoal       *bossrun.GoalProposal

	sessionStore  *bossSessionStore
	sessionID     string
	sessionTitle  string
	sessionLoaded bool
	sessionErr    error

	sessionPickerVisible  bool
	sessionPickerLoading  bool
	sessionPickerSessions []bossChatSession
	sessionPickerSelected int
	sessionPickerErr      error

	snapshot            StateSnapshot
	viewContext         ViewContext
	operationalNotices  []ViewSystemNotice
	transientActivities []ViewEngineerActivity
	deskEvents          []bossDeskEvent
	stateLoaded         bool
	stateErr            error
	sending             bool
	status              string
	spinnerFrame        int
	nowFn               func() time.Time

	assistantStreamID      int
	streamingAssistantText string
	streamingToolCalls     []string

	summaryFlashUntil map[string]time.Time
}

type StateLoadedMsg struct {
	snapshot StateSnapshot
	err      error
}

type HostNoticeMsg struct {
	Content        string
	AnnounceInChat bool
	Handoff        *HandoffHighlight
}

type AssistantReplyMsg struct {
	response       AssistantResponse
	err            error
	snapshot       StateSnapshot
	stateErr       error
	stateRefreshed bool
}

type assistantStreamStartedMsg struct {
	streamID int
	events   <-chan assistantStreamEnvelope
}

type assistantStreamEnvelope struct {
	event          AssistantStreamEvent
	response       AssistantResponse
	err            error
	snapshot       StateSnapshot
	stateErr       error
	stateRefreshed bool
	done           bool
}

type assistantStreamMsg struct {
	streamID int
	events   <-chan assistantStreamEnvelope
	envelope assistantStreamEnvelope
}

type bossSessionLoadedMsg struct {
	session  bossChatSession
	messages []ChatMessage
	created  bool
	prompt   string
	err      error
}

type bossSessionSavedMsg struct {
	err error
}

type bossSessionsListedMsg struct {
	sessions []bossChatSession
	err      error
}

type TickMsg time.Time

type ExitMsg struct{}

type AttentionItemKind string

const (
	AttentionItemProject   AttentionItemKind = "project"
	AttentionItemAgentTask AttentionItemKind = "agent_task"
)

type AttentionItem struct {
	Kind        AttentionItemKind
	ProjectPath string
	TaskID      string
}

type bossLayout struct {
	width            int
	height           int
	topHeight        int
	bottomHeight     int
	middleGapHeight  int
	chatWidth        int
	sidebarWidth     int
	chatInnerWidth   int
	transcriptHeight int
	inputHeight      int
	slashHeight      int
}

func New(ctx context.Context, svc *service.Service) Model {
	return newModel(ctx, svc, false)
}

func NewEmbedded(ctx context.Context, svc *service.Service) Model {
	return newModel(ctx, svc, true)
}

func NewEmbeddedWithViewContext(ctx context.Context, svc *service.Service, view ViewContext) Model {
	m := newModel(ctx, svc, true)
	m = m.WithViewContext(view)
	return m
}

func newModel(ctx context.Context, svc *service.Service, embedded bool) Model {
	input := textarea.New()
	input.Prompt = "> "
	input.SetPromptFunc(2, func(line int) string {
		if line == 0 {
			return "> "
		}
		return "  "
	})
	input.Placeholder = ""
	input.CharLimit = 6000
	input.ShowLineNumbers = false
	input.SetWidth(72)
	input.SetHeight(3)
	styleBossTextarea(&input)
	input.Focus()

	assistant := NewAssistant(svc)
	sessionStore := newBossSessionStoreForService(svc)
	m := Model{
		ctx:           ctx,
		svc:           svc,
		assistant:     assistant,
		embedded:      embedded,
		input:         input,
		chatViewport:  viewport.New(0, 0),
		status:        assistant.Label(),
		sessionStore:  sessionStore,
		sessionLoaded: sessionStore == nil,
		nowFn:         time.Now,
	}
	m.syncLayout(true)
	return m
}

func IsMessage(msg tea.Msg) bool {
	switch msg.(type) {
	case StateLoadedMsg, HostNoticeMsg, AssistantReplyMsg, assistantStreamStartedMsg, assistantStreamMsg, TickMsg, ExitMsg, bossSessionLoadedMsg, bossSessionSavedMsg, bossSessionsListedMsg, bossSkillsInventoryMsg, ControlInvocationResultMsg, GoalRunResultMsg:
		return true
	default:
		return false
	}
}

func IsBackgroundMessage(msg tea.Msg) bool {
	switch msg.(type) {
	case StateLoadedMsg, AssistantReplyMsg, assistantStreamStartedMsg, assistantStreamMsg, bossSessionLoadedMsg, bossSessionSavedMsg, bossSessionsListedMsg, bossSkillsInventoryMsg, ControlInvocationResultMsg, GoalRunResultMsg:
		return true
	default:
		return false
	}
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadStateCmd(), bossTickCmd(), tea.EnableMouseCellMotion}
	if m.hasPersistentSessions() {
		cmds = append(cmds, m.loadLatestBossSessionCmd())
	}
	return tea.Batch(cmds...)
}

func (m Model) ActivateCmd() tea.Cmd {
	return tea.Batch(m.loadStateCmd(), bossTickCmd(), tea.EnableMouseCellMotion)
}

func (m Model) RefreshCmd() tea.Cmd {
	return m.loadStateCmd()
}

func (m Model) WithViewContext(view ViewContext) Model {
	m.viewContext = view
	return m
}

func (m Model) HostNoticesReady() bool {
	return !m.hasPersistentSessions() || m.sessionLoaded
}

func (m Model) OperationalNotices() []ViewSystemNotice {
	return append([]ViewSystemNotice(nil), m.operationalNotices...)
}

func (m Model) assistantViewContext() ViewContext {
	view := m.viewContext
	view.EngineerActivities = m.assistantEngineerActivities()
	if len(m.operationalNotices) == 0 {
		return view
	}
	view.SystemNotices = append(append([]ViewSystemNotice(nil), view.SystemNotices...), m.operationalNotices...)
	return view
}

func (m Model) assistantEngineerActivities() []ViewEngineerActivity {
	out := append([]ViewEngineerActivity(nil), m.viewContext.EngineerActivities...)
	seen := map[string]bool{}
	for _, activity := range out {
		if key := viewEngineerActivityKey(activity); key != "" {
			seen[key] = true
		}
	}
	now := m.now()
	for _, activity := range m.transientActivities {
		if !activity.Active || transientEngineerActivityExpired(activity, now) {
			continue
		}
		key := viewEngineerActivityKey(activity)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, activity)
	}
	return out
}

func (m Model) recordOperationalNotice(code, severity, summary string) Model {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return m
	}
	code = strings.TrimSpace(code)
	if code == "" {
		code = "host_update"
	}
	severity = strings.TrimSpace(severity)
	if severity == "" {
		severity = "notice"
	}
	notice := ViewSystemNotice{
		Code:     code,
		Severity: severity,
		Summary:  summary,
	}
	if len(m.operationalNotices) > 0 {
		last := m.operationalNotices[len(m.operationalNotices)-1]
		if last.Code == notice.Code && last.Severity == notice.Severity && last.Summary == notice.Summary {
			return m
		}
	}
	m.operationalNotices = append(m.operationalNotices, notice)
	if len(m.operationalNotices) > maxOperationalNotices {
		m.operationalNotices = append([]ViewSystemNotice(nil), m.operationalNotices[len(m.operationalNotices)-maxOperationalNotices:]...)
	}
	return m
}

func (m Model) recordTransientEngineerActivity(activity ViewEngineerActivity) Model {
	if !activity.Active {
		return m
	}
	if strings.TrimSpace(activity.Title) == "" && strings.TrimSpace(activity.ProjectPath) == "" && strings.TrimSpace(activity.TaskID) == "" {
		return m
	}
	now := m.now()
	if activity.StartedAt.IsZero() {
		activity.StartedAt = now
	}
	if activity.LastEventAt.IsZero() {
		activity.LastEventAt = activity.StartedAt
	}
	if strings.TrimSpace(activity.Status) == "" {
		activity.Status = "working"
	}
	key := viewEngineerActivityKey(activity)
	if key == "" {
		return m
	}
	replaced := false
	for i, existing := range m.transientActivities {
		if viewEngineerActivityKey(existing) == key {
			m.transientActivities[i] = activity
			replaced = true
			break
		}
	}
	if !replaced {
		m.transientActivities = append(m.transientActivities, activity)
	}
	m.pruneTransientEngineerActivities()
	return m
}

func (m *Model) pruneTransientEngineerActivities() {
	if len(m.transientActivities) == 0 {
		return
	}
	now := m.now()
	out := m.transientActivities[:0]
	for _, activity := range m.transientActivities {
		if transientEngineerActivityExpired(activity, now) {
			continue
		}
		out = append(out, activity)
	}
	m.transientActivities = out
}

func transientEngineerActivityExpired(activity ViewEngineerActivity, now time.Time) bool {
	if now.IsZero() {
		return false
	}
	startedAt := activity.StartedAt
	if startedAt.IsZero() {
		startedAt = activity.LastEventAt
	}
	if startedAt.IsZero() {
		return false
	}
	return now.Sub(startedAt) > transientEngineerActivityTTL
}

func (m Model) StatusText() string {
	status := strings.TrimSpace(m.status)
	if status == "" {
		status = "ready"
	}
	if m.sending {
		status = "thinking " + spinnerDots(m.spinnerFrame)
	}
	return status
}

func (m Model) HotProjectPath(index int) string {
	item := m.HotAttentionItem(index)
	if item.Kind != AttentionItemProject {
		return ""
	}
	return strings.TrimSpace(item.ProjectPath)
}

func (m Model) HotAttentionItem(index int) AttentionItem {
	if index < 0 {
		return AttentionItem{}
	}
	if index < len(m.snapshot.OpenAgentTasks) {
		task := m.snapshot.OpenAgentTasks[index]
		return AttentionItem{
			Kind:   AttentionItemAgentTask,
			TaskID: strings.TrimSpace(task.ID),
		}
	}
	projectIndex := index - len(m.snapshot.OpenAgentTasks)
	if projectIndex < 0 || projectIndex >= len(m.snapshot.HotProjects) {
		return AttentionItem{}
	}
	return AttentionItem{
		Kind:        AttentionItemProject,
		ProjectPath: strings.TrimSpace(m.snapshot.HotProjects[projectIndex].Path),
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.syncLayout(false)
		return m, nil
	case StateLoadedMsg:
		m.stateLoaded = true
		m.stateErr = msg.err
		if msg.err == nil {
			m.syncSummaryFlashes(msg.snapshot)
			m.snapshot = msg.snapshot
			m.status = m.assistant.Label()
		} else {
			m.status = "State refresh failed: " + msg.err.Error()
		}
		m.syncLayout(false)
		return m, nil
	case HostNoticeMsg:
		return m.applyHostNotice(msg)
	case AssistantReplyMsg:
		return m.applyAssistantReply(msg.response, msg.err, msg.snapshot, msg.stateErr, msg.stateRefreshed)
	case assistantStreamStartedMsg:
		if msg.streamID != m.assistantStreamID || msg.events == nil {
			return m, nil
		}
		return m, waitAssistantStreamCmd(msg.streamID, msg.events)
	case assistantStreamMsg:
		if msg.streamID != m.assistantStreamID {
			return m, nil
		}
		if msg.envelope.done {
			return m.applyAssistantReply(msg.envelope.response, msg.envelope.err, msg.envelope.snapshot, msg.envelope.stateErr, msg.envelope.stateRefreshed)
		}
		m.applyAssistantStreamEvent(msg.envelope.event)
		m.syncLayout(true)
		return m, waitAssistantStreamCmd(msg.streamID, msg.events)
	case bossSessionLoadedMsg:
		m.sessionLoaded = true
		m.sessionErr = msg.err
		if msg.err != nil {
			m.status = "Boss chat session storage failed: " + msg.err.Error()
		} else {
			m.sessionID = strings.TrimSpace(msg.session.SessionID)
			m.sessionTitle = strings.TrimSpace(msg.session.Title)
			m.messages = chatMessagesFromBossMessages(msg.messages)
			if msg.created {
				m.status = "Boss chat session ready"
			} else if len(m.messages) > 0 {
				m.status = "Resumed boss chat session"
			}
		}
		m.syncLayout(true)
		if msg.err == nil && strings.TrimSpace(msg.prompt) != "" {
			return m.submitChatMessage(msg.prompt)
		}
		return m, nil
	case bossSessionSavedMsg:
		if msg.err != nil {
			m.sessionErr = msg.err
			m.status = "Boss chat session save failed: " + msg.err.Error()
		}
		return m, nil
	case bossSessionsListedMsg:
		return m.applyBossSessionsListed(msg)
	case bossSkillsInventoryMsg:
		return m.applyBossSkillsInventoryMsg(msg)
	case ControlInvocationResultMsg:
		return m.applyControlInvocationResult(msg)
	case GoalRunResultMsg:
		return m.applyGoalRunResult(msg)
	case TickMsg:
		m.spinnerFrame++
		m.pruneSummaryFlashes()
		m.pruneTransientEngineerActivities()
		if m.sending || len(m.supervisorItems(m.now())) > 0 {
			m.syncLayout(false)
		}
		cmds := []tea.Cmd{bossTickCmd()}
		if m.shouldRefreshSupervisorState() {
			cmds = append(cmds, m.loadStateCmd())
		}
		return m, tea.Batch(cmds...)
	case tea.KeyMsg:
		m.chatSelection = bossTextSelection{}
		if m.pendingControl != nil {
			return m.updateControlConfirmation(msg)
		}
		if m.pendingGoal != nil {
			return m.updateGoalConfirmation(msg)
		}
		if m.sessionPickerVisible {
			return m.updateBossSessionPicker(msg)
		}
		if m.inputCopyDialog != nil {
			return m.updateInputCopyDialog(msg)
		}
		if m.inputSelection != nil {
			return m.updateInputSelection(msg)
		}
		if m.bossSlashActive() {
			switch msg.String() {
			case "tab":
				if m.cycleAndApplyBossSlashSuggestion(1) {
					return m, nil
				}
			case "shift+tab":
				if m.cycleAndApplyBossSlashSuggestion(-1) {
					return m, nil
				}
			}
		}
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, m.exitCmd()
		case "alt+c":
			m.openInputCopyDialog()
			return m, nil
		case "alt+s":
			m.status = "Use Alt+C to copy input or output"
			return m, nil
		case "ctrl+r":
			m.status = "Refreshing project state..."
			return m, m.loadStateCmd()
		case "pgup":
			viewportnav.PageUp(&m.chatViewport)
			return m, nil
		case "pgdown":
			viewportnav.PageDown(&m.chatViewport)
			return m, nil
		case "home", "end":
			var cmd tea.Cmd
			m.chatViewport, cmd = m.chatViewport.Update(msg)
			return m, cmd
		case "enter":
			return m.submit()
		case "alt+enter", "ctrl+j":
			m.input.InsertString("\n")
			m.syncLayout(false)
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.syncBossSlashSelection()
		m.syncLayout(false)
		return m, cmd
	case tea.MouseMsg:
		if m.sessionPickerVisible {
			return m, nil
		}
		return m.updateMouse(msg)
	}
	return m, nil
}

func (m Model) applyAssistantReply(response AssistantResponse, err error, snapshot StateSnapshot, stateErr error, stateRefreshed bool) (tea.Model, tea.Cmd) {
	m.sending = false
	m.streamingAssistantText = ""
	m.streamingToolCalls = nil
	if stateRefreshed {
		m.syncSummaryFlashes(snapshot)
		m.snapshot = snapshot
		m.stateLoaded = true
		m.stateErr = nil
	} else if stateErr != nil {
		m.stateErr = stateErr
	}
	var saved ChatMessage
	if err != nil {
		m.pendingControl = nil
		m.pendingGoal = nil
		content := "I could not reach my chat backend yet: " + err.Error()
		status := "Boss chat could not answer"
		var proposalErr controlProposalError
		if errors.As(err, &proposalErr) {
			content = "I could not prepare that control action: " + proposalErr.Unwrap().Error()
			status = "Control action proposal failed"
		}
		var goalErr goalProposalError
		if errors.As(err, &goalErr) {
			content = "I could not prepare that goal run: " + goalProposalDetail(goalErr)
			status = "Goal run proposal failed"
		}
		saved = ChatMessage{
			Role:    "assistant",
			Content: content,
			At:      m.now(),
		}
		m.messages = append(m.messages, saved)
		m.status = status
	} else {
		content := strings.TrimSpace(response.Content)
		if content == "" {
			content = "I heard you, but the model returned an empty reply."
		}
		if response.ControlInvocation != nil {
			m.pendingControl = &ControlProposal{
				Invocation: copyControlInvocation(*response.ControlInvocation),
				Preview:    content,
			}
			m.pendingGoal = nil
			m.status = "Confirm control action with Enter, or Esc to cancel"
			m.syncLayout(true)
			return m, nil
		}
		if response.GoalProposal != nil {
			proposal := bossrun.CloneGoalProposal(*response.GoalProposal)
			m.pendingGoal = &proposal
			m.pendingControl = nil
			m.status = "Confirm goal run with Enter, or Esc to cancel"
			m.syncLayout(true)
			return m, nil
		}
		saved = ChatMessage{
			Role:    "assistant",
			Content: content,
			At:      m.now(),
		}
		m.messages = append(m.messages, saved)
		if modelName := strings.TrimSpace(response.Model); modelName != "" {
			m.pendingControl = nil
			m.pendingGoal = nil
			m.status = "Boss chat via " + modelName
		} else {
			m.pendingControl = nil
			m.pendingGoal = nil
			m.status = m.assistant.Label()
		}
	}
	m.syncLayout(true)
	return m, m.saveBossChatMessageCmd(saved)
}

func (m *Model) applyAssistantStreamEvent(event AssistantStreamEvent) {
	switch event.Kind {
	case AssistantStreamTextDelta:
		m.streamingAssistantText += event.Delta
		if strings.TrimSpace(m.streamingAssistantText) != "" {
			m.status = "Boss chat is answering..."
		}
	case AssistantStreamToolCall:
		line := formatAssistantToolCallStatus(event)
		if line == "" {
			return
		}
		m.streamingToolCalls = append(m.streamingToolCalls, line)
		if len(m.streamingToolCalls) > 6 {
			m.streamingToolCalls = append([]string(nil), m.streamingToolCalls[len(m.streamingToolCalls)-6:]...)
		}
		m.status = line
	}
}

func (m Model) copyInputToClipboard() (tea.Model, tea.Cmd) {
	text := m.input.Value()
	if text == "" {
		m.status = "Boss chat input is empty"
		return m, nil
	}
	if err := clipboardTextWriter(text); err != nil {
		m.status = "Boss chat input copy failed: " + err.Error()
		return m, nil
	}
	m.status = "Copied full boss chat input to clipboard"
	return m, nil
}

func (m Model) View() string {
	layout := m.layout()
	chat := m.renderChat(layout)
	top := chat
	if layout.sidebarWidth >= 12 {
		sidebar := m.renderBossSidebar(layout.sidebarWidth, layout.topHeight)
		top = lipgloss.JoinHorizontal(lipgloss.Top, chat, sidebar)
	}
	body := top
	if layout.bottomHeight >= 4 {
		bottom := m.renderBossLog(layout.width, layout.bottomHeight)
		if layout.middleGapHeight > 0 {
			body = lipgloss.JoinVertical(
				lipgloss.Left,
				top,
				fitRenderedBlock("", layout.width, layout.middleGapHeight),
				bottom,
			)
		} else {
			body = lipgloss.JoinVertical(lipgloss.Left, top, bottom)
		}
	}
	if m.sessionPickerVisible {
		body = m.renderBossSessionPickerOverlay(body, layout.width, layout.height)
	}
	if m.inputCopyDialog != nil {
		body = m.renderInputCopyDialogOverlay(body, layout.width, layout.height)
	}
	if m.pendingControl != nil {
		body = m.renderControlConfirmationOverlay(body, layout.width, layout.height)
	}
	if m.pendingGoal != nil {
		body = m.renderGoalConfirmationOverlay(body, layout.width, layout.height)
	}
	if m.embedded {
		return fitRenderedBlock(body, layout.width, layout.height)
	}
	return fitRenderedBlock(lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(layout.width), body), layout.width, layout.height+1)
}

func (m Model) submit() (tea.Model, tea.Cmd) {
	if m.sending {
		return m, nil
	}
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	if strings.HasPrefix(text, "/") {
		return m.runBossSlashCommand(text)
	}
	return m.submitChatMessage(text)
}

func (m Model) askAssistantCmd(messages []ChatMessage, snapshot StateSnapshot, view ViewContext) tea.Cmd {
	assistant := m.assistant
	parent := m.ctx
	svc := m.svc
	options := m.stateSnapshotOptions()
	return func() tea.Msg {
		ctx, cancel := childContext(parent, 120*time.Second)
		defer cancel()
		if resp, handled, err := assistant.replyStructuredHandle(ctx, AssistantRequest{
			Snapshot: snapshot,
			View:     view,
			Messages: messages,
		}, nil); handled || err != nil {
			return AssistantReplyMsg{response: resp, err: err, snapshot: snapshot}
		}
		stateErr := error(nil)
		stateRefreshed := false
		if svc != nil {
			refreshed, err := LoadStateSnapshot(ctx, svc, time.Now(), options)
			if err == nil {
				snapshot = refreshed
				stateRefreshed = true
			} else {
				stateErr = err
			}
		}
		resp, err := assistant.Reply(ctx, AssistantRequest{
			StateBrief: BuildStateBrief(snapshot, time.Now()),
			Snapshot:   snapshot,
			View:       view,
			Messages:   messages,
		})
		return AssistantReplyMsg{response: resp, err: err, snapshot: snapshot, stateErr: stateErr, stateRefreshed: stateRefreshed}
	}
}

func (m Model) askAssistantStreamCmd(streamID int, messages []ChatMessage, snapshot StateSnapshot, view ViewContext) tea.Cmd {
	assistant := m.assistant
	parent := m.ctx
	svc := m.svc
	options := m.stateSnapshotOptions()
	return func() tea.Msg {
		events := make(chan assistantStreamEnvelope, 128)
		go func() {
			defer close(events)
			ctx, cancel := childContext(parent, 120*time.Second)
			defer cancel()
			emit := func(event AssistantStreamEvent) {
				select {
				case events <- assistantStreamEnvelope{event: event}:
				case <-ctx.Done():
				}
			}
			if resp, handled, err := assistant.replyStructuredHandle(ctx, AssistantRequest{
				Snapshot: snapshot,
				View:     view,
				Messages: messages,
			}, emit); handled || err != nil {
				select {
				case events <- assistantStreamEnvelope{response: resp, err: err, snapshot: snapshot, done: true}:
				case <-ctx.Done():
				}
				return
			}
			stateErr := error(nil)
			stateRefreshed := false
			if svc != nil {
				refreshed, err := LoadStateSnapshot(ctx, svc, time.Now(), options)
				if err == nil {
					snapshot = refreshed
					stateRefreshed = true
				} else {
					stateErr = err
				}
			}
			resp, err := assistant.ReplyStream(ctx, AssistantRequest{
				StateBrief: BuildStateBrief(snapshot, time.Now()),
				Snapshot:   snapshot,
				View:       view,
				Messages:   messages,
			}, emit)
			select {
			case events <- assistantStreamEnvelope{response: resp, err: err, snapshot: snapshot, stateErr: stateErr, stateRefreshed: stateRefreshed, done: true}:
			case <-ctx.Done():
				select {
				case events <- assistantStreamEnvelope{err: ctx.Err(), snapshot: snapshot, stateErr: stateErr, stateRefreshed: stateRefreshed, done: true}:
				default:
				}
			}
		}()
		return assistantStreamStartedMsg{streamID: streamID, events: events}
	}
}

func waitAssistantStreamCmd(streamID int, events <-chan assistantStreamEnvelope) tea.Cmd {
	return func() tea.Msg {
		envelope, ok := <-events
		if !ok {
			return assistantStreamMsg{streamID: streamID, events: events, envelope: assistantStreamEnvelope{err: fmt.Errorf("boss chat stream closed before the final response"), done: true}}
		}
		return assistantStreamMsg{streamID: streamID, events: events, envelope: envelope}
	}
}

func (m Model) loadStateCmd() tea.Cmd {
	svc := m.svc
	parent := m.ctx
	options := m.stateSnapshotOptions()
	return func() tea.Msg {
		ctx, cancel := childContext(parent, 20*time.Second)
		defer cancel()
		snapshot, err := LoadStateSnapshot(ctx, svc, time.Now(), options)
		return StateLoadedMsg{snapshot: snapshot, err: err}
	}
}

func (m Model) stateSnapshotOptions() StateSnapshotOptions {
	if m.viewContext.Active {
		return StateSnapshotOptions{
			PrivacyMode:     m.viewContext.PrivacyMode,
			PrivacyPatterns: append([]string(nil), m.viewContext.PrivacyPatterns...),
		}
	}
	return stateSnapshotOptionsForService(m.svc)
}

func (m Model) exitCmd() tea.Cmd {
	if m.embedded {
		return func() tea.Msg { return ExitMsg{} }
	}
	return tea.Sequence(tea.DisableMouse, tea.Quit)
}

func childContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		return context.WithTimeout(context.Background(), timeout)
	}
	if _, ok := parent.Deadline(); ok || timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

func bossTickCmd() tea.Cmd {
	return tea.Tick(600*time.Millisecond, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

func (m *Model) syncLayout(gotoBottom bool) {
	layout := m.layout()
	m.input.SetWidth(maxInt(20, layout.chatInnerWidth))
	m.input.SetHeight(layout.inputHeight)
	m.chatViewport.Width = maxInt(1, layout.chatInnerWidth)
	m.chatViewport.Height = maxInt(1, layout.transcriptHeight)
	m.chatViewport.SetContent(m.renderTranscript(layout.chatInnerWidth))
	if gotoBottom {
		m.chatViewport.GotoBottom()
	}
}

func (m Model) layout() bossLayout {
	width := m.width
	if width <= 0 {
		width = defaultBossWidth
	}
	height := m.height
	if height <= 0 {
		height = defaultBossHeight
	}
	width = maxInt(48, width)
	minHeight := 18
	if m.embedded {
		minHeight = 8
	}
	height = maxInt(minHeight, height)
	if height > 18 {
		if !m.embedded {
			// Standalone boss mode owns its header bar and keeps one row of
			// slack so exact-height renders do not scroll frames out of view.
			height -= 2
		}
	}
	inputHeight := 2

	bottomHeight := 0
	if height >= 14 {
		bottomHeight = bossLogTargetHeight(height, m.embedded)
		minTopHeight := 8
		if !m.embedded {
			minTopHeight = 10
		}
		if height-bottomHeight < minTopHeight {
			bottomHeight = maxInt(0, height-minTopHeight)
		}
	}
	topHeight := maxInt(1, height-bottomHeight)

	if width < 78 {
		chatInnerWidth := bossPanelInnerWidth(width)
		transcriptHeight, slashHeight := m.chatAuxiliaryHeights(topHeight, inputHeight, false)
		return bossLayout{
			width:            width,
			height:           height,
			topHeight:        topHeight,
			bottomHeight:     bottomHeight,
			chatWidth:        width,
			chatInnerWidth:   chatInnerWidth,
			transcriptHeight: transcriptHeight,
			inputHeight:      inputHeight,
			slashHeight:      slashHeight,
		}
	}

	sidebarWidth := bossSidebarTargetWidth(width)
	chatWidth := width - sidebarWidth
	chatInnerWidth := bossPanelInnerWidth(chatWidth)
	transcriptHeight, slashHeight := m.chatAuxiliaryHeights(topHeight, inputHeight, !m.embedded)
	middleGapHeight := 0
	return bossLayout{
		width:            width,
		height:           height,
		topHeight:        topHeight,
		bottomHeight:     bottomHeight,
		middleGapHeight:  middleGapHeight,
		chatWidth:        chatWidth,
		sidebarWidth:     sidebarWidth,
		chatInnerWidth:   chatInnerWidth,
		transcriptHeight: transcriptHeight,
		inputHeight:      inputHeight,
		slashHeight:      slashHeight,
	}
}

func bossSidebarTargetWidth(width int) int {
	if width < 96 {
		return 0
	}
	sidebarWidth := clampInt(width/4, 28, 42)
	if width >= 150 {
		sidebarWidth = clampInt(width/5, 32, 44)
	}
	sidebarWidth = clampInt((sidebarWidth*13+9)/10, 36, 56)
	if width-sidebarWidth < 58 {
		return 0
	}
	return sidebarWidth
}

func bossLogTargetHeight(height int, embedded bool) int {
	if height < 14 {
		return 0
	}
	minHeight := 5
	maxHeight := 8
	if height >= 34 {
		minHeight = 6
		maxHeight = 9
	}
	if embedded && height < 18 {
		minHeight = 4
		maxHeight = 5
	}
	return clampInt(height/5, minHeight, maxHeight)
}

func (m Model) chatAuxiliaryHeights(topHeight, inputHeight int, includesHint bool) (int, int) {
	hintHeight := 0
	if includesHint {
		hintHeight = 1
	}
	available := maxInt(1, topHeight-inputHeight-hintHeight-4)
	rawSlashHeight := m.bossSlashBlockHeight()
	slashHeight := 0
	if rawSlashHeight > 0 {
		slashHeight = minInt(rawSlashHeight, maxInt(0, available-1))
	}
	transcriptHeight := maxInt(1, available-slashHeight)
	return transcriptHeight, slashHeight
}

func (m Model) renderChat(layout bossLayout) string {
	input := fitRenderedBlock(renderBossInputWithSelection(m.input, m.inputSelection, layout.chatInnerWidth), layout.chatInnerWidth, layout.inputHeight)
	transcript := m.chatViewport.View()
	if m.chatSelection.dragging && m.chatSelection.hasRange() {
		transcript = overlayBossSelectionHighlight(transcript, m.chatSelection, m.chatViewport.YOffset)
	}
	parts := []string{transcript}
	if slashBlock := m.renderBossSlashBlock(layout.chatInnerWidth, layout.slashHeight); slashBlock != "" {
		parts = append(parts, slashBlock)
	}
	if !m.embedded {
		hint := "Enter sends | Alt+Enter newline | Alt+C copy menu | Ctrl+R refresh | Esc hides"
		if m.bossSlashActive() {
			hint = "Enter runs command | Tab complete | Shift+Tab previous | Alt+Enter newline"
		}
		if m.pendingControl != nil {
			hint = "Enter confirms engineer prompt | Esc cancels"
		}
		if m.pendingGoal != nil {
			hint = "Enter runs approved goal | Esc cancels"
		}
		if m.sending {
			hint = "Boss chat is thinking " + spinnerDots(m.spinnerFrame)
		}
		parts = append(parts, bossMutedStyle.Render(fitLine(hint, layout.chatInnerWidth)))
	}
	parts = append(parts, input)
	content := strings.Join(parts, "\n")
	return m.renderRawPanel("Boss Chat", content, layout.chatWidth, layout.topHeight)
}

func (m Model) renderHeader(width int) string {
	escAction := "Esc hides"
	if m.pendingControl != nil || m.pendingGoal != nil || m.inputSelection != nil {
		escAction = "Esc cancels"
	} else if m.sessionPickerVisible || m.inputCopyDialog != nil {
		escAction = "Esc closes"
	}
	text := " Boss Mode  " + m.StatusText() + "  |  " + escAction + "  Ctrl+R refresh"
	return bossHeaderStyle.Width(width).Render(fitLine(text, width))
}

func (m Model) renderAttention(width, height int) string {
	return m.renderBossSidebar(width, height)
}

func (m Model) attentionContent(width, height int) string {
	return m.renderAttentionRows(bossPanelInnerWidth(width), attentionProjectLimit(height))
}

func (m Model) renderAttentionRows(width, limit int) string {
	width = maxInt(24, width)
	limit = clampInt(limit, 1, hotProjectLimit)
	totalRows := len(m.snapshot.OpenAgentTasks) + len(m.snapshot.HotProjects)
	if totalRows == 0 {
		return bossMutedStyle.Render(fitLine("Alt+1  waiting for projects or tasks", width))
	}

	keyW := 5
	flagW := 2
	assessmentW := 8
	fixed := keyW + 1 + flagW + 1 + assessmentW + 2 + 2
	remaining := maxInt(12, width-fixed)
	nameW := clampInt(remaining/3, 14, 28)
	summaryW := maxInt(10, remaining-nameW-2)
	if summaryW < 14 && nameW > 14 {
		shift := minInt(nameW-14, 14-summaryW)
		nameW -= shift
		summaryW += shift
	}

	rows := make([]string, 0, minInt(limit, totalRows))
	for _, task := range m.snapshot.OpenAgentTasks {
		if len(rows) >= limit {
			break
		}
		rows = append(rows, m.renderAgentTaskAttentionRow(task, len(rows), keyW, flagW, assessmentW, nameW, summaryW, width))
	}
	for _, project := range m.snapshot.HotProjects {
		if len(rows) >= limit {
			break
		}
		rows = append(rows, m.renderProjectAttentionRow(project, len(rows), keyW, flagW, assessmentW, nameW, summaryW, width))
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderProjectAttentionRow(project ProjectBrief, index, keyW, flagW, assessmentW, nameW, summaryW, width int) string {
	key := bossHotkeyStyle.Width(keyW).Render(fmt.Sprintf("Alt+%d", index+1))
	flags := bossRepoFlagStyle(project).Width(flagW).Align(lipgloss.Left).Render(bossRepoFlagText(project))
	assessmentText, assessmentStyle := bossAssessmentCell(project)
	activity, active := m.engineerActivityForProject(project.Path)
	if active {
		assessmentText, assessmentStyle = bossEngineerActivityCell(activity, m.now())
	}
	assessment := assessmentStyle.Width(assessmentW).Render(fitLine(assessmentText, assessmentW))
	name := bossProjectNameStyle.Width(nameW).Render(fitLine(compactProjectName(project), nameW))
	summaryStyle := bossSummaryStyle(project)
	if m.summaryFlashActive(project.Path) {
		summaryStyle = bossSummaryFlashStyle
	}
	summaryText := bossProjectSummaryText(project, m.now())
	if active {
		summaryText = bossEngineerActivitySummaryText(summaryText, activity, m.now())
		summaryStyle = bossSummaryTextStyle
	}
	summary := summaryStyle.Width(summaryW).Render(fitLine(summaryText, summaryW))
	return fitStyledLine(lipgloss.JoinHorizontal(
		lipgloss.Top,
		key,
		" ",
		flags,
		" ",
		assessment,
		"  ",
		name,
		"  ",
		summary,
	), width)
}

func (m Model) renderAgentTaskAttentionRow(task AgentTaskBrief, index, keyW, flagW, assessmentW, nameW, summaryW, width int) string {
	key := bossHotkeyStyle.Width(keyW).Render(fmt.Sprintf("Alt+%d", index+1))
	flags := bossTaskFlagStyle.Width(flagW).Align(lipgloss.Left).Render("T")
	assessmentText, assessmentStyle := bossAgentTaskStatusCell(task)
	activity, active := m.engineerActivityForAgentTask(task.ID)
	if active {
		assessmentText, assessmentStyle = bossEngineerActivityCell(activity, m.now())
	}
	assessment := assessmentStyle.Width(assessmentW).Render(fitLine(assessmentText, assessmentW))
	name := bossTaskNameStyle.Width(nameW).Render(fitLine(compactAgentTaskTitle(task), nameW))
	summaryText := bossAgentTaskSummaryText(task, m.now())
	if active {
		summaryText = bossEngineerActivitySummaryText(summaryText, activity, m.now())
	}
	summary := bossSummaryTextStyle.Width(summaryW).Render(fitLine(summaryText, summaryW))
	return fitStyledLine(lipgloss.JoinHorizontal(
		lipgloss.Top,
		key,
		" ",
		flags,
		" ",
		assessment,
		"  ",
		name,
		"  ",
		summary,
	), width)
}

func (m Model) engineerActivityForAgentTask(taskID string) (ViewEngineerActivity, bool) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ViewEngineerActivity{}, false
	}
	for _, activity := range m.activeEngineerActivities() {
		if strings.TrimSpace(activity.TaskID) == taskID && activity.Active {
			return activity, true
		}
	}
	return ViewEngineerActivity{}, false
}

func (m Model) engineerActivityForProject(projectPath string) (ViewEngineerActivity, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return ViewEngineerActivity{}, false
	}
	for _, activity := range m.activeEngineerActivities() {
		if strings.TrimSpace(activity.ProjectPath) == projectPath && activity.Active {
			return activity, true
		}
	}
	return ViewEngineerActivity{}, false
}

func bossRepoFlagText(project ProjectBrief) string {
	switch {
	case project.RepoConflict:
		return "!"
	case project.RepoDirty:
		return "!"
	case repoSyncFlag(project.RepoSyncStatus, project.RepoAheadCount, project.RepoBehindCount) != "":
		return "!"
	default:
		return ""
	}
}

func bossRepoFlagStyle(project ProjectBrief) lipgloss.Style {
	style := bossMutedStyle
	switch {
	case project.RepoConflict:
		style = bossRepoConflictStyle
	case project.RepoDirty:
		style = bossRepoDangerStyle
	case repoSyncFlag(project.RepoSyncStatus, project.RepoAheadCount, project.RepoBehindCount) != "":
		style = bossRepoWarningStyle
	}
	return style
}

func bossAssessmentCell(project ProjectBrief) (string, lipgloss.Style) {
	if label, category, ok := bossVisibleAssessmentStatus(project); ok {
		return label, bossAssessmentCategoryStyle(category)
	}
	switch project.ClassificationStatus {
	case model.ClassificationPending:
		return "queued", bossAssessmentPendingStyle
	case model.ClassificationRunning:
		return "running", bossAssessmentRunningStyle
	case model.ClassificationFailed:
		return "failed", bossAssessmentFailedStyle
	default:
		if strings.TrimSpace(project.LatestFormat) != "" {
			return "new", bossMutedStyle
		}
		if project.Status != "" {
			return bossAttentionStatusLabel(project.Status), bossStatusStyle(project.Status)
		}
		return "-", bossMutedStyle
	}
}

func bossVisibleAssessmentStatus(project ProjectBrief) (string, model.SessionCategory, bool) {
	if project.ClassificationStatus == model.ClassificationCompleted {
		if label, ok := bossAssessmentStatusLabel(project.LatestCategory); ok {
			return label, project.LatestCategory, true
		}
	}
	if label, ok := bossAssessmentStatusLabel(project.LatestCompletedKind); ok {
		return label, project.LatestCompletedKind, true
	}
	return "", model.SessionCategoryUnknown, false
}

func bossAssessmentStatusLabel(category model.SessionCategory) (string, bool) {
	switch category {
	case model.SessionCategoryCompleted:
		return "done", true
	case model.SessionCategoryBlocked:
		return "blocked", true
	case model.SessionCategoryWaitingForUser:
		return "waiting", true
	case model.SessionCategoryNeedsFollowUp:
		return "followup", true
	case model.SessionCategoryInProgress:
		return "working", true
	default:
		return "", false
	}
}

func bossAssessmentCategoryStyle(category model.SessionCategory) lipgloss.Style {
	switch category {
	case model.SessionCategoryCompleted:
		return bossAssessmentDoneStyle
	case model.SessionCategoryBlocked:
		return bossAssessmentBlockedStyle
	case model.SessionCategoryWaitingForUser:
		return bossAssessmentWaitingStyle
	case model.SessionCategoryNeedsFollowUp:
		return bossAssessmentFollowupStyle
	case model.SessionCategoryInProgress:
		return bossAssessmentWorkingStyle
	default:
		return bossMutedStyle
	}
}

func bossProjectSummaryText(project ProjectBrief, now time.Time) string {
	if summary := bestProjectSummary(project); summary != "" {
		return summary
	}
	if !project.LastActivity.IsZero() {
		return relativeAge(now, project.LastActivity)
	}
	return "-"
}

func bossSummaryStyle(project ProjectBrief) lipgloss.Style {
	if bestProjectSummary(project) == "" {
		return bossMutedStyle
	}
	return bossSummaryTextStyle
}

func compactAgentTaskTitle(task AgentTaskBrief) string {
	title := strings.TrimSpace(task.Title)
	if title == "" {
		title = strings.TrimSpace(task.ID)
	}
	if title == "" {
		return "agent task"
	}
	return title
}

func bossAgentTaskStatusCell(task AgentTaskBrief) (string, lipgloss.Style) {
	switch model.NormalizeAgentTaskStatus(task.Status) {
	case model.AgentTaskStatusWaiting:
		return "review", bossAssessmentWaitingStyle
	case model.AgentTaskStatusCompleted:
		return "done", bossAssessmentDoneStyle
	case model.AgentTaskStatusArchived:
		return "archived", bossMutedStyle
	default:
		if task.Provider != "" || strings.TrimSpace(task.SessionID) != "" {
			return "working", bossAssessmentWorkingStyle
		}
		return "active", bossAssessmentRunningStyle
	}
}

func bossEngineerActivityCell(activity ViewEngineerActivity, now time.Time) (string, lipgloss.Style) {
	status := strings.TrimSpace(activity.Status)
	switch status {
	case "stalled":
		return "stalled", bossAssessmentBlockedStyle
	case "waiting":
		return "waiting", bossAssessmentWaitingStyle
	case "finishing", "rechecking":
		if elapsed := bossEngineerActivityElapsedText(activity, now); elapsed != "" {
			return elapsed, bossAssessmentRunningStyle
		}
		return status, bossAssessmentRunningStyle
	default:
		if elapsed := bossEngineerActivityElapsedText(activity, now); elapsed != "" {
			return elapsed, bossAssessmentWorkingStyle
		}
		return "working", bossAssessmentWorkingStyle
	}
}

func bossEngineerActivitySummaryText(base string, activity ViewEngineerActivity, now time.Time) string {
	status := strings.TrimSpace(activity.Status)
	if status == "" {
		status = "working"
	}
	if elapsed := bossEngineerActivityElapsedText(activity, now); elapsed != "" {
		status += " " + elapsed
	}
	if name := strings.TrimSpace(activity.EngineerName); name != "" {
		status = name + " " + status
	}
	if strings.TrimSpace(base) == "" || base == "-" {
		return status
	}
	return status + " | " + base
}

func bossEngineerActivityElapsedText(activity ViewEngineerActivity, now time.Time) string {
	if activity.StartedAt.IsZero() || now.IsZero() {
		return ""
	}
	return bossRunningDuration(now.Sub(activity.StartedAt))
}

func bossAgentTaskSummaryText(task AgentTaskBrief, now time.Time) string {
	if summary := strings.TrimSpace(task.Summary); summary != "" {
		return summary
	}
	parts := make([]string, 0, 4)
	if name := strings.TrimSpace(task.EngineerName); name != "" {
		parts = append(parts, name)
	}
	if taskID := strings.TrimSpace(task.ID); taskID != "" {
		parts = append(parts, taskID)
	}
	if provider := model.NormalizeSessionSource(task.Provider); provider != "" {
		label := string(provider)
		if session := strings.TrimSpace(task.SessionID); session != "" {
			label += " " + session
		}
		parts = append(parts, label)
	}
	if resources := compactAgentTaskResources(task.Resources); resources != "" {
		parts = append(parts, resources)
	}
	if len(parts) == 0 && !task.LastTouchedAt.IsZero() {
		parts = append(parts, "touched "+relativeAge(now, task.LastTouchedAt))
	}
	if len(parts) == 0 {
		return "open delegated task"
	}
	return strings.Join(parts, " | ")
}

func bossAttentionStatusLabel(status model.ProjectStatus) string {
	switch status {
	case model.StatusPossiblyStuck:
		return "stuck"
	default:
		return string(status)
	}
}

func bossStatusStyle(status model.ProjectStatus) lipgloss.Style {
	switch status {
	case model.StatusActive:
		return bossAssessmentDoneStyle
	case model.StatusPossiblyStuck:
		return bossAssessmentBlockedStyle
	default:
		return bossAssessmentWaitingStyle
	}
}

func (m Model) renderPanel(title, content string, width, height int) string {
	width = maxInt(12, width)
	height = maxInt(4, height)
	innerWidth := bossPanelInnerWidth(width)
	innerHeight := maxInt(1, height-2)
	bodyHeight := maxInt(0, innerHeight-2)
	titleLine := panelTitleStyle.Render(fitLine(title, innerWidth))
	body := fitWrappedBlock(content, innerWidth, bodyHeight)
	rendered := panelStyle.Width(bossPanelStyleWidth(width)).Height(innerHeight).Render(titleLine + "\n" + body)
	return fitRenderedBlock(rendered, width, height)
}

func (m Model) renderRawPanel(title, content string, width, height int) string {
	width = maxInt(12, width)
	height = maxInt(4, height)
	innerWidth := bossPanelInnerWidth(width)
	innerHeight := maxInt(1, height-2)
	titleLine := panelTitleStyle.Render(fitLine(title, innerWidth))
	body := fitRenderedBlock(content, innerWidth, maxInt(0, innerHeight-2))
	rendered := panelStyle.Width(bossPanelStyleWidth(width)).Height(innerHeight).Render(titleLine + "\n" + body)
	return fitRenderedBlock(rendered, width, height)
}

func (m Model) renderTranscript(width int) string {
	width = maxInt(12, width)
	var blocks []string
	for _, message := range m.messages {
		if normalizeChatRole(message.Role) == "assistant" {
			blocks = append(blocks, renderAssistantChatMessage(message, width))
			continue
		}
		blocks = append(blocks, renderUserMessage(message.Content, width))
	}
	if m.sending {
		if pending := renderStreamingAssistantMessage(m.streamingAssistantText, m.streamingToolCalls, width, m.spinnerFrame); pending != "" {
			blocks = append(blocks, pending)
		}
	}
	return strings.Join(blocks, "\n\n")
}

func (m Model) activeEngineerActivities() []ViewEngineerActivity {
	out := make([]ViewEngineerActivity, 0, len(m.viewContext.EngineerActivities)+len(m.transientActivities))
	seen := map[string]bool{}
	for _, activity := range m.viewContext.EngineerActivities {
		if activity.Active {
			if key := viewEngineerActivityKey(activity); key != "" {
				seen[key] = true
			}
			out = append(out, activity)
		}
	}
	now := m.now()
	for _, activity := range m.transientActivities {
		if !activity.Active || transientEngineerActivityExpired(activity, now) {
			continue
		}
		key := viewEngineerActivityKey(activity)
		if key != "" && seen[key] {
			continue
		}
		if key != "" {
			seen[key] = true
		}
		out = append(out, activity)
	}
	return out
}

func viewEngineerActivityKey(activity ViewEngineerActivity) string {
	if taskID := strings.TrimSpace(activity.TaskID); taskID != "" {
		return "task:" + taskID
	}
	parts := []string{
		strings.TrimSpace(activity.Kind),
		strings.TrimSpace(activity.ProjectPath),
		strings.TrimSpace(string(model.NormalizeSessionSource(activity.Provider))),
		strings.TrimSpace(activity.SessionID),
	}
	if strings.Join(parts, "") == "" {
		return ""
	}
	return strings.Join(parts, "\x00")
}

func (m Model) now() time.Time {
	if m.nowFn != nil {
		return m.nowFn()
	}
	return time.Now()
}

func (m *Model) syncSummaryFlashes(next StateSnapshot) {
	now := m.now()
	if m.summaryFlashUntil == nil {
		m.summaryFlashUntil = map[string]time.Time{}
	}
	previous := map[string]ProjectBrief{}
	for _, project := range m.snapshot.HotProjects {
		if path := strings.TrimSpace(project.Path); path != "" {
			previous[path] = project
		}
	}
	for _, project := range next.HotProjects {
		path := strings.TrimSpace(project.Path)
		if path == "" {
			continue
		}
		if prev, ok := previous[path]; ok && bossSummaryFingerprint(prev) != bossSummaryFingerprint(project) {
			m.summaryFlashUntil[path] = now.Add(summaryFlashDuration)
			m.appendDeskEvent("project", "update", bossDeskTextWithDetail(compactProjectName(project), bestProjectSummary(project)))
		}
	}
	m.pruneSummaryFlashes()
}

func (m *Model) pruneSummaryFlashes() {
	if len(m.summaryFlashUntil) == 0 {
		return
	}
	now := m.now()
	for path, until := range m.summaryFlashUntil {
		if !until.IsZero() && !now.Before(until) {
			delete(m.summaryFlashUntil, path)
		}
	}
}

func (m Model) summaryFlashActive(projectPath string) bool {
	if len(m.summaryFlashUntil) == 0 {
		return false
	}
	until, ok := m.summaryFlashUntil[strings.TrimSpace(projectPath)]
	return ok && m.now().Before(until)
}

func bossSummaryFingerprint(project ProjectBrief) string {
	return bestProjectSummary(project)
}

var (
	clipboardTextWriter           = clipboard.WriteAll
	bossPanelBackground           = lipgloss.Color("#000000")
	bossInputBackground           = lipgloss.Color("#000000")
	bossInputCursorLineBackground = lipgloss.Color("#101010")
	bossPanelAccent               = lipgloss.Color("81")
	bossPanelText                 = lipgloss.Color("252")
	bossHeaderStyle               = lipgloss.NewStyle().
					Foreground(lipgloss.Color("16")).
					Background(bossPanelAccent).
					Bold(true)
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(bossPanelAccent).
			Padding(0, 1).
			Foreground(bossPanelText).
			Background(bossPanelBackground)
	panelTitleStyle = lipgloss.NewStyle().
			Foreground(bossPanelAccent).
			Bold(true)
	bossMutedStyle                  = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Background(bossPanelBackground)
	bossHotkeyStyle                 = lipgloss.NewStyle().Foreground(bossPanelAccent).Background(bossPanelBackground).Bold(true)
	bossProjectNameStyle            = lipgloss.NewStyle().Foreground(bossPanelText).Background(bossPanelBackground).Bold(true)
	bossTaskFlagStyle               = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Background(bossPanelBackground).Bold(true)
	bossTaskNameStyle               = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(bossPanelBackground).Bold(true)
	bossRepoWarningStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("178")).Background(bossPanelBackground).Bold(true)
	bossRepoDangerStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Background(bossPanelBackground).Bold(true)
	bossRepoConflictStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Background(bossPanelBackground).Bold(true)
	bossAssessmentDoneStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Background(bossPanelBackground).Bold(true)
	bossAssessmentBlockedStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Background(bossPanelBackground).Bold(true)
	bossAssessmentWaitingStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Background(bossPanelBackground).Bold(true)
	bossAssessmentFollowupStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Background(bossPanelBackground).Bold(true)
	bossAssessmentWorkingStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(bossPanelBackground).Bold(true)
	bossAssessmentPendingStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Background(bossPanelBackground).Bold(true)
	bossAssessmentRunningStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Background(bossPanelBackground).Bold(true)
	bossAssessmentFailedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Background(bossPanelBackground).Bold(true)
	bossSummaryTextStyle            = lipgloss.NewStyle().Foreground(bossPanelText).Background(bossPanelBackground)
	bossSummaryFlashStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("186")).Bold(true)
	bossAssistantMessageBackground  = bossPanelBackground
	bossUserMessageBackground       = bossPanelBackground
	bossAssistantMessageStyle       = lipgloss.NewStyle().Background(bossAssistantMessageBackground)
	bossAssistantPrefixStyle        = lipgloss.NewStyle().Foreground(bossPanelAccent).Background(bossAssistantMessageBackground).Bold(true)
	bossAssistantContinuationStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Background(bossAssistantMessageBackground)
	bossHandoffMessageStyle         = lipgloss.NewStyle().Background(bossPanelBackground)
	bossHandoffPrefixStyle          = lipgloss.NewStyle().Foreground(bossPanelAccent).Background(bossPanelBackground).Bold(true)
	bossHandoffContinuationStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Background(bossPanelBackground)
	bossHandoffText                 = lipgloss.Color("229")
	bossHandoffEngineerNameStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Background(bossPanelBackground).Bold(true)
	bossHandoffProjectLabelStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Background(bossPanelBackground).Bold(true)
	bossToolCallStyle               = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Background(bossPanelBackground)
	bossUserMessageStyle            = lipgloss.NewStyle().Background(bossUserMessageBackground)
	bossUserPrefixStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Background(bossUserMessageBackground).Bold(true)
	bossUserContinuationPrefixStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Background(bossUserMessageBackground)
	bossInputShellStyle             = lipgloss.NewStyle().Background(bossInputBackground).Foreground(bossPanelText)
)

func bossPanelInnerWidth(width int) int {
	return maxInt(1, width-4)
}

func bossPanelStyleWidth(width int) int {
	return maxInt(1, width-2)
}

func styleBossTextarea(input *textarea.Model) {
	focused := input.FocusedStyle
	focused.Base = focused.Base.Background(bossInputBackground).Foreground(bossPanelText)
	focused.CursorLine = focused.CursorLine.Background(bossInputCursorLineBackground)
	focused.EndOfBuffer = focused.EndOfBuffer.Foreground(lipgloss.Color("238")).Background(bossInputBackground)
	focused.Placeholder = focused.Placeholder.Foreground(lipgloss.Color("240")).Background(bossInputBackground)
	focused.Prompt = focused.Prompt.Foreground(bossPanelAccent).Background(bossInputBackground).Bold(true)
	focused.Text = focused.Text.Foreground(bossPanelText).Background(bossInputBackground)

	blurred := input.BlurredStyle
	blurred.Base = blurred.Base.Background(bossInputBackground).Foreground(bossPanelText)
	blurred.CursorLine = blurred.CursorLine.Background(bossInputBackground)
	blurred.EndOfBuffer = blurred.EndOfBuffer.Foreground(lipgloss.Color("238")).Background(bossInputBackground)
	blurred.Placeholder = blurred.Placeholder.Foreground(lipgloss.Color("240")).Background(bossInputBackground)
	blurred.Prompt = blurred.Prompt.Foreground(lipgloss.Color("244")).Background(bossInputBackground).Bold(true)
	blurred.Text = blurred.Text.Foreground(bossPanelText).Background(bossInputBackground)

	input.FocusedStyle = focused
	input.BlurredStyle = blurred
}

func renderBossInput(input textarea.Model, width int) string {
	return bossInputShellStyle.Width(width).Render(input.View())
}

func panelHeightForRawLines(contentLines int) int {
	return maxInt(4, contentLines+4)
}

func panelHeightForWrappedContent(content string, width int) int {
	return panelHeightForRawLines(countWrappedBlockLines(content, width))
}

func attentionProjectLimit(height int) int {
	bodyHeight := maxInt(0, height-4)
	if bodyHeight <= 0 {
		return defaultAttentionProjectLimit
	}
	return clampInt(bodyHeight, defaultAttentionProjectLimit, hotProjectLimit)
}

func renderAssistantMessage(content string, width int) string {
	return renderPrefixedMessage(content, "Boss> ", bossPanelText, bossAssistantPrefixStyle, bossAssistantContinuationStyle, bossAssistantMessageStyle, width, false)
}

func renderAssistantChatMessage(message ChatMessage, width int) string {
	highlights := handoffMessageHighlights(message.Content, message.Handoff)
	if len(highlights) == 0 {
		return renderAssistantMessage(message.Content, width)
	}
	return renderPrefixedMessageWithHighlights(message.Content, "Boss> ", bossPanelText, bossAssistantPrefixStyle, bossAssistantContinuationStyle, bossAssistantMessageStyle, width, false, highlights)
}

func renderStreamingAssistantMessage(content string, toolCalls []string, width, spinnerFrame int) string {
	var blocks []string
	if toolBlock := renderTemporaryToolCalls(toolCalls, width); toolBlock != "" {
		blocks = append(blocks, toolBlock)
	}
	if strings.TrimSpace(content) != "" {
		blocks = append(blocks, renderAssistantMessage(content, width))
	}
	if len(blocks) == 0 {
		blocks = append(blocks, bossMutedStyle.Render(fitLine("Boss chat is thinking "+spinnerDots(spinnerFrame), width)))
	}
	return strings.Join(blocks, "\n")
}

func renderTemporaryToolCalls(toolCalls []string, width int) string {
	if len(toolCalls) == 0 {
		return ""
	}
	start := maxInt(0, len(toolCalls)-4)
	lines := []string{"Tool calls"}
	for _, call := range toolCalls[start:] {
		if text := strings.TrimSpace(call); text != "" {
			lines = append(lines, "  "+text)
		}
	}
	for i, line := range lines {
		lines[i] = bossToolCallStyle.Render(fitLine(line, width))
	}
	return strings.Join(lines, "\n")
}

func formatAssistantToolCallStatus(event AssistantStreamEvent) string {
	call := strings.TrimSpace(event.ToolCall)
	if call == "" {
		return ""
	}
	switch strings.TrimSpace(event.ToolState) {
	case "running":
		return "tool: " + call
	case "done":
		return "done: " + call
	case "error":
		return "error: " + call
	default:
		return "tool: " + call
	}
}

func renderUserMessage(content string, width int) string {
	return renderPrefixedMessage(content, "You> ", bossPanelText, bossUserPrefixStyle, bossUserContinuationPrefixStyle, bossUserMessageStyle, width, true)
}

func renderBossHandoffMessage(content string, width int) string {
	return renderPrefixedMessage(content, "Boss> ", bossHandoffText, bossHandoffPrefixStyle, bossHandoffContinuationStyle, bossHandoffMessageStyle, width, false)
}

type prefixedMessageHighlight struct {
	Start int
	End   int
	Style lipgloss.Style
}

func renderPrefixedMessage(content, prefix string, bodyColor lipgloss.Color, prefixStyle, continuationStyle, lineStyle lipgloss.Style, width int, indentContinuation bool) string {
	return renderPrefixedMessageWithHighlights(content, prefix, bodyColor, prefixStyle, continuationStyle, lineStyle, width, indentContinuation, nil)
}

func renderPrefixedMessageWithHighlights(content, prefix string, bodyColor lipgloss.Color, prefixStyle, continuationStyle, lineStyle lipgloss.Style, width int, indentContinuation bool, highlights []prefixedMessageHighlight) string {
	contentWidth := maxInt(8, width-len(prefix))
	rendered := terminalmd.RenderBody(content, bodyColor, contentWidth)
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	if len(highlights) > 0 {
		lines[0] = applyPrefixedMessageHighlights(lines[0], highlights)
	}
	for i, line := range lines {
		if i == 0 {
			lines[i] = prefixStyle.Render(prefix) + line
			continue
		}
		if indentContinuation {
			lines[i] = continuationStyle.Render(strings.Repeat(" ", len(prefix))) + line
		}
	}
	return renderMessageLines(strings.Join(lines, "\n"), lineStyle, width)
}

func handoffMessageHighlights(content string, handoff *HandoffHighlight) []prefixedMessageHighlight {
	handoffValue, ok := normalizedHandoffHighlight(handoff)
	if !ok {
		handoffValue, ok = inferHandoffHighlight(content)
		if !ok {
			return nil
		}
	}
	lead := handoffValue.EngineerName + " is back from " + handoffValue.ProjectLabel
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, lead) {
		return nil
	}
	trailer := strings.TrimPrefix(content, lead)
	if !(strings.HasPrefix(trailer, ":") || strings.HasPrefix(trailer, ".")) {
		return nil
	}
	nameWidth := ansi.StringWidth(handoffValue.EngineerName)
	labelStart := ansi.StringWidth(handoffValue.EngineerName + " is back from ")
	labelEnd := labelStart + ansi.StringWidth(handoffValue.ProjectLabel)
	return []prefixedMessageHighlight{
		{Start: 0, End: nameWidth, Style: bossHandoffEngineerNameStyle},
		{Start: labelStart, End: labelEnd, Style: bossHandoffProjectLabelStyle},
	}
}

func inferHandoffHighlight(content string) (HandoffHighlight, bool) {
	content = strings.TrimSpace(content)
	engineerName, rest, ok := strings.Cut(content, " is back from ")
	if !ok || !knownHandoffEngineerName(engineerName) {
		return HandoffHighlight{}, false
	}
	projectLabel, _, ok := strings.Cut(rest, ":")
	if !ok {
		projectLabel, _, ok = strings.Cut(rest, ".")
	}
	projectLabel = strings.TrimSpace(projectLabel)
	if !ok || projectLabel == "" {
		return HandoffHighlight{}, false
	}
	return HandoffHighlight{EngineerName: strings.TrimSpace(engineerName), ProjectLabel: projectLabel}, true
}

func knownHandoffEngineerName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "Engineer" {
		return true
	}
	for _, candidate := range engineerNamePool {
		if name == candidate {
			return true
		}
	}
	return false
}

func normalizedHandoffHighlight(handoff *HandoffHighlight) (HandoffHighlight, bool) {
	if handoff == nil {
		return HandoffHighlight{}, false
	}
	out := HandoffHighlight{
		EngineerName: strings.TrimSpace(handoff.EngineerName),
		ProjectLabel: strings.TrimSpace(handoff.ProjectLabel),
	}
	if out.EngineerName == "" {
		out.EngineerName = "Engineer"
	}
	if out.ProjectLabel == "" {
		out.ProjectLabel = "engineer session"
	}
	return out, true
}

func applyPrefixedMessageHighlights(line string, highlights []prefixedMessageHighlight) string {
	width := ansi.StringWidth(ansi.Strip(line))
	if width <= 0 {
		return line
	}
	for i := len(highlights) - 1; i >= 0; i-- {
		highlight := highlights[i]
		start := clampInt(highlight.Start, 0, width)
		end := clampInt(highlight.End, 0, width)
		if start >= end {
			continue
		}
		before := ansi.Cut(line, 0, start)
		selected := ansi.Strip(ansi.Cut(line, start, end))
		after := ansi.Cut(line, end, width)
		line = before + highlight.Style.Render(selected) + after
	}
	return line
}

func renderMessageLines(content string, style lipgloss.Style, width int) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = style.Render(fitStyledLine(line, width))
	}
	return strings.Join(lines, "\n")
}

func spinnerDots(frame int) string {
	switch frame % 4 {
	case 0:
		return "."
	case 1:
		return ".."
	case 2:
		return "..."
	default:
		return ""
	}
}

func fitBlock(content string, width, height int) string {
	if height <= 0 {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, line := range lines {
		lines[i] = fitLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func fitWrappedBlock(content string, width, height int) string {
	if height <= 0 {
		return ""
	}
	lines := wrappedBlockLines(content, width)
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, line := range lines {
		lines[i] = fitLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func countBlockLines(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(strings.ReplaceAll(content, "\r\n", "\n"), "\n") + 1
}

func countWrappedBlockLines(content string, width int) int {
	return len(wrappedBlockLines(content, width))
}

func wrappedBlockLines(content string, width int) []string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, wrapText(line, width)...)
	}
	return lines
}

func fitLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(strings.TrimRight(line, " "))
	if len(runes) > width {
		if width <= 3 {
			return string(runes[:width])
		}
		return string(runes[:width-3]) + "..."
	}
	return string(runes) + strings.Repeat(" ", width-len(runes))
}

func fitStyledLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	line = ansi.Truncate(line, width, "")
	if padding := width - ansi.StringWidth(ansi.Strip(line)); padding > 0 {
		line += strings.Repeat(" ", padding)
	}
	return line
}

func blockWidth(content string) int {
	width := 0
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if w := ansi.StringWidth(ansi.Strip(line)); w > width {
			width = w
		}
	}
	return width
}

func fitRenderedBlock(content string, width, height int) string {
	if height <= 0 {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, line := range lines {
		lines[i] = fitStyledLine(line, width)
	}
	blank := strings.Repeat(" ", maxInt(0, width))
	for len(lines) < height {
		lines = append(lines, blank)
	}
	return strings.Join(lines, "\n")
}

func wrapText(text string, width int) []string {
	width = maxInt(8, width)
	paragraphs := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n")
	if len(paragraphs) == 0 {
		return []string{""}
	}
	var out []string
	for _, paragraph := range paragraphs {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		for _, word := range words {
			if line == "" {
				line = word
				continue
			}
			if len([]rune(line))+1+len([]rune(word)) > width {
				out = append(out, fitLine(line, width))
				line = word
				continue
			}
			line += " " + word
		}
		if line != "" {
			out = append(out, fitLine(line, width))
		}
	}
	return out
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
