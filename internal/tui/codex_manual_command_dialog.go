package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"lcroom/internal/codexapp"
)

const codexManualCommandOutcomeLimit = 1200

type codexManualCommandOutcomeState struct {
	ProjectPath string
	RequestID   string
	Input       textarea.Model
}

func codexManualCommandFromSnapshot(snapshot codexapp.Snapshot) (*codexapp.ToolInputRequest, *codexapp.ManualCommandRequest, bool) {
	request := snapshot.PendingToolInput
	if request == nil || request.ManualCommand == nil {
		return nil, nil, false
	}
	return request, request.ManualCommand, true
}

func (m Model) renderCodexManualCommandDialogOverlay(body string, bodyW, bodyH int, snapshot codexapp.Snapshot) string {
	request, command, ok := codexManualCommandFromSnapshot(snapshot)
	if !ok {
		return body
	}
	panelW := min(94, max(58, bodyW-18))
	panelW = min(panelW, max(14, bodyW-6))
	panelInnerW := max(10, panelW-4)
	content := m.renderCodexManualCommandDialogContent(snapshot, *request, *command, panelInnerW)
	content = clampDialogContent(
		content,
		max(1, bodyH-2),
		4,
		detailMutedStyle.Render("... command details shortened to fit; press C to copy the full command ..."),
	)
	panel := renderCodexManualCommandDialogPanel(panelW, panelInnerW, content)
	left := max(0, (bodyW-lipgloss.Width(panel))/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func renderCodexManualCommandDialogPanel(panelW, panelInnerW int, content string) string {
	return lipgloss.NewStyle().
		Width(panelW).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("178")).
		Padding(0, 1).
		Background(dialogPanelBackground).
		Foreground(lipgloss.Color("252")).
		Render(fillDialogBlock(content, panelInnerW))
}

func (m Model) renderCodexManualCommandDialogContent(
	snapshot codexapp.Snapshot,
	request codexapp.ToolInputRequest,
	command codexapp.ManualCommandRequest,
	width int,
) string {
	providerLabel := embeddedProvider(snapshot).Label()
	if providerLabel == "" {
		providerLabel = "LCAgent"
	}
	header := detailWarningStyle.Render("Manual terminal action required")
	header += detailMutedStyle.Render(" - " + providerLabel)
	lines := []string{header, ""}

	prompt := strings.TrimSpace(command.Prompt)
	if prompt == "" {
		prompt = "Run this command in your terminal, then report what happened."
	}
	lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, width, prompt)...)
	lines = append(lines, renderWrappedDialogTextLines(
		detailWarningStyle,
		width,
		"LCAgent is paused because it cannot perform this action with its current authority.",
	)...)

	if reason := strings.TrimSpace(command.Reason); reason != "" {
		lines = append(lines, "", detailSectionStyle.Render("Why it stopped"))
		lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, width, reason)...)
	}
	if cwd := strings.TrimSpace(command.CWD); cwd != "" {
		lines = append(lines, "", renderWrappedDetailField("Working directory", detailMutedStyle, width, m.displayPathWithHomeTilde(cwd)))
	}
	lines = append(lines, "", detailSectionStyle.Render("Command"))
	lines = append(lines, renderCodexManualCommandBlock(command.Command, width))
	lines = append(lines, renderWrappedDialogTextLines(
		detailMutedStyle,
		width,
		"This dialog does not execute or approve the command. Run it yourself only if you want this change.",
	)...)

	if outcome := m.activeCodexManualCommandOutcome(request.ID); outcome != nil {
		input := outcome.Input
		input.SetWidth(max(12, width-2))
		input.SetHeight(max(3, min(6, input.LineCount()+1)))
		lines = append(lines, "", detailSectionStyle.Render("Report another outcome"), input.View(), "")
		actions := []string{
			renderDialogAction("Enter", "send outcome", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("Alt+Enter", "newline", pushActionKeyStyle, pushActionTextStyle),
			renderDialogAction("Esc", "back", cancelActionKeyStyle, cancelActionTextStyle),
			renderDialogAction("Alt+Up", "hide pane", navigateActionKeyStyle, navigateActionTextStyle),
		}
		lines = append(lines, renderCodexElicitationActionLines(width, actions)...)
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "")
	copyLabel := "copy command"
	copyKeyStyle := navigateActionKeyStyle
	copyTextStyle := navigateActionTextStyle
	if m.codexManualCommandCopyBusy {
		copyLabel = "copying..."
		copyKeyStyle = disabledActionKeyStyle
		copyTextStyle = disabledActionTextStyle
	}
	actions := []string{
		renderDialogAction("C", copyLabel, copyKeyStyle, copyTextStyle),
		renderDialogAction("R", "I ran it — verify", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("S", "skip", cancelActionKeyStyle, cancelActionTextStyle),
		renderDialogAction("O", "report another outcome", pushActionKeyStyle, pushActionTextStyle),
		renderDialogAction("Esc", "hide pane", navigateActionKeyStyle, navigateActionTextStyle),
	}
	lines = append(lines, renderCodexElicitationActionLines(width, actions)...)
	return strings.Join(lines, "\n")
}

func renderCodexManualCommandBlock(command string, width int) string {
	command = strings.TrimSpace(command)
	if command == "" {
		command = "(command unavailable)"
	}
	return lipgloss.NewStyle().
		Width(max(1, width-2)).
		Padding(0, 1).
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("232")).
		Render(command)
}

func (m Model) activeCodexManualCommandOutcome(requestID string) *codexManualCommandOutcomeState {
	state := m.codexManualCommandOutcome
	if state == nil || strings.TrimSpace(state.RequestID) != strings.TrimSpace(requestID) ||
		normalizeProjectPath(state.ProjectPath) != normalizeProjectPath(m.codexVisibleProject) {
		return nil
	}
	return state
}

func (m *Model) beginCodexManualCommandOutcome(requestID string) tea.Cmd {
	input := newCodexTextarea()
	input.Placeholder = "Describe what happened"
	input.CharLimit = codexManualCommandOutcomeLimit
	input.SetHeight(3)
	cmd := input.Focus()
	m.codexManualCommandOutcome = &codexManualCommandOutcomeState{
		ProjectPath: m.codexVisibleProject,
		RequestID:   strings.TrimSpace(requestID),
		Input:       input,
	}
	m.status = "Type what happened, then press Enter to send it to LCAgent"
	return cmd
}

func (m Model) updateCodexManualCommandMode(
	request *codexapp.ToolInputRequest,
	command *codexapp.ManualCommandRequest,
	msg tea.KeyMsg,
) (tea.Model, tea.Cmd) {
	if request == nil || command == nil {
		return m, nil
	}
	if outcome := m.activeCodexManualCommandOutcome(request.ID); outcome != nil {
		switch msg.String() {
		case "esc":
			m.codexManualCommandOutcome = nil
			m.status = "Manual command request still waiting"
			return m, nil
		case "enter":
			answer := strings.TrimSpace(outcome.Input.Value())
			if answer == "" {
				m.status = "Describe what happened before sending the outcome"
				return m, nil
			}
			return m.submitCodexManualCommandAnswer(request, command, answer, "Sending reported command outcome...")
		case "alt+enter", "ctrl+j":
			outcome.Input.InsertString("\n")
			m.codexManualCommandOutcome = outcome
			return m, nil
		}
		var cmd tea.Cmd
		outcome.Input, cmd = outcome.Input.Update(msg)
		m.codexManualCommandOutcome = outcome
		return m, cmd
	}

	switch strings.ToLower(msg.String()) {
	case "c":
		return m, m.copyCodexManualCommand(request.ID, command.Command)
	case "r":
		label := firstNonEmptyString(strings.TrimSpace(command.CompletedLabel), "Ran it")
		return m.submitCodexManualCommandAnswer(request, command, label, "Reporting the command as run; LCAgent must verify the result...")
	case "s":
		label := firstNonEmptyString(strings.TrimSpace(command.DeclinedLabel), "Didn't run it")
		return m.submitCodexManualCommandAnswer(request, command, label, "Skipping the manual command...")
	case "o":
		return m, m.beginCodexManualCommandOutcome(request.ID)
	}
	return m, nil
}

func (m Model) submitCodexManualCommandAnswer(
	request *codexapp.ToolInputRequest,
	command *codexapp.ManualCommandRequest,
	answer string,
	status string,
) (tea.Model, tea.Cmd) {
	questionID := strings.TrimSpace(command.QuestionID)
	if questionID == "" && len(request.Questions) > 0 {
		questionID = strings.TrimSpace(request.Questions[0].ID)
	}
	if questionID == "" {
		m.status = "Manual command response cannot be sent: question ID is missing"
		return m, nil
	}
	m.codexManualCommandOutcome = nil
	m.status = status
	return m, m.respondVisibleToolInputCmd(map[string][]string{questionID: {answer}})
}

func (m *Model) copyCodexManualCommand(requestID, command string) tea.Cmd {
	command = strings.TrimSpace(command)
	if command == "" {
		m.status = "No manual command is available to copy"
		return nil
	}
	if m.codexManualCommandCopyBusy {
		m.status = "Manual command copy already in progress"
		return nil
	}
	m.codexManualCommandCopyBusy = true
	m.status = "Copying manual command..."
	projectPath := m.codexVisibleProject
	return func() tea.Msg {
		return codexManualCommandCopyMsg{
			projectPath: projectPath,
			requestID:   requestID,
			err:         clipboardTextWriter(command),
		}
	}
}

func (m Model) applyCodexManualCommandCopyMsg(msg codexManualCommandCopyMsg) (tea.Model, tea.Cmd) {
	m.codexManualCommandCopyBusy = false
	if msg.err != nil {
		m.reportError("Manual command copy failed", msg.err, msg.projectPath)
		return m, nil
	}
	m.err = nil
	m.status = "Copied manual command to clipboard"
	return m, nil
}
