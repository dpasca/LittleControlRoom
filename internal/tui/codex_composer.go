package tui

import (
	"fmt"
	"sort"
	"strings"

	"lcroom/internal/codexapp"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"
)

const (
	codexLargePasteCharacterThreshold = 500
	codexLargePasteLineThreshold      = 8
)

type codexPastedText struct {
	Token string
	Text  string
}

type codexDraft struct {
	Text        string
	Attachments []codexapp.Attachment
	PastedTexts []codexPastedText
}

func (d codexDraft) normalized() codexDraft {
	d.Attachments = cloneCodexAttachments(d.Attachments)
	d.PastedTexts = pruneCodexPastedTexts(d.Text, d.PastedTexts)
	return d
}

func (d codexDraft) Empty() bool {
	d = d.normalized()
	if strings.TrimSpace(d.Text) != "" {
		return false
	}
	for _, attachment := range d.Attachments {
		if strings.TrimSpace(attachment.Path) != "" {
			return false
		}
	}
	for _, pasted := range d.PastedTexts {
		if strings.TrimSpace(pasted.Token) != "" && pasted.Text != "" {
			return false
		}
	}
	return true
}

func (d codexDraft) Submission() codexapp.Submission {
	d = d.normalized()
	displayText := stripCodexAttachmentComposerTokens(d.Text, d.Attachments)
	sub := codexapp.Submission{
		Text:        expandCodexPastedTextTokens(displayText, d.PastedTexts),
		Attachments: cloneCodexAttachments(d.Attachments),
	}
	if len(d.PastedTexts) > 0 {
		sub.DisplayText = collapseCodexPastedTextTokens(displayText, d.PastedTexts)
	}
	return sub
}

type codexToolAnswerState struct {
	RequestID     string
	QuestionIndex int
	Answers       map[string][]string
}

var (
	codexComposerShellColor      = lipgloss.Color("236")
	codexComposerCursorLineColor = lipgloss.Color("237")
)

func newCodexTextarea() textarea.Model {
	input := textarea.New()
	input.Prompt = "> "
	input.SetPromptFunc(2, func(line int) string {
		if line == 0 {
			return "> "
		}
		return "  "
	})
	input.Placeholder = ""
	input.CharLimit = 10000
	input.SetWidth(72)
	input.SetHeight(3)
	input.ShowLineNumbers = false
	input.KeyMap.InsertNewline.SetEnabled(false)
	styleCodexTextarea(&input)
	return input
}

func styleCodexTextarea(input *textarea.Model) {
	focused := input.FocusedStyle
	focused.Base = focused.Base.Background(codexComposerShellColor).Foreground(lipgloss.Color("252"))
	focused.CursorLine = focused.CursorLine.Background(codexComposerCursorLineColor)
	focused.EndOfBuffer = focused.EndOfBuffer.Foreground(lipgloss.Color("238"))
	focused.Placeholder = focused.Placeholder.Foreground(lipgloss.Color("240"))
	focused.Prompt = focused.Prompt.Foreground(lipgloss.Color("81")).Bold(true)
	focused.Text = focused.Text.Foreground(lipgloss.Color("252"))

	blurred := input.BlurredStyle
	blurred.Base = blurred.Base.Background(codexComposerShellColor).Foreground(lipgloss.Color("252"))
	blurred.CursorLine = blurred.CursorLine.Background(codexComposerShellColor)
	blurred.EndOfBuffer = blurred.EndOfBuffer.Foreground(lipgloss.Color("238"))
	blurred.Placeholder = blurred.Placeholder.Foreground(lipgloss.Color("240"))
	blurred.Prompt = blurred.Prompt.Foreground(lipgloss.Color("244")).Bold(true)
	blurred.Text = blurred.Text.Foreground(lipgloss.Color("252"))

	input.FocusedStyle = focused
	input.BlurredStyle = blurred
}

func cloneCodexAttachments(in []codexapp.Attachment) []codexapp.Attachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]codexapp.Attachment, 0, len(in))
	for _, attachment := range in {
		if strings.TrimSpace(attachment.Path) == "" {
			continue
		}
		out = append(out, attachment)
	}
	return out
}

func cloneCodexPastedTexts(in []codexPastedText) []codexPastedText {
	if len(in) == 0 {
		return nil
	}
	out := make([]codexPastedText, 0, len(in))
	for _, pasted := range in {
		if strings.TrimSpace(pasted.Token) == "" || pasted.Text == "" {
			continue
		}
		out = append(out, pasted)
	}
	return out
}

func pruneCodexPastedTexts(text string, in []codexPastedText) []codexPastedText {
	if len(in) == 0 {
		return nil
	}
	out := make([]codexPastedText, 0, len(in))
	for _, pasted := range in {
		if strings.TrimSpace(pasted.Token) == "" || pasted.Text == "" {
			continue
		}
		if !strings.Contains(text, pasted.Token) {
			continue
		}
		out = append(out, pasted)
	}
	return out
}

func expandCodexPastedTextTokens(text string, pastedTexts []codexPastedText) string {
	expanded := text
	for _, pasted := range pruneCodexPastedTexts(text, pastedTexts) {
		expanded = strings.ReplaceAll(expanded, pasted.Token, pasted.Text)
	}
	return strings.TrimSpace(expanded)
}

func codexPastedTextPlaceholder(text string) string {
	n := codexVisibleLineCount(text)
	if n == 1 {
		return "[1 line pasted]"
	}
	return fmt.Sprintf("[%d lines pasted]", n)
}

func codexPastedTextComposerToken(id int, text string) string {
	n := codexVisibleLineCount(text)
	if n == 1 {
		return fmt.Sprintf("[Paste #%d: 1 line]", id)
	}
	return fmt.Sprintf("[Paste #%d: %d lines]", id, n)
}

func collapseCodexPastedTextTokens(text string, pastedTexts []codexPastedText) string {
	collapsed := text
	for _, pasted := range pruneCodexPastedTexts(text, pastedTexts) {
		collapsed = strings.ReplaceAll(collapsed, pasted.Token, codexPastedTextPlaceholder(pasted.Text))
	}
	return strings.TrimSpace(collapsed)
}

func codexVisibleRuneCount(text string) int {
	return len([]rune(text))
}

func codexVisibleLineCount(text string) int {
	if text == "" {
		return 0
	}
	// Normalize line endings: \r\n → \n, then standalone \r → \n.
	// Bracketed paste from terminals often uses \r instead of \n.
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return strings.Count(normalized, "\n") + 1
}

func shouldCollapseCodexPaste(text string) bool {
	return codexVisibleRuneCount(text) >= codexLargePasteCharacterThreshold || codexVisibleLineCount(text) >= codexLargePasteLineThreshold
}

func cloneCodexDraft(in codexDraft) codexDraft {
	return codexDraft{
		Text:        in.Text,
		Attachments: cloneCodexAttachments(in.Attachments),
		PastedTexts: cloneCodexPastedTexts(in.PastedTexts),
	}
}

func (m *Model) currentCodexDraftFor(projectPath string) codexDraft {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return codexDraft{}
	}
	draft := cloneCodexDraft(m.codexDrafts[projectPath])
	if projectPath == m.codexVisibleProject {
		draft.Text = m.codexInput.Value()
	}
	return draft.normalized()
}

func (m *Model) markCodexSessionLive(projectPath string) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	if m.codexClosedHandled == nil {
		m.codexClosedHandled = make(map[string]struct{})
	}
	delete(m.codexClosedHandled, projectPath)
}

func (m *Model) markCodexSessionClosedHandled(projectPath string) bool {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return false
	}
	if m.codexClosedHandled == nil {
		m.codexClosedHandled = make(map[string]struct{})
	}
	if _, ok := m.codexClosedHandled[projectPath]; ok {
		return false
	}
	m.codexClosedHandled[projectPath] = struct{}{}
	return true
}

func (m *Model) currentCodexDraft() codexDraft {
	return m.currentCodexDraftFor(m.codexVisibleProject)
}

func (m *Model) persistCodexDraft(projectPath string) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	if m.codexDrafts == nil {
		m.codexDrafts = make(map[string]codexDraft)
	}
	draft := m.currentCodexDraftFor(projectPath).normalized()
	if draft.Empty() {
		delete(m.codexDrafts, projectPath)
		return
	}
	m.codexDrafts[projectPath] = draft
}

func (m *Model) persistVisibleCodexDraft() {
	m.persistCodexDraft(m.codexVisibleProject)
}

func (m *Model) loadCodexDraft(projectPath string) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		m.codexInput.SetValue("")
		m.codexInput.CursorEnd()
		m.syncCodexSlashSelection()
		return
	}
	draft := cloneCodexDraft(m.codexDrafts[projectPath]).normalized()
	m.codexInput.SetValue(draft.Text)
	m.codexInput.CursorEnd()
	m.syncCodexComposerSize()
	m.syncCodexSlashSelection()
}

func (m *Model) restoreCodexDraft(projectPath string, draft codexDraft) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	if m.codexDrafts == nil {
		m.codexDrafts = make(map[string]codexDraft)
	}
	draft = cloneCodexDraft(draft).normalized()
	if draft.Empty() {
		delete(m.codexDrafts, projectPath)
	} else {
		m.codexDrafts[projectPath] = draft
	}
	if m.codexVisibleProject == projectPath {
		m.loadCodexDraft(projectPath)
	}
}

func (m *Model) clearCodexDraft(projectPath string) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	if m.codexDrafts == nil {
		m.codexDrafts = make(map[string]codexDraft)
	}
	delete(m.codexDrafts, projectPath)
	if m.codexVisibleProject == projectPath {
		m.codexInput.SetValue("")
		m.codexInput.CursorEnd()
		m.syncCodexComposerSize()
		m.syncCodexSlashSelection()
	}
}

func (m *Model) currentCodexAttachments() []codexapp.Attachment {
	return cloneCodexAttachments(m.currentCodexDraft().Attachments)
}

func (m *Model) currentCodexPastedTexts() []codexPastedText {
	return cloneCodexPastedTexts(m.currentCodexDraft().PastedTexts)
}

func (m *Model) setCurrentCodexAttachments(attachments []codexapp.Attachment) {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return
	}
	if m.codexDrafts == nil {
		m.codexDrafts = make(map[string]codexDraft)
	}
	draft := m.currentCodexDraft()
	draft.Attachments = cloneCodexAttachments(attachments)
	draft = draft.normalized()
	if draft.Empty() {
		delete(m.codexDrafts, projectPath)
	} else {
		m.codexDrafts[projectPath] = draft
	}
}

func (m *Model) setCurrentCodexPastedTexts(pastedTexts []codexPastedText) {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return
	}
	if m.codexDrafts == nil {
		m.codexDrafts = make(map[string]codexDraft)
	}
	draft := m.currentCodexDraft()
	draft.PastedTexts = cloneCodexPastedTexts(pastedTexts)
	draft = draft.normalized()
	if draft.Empty() {
		delete(m.codexDrafts, projectPath)
	} else {
		m.codexDrafts[projectPath] = draft
	}
}

func (m *Model) appendCurrentCodexAttachment(attachment codexapp.Attachment) {
	attachments := m.currentCodexAttachments()
	attachments = append(attachments, attachment)
	m.setCurrentCodexAttachments(attachments)
}

func (m *Model) appendCurrentCodexPastedText(pasted codexPastedText) {
	pastedTexts := m.currentCodexPastedTexts()
	pastedTexts = append(pastedTexts, pasted)
	m.setCurrentCodexPastedTexts(pastedTexts)
}

func (m *Model) removeLastCurrentCodexAttachment() bool {
	attachments := m.currentCodexAttachments()
	if len(attachments) == 0 {
		return false
	}
	m.setCurrentCodexAttachments(attachments[:len(attachments)-1])
	return true
}

func (m *Model) removeCurrentCodexAttachment(index int) bool {
	attachments := m.currentCodexAttachments()
	if index < 0 || index >= len(attachments) {
		return false
	}
	updated := append([]codexapp.Attachment(nil), attachments[:index]...)
	updated = append(updated, attachments[index+1:]...)
	m.setCurrentCodexAttachments(updated)
	return true
}

func (m *Model) removeCurrentCodexPastedTextByToken(token string) bool {
	pastedTexts := m.currentCodexPastedTexts()
	if len(pastedTexts) == 0 || strings.TrimSpace(token) == "" {
		return false
	}
	updated := make([]codexPastedText, 0, len(pastedTexts))
	removed := false
	for _, pasted := range pastedTexts {
		if pasted.Token == token {
			removed = true
			continue
		}
		updated = append(updated, pasted)
	}
	if removed {
		m.setCurrentCodexPastedTexts(updated)
	}
	return removed
}

func (m *Model) nextCodexPastedTextToken(text string) string {
	pastedTexts := m.currentCodexPastedTexts()
	for {
		m.codexPasteTokenSeq++
		token := codexPastedTextComposerToken(m.codexPasteTokenSeq, text)
		collision := false
		for _, pasted := range pastedTexts {
			if pasted.Token == token {
				collision = true
				break
			}
		}
		if !collision {
			return token
		}
	}
}

func codexAttachmentComposerToken(index int, attachment codexapp.Attachment) string {
	switch attachment.Kind {
	case codexapp.AttachmentLocalImage:
		return fmt.Sprintf("[Image #%d]", index+1)
	default:
		return fmt.Sprintf("[Attachment #%d]", index+1)
	}
}

func stripCodexAttachmentComposerTokens(text string, attachments []codexapp.Attachment) string {
	cleaned := text
	for i, attachment := range attachments {
		cleaned = strings.ReplaceAll(cleaned, codexAttachmentComposerToken(i, attachment), "")
	}
	return strings.TrimSpace(cleaned)
}

func codexTextareaCursorOffset(input textarea.Model) int {
	value := input.Value()
	lines := strings.Split(value, "\n")
	if len(lines) == 0 {
		return 0
	}
	row := input.Line()
	if row < 0 {
		row = 0
	}
	if row >= len(lines) {
		row = len(lines) - 1
	}
	info := input.LineInfo()
	col := info.StartColumn + info.ColumnOffset
	lineRunes := []rune(lines[row])
	if col < 0 {
		col = 0
	}
	if col > len(lineRunes) {
		col = len(lineRunes)
	}
	offset := 0
	for i := 0; i < row; i++ {
		offset += len([]rune(lines[i])) + 1
	}
	return offset + col
}

func codexTextOffsetPosition(text string, offset int) (row, col int) {
	runes := []rune(text)
	if offset < 0 {
		offset = 0
	}
	if offset > len(runes) {
		offset = len(runes)
	}
	for i := 0; i < offset; i++ {
		if runes[i] == '\n' {
			row++
			col = 0
			continue
		}
		col++
	}
	return row, col
}

func (m *Model) setCodexComposerValue(text string, cursorOffset int) {
	m.codexInput.SetValue(text)
	row, col := codexTextOffsetPosition(text, cursorOffset)
	for m.codexInput.Line() > row {
		m.codexInput.CursorUp()
	}
	for m.codexInput.Line() < row {
		m.codexInput.CursorDown()
	}
	m.codexInput.SetCursor(col)
	m.syncCodexSlashSelection()
}

func (m Model) liveCodexSnapshots() []codexapp.Snapshot {
	if m.codexManager == nil {
		return nil
	}
	snapshots := m.codexManager.Snapshots()
	live := make([]codexapp.Snapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.Closed || strings.TrimSpace(snapshot.ProjectPath) == "" {
			continue
		}
		live = append(live, snapshot)
	}
	sort.SliceStable(live, func(i, j int) bool {
		left := live[i].LastActivityAt
		right := live[j].LastActivityAt
		switch {
		case left.Equal(right):
			return live[i].ProjectPath < live[j].ProjectPath
		case left.IsZero():
			return false
		case right.IsZero():
			return true
		default:
			return left.After(right)
		}
	})
	return live
}

func (m Model) liveCodexProjects() []string {
	snapshots := m.liveCodexSnapshots()
	projects := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		projects = append(projects, snapshot.ProjectPath)
	}
	return projects
}

func (m Model) preferredHiddenCodexProject() string {
	projects := m.liveCodexProjects()
	if len(projects) == 0 {
		return ""
	}
	if strings.TrimSpace(m.codexHiddenProject) != "" && m.codexHiddenProject != m.codexVisibleProject {
		for _, projectPath := range projects {
			if projectPath == m.codexHiddenProject {
				return projectPath
			}
		}
	}
	for _, projectPath := range projects {
		if projectPath != m.codexVisibleProject {
			return projectPath
		}
	}
	return ""
}

func (m Model) nextLiveCodexProject() string {
	return m.stepLiveCodexProject(1)
}

func (m Model) previousLiveCodexProject() string {
	return m.stepLiveCodexProject(-1)
}

func (m Model) stepLiveCodexProject(delta int) string {
	projects := m.liveCodexProjects()
	if len(projects) == 0 {
		return ""
	}
	current := strings.TrimSpace(m.codexVisibleProject)
	if current == "" {
		return projects[0]
	}
	for i, projectPath := range projects {
		if projectPath == current {
			index := (i + delta) % len(projects)
			if index < 0 {
				index += len(projects)
			}
			return projects[index]
		}
	}
	return projects[0]
}

func (m *Model) resetCodexToolAnswerState(projectPath string) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	if m.codexToolAnswers == nil {
		m.codexToolAnswers = make(map[string]codexToolAnswerState)
	}
	snapshot, ok := m.codexSnapshotForProject(projectPath)
	if !ok || snapshot.PendingToolInput == nil {
		delete(m.codexToolAnswers, projectPath)
		return
	}
	state, ok := m.codexToolAnswers[projectPath]
	if !ok || state.RequestID != snapshot.PendingToolInput.ID {
		delete(m.codexToolAnswers, projectPath)
	}
}

func (m Model) codexSnapshotForProject(projectPath string) (codexapp.Snapshot, bool) {
	return m.nonBlockingCodexSnapshot(projectPath)
}

func (m *Model) ensureToolAnswerState(projectPath string, request *codexapp.ToolInputRequest) codexToolAnswerState {
	if request == nil {
		return codexToolAnswerState{}
	}
	projectPath = strings.TrimSpace(projectPath)
	if m.codexToolAnswers == nil {
		m.codexToolAnswers = make(map[string]codexToolAnswerState)
	}
	state, ok := m.codexToolAnswers[projectPath]
	if !ok || state.RequestID != request.ID {
		state = codexToolAnswerState{
			RequestID: request.ID,
			Answers:   make(map[string][]string),
		}
	}
	state.QuestionIndex = firstUnansweredToolQuestion(request, state.Answers)
	if state.QuestionIndex >= len(request.Questions) {
		state.QuestionIndex = max(0, len(request.Questions)-1)
	}
	m.codexToolAnswers[projectPath] = state
	return state
}

func (m Model) toolAnswerStateFor(projectPath string, request *codexapp.ToolInputRequest) codexToolAnswerState {
	if request == nil {
		return codexToolAnswerState{}
	}
	projectPath = strings.TrimSpace(projectPath)
	state, ok := m.codexToolAnswers[projectPath]
	if !ok || state.RequestID != request.ID {
		state = codexToolAnswerState{
			RequestID: request.ID,
			Answers:   make(map[string][]string),
		}
	}
	state.QuestionIndex = firstUnansweredToolQuestion(request, state.Answers)
	if state.QuestionIndex >= len(request.Questions) {
		state.QuestionIndex = max(0, len(request.Questions)-1)
	}
	return state
}

func firstUnansweredToolQuestion(request *codexapp.ToolInputRequest, answers map[string][]string) int {
	if request == nil {
		return 0
	}
	for i, question := range request.Questions {
		values := answers[question.ID]
		hasAnswer := false
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				hasAnswer = true
				break
			}
		}
		if !hasAnswer {
			return i
		}
	}
	return len(request.Questions)
}
