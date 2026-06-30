package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"lcroom/internal/model"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type categoryDialogMode uint8

const (
	categoryDialogModeActions categoryDialogMode = iota
	categoryDialogModeMoveItems
	categoryDialogModeMoveDestination
	categoryDialogModePrivacy
	categoryDialogModeRemove
	categoryDialogModeCreate
)

type categoryDialogAction uint8

const (
	categoryDialogActionMove categoryDialogAction = iota
	categoryDialogActionCreate
	categoryDialogActionPrivacy
	categoryDialogActionRemove
)

type categoryDialogState struct {
	Mode      categoryDialogMode
	Selected  int
	Input     textinput.Model
	MoveItems []categoryMoveItem
	Marked    map[string]bool
}

type categoryDialogActionChoice struct {
	Action  categoryDialogAction
	Label   string
	Summary string
}

type categoryMoveItem struct {
	Key        string
	Label      string
	Summary    string
	Current    bool
	Resource   model.CategoryResourceRef
	CategoryID string
	Archived   bool
}

func newCategoryNameInput() textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "category name"
	input.CharLimit = 48
	input.Width = 44
	return input
}

func newCategoryFilterInput() textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "filter items"
	input.Width = 44
	return input
}

func (m Model) openCategoryDialog() (tea.Model, tea.Cmd) {
	m.categoryDialog = &categoryDialogState{
		Mode:     categoryDialogModeActions,
		Selected: 0,
		Input:    newCategoryNameInput(),
		Marked:   map[string]bool{},
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
	case categoryDialogModeMoveItems:
		return m.updateCategoryMoveItemsMode(msg)
	case categoryDialogModeMoveDestination:
		return m.updateCategoryMoveDestinationMode(msg)
	case categoryDialogModePrivacy, categoryDialogModeRemove:
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
			return m.openCategoryMoveItemsMode()
		case categoryDialogActionCreate:
			m.categoryDialog.Mode = categoryDialogModeCreate
			m.categoryDialog.Selected = 0
			m.categoryDialog.Input = newCategoryNameInput()
			m.status = "Name the new category"
			return m, m.categoryDialog.Input.Focus()
		case categoryDialogActionPrivacy:
			if len(m.projectCategories) == 0 {
				m.status = "No categories available"
				return m, nil
			}
			m.categoryDialog.Mode = categoryDialogModePrivacy
			m.categoryDialog.Selected = 0
			m.status = "Choose a category to mark private or public"
			return m, nil
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

func (m Model) openCategoryMoveItemsMode() (tea.Model, tea.Cmd) {
	items, currentKey := m.categoryMoveItems()
	if len(items) == 0 {
		m.status = "No category items available"
		return m, nil
	}
	input := newCategoryFilterInput()
	m.categoryDialog.Mode = categoryDialogModeMoveItems
	m.categoryDialog.Input = input
	m.categoryDialog.MoveItems = items
	m.categoryDialog.Marked = map[string]bool{}
	m.categoryDialog.Selected = 0
	if currentKey != "" {
		m.categoryDialog.Marked[currentKey] = true
		for i, item := range items {
			if item.Key == currentKey {
				m.categoryDialog.Selected = i
				break
			}
		}
	}
	m.status = "Mark items to move"
	return m, m.categoryDialog.Input.Focus()
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
		case categoryDialogModePrivacy:
			m.closeCategoryDialog("")
			private := !category.Private
			if private {
				m.status = fmt.Sprintf("Marking category %q private...", strings.TrimSpace(category.Name))
			} else {
				m.status = fmt.Sprintf("Marking category %q public...", strings.TrimSpace(category.Name))
			}
			return m, m.setProjectCategoryPrivacyCmd(category.Name, private)
		case categoryDialogModeRemove:
			m.closeCategoryDialog("")
			m.status = fmt.Sprintf("Removing category %q...", strings.TrimSpace(category.Name))
			return m, m.removeProjectCategoryCmd(category.Name)
		}
	}
	return m, nil
}

func (m Model) updateCategoryMoveItemsMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.categoryDialog
	if dialog == nil {
		return m, nil
	}
	indexes := m.categoryFilteredMoveItemIndexes()
	m.clampCategoryDialogSelection(len(indexes))

	switch msg.String() {
	case "esc":
		dialog.Mode = categoryDialogModeActions
		dialog.Selected = 0
		dialog.Input.Blur()
		m.status = "Category operations open"
		return m, nil
	case "up", "k":
		m.moveCategoryDialogSelection(-1, len(indexes))
		return m, nil
	case "down", "j", "tab":
		m.moveCategoryDialogSelection(1, len(indexes))
		return m, nil
	case "shift+tab":
		m.moveCategoryDialogSelection(-1, len(indexes))
		return m, nil
	case " ":
		if len(indexes) == 0 {
			return m, nil
		}
		item := dialog.MoveItems[indexes[dialog.Selected]]
		if dialog.Marked[item.Key] {
			delete(dialog.Marked, item.Key)
		} else {
			dialog.Marked[item.Key] = true
		}
		m.status = fmt.Sprintf("%d marked", m.categoryMarkedMoveCount())
		return m, nil
	case "enter":
		if m.categoryMarkedMoveCount() == 0 {
			m.status = "Mark at least one item"
			return m, nil
		}
		dialog.Mode = categoryDialogModeMoveDestination
		dialog.Selected = m.categoryDefaultDestinationSelection()
		dialog.Input.Blur()
		m.status = "Choose the destination category"
		return m, nil
	}

	var cmd tea.Cmd
	dialog.Input, cmd = dialog.Input.Update(msg)
	m.clampCategoryDialogSelection(len(m.categoryFilteredMoveItemIndexes()))
	return m, cmd
}

func (m Model) updateCategoryMoveDestinationMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	total := 1 + len(m.projectCategories)
	m.clampCategoryDialogSelection(total)

	switch msg.String() {
	case "esc":
		m.categoryDialog.Mode = categoryDialogModeMoveItems
		m.categoryDialog.Selected = 0
		m.status = "Mark items to move"
		return m, m.categoryDialog.Input.Focus()
	case "up", "k":
		m.moveCategoryDialogSelection(-1, total)
		return m, nil
	case "down", "j", "tab":
		m.moveCategoryDialogSelection(1, total)
		return m, nil
	case "shift+tab":
		m.moveCategoryDialogSelection(-1, total)
		return m, nil
	case "enter":
		categoryName, targetCategoryID := m.categoryDestinationSelection()
		resources := m.categoryMarkedMoveResources()
		if len(resources) == 0 {
			m.status = "Mark at least one item"
			return m, nil
		}
		selectPath := m.categoryMoveSelectPathForResources(resources, targetCategoryID)
		m.closeCategoryDialog("")
		if categoryName == "" {
			m.status = fmt.Sprintf("Moving %d item(s) to Main...", len(resources))
		} else {
			m.status = fmt.Sprintf("Moving %d item(s) to %s...", len(resources), categoryName)
		}
		return m, m.moveCategoryResourcesCmd(resources, categoryName, selectPath)
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

func (m Model) categoryMoveSelectPathForResources(resources []model.CategoryResourceRef, targetCategoryID string) string {
	project, ok := m.selectedProject()
	if !ok {
		return ""
	}
	selectedRef := m.categoryResourceForSelectedProject(project)
	selectedKey := categoryMoveResourceKey(selectedRef)
	for _, resource := range resources {
		if categoryMoveResourceKey(resource) == selectedKey {
			return m.categoryMoveSelectPath(project, targetCategoryID)
		}
	}
	return ""
}

func (m Model) categoryResourceForSelectedProject(project model.ProjectSummary) model.CategoryResourceRef {
	if task, ok := m.agentTaskForProjectPath(project.Path); ok {
		return model.CategoryResourceRef{Kind: model.CategoryResourceAgentTask, ID: strings.TrimSpace(task.ID)}
	}
	return model.CategoryResourceRef{Kind: model.CategoryResourceProject, ID: filepath.Clean(strings.TrimSpace(project.Path))}
}

func (m Model) categoryDialogActions() []categoryDialogActionChoice {
	return []categoryDialogActionChoice{
		{Action: categoryDialogActionMove, Label: "Move to category...", Summary: "Mark one or more items, then choose Main or a category"},
		{Action: categoryDialogActionCreate, Label: "Create category", Summary: "Add a custom project-list tab"},
		{Action: categoryDialogActionPrivacy, Label: "Category privacy...", Summary: "Mark a category private or public"},
		{Action: categoryDialogActionRemove, Label: "Remove category", Summary: "Delete a custom tab and move its items back to Main"},
	}
}

func (m Model) categoryMoveItems() ([]categoryMoveItem, string) {
	selectedPath := ""
	if project, ok := m.selectedProject(); ok {
		selectedPath = filepath.Clean(strings.TrimSpace(project.Path))
	}
	currentAgentTaskID := ""
	if selectedPath != "" && selectedPath != "." {
		if task, ok := m.agentTaskForProjectPath(selectedPath); ok {
			currentAgentTaskID = strings.TrimSpace(task.ID)
		}
	}

	items := []categoryMoveItem{}
	seen := map[string]struct{}{}
	add := func(item categoryMoveItem) {
		item.Key = categoryMoveResourceKey(item.Resource)
		if item.Key == "" {
			return
		}
		if _, ok := seen[item.Key]; ok {
			return
		}
		seen[item.Key] = struct{}{}
		items = append(items, item)
	}

	for _, project := range append(append([]model.ProjectSummary(nil), m.allProjects...), m.archivedProjects...) {
		path := filepath.Clean(strings.TrimSpace(project.Path))
		if path == "" || path == "." {
			continue
		}
		current := currentAgentTaskID == "" && selectedPath == path
		add(categoryMoveItem{
			Label:      projectTitle(path, project.Name),
			Summary:    categoryMoveItemSummary(project.CategoryName, project.Archived, project.CategoryPrivate),
			Current:    current,
			Resource:   model.CategoryResourceRef{Kind: model.CategoryResourceProject, ID: path},
			CategoryID: strings.TrimSpace(project.CategoryID),
			Archived:   project.Archived,
		})
	}
	for _, task := range m.openAgentTasks {
		if !agentTaskIsOpen(task) {
			continue
		}
		taskID := strings.TrimSpace(task.ID)
		if taskID == "" {
			continue
		}
		label := strings.TrimSpace(task.Title)
		if label == "" {
			label = taskID
		}
		current := currentAgentTaskID != "" && currentAgentTaskID == taskID
		add(categoryMoveItem{
			Label:      label,
			Summary:    categoryMoveItemSummary(task.CategoryName, false, task.CategoryPrivate),
			Current:    current,
			Resource:   model.CategoryResourceRef{Kind: model.CategoryResourceAgentTask, ID: taskID},
			CategoryID: strings.TrimSpace(task.CategoryID),
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(items[i].Label))
		right := strings.ToLower(strings.TrimSpace(items[j].Label))
		if left != right {
			return left < right
		}
		return items[i].Key < items[j].Key
	})

	currentKey := ""
	for _, item := range items {
		if item.Current {
			currentKey = item.Key
			break
		}
	}
	return items, currentKey
}

func categoryMoveItemSummary(categoryName string, archived, private bool) string {
	parts := []string{}
	categoryName = strings.TrimSpace(categoryName)
	if categoryName == "" {
		parts = append(parts, "Main")
	} else {
		parts = append(parts, categoryName)
	}
	if archived {
		parts = append(parts, "archived")
	}
	if private {
		parts = append(parts, "private")
	}
	return strings.Join(parts, " - ")
}

func categoryMoveResourceKey(resource model.CategoryResourceRef) string {
	kind := model.NormalizeCategoryResourceKind(resource.Kind)
	id := strings.TrimSpace(resource.ID)
	if kind == model.CategoryResourceProject {
		id = filepath.Clean(id)
		if id == "." {
			id = ""
		}
	}
	if kind == "" || id == "" {
		return ""
	}
	return string(kind) + ":" + id
}

func (m Model) categoryFilteredMoveItemIndexes() []int {
	if m.categoryDialog == nil {
		return nil
	}
	filter := strings.ToLower(strings.TrimSpace(m.categoryDialog.Input.Value()))
	indexes := make([]int, 0, len(m.categoryDialog.MoveItems))
	for i, item := range m.categoryDialog.MoveItems {
		if filter == "" ||
			strings.Contains(strings.ToLower(item.Label), filter) ||
			strings.Contains(strings.ToLower(item.Summary), filter) {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func (m Model) categoryMarkedMoveCount() int {
	if m.categoryDialog == nil {
		return 0
	}
	count := 0
	for _, item := range m.categoryDialog.MoveItems {
		if m.categoryDialog.Marked[item.Key] {
			count++
		}
	}
	return count
}

func (m Model) categoryMarkedMoveResources() []model.CategoryResourceRef {
	if m.categoryDialog == nil {
		return nil
	}
	resources := make([]model.CategoryResourceRef, 0, len(m.categoryDialog.Marked))
	for _, item := range m.categoryDialog.MoveItems {
		if m.categoryDialog.Marked[item.Key] {
			resources = append(resources, item.Resource)
		}
	}
	return resources
}

func (m Model) categoryDefaultDestinationSelection() int {
	if m.categoryDialog == nil {
		return 0
	}
	for _, item := range m.categoryDialog.MoveItems {
		if !m.categoryDialog.Marked[item.Key] {
			continue
		}
		if strings.TrimSpace(item.CategoryID) != "" {
			return 0
		}
		if len(m.projectCategories) > 0 {
			return 1
		}
		return 0
	}
	return 0
}

func (m Model) categoryDestinationSelection() (string, string) {
	if m.categoryDialog == nil || m.categoryDialog.Selected <= 0 {
		return "", ""
	}
	idx := m.categoryDialog.Selected - 1
	if idx < 0 || idx >= len(m.projectCategories) {
		return "", ""
	}
	category := m.projectCategories[idx]
	return strings.TrimSpace(category.Name), strings.TrimSpace(category.ID)
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
	panelWidth := min(bodyW, min(max(60, bodyW-10), 96))
	panelInnerWidth := max(28, panelWidth-4)
	maxContentHeight := max(10, bodyH-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderCategoryDialogContent(panelInnerWidth, maxContentHeight))
}

func (m Model) renderCategoryDialogContent(width, maxHeight int) string {
	if m.categoryDialog == nil {
		return ""
	}
	switch m.categoryDialog.Mode {
	case categoryDialogModeMoveItems:
		return m.renderCategoryMoveItemsContent(width, maxHeight)
	case categoryDialogModeMoveDestination:
		return m.renderCategoryMoveDestinationContent(width, maxHeight)
	case categoryDialogModePrivacy:
		return m.renderCategoryPickerContent("Category Privacy", "Enter toggle  Esc back", width, maxHeight, true)
	case categoryDialogModeRemove:
		return m.renderCategoryPickerContent("Remove Category", "Enter remove  Esc back", width, maxHeight, false)
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

func (m Model) renderCategoryMoveItemsContent(width, maxHeight int) string {
	dialog := m.categoryDialog
	if dialog == nil {
		return ""
	}
	filter := dialog.Input
	filter.Width = max(12, width-2)
	indexes := m.categoryFilteredMoveItemIndexes()
	selected := clampedCategorySelection(dialog.Selected, len(indexes))
	marked := m.categoryMarkedMoveCount()
	lines := []string{
		commandPaletteTitleStyle.Render("Move To Category"),
		commandPaletteHintStyle.Render(fmt.Sprintf("Space mark  Enter destination  Esc back  %d marked", marked)),
		filter.View(),
		"",
	}
	if len(indexes) == 0 {
		lines = append(lines, detailMutedStyle.Render("No items match."))
		return strings.Join(lines, "\n")
	}
	limit := max(3, min(len(indexes), maxHeight-7))
	start := 0
	if selected >= limit {
		start = selected - limit + 1
	}
	end := min(len(indexes), start+limit)
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("up %d above", start)))
	}
	for visibleIdx := start; visibleIdx < end; visibleIdx++ {
		item := dialog.MoveItems[indexes[visibleIdx]]
		lines = append(lines, renderCategoryMoveItemRow(item, dialog.Marked[item.Key], visibleIdx == selected, width))
	}
	if end < len(indexes) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("down %d below", len(indexes)-end)))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderCategoryMoveDestinationContent(width, maxHeight int) string {
	lines := []string{
		commandPaletteTitleStyle.Render("Destination"),
		commandPaletteHintStyle.Render(fmt.Sprintf("Enter move %d marked  Esc back", m.categoryMarkedMoveCount())),
		"",
	}
	total := 1 + len(m.projectCategories)
	selected := clampedCategorySelection(m.categoryDialog.Selected, total)
	limit := max(3, min(total, maxHeight-5))
	start := 0
	if selected >= limit {
		start = selected - limit + 1
	}
	end := min(total, start+limit)
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("up %d above", start)))
	}
	for i := start; i < end; i++ {
		if i == 0 {
			lines = append(lines, renderCategoryDialogRow("Main", "Clear custom category", i == selected, width))
			continue
		}
		category := m.projectCategories[i-1]
		lines = append(lines, renderCategoryDialogRow(category.Name, categoryPrivacySummary(category), i == selected, width))
	}
	if end < total {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("down %d below", total-end)))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderCategoryPickerContent(title, hint string, width, maxHeight int, showPrivacy bool) string {
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
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("up %d above", start)))
	}
	for i := start; i < end; i++ {
		category := m.projectCategories[i]
		summary := ""
		if showPrivacy {
			summary = categoryPrivacySummary(category)
		}
		lines = append(lines, renderCategoryDialogRow(category.Name, summary, i == selected, width))
	}
	if end < len(m.projectCategories) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("down %d below", len(m.projectCategories)-end)))
	}
	return strings.Join(lines, "\n")
}

func categoryPrivacySummary(category model.ProjectCategory) string {
	if category.Private {
		return "private"
	}
	return "public"
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

func renderCategoryMoveItemRow(item categoryMoveItem, marked, selected bool, width int) string {
	mark := "[ ]"
	if marked {
		mark = "[x]"
	}
	label := strings.TrimSpace(item.Label)
	if label == "" {
		label = "(unnamed)"
	}
	summary := strings.TrimSpace(item.Summary)
	if item.Current {
		if summary == "" {
			summary = "current"
		} else {
			summary += " - current"
		}
	}
	maxLabel := max(16, min(36, width/2))
	left := mark + " " + truncateText(label, maxLabel)
	row := left
	if summary != "" {
		row += "  " + truncateText(summary, max(10, width-lipgloss.Width(left)-2))
	}
	if selected {
		return dialogSelectedRowStyle.Width(width).Render(row)
	}
	if summary != "" {
		return commandPaletteRowStyle.Width(width).Render(commandPalettePickStyle.Render(left) + "  " + detailMutedStyle.Render(truncateText(summary, max(10, width-lipgloss.Width(left)-2))))
	}
	return commandPaletteRowStyle.Width(width).Render(commandPalettePickStyle.Render(left))
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
