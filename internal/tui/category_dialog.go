package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/model"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type categoryDialogMode uint8

const (
	categoryDialogModeActions categoryDialogMode = iota
	categoryDialogModeMove
	categoryDialogModeRemove
	categoryDialogModeCreate
)

type categoryDialogAction uint8

const (
	categoryDialogActionMove categoryDialogAction = iota
	categoryDialogActionClear
	categoryDialogActionCreate
	categoryDialogActionRemove
)

type categoryDialogState struct {
	Mode     categoryDialogMode
	Selected int
	Input    textinput.Model
}

type categoryDialogActionChoice struct {
	Action  categoryDialogAction
	Label   string
	Summary string
}

func newCategoryNameInput() textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "category name"
	input.CharLimit = 48
	input.Width = 44
	return input
}

func (m Model) openCategoryDialog() (tea.Model, tea.Cmd) {
	m.categoryDialog = &categoryDialogState{
		Mode:     categoryDialogModeActions,
		Selected: 0,
		Input:    newCategoryNameInput(),
	}
	m.commandMode = false
	m.showHelp = false
	m.err = nil
	m.status = "Category operations open"
	return m, nil
}

func (m *Model) closeCategoryDialog(status string) {
	if m.categoryDialog != nil {
		m.categoryDialog.Input.Blur()
	}
	m.categoryDialog = nil
	if status != "" {
		m.status = status
	}
}

func (m Model) updateCategoryDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.categoryDialog
	if dialog == nil {
		return m, nil
	}

	switch dialog.Mode {
	case categoryDialogModeCreate:
		return m.updateCategoryCreateMode(msg)
	case categoryDialogModeMove, categoryDialogModeRemove:
		return m.updateCategoryPickerMode(msg)
	default:
		return m.updateCategoryActionMode(msg)
	}
}

func (m Model) updateCategoryActionMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	choices := m.categoryDialogActions()
	if len(choices) == 0 {
		m.closeCategoryDialog("No category actions available")
		return m, nil
	}
	m.clampCategoryDialogSelection(len(choices))

	switch msg.String() {
	case "esc":
		m.closeCategoryDialog("Category operations closed")
		return m, nil
	case "up", "k":
		m.moveCategoryDialogSelection(-1, len(choices))
		return m, nil
	case "down", "j", "tab":
		m.moveCategoryDialogSelection(1, len(choices))
		return m, nil
	case "shift+tab":
		m.moveCategoryDialogSelection(-1, len(choices))
		return m, nil
	case "enter":
		choice := choices[m.categoryDialog.Selected]
		switch choice.Action {
		case categoryDialogActionMove:
			if len(m.projectCategories) == 0 {
				m.status = "Create a category before moving projects"
				return m, nil
			}
			m.categoryDialog.Mode = categoryDialogModeMove
			m.categoryDialog.Selected = 0
			m.status = "Choose a category"
			return m, nil
		case categoryDialogActionClear:
			return m.submitCategoryDialogMove("")
		case categoryDialogActionCreate:
			m.categoryDialog.Mode = categoryDialogModeCreate
			m.categoryDialog.Input = newCategoryNameInput()
			m.status = "Name the new category"
			return m, m.categoryDialog.Input.Focus()
		case categoryDialogActionRemove:
			if len(m.projectCategories) == 0 {
				m.status = "No categories to remove"
				return m, nil
			}
			m.categoryDialog.Mode = categoryDialogModeRemove
			m.categoryDialog.Selected = 0
			m.status = "Choose a category to remove"
			return m, nil
		}
	}
	return m, nil
}

func (m Model) updateCategoryPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.projectCategories) == 0 {
		m.categoryDialog.Mode = categoryDialogModeActions
		m.categoryDialog.Selected = 0
		m.status = "No categories available"
		return m, nil
	}
	m.clampCategoryDialogSelection(len(m.projectCategories))

	switch msg.String() {
	case "esc":
		m.categoryDialog.Mode = categoryDialogModeActions
		m.categoryDialog.Selected = 0
		m.status = "Category operations open"
		return m, nil
	case "up", "k":
		m.moveCategoryDialogSelection(-1, len(m.projectCategories))
		return m, nil
	case "down", "j", "tab":
		m.moveCategoryDialogSelection(1, len(m.projectCategories))
		return m, nil
	case "shift+tab":
		m.moveCategoryDialogSelection(-1, len(m.projectCategories))
		return m, nil
	case "enter":
		category := m.projectCategories[m.categoryDialog.Selected]
		switch m.categoryDialog.Mode {
		case categoryDialogModeMove:
			return m.submitCategoryDialogMove(category.Name)
		case categoryDialogModeRemove:
			m.closeCategoryDialog("")
			m.status = fmt.Sprintf("Removing category %q...", strings.TrimSpace(category.Name))
			return m, m.removeProjectCategoryCmd(category.Name)
		}
	}
	return m, nil
}

func (m Model) updateCategoryCreateMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.categoryDialog
	if dialog == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		dialog.Mode = categoryDialogModeActions
		dialog.Selected = 0
		dialog.Input.Blur()
		m.status = "Category operations open"
		return m, nil
	case "enter":
		name := strings.TrimSpace(dialog.Input.Value())
		if name == "" {
			m.status = "Category name is required"
			return m, nil
		}
		m.closeCategoryDialog("")
		m.status = fmt.Sprintf("Creating category %q...", name)
		return m, m.createProjectCategoryCmd(name)
	}
	var cmd tea.Cmd
	dialog.Input, cmd = dialog.Input.Update(msg)
	return m, cmd
}

func (m Model) submitCategoryDialogMove(categoryName string) (tea.Model, tea.Cmd) {
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return m, nil
	}
	categoryName = strings.TrimSpace(categoryName)
	targetCategoryID := ""
	if categoryName != "" {
		if category, ok := m.projectCategoryByName(categoryName); ok {
			targetCategoryID = category.ID
		}
	}
	selectPath := m.categoryMoveSelectPath(project, targetCategoryID)
	m.closeCategoryDialog("")
	if categoryName == "" {
		m.status = "Moving selected item to Main..."
	} else {
		m.status = fmt.Sprintf("Moving selected item to %s...", categoryName)
	}
	if task, ok := m.agentTaskForProjectPath(project.Path); ok {
		return m, m.moveAgentTaskCategoryCmd(task, categoryName, selectPath)
	}
	return m, m.moveProjectCategoryCmd(project, categoryName, selectPath)
}

func (m Model) categoryMoveSelectPath(project model.ProjectSummary, targetCategoryID string) string {
	if project.Archived || m.archiveMode == projectArchiveArchived {
		return m.nextProjectSelectionPathAfter(project.Path)
	}
	if strings.TrimSpace(project.CategoryID) == strings.TrimSpace(targetCategoryID) {
		return ""
	}
	return m.nextProjectSelectionPathAfter(project.Path)
}

func (m Model) categoryDialogActions() []categoryDialogActionChoice {
	return []categoryDialogActionChoice{
		{Action: categoryDialogActionMove, Label: "Move selected", Summary: "Send the selected item to a custom category"},
		{Action: categoryDialogActionClear, Label: "Move selected to Main", Summary: "Clear the selected item's custom category"},
		{Action: categoryDialogActionCreate, Label: "Create category", Summary: "Add a new custom project-list tab"},
		{Action: categoryDialogActionRemove, Label: "Remove category", Summary: "Delete a custom tab and move its items back to Main"},
	}
}

func (m *Model) moveCategoryDialogSelection(delta, total int) {
	if m.categoryDialog == nil || total <= 0 || delta == 0 {
		return
	}
	m.categoryDialog.Selected += delta
	if m.categoryDialog.Selected < 0 {
		m.categoryDialog.Selected = total - 1
	}
	if m.categoryDialog.Selected >= total {
		m.categoryDialog.Selected = 0
	}
}

func (m *Model) clampCategoryDialogSelection(total int) {
	if m.categoryDialog == nil {
		return
	}
	if total <= 0 {
		m.categoryDialog.Selected = 0
		return
	}
	if m.categoryDialog.Selected < 0 {
		m.categoryDialog.Selected = 0
	}
	if m.categoryDialog.Selected >= total {
		m.categoryDialog.Selected = total - 1
	}
}

func (m Model) renderCategoryDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderCategoryDialogPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCategoryDialogPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(54, bodyW-10), 86))
	panelInnerWidth := max(28, panelWidth-4)
	maxContentHeight := max(10, bodyH-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderCategoryDialogContent(panelInnerWidth, maxContentHeight))
}

func (m Model) renderCategoryDialogContent(width, maxHeight int) string {
	if m.categoryDialog == nil {
		return ""
	}
	switch m.categoryDialog.Mode {
	case categoryDialogModeMove:
		return m.renderCategoryPickerContent("Move To Category", "Enter move  Esc back", width, maxHeight)
	case categoryDialogModeRemove:
		return m.renderCategoryPickerContent("Remove Category", "Enter remove  Esc back", width, maxHeight)
	case categoryDialogModeCreate:
		return m.renderCategoryCreateContent(width)
	default:
		return m.renderCategoryActionContent(width)
	}
}

func (m Model) renderCategoryActionContent(width int) string {
	lines := []string{
		commandPaletteTitleStyle.Render("Categories"),
		commandPaletteHintStyle.Render("Enter choose  Esc close"),
	}
	if p, ok := m.selectedProject(); ok {
		lines = append(lines, commandPaletteHintStyle.Render("Selected project: "+truncateText(projectTitle(p.Path, p.Name), width-18)))
	} else {
		lines = append(lines, commandPaletteHintStyle.Render("Selected project: none"))
	}
	lines = append(lines, "")
	choices := m.categoryDialogActions()
	selected := clampedCategorySelection(m.categoryDialog.Selected, len(choices))
	for i, choice := range choices {
		lines = append(lines, renderCategoryDialogRow(choice.Label, choice.Summary, i == selected, width))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderCategoryPickerContent(title, hint string, width, maxHeight int) string {
	lines := []string{
		commandPaletteTitleStyle.Render(title),
		commandPaletteHintStyle.Render(hint),
		"",
	}
	if len(m.projectCategories) == 0 {
		lines = append(lines, detailMutedStyle.Render("No categories yet."))
		return strings.Join(lines, "\n")
	}
	selected := clampedCategorySelection(m.categoryDialog.Selected, len(m.projectCategories))
	limit := max(3, min(len(m.projectCategories), maxHeight-5))
	start := 0
	if selected >= limit {
		start = selected - limit + 1
	}
	end := min(len(m.projectCategories), start+limit)
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d above", start)))
	}
	for i := start; i < end; i++ {
		category := m.projectCategories[i]
		lines = append(lines, renderCategoryDialogRow(category.Name, "", i == selected, width))
	}
	if end < len(m.projectCategories) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d below", len(m.projectCategories)-end)))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderCategoryCreateContent(width int) string {
	input := m.categoryDialog.Input
	input.Width = max(12, width-2)
	lines := []string{
		commandPaletteTitleStyle.Render("Create Category"),
		commandPaletteHintStyle.Render("Enter create  Esc back"),
		"",
		input.View(),
	}
	return strings.Join(lines, "\n")
}

func clampedCategorySelection(selected, total int) int {
	if total <= 0 {
		return 0
	}
	if selected < 0 {
		return 0
	}
	if selected >= total {
		return total - 1
	}
	return selected
}

func renderCategoryDialogRow(label, summary string, selected bool, width int) string {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "(unnamed)"
	}
	summary = strings.TrimSpace(summary)
	maxLabel := max(16, min(30, width/2))
	left := truncateText(label, maxLabel)
	row := left
	if summary != "" {
		row += "  " + truncateText(summary, max(10, width-maxLabel-2))
	}
	if selected {
		return dialogSelectedRowStyle.Width(width).Render(row)
	}
	if summary != "" {
		return commandPaletteRowStyle.Width(width).Render(commandPalettePickStyle.Render(left) + "  " + detailMutedStyle.Render(truncateText(summary, max(10, width-maxLabel-2))))
	}
	return commandPaletteRowStyle.Width(width).Render(commandPalettePickStyle.Render(left))
}
