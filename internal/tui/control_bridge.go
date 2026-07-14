package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	bossui "lcroom/internal/boss"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) executeBossControlInvocation(msg bossui.ControlInvocationConfirmedMsg) (tea.Model, tea.Cmd) {
	inv, err := control.ValidateInvocation(msg.Invocation)
	if err != nil {
		status := "Control request invalid: " + err.Error()
		m.status = status
		return m, bossControlResultCmd(msg.Invocation, status, err)
	}
	outcome := m.executeControlInvocationWithOutcome(inv)
	m = outcome.model
	resultInv := inv
	if outcome.inv.Capability != "" {
		resultInv = outcome.inv
	}
	if outcome.cmd == nil {
		return m, bossControlResultCmd(resultInv, m.status, outcome.err)
	}
	if resultInv.Capability == control.CapabilityProjectSetCategory || resultInv.Capability == control.CapabilityTodoAdd || resultInv.Capability == control.CapabilityProjectCreateAndStartEngineer || resultInv.Capability == control.CapabilityTodoCreateWorktreeAndStartEngineer {
		return m, outcome.cmd
	}
	return m, bossControlExecutionCmd(resultInv, outcome.cmd)
}

func (m Model) executeControlInvocation(inv control.Invocation) (tea.Model, tea.Cmd) {
	outcome := m.executeControlInvocationWithOutcome(inv)
	return outcome.model, outcome.cmd
}

type controlInvocationOutcome struct {
	model Model
	cmd   tea.Cmd
	err   error
	inv   control.Invocation
}

type bossTodoWorktreeTodoCreatedMsg struct {
	inv                control.Invocation
	input              control.TodoCreateWorktreeAndStartEngineerInput
	project            model.ProjectSummary
	provider           codexapp.Provider
	projectSetupAction service.CreateOrAttachProjectAction
	todo               model.TodoItem
	err                error
}

type bossProjectCreateAndStartEngineerCreatedMsg struct {
	inv            control.Invocation
	input          control.ProjectCreateAndStartEngineerInput
	provider       codexapp.Provider
	result         service.CreateOrAttachProjectResult
	project        model.ProjectSummary
	folderExists   bool
	gitInitialized bool
	projectTracked bool
	err            error
}

type bossProjectCategorySetMsg struct {
	inv      control.Invocation
	input    control.ProjectSetCategoryInput
	result   service.CreateOrAttachProjectResult
	project  model.ProjectSummary
	category model.ProjectCategory
	err      error
}

type bossTodoAddedMsg struct {
	inv     control.Invocation
	input   control.TodoAddInput
	project model.ProjectSummary
	todo    model.TodoItem
	err     error
}

type bossTodoWorktreePreparedMsg struct {
	inv                control.Invocation
	input              control.TodoCreateWorktreeAndStartEngineerInput
	project            model.ProjectSummary
	provider           codexapp.Provider
	projectSetupAction service.CreateOrAttachProjectAction
	todo               model.TodoItem
	result             service.CreateTodoWorktreeResult
	err                error
}

type bossTrackedTodo struct {
	ID          int64
	Label       string
	Text        string
	ProjectPath string
	ProjectName string
	Provider    model.SessionSource
	SessionID   string
	StartedAt   time.Time
}

func (m Model) executeControlInvocationWithOutcome(inv control.Invocation) controlInvocationOutcome {
	normalized, err := control.ValidateInvocation(inv)
	if err != nil {
		m.status = "Control request invalid: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}

	switch normalized.Capability {
	case control.CapabilityEngineerSendPrompt:
		var input control.EngineerSendPromptInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return m.executeEngineerSendPromptControlWithOutcome(input)
	case control.CapabilityAgentTaskCreate:
		var input control.AgentTaskCreateInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return m.executeAgentTaskCreateControlWithOutcome(input)
	case control.CapabilityAgentTaskContinue:
		var input control.AgentTaskContinueInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return m.executeAgentTaskContinueControlWithOutcome(input)
	case control.CapabilityAgentTaskClose:
		var input control.AgentTaskCloseInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return m.executeAgentTaskCloseControlWithOutcome(input)
	case control.CapabilityProjectCreateAndStartEngineer:
		var input control.ProjectCreateAndStartEngineerInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return m.executeProjectCreateAndStartEngineerControlWithOutcome(input)
	case control.CapabilityProjectSetCategory:
		var input control.ProjectSetCategoryInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return m.executeProjectSetCategoryControlWithOutcome(input)
	case control.CapabilityProjectArchive:
		var input control.ProjectArchiveInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return m.executeProjectArchiveControlWithOutcome(input)
	case control.CapabilityScratchTaskArchive:
		var input control.ScratchTaskArchiveInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return m.executeScratchTaskArchiveControlWithOutcome(input)
	case control.CapabilityTodoAdd:
		var input control.TodoAddInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return m.executeTodoAddControlWithOutcome(input)
	case control.CapabilityTodoCreateWorktreeAndStartEngineer:
		var input control.TodoCreateWorktreeAndStartEngineerInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return m.executeTodoCreateWorktreeAndStartEngineerControlWithOutcome(input)
	case control.CapabilityTodoComplete:
		var input control.TodoCompleteInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return m.executeTodoCompleteControlWithOutcome(input)
	case control.CapabilitySettingsUpdate:
		var input control.SettingsUpdateInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return m.executeSettingsUpdateControlWithOutcome(input)
	case control.CapabilityGitPrepareCommit:
		var input control.GitPrepareCommitInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return m.executeGitPrepareCommitControlWithOutcome(input)
	default:
		err := fmt.Errorf("unsupported capability: %s", normalized.Capability)
		m.status = "Control request unsupported: " + string(normalized.Capability)
		return controlInvocationOutcome{model: m, err: err}
	}
}

func (m Model) recordBossTrackedTodoFromControlResult(msg bossui.ControlInvocationResultMsg) Model {
	if msg.Err != nil || msg.Activity == nil || msg.Activity.TodoID <= 0 {
		return m
	}
	activity := *msg.Activity
	key := bossTrackedTodoKey(activity.ProjectPath, activity.Provider, activity.SessionID)
	if key == "" {
		return m
	}
	if m.bossTrackedTodos == nil {
		m.bossTrackedTodos = map[string]bossTrackedTodo{}
	}
	m.bossTrackedTodos[key] = bossTrackedTodo{
		ID:          activity.TodoID,
		Label:       strings.TrimSpace(activity.TodoLabel),
		Text:        strings.TrimSpace(activity.TodoText),
		ProjectPath: strings.TrimSpace(activity.ProjectPath),
		ProjectName: bossActivityProjectName(activity),
		Provider:    model.NormalizeSessionSource(activity.Provider),
		SessionID:   strings.TrimSpace(activity.SessionID),
		StartedAt:   activity.StartedAt,
	}
	return m
}

func bossActivityProjectName(activity bossui.ViewEngineerActivity) string {
	title := strings.TrimSpace(activity.Title)
	if activity.TodoID <= 0 {
		return title
	}
	label := bossTrackedTodoTargetLabel("", activity.TodoID, activity.TodoLabel, activity.TodoText)
	return strings.TrimSpace(strings.TrimSuffix(title, " "+label))
}

func bossTrackedTodoKey(projectPath string, provider model.SessionSource, sessionID string) string {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "." || projectPath == "" {
		return ""
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	provider = model.NormalizeSessionSource(provider)
	if provider == "" {
		provider = model.SessionSourceUnknown
	}
	return strings.Join([]string{projectPath, string(provider), sessionID}, "\x00")
}

func (m Model) bossTrackedTodoForSnapshot(projectPath string, snapshot codexapp.Snapshot) (bossTrackedTodo, bool) {
	if len(m.bossTrackedTodos) == 0 {
		return bossTrackedTodo{}, false
	}
	provider := modelSessionSourceFromCodexProvider(embeddedProvider(snapshot))
	key := bossTrackedTodoKey(projectPath, provider, snapshot.ThreadID)
	if key == "" {
		return bossTrackedTodo{}, false
	}
	todo, ok := m.bossTrackedTodos[key]
	return todo, ok
}

func (m Model) clearBossTrackedTodo(projectPath string, todoID int64) Model {
	if todoID <= 0 || len(m.bossTrackedTodos) == 0 {
		return m
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	for key, todo := range m.bossTrackedTodos {
		if todo.ID == todoID && filepath.Clean(strings.TrimSpace(todo.ProjectPath)) == projectPath {
			delete(m.bossTrackedTodos, key)
		}
	}
	return m
}

func bossControlExecutionCmd(inv control.Invocation, cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		msg := cmd()
		status, err := bossControlExecutionStatus(inv, msg)
		activity := bossControlOpenedSessionActivity(inv, msg)
		result := bossui.ControlInvocationResultMsg{
			Invocation:     copyControlInvocationForBoss(inv),
			Status:         status,
			Activity:       activity,
			Err:            err,
			AnnounceInChat: true,
		}
		if msg == nil {
			return result
		}
		return tea.BatchMsg{
			func() tea.Msg { return msg },
			func() tea.Msg { return result },
		}
	}
}

func bossControlExecutionStatus(inv control.Invocation, msg tea.Msg) (string, error) {
	if opened, ok := msg.(codexSessionOpenedMsg); ok {
		return bossControlOpenedSessionStatus(inv, opened), opened.err
	}
	if saved, ok := msg.(settingsSavedMsg); ok && inv.Capability == control.CapabilitySettingsUpdate {
		return bossSettingsUpdateSavedStatus(inv, saved), saved.err
	}
	if preview, ok := msg.(commitPreviewMsg); ok && inv.Capability == control.CapabilityGitPrepareCommit {
		return bossGitPrepareCommitStatus(inv, preview), preview.err
	}
	return "Control action completed.", nil
}

func bossSettingsUpdateSavedStatus(inv control.Invocation, saved settingsSavedMsg) string {
	if saved.err != nil {
		return "Settings save failed"
	}
	summary := bossSettingsUpdateSummary(inv)
	if summary == "" {
		summary = "Updated settings."
	}
	return summary
}

func bossGitPrepareCommitStatus(_ control.Invocation, preview commitPreviewMsg) string {
	if preview.err != nil {
		return "Commit preview failed"
	}
	project := strings.TrimSpace(preview.preview.ProjectName)
	if project == "" {
		project = bossControlProjectTargetLabel(preview.projectPath)
	}
	if project == "" {
		project = "the project"
	}
	action := "commit preview"
	if preview.intent == service.GitActionFinish {
		action = "commit & push preview"
	}
	return "Opened the " + action + " for " + project + ". Review it in the normal commit dialog before applying it."
}

func bossSettingsUpdateSummary(inv control.Invocation) string {
	normalized, err := control.ValidateInvocation(inv)
	if err != nil {
		return ""
	}
	var input control.SettingsUpdateInput
	if err := json.Unmarshal(normalized.Args, &input); err != nil {
		return ""
	}
	parts := make([]string, 0, len(input.Changes))
	for _, change := range input.Changes {
		if summary := settingsUpdateChangePastTense(change); summary != "" {
			parts = append(parts, summary)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "Updated settings: " + strings.Join(parts, "; ") + "."
}

func settingsUpdateChangePastTense(change control.SettingsChange) string {
	spec, ok := settingsUpdateFieldSpecs[change.Field]
	label := strings.ReplaceAll(string(change.Field), "_", " ")
	if ok && spec.Label != "" {
		label = spec.Label
	}
	switch change.Operation {
	case control.SettingsUpdateAppendUnique:
		return fmt.Sprintf("added %s to %s", strings.Join(change.Values, ", "), label)
	case control.SettingsUpdateRemove:
		return fmt.Sprintf("removed %s from %s", strings.Join(change.Values, ", "), label)
	case control.SettingsUpdateSet:
		if settingsUpdateFieldUsesBoolValue(change.Field) {
			return fmt.Sprintf("set %s to %t", label, change.BoolValue)
		}
		if len(change.Values) > 0 {
			return fmt.Sprintf("set %s to %s", label, strings.Join(change.Values, ", "))
		}
		return fmt.Sprintf("set %s to %s", label, change.Value)
	default:
		return ""
	}
}

func settingsUpdateFieldUsesBoolValue(field control.SettingsField) bool {
	spec, ok := settingsUpdateFieldSpecs[field]
	return ok && spec.Kind == settingsUpdateBool
}

func bossControlOpenedSessionActivity(inv control.Invocation, msg tea.Msg) *bossui.ViewEngineerActivity {
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok || opened.err != nil {
		return nil
	}
	normalized, err := control.ValidateInvocation(inv)
	if err != nil {
		return nil
	}
	provider := modelSessionSourceFromCodexProvider(embeddedProvider(opened.snapshot))
	sessionID := strings.TrimSpace(opened.snapshot.ThreadID)
	startedAt := bossControlActivityStartedAt(opened.snapshot)
	switch normalized.Capability {
	case control.CapabilityEngineerSendPrompt:
		var input control.EngineerSendPromptInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			return nil
		}
		title := bossControlProjectTargetLabel(input.ProjectName, input.ProjectPath, opened.projectPath)
		if title == "" {
			title = "the selected project"
		}
		title = bossTrackedTodoTargetLabel(title, input.TodoID, input.TodoLabel, input.TodoText)
		return &bossui.ViewEngineerActivity{
			Kind:        "project",
			ProjectPath: strings.TrimSpace(opened.projectPath),
			Title:       title,
			TodoID:      input.TodoID,
			TodoLabel:   strings.TrimSpace(input.TodoLabel),
			TodoText:    strings.TrimSpace(input.TodoText),
			Provider:    provider,
			SessionID:   sessionID,
			Status:      "working",
			Active:      true,
			StartedAt:   startedAt,
			LastEventAt: startedAt,
		}
	case control.CapabilityTodoCreateWorktreeAndStartEngineer:
		var input control.TodoCreateWorktreeAndStartEngineerInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			return nil
		}
		title := bossControlProjectTargetLabel(input.ProjectName, input.ProjectPath)
		if title == "" {
			title = "the selected project"
		}
		title = bossTrackedTodoTargetLabel(title, input.TodoID, input.TodoLabel, input.TodoText)
		return &bossui.ViewEngineerActivity{
			Kind:        "project",
			ProjectPath: strings.TrimSpace(opened.projectPath),
			Title:       title,
			TodoID:      input.TodoID,
			TodoLabel:   strings.TrimSpace(input.TodoLabel),
			TodoText:    strings.TrimSpace(input.TodoText),
			Provider:    provider,
			SessionID:   sessionID,
			Status:      "working",
			Active:      true,
			StartedAt:   startedAt,
			LastEventAt: startedAt,
		}
	case control.CapabilityProjectCreateAndStartEngineer:
		var input control.ProjectCreateAndStartEngineerInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			return nil
		}
		title := bossControlProjectTargetLabel(input.ProjectName, input.ProjectPath)
		if title == "" {
			title = "the new repository"
		}
		title = bossTrackedTodoTargetLabel(title, input.TodoID, input.TodoLabel, input.TodoText)
		return &bossui.ViewEngineerActivity{
			Kind:        "project",
			ProjectPath: strings.TrimSpace(opened.projectPath),
			Title:       title,
			TodoID:      input.TodoID,
			TodoLabel:   strings.TrimSpace(input.TodoLabel),
			TodoText:    strings.TrimSpace(input.TodoText),
			Provider:    provider,
			SessionID:   sessionID,
			Status:      "working",
			Active:      true,
			StartedAt:   startedAt,
			LastEventAt: startedAt,
		}
	case control.CapabilityAgentTaskCreate:
		var input control.AgentTaskCreateInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			return nil
		}
		taskID := strings.TrimSpace(opened.agentTaskID)
		title := firstNonEmptyTrimmed(opened.agentTaskTitle, input.Title, taskID, "agent task")
		return &bossui.ViewEngineerActivity{
			Kind:        "agent_task",
			TaskID:      taskID,
			ProjectPath: strings.TrimSpace(opened.projectPath),
			Title:       title,
			Provider:    provider,
			SessionID:   sessionID,
			Status:      "working",
			Active:      true,
			StartedAt:   startedAt,
			LastEventAt: startedAt,
		}
	case control.CapabilityAgentTaskContinue:
		var input control.AgentTaskContinueInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			return nil
		}
		taskID := firstNonEmptyTrimmed(opened.agentTaskID, input.TaskID)
		title := firstNonEmptyTrimmed(opened.agentTaskTitle, taskID, "agent task")
		return &bossui.ViewEngineerActivity{
			Kind:        "agent_task",
			TaskID:      taskID,
			ProjectPath: strings.TrimSpace(opened.projectPath),
			Title:       title,
			Provider:    provider,
			SessionID:   sessionID,
			Status:      "working",
			Active:      true,
			StartedAt:   startedAt,
			LastEventAt: startedAt,
		}
	default:
		return nil
	}
}

func bossControlActivityStartedAt(snapshot codexapp.Snapshot) time.Time {
	startedAt := bossEngineerActivityStartedAt(snapshot)
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	return startedAt
}

func bossControlOpenedSessionStatus(inv control.Invocation, opened codexSessionOpenedMsg) string {
	fallback := strings.TrimSpace(opened.status)
	if opened.err != nil {
		return fallback
	}
	normalized, err := control.ValidateInvocation(inv)
	if err != nil {
		return fallback
	}
	switch normalized.Capability {
	case control.CapabilityEngineerSendPrompt:
		var input control.EngineerSendPromptInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			return fallback
		}
		return bossEngineerPromptSentStatus(input, opened)
	case control.CapabilityAgentTaskCreate:
		return bossAgentTaskLaunchOpenedStatus("", fallback)
	case control.CapabilityAgentTaskContinue:
		var input control.AgentTaskContinueInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			return bossAgentTaskLaunchOpenedStatus("", fallback)
		}
		return bossAgentTaskLaunchOpenedStatus(input.TaskID, fallback)
	default:
		if fallback != "" {
			return fallback
		}
		return "Control action completed."
	}
}

func bossEngineerPromptSentStatus(input control.EngineerSendPromptInput, opened codexSessionOpenedMsg) string {
	sessionLabel := "engineer session"
	if providerLabel := bossControlOpenedProviderLabel(input.Provider, opened.snapshot); providerLabel != "" {
		sessionLabel = providerLabel + " engineer session"
	}
	target := bossControlProjectTargetLabel(input.ProjectName, input.ProjectPath, opened.projectPath)
	targetPhrase := ""
	if target != "" {
		targetPhrase = " for " + target
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return "Opened the " + sessionLabel + targetPhrase + "."
	}
	if target != "" {
		return "Work on " + bossTrackedTodoTargetLabel(target, input.TodoID, input.TodoLabel, input.TodoText) + " is underway."
	}
	return "The requested work is underway."
}

func bossTrackedTodoTargetLabel(target string, todoID int64, todoLabel, todoText string) string {
	target = strings.TrimSpace(target)
	if todoID <= 0 {
		return target
	}
	label := fmt.Sprintf("#%d", todoID)
	display := strings.Join(strings.Fields(strings.TrimSpace(todoLabel)), " ")
	if display == "" {
		display = strings.Join(strings.Fields(strings.TrimSpace(todoText)), " ")
		display = compactEngineerNoticeText(display, 48)
	}
	if display != "" {
		label += " " + display
	}
	if target == "" {
		return label
	}
	return target + " " + label
}

func bossAgentTaskLaunchOpenedStatus(taskID, fallback string) string {
	status := strings.TrimSpace(fallback)
	if status == "" {
		taskID = strings.TrimSpace(taskID)
		if taskID != "" {
			status = "Work on " + taskID + " is underway"
		} else {
			status = "The requested task is underway"
		}
	}
	return strings.TrimRight(status, ".") + "."
}

func bossAgentTaskHandoffStatus(task model.AgentTask) string {
	label := strings.TrimSpace(task.Title)
	if label == "" {
		label = strings.TrimSpace(task.ID)
	}
	if label == "" {
		label = "the task"
	}
	return "Work on " + label + " is underway"
}

func bossControlOpenedProviderLabel(requested control.Provider, snapshot codexapp.Snapshot) string {
	if provider := embeddedProvider(snapshot).Normalized(); provider != "" {
		return provider.Label()
	}
	if provider := codexProviderFromControlProvider(requested); provider != "" {
		return provider.Label()
	}
	return ""
}

func bossControlProjectTargetLabel(values ...string) string {
	target := firstNonEmptyTrimmed(values...)
	if target == "" {
		return ""
	}
	if strings.Contains(target, "/") || strings.Contains(target, string(filepath.Separator)) {
		base := filepath.Base(target)
		if base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	return target
}

func bossControlResultCmd(inv control.Invocation, status string, err error) tea.Cmd {
	return func() tea.Msg {
		return bossui.ControlInvocationResultMsg{
			Invocation:     copyControlInvocationForBoss(inv),
			Status:         strings.TrimSpace(status),
			Err:            err,
			AnnounceInChat: true,
		}
	}
}

func copyControlInvocationForBoss(inv control.Invocation) control.Invocation {
	out := inv
	if inv.Args != nil {
		out.Args = append([]byte(nil), inv.Args...)
	}
	return out
}

func (m Model) executeEngineerSendPromptControl(input control.EngineerSendPromptInput) (tea.Model, tea.Cmd) {
	outcome := m.executeEngineerSendPromptControlWithOutcome(input)
	return outcome.model, outcome.cmd
}

func (m Model) executeEngineerSendPromptControlWithOutcome(input control.EngineerSendPromptInput) controlInvocationOutcome {
	project, err := m.resolveControlProject(input)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	trackedTodo, err := m.resolveControlTrackedTodo(project, input.TodoID, input.TodoText)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	project, err = m.resolveControlTodoWorkProject(project, trackedTodo)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	provider, err := m.resolveControlEngineerProvider(input.Provider, project)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	if !project.PresentOnDisk {
		err := fmt.Errorf("%s launch requires a folder present on disk", provider.Label())
		m.status = err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	if block, blocked := m.embeddedLaunchBlock(project, provider, input.SessionMode == control.SessionModeNew); blocked {
		err := errors.New(block.Message)
		m.status = block.Message
		return controlInvocationOutcome{model: m, err: err}
	}
	if input.SessionMode == control.SessionModeNew {
		if message, blocked := m.controlFreshSessionBlockedByActiveEngineerTurn(project, provider, "project"); blocked {
			err := errors.New(message)
			m.status = message
			return controlInvocationOutcome{model: m, err: err}
		}
	}
	if controlPromptTargetsNonSteerableActiveEmbeddedSession(input, m, project.Path, provider) {
		err := fmt.Errorf("The embedded %s engineer session is already running, so I did not send the prompt into it. Start a fresh session or open the target session and send manually.", provider.Label())
		m.status = err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}

	input.TodoText = strings.TrimSpace(firstNonEmptyTrimmed(input.TodoText, trackedTodo.Text))
	input.TodoLabel = strings.TrimSpace(firstNonEmptyTrimmed(input.TodoLabel, todoDisplayLabelFromItem(trackedTodo)))
	prompt := m.engineerPromptWithRuntimeContext(project, input.Prompt, trackedTodo)
	if controlPromptWillSteerActiveEmbeddedSession(input, m, project.Path, provider) {
		prompt = m.promptWithRuntimeContext(engineerPromptWithTrackedTodo(input.Prompt, trackedTodo), m.projectRuntimeContextLines(project))
	}
	updated, cmd := m.launchEmbeddedForProjectWithOptions(project, provider, embeddedLaunchOptions{
		forceNew: input.SessionMode == control.SessionModeNew,
		prompt:   prompt,
		reveal:   input.Reveal,
	})
	m = normalizeUpdateModel(updated)
	if cmd == nil {
		status := strings.TrimSpace(m.status)
		if status == "" {
			status = "engineer session launch did not start"
		}
		err := errors.New(status)
		return controlInvocationOutcome{model: m, err: err}
	}
	cmd = m.todoEngineerLaunchTrackingCmd(project.Path, trackedTodo.ID, cmd)
	return controlInvocationOutcome{model: m, cmd: cmd, inv: engineerSendPromptInvocationFromInput(input)}
}

func (m Model) todoEngineerLaunchTrackingCmd(projectPath string, todoID int64, cmd tea.Cmd) tea.Cmd {
	if cmd == nil || m.svc == nil || todoID <= 0 {
		return cmd
	}
	projectPath = strings.TrimSpace(projectPath)
	return func() tea.Msg {
		msg := cmd()
		opened, ok := msg.(codexSessionOpenedMsg)
		if !ok || opened.err != nil {
			return msg
		}
		snapshot := opened.snapshot
		sessionID := strings.TrimSpace(snapshot.ThreadID)
		if sessionID == "" {
			return opened
		}
		path := strings.TrimSpace(opened.projectPath)
		if path == "" {
			path = strings.TrimSpace(snapshot.ProjectPath)
		}
		if path == "" {
			path = projectPath
		}
		if path == "" {
			return opened
		}
		at := embeddedSnapshotActivityAt(snapshot)
		if at.IsZero() {
			at = time.Now()
		}
		provider := modelSessionSourceFromCodexProvider(embeddedProvider(snapshot))
		if err := m.svc.MarkTodoWorkStarted(m.ctx, path, todoID, provider, sessionID, at); err != nil {
			if opened.status != "" {
				opened.status += "; TODO tracking update failed"
			} else {
				opened.status = "TODO tracking update failed"
			}
		}
		return opened
	}
}

func controlPromptTargetsNonSteerableActiveEmbeddedSession(input control.EngineerSendPromptInput, m Model, projectPath string, provider codexapp.Provider) bool {
	if input.SessionMode == control.SessionModeNew || strings.TrimSpace(input.Prompt) == "" {
		return false
	}
	snapshot, ok := m.liveEmbeddedSnapshotForProject(projectPath, provider)
	if !ok {
		return false
	}
	if !embeddedSessionBlocksProviderSwitch(snapshot) {
		return false
	}
	return !controlPromptCanSteerActiveEmbeddedSession(snapshot)
}

func controlPromptWillSteerActiveEmbeddedSession(input control.EngineerSendPromptInput, m Model, projectPath string, provider codexapp.Provider) bool {
	if input.SessionMode == control.SessionModeNew || strings.TrimSpace(input.Prompt) == "" {
		return false
	}
	snapshot, ok := m.liveEmbeddedSnapshotForProject(projectPath, provider)
	if !ok || !embeddedSessionBlocksProviderSwitch(snapshot) {
		return false
	}
	return controlPromptCanSteerActiveEmbeddedSession(snapshot)
}

func controlPromptCanSteerActiveEmbeddedSession(snapshot codexapp.Snapshot) bool {
	return embeddedProvider(snapshot) == codexapp.ProviderCodex && codexSnapshotCanSteer(snapshot)
}

func codexProviderFromControlProvider(provider control.Provider) codexapp.Provider {
	switch provider.Normalized() {
	case control.ProviderCodex:
		return codexapp.ProviderCodex
	case control.ProviderOpenCode:
		return codexapp.ProviderOpenCode
	case control.ProviderClaudeCode:
		return codexapp.ProviderClaudeCode
	case control.ProviderLCAgent:
		return codexapp.ProviderLCAgent
	default:
		return ""
	}
}

func controlProviderFromCodexProvider(provider codexapp.Provider) control.Provider {
	switch provider.Normalized() {
	case codexapp.ProviderOpenCode:
		return control.ProviderOpenCode
	case codexapp.ProviderClaudeCode:
		return control.ProviderClaudeCode
	case codexapp.ProviderLCAgent:
		return control.ProviderLCAgent
	default:
		return control.ProviderCodex
	}
}

func (m Model) executeAgentTaskCreateControlWithOutcome(input control.AgentTaskCreateInput) controlInvocationOutcome {
	if m.svc == nil {
		err := errors.New("service unavailable")
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	var provider codexapp.Provider
	if strings.TrimSpace(input.Prompt) != "" {
		var err error
		provider, err = m.resolveAgentTaskControlProvider(input.Provider, model.AgentTask{})
		if err != nil {
			m.status = "Control request failed: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
	}
	kind := modelAgentTaskKindFromControl(input.Kind)
	task, err := m.svc.CreateAgentTask(m.ctx, model.CreateAgentTaskInput{
		ParentTaskID: strings.TrimSpace(input.ParentTaskID),
		Title:        input.Title,
		Kind:         kind,
		Capabilities: append([]string(nil), input.Capabilities...),
		Resources:    agentTaskResourcesFromControl(input.Resources),
	})
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	m.upsertOpenAgentTask(task)
	if strings.TrimSpace(input.Prompt) == "" {
		m.status = "Created agent task " + task.ID
		return controlInvocationOutcome{model: m}
	}
	project, err := projectSummaryForAgentTask(task)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	prompt := m.agentTaskLaunchPromptWithRuntimeContext(task, input.Prompt)
	updated, cmd := m.launchEmbeddedForProjectWithOptions(project, provider, embeddedLaunchOptions{
		forceNew: true,
		prompt:   prompt,
		reveal:   input.Reveal,
	})
	m = normalizeUpdateModel(updated)
	if cmd == nil {
		status := strings.TrimSpace(m.status)
		if status == "" {
			status = "agent task engineer session launch did not start"
		}
		err := errors.New(status)
		return controlInvocationOutcome{model: m, err: err}
	}
	return controlInvocationOutcome{model: m, cmd: m.agentTaskLaunchTrackingCmd(task, cmd, bossAgentTaskHandoffStatus(task))}
}

func (m Model) executeAgentTaskContinueControlWithOutcome(input control.AgentTaskContinueInput) controlInvocationOutcome {
	if m.svc == nil {
		err := errors.New("service unavailable")
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	task, err := m.svc.GetAgentTask(m.ctx, input.TaskID)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	provider, err := m.resolveAgentTaskControlProvider(input.Provider, task)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	project, err := projectSummaryForAgentTask(task)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	if controlPromptTargetsNonSteerableActiveEmbeddedSession(control.EngineerSendPromptInput{
		SessionMode: input.SessionMode,
		Prompt:      input.Prompt,
	}, m, project.Path, provider) {
		err := fmt.Errorf("The embedded %s engineer session for agent task %s is already running, so I did not send the prompt into it. Wait for it to finish or start a fresh session.", provider.Label(), task.ID)
		m.status = err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	if input.SessionMode == control.SessionModeNew {
		label := strings.TrimSpace(task.ID)
		if label != "" {
			label = "agent task " + label
		} else {
			label = "agent task"
		}
		if message, blocked := m.controlFreshSessionBlockedByActiveEngineerTurn(project, provider, label); blocked {
			err := errors.New(message)
			m.status = message
			return controlInvocationOutcome{model: m, err: err}
		}
	}
	resumeID := taskSessionIDForProvider(task, provider)
	prompt := m.agentTaskLaunchPromptWithRuntimeContext(task, input.Prompt, agentTaskPromptOptions{
		ResumePausedGoal: input.SessionMode != control.SessionModeNew && resumeID != "",
	})
	updated, cmd := m.launchEmbeddedForProjectWithOptions(project, provider, embeddedLaunchOptions{
		forceNew: input.SessionMode == control.SessionModeNew,
		prompt:   prompt,
		reveal:   input.Reveal,
		resumeID: resumeID,
	})
	m = normalizeUpdateModel(updated)
	if cmd == nil {
		status := strings.TrimSpace(m.status)
		if status == "" {
			status = "agent task engineer session launch did not start"
		}
		err := errors.New(status)
		return controlInvocationOutcome{model: m, err: err}
	}
	return controlInvocationOutcome{model: m, cmd: m.agentTaskLaunchTrackingCmd(task, cmd, bossAgentTaskHandoffStatus(task))}
}

func (m Model) executeAgentTaskCloseControlWithOutcome(input control.AgentTaskCloseInput) controlInvocationOutcome {
	if m.svc == nil {
		err := errors.New("service unavailable")
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	task, err := m.svc.GetAgentTask(m.ctx, input.TaskID)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	if input.CloseSession && strings.TrimSpace(task.WorkspacePath) != "" {
		if snapshot, ok := m.liveAgentTaskSnapshot(task); ok && embeddedSessionBlocksProviderSwitch(snapshot) {
			err := fmt.Errorf("agent task %s still has an active embedded %s session; wait for it to finish before closing the session", task.ID, embeddedProvider(snapshot).Label())
			m.status = "Control request failed: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		if m.codexManager != nil {
			_ = m.codexManager.CloseProject(task.WorkspacePath)
		}
	}
	switch input.Status {
	case control.AgentTaskCloseArchived:
		task, err = m.svc.ArchiveAgentTask(m.ctx, input.TaskID)
	case control.AgentTaskCloseWaiting:
		status := model.AgentTaskStatusWaiting
		summary := strings.TrimSpace(input.Summary)
		task, err = m.svc.Store().UpdateAgentTask(m.ctx, model.UpdateAgentTaskInput{
			ID:      input.TaskID,
			Status:  &status,
			Summary: &summary,
			Touch:   true,
		})
	default:
		task, err = m.svc.CompleteAgentTask(m.ctx, input.TaskID, input.Summary)
	}
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	m.upsertOpenAgentTask(task)
	m.status = fmt.Sprintf("Agent task %s is now %s", task.ID, task.Status)
	return controlInvocationOutcome{model: m}
}

func (m Model) executeScratchTaskArchiveControlWithOutcome(input control.ScratchTaskArchiveInput) controlInvocationOutcome {
	if m.svc == nil {
		err := errors.New("service unavailable")
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	project, err := m.resolveControlProjectRef(input.ProjectPath, input.ProjectName)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	if model.NormalizeProjectKind(project.Kind) != model.ProjectKindScratchTask {
		err := fmt.Errorf("project is not a scratch task: %s", project.Path)
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	archivedPath, err := m.svc.ArchiveScratchTask(m.ctx, project.Path)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	m.removeProjectSummary(project.Path)
	name := projectRemovalName(project)
	m.status = fmt.Sprintf("Archived scratch task %q", name)
	if strings.TrimSpace(archivedPath) != "" {
		m.status += " to " + archivedPath
	}
	return controlInvocationOutcome{model: m}
}

func (m Model) executeProjectArchiveControlWithOutcome(input control.ProjectArchiveInput) controlInvocationOutcome {
	if m.svc == nil {
		err := errors.New("service unavailable")
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	targets, err := m.resolveProjectArchiveTargets(input)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	if len(targets) != 1 || len(input.Resources) > 0 {
		return m.executeProjectArchiveBatchControlWithOutcome(input, targets)
	}
	project := targets[0]
	switch model.NormalizeProjectKind(project.Kind) {
	case model.ProjectKindAgentTask:
		err := fmt.Errorf("agent tasks use agent_task.close with archived status: %s", project.Path)
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	case model.ProjectKindScratchTask:
		err := fmt.Errorf("scratch tasks use scratch_task.archive: %s", project.Path)
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}

	archive := input.Action == control.ProjectArchiveActionArchive
	name := projectRemovalName(project)
	if archive && project.Archived {
		m.status = fmt.Sprintf("%q is already archived", name)
		return controlInvocationOutcome{model: m}
	}
	if !archive && !project.Archived {
		if !project.InScope {
			m.status = fmt.Sprintf("%q is outside project scope", name)
		} else {
			m.status = fmt.Sprintf("%q is not archived", name)
		}
		return controlInvocationOutcome{model: m}
	}

	if archive {
		err = m.svc.ArchiveProject(m.ctx, project.Path)
	} else {
		err = m.svc.UnarchiveProject(m.ctx, project.Path)
	}
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}

	m.applyProjectArchiveStateLocally([]model.ProjectSummary{project}, archive)
	selectPath := project.Path
	if (archive && m.archiveMode != projectArchiveArchived) || (!archive && m.archiveMode == projectArchiveArchived) {
		selectPath = ""
	}
	m.rebuildProjectList(selectPath)
	m.syncDetailViewport(false)

	if archive {
		m.status = fmt.Sprintf("Archived %q", name)
	} else if project.InScope {
		m.status = fmt.Sprintf("Unarchived %q", name)
	} else {
		m.status = fmt.Sprintf("Unarchived %q; still outside project scope", name)
	}
	return controlInvocationOutcome{model: m}
}

func (m Model) executeProjectSetCategoryControlWithOutcome(input control.ProjectSetCategoryInput) controlInvocationOutcome {
	if m.svc == nil || m.svc.Store() == nil {
		err := errors.New("service unavailable")
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	if strings.TrimSpace(input.ProjectPath) == "" {
		project, err := m.resolveControlProjectRef("", input.ProjectName)
		if err != nil {
			m.status = "Control request failed: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		input.ProjectPath = strings.TrimSpace(project.Path)
		input.ProjectName = firstNonEmptyTrimmed(input.ProjectName, projectRemovalName(project))
	} else if project, ok := m.projectSummaryByPathAllProjects(input.ProjectPath); ok {
		input.ProjectName = firstNonEmptyTrimmed(input.ProjectName, projectRemovalName(project))
	}
	input, err := control.NormalizeProjectSetCategoryInput(input)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	inv := projectSetCategoryInvocationFromInput(input)
	m.status = fmt.Sprintf("Placing %s in %s...", bossControlProjectTargetLabel(input.ProjectName, input.ProjectPath), input.CategoryName)
	return controlInvocationOutcome{
		model: m,
		inv:   inv,
		cmd:   m.setBossProjectCategoryCmd(inv, input),
	}
}

func (m Model) setBossProjectCategoryCmd(inv control.Invocation, input control.ProjectSetCategoryInput) tea.Cmd {
	svc := m.svc
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		msg := bossProjectCategorySetMsg{inv: inv, input: input}
		if svc == nil || svc.Store() == nil {
			msg.err = errors.New("service unavailable")
			return msg
		}
		ctx, cancel := context.WithTimeout(parent, tuiProjectActionTimeout)
		defer cancel()
		category, err := svc.Store().GetProjectCategoryByName(ctx, input.CategoryName)
		if err != nil {
			msg.err = fmt.Errorf("load destination project category: %w", err)
			return msg
		}
		msg.category = category
		msg.result, msg.err = svc.CreateOrAttachProject(ctx, service.CreateOrAttachProjectRequest{
			ParentPath:       filepath.Dir(input.ProjectPath),
			Name:             filepath.Base(input.ProjectPath),
			RequireExisting:  true,
			CategoryID:       category.ID,
			CategoryExplicit: true,
		})
		msg.err = timeoutActionError(msg.err, tuiProjectActionTimeout, "registering and categorizing the project")
		if msg.err != nil {
			return msg
		}
		msg.project, msg.err = svc.Store().GetProjectSummary(ctx, msg.result.ProjectPath, true)
		if msg.err != nil {
			msg.err = fmt.Errorf("load categorized project: %w", msg.err)
		}
		return msg
	}
}

func (m Model) applyBossProjectCategorySet(msg bossProjectCategorySetMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		status := "Could not place the project in the requested category: " + msg.err.Error()
		m.status = status
		return m, bossControlResultCmd(msg.inv, status, errors.New(status))
	}
	msg.input.ProjectPath = strings.TrimSpace(msg.result.ProjectPath)
	msg.input.ProjectName = firstNonEmptyTrimmed(msg.input.ProjectName, msg.result.ProjectName, projectRemovalName(msg.project))
	msg.input.CategoryName = strings.TrimSpace(msg.category.Name)
	msg.inv = projectSetCategoryInvocationFromInput(msg.input)
	m.upsertProjectSummary(msg.project)
	m.rebuildProjectList(msg.project.Path)
	m.syncDetailViewport(false)
	name := projectRemovalName(msg.project)
	if name == "" {
		name = filepath.Base(msg.project.Path)
	}
	status := fmt.Sprintf("Placed %q in the %s category. No project work was created.", name, msg.category.Name)
	if msg.result.Action == service.CreateOrAttachProjectAdded {
		status = fmt.Sprintf("Added %q to Little Control Room in the %s category. No project work was created.", name, msg.category.Name)
	}
	m.status = status
	return m, batchCmds(
		bossControlResultCmd(msg.inv, status, nil),
		m.requestProjectInvalidationCmd(invalidateProjectStructure(msg.project.Path)),
	)
}

func projectSetCategoryInvocationFromInput(input control.ProjectSetCategoryInput) control.Invocation {
	normalized, err := control.NormalizeProjectSetCategoryInput(input)
	if err != nil {
		normalized = input
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return control.Invocation{}
	}
	return control.Invocation{Capability: control.CapabilityProjectSetCategory, RequestID: strings.TrimSpace(normalized.RequestID), Args: payload}
}

func (m Model) executeProjectArchiveBatchControlWithOutcome(input control.ProjectArchiveInput, targets []model.ProjectSummary) controlInvocationOutcome {
	archive := input.Action == control.ProjectArchiveActionArchive
	for _, project := range targets {
		switch model.NormalizeProjectKind(project.Kind) {
		case model.ProjectKindAgentTask:
			err := fmt.Errorf("agent tasks use agent_task.close with archived status: %s", project.Path)
			m.status = "Control request failed: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		case model.ProjectKindScratchTask:
			err := fmt.Errorf("scratch tasks use scratch_task.archive: %s", project.Path)
			m.status = "Control request failed: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
	}

	changed := 0
	already := 0
	paths := make([]string, 0, len(targets))
	for _, project := range targets {
		if project.Archived == archive {
			already++
			continue
		}
		paths = append(paths, project.Path)
	}
	if len(paths) > 0 {
		var err error
		if archive {
			err = m.svc.ArchiveProjects(m.ctx, paths)
		} else {
			err = m.svc.UnarchiveProjects(m.ctx, paths)
		}
		if err != nil {
			m.status = "Control request failed: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
	}
	for _, project := range targets {
		if project.Archived == archive {
			continue
		}
		changed++
	}
	m.applyProjectArchiveStateLocally(targets, archive)

	m.rebuildProjectList("")
	m.syncDetailViewport(false)

	verb := "Archived"
	if !archive {
		verb = "Unarchived"
	}
	switch {
	case changed > 0 && already > 0:
		m.status = fmt.Sprintf("%s %d projects; %d already matched that state", verb, changed, already)
	case changed > 0:
		m.status = fmt.Sprintf("%s %d projects", verb, changed)
	default:
		state := "archived"
		if !archive {
			state = "not archived"
		}
		m.status = fmt.Sprintf("All %d projects were already %s", len(targets), state)
	}
	return controlInvocationOutcome{model: m}
}

func (m Model) resolveProjectArchiveTargets(input control.ProjectArchiveInput) ([]model.ProjectSummary, error) {
	if len(input.Resources) == 0 {
		project, err := m.resolveControlProjectRef(input.ProjectPath, input.ProjectName)
		if err != nil {
			return nil, err
		}
		return []model.ProjectSummary{project}, nil
	}

	targets := make([]model.ProjectSummary, 0, len(input.Resources))
	seen := map[string]struct{}{}
	for _, resource := range input.Resources {
		path := firstNonEmptyTrimmed(resource.ProjectPath, resource.Path)
		name := strings.TrimSpace(resource.Label)
		project, err := m.resolveControlProjectRef(path, name)
		if err != nil {
			return nil, err
		}
		key := normalizeProjectPath(project.Path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, project)
	}
	if len(targets) == 0 {
		return nil, errors.New("project archive batch has no resolvable project resources")
	}
	return targets, nil
}

func (m Model) executeTodoAddControlWithOutcome(input control.TodoAddInput) controlInvocationOutcome {
	if m.svc == nil {
		err := errors.New("service unavailable")
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	project, err := m.resolveControlProjectRef(input.ProjectPath, input.ProjectName)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	input.ProjectPath = strings.TrimSpace(project.Path)
	input.ProjectName = firstNonEmptyTrimmed(input.ProjectName, projectNameForPicker(project, project.Path))
	inv := todoAddInvocationFromInput(input)
	m.status = "Adding project TODO..."
	return controlInvocationOutcome{model: m, inv: inv, cmd: m.createBossTodoAddCmd(inv, input, project)}
}

func (m Model) createBossTodoAddCmd(inv control.Invocation, input control.TodoAddInput, project model.ProjectSummary) tea.Cmd {
	svc := m.svc
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		if svc == nil {
			return bossTodoAddedMsg{inv: inv, input: input, project: project, err: errors.New("service unavailable")}
		}
		item, err := svc.AddTodo(ctx, project.Path, input.Text)
		return bossTodoAddedMsg{inv: inv, input: input, project: project, todo: item, err: err}
	}
}

func (m Model) applyBossTodoAdded(msg bossTodoAddedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		status := "Could not add the project TODO: " + msg.err.Error()
		m.status = status
		return m, bossControlResultCmd(msg.inv, status, errors.New(status))
	}
	status := fmt.Sprintf("Added TODO #%d to %s. It was not started.", msg.todo.ID, projectNameForPicker(msg.project, msg.project.Path))
	m.status = status
	return m, batchCmds(
		bossControlResultCmd(msg.inv, status, nil),
		m.requestProjectInvalidationCmd(invalidateProjectData(msg.project.Path)),
	)
}

func todoAddInvocationFromInput(input control.TodoAddInput) control.Invocation {
	normalized, err := control.NormalizeTodoAddInput(input)
	if err != nil {
		normalized = input
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return control.Invocation{}
	}
	return control.Invocation{Capability: control.CapabilityTodoAdd, RequestID: strings.TrimSpace(normalized.RequestID), Args: payload}
}

func (m Model) executeProjectCreateAndStartEngineerControlWithOutcome(input control.ProjectCreateAndStartEngineerInput) controlInvocationOutcome {
	if _, exists := m.projectSummaryByPathAllProjects(input.ProjectPath); exists {
		err := fmt.Errorf("project is already loaded: %s", input.ProjectPath)
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	if m.svc == nil || m.svc.Store() == nil {
		err := errors.New("service unavailable")
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	provider, err := m.resolveControlEngineerProviderForNewProject(input.Provider)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	input.Provider = controlProviderFromCodexProvider(provider)
	inv := projectCreateAndStartEngineerInvocationFromInput(input)
	m.status = "Setting up Git repository " + input.ProjectName + "..."
	return controlInvocationOutcome{
		model: m,
		inv:   inv,
		cmd:   m.createBossProjectAndStartEngineerCmd(inv, input, provider),
	}
}

func (m Model) resolveControlEngineerProviderForNewProject(requested control.Provider) (codexapp.Provider, error) {
	if requested.Normalized() == control.ProviderAuto {
		provider, _ := m.defaultEmbeddedProviderForNewItem()
		if provider.Normalized() == codexapp.ProviderClaudeCode {
			return "", errors.New("Claude Code is present in the protocol but disabled for control execution")
		}
		return provider, nil
	}
	return m.resolveControlEngineerProvider(requested, model.ProjectSummary{})
}

func (m Model) createBossProjectAndStartEngineerCmd(inv control.Invocation, input control.ProjectCreateAndStartEngineerInput, provider codexapp.Provider) tea.Cmd {
	svc := m.svc
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		msg := bossProjectCreateAndStartEngineerCreatedMsg{inv: inv, input: input, provider: provider}
		if svc == nil || svc.Store() == nil {
			msg.err = errors.New("service unavailable")
			return msg
		}
		ctx, cancel := context.WithTimeout(parent, tuiProjectActionTimeout)
		defer cancel()
		parentInfo, err := os.Stat(input.ParentPath)
		switch {
		case err != nil:
			msg.err = fmt.Errorf("check repository parent path: %w", err)
			return msg
		case !parentInfo.IsDir():
			msg.err = fmt.Errorf("repository parent path is not a directory: %s", input.ParentPath)
			return msg
		}
		targetInfo, targetErr := os.Stat(input.ProjectPath)
		targetExists := targetErr == nil
		switch {
		case targetErr != nil && !errors.Is(targetErr, os.ErrNotExist):
			msg.err = fmt.Errorf("check target repository path: %w", targetErr)
			return msg
		case targetExists && !targetInfo.IsDir():
			msg.err = fmt.Errorf("target repository path exists and is not a directory: %s", input.ProjectPath)
			return msg
		case targetExists:
			msg.folderExists = true
			if _, gitErr := os.Stat(filepath.Join(input.ProjectPath, ".git")); gitErr != nil {
				if errors.Is(gitErr, os.ErrNotExist) {
					msg.err = fmt.Errorf("target folder exists but is not a Git repository: %s", input.ProjectPath)
				} else {
					msg.err = fmt.Errorf("check existing Git repository: %w", gitErr)
				}
				return msg
			}
			msg.gitInitialized = true
		}
		msg.result, msg.err = svc.CreateOrAttachProject(ctx, service.CreateOrAttachProjectRequest{
			ParentPath:             input.ParentPath,
			Name:                   input.ProjectName,
			CreateGitRepo:          true,
			RequireNew:             !targetExists,
			PreferredSessionSource: modelSessionSourceFromCodexProvider(provider),
		})
		msg.err = timeoutActionError(msg.err, tuiProjectActionTimeout, "setting up and registering the repository")
		if info, statErr := os.Stat(input.ProjectPath); statErr == nil && info.IsDir() {
			msg.folderExists = true
		}
		if _, statErr := os.Stat(filepath.Join(input.ProjectPath, ".git")); statErr == nil {
			msg.gitInitialized = true
		}
		observeCtx, observeCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer observeCancel()
		if summary, summaryErr := svc.Store().GetProjectSummary(observeCtx, input.ProjectPath, true); summaryErr == nil {
			msg.project = summary
			msg.projectTracked = true
		} else if msg.err == nil {
			msg.err = fmt.Errorf("load registered project: %w", summaryErr)
		}
		if msg.err == nil && !msg.folderExists {
			msg.err = fmt.Errorf("repository setup returned without a target directory")
		}
		if msg.err == nil && !msg.gitInitialized {
			msg.err = fmt.Errorf("repository setup returned without a Git repository")
		}
		return msg
	}
}

func (m Model) applyBossProjectCreateAndStartEngineerCreated(msg bossProjectCreateAndStartEngineerCreatedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		status := bossProjectCreateAndStartEngineerFailureStatus(msg)
		m.status = status
		return m, batchCmds(
			bossControlResultCmd(msg.inv, status, errors.New(status)),
			m.requestProjectInvalidationCmd(invalidateProjectStructure("")),
		)
	}
	msg.input.ProjectPath = strings.TrimSpace(msg.result.ProjectPath)
	msg.input.Provider = controlProviderFromCodexProvider(msg.provider)
	msg.inv = projectCreateAndStartEngineerInvocationFromInput(msg.input)
	m.upsertProjectSummary(msg.project)
	status := projectSetupReceipt(msg.result.Action, msg.input.ProjectName) + ". Creating its tracked TODO..."
	m.status = status
	var noticeCmd tea.Cmd
	m, noticeCmd = m.updateBossHostChatNotice(status)
	trackedInput := todoCreateWorktreeInputFromProjectCreate(inputWithCreatedProjectDefaults(msg.input, msg.result))
	return m, batchCmds(
		noticeCmd,
		m.createBossTodoWorktreeTodoCmd(msg.inv, trackedInput, msg.project, msg.provider, msg.result.Action),
		m.requestProjectInvalidationCmd(invalidateProjectStructure(msg.result.ProjectPath)),
	)
}

func inputWithCreatedProjectDefaults(input control.ProjectCreateAndStartEngineerInput, result service.CreateOrAttachProjectResult) control.ProjectCreateAndStartEngineerInput {
	input.ProjectPath = firstNonEmptyTrimmed(result.ProjectPath, input.ProjectPath)
	input.ProjectName = firstNonEmptyTrimmed(result.ProjectName, input.ProjectName)
	return input
}

func todoCreateWorktreeInputFromProjectCreate(input control.ProjectCreateAndStartEngineerInput) control.TodoCreateWorktreeAndStartEngineerInput {
	return control.TodoCreateWorktreeAndStartEngineerInput{
		RequestID:    input.RequestID,
		ProjectPath:  input.ProjectPath,
		ProjectName:  input.ProjectName,
		TodoText:     input.TodoText,
		Prompt:       input.Prompt,
		Provider:     input.Provider,
		Reveal:       input.Reveal,
		TodoID:       input.TodoID,
		TodoLabel:    input.TodoLabel,
		WorktreePath: input.WorktreePath,
	}
}

func bossProjectCreateAndStartEngineerFailureStatus(msg bossProjectCreateAndStartEngineerCreatedMsg) string {
	status := fmt.Sprintf("Could not set up and register Git repository %s: %v.", msg.input.ProjectPath, msg.err)
	switch {
	case msg.projectTracked:
		return status + " The repository is registered in Little Control Room, but no TODO, worktree, or engineer was started."
	case msg.gitInitialized:
		return status + " The target folder and Git repository exist, but no TODO, worktree, or engineer was started."
	case msg.folderExists:
		return status + " The target folder exists, but it is not a usable Git repository; no TODO, worktree, or engineer was started."
	default:
		return status + " No folder, TODO, worktree, or engineer was created."
	}
}

func projectSetupReceipt(action service.CreateOrAttachProjectAction, target string) string {
	target = strings.TrimSpace(target)
	switch action {
	case service.CreateOrAttachProjectAdded:
		return "Registered existing Git repository " + target
	case service.CreateOrAttachProjectCreated:
		return "Created Git repository " + target
	default:
		return "Set up Git repository " + target
	}
}

func (m Model) executeTodoCreateWorktreeAndStartEngineerControlWithOutcome(input control.TodoCreateWorktreeAndStartEngineerInput) controlInvocationOutcome {
	if m.svc == nil {
		err := errors.New("service unavailable")
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	project, err := m.resolveControlProjectRef(input.ProjectPath, input.ProjectName)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	provider, err := m.resolveControlEngineerProvider(input.Provider, project)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	if !project.PresentOnDisk {
		err := fmt.Errorf("%s launch requires a folder present on disk", provider.Label())
		m.status = err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	input.ProjectPath = strings.TrimSpace(project.Path)
	input.ProjectName = firstNonEmptyTrimmed(input.ProjectName, projectNameForPicker(project, project.Path))
	input.Provider = controlProviderFromCodexProvider(provider)
	inv := todoCreateWorktreeAndStartEngineerInvocationFromInput(input)
	m.status = "Creating tracked TODO for " + bossControlProjectTargetLabel(input.ProjectName, input.ProjectPath) + "..."
	return controlInvocationOutcome{
		model: m,
		inv:   inv,
		cmd:   m.createBossTodoWorktreeTodoCmd(inv, input, project, provider, ""),
	}
}

func (m Model) createBossTodoWorktreeTodoCmd(inv control.Invocation, input control.TodoCreateWorktreeAndStartEngineerInput, project model.ProjectSummary, provider codexapp.Provider, projectSetupAction service.CreateOrAttachProjectAction) tea.Cmd {
	svc := m.svc
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		if svc == nil {
			return bossTodoWorktreeTodoCreatedMsg{inv: inv, input: input, project: project, provider: provider, projectSetupAction: projectSetupAction, err: errors.New("service unavailable")}
		}
		item, err := svc.AddTodo(ctx, project.Path, input.TodoText)
		return bossTodoWorktreeTodoCreatedMsg{inv: inv, input: input, project: project, provider: provider, projectSetupAction: projectSetupAction, todo: item, err: err}
	}
}

func (m Model) applyBossTodoWorktreeTodoCreated(msg bossTodoWorktreeTodoCreatedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		status := fmt.Sprintf("Could not create the tracked TODO for %s, so no worktree or engineer was started: %v", bossControlProjectTargetLabel(msg.input.ProjectName, msg.input.ProjectPath), msg.err)
		if msg.projectSetupAction != "" {
			status = fmt.Sprintf("%s, but could not create its tracked TODO. No worktree or engineer was started: %v", projectSetupReceipt(msg.projectSetupAction, msg.input.ProjectPath), msg.err)
		}
		return m, bossControlResultCmd(msg.inv, status, errors.New(status))
	}
	msg.input.TodoID = msg.todo.ID
	msg.input.TodoLabel = todoDisplayLabelFromItem(msg.todo)
	msg.inv = trackedWorkInvocationFromInput(msg.inv, msg.input)
	status := fmt.Sprintf("Starting TODO #%d for %s in a dedicated worktree...", msg.todo.ID, bossControlProjectTargetLabel(msg.input.ProjectName, msg.input.ProjectPath))
	m.status = status
	var noticeCmd tea.Cmd
	m, noticeCmd = m.updateBossHostChatNotice(status)
	return m, batchCmds(
		noticeCmd,
		m.createBossTodoWorktreeCmd(msg.inv, msg.input, msg.project, msg.provider, msg.projectSetupAction, msg.todo),
		m.requestProjectInvalidationCmd(invalidateProjectData(msg.project.Path)),
	)
}

func (m Model) createBossTodoWorktreeCmd(inv control.Invocation, input control.TodoCreateWorktreeAndStartEngineerInput, project model.ProjectSummary, provider codexapp.Provider, projectSetupAction service.CreateOrAttachProjectAction, todo model.TodoItem) tea.Cmd {
	svc := m.svc
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		if svc == nil {
			return bossTodoWorktreePreparedMsg{inv: inv, input: input, project: project, provider: provider, projectSetupAction: projectSetupAction, todo: todo, err: errors.New("service unavailable")}
		}
		result, err := svc.CreateTodoWorktree(ctx, service.CreateTodoWorktreeRequest{ProjectPath: project.Path, TodoID: todo.ID})
		return bossTodoWorktreePreparedMsg{inv: inv, input: input, project: project, provider: provider, projectSetupAction: projectSetupAction, todo: todo, result: result, err: err}
	}
}

func (m Model) applyBossTodoWorktreePrepared(msg bossTodoWorktreePreparedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		status := fmt.Sprintf("Added TODO #%d to %s, but could not prepare its dedicated worktree. No engineer was launched: %v", msg.todo.ID, bossControlProjectTargetLabel(msg.input.ProjectName, msg.input.ProjectPath), msg.err)
		if msg.projectSetupAction != "" {
			status = fmt.Sprintf("%s and added TODO #%d, but could not prepare its dedicated worktree. No engineer was launched: %v", projectSetupReceipt(msg.projectSetupAction, msg.input.ProjectPath), msg.todo.ID, msg.err)
		}
		m.status = status
		return m, bossControlResultCmd(msg.inv, status, errors.New(status))
	}
	msg.input.TodoID = msg.todo.ID
	msg.input.TodoLabel = firstNonEmptyTrimmed(msg.input.TodoLabel, todoDisplayLabelFromItem(msg.todo))
	msg.input.WorktreePath = strings.TrimSpace(msg.result.WorktreePath)
	msg.inv = trackedWorkInvocationFromInput(msg.inv, msg.input)
	worktreeProject := model.ProjectSummary{
		Path:                 msg.result.WorktreePath,
		Name:                 filepath.Base(msg.result.WorktreePath),
		PresentOnDisk:        true,
		InScope:              true,
		WorktreeRootPath:     msg.result.RootProjectPath,
		WorktreeKind:         model.WorktreeKindLinked,
		WorktreeParentBranch: msg.result.ParentBranch,
		WorktreeOriginTodoID: msg.todo.ID,
	}
	prompt := m.engineerPromptWithRuntimeContext(msg.project, msg.input.Prompt, msg.todo)
	updated, cmd := m.launchEmbeddedForProjectWithOptions(worktreeProject, msg.provider, embeddedLaunchOptions{
		forceNew: true,
		prompt:   prompt,
		reveal:   msg.input.Reveal,
	})
	m = normalizeUpdateModel(updated)
	if cmd == nil {
		cause := strings.TrimSpace(m.status)
		if cause == "" {
			cause = "engineer session launch did not start"
		}
		status := fmt.Sprintf("Added TODO #%d and prepared worktree %s, but no engineer was launched: %s", msg.todo.ID, filepath.Base(msg.result.WorktreePath), cause)
		if msg.projectSetupAction != "" {
			status = fmt.Sprintf("%s, added TODO #%d, and prepared worktree %s, but no engineer was launched: %s", projectSetupReceipt(msg.projectSetupAction, msg.input.ProjectPath), msg.todo.ID, filepath.Base(msg.result.WorktreePath), cause)
		}
		return m, bossControlResultCmd(msg.inv, status, errors.New(status))
	}
	m.status = fmt.Sprintf("Prepared worktree %s; launching %s for TODO #%d...", filepath.Base(msg.result.WorktreePath), msg.provider.Label(), msg.todo.ID)
	cmd = m.trackBossTodoWorktreeEngineerLaunchCmd(msg.input, msg.provider, msg.todo, msg.projectSetupAction, cmd)
	return m, bossControlExecutionCmd(msg.inv, cmd)
}

func (m Model) trackBossTodoWorktreeEngineerLaunchCmd(input control.TodoCreateWorktreeAndStartEngineerInput, provider codexapp.Provider, todo model.TodoItem, projectSetupAction service.CreateOrAttachProjectAction, cmd tea.Cmd) tea.Cmd {
	svc := m.svc
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		msg := cmd()
		opened, ok := msg.(codexSessionOpenedMsg)
		if !ok {
			return msg
		}
		worktreeLabel := filepath.Base(firstNonEmptyTrimmed(input.WorktreePath, opened.projectPath))
		projectLabel := bossControlProjectTargetLabel(input.ProjectName, input.ProjectPath)
		target := fmt.Sprintf("%s TODO #%d", projectLabel, todo.ID)
		if label := strings.TrimSpace(firstNonEmptyTrimmed(input.TodoLabel, todo.Text)); label != "" {
			target += " " + compactEngineerNoticeText(label, 48)
		}
		target = strings.TrimSpace(target)
		if opened.err != nil {
			opened.status = fmt.Sprintf("Added TODO #%d and prepared worktree %s, but no engineer was launched: %v", todo.ID, worktreeLabel, opened.err)
			if projectSetupAction != "" {
				opened.status = fmt.Sprintf("%s, added TODO #%d, and prepared worktree %s, but no engineer was launched: %v", projectSetupReceipt(projectSetupAction, input.ProjectPath), todo.ID, worktreeLabel, opened.err)
			}
			return opened
		}
		opened.status = fmt.Sprintf("%s AI engineer launched for %s in worktree %s.", provider.Label(), target, worktreeLabel)
		if projectSetupAction != "" {
			opened.status = fmt.Sprintf("%s; %s AI engineer launched for %s in worktree %s.", projectSetupReceipt(projectSetupAction, input.ProjectPath), provider.Label(), target, worktreeLabel)
		}
		if svc == nil {
			opened.status += " The engineer is running, but TODO session tracking is unavailable."
			return opened
		}
		at := embeddedSnapshotActivityAt(opened.snapshot)
		if at.IsZero() {
			at = time.Now()
		}
		source := modelSessionSourceFromCodexProvider(embeddedProvider(opened.snapshot))
		if err := svc.MarkTodoWorkStarted(ctx, opened.projectPath, todo.ID, source, opened.snapshot.ThreadID, at); err != nil {
			opened.status += " The engineer is running, but TODO session tracking failed: " + err.Error()
		}
		return opened
	}
}

func todoCreateWorktreeAndStartEngineerInvocationFromInput(input control.TodoCreateWorktreeAndStartEngineerInput) control.Invocation {
	normalized, err := control.NormalizeTodoCreateWorktreeAndStartEngineerInput(input)
	if err != nil {
		normalized = input
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return control.Invocation{}
	}
	return control.Invocation{Capability: control.CapabilityTodoCreateWorktreeAndStartEngineer, RequestID: strings.TrimSpace(normalized.RequestID), Args: payload}
}

func projectCreateAndStartEngineerInvocationFromInput(input control.ProjectCreateAndStartEngineerInput) control.Invocation {
	normalized, err := control.NormalizeProjectCreateAndStartEngineerInput(input)
	if err != nil {
		normalized = input
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return control.Invocation{}
	}
	return control.Invocation{Capability: control.CapabilityProjectCreateAndStartEngineer, RequestID: strings.TrimSpace(normalized.RequestID), Args: payload}
}

func trackedWorkInvocationFromInput(inv control.Invocation, input control.TodoCreateWorktreeAndStartEngineerInput) control.Invocation {
	if inv.Capability != control.CapabilityProjectCreateAndStartEngineer {
		return todoCreateWorktreeAndStartEngineerInvocationFromInput(input)
	}
	var createInput control.ProjectCreateAndStartEngineerInput
	if err := json.Unmarshal(inv.Args, &createInput); err != nil {
		return inv
	}
	createInput.ProjectPath = input.ProjectPath
	createInput.ProjectName = input.ProjectName
	createInput.Provider = input.Provider
	createInput.Reveal = input.Reveal
	createInput.TodoID = input.TodoID
	createInput.TodoLabel = input.TodoLabel
	createInput.WorktreePath = input.WorktreePath
	return projectCreateAndStartEngineerInvocationFromInput(createInput)
}

func (m Model) executeTodoCompleteControlWithOutcome(input control.TodoCompleteInput) controlInvocationOutcome {
	if m.svc == nil || m.svc.Store() == nil {
		err := errors.New("service unavailable")
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	item, err := m.svc.Store().GetTodo(m.ctx, input.TodoID)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	projectPath := strings.TrimSpace(item.ProjectPath)
	if projectPath == "" {
		projectPath = strings.TrimSpace(input.ProjectPath)
	}
	if projectPath == "" {
		err := fmt.Errorf("TODO #%d has no project path", input.TodoID)
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	if ref := strings.TrimSpace(input.ProjectPath); ref != "" && filepath.Clean(ref) != filepath.Clean(projectPath) {
		err := fmt.Errorf("TODO #%d belongs to %s, not %s", input.TodoID, projectPath, ref)
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	if item.Done {
		m.status = fmt.Sprintf("TODO #%d was already complete", input.TodoID)
		return controlInvocationOutcome{model: m}
	}
	if err := m.svc.ToggleTodoDone(m.ctx, projectPath, input.TodoID, true); err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	name := filepath.Base(projectPath)
	if project, ok := m.projectSummaryByPathAllProjects(projectPath); ok {
		name = projectNameForPicker(project, projectPath)
	}
	m = m.clearBossTrackedTodo(projectPath, input.TodoID)
	m.status = fmt.Sprintf("Marked TODO #%d complete in %s", input.TodoID, name)
	return controlInvocationOutcome{model: m}
}

func (m Model) executeGitPrepareCommitControlWithOutcome(input control.GitPrepareCommitInput) controlInvocationOutcome {
	if m.svc == nil {
		err := errors.New("service unavailable")
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	project, err := m.resolveControlProjectRef(input.ProjectPath, input.ProjectName)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	if !project.PresentOnDisk {
		err := errors.New("Commit preview requires a folder present on disk")
		m.status = err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	intent := service.GitActionCommit
	if input.PushAfterCommit {
		intent = service.GitActionFinish
	}
	target := projectNameForPicker(project, project.Path)
	m.closeHelpChatMode("Opening commit preview for " + target)
	cmd := m.startCommitPreview(project, intent, input.Message)
	if cmd == nil {
		err := errors.New("commit preview did not start")
		m.status = err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	return controlInvocationOutcome{model: m, cmd: cmd}
}

func (m Model) executeSettingsUpdateControlWithOutcome(input control.SettingsUpdateInput) controlInvocationOutcome {
	normalized, err := control.NormalizeSettingsUpdateInput(input)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	settings := m.currentSettingsBaseline()
	changed, err := applySettingsUpdateChanges(&settings, normalized.Changes)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	if !changed {
		m.status = "Settings already match the requested update."
		return controlInvocationOutcome{model: m, inv: settingsUpdateInvocationFromInput(normalized)}
	}
	m.status = "Saving settings..."
	return controlInvocationOutcome{model: m, cmd: m.saveSettingsCmd(settings), inv: settingsUpdateInvocationFromInput(normalized)}
}

type settingsUpdateValueKind int

const (
	settingsUpdateList settingsUpdateValueKind = iota
	settingsUpdateString
	settingsUpdateBool
)

type settingsUpdateFieldSpec struct {
	Label           string
	Kind            settingsUpdateValueKind
	GetList         func(config.EditableSettings) []string
	SetList         func(*config.EditableSettings, []string)
	NormalizeValues func([]string) ([]string, error)
	GetString       func(config.EditableSettings) string
	SetString       func(*config.EditableSettings, string) error
	GetBool         func(config.EditableSettings) bool
	SetBool         func(*config.EditableSettings, bool)
}

var settingsUpdateFieldSpecs = map[control.SettingsField]settingsUpdateFieldSpec{
	control.SettingsFieldIncludePaths: {
		Label: "include paths",
		Kind:  settingsUpdateList,
		GetList: func(settings config.EditableSettings) []string {
			return append([]string(nil), settings.IncludePaths...)
		},
		SetList: func(settings *config.EditableSettings, values []string) {
			settings.IncludePaths = append([]string(nil), values...)
		},
		NormalizeValues: normalizeSettingsPathValues,
	},
	control.SettingsFieldExcludePaths: {
		Label: "exclude paths",
		Kind:  settingsUpdateList,
		GetList: func(settings config.EditableSettings) []string {
			return append([]string(nil), settings.ExcludePaths...)
		},
		SetList: func(settings *config.EditableSettings, values []string) {
			settings.ExcludePaths = append([]string(nil), values...)
		},
		NormalizeValues: normalizeSettingsPathValues,
	},
	control.SettingsFieldExcludeProjectPatterns: {
		Label: "exclude project patterns",
		Kind:  settingsUpdateList,
		GetList: func(settings config.EditableSettings) []string {
			return append([]string(nil), settings.ExcludeProjectPatterns...)
		},
		SetList: func(settings *config.EditableSettings, values []string) {
			settings.ExcludeProjectPatterns = append([]string(nil), values...)
		},
		NormalizeValues: normalizeSettingsPatternValues,
	},
	control.SettingsFieldPrivacyMode: {
		Label: "privacy mode",
		Kind:  settingsUpdateBool,
		GetBool: func(settings config.EditableSettings) bool {
			return settings.PrivacyMode
		},
		SetBool: func(settings *config.EditableSettings, value bool) {
			settings.PrivacyMode = value
		},
	},
	control.SettingsFieldHideReasoningSections: {
		Label: "hide reasoning sections",
		Kind:  settingsUpdateBool,
		GetBool: func(settings config.EditableSettings) bool {
			return settings.HideReasoningSections
		},
		SetBool: func(settings *config.EditableSettings, value bool) {
			settings.HideReasoningSections = value
		},
	},
	control.SettingsFieldCodexLaunchPreset: {
		Label: "Codex launch preset",
		Kind:  settingsUpdateString,
		GetString: func(settings config.EditableSettings) string {
			return string(settings.CodexLaunchPreset)
		},
		SetString: func(settings *config.EditableSettings, value string) error {
			preset, err := codexcli.ParsePreset(value)
			if err != nil {
				return err
			}
			settings.CodexLaunchPreset = preset
			return nil
		},
	},
}

func applySettingsUpdateChanges(settings *config.EditableSettings, changes []control.SettingsChange) (bool, error) {
	if settings == nil {
		return false, errors.New("settings are unavailable")
	}
	changed := false
	for _, change := range changes {
		applied, err := applySettingsUpdateChange(settings, change)
		if err != nil {
			return false, err
		}
		changed = changed || applied
	}
	return changed, nil
}

func applySettingsUpdateChange(settings *config.EditableSettings, change control.SettingsChange) (bool, error) {
	spec, ok := settingsUpdateFieldSpecs[change.Field]
	if !ok {
		return false, fmt.Errorf("unsupported settings field: %s", change.Field)
	}
	switch spec.Kind {
	case settingsUpdateList:
		return applySettingsListChange(settings, spec, change)
	case settingsUpdateString:
		if change.Operation != control.SettingsUpdateSet {
			return false, fmt.Errorf("%s only supports set", spec.Label)
		}
		current := strings.TrimSpace(spec.GetString(*settings))
		next := strings.TrimSpace(change.Value)
		if current == next {
			return false, nil
		}
		if err := spec.SetString(settings, next); err != nil {
			return false, err
		}
		return true, nil
	case settingsUpdateBool:
		if change.Operation != control.SettingsUpdateSet {
			return false, fmt.Errorf("%s only supports set", spec.Label)
		}
		current := spec.GetBool(*settings)
		if current == change.BoolValue {
			return false, nil
		}
		spec.SetBool(settings, change.BoolValue)
		return true, nil
	default:
		return false, fmt.Errorf("unsupported settings field kind for %s", spec.Label)
	}
}

func applySettingsListChange(settings *config.EditableSettings, spec settingsUpdateFieldSpec, change control.SettingsChange) (bool, error) {
	current, err := spec.NormalizeValues(spec.GetList(*settings))
	if err != nil {
		return false, err
	}
	values, err := spec.NormalizeValues(change.Values)
	if err != nil {
		return false, err
	}
	var next []string
	switch change.Operation {
	case control.SettingsUpdateSet:
		next = values
	case control.SettingsUpdateAppendUnique:
		next = append([]string(nil), current...)
		seen := settingsValueSet(next)
		for _, value := range values {
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			next = append(next, value)
		}
	case control.SettingsUpdateRemove:
		remove := settingsValueSet(values)
		for _, value := range current {
			if _, ok := remove[value]; ok {
				continue
			}
			next = append(next, value)
		}
	default:
		return false, fmt.Errorf("%s does not support %s", spec.Label, change.Operation)
	}
	if stringSlicesEqual(current, next) {
		return false, nil
	}
	spec.SetList(settings, next)
	return true, nil
}

func normalizeSettingsPatternValues(values []string) ([]string, error) {
	return normalizeSettingsListValues(values, false)
}

func normalizeSettingsPathValues(values []string) ([]string, error) {
	normalized, err := normalizeSettingsListValues(values, true)
	if err != nil {
		return nil, err
	}
	for i, value := range normalized {
		expanded, err := expandHomeForSettingsUpdate(value)
		if err != nil {
			return nil, err
		}
		normalized[i] = filepath.Clean(expanded)
	}
	return dedupeSettingsValues(normalized), nil
}

func normalizeSettingsListValues(values []string, rejectComma bool) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("settings values must be one line")
		}
		if rejectComma && strings.Contains(value, ",") {
			return nil, fmt.Errorf("path settings values must not contain commas")
		}
		out = append(out, value)
	}
	return dedupeSettingsValues(out), nil
}

func dedupeSettingsValues(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func settingsValueSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func expandHomeForSettingsUpdate(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func settingsUpdateInvocationFromInput(input control.SettingsUpdateInput) control.Invocation {
	normalized, err := control.NormalizeSettingsUpdateInput(input)
	if err != nil {
		normalized = input
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return control.Invocation{}
	}
	return control.Invocation{
		Capability: control.CapabilitySettingsUpdate,
		RequestID:  strings.TrimSpace(normalized.RequestID),
		Args:       payload,
	}
}

func (m Model) resolveControlTrackedTodo(project model.ProjectSummary, todoID int64, todoText string) (model.TodoItem, error) {
	if todoID <= 0 {
		return model.TodoItem{ProjectPath: strings.TrimSpace(project.Path), Text: strings.TrimSpace(todoText)}, nil
	}
	if m.svc == nil || m.svc.Store() == nil {
		return model.TodoItem{}, errors.New("service unavailable for TODO tracking")
	}
	item, err := m.svc.Store().GetTodo(m.ctx, todoID)
	if err != nil {
		return model.TodoItem{}, err
	}
	projectPath := filepath.Clean(strings.TrimSpace(project.Path))
	todoProjectPath := filepath.Clean(strings.TrimSpace(item.ProjectPath))
	if projectPath != "" && projectPath != "." && todoProjectPath != "" && todoProjectPath != "." && projectPath != todoProjectPath {
		return model.TodoItem{}, fmt.Errorf("TODO #%d belongs to %s, not %s", todoID, item.ProjectPath, project.Path)
	}
	if item.Done {
		return model.TodoItem{}, fmt.Errorf("TODO #%d is already complete", todoID)
	}
	if strings.TrimSpace(item.Text) == "" {
		item.Text = strings.TrimSpace(todoText)
	}
	if suggestion, err := m.svc.Store().GetTodoWorktreeSuggestion(m.ctx, todoID); err == nil {
		item.WorktreeSuggestion = &suggestion
	}
	return item, nil
}

func (m Model) resolveControlTodoWorkProject(project model.ProjectSummary, todo model.TodoItem) (model.ProjectSummary, error) {
	if todo.ID <= 0 {
		return project, nil
	}
	workProjectPath := filepath.Clean(strings.TrimSpace(todo.WorkProjectPath))
	if workProjectPath == "" || workProjectPath == "." || normalizeProjectPath(workProjectPath) == normalizeProjectPath(project.Path) {
		return project, nil
	}
	workProject, err := m.resolveControlProjectRef(workProjectPath, "")
	if err != nil {
		return model.ProjectSummary{}, fmt.Errorf("TODO #%d is linked to worktree %s, but that worktree is unavailable: %w", todo.ID, workProjectPath, err)
	}
	return workProject, nil
}

func engineerSendPromptInvocationFromInput(input control.EngineerSendPromptInput) control.Invocation {
	normalized, err := control.NormalizeEngineerSendPromptInput(input)
	if err != nil {
		normalized = input
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return control.Invocation{}
	}
	return control.Invocation{
		Capability: control.CapabilityEngineerSendPrompt,
		RequestID:  strings.TrimSpace(normalized.RequestID),
		Args:       payload,
	}
}

func todoDisplayLabelFromItem(item model.TodoItem) string {
	if label := todoWorktreeSuggestionDisplayLabel(item.WorktreeSuggestion); label != "" {
		return label
	}
	return compactEngineerNoticeText(strings.Join(strings.Fields(strings.TrimSpace(item.Text)), " "), 48)
}

func todoWorktreeSuggestionDisplayLabel(suggestion *model.TodoWorktreeSuggestion) string {
	if suggestion == nil {
		return ""
	}
	raw := strings.TrimSpace(suggestion.WorktreeSuffix)
	if raw == "" {
		raw = strings.TrimSpace(suggestion.BranchName)
		if idx := strings.LastIndex(raw, "/"); idx >= 0 {
			raw = raw[idx+1:]
		}
	}
	for _, prefix := range []string{"todo-", "feat-", "feature-", "fix-", "docs-", "doc-", "chore-", "refactor-", "test-"} {
		raw = strings.TrimPrefix(raw, prefix)
	}
	raw = strings.Trim(raw, "-_ ")
	raw = strings.ReplaceAll(raw, "-", " ")
	raw = strings.ReplaceAll(raw, "_", " ")
	return compactEngineerNoticeText(strings.Join(strings.Fields(raw), " "), 48)
}

func (m Model) controlFreshSessionBlockedByActiveEngineerTurn(project model.ProjectSummary, provider codexapp.Provider, targetLabel string) (string, bool) {
	projectPath := strings.TrimSpace(project.Path)
	targetLabel = strings.TrimSpace(targetLabel)
	if targetLabel == "" {
		targetLabel = "this work"
	}
	if snapshot, ok := m.liveEmbeddedSnapshotForProject(projectPath, provider); ok && embeddedSessionBlocksProviderSwitch(snapshot) {
		return fmt.Sprintf("The embedded %s engineer session is already running for %s, so I did not start a fresh session. Wait for the current turn to finish, then try again.", provider.Label(), targetLabel), true
	}
	if snapshot, ok := m.liveEmbeddedSnapshotForProject(projectPath, provider); ok && snapshot.Started && !snapshot.Closed {
		return fmt.Sprintf("The embedded %s engineer session is still open for %s. An idle turn does not show that its task is finished, so I did not replace it. Finish or close that session, or start the new request in a dedicated worktree.", provider.Label(), targetLabel), true
	}
	latestProvider := providerForSessionFormat(project.LatestSessionFormat)
	if latestProvider == "" || latestProvider != provider {
		return "", false
	}
	if !projectLatestSessionBlocksProviderSwitch(project, m.currentTime(), m.embeddedLaunchProtectionWindow()) {
		return "", false
	}
	return fmt.Sprintf("The latest %s engineer turn is still unfinished for %s, so I did not start a fresh session. Wait for the current turn to finish, then try again.", provider.Label(), targetLabel), true
}

func (m Model) liveAgentTaskSnapshot(task model.AgentTask) (codexapp.Snapshot, bool) {
	path := strings.TrimSpace(task.WorkspacePath)
	if path == "" {
		return codexapp.Snapshot{}, false
	}
	providers := []codexapp.Provider{codexProviderFromSessionSource(task.Provider)}
	providers = append(providers, codexapp.ProviderCodex, codexapp.ProviderOpenCode, codexapp.ProviderClaudeCode, codexapp.ProviderLCAgent)
	for _, provider := range providers {
		if provider == "" {
			continue
		}
		if snapshot, ok := m.liveEmbeddedSnapshotForProject(path, provider); ok {
			return snapshot, true
		}
	}
	return codexapp.Snapshot{}, false
}

func (m Model) agentTaskLaunchTrackingCmd(task model.AgentTask, cmd tea.Cmd, successStatus string) tea.Cmd {
	return func() tea.Msg {
		msg := cmd()
		opened, ok := msg.(codexSessionOpenedMsg)
		if !ok || opened.err != nil || m.svc == nil {
			return msg
		}
		taskID := strings.TrimSpace(task.ID)
		opened.agentTaskID = taskID
		opened.agentTaskTitle = strings.TrimSpace(task.Title)
		provider := modelSessionSourceFromCodexProvider(embeddedProvider(opened.snapshot))
		sessionID := strings.TrimSpace(opened.snapshot.ThreadID)
		if _, err := m.svc.AttachAgentTaskEngineerSession(m.ctx, taskID, provider, sessionID); err != nil {
			opened.err = err
			opened.status = strings.TrimSpace(opened.status)
			if opened.status == "" {
				opened.status = successStatus
			}
			opened.status += "; task tracking update failed"
			return opened
		}
		status := strings.TrimSpace(successStatus)
		if status == "" {
			status = "The requested task is underway"
		}
		opened.status = status
		return opened
	}
}

func modelAgentTaskKindFromControl(kind control.AgentTaskKind) model.AgentTaskKind {
	switch kind.Normalized() {
	case control.AgentTaskKindSubagent:
		return model.AgentTaskKindSubagent
	default:
		return model.AgentTaskKindAgent
	}
}

func (m Model) resolveAgentTaskControlProvider(provider control.Provider, task model.AgentTask) (codexapp.Provider, error) {
	switch provider.Normalized() {
	case control.ProviderAuto:
		if resolved := codexProviderFromSessionSource(task.Provider); resolved != "" {
			return resolved, nil
		}
		return codexapp.ProviderCodex, nil
	case control.ProviderCodex:
		return codexapp.ProviderCodex, nil
	case control.ProviderOpenCode:
		return codexapp.ProviderOpenCode, nil
	case control.ProviderClaudeCode:
		return "", errors.New("Claude Code is present in the protocol but disabled for control execution")
	case control.ProviderLCAgent:
		return codexapp.ProviderLCAgent, nil
	default:
		return "", fmt.Errorf("unsupported engineer provider: %s", provider)
	}
}

func codexProviderFromSessionSource(source model.SessionSource) codexapp.Provider {
	switch model.NormalizeSessionSource(source) {
	case model.SessionSourceOpenCode:
		return codexapp.ProviderOpenCode
	case model.SessionSourceClaudeCode:
		return codexapp.ProviderClaudeCode
	case model.SessionSourceLCAgent:
		return codexapp.ProviderLCAgent
	case model.SessionSourceCodex:
		return codexapp.ProviderCodex
	default:
		return ""
	}
}

func modelSessionSourceFromCodexProvider(provider codexapp.Provider) model.SessionSource {
	switch provider.Normalized() {
	case codexapp.ProviderOpenCode:
		return model.SessionSourceOpenCode
	case codexapp.ProviderClaudeCode:
		return model.SessionSourceClaudeCode
	case codexapp.ProviderLCAgent:
		return model.SessionSourceLCAgent
	case codexapp.ProviderCodex:
		return model.SessionSourceCodex
	default:
		return model.SessionSourceUnknown
	}
}

func projectSummaryForAgentTask(task model.AgentTask) (model.ProjectSummary, error) {
	path := strings.TrimSpace(task.WorkspacePath)
	if path == "" {
		return model.ProjectSummary{}, fmt.Errorf("agent task %s does not have a workspace path", task.ID)
	}
	name := strings.TrimSpace(task.Title)
	if name == "" {
		name = task.ID
	}
	source := agentTaskDisplaySource(task)
	provider := codexProviderFromSessionSource(source)
	sessionID := taskSessionIDForProvider(task, provider)
	format := agentTaskSessionFormat(source)
	summary := agentTaskListSummary(task)
	return model.ProjectSummary{
		Path:                            path,
		Name:                            name,
		Kind:                            model.ProjectKindAgentTask,
		CategoryID:                      strings.TrimSpace(task.CategoryID),
		CategoryName:                    strings.TrimSpace(task.CategoryName),
		CategoryPrivate:                 task.CategoryPrivate,
		LastActivity:                    agentTaskLastActivity(task),
		Status:                          agentTaskProjectStatus(task),
		AttentionScore:                  agentTaskAttentionScore(task),
		PresentOnDisk:                   true,
		ManuallyAdded:                   true,
		LatestSessionSource:             source,
		LatestSessionID:                 sessionID,
		LatestRawSessionID:              sessionID,
		LatestSessionFormat:             format,
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: agentTaskClassificationType(task),
		LatestSessionSummary:            summary,
	}, nil
}

func taskSessionIDForProvider(task model.AgentTask, provider codexapp.Provider) string {
	source := modelSessionSourceFromCodexProvider(provider)
	for _, resource := range task.Resources {
		if model.NormalizeAgentTaskResourceKind(resource.Kind) != model.AgentTaskResourceEngineerSession {
			continue
		}
		if model.NormalizeSessionSource(resource.Provider) == source {
			return strings.TrimSpace(resource.SessionID)
		}
	}
	if model.NormalizeSessionSource(task.Provider) == source {
		return strings.TrimSpace(task.SessionID)
	}
	return ""
}

type agentTaskPromptOptions struct {
	ResumePausedGoal bool
}

func agentTaskLaunchPrompt(task model.AgentTask, prompt string, options agentTaskPromptOptions) string {
	lines := []string{
		"Little Control Room agent task:",
		"ID: " + strings.TrimSpace(task.ID),
		"Title: " + strings.TrimSpace(task.Title),
		"Kind: " + string(model.NormalizeAgentTaskKind(task.Kind)),
	}
	if parent := strings.TrimSpace(task.ParentTaskID); parent != "" {
		lines = append(lines, "Parent task: "+parent)
	}
	if len(task.Capabilities) > 0 {
		lines = append(lines, "Allowed capabilities: "+strings.Join(task.Capabilities, ", "))
	}
	if resources := agentTaskResourcePromptSummary(task.Resources); resources != "" {
		lines = append(lines, "Resources: "+resources)
	}
	if options.ResumePausedGoal {
		lines = append(lines,
			"",
			"Resume signal:",
			"The user explicitly confirmed this agent task continuation. If this session contains a paused /goal, treat this message as the user's explicit instruction to resume that goal now and continue toward the request below.",
			"Do not report that you are still paused merely because an earlier instruction said to wait for the user; this handoff is that user resume instruction.",
		)
	}
	lines = append(lines, engineerReportContractPromptLines()...)
	lines = append(lines, "", "User request:", strings.TrimSpace(prompt))
	return strings.Join(lines, "\n")
}

func (m Model) agentTaskLaunchPromptWithRuntimeContext(task model.AgentTask, prompt string, options ...agentTaskPromptOptions) string {
	opts := agentTaskPromptOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	return m.promptWithRuntimeContext(agentTaskLaunchPrompt(task, prompt, opts), m.agentTaskRuntimeContextLines(task))
}

func (m Model) engineerPromptWithRuntimeContext(project model.ProjectSummary, prompt string, todo model.TodoItem) string {
	return m.promptWithRuntimeContext(engineerLaunchPromptWithTodo(prompt, todo), m.projectRuntimeContextLines(project))
}

func engineerLaunchPrompt(prompt string) string {
	return engineerLaunchPromptWithTodo(prompt, model.TodoItem{})
}

func engineerLaunchPromptWithTodo(prompt string, todo model.TodoItem) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	lines := []string{"Little Control Room engineer task:"}
	lines = append(lines, trackedTodoPromptLines(todo)...)
	lines = append(lines, engineerReportContractPromptLines()...)
	lines = append(lines, "", "User request:", prompt)
	return strings.Join(lines, "\n")
}

func engineerPromptWithTrackedTodo(prompt string, todo model.TodoItem) string {
	prompt = strings.TrimSpace(prompt)
	if todo.ID <= 0 || prompt == "" {
		return prompt
	}
	lines := []string{"Little Control Room engineer task:"}
	lines = append(lines, trackedTodoPromptLines(todo)...)
	lines = append(lines, "", "User request:", prompt)
	return strings.Join(lines, "\n")
}

func trackedTodoPromptLines(todo model.TodoItem) []string {
	if todo.ID <= 0 {
		return nil
	}
	text := strings.TrimSpace(todo.Text)
	lines := []string{fmt.Sprintf("Tracked project TODO: #%d", todo.ID)}
	if text != "" {
		lines = append(lines, "TODO text: "+text)
	}
	lines = append(lines, "When done, report whether this TODO is complete, partial, blocked, or still open.")
	return lines
}

func engineerReportContractPromptLines() []string {
	return []string{
		"",
		"Report contract:",
		"- Answer the user's exact request directly, with enough concrete detail for Chat to summarize without guessing.",
		"- Preserve source, metric, timeframe, scope, negations, and explicit exclusions from the user request; if evidence answers a different question, report that mismatch instead of substituting it.",
		"- For comparison, diff, cleanup, or review work, name what was compared, what was kept, what was discarded, and the substantive differences.",
		"- For retry, sync, export, file, or document work, say whether content changed and summarize the meaningful changes; if nothing changed, name the file or document and say there were no content changes.",
		"- Do not final-report only success/failure plus artifact links; include the substantive outcome, or say exactly which requested outcome evidence is still missing.",
		"- Avoid vague wrap-ups like only saying the entries differ, the state is clean, canonical copies were kept, or the retry succeeded.",
	}
}

func (m Model) promptWithRuntimeContext(prompt string, contextLines []string) string {
	prompt = strings.TrimSpace(prompt)
	if len(contextLines) == 0 {
		return prompt
	}
	lines := []string{}
	if prompt != "" {
		lines = append(lines, prompt, "")
	}
	lines = append(lines, "Little Control Room testing context:")
	lines = append(lines, contextLines...)
	return strings.Join(lines, "\n")
}

func (m Model) agentTaskRuntimeContextLines(task model.AgentTask) []string {
	seen := map[string]bool{}
	var lines []string
	for _, resource := range task.Resources {
		if model.NormalizeAgentTaskResourceKind(resource.Kind) != model.AgentTaskResourceProject {
			continue
		}
		path := firstNonEmptyTrimmed(resource.ProjectPath, resource.Path)
		if path == "" {
			continue
		}
		cleanPath := filepath.Clean(path)
		if cleanPath == "." || seen[cleanPath] {
			continue
		}
		seen[cleanPath] = true
		project, ok := m.projectSummaryByPathAllProjects(cleanPath)
		if !ok {
			project = model.ProjectSummary{Path: cleanPath, Name: strings.TrimSpace(resource.Label)}
		}
		lines = append(lines, m.projectRuntimeContextLines(project)...)
	}
	return lines
}

func (m Model) projectRuntimeContextLines(project model.ProjectSummary) []string {
	projectPath := filepath.Clean(strings.TrimSpace(project.Path))
	if projectPath == "" || projectPath == "." {
		return nil
	}
	snapshot := m.projectRuntimeContextSnapshot(projectPath)
	if !runtimeDetailAvailable(project.RunCommand, snapshot) {
		return nil
	}
	context := bossRuntimeContextFromProject(project, snapshot)
	label := strings.TrimSpace(firstNonEmptyTrimmed(context.ProjectName, context.ProjectPath, "project"))
	prefix := "Project " + label + ": "
	lines := []string{}
	if url := strings.TrimSpace(context.PrimaryURL); url != "" {
		lines = append(lines, "- "+prefix+"use runtime/test URL "+url)
	}
	if len(context.AdditionalURLs) > 0 {
		lines = append(lines, "- "+prefix+"additional runtime URLs: "+strings.Join(context.AdditionalURLs, ", "))
	}
	if strings.TrimSpace(context.PrimaryURL) == "" && len(context.AdditionalURLs) == 0 {
		lines = append(lines, "- "+prefix+"no runtime/test URL detected; if browser testing is needed, start or inspect the app and report the URL used")
	}
	if len(context.Ports) > 0 {
		lines = append(lines, "- "+prefix+"detected listening ports: "+joinPorts(context.Ports))
	}
	if command := strings.TrimSpace(context.Command); command != "" {
		commandLabel := "managed runtime command"
		if snapshot.External {
			commandLabel = "local instance command"
		}
		lines = append(lines, "- "+prefix+commandLabel+": "+command)
	}
	if status := strings.TrimSpace(context.Status); status != "" {
		statusLabel := "managed runtime status"
		if snapshot.External {
			statusLabel = "local instance status"
		}
		lines = append(lines, "- "+prefix+statusLabel+": "+status)
	}
	return lines
}

func agentTaskResourcePromptSummary(resources []model.AgentTaskResource) string {
	if len(resources) == 0 {
		return ""
	}
	parts := make([]string, 0, len(resources))
	for _, resource := range resources {
		if text := compactControlAgentTaskResource(resource); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, ", ")
}

func compactControlAgentTaskResource(resource model.AgentTaskResource) string {
	label := strings.TrimSpace(resource.Label)
	switch model.NormalizeAgentTaskResourceKind(resource.Kind) {
	case model.AgentTaskResourceProject:
		return strings.TrimSpace(firstNonEmptyTrimmed(resource.ProjectPath, resource.Path, label))
	case model.AgentTaskResourceProcess:
		if resource.PID > 0 {
			return strings.TrimSpace(fmt.Sprintf("pid %d %s", resource.PID, label))
		}
	case model.AgentTaskResourcePort:
		if resource.Port > 0 {
			return strings.TrimSpace(fmt.Sprintf("port %d %s", resource.Port, label))
		}
	case model.AgentTaskResourceFile:
		return strings.TrimSpace(firstNonEmptyTrimmed(resource.Path, label))
	case model.AgentTaskResourceAgentTask:
		return strings.TrimSpace(firstNonEmptyTrimmed(resource.RefID, label))
	case model.AgentTaskResourceEngineerSession:
		session := strings.TrimSpace(resource.SessionID)
		if session == "" {
			return label
		}
		provider := string(model.NormalizeSessionSource(resource.Provider))
		if provider == "" {
			return "session " + session
		}
		return provider + " session " + session
	}
	return label
}

func agentTaskResourcesFromControl(resources []control.ResourceRef) []model.AgentTaskResource {
	out := make([]model.AgentTaskResource, 0, len(resources))
	for _, resource := range resources {
		converted := model.AgentTaskResource{
			Kind:        modelAgentTaskResourceKindFromControl(resource.Kind),
			RefID:       strings.TrimSpace(resource.ID),
			ProjectPath: strings.TrimSpace(resource.ProjectPath),
			Path:        strings.TrimSpace(resource.Path),
			PID:         resource.PID,
			Port:        resource.Port,
			Provider:    modelSessionSourceFromControlProvider(resource.Provider),
			SessionID:   strings.TrimSpace(resource.SessionID),
			Label:       strings.TrimSpace(resource.Label),
		}
		if converted.Kind == "" {
			continue
		}
		if converted.Kind == model.AgentTaskResourceEngineerSession && converted.SessionID == "" {
			converted.SessionID = strings.TrimSpace(resource.ID)
		}
		out = append(out, converted)
	}
	return out
}

func modelAgentTaskResourceKindFromControl(kind control.ResourceKind) model.AgentTaskResourceKind {
	switch kind {
	case control.ResourceProject:
		return model.AgentTaskResourceProject
	case control.ResourceProcess:
		return model.AgentTaskResourceProcess
	case control.ResourcePort:
		return model.AgentTaskResourcePort
	case control.ResourceFile:
		return model.AgentTaskResourceFile
	case control.ResourceAgentTask:
		return model.AgentTaskResourceAgentTask
	case control.ResourceEngineerSession:
		return model.AgentTaskResourceEngineerSession
	default:
		return ""
	}
}

func modelSessionSourceFromControlProvider(provider control.Provider) model.SessionSource {
	switch provider.Normalized() {
	case control.ProviderOpenCode:
		return model.SessionSourceOpenCode
	case control.ProviderClaudeCode:
		return model.SessionSourceClaudeCode
	case control.ProviderLCAgent:
		return model.SessionSourceLCAgent
	case control.ProviderCodex:
		return model.SessionSourceCodex
	default:
		return model.SessionSourceUnknown
	}
}

func (m Model) resolveControlProject(input control.EngineerSendPromptInput) (model.ProjectSummary, error) {
	return m.resolveControlProjectRef(input.ProjectPath, input.ProjectName)
}

func (m Model) resolveControlProjectRef(projectPath, projectName string) (model.ProjectSummary, error) {
	if path := normalizeProjectPath(projectPath); path != "" {
		if project, ok := m.projectSummaryByPathAllProjects(path); ok {
			return project, nil
		}
		return model.ProjectSummary{}, fmt.Errorf("project is not loaded: %s", path)
	}

	name := strings.TrimSpace(projectName)
	if name == "" {
		return model.ProjectSummary{}, errors.New("project_path or project_name required")
	}
	var (
		matched model.ProjectSummary
		found   bool
	)
	candidates := append(append([]model.ProjectSummary(nil), m.allProjects...), m.archivedProjects...)
	candidates = append(candidates, m.projects...)
	for _, project := range candidates {
		if !controlProjectNameMatches(project, name) {
			continue
		}
		if found && normalizeProjectPath(matched.Path) != normalizeProjectPath(project.Path) {
			return model.ProjectSummary{}, fmt.Errorf("project name is ambiguous: %s", name)
		}
		matched = project
		found = true
	}
	if !found {
		return model.ProjectSummary{}, fmt.Errorf("project is not loaded: %s", name)
	}
	return matched, nil
}

func controlProjectNameMatches(project model.ProjectSummary, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	candidates := []string{
		strings.TrimSpace(project.Name),
		projectNameForPicker(project, project.Path),
		strings.TrimSpace(filepath.Base(project.Path)),
	}
	for _, candidate := range candidates {
		if strings.EqualFold(strings.TrimSpace(candidate), name) {
			return true
		}
	}
	return false
}

func (m Model) resolveControlEngineerProvider(provider control.Provider, project model.ProjectSummary) (codexapp.Provider, error) {
	switch provider.Normalized() {
	case control.ProviderAuto:
		resolved := m.preferredEmbeddedProviderForProject(project)
		if resolved.Normalized() == codexapp.ProviderClaudeCode {
			return "", errors.New("Claude Code is present in the protocol but disabled for control execution")
		}
		return resolved, nil
	case control.ProviderCodex:
		return codexapp.ProviderCodex, nil
	case control.ProviderOpenCode:
		return codexapp.ProviderOpenCode, nil
	case control.ProviderClaudeCode:
		return "", errors.New("Claude Code is present in the protocol but disabled for control execution")
	case control.ProviderLCAgent:
		return codexapp.ProviderLCAgent, nil
	default:
		return "", fmt.Errorf("unsupported engineer provider: %s", provider)
	}
}
