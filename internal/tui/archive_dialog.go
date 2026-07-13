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

type archiveDialogState struct {
	Selected int
	Input    textinput.Model
	Projects []archiveProjectItem
	Marked   map[string]bool
}

type archiveProjectItem struct {
	Key     string
	Label   string
	Summary string
	Current bool
	Project model.ProjectSummary
}

func newArchiveFilterInput() textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "filter projects"
	input.Width = 44
	return input
}

func (m Model) openArchiveDialog() (tea.Model, tea.Cmd) {
	items, currentKey := m.archiveProjectItems()
	if len(items) == 0 {
		m.status = "No active projects available to archive"
		return m, nil
	}
	m.archiveDialog = &archiveDialogState{
		Selected: 0,
		Input:    newArchiveFilterInput(),
		Projects: items,
		Marked:   map[string]bool{},
	}
	if currentKey != "" {
		m.archiveDialog.Marked[currentKey] = true
		for i, item := range items {
			if item.Key == currentKey {
				m.archiveDialog.Selected = i
				break
			}
		}
	}
	m.commandMode = false
	m.err = nil
	m.status = "Mark projects to archive"
	return m, m.archiveDialog.Input.Focus()
}

func (m *Model) closeArchiveDialog(status string) {
	if m.archiveDialog != nil {
		m.archiveDialog.Input.Blur()
	}
	m.archiveDialog = nil
	if status != "" {
		m.status = status
	}
}

func (m Model) updateArchiveDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.archiveDialog
	if dialog == nil {
		return m, nil
	}
	indexes := m.archiveFilteredProjectIndexes()
	m.clampArchiveDialogSelection(len(indexes))

	switch msg.String() {
	case "esc":
		m.closeArchiveDialog("Archive closed")
		return m, nil
	case "up", "k":
		m.moveArchiveDialogSelection(-1, len(indexes))
		return m, nil
	case "down", "j", "tab":
		m.moveArchiveDialogSelection(1, len(indexes))
		return m, nil
	case "shift+tab":
		m.moveArchiveDialogSelection(-1, len(indexes))
		return m, nil
	case " ":
		if len(indexes) == 0 {
			return m, nil
		}
		item := dialog.Projects[indexes[dialog.Selected]]
		if dialog.Marked[item.Key] {
			delete(dialog.Marked, item.Key)
		} else {
			dialog.Marked[item.Key] = true
		}
		m.status = fmt.Sprintf("%d marked", m.archiveMarkedProjectCount())
		return m, nil
	case "enter":
		projects := m.archiveMarkedProjects()
		if len(projects) == 0 {
			m.status = "Mark at least one project"
			return m, nil
		}
		selectPath := m.archiveSelectPathAfterMarked()
		m.closeArchiveDialog("")
		m.status = fmt.Sprintf("Archiving %d project(s)...", len(projects))
		return m, m.archiveProjectsCmd(projects, selectPath)
	}

	var cmd tea.Cmd
	dialog.Input, cmd = dialog.Input.Update(msg)
	m.clampArchiveDialogSelection(len(m.archiveFilteredProjectIndexes()))
	return m, cmd
}

func (m Model) archiveProjectItems() ([]archiveProjectItem, string) {
	selectedPath := ""
	if project, ok := m.selectedProject(); ok && model.NormalizeProjectKind(project.Kind) == model.ProjectKindProject {
		selectedPath = normalizeProjectPath(project.Path)
	}

	seen := map[string]struct{}{}
	items := make([]archiveProjectItem, 0, len(m.allProjects))
	for _, project := range m.projectsVisibleForPrivacy(m.allProjects) {
		if model.NormalizeProjectKind(project.Kind) != model.ProjectKindProject || project.Archived {
			continue
		}
		key := archiveProjectKey(project)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, archiveProjectItem{
			Key:     key,
			Label:   projectTitle(project.Path, project.Name),
			Summary: archiveProjectSummary(project),
			Current: key == selectedPath,
			Project: project,
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

func archiveProjectKey(project model.ProjectSummary) string {
	path := normalizeProjectPath(project.Path)
	if path == "." {
		return ""
	}
	return path
}

func archiveProjectSummary(project model.ProjectSummary) string {
	parts := []string{}
	if category := strings.TrimSpace(project.CategoryName); category != "" {
		parts = append(parts, category)
	} else {
		parts = append(parts, "Main")
	}
	if project.Pinned {
		parts = append(parts, "pinned")
	}
	if !project.PresentOnDisk {
		parts = append(parts, "missing")
	}
	if project.CategoryPrivate {
		parts = append(parts, "private")
	}
	return strings.Join(parts, " - ")
}

func (m Model) archiveFilteredProjectIndexes() []int {
	if m.archiveDialog == nil {
		return nil
	}
	filter := strings.ToLower(strings.TrimSpace(m.archiveDialog.Input.Value()))
	indexes := make([]int, 0, len(m.archiveDialog.Projects))
	for i, item := range m.archiveDialog.Projects {
		path := strings.ToLower(strings.TrimSpace(item.Project.Path))
		if filter == "" ||
			strings.Contains(strings.ToLower(item.Label), filter) ||
			strings.Contains(strings.ToLower(item.Summary), filter) ||
			strings.Contains(path, filter) {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func (m Model) archiveMarkedProjectCount() int {
	if m.archiveDialog == nil {
		return 0
	}
	count := 0
	for _, item := range m.archiveDialog.Projects {
		if m.archiveDialog.Marked[item.Key] {
			count++
		}
	}
	return count
}

func (m Model) archiveMarkedProjects() []model.ProjectSummary {
	if m.archiveDialog == nil {
		return nil
	}
	projects := make([]model.ProjectSummary, 0, len(m.archiveDialog.Marked))
	for _, item := range m.archiveDialog.Projects {
		if m.archiveDialog.Marked[item.Key] {
			projects = append(projects, item.Project)
		}
	}
	return projects
}

func (m Model) archiveMarkedProjectPathSet() map[string]bool {
	marked := map[string]bool{}
	if m.archiveDialog == nil {
		return marked
	}
	for _, item := range m.archiveDialog.Projects {
		if m.archiveDialog.Marked[item.Key] {
			marked[item.Key] = true
		}
	}
	return marked
}

func (m Model) archiveSelectPathAfterMarked() string {
	marked := m.archiveMarkedProjectPathSet()
	currentPath := m.currentSelectedProjectPath()
	if currentPath == "" {
		return ""
	}
	if !marked[currentPath] {
		return currentPath
	}
	for i := m.selected + 1; i < len(m.projects); i++ {
		path := normalizeProjectPath(m.projects[i].Path)
		if path != "" && !marked[path] {
			return path
		}
	}
	for i := m.selected - 1; i >= 0; i-- {
		path := normalizeProjectPath(m.projects[i].Path)
		if path != "" && !marked[path] {
			return path
		}
	}
	return ""
}

func (m *Model) moveArchiveDialogSelection(delta, total int) {
	if m.archiveDialog == nil || total <= 0 || delta == 0 {
		return
	}
	m.archiveDialog.Selected += delta
	if m.archiveDialog.Selected < 0 {
		m.archiveDialog.Selected = total - 1
	}
	if m.archiveDialog.Selected >= total {
		m.archiveDialog.Selected = 0
	}
}

func (m *Model) clampArchiveDialogSelection(total int) {
	if m.archiveDialog == nil {
		return
	}
	if total <= 0 {
		m.archiveDialog.Selected = 0
		return
	}
	if m.archiveDialog.Selected < 0 {
		m.archiveDialog.Selected = 0
	}
	if m.archiveDialog.Selected >= total {
		m.archiveDialog.Selected = total - 1
	}
}

func (m Model) renderArchiveDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderArchiveDialogPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderArchiveDialogPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(60, bodyW-10), 96))
	panelInnerWidth := max(28, panelWidth-4)
	maxContentHeight := max(10, bodyH-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderArchiveDialogContent(panelInnerWidth, maxContentHeight))
}

func (m Model) renderArchiveDialogContent(width, maxHeight int) string {
	dialog := m.archiveDialog
	if dialog == nil {
		return ""
	}
	filter := dialog.Input
	filter.Width = max(12, width-2)
	indexes := m.archiveFilteredProjectIndexes()
	selected := clampedCategorySelection(dialog.Selected, len(indexes))
	marked := m.archiveMarkedProjectCount()
	lines := []string{
		commandPaletteTitleStyle.Render("Archive Projects"),
		renderCategoryDialogActionHints(
			renderCategoryDialogSecondaryAction("mark"),
			renderCategoryDialogPrimaryAction("archive"),
			renderCategoryDialogCancelAction("close"),
			commandPaletteHintStyle.Render(fmt.Sprintf("%d marked", marked)),
		),
		filter.View(),
		"",
	}
	if len(dialog.Projects) == 0 {
		lines = append(lines, detailMutedStyle.Render("No active projects available."))
		return strings.Join(lines, "\n")
	}
	if len(indexes) == 0 {
		lines = append(lines, detailMutedStyle.Render("No projects match."))
		return strings.Join(lines, "\n")
	}
	limit := max(3, maxHeight-7)
	start, end := dialogListWindow(selected, len(indexes), limit)
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("up %d above", start)))
	}
	for visibleIdx := start; visibleIdx < end; visibleIdx++ {
		item := dialog.Projects[indexes[visibleIdx]]
		lines = append(lines, renderArchiveProjectRow(item, dialog.Marked[item.Key], visibleIdx == selected, width))
	}
	if end < len(indexes) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("down %d below", len(indexes)-end)))
	}
	return strings.Join(lines, "\n")
}

func renderArchiveProjectRow(item archiveProjectItem, marked, selected bool, width int) string {
	return renderCategoryMoveItemRow(categoryMoveItem{
		Key:     item.Key,
		Label:   item.Label,
		Summary: item.Summary,
		Current: item.Current,
	}, marked, selected, width)
}

func (m Model) archiveDialogFooterLabel() string {
	return joinFooterSegments(renderFooterStatus("Archive:"), renderFooterActionList(
		footerNavAction("type", "filter"),
		footerHideAction("Space", "mark"),
		footerPrimaryAction("Enter", "archive"),
		footerExitAction("Esc", "close"),
	))
}

func (m Model) archiveProjectsCmd(projects []model.ProjectSummary, selectPath string) tea.Cmd {
	paths := make([]string, 0, len(projects))
	seen := map[string]struct{}{}
	names := make([]string, 0, len(projects))
	for _, project := range projects {
		path := filepath.Clean(strings.TrimSpace(project.Path))
		if path == "" || path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
		names = append(names, projectRemovalName(project))
	}
	return func() tea.Msg {
		if m.svc == nil {
			return actionMsg{status: "Archive failed", err: fmt.Errorf("service unavailable")}
		}
		if len(paths) == 0 {
			return actionMsg{status: "No projects archived", err: fmt.Errorf("no projects selected")}
		}
		ctx, cancel := m.actionContext(tuiProjectActionTimeout)
		defer cancel()
		if err := m.svc.ArchiveProjects(ctx, paths); err != nil {
			err = timeoutActionError(err, tuiProjectActionTimeout, "archiving projects")
			projectPath := paths[0]
			return actionMsg{
				projectPath: projectPath,
				status:      "Archive failed",
				refresh:     invalidateProjectStructure(""),
				err:         err,
			}
		}
		status := fmt.Sprintf("Archived %d projects", len(paths))
		if len(paths) == 1 && len(names) == 1 {
			status = fmt.Sprintf("Archived %q", names[0])
		}
		return actionMsg{
			projectPath: paths[0],
			selectPath:  selectPath,
			status:      status,
			refresh:     invalidateProjectStructure(selectPath),
		}
	}
}
