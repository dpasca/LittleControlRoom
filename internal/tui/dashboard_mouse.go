package tui

import tea "github.com/charmbracelet/bubbletea"

func (m *Model) handleDashboardMouse(msg tea.MouseMsg) tea.Cmd {
	if m.dashboardMouseBlocked() {
		return nil
	}
	if cmd, handled := m.handleRuntimePaneMouse(msg); handled {
		return cmd
	}

	layout := m.bodyLayout()
	if msg.X < 0 || msg.X >= layout.width || msg.Y < 1 || msg.Y >= 1+layout.listPaneHeight {
		return nil
	}
	if msg.Action != tea.MouseActionPress {
		return nil
	}
	switch msg.Button {
	case tea.MouseButtonLeft:
		m.focusProjectsPane()
	case tea.MouseButtonWheelUp:
		m.focusProjectsPane()
		return m.moveSelectionBy(-3)
	case tea.MouseButtonWheelDown:
		m.focusProjectsPane()
		return m.moveSelectionBy(3)
	}
	return nil
}

// Dashboard hit testing must not act on panes hidden behind a dialog or a
// different input surface. Keep this aligned with the key routing in update.
func (m Model) dashboardMouseBlocked() bool {
	return m.helpChatMode || m.codexVisible() || m.diffView != nil ||
		m.projectCollaborationDialog != nil || m.wakeupsDialog != nil || m.claudeAPIKeyWarning != nil ||
		m.externalControlReviewActive() || m.quitConfirm != nil || m.busySessionReplacement != nil ||
		m.actionNoticeDialog != nil || m.selfUpdateDialog != nil || m.attentionDialog != nil ||
		m.bossSetupPrompt != nil || m.codexArtifactPicker != nil || m.codexLCAgentProviderSetup != nil ||
		m.codexModelPickerVisible() || m.codexHandoffDialog != nil || m.codexPickerVisible ||
		m.localModelPickerVisible || m.settingsLCAgentModelPicker != nil ||
		m.settingsAIBackendPickerVisible || m.settingsBossChatPickerVisible ||
		m.settingsLCAgentProviderVisible || m.settingsBrowserPickerVisible ||
		m.settingsLCAgentSearchPickerVisible || m.settingsChoicePicker != nil ||
		m.ignoredPickerVisible || m.errorLogVisible || m.mobileDialogOpen ||
		m.cpuRemediationEditor != nil || m.cpuDialog != nil || m.portsDialog != nil ||
		m.processDialog != nil || m.skillsDialog != nil || m.suspendedTurnDialog != nil ||
		m.cloneProjectDialog != nil || m.newProjectDialog != nil || m.newTaskDialog != nil ||
		m.mergeConflictResolverProviderDialog != nil || m.runCommandDialog != nil ||
		m.staleWorktreeCleanupVisible() || m.codexCleanupVisible() ||
		m.worktreeRestore != nil || m.worktreeMergeRecoveryDialog != nil ||
		m.worktreeMergeConfirm != nil || m.worktreePostMerge != nil ||
		m.worktreeRemoveConfirm != nil || m.orphanedWorktreeInspection != nil ||
		m.repositoryIntegrityDialog != nil || m.scratchTaskAction != nil ||
		m.agentTaskAction != nil || m.projectRemoveConfirm != nil || m.externalStopConfirm != nil ||
		m.todoDeleteConfirm != nil || m.todoExistingWorktree != nil || m.todoPendingLaunchDialog != nil ||
		m.todoCopyDialog != nil || m.todoWorktreeEditor != nil || m.todoEditor != nil || m.todoDialog != nil ||
		m.projectFilterDialog != nil || m.categoryDialog != nil || m.archiveDialog != nil ||
		m.gitStatusDialog != nil || m.commitPreview != nil || m.commandMode ||
		m.setupMode || m.settingsMode || m.showPerf || m.showAIStats ||
		m.browserAttention != nil || m.questionNotify != nil
}
