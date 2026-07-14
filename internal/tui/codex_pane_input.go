package tui

import (
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexslash"
	"lcroom/internal/viewportnav"
	"strings"
	"unicode"
)

func (m Model) updateCodexMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if projectPath := m.codexPendingOpenProject(); m.codexPendingOpenVisible() && projectPath != "" {
		if snapshot, ok := m.nonBlockingCodexSnapshot(projectPath); ok && codexSnapshotCanSettlePendingOpen(snapshot) {
			requestID := m.codexPendingOpen.requestID
			reveal := m.revealPendingEmbeddedOpenForSnapshot(projectPath, snapshot)
			openCmd := m.finishCodexPendingOpenRequest(projectPath, requestID, snapshot, true, reveal)
			m.setCodexOpenRequestReveal(requestID, false)
			updated, cmd := m.updateCodexMode(msg)
			return updated, batchCmds(openCmd, cmd)
		}
		label := m.codexPendingOpenProvider().Label()
		switch msg.String() {
		case "alt+up", "esc":
			return m.hidePendingCodexOpen(projectPath)
		case "enter":
			if m.currentCodexDraft().Empty() {
				m.status = "Embedded " + label + " session is still starting..."
			} else {
				m.status = "Draft saved. Press Enter once the " + label + " session is ready to send it."
			}
			return m, nil
		case "alt+enter", "ctrl+j":
			m.codexInput.InsertString("\n")
			m.noteCodexComposerKey(true)
			m.persistVisibleCodexDraft()
			m.syncCodexComposerSize()
			return m, nil
		case "ctrl+v":
			if handled, cmd := m.tryHandleCodexPaste(msg, true); handled {
				return m, cmd
			}
		}
		if handled, cmd := m.tryHandleCodexPaste(msg, true); handled {
			return m, cmd
		}
		if codexShouldIgnoreTextareaWordBackward(&m.codexInput, msg) || codexShouldIgnoreStraySGRMousePacket(msg) {
			return m, nil
		}
		var cmd tea.Cmd
		before := m.codexInput.Value()
		m.codexInput, cmd = m.codexInput.Update(msg)
		m.noteCodexComposerKey(m.codexInput.Value() != before)
		m.persistVisibleCodexDraft()
		m.syncCodexComposerSize()
		return m, cmd
	}

	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		m.codexVisibleProject = ""
		m.status = "Embedded session is no longer available"
		return m, nil
	}
	label := embeddedProvider(snapshot).Label()
	m.normalizeEmbeddedCodexFocus()
	m.normalizeEmbeddedSidebarSelection(snapshot)

	if msg.String() == "alt+up" {
		return m.hideCodexSession()
	}

	if m.embeddedSidebarDetail != nil {
		return m.updateEmbeddedSidebarDetailMode(msg)
	}

	if m.codexInputCopyDialog != nil {
		return m.updateCodexInputCopyDialogMode(msg)
	}

	if m.codexInputSelectionActive() {
		return m.updateCodexInputSelectionMode(msg)
	}

	if msg.String() == "esc" {
		return m.hideCodexSession()
	}
	if msg.String() == "alt+s" {
		if m.codexPanelFocus == embeddedCodexFocusSidebar {
			return m.updateCodexSidebarMode(snapshot, msg)
		}
		cmd := m.focusEmbeddedCodexSidebar()
		return m, cmd
	}
	if m.codexPanelFocus == embeddedCodexFocusSidebar {
		return m.updateCodexSidebarMode(snapshot, msg)
	}

	switch msg.String() {
	case "f3":
		return m.cycleCodexSession(1)
	case "alt+[", "alt+]":
		return m, nil
	case "alt+l":
		m.codexDenseBlockMode = m.codexDenseBlockMode.next()
		m.status = m.codexDenseBlockMode.statusText()
		m.syncCodexViewport(false)
		return m, m.requestVisibleCodexTranscriptRenderCmd()
	case "alt+o":
		return m.openCodexArtifactPicker(snapshot)
	case "ctrl+c":
		if snapshot.BusyExternal {
			m.status = "This " + label + " session is busy in another process. Interrupt it there or hide it here with Alt+Up."
			return m, nil
		}
		if codexSnapshotBrowserWaitingForUser(snapshot) {
			interruptCmd := m.interruptVisibleCodexCmd()
			updated, hideCmd := m.hideCodexSession()
			m = normalizeUpdateModel(updated)
			m.status = "Stopping " + label + " browser wait and returning to the project list..."
			return m, batchCmds(interruptCmd, hideCmd)
		}
		if snapshot.Phase == codexapp.SessionPhaseReconciling && codexStatusIsCompacting(snapshot.Status) {
			m.status = label + " is compacting conversation history. Wait for it to finish or hide it with Alt+Up."
			return m, nil
		}
		if codexSnapshotCanInterruptActiveTurn(snapshot) {
			m.status = "Interrupting " + label + " turn..."
			return m, m.interruptVisibleCodexCmd()
		}
		if snapshot.Phase == codexapp.SessionPhaseStalled && snapshot.Busy && strings.TrimSpace(snapshot.ActiveTurnID) != "" {
			m.status = "Interrupting stuck " + label + " turn..."
			return m, m.interruptVisibleCodexCmd()
		}
		if snapshot.Phase == codexapp.SessionPhaseFinishing {
			m.status = label + " is finishing the current turn. Wait for the final output to settle or hide it with Alt+Up."
			return m, nil
		}
		if snapshot.Phase == codexapp.SessionPhaseReconciling {
			if codexStatusIsCompacting(snapshot.Status) {
				m.status = label + " is compacting conversation history. Wait for it to finish before sending another prompt."
				return m, nil
			}
			m.status = label + " is rechecking whether the current turn has gone idle. If this persists, use /reconnect or /sessions."
			return m, nil
		}
		m.status = "Closing embedded " + label + " session..."
		return m, m.closeVisibleCodexCmd()
	case "pgup":
		viewportnav.PageUp(&m.codexViewport)
		m.maybeLoadFullCodexHistoryAtViewportTop()
		return m, nil
	case "pgdown":
		viewportnav.PageDown(&m.codexViewport)
		return m, nil
	case "ctrl+u":
		m.codexViewport.HalfPageUp()
		m.maybeLoadFullCodexHistoryAtViewportTop()
		return m, nil
	case "ctrl+d":
		m.codexViewport.HalfPageDown()
		return m, nil
	case "alt+c":
		if snapshot.PendingApproval != nil || snapshot.Closed {
			return m, nil
		}
		m.openCodexInputCopyDialog()
		return m, nil
	}

	if snapshot.PendingApproval != nil {
		switch msg.String() {
		case "a":
			m.status = "Approving " + label + " request..."
			return m, m.respondVisibleApprovalCmd(codexapp.DecisionAccept)
		case "A":
			if !snapshot.PendingApproval.AllowsDecision(codexapp.DecisionAcceptForSession) {
				m.status = "This approval cannot be accepted for the whole session"
				return m, nil
			}
			if snapshot.Provider == codexapp.ProviderLCAgent {
				m.status = "Switching LCAgent to Medium for this run..."
			} else {
				m.status = "Approving " + label + " request for this session..."
			}
			return m, m.respondVisibleApprovalCmd(codexapp.DecisionAcceptForSession)
		case "d":
			m.status = "Declining " + label + " request..."
			return m, m.respondVisibleApprovalCmd(codexapp.DecisionDecline)
		case "c":
			m.status = "Canceling " + label + " request..."
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

	// Clear mouse selection on any keypress.
	m.codexComposerSelection = textSelection{}

	if m.codexSlashActive() {
		switch msg.String() {
		case "up", "ctrl+p":
			m.moveCodexSlashSelection(-1)
			return m, nil
		case "down", "ctrl+n":
			m.moveCodexSlashSelection(1)
			return m, nil
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
				if hostInv, ok := codexHostSlashCommand(raw); ok {
					m.clearCodexDraft(m.codexVisibleProject)
					m.err = nil
					return m.dispatchCommand(hostInv)
				}
				m.status = err.Error()
				return m, nil
			}
			m.clearCodexDraft(m.codexVisibleProject)
			if snapshot.Closed && (inv.Kind == codexslash.KindModel ||
				inv.Kind == codexslash.KindStatus ||
				inv.Kind == codexslash.KindShowStatus ||
				inv.Kind == codexslash.KindCompact ||
				inv.Kind == codexslash.KindReview ||
				inv.Kind == codexslash.KindGoal ||
				inv.Kind == codexslash.KindPermissions) {
				m.status = label + " session is closed. Use /resume, /new, or /reconnect to reopen it."
				return m, nil
			}
			switch inv.Kind {
			case codexslash.KindNew:
				m.status = "Starting a new embedded " + label + " session..."
				m.beginNewCodexPendingOpen(m.codexVisibleProject, embeddedProvider(snapshot))
				return m, m.restartVisibleCodexSessionCmd(inv.Prompt)
			case codexslash.KindResume:
				if strings.TrimSpace(inv.SessionID) == "" {
					return m.openVisibleCodexResumePicker()
				}
				return m.openCodexSessionChoice(codexSessionChoice{
					ProjectPath: m.codexVisibleProject,
					ProjectName: projectNameForPicker(m.pickerProjectSummary(m.codexVisibleProject), m.codexVisibleProject),
					SessionID:   inv.SessionID,
					Provider:    embeddedProvider(snapshot),
				})
			case codexslash.KindReconnect:
				m.status = "Reconnecting embedded " + label + " session..."
				m.beginCodexPendingOpen(m.codexVisibleProject, embeddedProvider(snapshot))
				return m, m.reconnectVisibleCodexSessionCmd()
			case codexslash.KindModel:
				if embeddedProvider(snapshot).Normalized() == codexapp.ProviderLCAgent {
					return m.openEmbeddedLCAgentModelPicker()
				}
				m.openCodexModelPickerLoading()
				m.status = "Loading embedded " + label + " models..."
				return m, m.openCodexModelPickerCmd()
			case codexslash.KindStatus, codexslash.KindShowStatus:
				m.setCodexLCAgentStatusVisible(snapshot.ProjectPath, true)
				m.status = "Reading embedded " + label + " status..."
				return m, m.showVisibleCodexStatusCmd()
			case codexslash.KindGoal:
				switch inv.GoalAction {
				case codexslash.GoalActionShow:
					m.status = "Reading embedded " + label + " goal..."
					return m, m.showVisibleCodexGoalCmd()
				case codexslash.GoalActionSet:
					m.status = "Setting embedded " + label + " goal..."
					return m, m.setVisibleCodexGoalCmd(inv.GoalObjective, inv.GoalTokenBudget)
				case codexslash.GoalActionPause:
					m.status = "Pausing embedded " + label + " goal..."
					return m, m.pauseVisibleCodexGoalCmd()
				case codexslash.GoalActionResume:
					m.status = "Resuming embedded " + label + " goal..."
					return m, m.resumeVisibleCodexGoalCmd()
				case codexslash.GoalActionClear:
					if snapshot.Busy && codexSnapshotGoalCanBeStopped(snapshot) {
						m.status = "Stopping embedded " + label + " goal..."
					} else {
						m.status = "Clearing embedded " + label + " goal..."
					}
					return m, m.clearVisibleCodexGoalCmd()
				default:
					m.status = "Unsupported embedded goal action"
					return m, nil
				}
			case codexslash.KindCompact:
				m.status = "Starting embedded " + label + " conversation compaction..."
				return m, m.compactVisibleCodexSessionCmd()
			case codexslash.KindReview:
				m.status = "Starting embedded " + label + " review..."
				return m, m.reviewVisibleCodexSessionCmd()
			case codexslash.KindDevLCReview:
				if embeddedProvider(snapshot) != codexapp.ProviderLCAgent {
					m.status = "/dev-lcreview is only available for embedded LCAgent sessions"
					return m, nil
				}
				m.status = "Adding LCAgent review TODO..."
				return m, m.addDevLCAgentReviewTodoCmd(snapshot)
			case codexslash.KindPermissions:
				if strings.TrimSpace(inv.PermissionLevel) == "" {
					m.status = "Reading embedded " + label + " permissions..."
					return m, m.showVisibleCodexPermissionsCmd()
				}
				m.status = "Setting embedded " + label + " permissions..."
				return m, m.setVisibleCodexPermissionCmd(inv.PermissionLevel)
			case codexslash.KindChat:
				return m.openHelpChatModeOrSetupPrompt()
			case codexslash.KindSkills:
				return m, m.openSkillsDialog()
			case codexslash.KindSettings:
				if embeddedProvider(snapshot) == codexapp.ProviderLCAgent {
					return m, m.openEmbeddedLCAgentSettingsMode(m.codexVisibleProject)
				}
				return m, m.openSettingsMode()
			case codexslash.KindTerminal:
				if strings.TrimSpace(m.codexVisibleProject) == "" {
					m.status = "Terminal unavailable: no embedded project"
					return m, nil
				}
				m.status = "Opening project terminal..."
				return m, m.openProjectDirInTerminalCmd(m.codexVisibleProject)
			default:
				m.status = "Unsupported embedded slash command"
				return m, nil
			}
		}
	}

	if snapshot.Closed {
		if msg.String() == "enter" {
			m.status = label + " session is closed. Use /resume, /new, /reconnect, or reopen it from the project list."
		}
		return m, nil
	}

	focusCmd := tea.Cmd(nil)
	if codexSnapshotBrowserWaitingForUser(snapshot) && !m.codexInput.Focused() {
		focusCmd = m.codexInput.Focus()
	}

	if handled, cmd := m.tryHandleCodexPaste(msg, true); handled {
		return m, batchCmds(focusCmd, cmd)
	}

	switch msg.String() {
	case "ctrl+o":
		pageURL := managedBrowserCurrentPageURL(snapshot)
		sessionKey := strings.TrimSpace(snapshot.ManagedBrowserSessionKey)
		if pageURL != "" && sessionKey != "" && snapshot.CurrentBrowserPageStale && !m.managedBrowserCanReveal(snapshot) {
			m.status = "That browser page came from the resumed transcript and is no longer attached. Ask the assistant to reopen it if you still need it."
			return m, nil
		}
		if sessionKey == "" || snapshot.BusyExternal || snapshot.Closed {
			if status := m.codexBrowserReconnectStatus(snapshot); status != "" {
				m.status = status
			}
			return m, nil
		}
		if !managedBrowserRevealTargetAttached(snapshot) {
			m.status = "Managed browser window is not attached to this session. Use /reconnect or start a fresh session if you need the browser again."
			return m, nil
		}
		canReveal := m.managedBrowserCanReveal(snapshot)
		if !canReveal {
			if _, ok := m.attachedManagedBrowserSessionState(snapshot); !ok {
				m.status = "Managed browser window is not attached to this session. Use /reconnect or start a fresh session if you need the browser again."
				return m, nil
			}
		}
		if canReveal {
			m.status = "Showing the managed browser window..."
		} else {
			m.status = "Checking the managed browser window..."
		}
		m.markManagedBrowserStateChecking(sessionKey)
		return m, m.probeAndRevealManagedBrowserCmd(
			sessionKey,
			managedBrowserLeaseRef(embeddedProvider(snapshot), firstNonEmptyString(snapshot.ProjectPath, m.codexVisibleProject), snapshot.ThreadID),
			"Managed browser window is ready. Continue there, then return here when you want Codex to keep going.",
		)
	case "enter":
		draft := m.currentCodexDraft()
		if draft.Empty() {
			return m, focusCmd
		}
		refreshCmd := tea.Cmd(nil)
		if refreshed, ok, needsAsync := m.refreshCodexSubmitSnapshot(m.codexVisibleProject, snapshot); ok {
			snapshot = refreshed
			if needsAsync {
				refreshCmd = m.deferredCodexSnapshotCmd(m.codexVisibleProject)
			}
		} else if needsAsync {
			refreshCmd = m.deferredCodexSnapshotCmd(m.codexVisibleProject)
		}
		if snapshot.BusyExternal {
			if cmd := embeddedNewCommand(embeddedProvider(snapshot)); cmd != "" {
				m.status = "This " + label + " session is already active in another process, so the embedded view cannot steer it. Use " + cmd + " for a separate session."
			} else {
				m.status = "This " + label + " session is read-only."
			}
			return m, batchCmds(focusCmd, refreshCmd)
		}
		if snapshot.Phase == codexapp.SessionPhaseFinishing {
			m.status = label + " is finishing the current turn. Wait for it to settle before sending another prompt."
			return m, batchCmds(focusCmd, refreshCmd)
		}
		if snapshot.Phase == codexapp.SessionPhaseReconciling {
			if codexStatusIsCompacting(snapshot.Status) {
				m.status = label + " is compacting conversation history. Wait for it to finish before sending another prompt."
				return m, batchCmds(focusCmd, refreshCmd)
			}
			m.status = label + " is rechecking the current turn state. If this persists, use /reconnect or /sessions before sending another prompt."
			return m, batchCmds(focusCmd, refreshCmd)
		}
		if snapshot.Phase == codexapp.SessionPhaseStalled {
			m.status = label + " looks stuck or disconnected. Interrupt the current turn with ctrl+c or use /reconnect before sending another prompt."
			return m, batchCmds(focusCmd, refreshCmd)
		}
		if snapshot.Busy && !codexSnapshotCanSubmitBusyInput(snapshot) {
			m.status = label + " is already running. Wait for it to finish before sending another prompt."
			return m, batchCmds(focusCmd, refreshCmd)
		}
		m.clearCodexDraft(m.codexVisibleProject)
		if codexSnapshotGoalPausesOnPrompt(snapshot) {
			m.status = "Pausing embedded " + label + " goal..."
		} else if codexSnapshotQueuesBusyInput(snapshot) {
			m.status = "Queueing steer for " + label + "..."
		} else if codexSnapshotCanSteer(snapshot) {
			m.status = "Sending follow-up to " + label + "..."
		} else {
			m.status = "Sending prompt to " + label + "..."
		}
		return m, batchCmds(refreshCmd, m.submitVisibleCodexCmd(draft))
	case "alt+enter", "ctrl+j":
		m.codexInput.InsertString("\n")
		m.noteCodexComposerKey(true)
		m.persistVisibleCodexDraft()
		m.syncCodexComposerSize()
		return m, focusCmd
	case "ctrl+v":
		return m, focusCmd
	case "backspace", "delete":
		if m.removeCodexPastedTextMarkerBeforeCursor() {
			m.noteCodexComposerKey(true)
			m.persistVisibleCodexDraft()
			m.syncCodexComposerSize()
			return m, focusCmd
		}
		if m.removeCodexAttachmentMarkerBeforeCursor() {
			m.noteCodexComposerKey(true)
			m.persistVisibleCodexDraft()
			m.syncCodexComposerSize()
			return m, focusCmd
		}
		if strings.TrimSpace(m.codexInput.Value()) == "" && m.removeLastCurrentCodexAttachment() {
			m.noteCodexComposerKey(true)
			m.status = "Removed the last image attachment"
			return m, focusCmd
		}
	}

	if codexShouldIgnoreTextareaWordBackward(&m.codexInput, msg) {
		return m, focusCmd
	}
	if codexShouldIgnoreStraySGRMousePacket(msg) {
		return m, focusCmd
	}

	var cmd tea.Cmd
	before := m.codexInput.Value()
	m.codexInput, cmd = m.codexInput.Update(msg)
	m.noteCodexComposerKey(m.codexInput.Value() != before)
	m.persistVisibleCodexDraft()
	m.syncCodexComposerSize()
	m.syncCodexSlashSelection()
	return m, batchCmds(focusCmd, cmd)
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

	if handled, cmd := m.tryHandleCodexPaste(msg, false); handled {
		return m, cmd
	}

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

	if codexShouldIgnoreTextareaWordBackward(&m.codexInput, msg) {
		return m, nil
	}
	if codexShouldIgnoreStraySGRMousePacket(msg) {
		return m, nil
	}

	var cmd tea.Cmd
	before := m.codexInput.Value()
	m.codexInput, cmd = m.codexInput.Update(msg)
	m.noteCodexComposerKey(m.codexInput.Value() != before)
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
	loginURL := managedBrowserLoginURL(embeddedProvider(snapshot), snapshot.BrowserActivity.Policy, request.Mode, request.URL)

	if request.Mode == codexapp.ElicitationModeForm {
		if handled, cmd := m.tryHandleCodexPaste(msg, false); handled {
			return m, cmd
		}
	}

	switch msg.String() {
	case "o":
		if loginURL == "" || strings.TrimSpace(snapshot.ManagedBrowserSessionKey) == "" {
			if status := m.codexBrowserReconnectStatus(snapshot); status != "" {
				m.status = status
			}
			return m, nil
		}
		return m.openManagedBrowserLogin(
			firstNonEmptyString(snapshot.ProjectPath, m.codexVisibleProject),
			embeddedProvider(snapshot),
			snapshot.ThreadID,
			snapshot.ManagedBrowserSessionKey,
			snapshot.BrowserActivity,
			loginURL,
			"Showing the managed browser window...",
			"Managed browser window is ready. Finish the browser flow there, then press Enter when you are ready to continue.",
		)
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
			m.noteCodexComposerKey(true)
			m.persistVisibleCodexDraft()
			m.syncCodexComposerSize()
			return m, nil
		}
		return m, nil
	}

	if request.Mode != codexapp.ElicitationModeForm {
		return m, nil
	}

	if codexShouldIgnoreTextareaWordBackward(&m.codexInput, msg) {
		return m, nil
	}
	if codexShouldIgnoreStraySGRMousePacket(msg) {
		return m, nil
	}

	var cmd tea.Cmd
	before := m.codexInput.Value()
	m.codexInput, cmd = m.codexInput.Update(msg)
	m.noteCodexComposerKey(m.codexInput.Value() != before)
	m.persistVisibleCodexDraft()
	m.syncCodexComposerSize()
	return m, cmd
}

func (m *Model) tryHandleCodexPaste(msg tea.KeyMsg, allowImage bool) (bool, tea.Cmd) {
	switch {
	case msg.Paste || (allowImage && codexBulkTextInput(msg)):
		text := string(msg.Runes)
		if !shouldCollapseCodexPaste(text) {
			return false, nil
		}
		m.insertCodexPastedText(text)
		m.noteCodexComposerKey(true)
		return true, nil
	case msg.Type != tea.KeyCtrlV:
		return false, nil
	}

	if allowImage {
		attached, err := m.tryAttachClipboardImage()
		if err != nil {
			m.status = err.Error()
			m.noteCodexComposerKey(false)
			return true, nil
		}
		if attached {
			m.noteCodexComposerKey(true)
			return true, nil
		}
	}

	text, err := clipboardTextReader()
	if err != nil {
		m.reportError("Clipboard paste failed", err, m.codexVisibleProject)
		m.noteCodexComposerKey(false)
		return true, nil
	}
	if text == "" {
		m.noteCodexComposerKey(false)
		return true, nil
	}
	if shouldCollapseCodexPaste(text) {
		m.insertCodexPastedText(text)
		m.noteCodexComposerKey(true)
		return true, nil
	}
	m.codexInput.InsertString(text)
	m.noteCodexComposerKey(true)
	m.persistVisibleCodexDraft()
	m.syncCodexComposerSize()
	m.syncCodexSlashSelection()
	return true, nil
}

func codexBulkTextInput(msg tea.KeyMsg) bool {
	if msg.Paste || msg.Type != tea.KeyRunes || msg.Alt {
		return false
	}
	return shouldCollapseCodexPaste(string(msg.Runes))
}

func (m *Model) tryAttachClipboardImage() (bool, error) {
	projectPath := m.codexComposerProjectPath()
	if projectPath == "" {
		return false, nil
	}
	attachment, err := clipboardImageAttachment()
	if err != nil {
		if err == errClipboardHasNoImage {
			return false, nil
		}
		return false, err
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

func (m *Model) insertCodexPastedText(text string) {
	if m.codexComposerProjectPath() == "" {
		return
	}
	token := m.nextCodexPastedTextToken(text)
	m.insertCodexComposerToken(token)
	pasted := codexPastedText{
		Token: token,
		Text:  text,
	}
	m.appendCurrentCodexPastedText(pasted)
	m.persistVisibleCodexDraft()
	m.syncCodexComposerSize()
	m.syncCodexSlashSelection()
	m.status = "Pasted " + codexPastedTextPlaceholder(text) + " as a placeholder"
}

func (m *Model) insertCodexAttachmentMarker(index int, attachment codexapp.Attachment) {
	m.insertCodexComposerToken(codexAttachmentComposerToken(index, attachment))
}

func (m *Model) insertCodexComposerToken(token string) {
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

func (m *Model) removeCodexPastedTextMarkerBeforeCursor() bool {
	pastedTexts := m.currentCodexPastedTexts()
	if len(pastedTexts) == 0 {
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
	for i := len(pastedTexts) - 1; i >= 0; i-- {
		token := pastedTexts[i].Token
		switch {
		case strings.HasSuffix(prefix, token+" "):
			start := cursor - len([]rune(token)) - 1
			m.setCodexComposerValue(string(runes[:start])+string(runes[cursor:]), start)
			m.removeCurrentCodexPastedTextByToken(token)
			m.status = "Removed " + codexPastedTextPlaceholder(pastedTexts[i].Text) + " placeholder"
			return true
		case strings.HasSuffix(prefix, token):
			start := cursor - len([]rune(token))
			m.setCodexComposerValue(string(runes[:start])+string(runes[cursor:]), start)
			m.removeCurrentCodexPastedTextByToken(token)
			m.status = "Removed " + codexPastedTextPlaceholder(pastedTexts[i].Text) + " placeholder"
			return true
		}
	}
	return false
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
	width := m.embeddedCodexMainWidth()
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
	done := m.beginUIPhase("syncCodexViewport", strings.TrimSpace(m.codexVisibleProject), fmt.Sprintf("reset=%t", resetToBottom))
	defer done()
	if !m.codexVisible() {
		return
	}
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		return
	}
	width := m.embeddedCodexMainWidth()
	if width <= 0 {
		width = 120
	}
	height := m.height
	if height <= 0 {
		height = 30
	}

	projectPath := strings.TrimSpace(m.codexVisibleProject)
	providerLabel := embeddedProvider(snapshot).Label()
	stickToBottom := resetToBottom || m.codexViewport.AtBottom()
	lowerBlocks := []string{}
	m.measureAISyncLatency("Embedded lower blocks", projectPath, providerLabel, func() {
		lowerBlocks = m.codexLowerBlocks(snapshot, width)
	})
	lowerHeight := countRenderedBlockLines(lowerBlocks)
	transcriptHeight := codexTranscriptContentHeight(height, lowerHeight)

	m.codexViewport.Width = max(24, width)
	m.codexViewport.Height = max(1, transcriptHeight)

	offset := m.codexViewport.YOffset
	if !m.codexViewportContentMatches(projectPath, m.codexViewport.Width) {
		if !m.codexViewportContentCanStayStale(projectPath, m.codexViewport.Width, snapshot) {
			m.measureAISyncLatency("Embedded viewport sync", projectPath, providerLabel, func() {
				m.setCodexViewportTranscript(projectPath, snapshot, m.codexViewport.Width)
			})
		}
	}
	// Keep the latest reply visible when lower assistant blocks appear or disappear
	// while the transcript is already pinned to the end.
	if stickToBottom {
		m.codexViewport.GotoBottom()
		return
	}
	maxOffset := max(0, m.codexViewport.TotalLineCount()-m.codexViewport.Height)
	if offset > maxOffset {
		offset = maxOffset
	}
	m.codexViewport.SetYOffset(offset)
}

func (m *Model) maybeLoadFullCodexHistoryAtViewportTop() bool {
	if !m.codexVisible() || m.codexViewport.YOffset > 0 {
		return false
	}
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" || m.codexTranscriptFullHistoryLoaded(projectPath) {
		return false
	}
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		return false
	}
	entries := codexTranscriptEntriesFromSnapshot(snapshot)
	if !codexTranscriptLiveViewWouldLimit(entries, m.codexDenseBlockMode.normalized()) {
		return false
	}

	width := m.codexViewport.Width
	if width <= 0 {
		width = m.width
	}
	if width <= 0 {
		width = 120
	}
	m.codexViewport.Width = max(24, width)
	m.loadFullCodexTranscriptHistory(projectPath)
	m.setCodexViewportTranscript(projectPath, snapshot, m.codexViewport.Width)
	m.codexViewport.SetYOffset(0)
	m.status = "Loaded full transcript history"
	return true
}
