package boss

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"lcroom/internal/terminalmd"
	"lcroom/internal/uistyle"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type bossOpenTarget struct {
	Kind  string
	Label string
	Path  string
	order int
}

type bossOpenTargetPickerState struct {
	Targets  []bossOpenTarget
	Selected int
}

type bossOpenTargetOpenedMsg struct {
	status string
	err    error
}

func (m Model) OpenTargetPickerActive() bool {
	return m.openTargetPicker != nil
}

func (m Model) openBossOpenTargetPicker() (tea.Model, tea.Cmd) {
	targets := m.bossOpenTargetsForPicker()
	if len(targets) == 0 {
		m.status = "No files or links in this boss chat"
		return m, nil
	}
	m.openTargetPicker = &bossOpenTargetPickerState{
		Targets:  targets,
		Selected: len(targets) - 1,
	}
	m.status = "File picker open"
	return m, nil
}

func (m *Model) closeBossOpenTargetPicker(status string) {
	m.openTargetPicker = nil
	if strings.TrimSpace(status) != "" {
		m.status = status
	}
}

func (m Model) updateBossOpenTargetPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	picker := m.openTargetPicker
	if picker == nil {
		return m, nil
	}
	if len(picker.Targets) == 0 {
		m.closeBossOpenTargetPicker("No files or links in this boss chat")
		return m, nil
	}
	m.clampBossOpenTargetPickerSelection(len(picker.Targets))
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeBossOpenTargetPicker("File picker closed")
		return m, nil
	case "up", "k":
		m.moveBossOpenTargetPickerSelection(-1, len(picker.Targets))
		return m, nil
	case "down", "j":
		m.moveBossOpenTargetPickerSelection(1, len(picker.Targets))
		return m, nil
	case "pgup", "ctrl+u":
		m.moveBossOpenTargetPickerSelection(-5, len(picker.Targets))
		return m, nil
	case "pgdown", "ctrl+d":
		m.moveBossOpenTargetPickerSelection(5, len(picker.Targets))
		return m, nil
	case "home":
		picker.Selected = 0
		return m, nil
	case "end":
		picker.Selected = len(picker.Targets) - 1
		return m, nil
	case "enter", "alt+o":
		target := picker.Targets[picker.Selected]
		m.closeBossOpenTargetPicker("Opening " + bossOpenTargetDisplay(target))
		return m, m.openBossOpenTargetCmd(target)
	case "f", "F":
		target := picker.Targets[picker.Selected]
		if strings.TrimSpace(target.Kind) == "url" {
			m.status = "Links do not have containing folders"
			return m, nil
		}
		m.closeBossOpenTargetPicker("Opening folder for " + bossOpenTargetDisplay(target))
		return m, m.openBossOpenTargetFolderCmd(target)
	default:
		return m, nil
	}
}

func (m Model) applyBossOpenTargetOpenedMsg(msg bossOpenTargetOpenedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = "Open failed: " + msg.err.Error()
		return m, nil
	}
	if strings.TrimSpace(msg.status) != "" {
		m.status = msg.status
	}
	return m, nil
}

func (m *Model) clampBossOpenTargetPickerSelection(total int) {
	if m.openTargetPicker == nil || total <= 0 {
		return
	}
	if m.openTargetPicker.Selected < 0 {
		m.openTargetPicker.Selected = 0
	}
	if m.openTargetPicker.Selected >= total {
		m.openTargetPicker.Selected = total - 1
	}
}

func (m *Model) moveBossOpenTargetPickerSelection(delta, total int) {
	if m.openTargetPicker == nil || total <= 0 {
		return
	}
	m.openTargetPicker.Selected = clampInt(m.openTargetPicker.Selected+delta, 0, total-1)
}

func (m Model) bossOpenTargetsForPicker() []bossOpenTarget {
	targets := make([]bossOpenTarget, 0)
	order := 0
	addText := func(text string) {
		for _, link := range terminalmd.ExtractOpenLinks(text) {
			path := strings.TrimSpace(link.OpenPath)
			if path == "" {
				continue
			}
			order++
			targets = append(targets, bossOpenTarget{
				Kind:  strings.TrimSpace(link.Kind),
				Label: strings.TrimSpace(link.Label),
				Path:  path,
				order: order,
			})
		}
	}
	for _, message := range m.messages {
		addText(message.Content)
	}
	if m.sending {
		addText(m.streamingAssistantText)
	}
	return mergeBossOpenTargets(targets)
}

func mergeBossOpenTargets(targets []bossOpenTarget) []bossOpenTarget {
	if len(targets) == 0 {
		return nil
	}
	type mergedTarget struct {
		target     bossOpenTarget
		order      int
		inputIndex int
	}
	merged := make([]mergedTarget, 0, len(targets))
	seen := make(map[string]int, len(targets))
	for inputIndex, target := range targets {
		path := strings.TrimSpace(target.Path)
		if path == "" {
			continue
		}
		kind := strings.TrimSpace(target.Kind)
		if kind == "" {
			kind = "file"
		}
		label := strings.TrimSpace(target.Label)
		order := target.order
		if order <= 0 {
			order = inputIndex + 1
		}
		key := bossOpenTargetKey(path)
		if existingIndex, ok := seen[key]; ok {
			existing := merged[existingIndex]
			if order > existing.order || (order == existing.order && inputIndex > existing.inputIndex) {
				target.Kind = kind
				target.Label = label
				target.Path = path
				target.order = order
				merged[existingIndex] = mergedTarget{target: target, order: order, inputIndex: inputIndex}
			}
			continue
		}
		seen[key] = len(merged)
		target.Kind = kind
		target.Label = label
		target.Path = path
		target.order = order
		merged = append(merged, mergedTarget{target: target, order: order, inputIndex: inputIndex})
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].order == merged[j].order {
			return merged[i].inputIndex < merged[j].inputIndex
		}
		return merged[i].order < merged[j].order
	})
	out := make([]bossOpenTarget, 0, len(merged))
	for _, item := range merged {
		out = append(out, item.target)
	}
	return out
}

func bossOpenTargetKey(path string) string {
	return strings.TrimSpace(path)
}

func (m Model) renderBossOpenTargetPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderBossOpenTargetPicker(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := maxInt(0, (bodyW-panelWidth)/2)
	top := maxInt(0, minInt((bodyH-panelHeight)/5, bodyH-panelHeight))
	return overlayBossBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderBossOpenTargetPicker(bodyW, bodyH int) string {
	panelWidth := minInt(bodyW, minInt(maxInt(58, bodyW-10), 92))
	panelInnerWidth := maxInt(28, panelWidth-4)
	content := m.renderBossOpenTargetPickerContent(panelInnerWidth, bodyH)
	panelHeight := minInt(maxInt(8, countBlockLines(content)+4), maxInt(8, bodyH-2))
	return m.renderRawPanel("Open Files", content, panelWidth, panelHeight)
}

func (m Model) renderBossOpenTargetPickerContent(width, bodyH int) string {
	picker := m.openTargetPicker
	if picker == nil {
		return ""
	}
	lines := []string{
		bossMutedStyle.Render(fitLine("Files and links found in this boss chat. Enter opens with the system app or browser.", width)),
		renderBossOpenTargetPickerAction("Enter/Alt+O", "open", uistyle.DialogActionPrimary) + "   " +
			renderBossOpenTargetPickerAction("f", "folder", uistyle.DialogActionNavigate) + "   " +
			renderBossOpenTargetPickerAction("Up/Down", "select", uistyle.DialogActionNavigate) + "   " +
			renderBossOpenTargetPickerAction("Esc", "close", uistyle.DialogActionCancel),
		"",
	}
	if len(picker.Targets) == 0 {
		lines = append(lines, bossMutedStyle.Render(fitLine("No files or links found.", width)))
		return strings.Join(lines, "\n")
	}
	start, end := m.bossOpenTargetPickerWindow(len(picker.Targets), bodyH)
	if start > 0 {
		lines = append(lines, bossMutedStyle.Render(fitLine(fmt.Sprintf("up %d more", start), width)))
	}
	for i := start; i < end; i++ {
		lines = append(lines, renderBossOpenTargetPickerRow(picker.Targets[i], i == picker.Selected, width))
	}
	if end < len(picker.Targets) {
		lines = append(lines, bossMutedStyle.Render(fitLine(fmt.Sprintf("down %d more", len(picker.Targets)-end), width)))
	}
	if selected, ok := m.currentBossOpenTarget(); ok {
		lines = append(lines, "")
		lines = append(lines, bossSessionPickerSectionStyle.Render(fitLine("Selected", width)))
		lines = append(lines, bossSessionPickerDetailStyle.Render(fitLine(bossOpenTargetDisplay(selected), width)))
		if path := strings.TrimSpace(selected.Path); path != "" {
			lines = append(lines, bossMutedStyle.Render(fitLine(path, width)))
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) bossOpenTargetPickerWindow(total, bodyH int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	limit := m.bossOpenTargetPickerListLimit(total, bodyH)
	limit = minInt(limit, total)
	start := 0
	if m.openTargetPicker != nil && m.openTargetPicker.Selected >= limit {
		start = m.openTargetPicker.Selected - limit + 1
	}
	maxStart := total - limit
	if start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	return start, start + limit
}

func (m Model) bossOpenTargetPickerListLimit(total, bodyH int) int {
	if total <= 0 {
		return 0
	}
	if bodyH <= 0 {
		bodyH = defaultBossHeight
	}
	panelMaxHeight := maxInt(8, bodyH-2)
	fixedLines := 12
	available := panelMaxHeight - fixedLines
	if available < 1 {
		available = 1
	}
	return minInt(total, minInt(12, available))
}

func renderBossOpenTargetPickerRow(target bossOpenTarget, selected bool, width int) string {
	badge := bossOpenTargetBadge(target)
	right := bossOpenTargetRight(target)
	badgeWidth := 6
	rightWidth := len([]rune(right))
	labelWidth := maxInt(12, width-badgeWidth-rightWidth-5)
	line := fmt.Sprintf("  %-4s %s", badge, clipText(bossOpenTargetLabel(target), labelWidth))
	if right != "" {
		line += " " + right
	}
	if selected {
		line = "> " + strings.TrimPrefix(line, "  ")
		return bossSessionPickerSelectedRowStyle.Width(width).Render(fitLine(line, width))
	}
	return bossSessionPickerRowStyle.Width(width).Render(fitLine(line, width))
}

func (m Model) currentBossOpenTarget() (bossOpenTarget, bool) {
	picker := m.openTargetPicker
	if picker == nil || len(picker.Targets) == 0 {
		return bossOpenTarget{}, false
	}
	index := picker.Selected
	if index < 0 {
		index = 0
	}
	if index >= len(picker.Targets) {
		index = len(picker.Targets) - 1
	}
	return picker.Targets[index], true
}

func bossOpenTargetBadge(target bossOpenTarget) string {
	switch strings.TrimSpace(target.Kind) {
	case "url":
		return "LINK"
	case "dir":
		return "DIR"
	case "source":
		return "SRC"
	case "image":
		return "IMG"
	case "pdf":
		return "PDF"
	case "sheet":
		return "DATA"
	case "deck":
		return "DECK"
	case "video":
		return "VID"
	case "audio":
		return "AUD"
	default:
		return "FILE"
	}
}

func bossOpenTargetLabel(target bossOpenTarget) string {
	label := strings.TrimSpace(target.Label)
	if label != "" {
		return label
	}
	if strings.TrimSpace(target.Kind) == "url" {
		if right := bossOpenTargetRight(target); right != "" {
			return right
		}
	}
	base := filepath.Base(strings.TrimSpace(target.Path))
	if base == "." || base == string(filepath.Separator) || base == "" {
		return strings.TrimSpace(target.Path)
	}
	return base
}

func bossOpenTargetDisplay(target bossOpenTarget) string {
	label := bossOpenTargetLabel(target)
	right := bossOpenTargetRight(target)
	if right == "" || right == label {
		return label
	}
	return label + " (" + right + ")"
}

func bossOpenTargetRight(target bossOpenTarget) string {
	path := strings.TrimSpace(target.Path)
	if path == "" {
		return ""
	}
	if strings.TrimSpace(target.Kind) == "url" {
		parsed, err := url.Parse(path)
		if err == nil && parsed.Host != "" {
			return parsed.Host
		}
		return path
	}
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) {
		return path
	}
	return base
}

func renderBossOpenTargetPickerAction(key, label string, tone uistyle.DialogActionTone) string {
	return uistyle.RenderDialogActionTone(key, label, tone, bossDialogActionFillStyle)
}

func (m Model) openBossOpenTargetCmd(target bossOpenTarget) tea.Cmd {
	target = normalizedBossOpenTarget(target)
	return func() tea.Msg {
		if strings.TrimSpace(target.Kind) == "url" {
			if err := bossOpenBrowserURL(target.Path, "open link"); err != nil {
				return bossOpenTargetOpenedMsg{err: err}
			}
			return bossOpenTargetOpenedMsg{status: "Opened link"}
		}
		if err := bossExternalPathOpener(target.Path); err != nil {
			return bossOpenTargetOpenedMsg{err: err}
		}
		return bossOpenTargetOpenedMsg{status: "Opened file"}
	}
}

func (m Model) openBossOpenTargetFolderCmd(target bossOpenTarget) tea.Cmd {
	target = normalizedBossOpenTarget(target)
	return func() tea.Msg {
		if strings.TrimSpace(target.Kind) == "url" {
			return bossOpenTargetOpenedMsg{err: fmt.Errorf("links do not have containing folders")}
		}
		if err := bossExternalPathRevealer(target.Path); err != nil {
			return bossOpenTargetOpenedMsg{err: err}
		}
		return bossOpenTargetOpenedMsg{status: "Opened containing folder"}
	}
}

func normalizedBossOpenTarget(target bossOpenTarget) bossOpenTarget {
	target.Kind = strings.TrimSpace(target.Kind)
	if target.Kind == "" {
		target.Kind = "file"
	}
	target.Label = strings.TrimSpace(target.Label)
	target.Path = strings.TrimSpace(target.Path)
	return target
}

func bossOpenTargetContainingFolder(target bossOpenTarget) (string, error) {
	if strings.TrimSpace(target.Kind) == "url" {
		return "", fmt.Errorf("links do not have containing folders")
	}
	path := strings.TrimSpace(target.Path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		return path, nil
	}
	folder := filepath.Dir(path)
	if folder == "" || folder == "." {
		if err != nil {
			return "", fmt.Errorf("inspect path: %w", err)
		}
		return "", fmt.Errorf("containing folder is not available")
	}
	info, err = os.Stat(folder)
	if err != nil {
		return "", fmt.Errorf("inspect containing folder: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("containing path is not a directory")
	}
	return folder, nil
}

var (
	bossExternalBrowserOpener = bossOpenExternalBrowserURL
	bossExternalPathOpener    = bossOpenExternalPath
	bossExternalPathRevealer  = bossRevealExternalPath
)

func bossOpenBrowserURL(rawURL, action string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return fmt.Errorf("browser URL is required")
	}
	if err := bossExternalBrowserOpener(rawURL); err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return nil
}

func bossOpenExternalBrowserURL(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Run()
}

func bossOpenExternalPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("inspect path: %w", err)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Run()
}

func bossRevealExternalPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		return bossExternalPathOpener(path)
	}
	if err != nil {
		folder, folderErr := bossOpenTargetContainingFolder(bossOpenTarget{Kind: "file", Path: path})
		if folderErr != nil {
			return folderErr
		}
		return bossExternalPathOpener(folder)
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", path)
	case "windows":
		cmd = exec.Command("explorer", "/select,"+path)
	default:
		folder, folderErr := bossOpenTargetContainingFolder(bossOpenTarget{Kind: "file", Path: path})
		if folderErr != nil {
			return folderErr
		}
		return bossExternalPathOpener(folder)
	}
	return cmd.Run()
}
