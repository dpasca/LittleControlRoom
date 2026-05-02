package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

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
	if outcome.cmd == nil {
		return m, bossControlResultCmd(inv, m.status, outcome.err)
	}
	return m, bossControlExecutionCmd(inv, outcome.cmd)
}

func (m Model) executeControlInvocation(inv control.Invocation) (tea.Model, tea.Cmd) {
	outcome := m.executeControlInvocationWithOutcome(inv)
	return outcome.model, outcome.cmd
}

type controlInvocationOutcome struct {
	model Model
	cmd   tea.Cmd
	err   error
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
	default:
		err := fmt.Errorf("unsupported capability: %s", normalized.Capability)
		m.status = "Control request unsupported: " + string(normalized.Capability)
		return controlInvocationOutcome{model: m, err: err}
	}
}

func bossControlExecutionCmd(inv control.Invocation, cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		msg := cmd()
		status := "Control action completed."
		var err error
		if opened, ok := msg.(codexSessionOpenedMsg); ok {
			status = strings.TrimSpace(opened.status)
			err = opened.err
		}
		result := bossui.ControlInvocationResultMsg{
			Invocation: copyControlInvocationForBoss(inv),
			Status:     status,
			Err:        err,
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

func bossControlResultCmd(inv control.Invocation, status string, err error) tea.Cmd {
	return func() tea.Msg {
		return bossui.ControlInvocationResultMsg{
			Invocation: copyControlInvocationForBoss(inv),
			Status:     strings.TrimSpace(status),
			Err:        err,
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
	if block, blocked := m.embeddedLaunchBlock(project, provider); blocked {
		err := errors.New(block.Message)
		m.status = block.Message
		return controlInvocationOutcome{model: m, err: err}
	}
	if controlPromptTargetsActiveEmbeddedSession(input, m, project.Path, provider) {
		err := fmt.Errorf("Boss control will not send prompts into an active embedded %s session. Start a fresh session or open the target session and send manually.", provider.Label())
		m.status = err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}

	updated, cmd := m.launchEmbeddedForProjectWithOptions(project, provider, embeddedLaunchOptions{
		forceNew: input.SessionMode == control.SessionModeNew,
		prompt:   input.Prompt,
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
	return controlInvocationOutcome{model: m, cmd: cmd}
}

func controlPromptTargetsActiveEmbeddedSession(input control.EngineerSendPromptInput, m Model, projectPath string, provider codexapp.Provider) bool {
	if input.SessionMode == control.SessionModeNew || strings.TrimSpace(input.Prompt) == "" {
		return false
	}
	snapshot, ok := m.liveEmbeddedSnapshotForProject(projectPath, provider)
	if !ok {
		return false
	}
	return embeddedSessionBlocksProviderSwitch(snapshot)
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
	if strings.TrimSpace(input.Prompt) == "" {
		m.status = "Created agent task " + task.ID
		return controlInvocationOutcome{model: m}
	}
	project, err := projectSummaryForAgentTask(task)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	prompt := agentTaskLaunchPrompt(task, input.Prompt)
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
	return controlInvocationOutcome{model: m, cmd: m.agentTaskLaunchTrackingCmd(task.ID, cmd, "Created agent task "+task.ID)}
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
	if controlPromptTargetsActiveEmbeddedSession(control.EngineerSendPromptInput{
		SessionMode: input.SessionMode,
		Prompt:      input.Prompt,
	}, m, project.Path, provider) {
		err := fmt.Errorf("Boss control will not send prompts into an active embedded %s session for agent task %s. Wait for it to finish or start a fresh session.", provider.Label(), task.ID)
		m.status = err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	prompt := agentTaskLaunchPrompt(task, input.Prompt)
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
	return controlInvocationOutcome{model: m, cmd: m.agentTaskLaunchTrackingCmd(task.ID, cmd, "Continued agent task "+task.ID)}
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
	m.status = fmt.Sprintf("Agent task %s is now %s", task.ID, task.Status)
	return controlInvocationOutcome{model: m}
}

func (m Model) liveAgentTaskSnapshot(task model.AgentTask) (codexapp.Snapshot, bool) {
	path := strings.TrimSpace(task.WorkspacePath)
	if path == "" {
		return codexapp.Snapshot{}, false
	}
	providers := []codexapp.Provider{codexProviderFromSessionSource(task.Provider)}
	providers = append(providers, codexapp.ProviderCodex, codexapp.ProviderOpenCode, codexapp.ProviderClaudeCode)
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

func (m Model) agentTaskLaunchTrackingCmd(taskID string, cmd tea.Cmd, prefix string) tea.Cmd {
	return func() tea.Msg {
		msg := cmd()
		opened, ok := msg.(codexSessionOpenedMsg)
		if !ok || opened.err != nil || m.svc == nil {
			return msg
		}
		provider := modelSessionSourceFromCodexProvider(embeddedProvider(opened.snapshot))
		sessionID := strings.TrimSpace(opened.snapshot.ThreadID)
		if _, err := m.svc.AttachAgentTaskEngineerSession(m.ctx, taskID, provider, sessionID); err != nil {
			opened.err = err
			opened.status = strings.TrimSpace(opened.status)
			if opened.status == "" {
				opened.status = prefix
			}
			opened.status += "; task tracking update failed"
			return opened
		}
		status := strings.TrimSpace(opened.status)
		if status == "" {
			status = "engineer session opened"
		}
		opened.status = prefix + " and " + lowerFirst(status)
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
	return model.ProjectSummary{
		Path:          path,
		Name:          name,
		PresentOnDisk: true,
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
	lines = append(lines, "", "User request:", strings.TrimSpace(prompt))
	return strings.Join(lines, "\n")
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
	case control.ProviderCodex:
		return model.SessionSourceCodex
	default:
		return model.SessionSourceUnknown
	}
}

func lowerFirst(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ToLower(value[:1]) + value[1:]
}

func (m Model) resolveControlProject(input control.EngineerSendPromptInput) (model.ProjectSummary, error) {
	if path := normalizeProjectPath(input.ProjectPath); path != "" {
		if project, ok := m.projectSummaryByPathAllProjects(path); ok {
			return project, nil
		}
		return model.ProjectSummary{}, fmt.Errorf("project is not loaded: %s", path)
	}

	name := strings.TrimSpace(input.ProjectName)
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
	default:
		return "", fmt.Errorf("unsupported engineer provider: %s", provider)
	}
}
