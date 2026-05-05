package boss

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"lcroom/internal/control"

	tea "github.com/charmbracelet/bubbletea"
)

type ControlProposal struct {
	Invocation control.Invocation
	Preview    string
}

type ControlInvocationConfirmedMsg struct {
	Invocation control.Invocation
}

type ControlInvocationResultMsg struct {
	Invocation     control.Invocation
	Status         string
	Activity       *ViewEngineerActivity
	Err            error
	AnnounceInChat bool
}

type controlProposalError struct {
	err error
}

func (e controlProposalError) Error() string {
	if e.err == nil {
		return "control proposal failed"
	}
	return "control proposal failed: " + e.err.Error()
}

func (e controlProposalError) Unwrap() error {
	return e.err
}

func wrapControlProposalError(err error) error {
	if err == nil {
		return nil
	}
	return controlProposalError{err: err}
}

func controlProposalFromBossAction(action bossAction) (control.Invocation, string, error) {
	capability := control.CapabilityName(strings.TrimSpace(action.ControlCapability))
	if capability == "" {
		return control.Invocation{}, "", errors.New("control proposal needs a capability")
	}
	var payload any
	switch capability {
	case control.CapabilityEngineerSendPrompt:
		payload = control.EngineerSendPromptInput{
			RequestID:   strings.TrimSpace(action.RequestID),
			ProjectPath: strings.TrimSpace(action.ProjectPath),
			ProjectName: strings.TrimSpace(action.ProjectName),
			Provider:    control.Provider(strings.TrimSpace(action.EngineerProvider)),
			SessionMode: control.SessionMode(strings.TrimSpace(action.SessionMode)),
			Prompt:      bossLosslessControlPrompt(action),
			Reveal:      action.Reveal,
		}
	case control.CapabilityAgentTaskCreate:
		payload = control.AgentTaskCreateInput{
			RequestID:    strings.TrimSpace(action.RequestID),
			Title:        strings.TrimSpace(action.TaskTitle),
			Kind:         control.AgentTaskKind(strings.TrimSpace(action.TaskKind)),
			ParentTaskID: strings.TrimSpace(action.ParentTaskID),
			Prompt:       bossLosslessControlPrompt(action),
			Provider:     control.Provider(strings.TrimSpace(action.EngineerProvider)),
			Reveal:       action.Reveal,
			Capabilities: append([]string(nil), action.Capabilities...),
			Resources:    append([]control.ResourceRef(nil), action.Resources...),
		}
	case control.CapabilityAgentTaskContinue:
		payload = control.AgentTaskContinueInput{
			RequestID:   strings.TrimSpace(action.RequestID),
			TaskID:      strings.TrimSpace(action.TaskID),
			Provider:    control.Provider(strings.TrimSpace(action.EngineerProvider)),
			SessionMode: control.SessionMode(strings.TrimSpace(action.SessionMode)),
			Prompt:      bossLosslessControlPrompt(action),
			Reveal:      action.Reveal,
		}
	case control.CapabilityAgentTaskClose:
		payload = control.AgentTaskCloseInput{
			RequestID:    strings.TrimSpace(action.RequestID),
			TaskID:       strings.TrimSpace(action.TaskID),
			Status:       control.AgentTaskCloseStatus(strings.TrimSpace(action.TaskCloseStatus)),
			Summary:      strings.TrimSpace(action.TaskSummary),
			CloseSession: action.CloseSession,
		}
	case control.CapabilityScratchTaskArchive:
		payload = control.ScratchTaskArchiveInput{
			RequestID:   strings.TrimSpace(action.RequestID),
			ProjectPath: strings.TrimSpace(action.ProjectPath),
			ProjectName: strings.TrimSpace(action.ProjectName),
		}
	default:
		return control.Invocation{}, "", fmt.Errorf("unsupported control capability: %s", capability)
	}
	args, err := json.Marshal(payload)
	if err != nil {
		return control.Invocation{}, "", err
	}
	inv := control.Invocation{
		RequestID:  strings.TrimSpace(action.RequestID),
		Capability: capability,
		Args:       args,
	}
	normalized, err := control.ValidateInvocation(inv)
	if err != nil {
		return control.Invocation{}, "", err
	}
	content, err := controlConfirmationContent(normalized)
	if err != nil {
		return control.Invocation{}, "", err
	}
	return normalized, content, nil
}

func bossLosslessControlPrompt(action bossAction) string {
	task := strings.TrimSpace(action.Prompt)
	if task == "" || !bossActionHasLosslessPacket(action) {
		return task
	}
	excerpt := strings.TrimSpace(action.IntentExcerpt)
	preserved := strings.TrimSpace(action.PreservedMeaning)
	if preserved == "" {
		preserved = "Preserve the source, metric, timeframe, scope, negations, and explicit exclusions in the user wording above."
	}
	success := strings.TrimSpace(action.SuccessCondition)
	if success == "" {
		success = "Answer the user's original request directly. If available evidence answers a different source, metric, timeframe, or scope, report that mismatch instead of substituting it."
	}

	lines := []string{"Boss Chat lossless task packet:"}
	if excerpt != "" {
		lines = append(lines, "", "Original user wording to preserve:", excerpt)
	}
	lines = append(lines,
		"",
		"Boss-reframed executable task:",
		task,
		"",
		"Preserved meaning:",
		preserved,
		"",
		"Success condition:",
		success,
		"",
		"Report contract:",
		"- Keep source names, metric names, timeframes, and exclusions explicit.",
		"- If the requested evidence is unavailable or a different source was checked, say that plainly and name what was checked instead.",
	)
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func bossActionHasLosslessPacket(action bossAction) bool {
	return strings.TrimSpace(action.IntentExcerpt) != "" ||
		strings.TrimSpace(action.PreservedMeaning) != "" ||
		strings.TrimSpace(action.SuccessCondition) != ""
}

func controlConfirmationContent(inv control.Invocation) (string, error) {
	switch inv.Capability {
	case control.CapabilityEngineerSendPrompt:
		var input control.EngineerSendPromptInput
		if err := json.Unmarshal(inv.Args, &input); err != nil {
			return "", err
		}
		provider := input.Provider.Label()
		if input.Provider == control.ProviderAuto {
			provider = "the preferred engineer session"
		}
		target := firstNonEmpty(input.ProjectName, input.ProjectPath)
		if target == "" {
			target = "the selected project"
		}
		mode := "resume or open the"
		if input.SessionMode == control.SessionModeNew {
			mode = "start a fresh"
		}
		visibility := "keep it in the background"
		if input.Reveal {
			visibility = "show it afterward"
		}
		lines := []string{
			fmt.Sprintf("Send this to %s for %s?", provider, target),
			"",
			strings.TrimSpace(input.Prompt),
			"",
			fmt.Sprintf("I will %s session and %s. Enter confirms; Esc cancels.", mode, visibility),
		}
		return strings.TrimSpace(strings.Join(lines, "\n")), nil
	case control.CapabilityAgentTaskCreate:
		var input control.AgentTaskCreateInput
		if err := json.Unmarshal(inv.Args, &input); err != nil {
			return "", err
		}
		provider := input.Provider.Label()
		if input.Provider == control.ProviderAuto {
			provider = "the preferred engineer session"
		}
		lines := []string{
			fmt.Sprintf("Create agent task %q and use %s?", input.Title, provider),
			"",
			strings.TrimSpace(input.Prompt),
			"",
			fmt.Sprintf("Capabilities: %s", strings.Join(input.Capabilities, ", ")),
			fmt.Sprintf("Resources: %s", controlResourceSummary(input.Resources)),
			"Enter confirms; Esc cancels.",
		}
		return strings.TrimSpace(strings.Join(lines, "\n")), nil
	case control.CapabilityAgentTaskContinue:
		var input control.AgentTaskContinueInput
		if err := json.Unmarshal(inv.Args, &input); err != nil {
			return "", err
		}
		mode := "resume or open"
		if input.SessionMode == control.SessionModeNew {
			mode = "start fresh"
		}
		lines := []string{
			fmt.Sprintf("Continue agent task %s?", input.TaskID),
			"",
			strings.TrimSpace(input.Prompt),
			"",
			fmt.Sprintf("I will %s the task's engineer session. Enter confirms; Esc cancels.", mode),
		}
		return strings.TrimSpace(strings.Join(lines, "\n")), nil
	case control.CapabilityAgentTaskClose:
		var input control.AgentTaskCloseInput
		if err := json.Unmarshal(inv.Args, &input); err != nil {
			return "", err
		}
		lines := []string{
			fmt.Sprintf("Mark agent task %s as %s?", input.TaskID, input.Status),
			"",
			strings.TrimSpace(input.Summary),
			"",
			"Enter confirms; Esc cancels.",
		}
		return strings.TrimSpace(strings.Join(lines, "\n")), nil
	case control.CapabilityScratchTaskArchive:
		var input control.ScratchTaskArchiveInput
		if err := json.Unmarshal(inv.Args, &input); err != nil {
			return "", err
		}
		target := firstNonEmpty(input.ProjectName, input.ProjectPath)
		lines := []string{
			fmt.Sprintf("Archive scratch task %s?", target),
			"",
			"This moves the task into the scratch-task archive and removes it from the active dashboard.",
			"",
			"Enter confirms; Esc cancels.",
		}
		return strings.TrimSpace(strings.Join(lines, "\n")), nil
	default:
		return "", fmt.Errorf("unsupported control capability: %s", inv.Capability)
	}
}

func controlResourceSummary(resources []control.ResourceRef) string {
	if len(resources) == 0 {
		return "none"
	}
	parts := make([]string, 0, minInt(len(resources), 5))
	for _, resource := range resources {
		label := strings.TrimSpace(resource.Label)
		switch resource.Kind {
		case control.ResourceProcess:
			if resource.PID > 0 {
				label = strings.TrimSpace(fmt.Sprintf("pid %d %s", resource.PID, label))
			}
		case control.ResourcePort:
			if resource.Port > 0 {
				label = strings.TrimSpace(fmt.Sprintf("port %d %s", resource.Port, label))
			}
		case control.ResourceProject:
			label = firstNonEmpty(resource.ProjectPath, resource.Path, label)
		case control.ResourceFile:
			label = firstNonEmpty(resource.Path, label)
		case control.ResourceAgentTask:
			label = firstNonEmpty(resource.ID, label)
		case control.ResourceEngineerSession:
			label = firstNonEmpty(resource.SessionID, label)
		}
		if label == "" {
			label = string(resource.Kind)
		}
		parts = append(parts, label)
		if len(parts) >= 5 {
			break
		}
	}
	return strings.Join(parts, ", ")
}

func controlResultContent(msg ControlInvocationResultMsg) string {
	status := strings.TrimSpace(msg.Status)
	if msg.Err != nil {
		errText := strings.TrimSpace(msg.Err.Error())
		switch {
		case status == "":
			status = errText
		case errText != "" && status != errText:
			status += ": " + errText
		}
		return "I could not complete that control action: " + status
	}
	if status == "" {
		status = "Control action completed."
	}
	return status
}

func copyControlInvocation(inv control.Invocation) control.Invocation {
	out := inv
	if inv.Args != nil {
		out.Args = append([]byte(nil), inv.Args...)
	}
	return out
}

func (m Model) ControlConfirmationActive() bool {
	return m.pendingControl != nil
}

func (m Model) updateControlConfirmation(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pendingControl == nil {
		return m, nil
	}
	switch msg.String() {
	case "enter":
		if !m.embedded {
			m.status = "Control actions need the main TUI host"
			return m, nil
		}
		inv := copyControlInvocation(m.pendingControl.Invocation)
		m.pendingControl = nil
		m.status = "Sending request to engineer session..."
		return m, func() tea.Msg {
			return ControlInvocationConfirmedMsg{Invocation: inv}
		}
	case "esc", "ctrl+c":
		m.pendingControl = nil
		m.status = "Control action canceled"
		m = m.recordOperationalNotice("control_canceled", "notice", "The user canceled a pending control action.")
		m.appendDeskEvent("control", "cancel", "The pending control action was canceled.")
		m.syncLayout(false)
		return m, nil
	default:
		m.status = "Confirm control action with Enter, or Esc to cancel"
		return m, nil
	}
}

func (m Model) applyControlInvocationResult(msg ControlInvocationResultMsg) (tea.Model, tea.Cmd) {
	m.pendingControl = nil
	content := controlResultContent(msg)
	var cmds []tea.Cmd
	if msg.Err != nil {
		m.status = operationalStatusLine(content, "Control action failed")
		m = m.recordOperationalNotice("control_failed", "error", content)
		m.appendDeskEvent("control", "failed", content)
	} else {
		m.status = operationalStatusLine(content, "Control action completed")
		m = m.recordOperationalNotice("control_completed", "notice", content)
		if msg.Activity != nil {
			m = m.recordTransientEngineerActivity(*msg.Activity)
			m.appendDeskEvent("engineer", "start", bossDeskActivityEventSummary(*msg.Activity, content))
		} else {
			m.appendDeskEvent("control", "done", content)
		}
	}
	if msg.AnnounceInChat {
		if saved, ok := m.appendAssistantNoticeMessage(content); ok {
			cmds = append(cmds, m.saveBossChatMessageCmd(saved))
		}
	}
	m.syncLayout(msg.AnnounceInChat)
	if m.svc != nil {
		cmds = append(cmds, m.loadStateCmd())
	}
	return m, tea.Batch(cmds...)
}

func (m Model) applyHostNotice(msg HostNoticeMsg) (tea.Model, tea.Cmd) {
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return m, nil
	}
	m = m.recordOperationalNotice("host_update", "notice", content)
	m.appendDeskEvent("host", "update", content)
	var cmds []tea.Cmd
	if msg.AnnounceInChat {
		if saved, ok := m.appendAssistantNoticeMessage(content); ok {
			cmds = append(cmds, m.saveBossChatMessageCmd(saved))
		}
	}
	m.status = operationalStatusLine(content, "Host update")
	m.syncLayout(msg.AnnounceInChat)
	return m, tea.Batch(cmds...)
}

func operationalStatusLine(content, fallback string) string {
	line := strings.Join(strings.Fields(content), " ")
	if line == "" {
		line = fallback
	}
	return clipText(line, 160)
}
