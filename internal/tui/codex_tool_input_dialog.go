package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"lcroom/internal/codexapp"
)

// codexToolInputSubmittedMsg reports the outcome of a structured answer so the
// dialog can drop its in-flight guard even when the provider rejects the
// response and the same request stays pending.
type codexToolInputSubmittedMsg struct {
	ProjectPath string
	RequestID   string
	Result      tea.Msg
}

// codexToolInputRow is one selectable line in the structured question dialog.
// Provider options and the optional free-text row share the same shape so
// numbering, cursor movement, and rendering stay in one place.
type codexToolInputRow struct {
	Number      int
	Label       string
	Answer      string
	Description string
	Checked     bool
	IsOther     bool
}

const codexToolInputOtherRowLabel = "Type a different answer"

func codexToolInputQuestionsFromSnapshot(snapshot codexapp.Snapshot) (*codexapp.ToolInputRequest, bool) {
	request := snapshot.PendingToolInput
	if request == nil || request.ManualCommand != nil || len(request.Questions) == 0 {
		return nil, false
	}
	return request, true
}

func codexToolInputRowsFor(question codexapp.ToolInputQuestion, answers []string) []codexToolInputRow {
	rows := make([]codexToolInputRow, 0, len(question.Options)+1)
	for _, option := range question.Options {
		label := strings.TrimSpace(option.Label)
		if label == "" {
			continue
		}
		rows = append(rows, codexToolInputRow{
			Label:       label,
			Answer:      label,
			Description: strings.TrimSpace(option.Description),
			Checked:     toolInputAnswerSelected(answers, label),
		})
	}
	if len(rows) > 0 && question.IsOther {
		rows = append(rows, codexToolInputRow{Label: codexToolInputOtherRowLabel, IsOther: true})
	}
	for i := range rows {
		rows[i].Number = i + 1
	}
	return rows
}

// codexToolInputAllowsText reports whether the question accepts a typed answer.
// Questions without usable options always do, otherwise the provider has to
// mark the question as accepting a custom answer.
func codexToolInputAllowsText(question codexapp.ToolInputQuestion, rows []codexToolInputRow) bool {
	return len(rows) == 0 || question.IsOther
}

func codexToolInputComposerFocused(rows []codexToolInputRow, cursor int) bool {
	if len(rows) == 0 {
		return true
	}
	return cursor >= 0 && cursor < len(rows) && rows[cursor].IsOther
}

// codexToolInputCursorForAnswers highlights the answer already recorded for a
// question so revisiting it shows the earlier choice instead of resetting to
// the first option.
func codexToolInputCursorForAnswers(question codexapp.ToolInputQuestion, answers map[string][]string) int {
	recorded := answers[question.ID]
	if len(nonEmptyToolInputAnswers(recorded)) == 0 {
		return 0
	}
	rows := codexToolInputRowsFor(question, recorded)
	for i, row := range rows {
		if row.IsOther {
			continue
		}
		if toolInputAnswerSelected(recorded, row.Answer) {
			return i
		}
	}
	if len(rows) > 0 && rows[len(rows)-1].IsOther {
		// The recorded answer was typed, so keep the free-text row in focus.
		return len(rows) - 1
	}
	return 0
}

func countAnsweredToolQuestions(request codexapp.ToolInputRequest, answers map[string][]string) int {
	answered := 0
	for _, question := range request.Questions {
		if len(nonEmptyToolInputAnswers(answers[question.ID])) > 0 {
			answered++
		}
	}
	return answered
}

func (m Model) codexToolInputSubmitPending(requestID string) bool {
	return strings.TrimSpace(m.codexToolInputSubmitting) != "" &&
		m.codexToolInputSubmitting == strings.TrimSpace(requestID) &&
		normalizeProjectPath(m.codexToolInputSubmitProject) == normalizeProjectPath(m.codexVisibleProject)
}

// codexToolInputComposerActive reports whether Esc should return to the option
// list instead of hiding the whole pane.
func (m Model) codexToolInputComposerActive(snapshot codexapp.Snapshot) bool {
	if m.codexPanelFocus == embeddedCodexFocusSidebar || snapshot.PendingApproval != nil {
		return false
	}
	request, ok := codexToolInputQuestionsFromSnapshot(snapshot)
	if !ok {
		return false
	}
	state := m.toolAnswerStateFor(m.codexVisibleProject, request)
	question := request.Questions[state.QuestionIndex]
	rows := codexToolInputRowsFor(question, state.Answers[question.ID])
	return len(rows) > 0 && codexToolInputComposerFocused(rows, state.OptionIndex)
}

func (m Model) renderCodexToolInputDialogOverlay(body string, bodyW, bodyH int, snapshot codexapp.Snapshot) string {
	request, ok := codexToolInputQuestionsFromSnapshot(snapshot)
	if !ok {
		return body
	}
	panelW := min(92, max(44, bodyW-14))
	panelW = min(panelW, max(14, bodyW-6))
	panelInnerW := max(10, panelW-4)
	maxLines := max(1, bodyH-4)
	content := m.renderCodexToolInputDialogContent(snapshot, *request, panelInnerW, maxLines)
	content = clampDialogContent(
		content,
		maxLines,
		4,
		detailMutedStyle.Render("... options shortened to fit ..."),
	)
	panel := renderDialogPanel(panelW, panelInnerW, content)
	left := max(0, (bodyW-lipgloss.Width(panel))/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

// codexToolInputDescriptionMode controls how much option help the dialog keeps
// when the pane is too short for the full layout.
type codexToolInputDescriptionMode int

const (
	codexToolInputDescriptionsAll codexToolInputDescriptionMode = iota
	codexToolInputDescriptionsSelected
	codexToolInputDescriptionsNone
)

// renderCodexToolInputDialogContent drops option help progressively instead of
// truncating it: full descriptions first, then only the highlighted one, then
// labels alone. Keeping every option visible matters more than its help text.
func (m Model) renderCodexToolInputDialogContent(
	snapshot codexapp.Snapshot,
	request codexapp.ToolInputRequest,
	width int,
	maxLines int,
) string {
	modes := []codexToolInputDescriptionMode{
		codexToolInputDescriptionsAll,
		codexToolInputDescriptionsSelected,
		codexToolInputDescriptionsNone,
	}
	content := ""
	for _, mode := range modes {
		content = m.renderCodexToolInputDialogBody(snapshot, request, width, mode)
		if maxLines <= 0 || strings.Count(content, "\n")+1 <= maxLines {
			return content
		}
	}
	return content
}

func (m Model) renderCodexToolInputDialogBody(
	snapshot codexapp.Snapshot,
	request codexapp.ToolInputRequest,
	width int,
	descriptions codexToolInputDescriptionMode,
) string {
	providerLabel := embeddedProvider(snapshot).Label()
	state := m.toolAnswerStateFor(m.codexVisibleProject, &request)
	question := request.Questions[state.QuestionIndex]
	rows := codexToolInputRowsFor(question, state.Answers[question.ID])
	composerFocused := codexToolInputComposerFocused(rows, state.OptionIndex)
	allowsText := codexToolInputAllowsText(question, rows)
	submitting := m.codexToolInputSubmitPending(request.ID)

	projectName := strings.TrimSpace(filepath.Base(firstNonEmptyString(snapshot.ProjectPath, m.codexVisibleProject)))
	if projectName == "." {
		projectName = ""
	}
	lines := []string{renderDialogHeader(providerLabel+" needs your answer", projectName, "", width), ""}

	if len(request.Questions) > 1 {
		progress := fmt.Sprintf("Question %d of %d", state.QuestionIndex+1, len(request.Questions))
		if answered := countAnsweredToolQuestions(request, state.Answers); answered > 0 {
			progress += fmt.Sprintf(" · %d of %d answered", answered, len(request.Questions))
		}
		lines = append(lines, detailMutedStyle.Render(fitFooterWidth(progress, width)))
	}
	if header := strings.TrimSpace(question.Header); header != "" {
		lines = append(lines, detailSectionStyle.Render(fitFooterWidth(header, width)))
	}
	lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, width, question.Question)...)

	hints := []string{}
	if question.MultiSelect {
		hints = append(hints, "Choose one or more")
	}
	if question.IsSecret {
		hints = append(hints, "Secret answer")
	}
	if len(hints) > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fitFooterWidth(strings.Join(hints, " · "), width)))
	}

	if len(rows) > 0 {
		lines = append(lines, "")
		lines = append(lines, renderCodexToolInputRows(rows, state.OptionIndex, question.MultiSelect, width, descriptions)...)
	}

	// The composer only appears once it is the active answer, so a question with
	// options is not padded with an empty input the user is not editing yet.
	if allowsText && (composerFocused || strings.TrimSpace(m.codexInput.Value()) != "") {
		lines = append(lines, "")
		label := "Your answer"
		if len(rows) > 0 {
			label = "Your own answer"
		}
		if composerFocused {
			lines = append(lines, detailSectionStyle.Render(label))
		} else {
			lines = append(lines, detailMutedStyle.Render(label))
		}
		input := m.codexInput
		input.SetWidth(max(12, width-2))
		input.SetHeight(max(1, min(6, input.LineCount())))
		if !composerFocused {
			input.Blur()
		}
		lines = append(lines, input.View())
	}

	lines = append(lines, "")
	if submitting {
		lines = append(lines, renderWrappedDialogTextLines(
			detailMutedStyle,
			width,
			"Sending your answer to "+providerLabel+"...",
		)...)
		return strings.Join(lines, "\n")
	}
	lines = append(lines, renderCodexElicitationActionLines(
		width,
		codexToolInputDialogActions(request, question, rows, composerFocused, allowsText),
	)...)
	return strings.Join(lines, "\n")
}

func codexToolInputDialogActions(
	request codexapp.ToolInputRequest,
	question codexapp.ToolInputQuestion,
	rows []codexToolInputRow,
	composerFocused bool,
	allowsText bool,
) []string {
	actions := []string{}
	switch {
	case composerFocused:
		actions = append(actions,
			renderDialogAction("Enter", "send answer", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("alt+enter", "newline", pushActionKeyStyle, pushActionTextStyle),
		)
		if len(rows) > 0 {
			actions = append(actions, renderDialogAction("Esc", "back to options", cancelActionKeyStyle, cancelActionTextStyle))
		}
	case question.MultiSelect:
		actions = append(actions,
			renderDialogAction("Enter", "submit", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Space", "toggle", pushActionKeyStyle, pushActionTextStyle),
			renderDialogAction("1-9", "toggle", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("up/down", "move", navigateActionKeyStyle, navigateActionTextStyle),
		)
	default:
		actions = append(actions,
			renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("1-9", "choose", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("up/down", "move", navigateActionKeyStyle, navigateActionTextStyle),
		)
	}
	if !composerFocused && allowsText {
		actions = append(actions, renderDialogAction("type", "own answer", navigateActionKeyStyle, navigateActionTextStyle))
	}
	if len(request.Questions) > 1 {
		actions = append(actions, renderDialogAction("Tab", "next question", navigateActionKeyStyle, navigateActionTextStyle))
	}
	actions = append(actions,
		renderDialogAction("alt+up", "hide", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("ctrl+c", "interrupt", cancelActionKeyStyle, cancelActionTextStyle),
	)
	return actions
}

func renderCodexToolInputRows(
	rows []codexToolInputRow,
	cursor int,
	multiSelect bool,
	width int,
	descriptions codexToolInputDescriptionMode,
) []string {
	out := make([]string, 0, len(rows)*2)
	for i, row := range rows {
		prefix := "  "
		if i == cursor {
			prefix = "> "
		}
		if row.Number > 0 {
			prefix += fmt.Sprintf("%d ", row.Number)
		}
		if multiSelect && !row.IsOther {
			if row.Checked {
				prefix += "[x] "
			} else {
				prefix += "[ ] "
			}
		}
		text := fitFooterWidth(prefix+row.Label, width)
		if i == cursor {
			out = append(out, dialogSelectedRowStyle.Width(width).Render(text))
		} else {
			out = append(out, commandPaletteRowStyle.Render(text))
		}
		description := strings.TrimSpace(row.Description)
		if description == "" || descriptions == codexToolInputDescriptionsNone {
			continue
		}
		if descriptions == codexToolInputDescriptionsSelected && i != cursor {
			continue
		}
		indent := "     "
		for _, line := range renderWrappedDialogTextLines(detailMutedStyle, max(8, width-len(indent)), description) {
			out = append(out, detailMutedStyle.Render(indent)+line)
		}
	}
	return out
}

func (m Model) updateCodexToolInputMode(snapshot codexapp.Snapshot, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	request, ok := codexToolInputQuestionsFromSnapshot(snapshot)
	if !ok {
		return m, nil
	}
	if m.codexToolInputSubmitPending(request.ID) {
		// The answer is already on its way; repeat activation would send it twice.
		return m, nil
	}
	state := m.ensureToolAnswerState(m.codexVisibleProject, request)
	question := request.Questions[state.QuestionIndex]
	rows := codexToolInputRowsFor(question, state.Answers[question.ID])
	composerFocused := codexToolInputComposerFocused(rows, state.OptionIndex)
	allowsText := codexToolInputAllowsText(question, rows)

	var focusCmd tea.Cmd
	if allowsText {
		if handled, cmd := m.tryHandleCodexPaste(msg, false); handled {
			if !composerFocused {
				// Pasting is another way into the free-text answer.
				state.OptionIndex = max(0, len(rows)-1)
				m.codexToolAnswers[m.codexVisibleProject] = state
				focusCmd = m.codexInput.Focus()
			}
			return m, batchCmds(focusCmd, cmd)
		}
	}

	switch msg.String() {
	case "tab", "shift+tab":
		if len(request.Questions) < 2 {
			return m, nil
		}
		delta := 1
		notice := "Moved to the next structured question"
		if msg.String() == "shift+tab" {
			delta = -1
			notice = "Moved to the previous structured question"
		}
		state.QuestionIndex = (state.QuestionIndex + delta + len(request.Questions)) % len(request.Questions)
		state.OptionIndex = codexToolInputCursorForAnswers(request.Questions[state.QuestionIndex], state.Answers)
		m.codexToolAnswers[m.codexVisibleProject] = state
		m.status = notice
		return m, nil
	case "esc":
		if composerFocused && len(rows) > 0 {
			state.OptionIndex = 0
			m.codexToolAnswers[m.codexVisibleProject] = state
			m.status = "Back to the listed options"
			return m, nil
		}
		return m.hideCodexSession()
	case "up", "ctrl+p":
		if composerFocused && strings.TrimSpace(m.codexInput.Value()) != "" {
			break
		}
		if len(rows) == 0 {
			break
		}
		state.OptionIndex = (state.OptionIndex - 1 + len(rows)) % len(rows)
		m.codexToolAnswers[m.codexVisibleProject] = state
		return m, m.codexInput.Focus()
	case "down", "ctrl+n":
		if composerFocused && strings.TrimSpace(m.codexInput.Value()) != "" {
			break
		}
		if len(rows) == 0 {
			break
		}
		state.OptionIndex = (state.OptionIndex + 1) % len(rows)
		m.codexToolAnswers[m.codexVisibleProject] = state
		return m, m.codexInput.Focus()
	case " ":
		if composerFocused {
			break
		}
		if !question.MultiSelect || state.OptionIndex >= len(rows) {
			// Space is the multi-select toggle; elsewhere it must not leak into
			// the free-text answer the user has not focused yet.
			return m, nil
		}
		return m.toggleCodexToolInputRow(request, state, question, rows[state.OptionIndex])
	case "enter":
		return m.submitCodexToolInputAnswer(request, state, question, rows, composerFocused)
	case "backspace", "delete":
		if !composerFocused {
			return m, nil
		}
	default:
		if optionIndex, ok := numericOptionSelection(msg.String()); ok && !composerFocused && optionIndex < len(rows) {
			return m.chooseCodexToolInputRow(request, state, question, rows, optionIndex)
		}
		if !composerFocused {
			if !codexToolInputTypedRune(msg) {
				return m, nil
			}
			if !allowsText {
				m.status = "This question only accepts the listed options"
				return m, nil
			}
			// Typing is the shortcut into the free-text row.
			state.OptionIndex = max(0, len(rows)-1)
			m.codexToolAnswers[m.codexVisibleProject] = state
			focusCmd = m.codexInput.Focus()
		}
	}

	if !allowsText {
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
	return m, batchCmds(focusCmd, cmd)
}

// codexToolInputTypedRune reports whether the key would insert text, so typing
// can move focus into the free-text row without swallowing shortcuts.
func codexToolInputTypedRune(msg tea.KeyMsg) bool {
	if msg.Type != tea.KeyRunes || msg.Alt {
		return false
	}
	return len(msg.Runes) > 0
}

func (m Model) chooseCodexToolInputRow(
	request *codexapp.ToolInputRequest,
	state codexToolAnswerState,
	question codexapp.ToolInputQuestion,
	rows []codexToolInputRow,
	rowIndex int,
) (tea.Model, tea.Cmd) {
	row := rows[rowIndex]
	if row.IsOther {
		state.OptionIndex = rowIndex
		m.codexToolAnswers[m.codexVisibleProject] = state
		m.status = "Type your own answer, then press Enter to send it"
		return m, m.codexInput.Focus()
	}
	state.OptionIndex = rowIndex
	if question.MultiSelect {
		return m.toggleCodexToolInputRow(request, state, question, row)
	}
	state.Answers[question.ID] = []string{row.Answer}
	m.codexToolAnswers[m.codexVisibleProject] = state
	m.clearCodexDraft(m.codexVisibleProject)
	return m.finishOrAdvanceToolInput(request, state)
}

func (m Model) toggleCodexToolInputRow(
	request *codexapp.ToolInputRequest,
	state codexToolAnswerState,
	question codexapp.ToolInputQuestion,
	row codexToolInputRow,
) (tea.Model, tea.Cmd) {
	state.Answers[question.ID] = toggleToolInputAnswer(state.Answers[question.ID], row.Answer)
	m.codexToolAnswers[m.codexVisibleProject] = state
	m.status = "Selection toggled. Choose more options or press Enter to submit."
	return m, nil
}

func (m Model) submitCodexToolInputAnswer(
	request *codexapp.ToolInputRequest,
	state codexToolAnswerState,
	question codexapp.ToolInputQuestion,
	rows []codexToolInputRow,
	composerFocused bool,
) (tea.Model, tea.Cmd) {
	typed := strings.TrimSpace(m.codexInput.Value())
	if composerFocused && typed != "" {
		if question.MultiSelect {
			state.Answers[question.ID] = addToolInputAnswer(state.Answers[question.ID], typed)
		} else {
			state.Answers[question.ID] = []string{typed}
		}
		m.codexToolAnswers[m.codexVisibleProject] = state
		m.clearCodexDraft(m.codexVisibleProject)
		return m.finishOrAdvanceToolInput(request, state)
	}
	if !question.MultiSelect && !composerFocused && state.OptionIndex < len(rows) {
		return m.chooseCodexToolInputRow(request, state, question, rows, state.OptionIndex)
	}
	if len(nonEmptyToolInputAnswers(state.Answers[question.ID])) == 0 {
		switch {
		case question.MultiSelect && len(rows) > 0:
			m.status = "Select at least one option before submitting"
		case len(rows) > 0:
			m.status = "Choose an option or type your own answer"
		default:
			m.status = "Type an answer before sending it"
		}
		return m, nil
	}
	m.codexToolAnswers[m.codexVisibleProject] = state
	m.clearCodexDraft(m.codexVisibleProject)
	return m.finishOrAdvanceToolInput(request, state)
}

func (m Model) finishOrAdvanceToolInput(request *codexapp.ToolInputRequest, state codexToolAnswerState) (tea.Model, tea.Cmd) {
	nextIndex := firstUnansweredToolQuestion(request, state.Answers)
	m.codexToolAnswers[m.codexVisibleProject] = state
	if nextIndex < len(request.Questions) {
		state.QuestionIndex = nextIndex
		state.OptionIndex = codexToolInputCursorForAnswers(request.Questions[nextIndex], state.Answers)
		m.codexToolAnswers[m.codexVisibleProject] = state
		m.status = "Answer recorded. Continue with the next structured question."
		return m, m.codexInput.Focus()
	}
	answers := state.Answers
	delete(m.codexToolAnswers, m.codexVisibleProject)
	m.status = "Sending structured input..."
	cmd := m.respondVisibleToolInputCmd(answers)
	if cmd == nil {
		return m, nil
	}
	projectPath, requestID := m.codexVisibleProject, strings.TrimSpace(request.ID)
	m.codexToolInputSubmitting = requestID
	m.codexToolInputSubmitProject = projectPath
	return m, func() tea.Msg {
		return codexToolInputSubmittedMsg{ProjectPath: projectPath, RequestID: requestID, Result: cmd()}
	}
}
