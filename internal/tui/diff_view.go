package tui

import (
	"bytes"
	"fmt"
	"image"
	"path/filepath"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"regexp"
	"strconv"
	"strings"

	"lcroom/internal/scanner"
	"lcroom/internal/service"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type diffPaneFocus string

const (
	diffFocusFiles   diffPaneFocus = "files"
	diffFocusContent diffPaneFocus = "content"
)

type diffRenderMode string

const (
	diffRenderModeSideBySide diffRenderMode = "side_by_side"
	diffRenderModeUnified    diffRenderMode = "unified"
)

type diffListRow struct {
	Title     string
	FileIndex int
}

type diffTextSection struct {
	Title string
	Lines []string
}

type diffCellTone int

const (
	diffCellToneNeutral diffCellTone = iota
	diffCellToneDeleted
	diffCellToneAdded
	diffCellToneMeta
	diffCellToneHunk
	diffCellToneNote
	diffCellToneHeader
)

type diffSideBySideRow struct {
	Full        string
	Left        string
	Right       string
	FullTone    diffCellTone
	LeftTone    diffCellTone
	RightTone   diffCellTone
	LeftCode    bool
	RightCode   bool
	FullWidth   bool
	LeftLineNum int // 0 means no line number
	RightLineNum int
}

type diffRenderCacheKey struct {
	FileIndex int
	Width     int
	Mode      diffRenderMode
}

type commitPreviewReturnState struct {
	preview         service.CommitPreview
	messageOverride string
}

type diffViewState struct {
	ProjectPath string
	ProjectName string

	loading               bool
	preview               *service.DiffPreview
	returnToCommitPreview *commitPreviewReturnState

	selected int
	offset   int
	focus    diffPaneFocus
	mode     diffRenderMode

	contentViewport viewport.Model
	renderCache     map[diffRenderCacheKey]string

	// Continuous scroll: all files concatenated into one scrollable document.
	continuousContent   string
	continuousOffsets   []int          // line offset where each file starts
	continuousFileOrder []int          // file indices matching continuousOffsets
	continuousWidth     int
	continuousMode      diffRenderMode
}

func (state *diffViewState) hasFiles() bool {
	return state != nil && state.preview != nil && len(state.preview.Files) > 0
}

func newDiffViewState(projectPath, projectName string) *diffViewState {
	return &diffViewState{
		ProjectPath:     strings.TrimSpace(projectPath),
		ProjectName:     strings.TrimSpace(projectName),
		loading:         true,
		focus:           diffFocusFiles,
		mode:            diffRenderModeSideBySide,
		contentViewport: viewport.New(0, 0),
		renderCache:     make(map[diffRenderCacheKey]string),
	}
}

func (state *diffViewState) resetRenderCache() {
	if state == nil {
		return
	}
	state.renderCache = make(map[diffRenderCacheKey]string)
	state.continuousContent = ""
	state.continuousOffsets = nil
	state.continuousFileOrder = nil
	state.continuousWidth = 0
	state.continuousMode = ""
}

func (m Model) updateDiffMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.diffView == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		return m.closeDiffView("Diff view closed")
	case "alt+up":
		return m.closeDiffView("Focus: project list")
	case "/":
		m.openCommandMode()
		return m, textinput.Blink
	}

	if m.diffView.loading {
		return m, nil
	}
	if !m.diffView.hasFiles() {
		return m, nil
	}

	if m.pendingG {
		m.pendingG = false
		if msg.String() == "g" {
			if m.diffView.focus == diffFocusFiles {
				m.moveDiffSelectionTo(0)
				return m, nil
			}
			m.diffView.contentViewport.GotoTop()
			m.updateDiffSelectionFromScroll()
			return m, nil
		}
	}

	switch msg.String() {
	case "-":
		file, ok := m.selectedDiffFile()
		if !ok {
			return m, nil
		}
		m.diffView.loading = true
		if file.Staged {
			m.status = "Unstaging selected file..."
		} else {
			m.status = "Staging selected file..."
		}
		return m, m.toggleDiffStageCmd(m.diffView.ProjectPath, file, file.Staged)
	case "m":
		m.toggleDiffRenderMode()
		return m, nil
	case "tab":
		m.toggleDiffFocus()
		return m, nil
	case "shift+tab":
		m.toggleDiffFocus()
		return m, nil
	case "left", "h":
		m.setDiffFocus(diffFocusFiles)
		return m, nil
	case "right", "l", "enter":
		m.setDiffFocus(diffFocusContent)
		return m, nil
	case "up", "k":
		if m.diffView.focus == diffFocusFiles {
			m.moveDiffSelectionBy(-1)
			return m, nil
		}
		m.diffView.contentViewport.LineUp(1)
		m.updateDiffSelectionFromScroll()
		return m, nil
	case "down", "j":
		if m.diffView.focus == diffFocusFiles {
			m.moveDiffSelectionBy(1)
			return m, nil
		}
		m.diffView.contentViewport.LineDown(1)
		m.updateDiffSelectionFromScroll()
		return m, nil
	case "pgup":
		if m.diffView.focus == diffFocusFiles {
			m.moveDiffSelectionBy(-m.diffVisibleRows())
			return m, nil
		}
		m.diffView.contentViewport.PageUp()
		m.updateDiffSelectionFromScroll()
		return m, nil
	case "pgdown":
		if m.diffView.focus == diffFocusFiles {
			m.moveDiffSelectionBy(m.diffVisibleRows())
			return m, nil
		}
		m.diffView.contentViewport.PageDown()
		m.updateDiffSelectionFromScroll()
		return m, nil
	case "ctrl+u":
		if m.diffView.focus == diffFocusFiles {
			m.moveDiffSelectionBy(-m.diffVisibleRows())
			return m, nil
		}
		m.diffView.contentViewport.HalfPageUp()
		m.updateDiffSelectionFromScroll()
		return m, nil
	case "ctrl+d":
		if m.diffView.focus == diffFocusFiles {
			m.moveDiffSelectionBy(m.diffVisibleRows())
			return m, nil
		}
		m.diffView.contentViewport.HalfPageDown()
		m.updateDiffSelectionFromScroll()
		return m, nil
	case "home":
		if m.diffView.focus == diffFocusFiles {
			m.moveDiffSelectionTo(0)
			return m, nil
		}
		m.diffView.contentViewport.GotoTop()
		m.updateDiffSelectionFromScroll()
		return m, nil
	case "end", "G":
		if m.diffView.focus == diffFocusFiles {
			last := 0
			if m.diffView.preview != nil {
				last = max(0, len(m.diffView.preview.Files)-1)
			}
			m.moveDiffSelectionTo(last)
			return m, nil
		}
		m.diffView.contentViewport.GotoBottom()
		m.updateDiffSelectionFromScroll()
		return m, nil
	case "g":
		m.pendingG = true
		return m, nil
	}
	return m, nil
}

func (m Model) closeDiffView(fallbackStatus string) (tea.Model, tea.Cmd) {
	if m.diffView == nil {
		return m, nil
	}
	cached := m.diffView.returnToCommitPreview
	m.clearPendingGitSummary(m.diffView.ProjectPath)
	m.diffView = nil
	if cached == nil {
		m.status = fallbackStatus
		return m, nil
	}

	preview := cached.preview
	m.commitPreviewRequestID++
	m.commitPreview = &preview
	m.commitPreviewMessageOverride = cached.messageOverride
	m.commitPreviewRefreshing = true
	m.commitApplying = false
	m.setPendingGitSummary(preview.ProjectPath, "Refreshing commit preview...")
	m.status = "Refreshing commit preview..."
	return m, m.resumeCommitPreviewCmd(cached.preview, cached.messageOverride)
}

func (m *Model) toggleDiffFocus() {
	if m.diffView == nil {
		return
	}
	if m.diffView.focus == diffFocusFiles {
		m.diffView.focus = diffFocusContent
	} else {
		m.diffView.focus = diffFocusFiles
	}
	m.status = diffViewReadyStatus(*m.diffView)
}

func (m *Model) setDiffFocus(focus diffPaneFocus) {
	if m.diffView == nil {
		return
	}
	m.diffView.focus = focus
	m.status = diffViewReadyStatus(*m.diffView)
}

func (m *Model) toggleDiffRenderMode() {
	if m.diffView == nil {
		return
	}
	if m.diffView.mode == diffRenderModeUnified {
		m.diffView.mode = diffRenderModeSideBySide
	} else {
		m.diffView.mode = diffRenderModeUnified
	}
	m.diffView.continuousContent = ""
	m.diffView.continuousWidth = 0
	m.diffView.continuousMode = ""
	m.syncDiffView(false)
	m.status = diffViewReadyStatus(*m.diffView)
}

func (m *Model) moveDiffSelectionBy(delta int) {
	if m.diffView == nil || m.diffView.preview == nil || len(m.diffView.preview.Files) == 0 || delta == 0 {
		return
	}
	m.moveDiffSelectionTo(m.diffView.selected + delta)
}

func (m *Model) moveDiffSelectionTo(index int) {
	if m.diffView == nil || m.diffView.preview == nil || len(m.diffView.preview.Files) == 0 {
		return
	}
	if index < 0 {
		index = 0
	}
	if index >= len(m.diffView.preview.Files) {
		index = len(m.diffView.preview.Files) - 1
	}
	if index == m.diffView.selected {
		return
	}
	m.diffView.selected = index
	m.ensureDiffSelectionVisible()

	// Scroll the continuous viewport to the selected file.
	targetOffset := m.continuousOffsetForFile(index)
	layout := m.bodyLayout()
	_, contentPaneW := diffPaneWidths(layout.width)
	contentWidth := max(20, contentPaneW-4)
	contentHeight := max(1, max(3, layout.height-2)-2)
	m.diffView.contentViewport.Width = contentWidth
	m.diffView.contentViewport.Height = contentHeight
	m.ensureRenderedContinuousContent(contentWidth)
	m.diffView.contentViewport.SetContent(m.diffView.continuousContent)
	m.diffView.contentViewport.SetYOffset(targetOffset)
}

func (m *Model) ensureDiffSelectionVisible() {
	if m.diffView == nil || m.diffView.preview == nil || len(m.diffView.preview.Files) == 0 {
		return
	}
	rows := buildDiffListRows(m.diffView.preview.Files)
	selectedRow := diffListRowIndex(rows, m.diffView.selected)
	if selectedRow < 0 {
		selectedRow = 0
	}
	visible := m.diffVisibleRows()
	if visible <= 0 {
		visible = 1
	}
	maxOffset := max(0, len(rows)-visible)
	if m.diffView.offset > maxOffset {
		m.diffView.offset = maxOffset
	}
	if selectedRow < m.diffView.offset {
		m.diffView.offset = selectedRow
	}
	if selectedRow >= m.diffView.offset+visible {
		m.diffView.offset = selectedRow - visible + 1
	}
	if m.diffView.offset < 0 {
		m.diffView.offset = 0
	}
}

func (m Model) diffVisibleRows() int {
	layout := m.bodyLayout()
	innerHeight := max(3, layout.height-2)
	return max(1, innerHeight-1)
}

func (m *Model) syncDiffView(reset bool) {
	if m.diffView == nil {
		return
	}
	m.ensureDiffSelectionVisible()
	layout := m.bodyLayout()
	_, contentPaneW := diffPaneWidths(layout.width)
	innerHeight := max(3, layout.height-2)
	contentWidth := max(20, contentPaneW-4)
	contentHeight := max(1, innerHeight-2)

	m.diffView.contentViewport.Width = contentWidth
	m.diffView.contentViewport.Height = contentHeight
	m.ensureRenderedContinuousContent(contentWidth)

	offset := m.diffView.contentViewport.YOffset
	m.diffView.contentViewport.SetContent(m.diffView.continuousContent)
	if reset {
		targetOffset := m.continuousOffsetForFile(m.diffView.selected)
		m.diffView.contentViewport.SetYOffset(targetOffset)
		return
	}
	maxOffset := max(0, m.diffView.contentViewport.TotalLineCount()-m.diffView.contentViewport.Height)
	if offset > maxOffset {
		offset = maxOffset
	}
	m.diffView.contentViewport.SetYOffset(offset)
}

func (m *Model) ensureRenderedContinuousContent(width int) {
	if m.diffView == nil || !m.diffView.hasFiles() {
		return
	}
	if width < 1 {
		width = 1
	}
	if m.diffView.continuousWidth == width && m.diffView.continuousMode == m.diffView.mode && m.diffView.continuousContent != "" {
		return
	}

	files := m.diffView.preview.Files
	rows := buildDiffListRows(files)

	var parts []string
	offsets := make([]int, 0, len(files))
	fileOrder := make([]int, 0, len(files))
	currentLine := 0
	seen := make(map[int]bool)

	for _, row := range rows {
		if row.FileIndex < 0 {
			header := renderContinuousSectionHeader(row.Title, width)
			parts = append(parts, header)
			currentLine += strings.Count(header, "\n") + 1
			continue
		}
		if seen[row.FileIndex] {
			continue
		}
		seen[row.FileIndex] = true

		file := files[row.FileIndex]
		fileHeader := renderContinuousFileHeader(file, width)
		parts = append(parts, fileHeader)
		currentLine += strings.Count(fileHeader, "\n") + 1

		offsets = append(offsets, currentLine)
		fileOrder = append(fileOrder, row.FileIndex)

		cacheKey := diffRenderCacheKey{FileIndex: row.FileIndex, Width: width, Mode: m.diffView.mode}
		body, ok := m.diffView.renderCache[cacheKey]
		if !ok {
			body = renderDiffEntryBody(file, width, m.diffView.mode)
			if m.diffView.renderCache == nil {
				m.diffView.renderCache = make(map[diffRenderCacheKey]string)
			}
			m.diffView.renderCache[cacheKey] = body
		}

		parts = append(parts, body)
		currentLine += strings.Count(body, "\n") + 1

		// Blank separator between files.
		parts = append(parts, "")
		currentLine++
	}

	m.diffView.continuousContent = strings.Join(parts, "\n")
	m.diffView.continuousOffsets = offsets
	m.diffView.continuousFileOrder = fileOrder
	m.diffView.continuousWidth = width
	m.diffView.continuousMode = m.diffView.mode
}

func renderContinuousSectionHeader(title string, width int) string {
	line := strings.Repeat("─", max(1, width))
	header := lipgloss.NewStyle().
		Foreground(lipgloss.Color("246")).
		Bold(true).
		Render(title)
	rule := lipgloss.NewStyle().
		Foreground(lipgloss.Color("238")).
		Render(line)
	return rule + "\n" + header + "\n" + rule
}

func renderContinuousFileHeader(file service.DiffFilePreview, width int) string {
	kindCode := diffFileKindCode(file)
	state := diffFileStateWord(file)
	label := kindCode + " " + state + "  " + file.Summary
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("252")).
		Bold(true).
		Width(width).
		Render(truncateText(label, max(1, width)))
}

func (m *Model) continuousFileAtOffset(yOffset int) int {
	if m.diffView == nil || len(m.diffView.continuousOffsets) == 0 {
		return 0
	}
	// Find the last file whose offset is <= yOffset.
	result := m.diffView.continuousFileOrder[0]
	for i, off := range m.diffView.continuousOffsets {
		if off <= yOffset {
			result = m.diffView.continuousFileOrder[i]
		} else {
			break
		}
	}
	return result
}

func (m *Model) continuousOffsetForFile(fileIndex int) int {
	if m.diffView == nil {
		return 0
	}
	for i, idx := range m.diffView.continuousFileOrder {
		if idx == fileIndex {
			return m.diffView.continuousOffsets[i]
		}
	}
	return 0
}

func (m *Model) updateDiffSelectionFromScroll() {
	if m.diffView == nil || !m.diffView.hasFiles() || len(m.diffView.continuousOffsets) == 0 {
		return
	}
	fileIndex := m.continuousFileAtOffset(m.diffView.contentViewport.YOffset)
	if fileIndex != m.diffView.selected {
		m.diffView.selected = fileIndex
		m.ensureDiffSelectionVisible()
	}
}

func (m Model) selectedDiffFile() (service.DiffFilePreview, bool) {
	if m.diffView == nil || m.diffView.preview == nil || m.diffView.selected < 0 || m.diffView.selected >= len(m.diffView.preview.Files) {
		return service.DiffFilePreview{}, false
	}
	return m.diffView.preview.Files[m.diffView.selected], true
}

func (m Model) renderDiffView(width, height int) string {
	filesPaneW, contentPaneW := diffPaneWidths(width)
	innerHeight := max(3, height-2)
	files := m.renderDiffFileList(max(20, filesPaneW-4), innerHeight)
	content := m.renderDiffContentPane(max(20, contentPaneW-4), innerHeight)
	left := m.renderFramedPane(files, filesPaneW, innerHeight, m.diffView != nil && m.diffView.focus == diffFocusFiles)
	right := m.renderFramedPane(content, contentPaneW, innerHeight, m.diffView != nil && m.diffView.focus == diffFocusContent)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

func diffPaneWidths(totalWidth int) (int, int) {
	gap := 1
	filesWidth := min(36, max(24, totalWidth/3))
	contentWidth := totalWidth - filesWidth - gap
	if contentWidth < 28 {
		contentWidth = 28
		filesWidth = max(20, totalWidth-contentWidth-gap)
	}
	if filesWidth < 20 {
		filesWidth = 20
		contentWidth = max(24, totalWidth-filesWidth-gap)
	}
	return filesWidth, contentWidth
}

func (m Model) renderDiffFileList(width, height int) string {
	lines := []string{commandPaletteTitleStyle.Render("Files")}
	if m.diffView == nil {
		return fitPaneContent(strings.Join(lines, "\n"), width, height)
	}
	if m.diffView.loading {
		lines = append(lines, commandPaletteHintStyle.Render("Loading git diff..."))
		return fitPaneContent(strings.Join(lines, "\n"), width, height)
	}
	if !m.diffView.hasFiles() {
		lines = append(lines, commandPaletteHintStyle.Render(diffViewEmptyMeta(*m.diffView)))
		return fitPaneContent(strings.Join(lines, "\n"), width, height)
	}

	rows := buildDiffListRows(m.diffView.preview.Files)
	visible := max(1, height-1)
	start := m.diffView.offset
	maxOffset := max(0, len(rows)-visible)
	if start > maxOffset {
		start = maxOffset
	}
	end := min(len(rows), start+visible)
	for i := start; i < end; i++ {
		row := rows[i]
		if row.FileIndex < 0 {
			lines = append(lines, renderDiffSectionHeader(row.Title, width))
			continue
		}
		lines = append(lines, renderDiffFileRow(m.diffView.preview.Files[row.FileIndex], row.FileIndex == m.diffView.selected, width))
	}
	if end < len(rows) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more", len(rows)-end)))
	}
	return fitPaneContent(strings.Join(lines, "\n"), width, height)
}

func renderDiffSectionHeader(title string, width int) string {
	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color("246")).
		Bold(true).
		Render(truncateText(title, max(1, width)))
}

func diffFileNameDir(summary string) (name, dir string) {
	// Handle renames like "old/path -> new/path".
	if idx := strings.Index(summary, " -> "); idx >= 0 {
		newPath := summary[idx+4:]
		name = filepath.Base(newPath)
		dir = filepath.Dir(newPath)
		oldDir := filepath.Dir(summary[:idx])
		if oldDir != dir {
			dir = oldDir + " → " + dir
		}
	} else {
		name = filepath.Base(summary)
		dir = filepath.Dir(summary)
	}
	if dir == "." {
		dir = ""
	}
	return name, dir
}

func renderDiffFileRow(file service.DiffFilePreview, selected bool, width int) string {
	kindCode := diffFileKindCode(file)
	stateWord := diffFileStateWord(file)
	fileName, fileDir := diffFileNameDir(file.Summary)
	pathWidth := max(8, width-5)
	label := truncateText(fileName, pathWidth)
	if fileDir != "" {
		remaining := max(0, pathWidth-ansi.StringWidth(fileName)-1)
		if remaining > 2 {
			label += " " + truncateText(fileDir, remaining)
		}
	}
	base := fmt.Sprintf(" %s %s %s", kindCode, stateWord, label)
	if selected {
		return commandPaletteSelectStyle.Width(width).Render(truncateText(base, max(1, width)))
	}
	code := diffFileCodeStyle(file).Render(kindCode)
	dirPart := ""
	if fileDir != "" {
		remaining := max(0, pathWidth-ansi.StringWidth(fileName)-1)
		if remaining > 2 {
			dirPart = " " + commandPaletteHintStyle.Render(truncateText(fileDir, remaining))
		}
	}
	row := " " + code + " " + commandPaletteRowStyle.Render(truncateText(fileName, pathWidth)) + dirPart
	return commandPaletteRowStyle.Width(width).Render(row)
}

func diffFileCodeStyle(file service.DiffFilePreview) lipgloss.Style {
	switch file.Kind {
	case scanner.GitChangeDeleted:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	case scanner.GitChangeUntracked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	case scanner.GitChangeRenamed, scanner.GitChangeCopied:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	}
}

func diffFileKindCode(file service.DiffFilePreview) string {
	switch file.Kind {
	case scanner.GitChangeModified:
		return "M"
	case scanner.GitChangeAdded:
		return "A"
	case scanner.GitChangeDeleted:
		return "D"
	case scanner.GitChangeRenamed:
		return "R"
	case scanner.GitChangeCopied:
		return "C"
	case scanner.GitChangeType:
		return "T"
	case scanner.GitChangeUnmerged:
		return "U"
	case scanner.GitChangeUntracked:
		return "?"
	default:
		return "·"
	}
}

func diffFileStateWord(file service.DiffFilePreview) string {
	switch file.Kind {
	case scanner.GitChangeModified:
		return "modified"
	case scanner.GitChangeAdded:
		return "added"
	case scanner.GitChangeDeleted:
		return "deleted"
	case scanner.GitChangeRenamed:
		return "renamed"
	case scanner.GitChangeCopied:
		return "copied"
	case scanner.GitChangeType:
		return "type"
	case scanner.GitChangeUnmerged:
		return "unmerged"
	case scanner.GitChangeUntracked:
		return "untracked"
	default:
		return "changed"
	}
}

func (m Model) renderDiffContentPane(width, height int) string {
	title := commandPaletteTitleStyle.Render("Diff")
	meta := commandPaletteHintStyle.Render("Preparing preview...")
	body := ""

	if m.diffView != nil {
		switch {
		case m.diffView.loading:
			title = commandPaletteTitleStyle.Render("Diff")
			meta = commandPaletteHintStyle.Render("Loading changed files and previews...")
			body = renderCodexMessageBlock("Status", "Building the selected project's diff preview.", lipgloss.Color("81"), lipgloss.Color("252"), width)
		case m.diffView.hasFiles():
			visibleIndex := m.diffView.selected
			if len(m.diffView.continuousOffsets) > 0 {
				visibleIndex = m.continuousFileAtOffset(m.diffView.contentViewport.YOffset)
			}
			file := m.diffView.preview.Files[visibleIndex]
			title = commandPaletteTitleStyle.Render(truncateText(file.Summary, max(1, width)))
			meta = commandPaletteHintStyle.Render(diffFileMeta(file, m.diffView.mode))
			body = m.diffView.contentViewport.View()
		default:
			title = commandPaletteTitleStyle.Render(truncateText(diffViewEmptyTitle(*m.diffView), max(1, width)))
			meta = commandPaletteHintStyle.Render(diffViewEmptyMeta(*m.diffView))
			body = renderCodexMessageBlock("Nothing to diff", diffViewEmptyMessage(*m.diffView), lipgloss.Color("81"), lipgloss.Color("252"), width)
		}
	}

	content := []string{title, meta}
	if strings.TrimSpace(body) != "" {
		content = append(content, body)
	}
	return fitPaneContent(strings.Join(content, "\n"), width, height)
}

func diffFileMeta(file service.DiffFilePreview, mode diffRenderMode) string {
	parts := []string{diffFileStateWord(file)}
	switch {
	case file.Staged && file.Unstaged:
		parts = append(parts, "staged + unstaged")
	case file.Staged:
		parts = append(parts, "staged")
	case file.Unstaged:
		parts = append(parts, "unstaged")
	}
	if file.IsImage {
		parts = append(parts, "image preview")
	} else {
		parts = append(parts, diffRenderModeMetaLabel(mode))
	}
	return strings.Join(parts, " | ")
}

func diffSyntaxFilename(file service.DiffFilePreview) string {
	if path := strings.TrimSpace(file.Path); path != "" {
		return path
	}
	return strings.TrimSpace(file.OriginalPath)
}

func renderDiffEntryBody(file service.DiffFilePreview, width int, mode diffRenderMode) string {
	if file.IsImage {
		return renderDiffImageBody(file, width)
	}
	body := strings.TrimSpace(file.Body)
	if body == "" {
		body = "No textual diff available."
	}
	syntaxFilename := diffSyntaxFilename(file)
	switch mode {
	case diffRenderModeUnified:
		return renderDiffUnifiedTextBody(body, syntaxFilename, width)
	default:
		return renderDiffSideBySideTextBody(body, syntaxFilename, width)
	}
}

func renderDiffImageBody(file service.DiffFilePreview, width int) string {
	blocks := []string{}
	if note := strings.TrimSpace(file.Body); note != "" {
		blocks = append(blocks, renderCodexMessageBlock("Image", note, lipgloss.Color("81"), lipgloss.Color("252"), width))
	}
	if imageBlock := renderDiffImagePreviewSet(file, width); strings.TrimSpace(imageBlock) != "" {
		blocks = append(blocks, imageBlock)
	}
	if len(blocks) == 0 {
		blocks = append(blocks, renderCodexMessageBlock("Image", "Preview unavailable.", lipgloss.Color("81"), lipgloss.Color("252"), width))
	}
	return strings.Join(blocks, "\n\n")
}

func renderDiffUnifiedTextBody(body, syntaxFilename string, width int) string {
	contentWidth := max(10, width-2)
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")).Render("Diff")
	highlightPlan := newSyntaxHighlightPlan("", syntaxFilename, body)
	renderedLines := make([]string, 0, len(strings.Split(body, "\n")))
	for _, line := range strings.Split(body, "\n") {
		renderedLines = append(renderedLines, renderUnifiedDiffLine(line, contentWidth, highlightPlan))
	}
	bodyBlock := lipgloss.NewStyle().Width(contentWidth).Render(strings.Join(renderedLines, "\n"))
	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(lipgloss.Color("81")).
		PaddingLeft(1).
		Render(title + "\n" + bodyBlock)
}

func renderDiffSideBySideTextBody(body, syntaxFilename string, width int) string {
	body = strings.TrimSpace(body)
	if body == "" {
		body = "No textual diff available."
	}

	contentWidth := max(10, width-2)
	highlightPlan := newSyntaxHighlightPlan("", syntaxFilename, body)
	sections := parseDiffTextSections(body)
	rendered := make([]string, 0, len(sections))
	for _, section := range sections {
		block := renderDiffTextSection(section, highlightPlan, contentWidth)
		if strings.TrimSpace(ansi.Strip(block)) != "" {
			rendered = append(rendered, block)
		}
	}
	if len(rendered) == 0 {
		rendered = append(rendered, renderDiffFullRow("No textual diff available.", contentWidth, diffCellToneNote))
	}
	return renderDiffTextBlock("Diff", strings.Join(rendered, "\n\n"), lipgloss.Color("81"))
}

func parseDiffTextSections(body string) []diffTextSection {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	sections := make([]diffTextSection, 0, 3)
	current := diffTextSection{}
	haveCurrent := false

	flush := func() {
		if !haveCurrent {
			return
		}
		current.Lines = trimBlankLines(current.Lines)
		if current.Title != "" || len(current.Lines) > 0 {
			sections = append(sections, current)
		}
		current = diffTextSection{}
		haveCurrent = false
	}

	for _, line := range lines {
		if title, ok := diffTextSectionTitle(line); ok {
			flush()
			current = diffTextSection{Title: title}
			haveCurrent = true
			continue
		}
		if !haveCurrent {
			haveCurrent = true
		}
		current.Lines = append(current.Lines, line)
	}
	flush()

	if len(sections) == 0 {
		return []diffTextSection{{Lines: trimBlankLines(lines)}}
	}
	return sections
}

func diffTextSectionTitle(line string) (string, bool) {
	if !strings.HasPrefix(line, "# ") {
		return "", false
	}
	title := strings.TrimSpace(strings.TrimPrefix(line, "# "))
	switch title {
	case "Staged", "Unstaged", "Untracked":
		return title, true
	default:
		return "", false
	}
}

func trimBlankLines(lines []string) []string {
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	out := make([]string, end-start)
	copy(out, lines[start:end])
	return out
}

func renderDiffTextSection(section diffTextSection, highlightPlan syntaxHighlightPlan, width int) string {
	rows := buildDiffSideBySideRows(section)
	if len(rows) == 0 {
		return ""
	}
	rendered := make([]string, 0, len(rows))
	for _, row := range rows {
		rendered = append(rendered, renderDiffSideBySideRow(row, width, highlightPlan))
	}
	return strings.Join(rendered, "\n")
}

func renderUnifiedDiffLine(line string, width int, highlightPlan syntaxHighlightPlan) string {
	switch {
	case strings.HasPrefix(line, "$ "):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#66d9ef")).Bold(true).Render(line)
	case strings.HasPrefix(line, "diff --git "), strings.HasPrefix(line, "index "):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#75715e")).Bold(true).Render(line)
	case strings.HasPrefix(line, "@@"):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#fd971f")).Bold(true).Render(line)
	case strings.HasPrefix(line, "# "):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#75715e")).Faint(true).Render(line)
	case strings.HasPrefix(line, "[command ") || strings.HasPrefix(line, "[file changes "):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e22e")).Bold(true).Render(line)
	case diffPatchLineKind(line) != "":
		return renderDiffHighlightedPatchLine(line, width, diffToneForPatchLine(line), highlightPlan)
	case strings.HasPrefix(line, "+++"):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e22e")).Render(line)
	case strings.HasPrefix(line, "---"):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#f92672")).Render(line)
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#f8f8f2")).Render(line)
	}
}

// Pair adjacent removed and added runs so the diff content reads as before/after columns.
func buildDiffSideBySideRows(section diffTextSection) []diffSideBySideRow {
	rows := []diffSideBySideRow{}
	if title := strings.TrimSpace(section.Title); title != "" {
		rows = append(rows, diffSideBySideRow{
			Full:      title,
			FullTone:  diffCellToneHeader,
			FullWidth: true,
		})
	}
	if len(section.Lines) == 0 {
		return rows
	}
	if diffSectionUsesSideBySide(section.Lines) {
		rows = append(rows, diffSideBySideRow{
			Left:      "Before",
			Right:     "After",
			LeftTone:  diffCellToneHeader,
			RightTone: diffCellToneHeader,
		})
	}

	type numberedLine struct {
		text    string
		lineNum int
	}

	var removed []numberedLine
	var added []numberedLine
	pendingOldPath := ""
	leftLineNum := 0
	rightLineNum := 0

	flushPair := func() {
		if len(removed) == 0 && len(added) == 0 {
			return
		}
		pairs := max(len(removed), len(added))
		for i := 0; i < pairs; i++ {
			left := ""
			right := ""
			leftNum := 0
			rightNum := 0
			if i < len(removed) {
				left = removed[i].text
				leftNum = removed[i].lineNum
			}
			if i < len(added) {
				right = added[i].text
				rightNum = added[i].lineNum
			}
			rows = append(rows, diffSideBySideRow{
				Left:         left,
				Right:        right,
				LeftTone:     diffToneForPatchCell(left, diffCellToneDeleted),
				RightTone:    diffToneForPatchCell(right, diffCellToneAdded),
				LeftCode:     left != "",
				RightCode:    right != "",
				LeftLineNum:  leftNum,
				RightLineNum: rightNum,
			})
		}
		removed = nil
		added = nil
	}

	for _, line := range section.Lines {
		if strings.TrimSpace(line) == "" {
			flushPair()
			continue
		}

		switch {
		case strings.HasPrefix(line, "diff --git "), strings.HasPrefix(line, "index "):
			flushPair()
			rows = append(rows, diffSideBySideRow{
				Full:      line,
				FullTone:  diffCellToneMeta,
				FullWidth: true,
			})
		case strings.HasPrefix(line, "--- "):
			flushPair()
			pendingOldPath = line
		case strings.HasPrefix(line, "+++ "):
			flushPair()
			if pendingOldPath != "" {
				rows = append(rows, diffSideBySideRow{
					Left:      pendingOldPath,
					Right:     line,
					LeftTone:  diffCellToneDeleted,
					RightTone: diffCellToneAdded,
				})
				pendingOldPath = ""
				continue
			}
			rows = append(rows, diffSideBySideRow{
				Left:      "",
				Right:     line,
				LeftTone:  diffCellToneNeutral,
				RightTone: diffCellToneAdded,
				RightCode: false,
			})
		case strings.HasPrefix(line, "@@"):
			flushPair()
			if pendingOldPath != "" {
				rows = append(rows, diffSideBySideRow{
					Left:      pendingOldPath,
					Right:     "",
					LeftTone:  diffCellToneDeleted,
					RightTone: diffCellToneNeutral,
				})
				pendingOldPath = ""
			}
			oldStart, newStart := parseDiffHunkHeader(line)
			leftLineNum = oldStart
			rightLineNum = newStart
			rows = append(rows, diffSideBySideRow{
				Full:      line,
				FullTone:  diffCellToneHunk,
				FullWidth: true,
			})
		default:
			switch diffPatchLineKind(line) {
			case "-":
				removed = append(removed, numberedLine{text: line, lineNum: leftLineNum})
				leftLineNum++
			case "+":
				added = append(added, numberedLine{text: line, lineNum: rightLineNum})
				rightLineNum++
			case " ":
				flushPair()
				if pendingOldPath != "" {
					rows = append(rows, diffSideBySideRow{
						Left:      pendingOldPath,
						Right:     "",
						LeftTone:  diffCellToneDeleted,
						RightTone: diffCellToneNeutral,
					})
					pendingOldPath = ""
				}
				rows = append(rows, diffSideBySideRow{
					Left:         line,
					Right:        line,
					LeftTone:     diffCellToneNeutral,
					RightTone:    diffCellToneNeutral,
					LeftCode:     true,
					RightCode:    true,
					LeftLineNum:  leftLineNum,
					RightLineNum: rightLineNum,
				})
				leftLineNum++
				rightLineNum++
			default:
				flushPair()
				if pendingOldPath != "" {
					rows = append(rows, diffSideBySideRow{
						Left:      pendingOldPath,
						Right:     "",
						LeftTone:  diffCellToneDeleted,
						RightTone: diffCellToneNeutral,
					})
					pendingOldPath = ""
				}
				rows = append(rows, diffSideBySideRow{
					Full:      line,
					FullTone:  diffToneForFullDiffLine(line),
					FullWidth: true,
				})
			}
		}
	}

	flushPair()
	if pendingOldPath != "" {
		rows = append(rows, diffSideBySideRow{
			Left:      pendingOldPath,
			Right:     "",
			LeftTone:  diffCellToneDeleted,
			RightTone: diffCellToneNeutral,
		})
	}
	return rows
}

var diffHunkHeaderRegexp = regexp.MustCompile(`@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func parseDiffHunkHeader(line string) (oldStart, newStart int) {
	m := diffHunkHeaderRegexp.FindStringSubmatch(line)
	if m == nil {
		return 0, 0
	}
	oldStart, _ = strconv.Atoi(m[1])
	newStart, _ = strconv.Atoi(m[2])
	return oldStart, newStart
}

func diffSectionUsesSideBySide(lines []string) bool {
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git "), strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "@@"):
			return true
		case diffPatchLineKind(line) != "":
			return true
		}
	}
	return false
}

func diffPatchLineKind(line string) string {
	if line == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "):
		return ""
	case strings.HasPrefix(line, "+"):
		return "+"
	case strings.HasPrefix(line, "-"):
		return "-"
	case strings.HasPrefix(line, " "):
		return " "
	default:
		return ""
	}
}

func diffToneForPatchCell(line string, fallback diffCellTone) diffCellTone {
	if strings.TrimSpace(line) == "" {
		return diffCellToneNeutral
	}
	return fallback
}

func diffToneForPatchLine(line string) diffCellTone {
	switch diffPatchLineKind(line) {
	case "+":
		return diffCellToneAdded
	case "-":
		return diffCellToneDeleted
	default:
		return diffCellToneNeutral
	}
}

func diffToneForFullDiffLine(line string) diffCellTone {
	switch {
	case strings.HasPrefix(line, "\\ "):
		return diffCellToneMeta
	case strings.HasPrefix(line, "# "):
		return diffCellToneNote
	case strings.HasPrefix(line, "new file mode"), strings.HasPrefix(line, "deleted file mode"),
		strings.HasPrefix(line, "old mode"), strings.HasPrefix(line, "new mode"),
		strings.HasPrefix(line, "rename from "), strings.HasPrefix(line, "rename to "),
		strings.HasPrefix(line, "similarity index"), strings.HasPrefix(line, "dissimilarity index"),
		strings.HasPrefix(line, "Binary files "):
		return diffCellToneMeta
	default:
		return diffCellToneNote
	}
}

const diffLineNumWidth = 4

func diffLineNumStyle(tone diffCellTone) lipgloss.Style {
	switch tone {
	case diffCellToneAdded:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#5a9e6a")).Background(diffToneBackgroundColor(tone))
	case diffCellToneDeleted:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#b35b6b")).Background(diffToneBackgroundColor(tone))
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("239"))
	}
}

func renderDiffLineNum(num int, tone diffCellTone) string {
	style := diffLineNumStyle(tone)
	if num <= 0 {
		return style.Width(diffLineNumWidth).Render("")
	}
	s := strconv.Itoa(num)
	if len(s) > diffLineNumWidth {
		s = s[len(s)-diffLineNumWidth:]
	}
	return style.Width(diffLineNumWidth).Align(lipgloss.Right).Render(s)
}

func diffGutterSep(tone diffCellTone) string {
	bg := diffToneBackgroundColor(tone)
	if bg == "" {
		return " "
	}
	return lipgloss.NewStyle().Background(bg).Render(" ")
}

func renderDiffSideBySideRow(row diffSideBySideRow, width int, highlightPlan syntaxHighlightPlan) string {
	if row.FullWidth {
		return renderDiffFullRow(row.Full, width, row.FullTone)
	}

	gap := lipgloss.NewStyle().Foreground(lipgloss.Color("239")).Render(" │ ")
	gapWidth := ansi.StringWidth(ansi.Strip(gap))
	gutterWidth := diffLineNumWidth + 1 // line number + space
	leftWidth := max(1, (width-gapWidth-gutterWidth*2)/2)
	rightWidth := max(1, width-leftWidth-gapWidth-gutterWidth*2)

	leftLines := wrapDiffCell(row.Left, leftWidth)
	rightLines := wrapDiffCell(row.Right, rightWidth)
	lineCount := max(len(leftLines), len(rightLines))
	rendered := make([]string, 0, lineCount)
	for i := 0; i < lineCount; i++ {
		left := ""
		right := ""
		if i < len(leftLines) {
			left = leftLines[i]
		}
		if i < len(rightLines) {
			right = rightLines[i]
		}
		leftNum := 0
		rightNum := 0
		if i == 0 {
			leftNum = row.LeftLineNum
			rightNum = row.RightLineNum
		}
		leftSep := diffGutterSep(row.LeftTone)
		rightSep := diffGutterSep(row.RightTone)
		rendered = append(rendered,
			renderDiffLineNum(leftNum, row.LeftTone)+leftSep+
				renderDiffCellLine(left, leftWidth, row.LeftTone, highlightPlan, row.LeftCode)+
				gap+
				renderDiffLineNum(rightNum, row.RightTone)+rightSep+
				renderDiffCellLine(right, rightWidth, row.RightTone, highlightPlan, row.RightCode),
		)
	}
	return strings.Join(rendered, "\n")
}

func wrapDiffCell(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	text = strings.ReplaceAll(text, "\t", "    ")
	if text == "" {
		return []string{""}
	}
	lines := []string{}
	remaining := text
	for remaining != "" {
		part := ansi.Truncate(remaining, width, "")
		if part == "" {
			runes := []rune(remaining)
			part = string(runes[:1])
		}
		lines = append(lines, part)
		remaining = strings.TrimPrefix(remaining, part)
	}
	return lines
}

func renderDiffCellLine(text string, width int, tone diffCellTone, highlightPlan syntaxHighlightPlan, highlightSyntax bool) string {
	if highlightSyntax && strings.TrimSpace(text) != "" {
		return renderDiffHighlightedCellLine(text, width, tone, highlightPlan)
	}
	style := diffToneCellStyle(tone)
	if width > 0 {
		style = style.Width(width)
	}
	return style.Render(text)
}

func renderDiffHighlightedCellLine(text string, width int, tone diffCellTone, highlightPlan syntaxHighlightPlan) string {
	prefix, body := splitDiffSyntaxPrefix(text)
	highlighted := diffToneCellStyle(tone).Render(prefix) + highlightPlan.Render(body, syntaxHighlightOptions{
		DefaultColor:    diffToneCellDefaultColor(tone),
		BackgroundColor: diffToneBackgroundColor(tone),
		NoItalic:        true,
	})
	strippedWidth := ansi.StringWidth(ansi.Strip(highlighted))
	if width > 0 && strippedWidth < width {
		highlighted += diffToneFill(tone, width-strippedWidth)
	}
	if width > 0 && ansi.StringWidth(ansi.Strip(highlighted)) > width {
		return ansi.Truncate(highlighted, width, "")
	}
	return highlighted
}

func renderDiffHighlightedPatchLine(line string, width int, tone diffCellTone, highlightPlan syntaxHighlightPlan) string {
	prefix, body := splitDiffSyntaxPrefix(line)
	// Unified view: use background color to indicate add/delete, keep foreground neutral
	prefixStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#f8f8f2")).
		Background(diffToneBackgroundColor(tone))
	highlighted := prefixStyle.Render(prefix) + highlightPlan.Render(body, syntaxHighlightOptions{
		DefaultColor:    lipgloss.Color("#f8f8f2"),
		BackgroundColor: diffToneBackgroundColor(tone),
		NoItalic:        true,
	})
	if width > 0 {
		strippedWidth := ansi.StringWidth(ansi.Strip(highlighted))
		if strippedWidth < width {
			highlighted += diffToneFill(tone, width-strippedWidth)
		}
	}
	return highlighted
}

func splitDiffSyntaxPrefix(text string) (string, string) {
	if text == "" {
		return "", ""
	}
	return text[:1], text[1:]
}

func diffToneFill(tone diffCellTone, width int) string {
	if width <= 0 {
		return ""
	}
	fill := strings.Repeat(" ", width)
	if bg := diffToneBackgroundColor(tone); bg != "" {
		return lipgloss.NewStyle().Background(bg).Render(fill)
	}
	return fill
}

func diffToneDefaultColor(tone diffCellTone) lipgloss.Color {
	switch tone {
	case diffCellToneDeleted:
		return lipgloss.Color("#f8f8f2")
	case diffCellToneAdded:
		return lipgloss.Color("#f8f8f2")
	default:
		return lipgloss.Color("#f8f8f2")
	}
}

func diffToneCellDefaultColor(tone diffCellTone) lipgloss.Color {
	switch tone {
	case diffCellToneDeleted, diffCellToneAdded:
		return lipgloss.Color("#f8f8f2")
	default:
		return diffToneDefaultColor(tone)
	}
}

func diffToneBackgroundColor(tone diffCellTone) lipgloss.Color {
	switch tone {
	case diffCellToneDeleted:
		return lipgloss.Color("#2a1018")
	case diffCellToneAdded:
		return lipgloss.Color("#122a16")
	default:
		return ""
	}
}

func diffToneCellStyle(tone diffCellTone) lipgloss.Style {
	switch tone {
	case diffCellToneDeleted, diffCellToneAdded:
		return lipgloss.NewStyle().
			Foreground(diffToneCellDefaultColor(tone)).
			Background(diffToneBackgroundColor(tone))
	default:
		return diffToneStyle(tone)
	}
}

func renderDiffFullRow(text string, width int, tone diffCellTone) string {
	if text == "" {
		return strings.Repeat(" ", max(0, width))
	}
	return fitStyledWidth(diffToneStyle(tone).Render(text), width)
}

func diffToneStyle(tone diffCellTone) lipgloss.Style {
	switch tone {
	case diffCellToneDeleted:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#f8f8f2")).Background(lipgloss.Color("#2a1018"))
	case diffCellToneAdded:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#f8f8f2")).Background(lipgloss.Color("#122a16"))
	case diffCellToneMeta:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#75715e")).Bold(true)
	case diffCellToneHunk:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#fd971f")).Bold(true)
	case diffCellToneHeader:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e22e")).Bold(true)
	case diffCellToneNote:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#75715e")).Faint(true)
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#f8f8f2"))
	}
}

func renderDiffTextBlock(label, body string, accent lipgloss.Color) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(accent).Render(label)
	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(accent).
		PaddingLeft(1).
		Render(title + "\n" + body)
}

func renderDiffImagePreviewSet(file service.DiffFilePreview, width int) string {
	switch {
	case len(file.OldImage) > 0 && len(file.NewImage) > 0:
		gap := "  "
		colWidth := max(12, (width-len(gap))/2)
		left := renderDiffImageVariant("HEAD image", file.OldImage, colWidth)
		right := renderDiffImageVariant("Working tree image", file.NewImage, colWidth)
		return lipgloss.JoinHorizontal(lipgloss.Top, left, gap, right)
	case len(file.OldImage) > 0:
		return renderDiffImageVariant("HEAD image", file.OldImage, width)
	case len(file.NewImage) > 0:
		return renderDiffImageVariant("Working tree image", file.NewImage, width)
	default:
		return ""
	}
}

func renderDiffImageVariant(title string, data []byte, width int) string {
	preview := renderANSIImagePreview(data, max(8, width), 18)
	if strings.TrimSpace(preview) == "" {
		preview = commandPaletteHintStyle.Render("Image preview unavailable.")
	}
	return lipgloss.NewStyle().Width(width).Render(commandPaletteTitleStyle.Render(title) + "\n" + preview)
}

func renderANSIImagePreview(data []byte, width, maxRows int) string {
	if len(data) == 0 || width <= 0 || maxRows <= 0 {
		return ""
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return commandPaletteHintStyle.Render("Image preview unavailable: " + strings.TrimSpace(err.Error()))
	}

	bounds := img.Bounds()
	imgWidth := max(1, bounds.Dx())
	imgHeight := max(1, bounds.Dy())
	cols := min(max(4, width), 40)
	rows := int(float64(imgHeight) * float64(cols) / float64(imgWidth) / 2.0)
	rows = max(3, min(maxRows, rows))

	var out strings.Builder
	totalPixelRows := max(1, rows*2)
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			x := bounds.Min.X + (col * imgWidth / cols)
			topY := bounds.Min.Y + ((row * 2) * imgHeight / totalPixelRows)
			bottomY := bounds.Min.Y + ((row*2 + 1) * imgHeight / totalPixelRows)
			if topY >= bounds.Max.Y {
				topY = bounds.Max.Y - 1
			}
			if bottomY >= bounds.Max.Y {
				bottomY = bounds.Max.Y - 1
			}
			top := rgbaForPreview(img.At(x, topY))
			bottom := rgbaForPreview(img.At(x, bottomY))
			fmt.Fprintf(&out, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", top[0], top[1], top[2], bottom[0], bottom[1], bottom[2])
		}
		out.WriteString("\x1b[0m")
		if row < rows-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func rgbaForPreview(c color.Color) [3]uint8 {
	const bg = 18
	r16, g16, b16, a16 := c.RGBA()
	alpha := float64(a16) / 65535.0
	if alpha <= 0 {
		return [3]uint8{bg, bg, bg}
	}
	r := uint8((float64(uint8(r16>>8)) * alpha) + (bg * (1 - alpha)))
	g := uint8((float64(uint8(g16>>8)) * alpha) + (bg * (1 - alpha)))
	b := uint8((float64(uint8(b16>>8)) * alpha) + (bg * (1 - alpha)))
	return [3]uint8{r, g, b}
}

func diffRenderModeLabel(mode diffRenderMode) string {
	switch mode {
	case diffRenderModeUnified:
		return "unified"
	default:
		return "split"
	}
}

func diffRenderModeMetaLabel(mode diffRenderMode) string {
	return diffRenderModeLabel(mode)
}

func diffRenderModeToggleLabel(mode diffRenderMode) string {
	switch mode {
	case diffRenderModeUnified:
		return "split"
	default:
		return "unified"
	}
}

func diffViewReadyStatus(state diffViewState) string {
	closeLabel := diffViewCloseLabel(state)
	if state.loading {
		return "Preparing diff view..."
	}
	if !state.hasFiles() {
		return "Worktree clean. Esc " + closeLabel
	}
	modeLabel := diffRenderModeToggleLabel(state.mode)
	switch state.focus {
	case diffFocusContent:
		return "Diff " + diffRenderModeLabel(state.mode) + ". Up/Down scroll, M " + modeLabel + ", Tab files, Esc " + closeLabel
	default:
		return "Diff " + diffRenderModeLabel(state.mode) + ". Up/Down choose file, M " + modeLabel + ", Tab scroll pane, Esc " + closeLabel
	}
}

func diffViewFooterLabel(state diffViewState) string {
	closeLabel := diffViewCloseLabel(state)
	if state.loading {
		return "Diff loading. Esc " + closeLabel
	}
	if !state.hasFiles() {
		return "Diff clean. Esc " + closeLabel
	}
	modeLabel := diffRenderModeToggleLabel(state.mode)
	switch state.focus {
	case diffFocusContent:
		return "Diff: Up/Down scroll, M " + modeLabel + ", PgUp/PgDn page, Left/Tab files, Esc " + closeLabel
	default:
		return "Diff: Up/Down choose, M " + modeLabel + ", Enter/Right open, PgUp/PgDn page, Esc " + closeLabel
	}
}

func renderDiffFooter(width int, state diffViewState, usageSegment string) string {
	hideLabel := "list"
	closeLabel := "close"
	if state.returnToCommitPreview != nil {
		hideLabel = "back"
		closeLabel = "back"
	}
	if state.loading {
		return renderFooterLine(
			width,
			renderFooterMeta("Diff"),
			renderFooterActionList(
				footerHideAction("Alt+Up", hideLabel),
				footerExitAction("Esc", closeLabel),
			),
			usageSegment,
		)
	}
	if !state.hasFiles() {
		return renderFooterLine(
			width,
			renderFooterMeta("Diff: clean worktree"),
			renderFooterActionList(
				footerHideAction("Alt+Up", hideLabel),
				footerExitAction("Esc", closeLabel),
			),
			usageSegment,
		)
	}

	meta := renderFooterMeta("Diff: " + diffRenderModeMetaLabel(state.mode))

	stageLabel := "stage"
	if file, ok := selectedDiffFileFromState(state); ok && file.Staged {
		stageLabel = "unstage"
	}

	actions := []footerAction{
		footerPrimaryAction("-", stageLabel),
		footerHideAction("Alt+Up", hideLabel),
		footerLowAction("m", diffRenderModeToggleLabel(state.mode)),
	}
	switch state.focus {
	case diffFocusContent:
		actions = append(actions,
			footerNavAction("Up/Down", "scroll"),
			footerNavAction("Tab/Left", "files"),
			footerNavAction("PgUp/PgDn", "page"),
		)
	default:
		actions = append(actions,
			footerNavAction("Up/Down", "choose"),
			footerNavAction("Enter/Tab", "diff"),
			footerNavAction("PgUp/PgDn", "page"),
		)
	}
	actions = append(actions, footerExitAction("Esc", closeLabel))
	return renderFooterLine(width, meta, renderFooterActionList(actions...), usageSegment)
}

func diffViewCloseLabel(state diffViewState) string {
	if state.returnToCommitPreview != nil {
		return "back"
	}
	return "close"
}

func diffViewEmptyTitle(state diffViewState) string {
	if project := strings.TrimSpace(state.ProjectName); project != "" {
		return project
	}
	if state.preview != nil && strings.TrimSpace(state.preview.ProjectName) != "" {
		return strings.TrimSpace(state.preview.ProjectName)
	}
	return "Diff"
}

func diffViewEmptyMeta(state diffViewState) string {
	parts := []string{"Clean worktree"}
	if state.preview != nil {
		if branch := strings.TrimSpace(state.preview.Branch); branch != "" {
			parts = append(parts, branch)
		}
	}
	return strings.Join(parts, " | ")
}

func diffViewEmptyMessage(state diffViewState) string {
	project := diffViewEmptyTitle(state)
	if strings.EqualFold(project, "Diff") {
		return "This project has no staged, unstaged, or untracked changes right now."
	}
	return fmt.Sprintf("%s has no staged, unstaged, or untracked changes right now.", project)
}

func buildDiffListRows(files []service.DiffFilePreview) []diffListRow {
	if len(files) == 0 {
		return nil
	}
	stagedCount := 0
	unstagedCount := 0
	for _, file := range files {
		if file.Staged {
			stagedCount++
		}
		if file.Unstaged || file.Untracked {
			unstagedCount++
		}
	}
	rows := make([]diffListRow, 0, len(files)+2)
	if stagedCount > 0 {
		rows = append(rows, diffListRow{Title: fmt.Sprintf("Staged (%d)", stagedCount), FileIndex: -1})
		for i, file := range files {
			if file.Staged {
				rows = append(rows, diffListRow{FileIndex: i})
			}
		}
	}
	if unstagedCount > 0 {
		rows = append(rows, diffListRow{Title: fmt.Sprintf("Unstaged (%d)", unstagedCount), FileIndex: -1})
		for i, file := range files {
			if file.Unstaged || file.Untracked {
				rows = append(rows, diffListRow{FileIndex: i})
			}
		}
	}
	return rows
}

func diffListRowIndex(rows []diffListRow, fileIndex int) int {
	for i, row := range rows {
		if row.FileIndex == fileIndex {
			return i
		}
	}
	return -1
}

func selectedDiffFileFromState(state diffViewState) (service.DiffFilePreview, bool) {
	if state.preview == nil || state.selected < 0 || state.selected >= len(state.preview.Files) {
		return service.DiffFilePreview{}, false
	}
	return state.preview.Files[state.selected], true
}
