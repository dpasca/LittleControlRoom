package boss

import (
	"encoding/json"
	"fmt"
	"strings"

	"lcroom/internal/control"
	"lcroom/internal/uistyle"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderControlConfirmationOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderControlConfirmationDialog(bodyW, bodyH)
	panelW := lipgloss.Width(panel)
	panelH := lipgloss.Height(panel)
	left := maxInt(0, (bodyW-panelW)/2)
	top := maxInt(0, minInt((bodyH-panelH)/3, bodyH-panelH))
	return overlayBossBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderControlConfirmationDialog(bodyW, bodyH int) string {
	panelW := minInt(bodyW, minInt(maxInt(58, bodyW-10), 88))
	panelInnerW := maxInt(28, panelW-4)
	content := m.renderControlConfirmationContent(panelInnerW)
	panelH := maxInt(12, countBlockLines(content)+4)
	if bodyH > 0 {
		panelH = minInt(panelH, maxInt(8, bodyH-2))
	}
	return renderBossControlPanel("Confirm Control Action", content, panelW, panelH)
}

func (m Model) renderControlConfirmationContent(width int) string {
	if m.pendingControl == nil {
		return ""
	}
	input, err := engineerSendPromptInputFromInvocation(m.pendingControl.Invocation)
	if err == nil {
		return renderEngineerSendPromptConfirmation(input, width)
	}
	return m.renderStructuredControlConfirmationContent(width)
}

func renderEngineerSendPromptConfirmation(input control.EngineerSendPromptInput, width int) string {
	provider := input.Provider.Label()
	if input.Provider == control.ProviderAuto {
		provider = "Auto"
	}
	target := firstNonEmpty(input.ProjectName, input.ProjectPath)
	if target == "" {
		target = "selected project"
	}
	mode := "resume or open"
	if input.SessionMode == control.SessionModeNew {
		mode = "start fresh"
	}
	visibility := "background"
	if input.Reveal {
		visibility = "show after send"
	}

	lines := []string{
		bossControlNoticeStyle.Render(fitLine("External action: send a prompt to an engineer session", width)),
		"",
		renderBossControlDetail("Provider", provider, width),
		renderBossControlDetail("Project", target, width),
		renderBossControlDetail("Mode", mode, width),
		renderBossControlDetail("View", visibility, width),
		"",
		bossControlSectionStyle.Render(fitLine("Prompt", width)),
		renderBossControlPromptBox(input.Prompt, width),
		"",
		strings.Join([]string{
			renderBossControlAction("Enter", "send", uistyle.DialogActionPrimary),
			renderBossControlAction("Esc", "cancel", uistyle.DialogActionCancel),
			renderBossControlAction("Alt+Up", "hide", uistyle.DialogActionNavigate),
		}, "   "),
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderStructuredControlConfirmationContent(width int) string {
	if m.pendingControl == nil {
		return ""
	}
	switch m.pendingControl.Invocation.Capability {
	case control.CapabilityAgentTaskCreate:
		var input control.AgentTaskCreateInput
		if err := json.Unmarshal(m.pendingControl.Invocation.Args, &input); err == nil {
			capabilities := strings.Join(input.Capabilities, ", ")
			if capabilities == "" {
				capabilities = "none"
			}
			lines := []string{
				bossControlNoticeStyle.Render(fitLine("External action: create an agent task", width)),
				"",
				renderBossControlDetail("Task", input.Title, width),
				renderBossControlDetail("Kind", string(input.Kind), width),
				renderBossControlDetail("Provider", input.Provider.Label(), width),
				renderBossControlDetail("Caps", capabilities, width),
				"",
				bossControlSectionStyle.Render(fitLine("Prompt", width)),
				renderBossControlPromptBox(input.Prompt, width),
				"",
				strings.Join([]string{
					renderBossControlAction("Enter", "run", uistyle.DialogActionPrimary),
					renderBossControlAction("Esc", "cancel", uistyle.DialogActionCancel),
					renderBossControlAction("Alt+Up", "hide", uistyle.DialogActionNavigate),
				}, "   "),
			}
			return strings.Join(lines, "\n")
		}
	case control.CapabilityAgentTaskContinue:
		var input control.AgentTaskContinueInput
		if err := json.Unmarshal(m.pendingControl.Invocation.Args, &input); err == nil {
			lines := []string{
				bossControlNoticeStyle.Render(fitLine("External action: continue an agent task", width)),
				"",
				renderBossControlDetail("Task", input.TaskID, width),
				renderBossControlDetail("Provider", input.Provider.Label(), width),
				renderBossControlDetail("Mode", string(input.SessionMode), width),
				"",
				bossControlSectionStyle.Render(fitLine("Prompt", width)),
				renderBossControlPromptBox(input.Prompt, width),
				"",
				strings.Join([]string{
					renderBossControlAction("Enter", "run", uistyle.DialogActionPrimary),
					renderBossControlAction("Esc", "cancel", uistyle.DialogActionCancel),
					renderBossControlAction("Alt+Up", "hide", uistyle.DialogActionNavigate),
				}, "   "),
			}
			return strings.Join(lines, "\n")
		}
	case control.CapabilityAgentTaskClose:
		var input control.AgentTaskCloseInput
		if err := json.Unmarshal(m.pendingControl.Invocation.Args, &input); err == nil {
			lines := []string{
				bossControlNoticeStyle.Render(fitLine("External action: close an agent task", width)),
				"",
				renderBossControlDetail("Task", input.TaskID, width),
				renderBossControlDetail("Status", string(input.Status), width),
				renderBossControlDetail("Session", fmt.Sprintf("close: %t", input.CloseSession), width),
				"",
				bossControlSectionStyle.Render(fitLine("Summary", width)),
				renderBossControlPromptBox(input.Summary, width),
				"",
				strings.Join([]string{
					renderBossControlAction("Enter", "run", uistyle.DialogActionPrimary),
					renderBossControlAction("Esc", "cancel", uistyle.DialogActionCancel),
					renderBossControlAction("Alt+Up", "hide", uistyle.DialogActionNavigate),
				}, "   "),
			}
			return strings.Join(lines, "\n")
		}
	}
	return m.renderFallbackControlConfirmationContent(width)
}

func (m Model) renderFallbackControlConfirmationContent(width int) string {
	preview := strings.TrimSpace(m.pendingControl.Preview)
	if preview == "" {
		preview = "A control action is waiting for confirmation."
	}
	lines := []string{
		bossControlNoticeStyle.Render(fitLine("External action waiting for confirmation", width)),
		"",
		renderBossControlPromptBox(preview, width),
		"",
		strings.Join([]string{
			renderBossControlAction("Enter", "send", uistyle.DialogActionPrimary),
			renderBossControlAction("Esc", "cancel", uistyle.DialogActionCancel),
			renderBossControlAction("Alt+Up", "hide", uistyle.DialogActionNavigate),
		}, "   "),
	}
	return strings.Join(lines, "\n")
}

func engineerSendPromptInputFromInvocation(inv control.Invocation) (control.EngineerSendPromptInput, error) {
	if inv.Capability != control.CapabilityEngineerSendPrompt {
		return control.EngineerSendPromptInput{}, fmt.Errorf("unsupported capability: %s", inv.Capability)
	}
	var input control.EngineerSendPromptInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return control.EngineerSendPromptInput{}, err
	}
	return input, nil
}

func renderBossControlDetail(label, value string, width int) string {
	labelW := 10
	if width < 42 {
		labelW = 8
	}
	valueW := maxInt(8, width-labelW-2)
	left := bossControlLabelStyle.Width(labelW).Render(fitLine(label, labelW))
	right := bossControlValueStyle.Render(fitLine(value, valueW))
	return fitStyledLine(left+"  "+right, width)
}

func renderBossControlPromptBox(prompt string, width int) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = "(empty prompt)"
	}
	innerW := maxInt(8, width-2)
	lines := wrappedBlockLines(prompt, innerW)
	maxLines := 7
	if len(lines) > maxLines {
		lines = append(lines[:maxLines-1], "...")
	}
	for i, line := range lines {
		lines[i] = bossControlPromptStyle.Width(width).Render(fitStyledLine(" "+line, width))
	}
	return strings.Join(lines, "\n")
}

func renderBossControlAction(key, label string, tone uistyle.DialogActionTone) string {
	return uistyle.RenderDialogActionTone(key, label, tone, bossDialogActionFillStyle)
}

func renderBossControlPanel(title, content string, width, height int) string {
	width = maxInt(12, width)
	height = maxInt(4, height)
	innerWidth := bossPanelInnerWidth(width)
	innerHeight := maxInt(1, height-2)
	titleLine := bossControlTitleStyle.Render(fitLine(title, innerWidth))
	body := fitRenderedBlock(content, innerWidth, maxInt(0, innerHeight-2))
	rendered := bossControlPanelStyle.Width(bossPanelStyleWidth(width)).Height(innerHeight).Render(titleLine + "\n" + body)
	return fitRenderedBlock(rendered, width, height)
}

var (
	bossControlAccent       = lipgloss.Color("214")
	bossControlPanelStyle   = panelStyle.BorderForeground(bossControlAccent)
	bossControlTitleStyle   = lipgloss.NewStyle().Foreground(bossControlAccent).Background(bossPanelBackground).Bold(true)
	bossControlNoticeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(bossControlAccent).Bold(true)
	bossControlSectionStyle = lipgloss.NewStyle().
				Foreground(bossControlAccent).
				Background(bossPanelBackground).
				Bold(true)
	bossControlLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("244")).
				Background(bossPanelBackground)
	bossControlValueStyle = lipgloss.NewStyle().
				Foreground(bossPanelText).
				Background(bossPanelBackground).
				Bold(true)
	bossControlPromptStyle = lipgloss.NewStyle().
				Foreground(bossPanelText).
				Background(lipgloss.Color("#101820"))
)
