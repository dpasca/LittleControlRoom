package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/codexapp"
	"lcroom/internal/fuzzyfilter"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type codexArtifactOpenTarget struct {
	Kind        string
	Label       string
	Path        string
	PreviewData []byte
	order       int
}

type codexArtifactPickerState struct {
	ProjectPath     string
	Title           string
	Hint            string
	Targets         []codexArtifactOpenTarget
	Filter          string
	Selected        int
	PreviewSeq      int64
	PreviewRequests map[string]int64
	PreviewData     map[string][]byte
	PreviewErrors   map[string]string
}

func (m Model) openCodexArtifactPicker(snapshot codexapp.Snapshot) (tea.Model, tea.Cmd) {
	projectPath := strings.TrimSpace(firstNonEmptyString(snapshot.ProjectPath, m.codexVisibleProject))
	targets := m.codexOpenTargetsForPicker(snapshot)
	if len(targets) == 0 {
		scanCmd := m.maybeStartCodexArtifactLinkScanForPicker(projectPath, snapshot)
		if scanCmd == nil && !m.codexArtifactLinkScanInFlight(projectPath, m.codexTranscriptRevision(projectPath)) {
			m.status = "No openable links in this embedded transcript"
			return m, nil
		}
		m.codexArtifactPicker = &codexArtifactPickerState{
			ProjectPath:     projectPath,
			Title:           "Open Links",
			Hint:            "Scanning this embedded transcript for links.",
			Targets:         nil,
			Selected:        0,
			PreviewRequests: make(map[string]int64),
			PreviewData:     make(map[string][]byte),
			PreviewErrors:   make(map[string]string),
		}
		m.status = "Scanning transcript links..."
		return m, scanCmd
	}
	m.codexArtifactPicker = &codexArtifactPickerState{
		ProjectPath:     projectPath,
		Title:           "Open Links",
		Hint:            "Links found in this embedded transcript. Type to filter by name.",
		Targets:         targets,
		Selected:        len(targets) - 1,
		PreviewRequests: make(map[string]int64),
		PreviewData:     make(map[string][]byte),
		PreviewErrors:   make(map[string]string),
	}
	m.status = "Link picker open"
	return m, m.codexArtifactPickerPreviewCmd()
}

func (m Model) updateCodexArtifactPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	picker := m.codexArtifactPicker
	if picker == nil {
		return m, nil
	}
	if len(picker.Targets) == 0 {
		m.closeCodexArtifactPicker("No openable links in this embedded transcript")
		return m, nil
	}
	m.normalizeCodexArtifactPickerSelection()
	switch msg.String() {
	case "esc":
		m.closeCodexArtifactPicker("Link picker closed")
		return m, nil
	case "up":
		if codexArtifactPickerFilteredCount(picker) > 0 {
			picker.Selected = max(0, picker.Selected-1)
		}
		return m, m.codexArtifactPickerPreviewCmd()
	case "down":
		if count := codexArtifactPickerFilteredCount(picker); count > 0 {
			picker.Selected = min(count-1, picker.Selected+1)
		}
		return m, m.codexArtifactPickerPreviewCmd()
	case "pgup", "ctrl+u":
		if codexArtifactPickerFilteredCount(picker) > 0 {
			picker.Selected = max(0, picker.Selected-5)
		}
		return m, m.codexArtifactPickerPreviewCmd()
	case "pgdown", "ctrl+d":
		if count := codexArtifactPickerFilteredCount(picker); count > 0 {
			picker.Selected = min(count-1, picker.Selected+5)
		}
		return m, m.codexArtifactPickerPreviewCmd()
	case "home":
		picker.Selected = 0
		return m, m.codexArtifactPickerPreviewCmd()
	case "end":
		if count := codexArtifactPickerFilteredCount(picker); count > 0 {
			picker.Selected = count - 1
		}
		return m, m.codexArtifactPickerPreviewCmd()
	case "enter", "alt+o":
		target, ok := m.currentCodexArtifactTarget()
		if !ok {
			m.status = "No links match the current filter"
			return m, nil
		}
		m.closeCodexArtifactPicker("Opening " + codexArtifactTargetDisplay(target))
		return m, m.openCodexLinkTargetCmd(target)
	case "alt+f":
		target, ok := m.currentCodexArtifactTarget()
		if !ok {
			m.status = "No links match the current filter"
			return m, nil
		}
		if strings.TrimSpace(target.Kind) == "url" {
			m.status = "Links do not have containing folders"
			return m, nil
		}
		m.closeCodexArtifactPicker("Opening folder for " + codexArtifactTargetDisplay(target))
		return m, m.openCodexLinkTargetFolderCmd(target)
	case "backspace", "ctrl+h":
		if picker.Filter != "" {
			runes := []rune(picker.Filter)
			picker.Filter = string(runes[:len(runes)-1])
			m.normalizeCodexArtifactPickerSelection()
			return m, m.codexArtifactPickerPreviewCmd()
		}
		return m, nil
	case "delete":
		if picker.Filter != "" {
			picker.Filter = ""
			m.normalizeCodexArtifactPickerSelection()
			return m, m.codexArtifactPickerPreviewCmd()
		}
		return m, nil
	}
	if msg.Type == tea.KeyRunes && !msg.Alt {
		picker.Filter += string(msg.Runes)
		m.normalizeCodexArtifactPickerSelection()
		return m, m.codexArtifactPickerPreviewCmd()
	}
	return m, nil
}

func (m Model) visibleCodexOpenTargets(snapshot codexapp.Snapshot) []codexArtifactOpenTarget {
	return m.visibleCodexOpenTargetsWithCachePolicy(snapshot, true)
}

func (m Model) cachedVisibleCodexOpenTargets(snapshot codexapp.Snapshot) []codexArtifactOpenTarget {
	return m.visibleCodexOpenTargetsWithCachePolicy(snapshot, false)
}

func (m Model) codexOpenTargetsForPicker(snapshot codexapp.Snapshot) []codexArtifactOpenTarget {
	visibleTargets := m.visibleCodexOpenTargets(snapshot)
	progressiveTargets, progressiveComplete := m.cachedProgressiveCodexOpenTargetsWithState(snapshot)
	if len(progressiveTargets) == 0 {
		return normalizeCodexArtifactOpenTargets(visibleTargets)
	}
	if progressiveComplete {
		return normalizeCodexArtifactOpenTargets(progressiveTargets)
	}
	return normalizeCodexArtifactOpenTargets(append(progressiveTargets, visibleTargets...))
}

func (m Model) cachedCodexOpenTargetsForPicker(snapshot codexapp.Snapshot) []codexArtifactOpenTarget {
	visibleTargets := m.cachedVisibleCodexOpenTargets(snapshot)
	progressiveTargets, progressiveComplete := m.cachedProgressiveCodexOpenTargetsWithState(snapshot)
	if len(progressiveTargets) == 0 {
		return normalizeCodexArtifactOpenTargets(visibleTargets)
	}
	if progressiveComplete {
		return normalizeCodexArtifactOpenTargets(progressiveTargets)
	}
	return normalizeCodexArtifactOpenTargets(append(progressiveTargets, visibleTargets...))
}

func (m Model) cachedProgressiveCodexOpenTargets(snapshot codexapp.Snapshot) []codexArtifactOpenTarget {
	targets, _ := m.cachedProgressiveCodexOpenTargetsWithState(snapshot)
	return targets
}

func (m Model) cachedProgressiveCodexOpenTargetsWithState(snapshot codexapp.Snapshot) ([]codexArtifactOpenTarget, bool) {
	projectPath := strings.TrimSpace(firstNonEmptyString(snapshot.ProjectPath, m.codexVisibleProject))
	if projectPath == "" {
		return nil, false
	}
	state, ok := m.codexArtifactLinkScans[projectPath]
	if !ok || state.transcriptRev != m.codexTranscriptRevision(projectPath) || len(state.targets) == 0 {
		return nil, false
	}
	return append([]codexArtifactOpenTarget(nil), state.targets...), state.complete
}

func (m Model) codexArtifactLinkScanInFlight(projectPath string, transcriptRev uint64) bool {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return false
	}
	state, ok := m.codexArtifactLinkScans[projectPath]
	return ok && state.transcriptRev == transcriptRev && state.inFlight
}

func (m Model) visibleCodexOpenTargetsWithCachePolicy(snapshot codexapp.Snapshot, renderOnMiss bool) []codexArtifactOpenTarget {
	projectPath := strings.TrimSpace(firstNonEmptyString(snapshot.ProjectPath, m.codexVisibleProject))
	width := m.codexViewport.Width
	if width <= 0 {
		width = m.width
	}
	if width <= 0 {
		width = 80
	}
	if projectPath == "" || !m.codexTranscriptCacheMatches(projectPath, width) {
		if !renderOnMiss {
			return nil
		}
		_ = m.renderAndCacheCodexTranscript(projectPath, snapshot, width)
	}
	links := m.codexTranscriptCache.links
	if len(links) == 0 {
		return nil
	}
	if m.codexViewport.Height <= 0 {
		return codexTargetsFromLinkSpans(links)
	}
	startLine := max(0, m.codexViewport.YOffset)
	endLine := startLine + max(1, m.codexViewport.Height)
	targets := make([]codexArtifactOpenTarget, 0, len(links))
	for _, link := range links {
		if link.EndLine <= startLine || link.StartLine >= endLine {
			continue
		}
		targets = append(targets, link.Target)
	}
	return targets
}

func codexTargetsFromLinkSpans(links []codexTranscriptLinkSpan) []codexArtifactOpenTarget {
	targets := make([]codexArtifactOpenTarget, 0, len(links))
	for _, link := range links {
		targets = append(targets, link.Target)
	}
	return targets
}

const (
	codexArtifactLinkScanEntryBudget = 24
	codexArtifactLinkScanByteBudget  = 128 * 1024
	codexInlineCodePathScanLimit     = codexMarkdownLinkTargetScanLimit
)

func (m *Model) maybeStartCodexArtifactLinkScan(projectPath string, snapshot codexapp.Snapshot) tea.Cmd {
	return m.maybeStartCodexArtifactLinkScanWithPolicy(projectPath, snapshot, false)
}

func (m *Model) maybeStartCodexArtifactLinkScanForPicker(projectPath string, snapshot codexapp.Snapshot) tea.Cmd {
	return m.maybeStartCodexArtifactLinkScanWithPolicy(projectPath, snapshot, true)
}

func (m *Model) maybeStartCodexArtifactLinkScanWithPolicy(projectPath string, snapshot codexapp.Snapshot, allowBusy bool) tea.Cmd {
	projectPath = strings.TrimSpace(firstNonEmptyString(projectPath, snapshot.ProjectPath, m.codexVisibleProject))
	if projectPath == "" {
		return nil
	}
	if snapshot.Busy && !allowBusy && !m.codexArtifactPickerOpenForProject(projectPath) {
		return nil
	}
	transcriptRev := m.codexTranscriptRevision(projectPath)
	entries := codexTranscriptEntriesFromSnapshot(snapshot)
	if m.codexArtifactLinkScans == nil {
		m.codexArtifactLinkScans = make(map[string]codexArtifactLinkScanState)
	}
	state := m.codexArtifactLinkScans[projectPath]
	if state.transcriptRev != transcriptRev {
		state = codexArtifactLinkScanState{transcriptRev: transcriptRev}
	}
	if len(entries) == 0 {
		state.complete = true
		state.inFlight = false
		m.codexArtifactLinkScans[projectPath] = state
		return nil
	}
	if state.complete || state.inFlight {
		m.codexArtifactLinkScans[projectPath] = state
		return nil
	}
	state.inFlight = true
	m.codexArtifactLinkScanSeq++
	state.scanSeq = m.codexArtifactLinkScanSeq
	startEntry := max(0, state.nextEntry)
	startOffset := max(0, state.nextTextOffset)
	m.codexArtifactLinkScans[projectPath] = state
	return codexArtifactLinkScanCmd(projectPath, state.scanSeq, transcriptRev, entries, startEntry, startOffset)
}

func (m Model) codexArtifactPickerOpenForProject(projectPath string) bool {
	projectPath = strings.TrimSpace(projectPath)
	return projectPath != "" && m.codexArtifactPicker != nil && strings.TrimSpace(m.codexArtifactPicker.ProjectPath) == projectPath
}

func codexArtifactLinkScanCmd(projectPath string, scanSeq int64, transcriptRev uint64, entries []codexapp.TranscriptEntry, startEntry, startTextOffset int) tea.Cmd {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return nil
	}
	entries = append([]codexapp.TranscriptEntry(nil), entries...)
	return func() tea.Msg {
		targets, nextEntry, nextTextOffset, complete := scanCodexArtifactLinksChunk(projectPath, entries, startEntry, startTextOffset)
		return codexArtifactLinkScanMsg{
			projectPath:    projectPath,
			scanSeq:        scanSeq,
			transcriptRev:  transcriptRev,
			nextEntry:      nextEntry,
			nextTextOffset: nextTextOffset,
			complete:       complete,
			targets:        targets,
		}
	}
}

func scanCodexArtifactLinksChunk(projectPath string, entries []codexapp.TranscriptEntry, startEntry, startTextOffset int) ([]codexArtifactOpenTarget, int, int, bool) {
	if len(entries) == 0 {
		return nil, 0, 0, true
	}
	entryIndex := max(0, startEntry)
	textOffset := max(0, startTextOffset)
	if entryIndex >= len(entries) {
		return nil, len(entries), 0, true
	}
	targets := make([]codexArtifactOpenTarget, 0)
	entriesScanned := 0
	bytesScanned := 0
	for entryIndex < len(entries) && entriesScanned < codexArtifactLinkScanEntryBudget && bytesScanned < codexArtifactLinkScanByteBudget {
		entry := entries[entryIndex]
		if textOffset == 0 {
			if target, ok := codexGeneratedImageOpenTarget(entry.GeneratedImage); ok {
				targets = append(targets, target)
			}
		}
		text := codexFullTranscriptEntryLinkScanText(entry)
		if textOffset >= len(text) {
			entryIndex++
			textOffset = 0
			entriesScanned++
			continue
		}
		remainingBudget := codexArtifactLinkScanByteBudget - bytesScanned
		if remainingBudget <= 0 {
			break
		}
		scanLen := min(len(text)-textOffset, remainingBudget)
		parseEnd := min(len(text), textOffset+scanLen+codexMarkdownLinkLabelScanLimit+max(codexMarkdownLinkTargetScanLimit, codexInlineCodePathScanLimit)+4)
		targets = append(targets, codexArtifactOpenTargetsFromMarkdownPrefixInProject(text[textOffset:parseEnd], scanLen, projectPath)...)
		bytesScanned += scanLen
		if textOffset+scanLen < len(text) {
			textOffset += scanLen
			return normalizeCodexArtifactOpenTargets(targets), entryIndex, textOffset, false
		}
		entryIndex++
		textOffset = 0
		entriesScanned++
	}
	complete := entryIndex >= len(entries)
	return normalizeCodexArtifactOpenTargets(targets), entryIndex, textOffset, complete
}

func (m Model) applyCodexArtifactLinkScanMsg(msg codexArtifactLinkScanMsg) (tea.Model, tea.Cmd) {
	projectPath := strings.TrimSpace(msg.projectPath)
	if projectPath == "" || msg.transcriptRev != m.codexTranscriptRevision(projectPath) {
		return m, nil
	}
	if m.codexArtifactLinkScans == nil {
		m.codexArtifactLinkScans = make(map[string]codexArtifactLinkScanState)
	}
	state := m.codexArtifactLinkScans[projectPath]
	if state.transcriptRev != msg.transcriptRev || state.scanSeq != msg.scanSeq || !state.inFlight {
		return m, nil
	}
	state.targets = normalizeCodexArtifactOpenTargets(append(state.targets, msg.targets...))
	state.nextEntry = max(0, msg.nextEntry)
	state.nextTextOffset = max(0, msg.nextTextOffset)
	state.complete = msg.complete
	state.inFlight = false
	m.codexArtifactLinkScans[projectPath] = state

	previewCmd := tea.Cmd(nil)
	if picker := m.codexArtifactPicker; picker != nil && strings.TrimSpace(picker.ProjectPath) == projectPath {
		previousCount := len(picker.Targets)
		previousFilteredCount := codexArtifactPickerFilteredCount(picker)
		wasAtLatest := previousFilteredCount == 0 || picker.Selected >= previousFilteredCount-1
		picker.Targets = normalizeCodexArtifactOpenTargets(append(picker.Targets, msg.targets...))
		filteredCount := codexArtifactPickerFilteredCount(picker)
		if wasAtLatest && filteredCount > 0 {
			picker.Selected = filteredCount - 1
		}
		if previousCount == 0 && len(picker.Targets) > 0 {
			picker.Hint = "Links found in this embedded transcript. Type to filter by name."
			m.status = "Link picker open"
			previewCmd = m.codexArtifactPickerPreviewCmd()
		} else if wasAtLatest && filteredCount > previousFilteredCount {
			previewCmd = m.codexArtifactPickerPreviewCmd()
		}
		if len(picker.Targets) == 0 && state.complete {
			m.closeCodexArtifactPicker("No openable links in this embedded transcript")
		}
	}

	nextScanCmd := tea.Cmd(nil)
	if !state.complete {
		if snapshot, ok := m.codexCachedSnapshot(projectPath); ok {
			nextScanCmd = m.maybeStartCodexArtifactLinkScan(projectPath, snapshot)
		}
	}
	return m, batchCmds(previewCmd, nextScanCmd)
}

func (m *Model) closeCodexArtifactPicker(status string) {
	m.codexArtifactPicker = nil
	if strings.TrimSpace(status) != "" {
		m.status = status
	}
}

func codexArtifactPickerAllowed(snapshot codexapp.Snapshot) bool {
	return !codexSnapshotHasPendingUserResponse(snapshot)
}

func codexSnapshotCanSettlePendingOpen(snapshot codexapp.Snapshot) bool {
	return !snapshot.Closed && (snapshot.Started ||
		strings.TrimSpace(snapshot.ThreadID) != "" ||
		strings.TrimSpace(snapshot.Status) != "" ||
		codexSnapshotHasPendingUserResponse(snapshot))
}

func codexSnapshotHasPendingUserResponse(snapshot codexapp.Snapshot) bool {
	return snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil
}

func (m *Model) codexArtifactPickerPreviewCmd() tea.Cmd {
	picker := m.codexArtifactPicker
	if picker == nil {
		return nil
	}
	target, ok := m.currentCodexArtifactTarget()
	if !ok {
		return nil
	}
	path := strings.TrimSpace(target.Path)
	if strings.TrimSpace(target.Kind) != "image" || path == "" || len(target.PreviewData) > 0 {
		return nil
	}
	if len(picker.PreviewData[path]) > 0 || strings.TrimSpace(picker.PreviewErrors[path]) != "" {
		return nil
	}
	if picker.PreviewRequests == nil {
		picker.PreviewRequests = make(map[string]int64)
	}
	if picker.PreviewRequests[path] > 0 {
		return nil
	}
	picker.PreviewSeq++
	seq := picker.PreviewSeq
	picker.PreviewRequests[path] = seq
	projectPath := strings.TrimSpace(picker.ProjectPath)
	return loadCodexArtifactPreviewCmd(projectPath, path, seq)
}

const maxCodexArtifactPreviewBytes = 25 * 1024 * 1024

func loadCodexArtifactPreviewCmd(projectPath, path string, seq int64) tea.Cmd {
	return func() tea.Msg {
		info, err := os.Stat(path)
		if err != nil {
			return codexArtifactPreviewMsg{projectPath: projectPath, path: path, seq: seq, err: fmt.Errorf("inspect preview: %w", err)}
		}
		if info.IsDir() {
			return codexArtifactPreviewMsg{projectPath: projectPath, path: path, seq: seq, err: fmt.Errorf("path is a directory")}
		}
		if info.Size() > maxCodexArtifactPreviewBytes {
			return codexArtifactPreviewMsg{projectPath: projectPath, path: path, seq: seq, err: fmt.Errorf("image is too large for preview")}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return codexArtifactPreviewMsg{projectPath: projectPath, path: path, seq: seq, err: fmt.Errorf("read preview: %w", err)}
		}
		return codexArtifactPreviewMsg{projectPath: projectPath, path: path, seq: seq, data: data}
	}
}

func (m Model) applyCodexArtifactPreviewMsg(msg codexArtifactPreviewMsg) (tea.Model, tea.Cmd) {
	picker := m.codexArtifactPicker
	if picker == nil {
		return m, nil
	}
	path := strings.TrimSpace(msg.path)
	if path == "" || strings.TrimSpace(msg.projectPath) != strings.TrimSpace(picker.ProjectPath) {
		return m, nil
	}
	if picker.PreviewRequests == nil || picker.PreviewRequests[path] != msg.seq {
		return m, nil
	}
	delete(picker.PreviewRequests, path)
	if picker.PreviewData == nil {
		picker.PreviewData = make(map[string][]byte)
	}
	if picker.PreviewErrors == nil {
		picker.PreviewErrors = make(map[string]string)
	}
	if msg.err != nil {
		picker.PreviewErrors[path] = strings.TrimSpace(msg.err.Error())
		return m, nil
	}
	delete(picker.PreviewErrors, path)
	picker.PreviewData[path] = append([]byte(nil), msg.data...)
	return m, nil
}

func codexArtifactTargetDisplay(target codexArtifactOpenTarget) string {
	label := strings.TrimSpace(target.Label)
	if strings.TrimSpace(target.Kind) == "url" {
		right := codexArtifactTargetRight(target)
		if label == "" || label == strings.TrimSpace(target.Path) {
			return right
		}
		if right == "" || label == right {
			return label
		}
		return label + " (" + right + ")"
	}
	base := filepath.Base(strings.TrimSpace(target.Path))
	if label == "" || label == base {
		return base
	}
	return label + " (" + base + ")"
}

func codexArtifactTargetRight(target codexArtifactOpenTarget) string {
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
	return filepath.Base(path)
}

func (m *Model) normalizeCodexArtifactPickerSelection() {
	picker := m.codexArtifactPicker
	if picker == nil {
		return
	}
	count := codexArtifactPickerFilteredCount(picker)
	if count == 0 {
		picker.Selected = 0
		return
	}
	if picker.Selected < 0 {
		picker.Selected = 0
	}
	if picker.Selected >= count {
		picker.Selected = count - 1
	}
}

func codexArtifactPickerFilteredCount(picker *codexArtifactPickerState) int {
	return len(codexArtifactPickerFilteredIndexes(picker))
}

func codexArtifactPickerFilteredIndexes(picker *codexArtifactPickerState) []int {
	if picker == nil || len(picker.Targets) == 0 {
		return nil
	}
	filter := strings.TrimSpace(picker.Filter)
	indexes := make([]int, 0, len(picker.Targets))
	for i, target := range picker.Targets {
		if codexArtifactTargetMatchesFilter(target, filter) {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func codexArtifactTargetMatchesFilter(target codexArtifactOpenTarget, filter string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	if strings.TrimSpace(target.Kind) == "url" {
		return fuzzyfilter.Match(filter, codexArtifactTargetName(target), strings.TrimSpace(target.Label), codexArtifactTargetRight(target))
	}
	return fuzzyfilter.Match(filter, codexArtifactTargetName(target), strings.TrimSpace(target.Label))
}

func (m Model) currentCodexArtifactTarget() (codexArtifactOpenTarget, bool) {
	picker := m.codexArtifactPicker
	if picker == nil || len(picker.Targets) == 0 {
		return codexArtifactOpenTarget{}, false
	}
	indexes := codexArtifactPickerFilteredIndexes(picker)
	if len(indexes) == 0 {
		return codexArtifactOpenTarget{}, false
	}
	index := picker.Selected
	if index < 0 {
		index = 0
	}
	if index >= len(indexes) {
		index = len(indexes) - 1
	}
	return picker.Targets[indexes[index]], true
}

func (m Model) renderCodexArtifactPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderCodexArtifactPicker(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, min((bodyH-panelHeight)/5, bodyH-panelHeight))
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCodexArtifactPicker(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(72, bodyW-8), 120))
	panelInnerWidth := max(28, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderCodexArtifactPickerContent(panelInnerWidth, bodyH))
}

func (m Model) renderCodexArtifactPickerContent(width, bodyH int) string {
	picker := m.codexArtifactPicker
	if picker == nil {
		return ""
	}
	title := strings.TrimSpace(picker.Title)
	if title == "" {
		title = "Open Links"
	}
	hint := strings.TrimSpace(picker.Hint)
	if hint == "" {
		hint = "Links found in this embedded transcript."
	}
	lines := []string{
		commandPaletteTitleStyle.Render(title),
		commandPaletteHintStyle.Render(hint),
		"",
		renderCodexArtifactPickerFilterLine(picker, width),
		"",
		renderDialogAction("Enter/Alt+O", "open", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Alt+F", "folder", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("↑↓", "select", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
		"",
	}
	if len(picker.Targets) == 0 {
		lines = append(lines, commandPaletteHintStyle.Render("No links found."))
		return strings.Join(lines, "\n")
	}
	indexes := codexArtifactPickerFilteredIndexes(picker)
	if len(indexes) == 0 {
		lines = append(lines, commandPaletteHintStyle.Render("No names match the current filter."))
		return strings.Join(lines, "\n")
	}
	selected := picker.Selected
	if selected < 0 {
		selected = 0
	}
	if selected >= len(indexes) {
		selected = len(indexes) - 1
	}
	start, end := codexArtifactPickerWindow(selected, len(indexes), bodyH)
	layout := newCodexArtifactPickerRowLayout(width)
	lines = append(lines, renderCodexArtifactPickerHeader(layout, width))
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d more", start)))
	}
	for i := start; i < end; i++ {
		lines = append(lines, renderCodexArtifactPickerRow(picker.Targets[indexes[i]], i == selected, width, layout))
	}
	if end < len(indexes) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more", len(indexes)-end)))
	}
	if selected, ok := m.currentCodexArtifactTarget(); ok {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("Selected"))
		lines = append(lines, renderCodexArtifactSelectedDetails(selected, width)...)
		if preview := strings.TrimSpace(m.renderCodexArtifactPreview(selected, width, bodyH)); preview != "" {
			lines = append(lines, "")
			lines = append(lines, commandPaletteTitleStyle.Render("Preview"))
			lines = append(lines, preview)
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderCodexArtifactPreview(target codexArtifactOpenTarget, width, bodyH int) string {
	if strings.TrimSpace(target.Kind) != "image" {
		return ""
	}
	data := target.PreviewData
	path := strings.TrimSpace(target.Path)
	picker := m.codexArtifactPicker
	if len(data) == 0 && picker != nil && path != "" {
		data = picker.PreviewData[path]
	}
	maxRows := max(3, min(8, bodyH/4))
	if len(data) > 0 {
		return renderANSIImagePreview(data, max(12, width), maxRows)
	}
	if picker != nil && path != "" {
		if errText := strings.TrimSpace(picker.PreviewErrors[path]); errText != "" {
			return commandPaletteHintStyle.Render("Preview unavailable: " + truncateText(errText, max(20, width-22)))
		}
		if picker.PreviewRequests[path] > 0 {
			return commandPaletteHintStyle.Render("Loading preview...")
		}
	}
	return commandPaletteHintStyle.Render("Preview unavailable.")
}

func codexArtifactPickerWindow(selected, total, bodyH int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if bodyH <= 0 {
		bodyH = 30
	}
	limit := min(total, max(3, min(8, bodyH-20)))
	start := 0
	if selected >= limit {
		start = selected - limit + 1
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

type codexArtifactPickerRowLayout struct {
	NameWidth int
	TypeWidth int
	PathWidth int
}

func newCodexArtifactPickerRowLayout(width int) codexArtifactPickerRowLayout {
	typeWidth := 7
	gutterWidth := 2
	gapWidth := 4
	available := max(24, width-gutterWidth-gapWidth-typeWidth)
	nameWidth := max(18, min(68, available*42/100))
	pathWidth := max(10, available-nameWidth)
	return codexArtifactPickerRowLayout{
		NameWidth: nameWidth,
		TypeWidth: typeWidth,
		PathWidth: pathWidth,
	}
}

func renderCodexArtifactPickerFilterLine(picker *codexArtifactPickerState, width int) string {
	filter := strings.TrimSpace(picker.Filter)
	value := detailMutedStyle.Render("type to filter by name")
	if filter != "" {
		value = detailValueStyle.Render(fitFooterWidth(filter, max(8, width-10)))
	}
	count := codexArtifactPickerFilteredCount(picker)
	total := len(picker.Targets)
	summary := fmt.Sprintf("%d/%d", count, total)
	return fitFooterWidth(detailField("Filter", value)+"  "+detailMutedStyle.Render(summary), width)
}

func renderCodexArtifactSelectedDetails(target codexArtifactOpenTarget, width int) []string {
	lines := []string{
		detailField("Name", detailValueStyle.Render(fitFooterWidth(codexArtifactTargetName(target), max(8, width-8)))),
		detailField("Type", detailValueStyle.Render(codexArtifactTargetTypeLabel(target))),
	}
	label := strings.TrimSpace(target.Label)
	if label != "" && label != codexArtifactTargetName(target) {
		lines = append(lines, detailField("Mention", detailMutedStyle.Render(fitFooterWidth(label, max(8, width-11)))))
	}
	if path := strings.TrimSpace(target.Path); path != "" {
		lines = append(lines, renderWrappedDetailField("Path", detailMutedStyle, width, path))
	}
	return lines
}

func renderCodexArtifactPickerHeader(layout codexArtifactPickerRowLayout, width int) string {
	row := fmt.Sprintf("  %s  %s  %s",
		renderCodexArtifactCell("Name", layout.NameWidth, codexArtifactHeaderStyle()),
		renderCodexArtifactCell("Type", layout.TypeWidth, codexArtifactHeaderStyle()),
		renderCodexArtifactCell("Path", layout.PathWidth, codexArtifactHeaderStyle()),
	)
	return fitStyledWidth(row, width)
}

func renderCodexArtifactPickerRow(target codexArtifactOpenTarget, selected bool, width int, layout codexArtifactPickerRowLayout) string {
	kind := codexArtifactTargetTypeLabel(target)
	name := codexArtifactTargetName(target)
	path := strings.TrimSpace(target.Path)
	if path == "" {
		path = codexArtifactTargetRight(target)
	}
	row := fmt.Sprintf("  %s  %s  %s",
		renderCodexArtifactCell(name, layout.NameWidth, codexArtifactNameStyle(selected)),
		renderCodexArtifactCell(kind, layout.TypeWidth, codexArtifactTypeStyle(kind, selected)),
		renderCodexArtifactCell(shortenHeadTail(path, layout.PathWidth), layout.PathWidth, codexArtifactPathStyle(selected)),
	)
	if selected {
		row = "> " + strings.TrimPrefix(row, "  ")
		return codexArtifactSelectedRowStyle().Width(width).Render(row)
	}
	return commandPaletteRowStyle.Width(width).Render(row)
}

func renderCodexArtifactCell(value string, width int, style lipgloss.Style) string {
	return fitStyledWidth(style.Render(fitFooterWidth(value, width)), width)
}

func codexArtifactHeaderStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
}

func codexArtifactNameStyle(selected bool) lipgloss.Style {
	if selected {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
}

func codexArtifactPathStyle(selected bool) lipgloss.Style {
	if selected {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Bold(true)
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
}

func codexArtifactSelectedRowStyle() lipgloss.Style {
	return lipgloss.NewStyle().Background(lipgloss.Color("24")).Bold(true)
}

func codexArtifactTypeStyle(kind string, selected bool) lipgloss.Style {
	if selected {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
	}
	color := lipgloss.Color("153")
	switch strings.ToUpper(strings.TrimSpace(kind)) {
	case "DIR":
		color = lipgloss.Color("214")
	case "DOC":
		color = lipgloss.Color("111")
	case "HTML":
		color = lipgloss.Color("42")
	case "IMAGE":
		color = lipgloss.Color("213")
	case "PDF":
		color = lipgloss.Color("203")
	case "SOURCE":
		color = lipgloss.Color("149")
	case "URL":
		color = lipgloss.Color("81")
	}
	return lipgloss.NewStyle().Foreground(color).Bold(true)
}

func codexArtifactTargetName(target codexArtifactOpenTarget) string {
	path := strings.TrimSpace(target.Path)
	if strings.TrimSpace(target.Kind) == "url" {
		label := strings.TrimSpace(target.Label)
		if label != "" && label != path {
			return label
		}
		if right := codexArtifactTargetRight(target); right != "" {
			return right
		}
		return path
	}
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) || base == "" {
		if label := strings.TrimSpace(target.Label); label != "" {
			return label
		}
		return path
	}
	return base
}

func codexArtifactTargetTypeLabel(target codexArtifactOpenTarget) string {
	switch strings.ToLower(strings.TrimSpace(target.Kind)) {
	case "dir":
		return "DIR"
	case "image":
		return "IMAGE"
	case "source":
		return "SOURCE"
	case "url":
		return "URL"
	case "pdf":
		return "PDF"
	case "":
		return "FILE"
	default:
		return strings.ToUpper(strings.TrimSpace(target.Kind))
	}
}

func shortenHeadTail(text string, width int) string {
	text = strings.TrimSpace(text)
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	if width <= 2 {
		return ansi.Cut(text, 0, width)
	}
	if width <= 4 {
		return ansi.Truncate(text, width, "..")
	}
	runes := []rune(text)
	innerWidth := width - 2
	headWidth := max(1, innerWidth/3)
	tailWidth := max(1, innerWidth-headWidth)
	if headWidth+tailWidth >= len(runes) {
		return truncateText(text, width)
	}
	return string(runes[:headWidth]) + ".." + string(runes[len(runes)-tailWidth:])
}

func codexArtifactOpenTargets(snapshot codexapp.Snapshot) []codexArtifactOpenTarget {
	targets := make([]codexArtifactOpenTarget, 0)
	projectPath := strings.TrimSpace(snapshot.ProjectPath)
	for _, entry := range codexTranscriptEntriesFromSnapshot(snapshot) {
		targets = append(targets, codexOpenTargetsFromTranscriptEntryFullInProject(entry, projectPath)...)
	}
	return normalizeCodexArtifactOpenTargets(targets)
}

func codexOpenTargetsFromTranscriptEntry(entry codexapp.TranscriptEntry) []codexArtifactOpenTarget {
	return codexOpenTargetsFromTranscriptEntryForBlockMode(entry, codexDenseBlockSummary)
}

func codexOpenTargetsFromTranscriptEntryForBlockMode(entry codexapp.TranscriptEntry, blockMode codexDenseBlockMode) []codexArtifactOpenTarget {
	return codexOpenTargetsFromTranscriptEntryForBlockModeInProject(entry, blockMode, "")
}

func codexOpenTargetsFromTranscriptEntryForBlockModeInProject(entry codexapp.TranscriptEntry, blockMode codexDenseBlockMode, projectPath string) []codexArtifactOpenTarget {
	if target, ok := codexGeneratedImageOpenTarget(entry.GeneratedImage); ok {
		return []codexArtifactOpenTarget{{
			Kind:        target.Kind,
			Label:       target.Label,
			Path:        target.Path,
			PreviewData: append([]byte(nil), target.PreviewData...),
		}}
	}
	text, ok := codexTranscriptEntryLinkScanText(entry, blockMode)
	if !ok {
		return nil
	}
	return codexArtifactOpenTargetsFromMarkdownInProject(text, projectPath)
}

func codexOpenTargetsFromTranscriptEntryFull(entry codexapp.TranscriptEntry) []codexArtifactOpenTarget {
	return codexOpenTargetsFromTranscriptEntryFullInProject(entry, "")
}

func codexOpenTargetsFromTranscriptEntryFullInProject(entry codexapp.TranscriptEntry, projectPath string) []codexArtifactOpenTarget {
	targets := make([]codexArtifactOpenTarget, 0, 1)
	if target, ok := codexGeneratedImageOpenTarget(entry.GeneratedImage); ok {
		targets = append(targets, target)
	}
	if text := codexFullTranscriptEntryLinkScanText(entry); strings.TrimSpace(text) != "" {
		targets = append(targets, codexArtifactOpenTargetsFromMarkdownInProject(text, projectPath)...)
	}
	return normalizeCodexArtifactOpenTargets(targets)
}

func codexGeneratedImageOpenTarget(image *codexapp.GeneratedImageArtifact) (codexArtifactOpenTarget, bool) {
	if image == nil {
		return codexArtifactOpenTarget{}, false
	}
	path := strings.TrimSpace(image.Path)
	if path == "" {
		path = strings.TrimSpace(image.SourcePath)
	}
	if path == "" {
		return codexArtifactOpenTarget{}, false
	}
	return codexArtifactOpenTarget{
		Kind:        "image",
		Label:       "Generated image",
		Path:        path,
		PreviewData: append([]byte(nil), image.PreviewData...),
	}, true
}

func codexFullTranscriptEntryLinkScanText(entry codexapp.TranscriptEntry) string {
	if entry.Kind == codexapp.TranscriptUser {
		if displayText := strings.TrimSpace(entry.DisplayText); displayText != "" {
			return displayText
		}
	}
	return entry.Text
}

func codexTranscriptEntryLinkScanText(entry codexapp.TranscriptEntry, blockMode codexDenseBlockMode) (string, bool) {
	if entry.Kind == codexapp.TranscriptUser {
		if displayText := strings.TrimSpace(entry.DisplayText); displayText != "" {
			return displayText, true
		}
	}
	switch entry.Kind {
	case codexapp.TranscriptCommand, codexapp.TranscriptFileChange:
		return "", false
	case codexapp.TranscriptTool, codexapp.TranscriptReasoning:
		return "", false
	default:
		return entry.Text, true
	}
}

func codexArtifactOpenTargetsFromMarkdown(text string) []codexArtifactOpenTarget {
	return codexArtifactOpenTargetsFromMarkdownInProject(text, "")
}

func codexArtifactOpenTargetsFromMarkdownInProject(text, projectPath string) []codexArtifactOpenTarget {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return codexArtifactOpenTargetsFromMarkdownPrefixInProject(text, len(text), projectPath)
}

func codexArtifactOpenTargetsFromMarkdownPrefix(text string, scanLimit int) []codexArtifactOpenTarget {
	return codexArtifactOpenTargetsFromMarkdownPrefixInProject(text, scanLimit, "")
}

func codexArtifactOpenTargetsFromMarkdownPrefixInProject(text string, scanLimit int, projectPath string) []codexArtifactOpenTarget {
	if scanLimit <= 0 || strings.TrimSpace(text) == "" {
		return nil
	}
	if scanLimit > len(text) {
		scanLimit = len(text)
	}
	targets := make([]codexArtifactOpenTarget, 0)
	remaining := text
	remainingScanLimit := scanLimit
	for len(remaining) > 0 && remainingScanLimit > 0 {
		scanWindow := remaining[:min(len(remaining), remainingScanLimit)]
		linkIdx := strings.IndexByte(scanWindow, '[')
		codeIdx := strings.IndexByte(scanWindow, '`')
		idx := earliestNonNegativeIndex(linkIdx, codeIdx)
		if idx < 0 {
			return targets
		}

		if codeIdx == idx {
			code, consumed, ok := parseCodexInlineCodeSpan(remaining[idx:])
			if !ok {
				advance := idx + max(1, consumed)
				remaining = remaining[advance:]
				remainingScanLimit -= advance
				continue
			}
			if target, ok := codexArtifactOpenTargetFromInlineCodePath(code, projectPath); ok {
				targets = append(targets, target)
			}
			advance := idx + max(1, consumed)
			remaining = remaining[advance:]
			remainingScanLimit -= advance
			continue
		}

		label, target, consumed, ok := parseCodexMarkdownLink(remaining[idx:])
		if !ok {
			remaining = remaining[idx+1:]
			remainingScanLimit -= idx + 1
			continue
		}
		if localPath, ok := codexLocalLinkTextForProject(target, projectPath); ok {
			if artifactPath, kind, ok := codexLocalArtifactOpenTarget(label, localPath); ok {
				targets = append(targets, codexArtifactOpenTarget{Kind: kind, Label: label, Path: artifactPath})
			} else if openPath, _ := codexLocalOpenPath(localPath); strings.TrimSpace(openPath) != "" {
				targets = append(targets, codexArtifactOpenTarget{
					Kind:  codexLocalLinkKind(openPath, localPath),
					Label: codexLocalLinkLabel(label, localPath),
					Path:  openPath,
				})
			}
		} else if externalTarget, ok := codexExternalLinkTarget(target); ok {
			targets = append(targets, codexArtifactOpenTarget{Kind: "url", Label: label, Path: externalTarget})
		}
		advance := idx + max(1, consumed)
		remaining = remaining[advance:]
		remainingScanLimit -= advance
	}
	return targets
}

func earliestNonNegativeIndex(indexes ...int) int {
	earliest := -1
	for _, idx := range indexes {
		if idx >= 0 && (earliest < 0 || idx < earliest) {
			earliest = idx
		}
	}
	return earliest
}

func parseCodexInlineCodeSpan(text string) (code string, consumed int, ok bool) {
	if text == "" || text[0] != '`' {
		return "", 0, false
	}
	runLength := leadingBacktickRunLength(text)
	if runLength != 1 {
		return "", runLength, false
	}
	closeOffset := boundedIndexByte(text[1:], '`', codexInlineCodePathScanLimit)
	if closeOffset <= 0 {
		return "", 1, false
	}
	code = strings.TrimSpace(text[1 : 1+closeOffset])
	if code == "" {
		return "", 1 + closeOffset + 1, false
	}
	return code, 1 + closeOffset + 1, true
}

func leadingBacktickRunLength(text string) int {
	count := 0
	for count < len(text) && text[count] == '`' {
		count++
	}
	return count
}

func codexArtifactOpenTargetFromInlineCodePath(rawPath, projectPath string) (codexArtifactOpenTarget, bool) {
	rawPath = strings.TrimSpace(rawPath)
	if !codexInlineCodePathCandidate(rawPath) {
		return codexArtifactOpenTarget{}, false
	}
	if localPath, ok := codexLocalLinkTextForProject(rawPath, projectPath); ok {
		label := codexLocalLinkLabel("", localPath)
		if artifactPath, kind, ok := codexLocalArtifactOpenTarget(label, localPath); ok {
			return codexArtifactOpenTarget{Kind: kind, Label: label, Path: artifactPath}, true
		}
		if openPath, _ := codexLocalOpenPath(localPath); strings.TrimSpace(openPath) != "" {
			return codexArtifactOpenTarget{
				Kind:  codexLocalLinkKind(openPath, localPath),
				Label: label,
				Path:  openPath,
			}, true
		}
	}
	if externalTarget, ok := codexExternalLinkTarget(rawPath); ok {
		return codexArtifactOpenTarget{Kind: "url", Label: externalTarget, Path: externalTarget}, true
	}
	return codexArtifactOpenTarget{}, false
}

func codexInlineCodePathCandidate(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || strings.ContainsAny(text, "\r\n") {
		return false
	}
	if strings.HasPrefix(text, "/") ||
		strings.HasPrefix(text, "./") ||
		strings.HasPrefix(text, "../") ||
		strings.HasPrefix(text, "file://") ||
		strings.HasPrefix(text, "http://") ||
		strings.HasPrefix(text, "https://") {
		return true
	}
	pathPart, _ := codexLocalOpenPath(text)
	return strings.Contains(filepath.ToSlash(pathPart), "/") || strings.Contains(pathPart, "\\")
}

func normalizeCodexArtifactOpenTargets(targets []codexArtifactOpenTarget) []codexArtifactOpenTarget {
	if len(targets) == 0 {
		return nil
	}
	out := make([]codexArtifactOpenTarget, 0, len(targets))
	for inputIndex, target := range targets {
		path := strings.TrimSpace(target.Path)
		if path == "" {
			continue
		}
		order := target.order
		if order <= 0 {
			order = inputIndex + 1
		}
		target.Kind = strings.TrimSpace(target.Kind)
		target.Label = strings.TrimSpace(target.Label)
		target.Path = path
		target.order = order
		if len(target.PreviewData) > 0 {
			target.PreviewData = append([]byte(nil), target.PreviewData...)
		}
		out = append(out, target)
	}
	return out
}

func codexExternalLinkTarget(target string) (string, bool) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", false
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" || parsed.Scheme == "file" {
		return "", false
	}
	return parsed.String(), true
}

func codexLocalLinkKind(openPath, rawPath string) string {
	if _, location := codexLocalOpenPath(rawPath); location != "" {
		return "source"
	}
	if kind := codexArtifactKindForPath(openPath); kind != "" {
		return kind
	}
	return "file"
}

func normalizedCodexStatus(status string) string {
	status = strings.TrimSpace(status)
	switch status {
	case "", "Codex session ready", "OpenCode session ready":
		return ""
	case "Codex turn complete", "Codex turn completed", "OpenCode turn complete", "OpenCode turn completed", "Turn completed":
		return "Turn completed"
	default:
		for _, prefix := range []string{"Codex turn ", "OpenCode turn "} {
			if strings.HasPrefix(status, prefix) {
				return "Turn " + strings.TrimSpace(strings.TrimPrefix(status, prefix))
			}
		}
		return status
	}
}

func codexStatusIsCompacting(status string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(status)), "compacting conversation history")
}
