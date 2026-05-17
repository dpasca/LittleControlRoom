package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	bossui "lcroom/internal/boss"
	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"

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
	case control.CapabilityTodoComplete:
		var input control.TodoCompleteInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return m.executeTodoCompleteControlWithOutcome(input)
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
		status := "Control action completed."
		var err error
		if opened, ok := msg.(codexSessionOpenedMsg); ok {
			status = bossControlOpenedSessionStatus(inv, opened)
			err = opened.err
		}
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
			Kind:         "project",
			ProjectPath:  strings.TrimSpace(opened.projectPath),
			Title:        title,
			TodoID:       input.TodoID,
			TodoLabel:    strings.TrimSpace(input.TodoLabel),
			TodoText:     strings.TrimSpace(input.TodoText),
			EngineerName: bossui.EngineerNameForKey("project", opened.projectPath, sessionID),
			Provider:     provider,
			SessionID:    sessionID,
			Status:       "working",
			Active:       true,
			StartedAt:    startedAt,
			LastEventAt:  startedAt,
		}
	case control.CapabilityAgentTaskCreate:
		var input control.AgentTaskCreateInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			return nil
		}
		taskID := strings.TrimSpace(opened.agentTaskID)
		title := firstNonEmptyTrimmed(opened.agentTaskTitle, input.Title, taskID, "agent task")
		name := firstNonEmptyTrimmed(opened.agentTaskName, bossui.EngineerNameForKey("agent_task", taskID), "Engineer")
		return &bossui.ViewEngineerActivity{
			Kind:         "agent_task",
			TaskID:       taskID,
			ProjectPath:  strings.TrimSpace(opened.projectPath),
			Title:        title,
			EngineerName: name,
			Provider:     provider,
			SessionID:    sessionID,
			Status:       "working",
			Active:       true,
			StartedAt:    startedAt,
			LastEventAt:  startedAt,
		}
	case control.CapabilityAgentTaskContinue:
		var input control.AgentTaskContinueInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			return nil
		}
		taskID := firstNonEmptyTrimmed(opened.agentTaskID, input.TaskID)
		title := firstNonEmptyTrimmed(opened.agentTaskTitle, taskID, "agent task")
		name := firstNonEmptyTrimmed(opened.agentTaskName, bossui.EngineerNameForKey("agent_task", taskID), "Engineer")
		return &bossui.ViewEngineerActivity{
			Kind:         "agent_task",
			TaskID:       taskID,
			ProjectPath:  strings.TrimSpace(opened.projectPath),
			Title:        title,
			EngineerName: name,
			Provider:     provider,
			SessionID:    sessionID,
			Status:       "working",
			Active:       true,
			StartedAt:    startedAt,
			LastEventAt:  startedAt,
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
	name := bossui.EngineerNameForKey("project", opened.projectPath, opened.snapshot.ThreadID)
	targetPhrase := ""
	if target != "" {
		targetPhrase = " for " + target
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return "Opened the " + sessionLabel + targetPhrase + "."
	}
	if target != "" {
		return "Ok, " + name + " is working on " + bossTrackedTodoTargetLabel(target, input.TodoID, input.TodoLabel, input.TodoText) + "."
	}
	return "Ok, " + name + " is working on it now."
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
		name := bossui.EngineerNameForKey("agent_task", taskID)
		if taskID != "" {
			status = "Ok, " + name + " is working on " + taskID
		} else {
			status = "Ok, the engineer is working on the task"
		}
	}
	return strings.TrimRight(status, ".") + "."
}

func bossAgentTaskHandoffStatus(task model.AgentTask) string {
	name := bossEngineerNameForAgentTask(task)
	label := strings.TrimSpace(task.Title)
	if label == "" {
		label = strings.TrimSpace(task.ID)
	}
	if label == "" {
		label = "the task"
	}
	return "Ok, " + name + " is working on " + label
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

	trackedTodo, err := m.resolveControlTrackedTodo(project, input.TodoID, input.TodoText)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
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
		path := projectPath
		if path == "" {
			path = strings.TrimSpace(opened.projectPath)
		}
		if path == "" {
			path = strings.TrimSpace(snapshot.ProjectPath)
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
	prompt := m.agentTaskLaunchPromptWithRuntimeContext(task, input.Prompt)
	updated, cmd := m.launchEmbeddedForProjectWithOptions(project, provider, embeddedLaunchOptions{
		forceNew: input.SessionMode == control.SessionModeNew,
		prompt:   prompt,
		reveal:   input.Reveal,
		resumeID: taskSessionIDForProvider(task, provider),
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
	item, err := m.svc.AddTodo(m.ctx, project.Path, input.Text)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	name := projectNameForPicker(project, project.Path)
	m.status = fmt.Sprintf("Added TODO #%d to %s", item.ID, name)
	return controlInvocationOutcome{model: m}
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
		opened.agentTaskName = bossEngineerNameForAgentTask(task)
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
			status = "Ok, the engineer is working on the task"
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

func agentTaskLaunchPrompt(task model.AgentTask, prompt string) string {
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
	lines = append(lines, engineerReportContractPromptLines()...)
	lines = append(lines, "", "User request:", strings.TrimSpace(prompt))
	return strings.Join(lines, "\n")
}

func (m Model) agentTaskLaunchPromptWithRuntimeContext(task model.AgentTask, prompt string) string {
	return m.promptWithRuntimeContext(agentTaskLaunchPrompt(task, prompt), m.agentTaskRuntimeContextLines(task))
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
		"- Answer the user's exact request directly, with enough concrete detail for Boss Chat to summarize without guessing.",
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
	snapshot := m.projectRuntimeSnapshot(projectPath)
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
		lines = append(lines, "- "+prefix+"managed runtime command: "+command)
	}
	if status := strings.TrimSpace(context.Status); status != "" {
		lines = append(lines, "- "+prefix+"managed runtime status: "+status)
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
	for _, project := range append(append([]model.ProjectSummary(nil), m.allProjects...), m.projects...) {
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
