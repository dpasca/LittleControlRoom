package tui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"lcroom/internal/scanner"
	"lcroom/internal/service"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type diffPaneFocus string

const (
	diffFocusFiles   diffPaneFocus = "files"
	diffFocusContent diffPaneFocus = "content"
)

type diffViewState struct {
	ProjectPath string
	ProjectName string

	loading bool
	preview *service.DiffPreview

	selected int
	offset   int
	focus    diffPaneFocus

	contentViewport viewport.Model
	renderedWidth   int
	renderedIndex   int
	renderedContent string
}

func newDiffViewState(projectPath, projectName string) *diffViewState {
	return &diffViewState{
		ProjectPath:     strings.TrimSpace(projectPath),
		ProjectName:     strings.TrimSpace(projectName),
		loading:         true,
		focus:           diffFocusFiles,
		contentViewport: viewport.New(0, 0),
		renderedIndex:   -1,
	}
}

func (m Model) updateDiffMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.diffView == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.diffView = nil
		m.status = "Diff view closed"
		return m, nil
	}

	if m.diffView.loading {
		return m, nil
	}

	switch msg.String() {
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
		return m, nil
	case "down", "j":
		if m.diffView.focus == diffFocusFiles {
			m.moveDiffSelectionBy(1)
			return m, nil
		}
		m.diffView.contentViewport.LineDown(1)
		return m, nil
	case "pgup":
		if m.diffView.focus == diffFocusFiles {
			m.moveDiffSelectionBy(-m.diffVisibleRows())
			return m, nil
		}
		m.diffView.contentViewport.PageUp()
		return m, nil
	case "pgdown":
		if m.diffView.focus == diffFocusFiles {
			m.moveDiffSelectionBy(m.diffVisibleRows())
			return m, nil
		}
		m.diffView.contentViewport.PageDown()
		return m, nil
	case "home":
		if m.diffView.focus == diffFocusFiles {
			m.moveDiffSelectionTo(0)
			return m, nil
		}
		m.diffView.contentViewport.GotoTop()
		return m, nil
	case "end":
		if m.diffView.focus == diffFocusFiles {
			last := 0
			if m.diffView.preview != nil {
				last = max(0, len(m.diffView.preview.Files)-1)
			}
			m.moveDiffSelectionTo(last)
			return m, nil
		}
		m.diffView.contentViewport.GotoBottom()
		return m, nil
	}
	return m, nil
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
	m.diffView.renderedIndex = -1
	m.ensureDiffSelectionVisible()
	m.syncDiffView(true)
}

func (m *Model) ensureDiffSelectionVisible() {
	if m.diffView == nil || m.diffView.preview == nil || len(m.diffView.preview.Files) == 0 {
		return
	}
	visible := m.diffVisibleRows()
	if visible <= 0 {
		visible = 1
	}
	maxOffset := max(0, len(m.diffView.preview.Files)-visible)
	if m.diffView.offset > maxOffset {
		m.diffView.offset = maxOffset
	}
	if m.diffView.selected < m.diffView.offset {
		m.diffView.offset = m.diffView.selected
	}
	if m.diffView.selected >= m.diffView.offset+visible {
		m.diffView.offset = m.diffView.selected - visible + 1
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
	m.ensureRenderedDiffContent(contentWidth)

	offset := m.diffView.contentViewport.YOffset
	m.diffView.contentViewport.SetContent(m.diffView.renderedContent)
	if reset {
		m.diffView.contentViewport.GotoTop()
		return
	}
	maxOffset := max(0, m.diffView.contentViewport.TotalLineCount()-m.diffView.contentViewport.Height)
	if offset > maxOffset {
		offset = maxOffset
	}
	m.diffView.contentViewport.SetYOffset(offset)
}

func (m *Model) ensureRenderedDiffContent(width int) {
	if m.diffView == nil {
		return
	}
	if width < 1 {
		width = 1
	}
	if m.diffView.preview == nil || len(m.diffView.preview.Files) == 0 {
		m.diffView.renderedContent = renderCodexMessageBlock("Diff", "No changed files loaded.", lipgloss.Color("81"), lipgloss.Color("252"), width)
		m.diffView.renderedWidth = width
		m.diffView.renderedIndex = -1
		return
	}
	if m.diffView.renderedWidth == width && m.diffView.renderedIndex == m.diffView.selected && m.diffView.renderedContent != "" {
		return
	}
	file := m.diffView.preview.Files[m.diffView.selected]
	m.diffView.renderedContent = renderDiffEntryBody(file, width)
	m.diffView.renderedWidth = width
	m.diffView.renderedIndex = m.diffView.selected
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
	if m.diffView.preview == nil || len(m.diffView.preview.Files) == 0 {
		lines = append(lines, commandPaletteHintStyle.Render("No changed files"))
		return fitPaneContent(strings.Join(lines, "\n"), width, height)
	}

	visible := max(1, height-1)
	start := m.diffView.offset
	maxOffset := max(0, len(m.diffView.preview.Files)-visible)
	if start > maxOffset {
		start = maxOffset
	}
	end := min(len(m.diffView.preview.Files), start+visible)
	for i := start; i < end; i++ {
		lines = append(lines, renderDiffFileRow(m.diffView.preview.Files[i], i == m.diffView.selected, width))
	}
	if end < len(m.diffView.preview.Files) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more", len(m.diffView.preview.Files)-end)))
	}
	return fitPaneContent(strings.Join(lines, "\n"), width, height)
}

func renderDiffFileRow(file service.DiffFilePreview, selected bool, width int) string {
	state := diffFileStateWord(file)
	base := fmt.Sprintf("%-3s %-9s %s", file.Code, state, truncateText(file.Summary, max(8, width-15)))
	if selected {
		return commandPaletteSelectStyle.Width(width).Render(" " + truncateText(base, max(1, width-1)))
	}
	code := diffFileCodeStyle(file).Render(file.Code)
	pathWidth := max(8, width-15)
	row := " " + code + " " + commandPaletteHintStyle.Render(fmt.Sprintf("%-9s", state)) + " " + commandPaletteRowStyle.Render(truncateText(file.Summary, pathWidth))
	return commandPaletteRowStyle.Width(width).Render(row)
}

func diffFileCodeStyle(file service.DiffFilePreview) lipgloss.Style {
	switch diffFileStateWord(file) {
	case "untracked":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	case "deleted":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	}
}

func diffFileStateWord(file service.DiffFilePreview) string {
	switch {
	case file.Untracked:
		return "untracked"
	case file.Kind == scanner.GitChangeDeleted:
		return "deleted"
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
		case m.diffView.preview != nil && len(m.diffView.preview.Files) > 0:
			file := m.diffView.preview.Files[m.diffView.selected]
			title = commandPaletteTitleStyle.Render(truncateText(file.Summary, max(1, width)))
			meta = commandPaletteHintStyle.Render(diffFileMeta(file))
			body = m.diffView.contentViewport.View()
		default:
			meta = commandPaletteHintStyle.Render("No changed files")
			body = renderCodexMessageBlock("Status", "No changed files loaded.", lipgloss.Color("81"), lipgloss.Color("252"), width)
		}
	}

	content := []string{title, meta}
	if strings.TrimSpace(body) != "" {
		content = append(content, body)
	}
	return fitPaneContent(strings.Join(content, "\n"), width, height)
}

func diffFileMeta(file service.DiffFilePreview) string {
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
	}
	return strings.Join(parts, " | ")
}

func renderDiffEntryBody(file service.DiffFilePreview, width int) string {
	if file.IsImage {
		return renderDiffImageBody(file, width)
	}
	body := strings.TrimSpace(file.Body)
	if body == "" {
		body = "No textual diff available."
	}
	return renderCodexMonospaceBlock("Diff", body, lipgloss.Color("81"), width)
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

func diffViewReadyStatus(state diffViewState) string {
	if state.loading {
		return "Preparing diff view..."
	}
	switch state.focus {
	case diffFocusContent:
		return "Diff ready. Up/Down scroll, Tab files, Esc close"
	default:
		return "Diff ready. Up/Down choose file, Tab scroll pane, Esc close"
	}
}

func diffViewFooterLabel(state diffViewState) string {
	if state.loading {
		return "Diff loading. Esc close"
	}
	switch state.focus {
	case diffFocusContent:
		return "Diff: Up/Down scroll, PgUp/PgDn page, Left/Tab files, Esc close"
	default:
		return "Diff: Up/Down choose, Enter/Right open, PgUp/PgDn page, Esc close"
	}
}
