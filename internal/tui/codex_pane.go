package tui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"lcroom/internal/brand"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/codexslash"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func (m *Model) ensureCodexRuntime() {
	if m.codexManager == nil {
		m.codexManager = codexapp.NewManager()
	}
	if m.codexInput.CharLimit == 0 && m.codexInput.Width() == 0 {
		m.codexInput = newCodexTextarea()
	}
	if m.codexDrafts == nil {
		m.codexDrafts = make(map[string]codexDraft)
	}
	if m.codexClosedHandled == nil {
		m.codexClosedHandled = make(map[string]struct{})
	}
	if m.codexToolAnswers == nil {
		m.codexToolAnswers = make(map[string]codexToolAnswerState)
	}
	if m.codexViewport.Width == 0 && m.codexViewport.Height == 0 {
		m.codexViewport = viewport.New(0, 0)
	}
	m.syncCodexComposerSize()
}

func (m Model) waitCodexCmd() tea.Cmd {
	if m.codexManager == nil || m.codexManager.Updates() == nil {
		return nil
	}
	return func() tea.Msg {
		projectPath, ok := <-m.codexManager.Updates()
		if !ok {
			return nil
		}
		return codexUpdateMsg{projectPath: projectPath}
	}
}

func (m Model) codexVisible() bool {
	return strings.TrimSpace(m.codexVisibleProject) != "" || strings.TrimSpace(m.codexPendingOpenProject()) != ""
}

func (m Model) codexPendingOpenProject() string {
	if m.codexPendingOpen == nil {
		return ""
	}
	return strings.TrimSpace(m.codexPendingOpen.projectPath)
}

func (m *Model) beginCodexPendingOpen(projectPath string) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		m.codexPendingOpen = nil
		return
	}
	if current := strings.TrimSpace(m.codexVisibleProject); current != "" && current != projectPath {
		m.persistVisibleCodexDraft()
	}
	m.codexPendingOpen = &codexPendingOpenState{projectPath: projectPath}
}

func (m *Model) finishCodexPendingOpen(projectPath string, opened bool) {
	projectPath = strings.TrimSpace(projectPath)
	if pending := m.codexPendingOpenProject(); pending != "" && pending == projectPath {
		m.codexPendingOpen = nil
	}
	if !opened {
		m.pruneCodexSessionVisibility()
		return
	}
	m.markCodexSessionLive(projectPath)
	m.codexVisibleProject = projectPath
	m.codexHiddenProject = projectPath
	m.loadCodexDraft(projectPath)
	m.syncCodexViewport(true)
	m.syncCodexComposerSize()
}

func (m *Model) pruneCodexSessionVisibility() {
	if projectPath := strings.TrimSpace(m.codexVisibleProject); projectPath != "" {
		if _, ok := m.codexSession(projectPath); !ok {
			m.codexVisibleProject = ""
			m.codexInput.Blur()
		}
	}
	if projectPath := strings.TrimSpace(m.codexHiddenProject); projectPath != "" {
		if _, ok := m.codexSession(projectPath); !ok {
			m.codexHiddenProject = ""
		}
	}
}

func (m Model) codexSession(projectPath string) (codexapp.Session, bool) {
	if m.codexManager == nil {
		return nil, false
	}
	return m.codexManager.Session(projectPath)
}

func (m Model) currentCodexSession() (codexapp.Session, bool) {
	return m.codexSession(m.codexVisibleProject)
}

func (m Model) currentCodexSnapshot() (codexapp.Snapshot, bool) {
	session, ok := m.currentCodexSession()
	if !ok {
		return codexapp.Snapshot{}, false
	}
	return session.Snapshot(), true
}

func (m Model) hasHiddenCodexSession() bool {
	return strings.TrimSpace(m.preferredHiddenCodexProject()) != ""
}

type codexBusyElsewhereRefresher interface {
	RefreshBusyElsewhere() error
}

func (m Model) refreshBusyElsewhereCmd(projectPath string) tea.Cmd {
	session, ok := m.codexSession(projectPath)
	if !ok {
		return nil
	}
	snapshot := session.Snapshot()
	if snapshot.Closed || !snapshot.BusyExternal {
		return nil
	}
	refresher, ok := session.(codexBusyElsewhereRefresher)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		_ = refresher.RefreshBusyElsewhere()
		return codexUpdateMsg{projectPath: projectPath}
	}
}

func (m Model) openCodexSessionCmd(plan codexcli.LaunchPlan) tea.Cmd {
	manager := m.codexManager
	return func() tea.Msg {
		if manager == nil {
			return codexSessionOpenedMsg{err: fmt.Errorf("Codex manager unavailable")}
		}
		session, reused, err := manager.Open(codexapp.LaunchRequest{
			ProjectPath: plan.ProjectPath,
			ResumeID:    plan.SessionID,
			ForceNew:    plan.Kind == codexcli.LaunchNew,
			Prompt:      plan.Prompt,
			Preset:      plan.Preset,
		})
		if err != nil {
			return codexSessionOpenedMsg{projectPath: plan.ProjectPath, err: err}
		}
		status := "Embedded Codex session opened. Alt+Up hides it."
		snapshot := session.Snapshot()
		switch {
		case snapshot.BusyExternal && strings.TrimSpace(plan.Prompt) != "":
			status = "Codex is already active in another process. Prompt was not sent; use /codex-new for a separate session."
		case snapshot.BusyExternal:
			status = "Codex is already active in another process. Embedded view is read-only until it finishes."
		case strings.TrimSpace(plan.Prompt) != "":
			status = "Prompt sent to embedded Codex. Alt+Up hides it."
		case reused:
			status = "Embedded Codex session reopened. Alt+Up hides it."
		}
		return codexSessionOpenedMsg{
			projectPath: plan.ProjectPath,
			status:      status,
		}
	}
}

func (m Model) submitVisibleCodexCmd(draft codexDraft) tea.Cmd {
	session, ok := m.currentCodexSession()
	if !ok {
		return nil
	}
	projectPath := m.codexVisibleProject
	submission := draft.Submission()
	steer := false
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		steer = codexSnapshotCanSteer(snapshot)
	}
	return func() tea.Msg {
		if err := session.SubmitInput(submission); err != nil {
			return codexActionMsg{projectPath: projectPath, restoreDraft: draft, err: err}
		}
		status := "Prompt sent to Codex"
		if steer {
			status = "Steer sent to the active Codex turn"
		}
		return codexActionMsg{projectPath: projectPath, status: status}
	}
}

func (m Model) showVisibleCodexStatusCmd() tea.Cmd {
	session, ok := m.currentCodexSession()
	if !ok {
		return nil
	}
	projectPath := m.codexVisibleProject
	return func() tea.Msg {
		if err := session.ShowStatus(); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "Embedded Codex status added to the transcript"}
	}
}

func (m Model) restartVisibleCodexSessionCmd(prompt string) tea.Cmd {
	if m.codexManager == nil || strings.TrimSpace(m.codexVisibleProject) == "" {
		return nil
	}
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	preset := codexcli.DefaultPreset()
	if snapshot, ok := m.currentCodexSnapshot(); ok && snapshot.Preset != "" {
		preset = snapshot.Preset
	}
	plan, err := codexcli.BuildLaunchPlan(projectPath, "", prompt, true, preset)
	if err != nil {
		return func() tea.Msg {
			return codexSessionOpenedMsg{projectPath: projectPath, err: err}
		}
	}
	return m.openCodexSessionCmd(plan)
}

func (m Model) interruptVisibleCodexCmd() tea.Cmd {
	session, ok := m.currentCodexSession()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		if err := session.Interrupt(); err != nil {
			return codexActionMsg{err: err}
		}
		return codexActionMsg{status: "Interrupt sent to Codex"}
	}
}

func (m Model) closeVisibleCodexCmd() tea.Cmd {
	if m.codexManager == nil || strings.TrimSpace(m.codexVisibleProject) == "" {
		return nil
	}
	projectPath := m.codexVisibleProject
	manager := m.codexManager
	return func() tea.Msg {
		if err := manager.CloseProject(projectPath); err != nil {
			return codexActionMsg{err: err}
		}
		return codexActionMsg{
			projectPath: projectPath,
			status:      "Embedded Codex session closed",
			closed:      true,
		}
	}
}

func (m Model) respondVisibleApprovalCmd(decision codexapp.ApprovalDecision) tea.Cmd {
	session, ok := m.currentCodexSession()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		if err := session.RespondApproval(decision); err != nil {
			return codexActionMsg{err: err}
		}
		return codexActionMsg{status: "Approval decision sent to Codex"}
	}
}

func (m Model) respondVisibleToolInputCmd(answers map[string][]string) tea.Cmd {
	session, ok := m.currentCodexSession()
	if !ok {
		return nil
	}
	projectPath := m.codexVisibleProject
	return func() tea.Msg {
		if err := session.RespondToolInput(answers); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "Structured input sent to Codex"}
	}
}

func (m Model) respondVisibleElicitationCmd(decision codexapp.ElicitationDecision, content json.RawMessage, restoreDraft codexDraft) tea.Cmd {
	session, ok := m.currentCodexSession()
	if !ok {
		return nil
	}
	projectPath := m.codexVisibleProject
	return func() tea.Msg {
		if err := session.RespondElicitation(decision, content); err != nil {
			return codexActionMsg{projectPath: projectPath, restoreDraft: restoreDraft, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "MCP input sent to Codex"}
	}
}

func (m Model) toggleCodexVisibility() (tea.Model, tea.Cmd) {
	m.ensureCodexRuntime()
	if m.codexVisible() {
		return m.hideCodexSession(), nil
	}
	projectPath := m.preferredHiddenCodexProject()
	if projectPath == "" {
		m.status = "No hidden Codex session"
		return m, nil
	}
	m.codexVisibleProject = projectPath
	m.codexHiddenProject = projectPath
	m.loadCodexDraft(projectPath)
	m.syncCodexViewport(true)
	m.status = "Embedded Codex session restored"
	return m, tea.Batch(m.codexInput.Focus(), m.refreshBusyElsewhereCmd(projectPath))
}

func (m Model) hideCodexSession() Model {
	if !m.codexVisible() {
		return m
	}
	m.persistVisibleCodexDraft()
	m.codexHiddenProject = m.codexVisibleProject
	m.codexVisibleProject = ""
	m.codexInput.Blur()
	m.status = "Embedded Codex session hidden."
	return m
}

func (m Model) cycleCodexSession(direction int) (tea.Model, tea.Cmd) {
	m.ensureCodexRuntime()
	nextProject := m.nextLiveCodexProject()
	if direction < 0 {
		nextProject = m.previousLiveCodexProject()
	}
	if nextProject == "" {
		m.status = "No live Codex sessions"
		return m, nil
	}
	current := strings.TrimSpace(m.codexVisibleProject)
	if current != "" && nextProject == current && len(m.liveCodexProjects()) == 1 {
		m.status = "Only one live Codex session"
		return m, nil
	}
	if current != "" {
		m.persistVisibleCodexDraft()
		m.codexHiddenProject = current
	}
	m.codexVisibleProject = nextProject
	m.codexHiddenProject = nextProject
	m.loadCodexDraft(nextProject)
	m.syncCodexViewport(true)
	if direction < 0 {
		m.status = "Switched to the previous embedded Codex session"
	} else {
		m.status = "Switched to the next embedded Codex session"
	}
	return m, tea.Batch(m.codexInput.Focus(), m.refreshBusyElsewhereCmd(nextProject))
}

func (m Model) updateCodexMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if projectPath := m.codexPendingOpenProject(); projectPath != "" {
		switch msg.String() {
		case "esc", "alt+up":
			m.status = "Embedded Codex is still starting for " + filepath.Base(projectPath)
		default:
			m.status = "Embedded Codex session is still starting..."
		}
		return m, nil
	}

	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		m.codexVisibleProject = ""
		m.status = "Codex session is no longer available"
		return m, nil
	}

	switch msg.String() {
	case "f3":
		return m.cycleCodexSession(1)
	case "alt+down":
		return m.openCodexPicker()
	case "esc":
		hidden := m.hideCodexSession()
		return hidden, nil
	case "alt+up":
		hidden := m.hideCodexSession()
		return hidden, nil
	case "alt+[":
		return m.cycleCodexSession(-1)
	case "alt+]":
		return m.cycleCodexSession(1)
	case "alt+l":
		m.codexDenseExpanded = !m.codexDenseExpanded
		if m.codexDenseExpanded {
			m.status = "Expanded dense transcript blocks"
		} else {
			m.status = "Collapsed dense transcript blocks"
		}
		m.syncCodexViewport(false)
		return m, nil
	case "ctrl+c":
		if snapshot.BusyExternal {
			m.status = "This Codex session is busy in another process. Interrupt it there or hide it here with Alt+Up."
			return m, nil
		}
		if codexSnapshotCanSteer(snapshot) {
			m.status = "Interrupting Codex turn..."
			return m, m.interruptVisibleCodexCmd()
		}
		if snapshot.Phase == codexapp.SessionPhaseFinishing {
			m.status = "Codex is finishing the current turn. Wait for the final output to settle or hide it with Alt+Up."
			return m, nil
		}
		if snapshot.Phase == codexapp.SessionPhaseReconciling {
			m.status = "Codex is rechecking whether the current turn has gone idle. Please wait a moment."
			return m, nil
		}
		m.status = "Closing embedded Codex session..."
		return m, m.closeVisibleCodexCmd()
	case "pgup":
		m.codexViewport.PageUp()
		return m, nil
	case "pgdown":
		m.codexViewport.PageDown()
		return m, nil
	}

	if snapshot.PendingApproval != nil {
		switch msg.String() {
		case "a":
			m.status = "Approving Codex request..."
			return m, m.respondVisibleApprovalCmd(codexapp.DecisionAccept)
		case "A":
			if !snapshot.PendingApproval.AllowsDecision(codexapp.DecisionAcceptForSession) {
				m.status = "This approval cannot be accepted for the whole session"
				return m, nil
			}
			m.status = "Approving Codex request for this session..."
			return m, m.respondVisibleApprovalCmd(codexapp.DecisionAcceptForSession)
		case "d":
			m.status = "Declining Codex request..."
			return m, m.respondVisibleApprovalCmd(codexapp.DecisionDecline)
		case "c":
			m.status = "Canceling Codex request..."
			return m, m.respondVisibleApprovalCmd(codexapp.DecisionCancel)
		default:
			return m, nil
		}
	}

	if snapshot.PendingToolInput != nil {
		return m.updateCodexToolInputMode(snapshot, msg)
	}

	if snapshot.PendingElicitation != nil {
		return m.updateCodexElicitationMode(snapshot, msg)
	}

	if snapshot.Closed {
		if msg.String() == "enter" {
			m.status = "Codex session is closed. Press Esc or Alt+Up to hide it, then reopen from the project list."
		}
		return m, nil
	}

	if m.codexSlashActive() {
		switch msg.String() {
		case "tab":
			if m.cycleAndApplyCodexSlashSuggestion(1) {
				return m, nil
			}
		case "shift+tab":
			if m.cycleAndApplyCodexSlashSuggestion(-1) {
				return m, nil
			}
		case "enter":
			raw := m.resolvedCodexSlashInput()
			inv, err := codexslash.Parse(raw)
			if err != nil {
				m.status = err.Error()
				return m, nil
			}
			m.clearCodexDraft(m.codexVisibleProject)
			switch inv.Kind {
			case codexslash.KindNew:
				m.status = "Starting a fresh embedded Codex session..."
				m.beginCodexPendingOpen(m.codexVisibleProject)
				return m, m.restartVisibleCodexSessionCmd(inv.Prompt)
			case codexslash.KindModel:
				m.openCodexModelPickerLoading()
				m.status = "Loading embedded Codex models..."
				return m, m.openCodexModelPickerCmd()
			case codexslash.KindStatus:
				m.status = "Reading embedded Codex status..."
				return m, m.showVisibleCodexStatusCmd()
			default:
				m.status = "Unsupported embedded slash command"
				return m, nil
			}
		}
	}

	switch msg.String() {
	case "enter":
		draft := m.currentCodexDraft()
		if draft.Empty() {
			return m, nil
		}
		if snapshot.BusyExternal {
			m.status = "This Codex session is already active in another process, so embedded Codex cannot steer it. Use /codex-new for a separate session."
			return m, nil
		}
		if snapshot.Phase == codexapp.SessionPhaseFinishing {
			m.status = "Codex is finishing the current turn. Wait for it to settle before sending another prompt."
			return m, nil
		}
		if snapshot.Phase == codexapp.SessionPhaseReconciling {
			m.status = "Codex is rechecking the current turn state. Wait for that to finish before sending another prompt."
			return m, nil
		}
		m.clearCodexDraft(m.codexVisibleProject)
		if codexSnapshotCanSteer(snapshot) {
			m.status = "Sending follow-up to Codex..."
		} else {
			m.status = "Sending prompt to Codex..."
		}
		return m, m.submitVisibleCodexCmd(draft)
	case "alt+enter", "ctrl+j":
		m.codexInput.InsertString("\n")
		m.persistVisibleCodexDraft()
		m.syncCodexComposerSize()
		return m, nil
	case "ctrl+v":
		if attached, err := m.tryAttachClipboardImage(); err != nil {
			m.status = err.Error()
			return m, nil
		} else if attached {
			return m, nil
		}
	case "backspace", "delete":
		if m.removeCodexAttachmentMarkerBeforeCursor() {
			m.persistVisibleCodexDraft()
			m.syncCodexComposerSize()
			return m, nil
		}
		if strings.TrimSpace(m.codexInput.Value()) == "" && m.removeLastCurrentCodexAttachment() {
			m.status = "Removed the last image attachment"
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.codexInput, cmd = m.codexInput.Update(msg)
	m.persistVisibleCodexDraft()
	m.syncCodexComposerSize()
	m.syncCodexSlashSelection()
	return m, cmd
}

func (m Model) updateCodexToolInputMode(snapshot codexapp.Snapshot, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	request := snapshot.PendingToolInput
	if request == nil {
		return m, nil
	}
	state := m.ensureToolAnswerState(m.codexVisibleProject, request)
	if len(request.Questions) == 0 {
		return m, nil
	}
	if state.QuestionIndex >= len(request.Questions) {
		state.QuestionIndex = max(0, len(request.Questions)-1)
	}
	question := request.Questions[state.QuestionIndex]

	switch msg.String() {
	case "tab":
		if len(request.Questions) > 1 {
			state.QuestionIndex = (state.QuestionIndex + 1) % len(request.Questions)
			m.codexToolAnswers[m.codexVisibleProject] = state
			m.status = "Moved to the next structured question"
		}
		return m, nil
	case "shift+tab":
		if len(request.Questions) > 1 {
			state.QuestionIndex = (state.QuestionIndex - 1 + len(request.Questions)) % len(request.Questions)
			m.codexToolAnswers[m.codexVisibleProject] = state
			m.status = "Moved to the previous structured question"
		}
		return m, nil
	case "enter":
		answer := strings.TrimSpace(m.codexInput.Value())
		if answer == "" {
			return m, nil
		}
		state.Answers[question.ID] = []string{answer}
		m.codexToolAnswers[m.codexVisibleProject] = state
		m.clearCodexDraft(m.codexVisibleProject)
		return m.finishOrAdvanceToolInput(request, state)
	case "backspace", "delete":
		// Keep textarea editing behavior below.
	default:
		if optionIndex, ok := numericOptionSelection(msg.String()); ok && optionIndex < len(question.Options) {
			state.Answers[question.ID] = []string{question.Options[optionIndex].Label}
			m.codexToolAnswers[m.codexVisibleProject] = state
			m.clearCodexDraft(m.codexVisibleProject)
			return m.finishOrAdvanceToolInput(request, state)
		}
	}

	var cmd tea.Cmd
	m.codexInput, cmd = m.codexInput.Update(msg)
	m.persistVisibleCodexDraft()
	m.syncCodexComposerSize()
	return m, cmd
}

func (m Model) finishOrAdvanceToolInput(request *codexapp.ToolInputRequest, state codexToolAnswerState) (tea.Model, tea.Cmd) {
	nextIndex := firstUnansweredToolQuestion(request, state.Answers)
	m.codexToolAnswers[m.codexVisibleProject] = state
	if nextIndex < len(request.Questions) {
		state.QuestionIndex = nextIndex
		m.codexToolAnswers[m.codexVisibleProject] = state
		m.status = "Answer recorded. Continue with the next structured question."
		return m, m.codexInput.Focus()
	}
	delete(m.codexToolAnswers, m.codexVisibleProject)
	m.status = "Sending structured input..."
	return m, m.respondVisibleToolInputCmd(state.Answers)
}

func (m Model) updateCodexElicitationMode(snapshot codexapp.Snapshot, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	request := snapshot.PendingElicitation
	if request == nil {
		return m, nil
	}

	switch msg.String() {
	case "d":
		m.status = "Declining MCP input request..."
		return m, m.respondVisibleElicitationCmd(codexapp.ElicitationDecline, nil, codexDraft{})
	case "c":
		m.status = "Canceling MCP input request..."
		return m, m.respondVisibleElicitationCmd(codexapp.ElicitationCancel, nil, codexDraft{})
	case "a", "enter":
		restore := m.currentCodexDraft()
		var content json.RawMessage
		if request.Mode == codexapp.ElicitationModeForm {
			content = encodeElicitationComposerInput(m.codexInput.Value())
			m.clearCodexDraft(m.codexVisibleProject)
		}
		m.status = "Sending MCP input..."
		return m, m.respondVisibleElicitationCmd(codexapp.ElicitationAccept, content, restore)
	case "alt+enter", "ctrl+j":
		if request.Mode == codexapp.ElicitationModeForm {
			m.codexInput.InsertString("\n")
			m.persistVisibleCodexDraft()
			m.syncCodexComposerSize()
			return m, nil
		}
		return m, nil
	}

	if request.Mode != codexapp.ElicitationModeForm {
		return m, nil
	}

	var cmd tea.Cmd
	m.codexInput, cmd = m.codexInput.Update(msg)
	m.persistVisibleCodexDraft()
	m.syncCodexComposerSize()
	return m, cmd
}

func (m *Model) tryAttachClipboardImage() (bool, error) {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return false, nil
	}
	path, err := clipboardImageExporter()
	if err != nil {
		if err == errClipboardHasNoImage {
			return false, nil
		}
		return false, err
	}
	attachment := codexapp.Attachment{
		Kind: codexapp.AttachmentLocalImage,
		Path: path,
	}
	m.appendCurrentCodexAttachment(attachment)
	attachments := m.currentCodexAttachments()
	index := len(attachments) - 1
	m.insertCodexAttachmentMarker(index, attachment)
	m.persistVisibleCodexDraft()
	m.syncCodexComposerSize()
	m.status = "Attached " + codexAttachmentComposerToken(index, attachment)
	return true, nil
}

func (m *Model) insertCodexAttachmentMarker(index int, attachment codexapp.Attachment) {
	token := codexAttachmentComposerToken(index, attachment)
	valueRunes := []rune(m.codexInput.Value())
	cursor := codexTextareaCursorOffset(m.codexInput)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(valueRunes) {
		cursor = len(valueRunes)
	}
	insert := token
	if cursor > 0 && !unicode.IsSpace(valueRunes[cursor-1]) {
		insert = " " + insert
	}
	if cursor == len(valueRunes) {
		if len(valueRunes) == 0 || !unicode.IsSpace(valueRunes[len(valueRunes)-1]) {
			insert += " "
		}
	} else if !unicode.IsSpace(valueRunes[cursor]) {
		insert += " "
	}
	m.codexInput.InsertString(insert)
}

func (m *Model) removeCodexAttachmentMarkerBeforeCursor() bool {
	attachments := m.currentCodexAttachments()
	if len(attachments) == 0 {
		return false
	}
	value := m.codexInput.Value()
	cursor := codexTextareaCursorOffset(m.codexInput)
	runes := []rune(value)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	prefix := string(runes[:cursor])
	for i := len(attachments) - 1; i >= 0; i-- {
		token := codexAttachmentComposerToken(i, attachments[i])
		switch {
		case strings.HasSuffix(prefix, token+" "):
			start := cursor - len([]rune(token)) - 1
			m.setCodexComposerValue(string(runes[:start])+string(runes[cursor:]), start)
			m.removeCurrentCodexAttachment(i)
			m.status = "Removed " + token
			return true
		case strings.HasSuffix(prefix, token):
			start := cursor - len([]rune(token))
			m.setCodexComposerValue(string(runes[:start])+string(runes[cursor:]), start)
			m.removeCurrentCodexAttachment(i)
			m.status = "Removed " + token
			return true
		}
	}
	return false
}

func (m *Model) syncCodexComposerSize() {
	width := m.width
	if width <= 0 {
		width = 120
	}
	m.codexInput.SetWidth(max(20, width-4))

	maxHeight := 8
	if m.height > 0 {
		maxHeight = max(3, min(10, m.height/3))
	}
	targetHeight := max(3, min(maxHeight, m.codexInput.LineCount()+1))
	m.codexInput.SetHeight(targetHeight)
}

func (m *Model) syncCodexViewport(resetToBottom bool) {
	if !m.codexVisible() {
		return
	}
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		return
	}
	width := m.width
	if width <= 0 {
		width = 120
	}
	height := m.height
	if height <= 0 {
		height = 30
	}

	lowerBlocks := m.codexLowerBlocks(snapshot, width)
	lowerHeight := countRenderedBlockLines(lowerBlocks)
	transcriptHeight := codexTranscriptContentHeight(height, lowerHeight)

	m.codexViewport.Width = max(24, width-4)
	m.codexViewport.Height = max(1, transcriptHeight)

	offset := m.codexViewport.YOffset
	m.codexViewport.SetContent(m.renderCodexTranscriptContent(m.codexViewport.Width))
	if resetToBottom {
		m.codexViewport.GotoBottom()
		return
	}
	maxOffset := max(0, m.codexViewport.TotalLineCount()-m.codexViewport.Height)
	if offset > maxOffset {
		offset = maxOffset
	}
	m.codexViewport.SetYOffset(offset)
}

func (m Model) renderCodexView() string {
	if projectPath := m.codexPendingOpenProject(); projectPath != "" {
		return m.renderCodexOpeningView(projectPath)
	}
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		return lipgloss.NewStyle().Bold(true).Render(brand.FullTitle + " | Codex session unavailable")
	}

	width := m.width
	if width <= 0 {
		width = 120
	}
	height := m.height
	if height <= 0 {
		height = 30
	}

	lowerBlocks := m.codexLowerBlocks(snapshot, width)
	lowerHeight := countRenderedBlockLines(lowerBlocks)
	transcriptHeight := codexTranscriptContentHeight(height, lowerHeight)

	transcript := m.codexViewport
	transcript.Width = max(24, width-4)
	transcript.Height = max(1, transcriptHeight)
	transcript.SetContent(m.renderCodexTranscriptContent(transcript.Width))
	body := m.renderFramedPane(transcript.View(), width, transcriptHeight, true)

	lines := []string{m.renderCodexBanner(snapshot, width), body}
	lines = append(lines, lowerBlocks...)
	return strings.Join(lines, "\n")
}

func codexTranscriptContentHeight(totalHeight, lowerHeight int) int {
	return max(3, totalHeight-lowerHeight-3)
}

func (m Model) renderCodexOpeningView(projectPath string) string {
	width := m.width
	if width <= 0 {
		width = 120
	}
	height := m.height
	if height <= 0 {
		height = 30
	}

	projectName := strings.TrimSpace(filepath.Base(projectPath))
	if projectName == "" || projectName == "." {
		projectName = projectPath
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")).Render("Codex | " + projectName)
	bodyHeight := max(3, height-6)
	body := m.renderFramedPane(strings.Join([]string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")).Render("Opening embedded Codex session..."),
		"",
		fitFooterWidth("Project: "+projectPath, max(24, width-8)),
		fitFooterWidth("Waiting for the previous Codex app-server to settle and for the new session to come online.", max(24, width-8)),
	}, "\n"), width, bodyHeight, true)
	footer := renderFooterLine(width, renderFooterStatus("Opening embedded Codex session"))
	return strings.Join([]string{renderFooterLine(width, title), body, footer}, "\n")
}

func (m Model) codexLowerBlocks(snapshot codexapp.Snapshot, width int) []string {
	switch {
	case snapshot.PendingApproval != nil:
		approvalActions := []footerAction{
			footerPrimaryAction("a", "accept"),
			footerExitAction("d", "decline"),
			footerLowAction("c", "cancel"),
			footerHideAction("Alt+Up", "hide"),
		}
		if snapshot.PendingApproval.AllowsDecision(codexapp.DecisionAcceptForSession) {
			approvalActions = []footerAction{
				footerPrimaryAction("a", "accept"),
				footerNavAction("A", "session"),
				footerExitAction("d", "decline"),
				footerLowAction("c", "cancel"),
				footerHideAction("Alt+Up", "hide"),
			}
		}
		lines := []string{}
		if meta := m.renderCodexSessionMeta(snapshot, width); meta != "" {
			lines = append(lines, meta)
		}
		lines = append(lines,
			fitFooterWidth("Approval: "+snapshot.PendingApproval.Summary(), width),
			renderFooterLine(width, renderFooterActionList(approvalActions...)),
		)
		return lines
	case snapshot.Closed:
		return []string{
			fitFooterWidth("Codex session closed. Esc or Alt+Up hides it; Enter on the project opens a new one.", width),
			"",
		}
	default:
		lines := []string{}
		if meta := m.renderCodexSessionMeta(snapshot, width); meta != "" {
			lines = append(lines, meta)
		}
		if snapshot.BusyExternal {
			lines = append(lines, m.renderCodexBusyElsewhereNotice(snapshot, width))
		}
		lines = append(lines, m.renderCodexFooter(snapshot, width))
		lines = append(lines, m.renderCodexRequestBlocks(snapshot, width)...)
		lines = append(lines, m.renderCodexSlashBlocks(width)...)
		input := m.codexInput
		input.SetWidth(max(20, width-4))
		input.SetHeight(max(3, min(10, m.codexInput.LineCount()+1)))
		lines = append(lines, renderCodexComposer(input, width))
		return lines
	}
}

func (m Model) renderCodexRequestBlocks(snapshot codexapp.Snapshot, width int) []string {
	switch {
	case snapshot.PendingToolInput != nil:
		return m.renderCodexToolInputBlocks(*snapshot.PendingToolInput, width)
	case snapshot.PendingElicitation != nil:
		return m.renderCodexElicitationBlocks(*snapshot.PendingElicitation, width)
	default:
		return nil
	}
}

func (m Model) renderCodexToolInputBlocks(request codexapp.ToolInputRequest, width int) []string {
	lines := []string{fitFooterWidth("Structured input: "+request.Summary(), width)}
	if len(request.Questions) == 0 {
		return lines
	}
	state := m.toolAnswerStateFor(m.codexVisibleProject, &request)
	if state.QuestionIndex >= len(request.Questions) {
		state.QuestionIndex = max(0, len(request.Questions)-1)
	}
	question := request.Questions[state.QuestionIndex]
	if len(request.Questions) > 1 {
		lines = append(lines, fitFooterWidth(fmt.Sprintf("Question %d/%d", state.QuestionIndex+1, len(request.Questions)), width))
	}
	if header := strings.TrimSpace(question.Header); header != "" {
		lines = append(lines, fitFooterWidth(header, width))
	}
	prompt := question.Question
	if question.IsSecret {
		prompt += " [secret]"
	}
	lines = append(lines, fitFooterWidth(prompt, width))
	for i, option := range question.Options {
		line := fmt.Sprintf("%d %s", i+1, strings.TrimSpace(option.Label))
		if desc := strings.TrimSpace(option.Description); desc != "" {
			line += " - " + desc
		}
		lines = append(lines, fitFooterWidth(line, width))
	}
	return lines
}

func (m Model) renderCodexElicitationBlocks(request codexapp.ElicitationRequest, width int) []string {
	lines := []string{fitFooterWidth("MCP input: "+request.Summary(), width)}
	if request.Mode == codexapp.ElicitationModeURL && strings.TrimSpace(request.URL) != "" {
		lines = append(lines, fitFooterWidth("Open this URL externally: "+request.URL, width))
	}
	if request.Mode == codexapp.ElicitationModeForm {
		lines = append(lines, fitFooterWidth("Paste JSON or text into the composer, then press Enter to accept.", width))
		if len(request.RequestedSchema) > 0 {
			schemaSummary := strings.TrimSpace(string(request.RequestedSchema))
			lines = append(lines, fitFooterWidth("Requested schema: "+schemaSummary, width))
		}
	}
	return lines
}

func (m Model) renderCodexBanner(snapshot codexapp.Snapshot, width int) string {
	parts := []string{"Codex"}
	if projectName := strings.TrimSpace(filepath.Base(snapshot.ProjectPath)); projectName != "" && projectName != "." {
		parts = append(parts, projectName)
	}
	if snapshot.BusyExternal {
		parts = append(parts, "Read-only")
	}
	if m.codexDenseExpanded {
		parts = append(parts, "Blocks expanded")
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")).Render(strings.Join(parts, " | "))
	actions := renderFooterActionList(
		footerNavAction("Alt+Down", "picker"),
		footerNavAction("Alt+[", "prev"),
		footerNavAction("Alt+]", "next"),
		footerLowAction("Alt+L", "blocks"),
	)
	overlay := ""
	contentWidth := width
	if snapshot.Preset == codexcli.PresetYolo && !snapshot.Closed {
		overlay = "  " + detailDangerStyle.Render("YOLO MODE")
		if overlayWidth := lipgloss.Width(overlay); overlayWidth >= width {
			return ansi.Cut(overlay, max(0, overlayWidth-width), overlayWidth)
		} else if width > 0 {
			contentWidth = width - overlayWidth
		}
	}
	line := renderFooterLine(contentWidth, title, actions)
	banner := lipgloss.PlaceHorizontal(max(0, contentWidth), lipgloss.Left, line)
	if overlay != "" {
		return banner + overlay
	}
	return banner
}

func overlayCodexBannerRight(base, overlay string, width int) string {
	if overlay == "" {
		return base
	}
	if width <= 0 {
		width = max(lipgloss.Width(base), lipgloss.Width(overlay))
	}
	if lipgloss.Width(base) < width {
		base = lipgloss.PlaceHorizontal(width, lipgloss.Left, base)
	}
	overlayWidth := lipgloss.Width(overlay)
	if overlayWidth >= width {
		return ansi.Cut(overlay, max(0, overlayWidth-width), overlayWidth)
	}
	left := width - overlayWidth
	prefix := ansi.Cut(base, 0, left)
	suffix := ansi.Cut(base, left+overlayWidth, width)
	return prefix + overlay + suffix
}

func (m Model) renderCodexBusyElsewhereNotice(snapshot codexapp.Snapshot, width int) string {
	message := strings.TrimSpace(snapshot.LastSystemNotice)
	if message == "" || !strings.Contains(strings.ToLower(message), "another codex process") {
		sessionID := shortID(snapshot.ThreadID)
		if sessionID == "" {
			sessionID = "this session"
		}
		message = fmt.Sprintf("Embedded Codex session %s is already active in another Codex process, so embedded controls are read-only until it finishes.", sessionID)
	}
	return renderCodexMessageBlock("Read-only", message, lipgloss.Color("221"), lipgloss.Color("252"), max(24, width-4))
}

func (m Model) renderCodexSessionMeta(snapshot codexapp.Snapshot, width int) string {
	segments := []string{}
	if model := strings.TrimSpace(snapshot.Model); model != "" {
		segments = append(segments, renderFooterMeta("Model")+" "+renderFooterStatus(model))
	}
	if reasoning := strings.TrimSpace(snapshot.ReasoningEffort); reasoning != "" {
		segments = append(segments, renderFooterMeta("Reasoning")+" "+renderFooterStatus(reasoning))
	}
	if context := codexSnapshotContextLeftLabel(snapshot); context != "" {
		segments = append(segments, renderFooterMeta("Context")+" "+renderFooterStatus(context))
	}
	if nextModel := strings.TrimSpace(snapshot.PendingModel); nextModel != "" {
		nextReasoning := firstNonEmptyCodexLabel(strings.TrimSpace(snapshot.PendingReasoning), strings.TrimSpace(snapshot.ReasoningEffort))
		next := nextModel
		if nextReasoning != "" {
			next += " / " + nextReasoning
		}
		segments = append(segments, renderFooterMeta("Next")+" "+renderFooterUsage(next))
	}
	if len(segments) == 0 {
		return ""
	}
	return renderFooterLine(width, segments...)
}

func codexSnapshotContextLeftLabel(snapshot codexapp.Snapshot) string {
	if snapshot.TokenUsage == nil || snapshot.TokenUsage.ModelContextWindow <= 0 {
		return ""
	}
	return fmt.Sprintf("%d%% left (%s tok)", snapshot.TokenUsage.ContextLeftPercent(), formatInt64(snapshot.TokenUsage.ContextLeftTokens()))
}

func firstNonEmptyCodexLabel(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (m Model) renderCodexFooter(snapshot codexapp.Snapshot, width int) string {
	status := codexFooterStatus(snapshot, m.currentTime())

	var actions []footerAction
	switch {
	case snapshot.PendingToolInput != nil:
		actions = append(actions,
			footerPrimaryAction("Enter", "answer"),
			footerExitAction("Ctrl+C", "close"),
			footerHideAction("Alt+Up", "hide"),
		)
		state := m.toolAnswerStateFor(m.codexVisibleProject, snapshot.PendingToolInput)
		if state.QuestionIndex >= 0 && state.QuestionIndex < len(snapshot.PendingToolInput.Questions) {
			if len(snapshot.PendingToolInput.Questions[state.QuestionIndex].Options) > 0 {
				actions = append(actions, footerNavAction("1-9", "choose"))
			}
		}
		if len(snapshot.PendingToolInput.Questions) > 1 {
			actions = append(actions, footerNavAction("Tab", "next"))
		}
	case snapshot.PendingElicitation != nil && snapshot.PendingElicitation.Mode == codexapp.ElicitationModeForm:
		actions = []footerAction{
			footerPrimaryAction("Enter", "accept"),
			footerExitAction("Ctrl+C", "close"),
			footerHideAction("Alt+Up", "hide"),
			footerExitAction("d", "decline"),
			footerLowAction("c", "cancel"),
			footerNavAction("Alt+Enter", "newline"),
		}
	case snapshot.PendingElicitation != nil:
		actions = []footerAction{
			footerPrimaryAction("Enter", "accept"),
			footerExitAction("Ctrl+C", "close"),
			footerHideAction("Alt+Up", "hide"),
			footerExitAction("d", "decline"),
			footerLowAction("c", "cancel"),
		}
	case m.codexSlashActive():
		actions = []footerAction{
			footerPrimaryAction("Enter", "run"),
			footerExitAction("Ctrl+C", "close"),
			footerHideAction("Alt+Up", "hide"),
			footerNavAction("Tab", "complete"),
			footerNavAction("Shift+Tab", "previous"),
			footerNavAction("Alt+Enter", "newline"),
		}
	case snapshot.BusyExternal:
		actions = []footerAction{
			footerHideAction("Alt+Up", "hide"),
			footerNavAction("/codex-new", "session"),
		}
	case snapshot.Phase == codexapp.SessionPhaseReconciling:
		actions = []footerAction{
			footerHideAction("Alt+Up", "hide"),
		}
	case snapshot.Phase == codexapp.SessionPhaseFinishing:
		actions = []footerAction{
			footerHideAction("Alt+Up", "hide"),
		}
	case snapshot.Busy:
		actions = []footerAction{
			footerPrimaryAction("Enter", "steer"),
			footerExitAction("Ctrl+C", "interrupt"),
			footerHideAction("Alt+Up", "hide"),
			footerNavAction("Alt+Enter", "newline"),
			footerNavAction("Ctrl+V", "image"),
		}
	case snapshot.Closed:
		actions = []footerAction{
			footerHideAction("Alt+Up", "hide"),
			footerLowAction("PgUp/PgDn", "scroll"),
		}
	default:
		actions = []footerAction{
			footerPrimaryAction("Enter", "send"),
			footerExitAction("Ctrl+C", "close"),
			footerHideAction("Alt+Up", "hide"),
			footerNavAction("Alt+Enter", "newline"),
			footerNavAction("Ctrl+V", "image"),
		}
	}

	segments := []string{}
	if status != "" {
		segments = append(segments, renderFooterStatus(status))
	}
	segments = append(segments, renderFooterActionList(actions...))
	return renderFooterLine(width, segments...)
}

func (m Model) renderCodexTranscriptContent(width int) string {
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		return "Codex session unavailable"
	}
	if rendered := m.renderCodexTranscriptEntries(snapshot, width); strings.TrimSpace(rendered) != "" {
		return rendered
	}
	if snapshot.Closed {
		return "Codex session closed."
	}
	if notice := strings.TrimSpace(snapshot.LastSystemNotice); notice != "" {
		return "[system] " + sanitizeCodexRenderedText(notice)
	}
	return "Type a prompt and press Enter."
}

func normalizedCodexStatus(status string) string {
	status = strings.TrimSpace(status)
	switch status {
	case "", "Codex session ready":
		return ""
	case "Codex turn complete", "Codex turn completed":
		return "Turn completed"
	default:
		if strings.HasPrefix(status, "Codex turn ") {
			return "Turn " + strings.TrimSpace(strings.TrimPrefix(status, "Codex turn "))
		}
		return status
	}
}

func codexSnapshotCanSteer(snapshot codexapp.Snapshot) bool {
	switch snapshot.Phase {
	case "", codexapp.SessionPhaseRunning:
		return snapshot.Busy && !snapshot.BusyExternal
	default:
		return false
	}
}

func codexFooterStatus(snapshot codexapp.Snapshot, now time.Time) string {
	switch snapshot.Phase {
	case codexapp.SessionPhaseReconciling:
		return "Rechecking turn status"
	case codexapp.SessionPhaseFinishing:
		if !snapshot.BusySince.IsZero() {
			return "Finishing " + formatRunningDuration(now.Sub(snapshot.BusySince))
		}
		return "Finishing"
	case codexapp.SessionPhaseExternal:
		if !snapshot.BusySince.IsZero() {
			return "Working elsewhere " + formatRunningDuration(now.Sub(snapshot.BusySince))
		}
		return "Working elsewhere"
	}
	if snapshot.Busy {
		if !snapshot.BusySince.IsZero() {
			return "Working " + formatRunningDuration(now.Sub(snapshot.BusySince))
		}
		return "Working"
	}
	return normalizedCodexStatus(snapshot.Status)
}

func formatRunningDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	totalSeconds := int64(d / time.Second)
	days := totalSeconds / (24 * 60 * 60)
	hours := (totalSeconds % (24 * 60 * 60)) / (60 * 60)
	minutes := (totalSeconds % (60 * 60)) / 60
	seconds := totalSeconds % 60

	switch {
	case days > 0:
		if hours > 0 {
			return fmt.Sprintf("%dd %02dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	case totalSeconds >= int64(time.Hour/time.Second):
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	default:
		return fmt.Sprintf("%02d:%02d", minutes, seconds)
	}
}

func (m Model) renderCodexTranscriptEntries(snapshot codexapp.Snapshot, width int) string {
	if len(snapshot.Entries) == 0 {
		snapshot.Entries = parseLegacyCodexTranscript(snapshot.Transcript)
	}
	if len(snapshot.Entries) == 0 {
		return ""
	}
	if width <= 0 {
		width = 80
	}
	contentWidth := max(18, width-4)
	blocks := make([]string, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		block := renderCodexTranscriptEntry(entry, contentWidth, m.codexDenseExpanded)
		if strings.TrimSpace(block) != "" {
			blocks = append(blocks, block)
		}
	}
	return strings.Join(blocks, "\n\n")
}

func renderCodexTranscriptEntry(entry codexapp.TranscriptEntry, width int, expanded bool) string {
	text := strings.TrimSpace(sanitizeCodexRenderedText(entry.Text))
	if text == "" {
		return ""
	}
	switch entry.Kind {
	case codexapp.TranscriptUser:
		return renderCodexUserMessageBlock(text, width)
	case codexapp.TranscriptAgent:
		return renderCodexMessageBlock("", text, lipgloss.Color("120"), lipgloss.Color("252"), width)
	case codexapp.TranscriptPlan:
		return renderCodexMessageBlock("Plan", text, lipgloss.Color("214"), lipgloss.Color("252"), width)
	case codexapp.TranscriptReasoning:
		return renderCodexMessageBlock("Reasoning", text, lipgloss.Color("220"), lipgloss.Color("250"), width)
	case codexapp.TranscriptCommand:
		return renderCodexDenseBlock("Command", text, lipgloss.Color("111"), width, expanded)
	case codexapp.TranscriptFileChange:
		return renderCodexDenseBlock("File changes", text, lipgloss.Color("179"), width, expanded)
	case codexapp.TranscriptTool:
		return renderCodexDenseBlock("Tool", text, lipgloss.Color("141"), width, expanded)
	case codexapp.TranscriptError:
		return renderCodexMessageBlock("Error", text, lipgloss.Color("203"), lipgloss.Color("252"), width)
	case codexapp.TranscriptStatus:
		return renderCodexStatusBlock(text, width)
	case codexapp.TranscriptSystem:
		return renderCodexMessageBlock("System", text, lipgloss.Color("244"), lipgloss.Color("246"), width)
	default:
		return renderCodexMessageBlock("", text, lipgloss.Color("244"), lipgloss.Color("252"), width)
	}
}

func sanitizeCodexRenderedText(text string) string {
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = ansi.Strip(text)

	var out strings.Builder
	out.Grow(len(text))
	for _, r := range text {
		switch {
		case r == '\n' || r == '\t':
			out.WriteRune(r)
		case unicode.IsControl(r):
			continue
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

func parseLegacyCodexTranscript(transcript string) []codexapp.TranscriptEntry {
	blocks := splitLegacyCodexTranscriptBlocks(transcript)
	if len(blocks) == 0 {
		return nil
	}
	entries := make([]codexapp.TranscriptEntry, 0, len(blocks))
	for _, block := range blocks {
		kind, text := parseLegacyCodexTranscriptBlock(block)
		if strings.TrimSpace(text) == "" {
			continue
		}
		entries = append(entries, codexapp.TranscriptEntry{Kind: kind, Text: text})
	}
	return entries
}

func splitLegacyCodexTranscriptBlocks(transcript string) []string {
	lines := strings.Split(strings.TrimSpace(transcript), "\n")
	blocks := make([]string, 0, len(lines))
	current := make([]string, 0, 4)
	flush := func() {
		if len(current) == 0 {
			return
		}
		blocks = append(blocks, strings.Join(current, "\n"))
		current = current[:0]
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	return blocks
}

func parseLegacyCodexTranscriptBlock(block string) (codexapp.TranscriptKind, string) {
	switch {
	case legacyTranscriptBlockHasPrefix(block, "You: "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "You: ")
		return codexapp.TranscriptUser, text
	case legacyTranscriptBlockHasPrefix(block, "Codex: "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "Codex: ")
		return codexapp.TranscriptAgent, text
	case legacyTranscriptBlockHasPrefix(block, "Plan: "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "Plan: ")
		return codexapp.TranscriptPlan, text
	case legacyTranscriptBlockHasPrefix(block, "Reasoning: "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "Reasoning: ")
		return codexapp.TranscriptReasoning, text
	case legacyTranscriptBlockHasPrefix(block, "[status] "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "[status] ")
		return codexapp.TranscriptStatus, text
	case legacyTranscriptBlockHasPrefix(block, "[system] "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "[system] ")
		return codexapp.TranscriptSystem, text
	case legacyTranscriptBlockHasPrefix(block, "[error] "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "[error] ")
		return codexapp.TranscriptError, text
	default:
		return codexapp.TranscriptOther, strings.TrimSpace(block)
	}
}

func legacyTranscriptBlockHasPrefix(block, prefix string) bool {
	_, ok := trimLegacyCodexTranscriptPrefix(block, prefix)
	return ok
}

func trimLegacyCodexTranscriptPrefix(block, prefix string) (string, bool) {
	lines := strings.Split(strings.TrimSpace(block), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], prefix) {
		return "", false
	}
	lines[0] = strings.TrimPrefix(lines[0], prefix)
	return strings.TrimSpace(strings.Join(lines, "\n")), true
}

type codexStatusBlockData struct {
	ThreadID           string
	ProjectPath        string
	CWD                string
	Model              string
	ModelProvider      string
	ReasoningEffort    string
	ServiceTier        string
	Approval           string
	Sandbox            string
	Network            string
	WritableRoots      string
	ContextTokens      int64
	TotalTokens        int64
	ModelContextWindow int64
	ContextUsedPercent int
	HasContextPercent  bool
	LastTurnTokens     int64
	UsageWindows       []codexStatusUsageWindow
}

type codexStatusUsageWindow struct {
	Limit       string
	Plan        string
	Window      string
	LeftPercent int
	ResetsAt    time.Time
}

type codexStatusUsageGroup struct {
	Limit   string
	Plan    string
	Windows []codexStatusUsageWindow
}

func renderCodexStatusBlock(body string, width int) string {
	status, ok := parseCodexStatusBlock(body)
	if !ok {
		return renderCodexMessageBlock("Status", body, lipgloss.Color("81"), lipgloss.Color("252"), width)
	}
	contentWidth := max(20, width-2)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	sectionStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	groupStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("153"))

	lines := []string{titleStyle.Render("Status")}
	lines = append(lines, renderCodexStatusSummaryRows(status, contentWidth)...)

	groups := groupCodexStatusUsageWindows(status.UsageWindows)
	if len(groups) > 0 {
		lines = append(lines, "")
		lines = append(lines, sectionStyle.Render("Usage left"))
		for index, group := range groups {
			if index > 0 {
				lines = append(lines, "")
			}
			title := group.Limit
			if strings.TrimSpace(group.Plan) != "" {
				title += " (" + group.Plan + ")"
			}
			lines = append(lines, groupStyle.Render(title))
			for _, window := range group.Windows {
				lines = append(lines, renderCodexStatusUsageRow(window, contentWidth))
			}
		}
	}

	footerRows := renderCodexStatusFooterRows(status)
	if len(footerRows) > 0 {
		lines = append(lines, "")
		lines = append(lines, footerRows...)
	}

	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(lipgloss.Color("81")).
		PaddingLeft(1).
		Render(strings.Join(lines, "\n"))
}

func parseCodexStatusBlock(body string) (codexStatusBlockData, bool) {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "Embedded Codex status" {
		return codexStatusBlockData{}, false
	}
	status := codexStatusBlockData{}
	for _, rawLine := range lines[1:] {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "usage window:") {
			window, ok := parseCodexStatusUsageWindow(strings.TrimSpace(strings.TrimPrefix(line, "usage window:")))
			if ok {
				status.UsageWindows = append(status.UsageWindows, window)
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "thread":
			status.ThreadID = value
		case "project":
			status.ProjectPath = value
		case "cwd":
			status.CWD = value
		case "model":
			status.Model = value
		case "model provider":
			status.ModelProvider = value
		case "reasoning effort":
			status.ReasoningEffort = value
		case "service tier":
			status.ServiceTier = value
		case "approval":
			status.Approval = value
		case "sandbox":
			status.Sandbox = value
		case "network":
			status.Network = value
		case "writable roots":
			status.WritableRoots = value
		case "total tokens":
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				status.TotalTokens = parsed
			}
		case "context tokens":
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				status.ContextTokens = parsed
			}
		case "model context window":
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				status.ModelContextWindow = parsed
			}
		case "context used percent":
			if parsed, err := strconv.Atoi(value); err == nil {
				status.ContextUsedPercent = clampCodexStatusPercent(parsed)
				status.HasContextPercent = true
			}
		case "last turn tokens":
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				status.LastTurnTokens = parsed
			}
		}
	}
	return status, true
}

func parseCodexStatusUsageWindow(spec string) (codexStatusUsageWindow, bool) {
	window := codexStatusUsageWindow{}
	hasLimit := false
	hasWindow := false
	hasLeft := false
	for _, rawPart := range strings.Split(spec, ";") {
		part := strings.TrimSpace(rawPart)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "limit":
			window.Limit = value
			hasLimit = value != ""
		case "plan":
			window.Plan = value
		case "window":
			window.Window = value
			hasWindow = value != ""
		case "left":
			if parsed, err := strconv.Atoi(value); err == nil {
				window.LeftPercent = clampCodexStatusPercent(parsed)
				hasLeft = true
			}
		case "resetsat":
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed > 0 {
				window.ResetsAt = time.Unix(parsed, 0).Local()
			}
		}
	}
	if !hasLimit || !hasWindow || !hasLeft {
		return codexStatusUsageWindow{}, false
	}
	return window, true
}

func renderCodexStatusSummaryRows(status codexStatusBlockData, width int) []string {
	labelWidth := 11
	rows := make([]string, 0, 6)
	modelStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	reasoningStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	valueStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	if status.Model != "" {
		value := modelStyle.Render(status.Model)
		extras := make([]string, 0, 2)
		if status.ModelProvider != "" {
			extras = append(extras, status.ModelProvider)
		}
		if status.ServiceTier != "" {
			extras = append(extras, "tier "+status.ServiceTier)
		}
		if len(extras) > 0 {
			value += " " + mutedStyle.Render("("+strings.Join(extras, ", ")+")")
		}
		rows = append(rows, renderCodexStatusField("Model", value, labelWidth))
	}
	if status.ReasoningEffort != "" {
		rows = append(rows, renderCodexStatusField("Reasoning", reasoningStyle.Render(status.ReasoningEffort), labelWidth))
	}
	directory := status.CWD
	if strings.TrimSpace(directory) == "" {
		directory = status.ProjectPath
	}
	if directory != "" {
		rows = append(rows, renderCodexStatusField("Directory", valueStyle.Render(directory), labelWidth))
	}
	contextTokens := status.ContextTokens
	if contextTokens <= 0 {
		contextTokens = status.TotalTokens
	}
	if contextTokens > 0 {
		contextValue := valueStyle.Render(fmt.Sprintf("%s tokens", formatInt64(contextTokens)))
		if status.ModelContextWindow > 0 {
			details := fmt.Sprintf("of %s", formatInt64(status.ModelContextWindow))
			if status.HasContextPercent {
				details += fmt.Sprintf(" (%d%% used)", status.ContextUsedPercent)
			}
			contextValue += " " + mutedStyle.Render(details)
		}
		rows = append(rows, renderCodexStatusField("Context", contextValue, labelWidth))
	}
	if status.LastTurnTokens > 0 {
		rows = append(rows, renderCodexStatusField("Last turn", valueStyle.Render(fmt.Sprintf("%s tokens", formatInt64(status.LastTurnTokens))), labelWidth))
	}
	return rows
}

func renderCodexStatusFooterRows(status codexStatusBlockData) []string {
	labelWidth := 11
	mutedValueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
	rows := make([]string, 0, 2)

	accessParts := make([]string, 0, 3)
	if status.Approval != "" {
		accessParts = append(accessParts, status.Approval)
	}
	if status.Sandbox != "" {
		accessParts = append(accessParts, status.Sandbox)
	}
	if status.Network != "" {
		accessParts = append(accessParts, "network "+status.Network)
	}
	if len(accessParts) > 0 {
		rows = append(rows, renderCodexStatusField("Access", mutedValueStyle.Render(strings.Join(accessParts, " | ")), labelWidth))
	}
	if status.WritableRoots != "" {
		rows = append(rows, renderCodexStatusField("Writable", mutedValueStyle.Render(status.WritableRoots), labelWidth))
	}
	if status.ThreadID != "" {
		rows = append(rows, renderCodexStatusField("Session", mutedValueStyle.Render(status.ThreadID), labelWidth))
	}
	return rows
}

func groupCodexStatusUsageWindows(windows []codexStatusUsageWindow) []codexStatusUsageGroup {
	groups := make([]codexStatusUsageGroup, 0, len(windows))
	indexByKey := make(map[string]int)
	for _, window := range windows {
		key := strings.ToLower(window.Limit) + "|" + strings.ToLower(window.Plan)
		index, ok := indexByKey[key]
		if !ok {
			index = len(groups)
			indexByKey[key] = index
			groups = append(groups, codexStatusUsageGroup{
				Limit: window.Limit,
				Plan:  window.Plan,
			})
		}
		groups[index].Windows = append(groups[index].Windows, window)
	}
	return groups
}

func renderCodexStatusUsageRow(window codexStatusUsageWindow, width int) string {
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Width(12)
	resetStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	percentStyle := codexStatusUsagePercentStyle(window.LeftPercent)
	label := codexStatusWindowTitle(window.Window)
	bar := renderCodexStatusProgressBar(window.LeftPercent, codexStatusProgressBarWidth(width))
	leftText := percentStyle.Render(fmt.Sprintf("%3d%% left", window.LeftPercent))
	base := labelStyle.Render(label) + " " + bar + " " + leftText
	resetText := formatCodexStatusReset(window.ResetsAt)
	if resetText == "" {
		return base
	}
	resetRendered := resetStyle.Render(resetText)
	if lipgloss.Width(base+" "+resetRendered) <= width {
		return base + " " + resetRendered
	}
	return base + "\n" + lipgloss.NewStyle().MarginLeft(13).Foreground(lipgloss.Color("244")).Render(resetText)
}

func renderCodexStatusProgressBar(leftPercent, width int) string {
	if width <= 0 {
		width = 10
	}
	filled := (clampCodexStatusPercent(leftPercent)*width + 50) / 100
	if filled > width {
		filled = width
	}
	empty := width - filled
	fillStyle := codexStatusUsagePercentStyle(leftPercent)
	frameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	emptyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	return frameStyle.Render("[") +
		fillStyle.Render(strings.Repeat("=", filled)) +
		emptyStyle.Render(strings.Repeat("-", empty)) +
		frameStyle.Render("]")
}

func renderCodexStatusField(label, value string, labelWidth int) string {
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Width(labelWidth)
	return labelStyle.Render(label+":") + " " + value
}

func codexStatusProgressBarWidth(width int) int {
	switch {
	case width >= 84:
		return 22
	case width >= 66:
		return 18
	case width >= 52:
		return 14
	default:
		return 10
	}
}

func codexStatusUsagePercentStyle(leftPercent int) lipgloss.Style {
	switch {
	case leftPercent >= 75:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	case leftPercent >= 40:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	default:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
	}
}

func codexStatusWindowTitle(window string) string {
	window = strings.TrimSpace(window)
	switch strings.ToLower(window) {
	case "":
		return "Limit"
	case "weekly":
		return "Weekly limit"
	}
	return window + " limit"
}

func formatCodexStatusReset(reset time.Time) string {
	if reset.IsZero() {
		return ""
	}
	now := time.Now().In(reset.Location())
	if sameCodexStatusDay(now, reset) {
		return "resets " + reset.Format("15:04")
	}
	if now.Year() == reset.Year() {
		return "resets " + reset.Format("15:04 on 2 Jan")
	}
	return "resets " + reset.Format("15:04 on 2 Jan 2006")
}

func sameCodexStatusDay(left, right time.Time) bool {
	left = left.In(right.Location())
	return left.Year() == right.Year() && left.YearDay() == right.YearDay()
}

func clampCodexStatusPercent(percent int) int {
	switch {
	case percent < 0:
		return 0
	case percent > 100:
		return 100
	default:
		return percent
	}
}

func formatInt64(value int64) string {
	if value == 0 {
		return "0"
	}
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	digits := strconv.FormatInt(value, 10)
	if len(digits) <= 3 {
		return sign + digits
	}
	var out strings.Builder
	out.Grow(len(digits) + len(digits)/3)
	for index, r := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(r)
	}
	return sign + out.String()
}

func renderCodexMessageBlock(label, body string, accent, bodyColor lipgloss.Color, width int) string {
	return renderCodexMessageBlockWithStyle(label, body, accent, bodyColor, width, false)
}

func renderCodexUserMessageBlock(body string, width int) string {
	return renderCodexMessageBlockWithStyle("", body, lipgloss.Color("81"), lipgloss.Color("252"), width, true)
}

func renderCodexMessageBlockWithStyle(label, body string, accent, bodyColor lipgloss.Color, width int, shaded bool) string {
	paddingRight := 0
	style := lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(accent).
		PaddingLeft(1)
	if shaded {
		paddingRight = 1
		style = style.PaddingRight(1).Background(codexComposerShellColor)
	}
	contentWidth := max(10, width-2-paddingRight)
	lines := []string{}
	if strings.TrimSpace(label) != "" {
		lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(accent).Render(label))
	}
	lines = append(lines, renderCodexBody(body, bodyColor, contentWidth))
	return style.Render(strings.Join(lines, "\n"))
}

func renderCodexComposer(input textarea.Model, width int) string {
	if width <= 0 {
		width = input.Width() + 4
	}
	return lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Background(codexComposerShellColor).
		Foreground(lipgloss.Color("252")).
		Render(input.View())
}

func renderCodexMonospaceBlock(label, body string, accent lipgloss.Color, width int) string {
	contentWidth := max(10, width-2)
	title := lipgloss.NewStyle().Bold(true).Foreground(accent).Render(label)
	renderedLines := make([]string, 0, len(strings.Split(body, "\n")))
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "$ "):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true).Render(line))
		case strings.HasPrefix(line, "diff --git "), strings.HasPrefix(line, "index "):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Bold(true).Render(line))
		case strings.HasPrefix(line, "@@"):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true).Render(line))
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "+"):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(line))
		case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "-"):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render(line))
		case strings.HasPrefix(line, "# "):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true).Render(line))
		case strings.HasPrefix(line, "[command ") || strings.HasPrefix(line, "[file changes "):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("149")).Bold(true).Render(line))
		default:
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Render(line))
		}
	}
	bodyBlock := lipgloss.NewStyle().Width(contentWidth).Render(strings.Join(renderedLines, "\n"))
	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(accent).
		PaddingLeft(1).
		Render(title + "\n" + bodyBlock)
}

func renderCodexDenseBlock(label, body string, accent lipgloss.Color, width int, expanded bool) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 {
		return ""
	}
	const compactLimit = 8
	hidden := 0
	if !expanded && len(lines) > compactLimit {
		hidden = len(lines) - compactLimit
		lines = lines[:compactLimit]
	}
	title := label
	if hidden > 0 {
		title = fmt.Sprintf("%s (%d more lines hidden; Alt+L expands)", label, hidden)
	}
	return renderCodexMonospaceBlock(title, strings.Join(lines, "\n"), accent, width)
}

func renderCodexBody(body string, color lipgloss.Color, width int) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			inFence = !inFence
			out = append(out, lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true).Render(line))
		case inFence:
			out = append(out, lipgloss.NewStyle().Foreground(lipgloss.Color("180")).Render(line))
		case strings.HasPrefix(trimmed, "[attached image]"):
			out = append(out, renderCodexInlineMarkdown(line, lipgloss.NewStyle().Foreground(lipgloss.Color("179")).Bold(true)))
		case strings.HasPrefix(trimmed, "## "):
			out = append(out, renderCodexInlineMarkdown(strings.TrimPrefix(trimmed, "## "), lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)))
		case strings.HasPrefix(trimmed, "# "):
			out = append(out, renderCodexInlineMarkdown(strings.TrimPrefix(trimmed, "# "), lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)))
		case strings.HasPrefix(trimmed, "> "):
			out = append(out, renderCodexInlineMarkdown(line, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Italic(true)))
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			out = append(out, renderCodexInlineMarkdown("• "+strings.TrimSpace(trimmed[2:]), lipgloss.NewStyle().Foreground(lipgloss.Color("151"))))
		default:
			out = append(out, renderCodexInlineMarkdown(line, lipgloss.NewStyle().Foreground(color)))
		}
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(out, "\n"))
}

func renderCodexInlineMarkdown(text string, style lipgloss.Style) string {
	if text == "" {
		return style.Render(text)
	}
	var out strings.Builder
	remaining := text
	for len(remaining) > 0 {
		start := strings.IndexByte(remaining, '[')
		if start < 0 {
			out.WriteString(style.Render(remaining))
			break
		}
		if start > 0 {
			out.WriteString(style.Render(remaining[:start]))
		}
		label, target, consumed, ok := parseCodexMarkdownLink(remaining[start:])
		if !ok {
			out.WriteString(style.Render(string(remaining[start])))
			remaining = remaining[start+1:]
			continue
		}
		out.WriteString(renderCodexHyperlink(label, target, style))
		remaining = remaining[start+consumed:]
	}
	return out.String()
}

func parseCodexMarkdownLink(text string) (label, target string, consumed int, ok bool) {
	if !strings.HasPrefix(text, "[") {
		return "", "", 0, false
	}
	closeLabel := strings.Index(text, "](")
	if closeLabel <= 1 {
		return "", "", 0, false
	}
	closeTarget := strings.IndexByte(text[closeLabel+2:], ')')
	if closeTarget < 0 {
		return "", "", 0, false
	}
	label = text[1:closeLabel]
	target = text[closeLabel+2 : closeLabel+2+closeTarget]
	if strings.TrimSpace(label) == "" || strings.TrimSpace(target) == "" {
		return "", "", 0, false
	}
	return label, target, closeLabel + 3 + closeTarget, true
}

func renderCodexHyperlink(label, target string, style lipgloss.Style) string {
	target = codexHyperlinkTarget(target)
	linkStyle := style.Copy().Foreground(lipgloss.Color("111")).Underline(true)
	renderedLabel := linkStyle.Render(label)
	if target == "" {
		return renderedLabel
	}
	return ansi.SetHyperlink(target) + renderedLabel + ansi.ResetHyperlink()
}

func codexHyperlinkTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if strings.HasPrefix(target, "/") {
		path := target
		fragment := ""
		if before, after, found := strings.Cut(path, "#"); found {
			path = before
			fragment = after
		}
		return (&url.URL{Scheme: "file", Path: path, Fragment: fragment}).String()
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" {
		return target
	}
	return parsed.String()
}

func numericOptionSelection(key string) (int, bool) {
	if len(key) != 1 {
		return 0, false
	}
	if key[0] < '1' || key[0] > '9' {
		return 0, false
	}
	return int(key[0] - '1'), true
}

func encodeElicitationComposerInput(text string) json.RawMessage {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if json.Valid([]byte(text)) {
		return json.RawMessage(text)
	}
	encoded, err := json.Marshal(text)
	if err != nil {
		return nil
	}
	return json.RawMessage(encoded)
}

func countRenderedBlockLines(blocks []string) int {
	total := 0
	for _, block := range blocks {
		if block == "" {
			total++
			continue
		}
		total += strings.Count(block, "\n") + 1
	}
	return total
}
