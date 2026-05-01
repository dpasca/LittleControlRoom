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
	Invocation control.Invocation
	Status     string
	Err        error
}

func controlProposalFromBossAction(action bossAction) (control.Invocation, string, error) {
	capability := control.CapabilityName(strings.TrimSpace(action.ControlCapability))
	if capability == "" {
		return control.Invocation{}, "", errors.New("control proposal needs a capability")
	}
	if capability != control.CapabilityEngineerSendPrompt {
		return control.Invocation{}, "", fmt.Errorf("unsupported control capability: %s", capability)
	}
	input := control.EngineerSendPromptInput{
		RequestID:   strings.TrimSpace(action.RequestID),
		ProjectPath: strings.TrimSpace(action.ProjectPath),
		ProjectName: strings.TrimSpace(action.ProjectName),
		Provider:    control.Provider(strings.TrimSpace(action.EngineerProvider)),
		SessionMode: control.SessionMode(strings.TrimSpace(action.SessionMode)),
		Prompt:      strings.TrimSpace(action.Prompt),
		Reveal:      action.Reveal,
	}
	args, err := json.Marshal(input)
	if err != nil {
		return control.Invocation{}, "", err
	}
	inv := control.Invocation{
		RequestID:  input.RequestID,
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
	default:
		return "", fmt.Errorf("unsupported control capability: %s", inv.Capability)
	}
}

func controlResultContent(msg ControlInvocationResultMsg) string {
	status := strings.TrimSpace(msg.Status)
	if msg.Err != nil {
		if status == "" {
			status = msg.Err.Error()
		} else {
			status += ": " + msg.Err.Error()
		}
		return "I could not send that to the engineer session: " + status
	}
	if status == "" {
		status = "Control request sent to the engineer session."
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
		message := ChatMessage{
			Role:    "assistant",
			Content: "Canceled. I did not send anything to the engineer session.",
			At:      m.now(),
		}
		m.messages = append(m.messages, message)
		m.status = "Engineer prompt canceled"
		m.syncLayout(true)
		return m, m.saveBossChatMessageCmd(message)
	case "alt+up":
		return m, m.exitCmd()
	default:
		m.status = "Confirm engineer prompt with Enter, or Esc to cancel"
		return m, nil
	}
}

func (m Model) applyControlInvocationResult(msg ControlInvocationResultMsg) (tea.Model, tea.Cmd) {
	m.pendingControl = nil
	message := ChatMessage{
		Role:    "assistant",
		Content: controlResultContent(msg),
		At:      m.now(),
	}
	m.messages = append(m.messages, message)
	if msg.Err != nil {
		m.status = "Engineer prompt failed"
	} else {
		m.status = "Engineer prompt sent"
	}
	m.syncLayout(true)
	return m, m.saveBossChatMessageCmd(message)
}
