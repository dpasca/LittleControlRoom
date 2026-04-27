package boss

import (
	"context"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/service"
	"lcroom/internal/terminalmd"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	defaultBossWidth  = 112
	defaultBossHeight = 32
)

type Model struct {
	ctx       context.Context
	svc       *service.Service
	assistant *Assistant
	embedded  bool

	width  int
	height int

	input        textarea.Model
	chatViewport viewport.Model
	messages     []ChatMessage

	snapshot     StateSnapshot
	viewContext  ViewContext
	stateLoaded  bool
	stateErr     error
	sending      bool
	status       string
	spinnerFrame int
	nowFn        func() time.Time
}

type StateLoadedMsg struct {
	snapshot StateSnapshot
	err      error
}

type AssistantReplyMsg struct {
	response AssistantResponse
	err      error
}

type TickMsg time.Time

type ExitMsg struct{}

type bossLayout struct {
	width            int
	height           int
	topHeight        int
	bottomHeight     int
	middleGapHeight  int
	chatWidth        int
	sideWidth        int
	deskWidth        int
	notebookWidth    int
	chatInnerWidth   int
	transcriptHeight int
	inputHeight      int
	narrow           bool
}

func New(ctx context.Context, svc *service.Service) Model {
	return newModel(ctx, svc, false)
}

func NewEmbedded(ctx context.Context, svc *service.Service) Model {
	return newModel(ctx, svc, true)
}

func NewEmbeddedWithViewContext(ctx context.Context, svc *service.Service, view ViewContext) Model {
	m := newModel(ctx, svc, true)
	m.viewContext = view
	return m
}

func newModel(ctx context.Context, svc *service.Service, embedded bool) Model {
	input := textarea.New()
	input.Prompt = "> "
	input.SetPromptFunc(2, func(line int) string {
		if line == 0 {
			return "> "
		}
		return "  "
	})
	input.Placeholder = ""
	input.CharLimit = 6000
	input.ShowLineNumbers = false
	input.SetWidth(72)
	input.SetHeight(3)
	styleBossTextarea(&input)
	input.Focus()

	assistant := NewAssistant(svc)
	m := Model{
		ctx:          ctx,
		svc:          svc,
		assistant:    assistant,
		embedded:     embedded,
		input:        input,
		chatViewport: viewport.New(0, 0),
		messages: []ChatMessage{{
			Role:    "assistant",
			Content: "Hi. Ask what deserves attention, what to do next, or what can safely wait. I will keep a compact view of the project board in mind on every turn.",
			At:      time.Now(),
		}},
		status: assistant.Label(),
		nowFn:  time.Now,
	}
	m.syncLayout(true)
	return m
}

func IsMessage(msg tea.Msg) bool {
	switch msg.(type) {
	case StateLoadedMsg, AssistantReplyMsg, TickMsg, ExitMsg:
		return true
	default:
		return false
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.loadStateCmd(), bossTickCmd())
}

func (m Model) StatusText() string {
	status := strings.TrimSpace(m.status)
	if status == "" {
		status = "ready"
	}
	if m.sending {
		status = "thinking " + spinnerDots(m.spinnerFrame)
	}
	return status
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.syncLayout(false)
		return m, nil
	case StateLoadedMsg:
		m.stateLoaded = true
		m.stateErr = msg.err
		if msg.err == nil {
			m.snapshot = msg.snapshot
			m.status = m.assistant.Label()
		} else {
			m.status = "State refresh failed: " + msg.err.Error()
		}
		m.syncLayout(false)
		return m, nil
	case AssistantReplyMsg:
		m.sending = false
		if msg.err != nil {
			m.messages = append(m.messages, ChatMessage{
				Role:    "assistant",
				Content: "I could not reach my chat backend yet: " + msg.err.Error(),
				At:      m.now(),
			})
			m.status = "Boss chat could not answer"
		} else {
			content := strings.TrimSpace(msg.response.Content)
			if content == "" {
				content = "I heard you, but the model returned an empty reply."
			}
			m.messages = append(m.messages, ChatMessage{
				Role:    "assistant",
				Content: content,
				At:      m.now(),
			})
			if modelName := strings.TrimSpace(msg.response.Model); modelName != "" {
				m.status = "Boss chat via " + modelName
			} else {
				m.status = m.assistant.Label()
			}
		}
		m.syncLayout(true)
		return m, nil
	case TickMsg:
		m.spinnerFrame++
		return m, bossTickCmd()
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, m.exitCmd()
		case "ctrl+r":
			m.status = "Refreshing project state..."
			return m, m.loadStateCmd()
		case "pgup", "pgdown", "home", "end":
			var cmd tea.Cmd
			m.chatViewport, cmd = m.chatViewport.Update(msg)
			return m, cmd
		case "enter":
			return m.submit()
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.syncLayout(false)
		return m, cmd
	}
	return m, nil
}

func (m Model) View() string {
	layout := m.layout()
	body := ""
	if layout.narrow {
		body = m.renderNarrow(layout)
	} else {
		chat := m.renderChat(layout)
		situationWidth := layout.sideWidth
		situation := m.renderSituation(situationWidth, layout.topHeight)
		top := lipgloss.JoinHorizontal(
			lipgloss.Top,
			chat,
			" ",
			situation,
		)
		if delta := layout.width - blockWidth(top); delta > 0 {
			situationWidth += delta
			situation = m.renderSituation(situationWidth, layout.topHeight)
			top = lipgloss.JoinHorizontal(lipgloss.Top, chat, " ", situation)
		}

		if layout.bottomHeight < 4 {
			body = top
		} else {
			attention := m.renderAttention(layout.deskWidth, layout.bottomHeight)
			notesWidth := layout.notebookWidth
			notes := m.renderPanel("Notes", NotesText(m.snapshot), notesWidth, layout.bottomHeight)
			bottom := lipgloss.JoinHorizontal(
				lipgloss.Top,
				attention,
				" ",
				notes,
			)
			if delta := layout.width - blockWidth(bottom); delta > 0 {
				notesWidth += delta
				notes = m.renderPanel("Notes", NotesText(m.snapshot), notesWidth, layout.bottomHeight)
				bottom = lipgloss.JoinHorizontal(lipgloss.Top, attention, " ", notes)
			}
			if layout.middleGapHeight > 0 {
				body = lipgloss.JoinVertical(
					lipgloss.Left,
					top,
					fitRenderedBlock("", layout.width, layout.middleGapHeight),
					bottom,
				)
			} else {
				body = lipgloss.JoinVertical(lipgloss.Left, top, bottom)
			}
		}
	}
	if m.embedded {
		return fitRenderedBlock(body, layout.width, layout.height)
	}
	return fitRenderedBlock(lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(layout.width), body), layout.width, layout.height+1)
}

func (m Model) submit() (tea.Model, tea.Cmd) {
	if m.sending {
		return m, nil
	}
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	if bossOffCommand(text) {
		m.input.Reset()
		return m, m.exitCmd()
	}
	m.messages = append(m.messages, ChatMessage{
		Role:    "user",
		Content: text,
		At:      m.now(),
	})
	m.input.Reset()
	m.sending = true
	m.status = "Boss chat is thinking..."
	m.syncLayout(true)
	return m, m.askAssistantCmd(append([]ChatMessage(nil), m.messages...), m.snapshot, m.viewContext)
}

func (m Model) askAssistantCmd(messages []ChatMessage, snapshot StateSnapshot, view ViewContext) tea.Cmd {
	assistant := m.assistant
	parent := m.ctx
	return func() tea.Msg {
		ctx, cancel := childContext(parent, 120*time.Second)
		defer cancel()
		resp, err := assistant.Reply(ctx, AssistantRequest{
			StateBrief: BuildStateBrief(snapshot, time.Now()),
			Snapshot:   snapshot,
			View:       view,
			Messages:   messages,
		})
		return AssistantReplyMsg{response: resp, err: err}
	}
}

func (m Model) loadStateCmd() tea.Cmd {
	svc := m.svc
	parent := m.ctx
	return func() tea.Msg {
		ctx, cancel := childContext(parent, 20*time.Second)
		defer cancel()
		snapshot, err := LoadStateSnapshot(ctx, svc, time.Now())
		return StateLoadedMsg{snapshot: snapshot, err: err}
	}
}

func (m Model) exitCmd() tea.Cmd {
	if m.embedded {
		return func() tea.Msg { return ExitMsg{} }
	}
	return tea.Quit
}

func bossOffCommand(text string) bool {
	switch strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " ")) {
	case "/boss", "/boss off", "/boss close", "/boss exit":
		return true
	default:
		return false
	}
}

func childContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		return context.WithTimeout(context.Background(), timeout)
	}
	if _, ok := parent.Deadline(); ok || timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

func bossTickCmd() tea.Cmd {
	return tea.Tick(600*time.Millisecond, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

func (m *Model) syncLayout(gotoBottom bool) {
	layout := m.layout()
	m.input.SetWidth(maxInt(20, layout.chatInnerWidth))
	m.input.SetHeight(layout.inputHeight)
	m.chatViewport.Width = maxInt(1, layout.chatInnerWidth)
	m.chatViewport.Height = maxInt(1, layout.transcriptHeight)
	m.chatViewport.SetContent(m.renderTranscript(layout.chatInnerWidth))
	if gotoBottom {
		m.chatViewport.GotoBottom()
	}
}

func (m Model) layout() bossLayout {
	width := m.width
	if width <= 0 {
		width = defaultBossWidth
	}
	height := m.height
	if height <= 0 {
		height = defaultBossHeight
	}
	width = maxInt(48, width)
	minHeight := 18
	if m.embedded {
		minHeight = 8
	}
	height = maxInt(minHeight, height)
	if height > 18 {
		if !m.embedded {
			// Standalone boss mode owns its header bar and keeps one row of
			// slack so exact-height renders do not scroll frames out of view.
			height -= 2
		}
	}
	inputHeight := 2

	if width < 78 {
		chatInnerWidth := bossPanelInnerWidth(width)
		topHeight := maxInt(8, panelHeightForRawLines(maxInt(2, countBlockLines(m.renderTranscript(chatInnerWidth)))+1+inputHeight))
		if height-topHeight < 4 {
			topHeight = height
		}
		return bossLayout{
			width:            width,
			height:           height,
			topHeight:        topHeight,
			bottomHeight:     maxInt(0, height-topHeight),
			chatWidth:        width,
			sideWidth:        width,
			deskWidth:        width,
			notebookWidth:    width,
			chatInnerWidth:   chatInnerWidth,
			transcriptHeight: maxInt(2, topHeight-inputHeight-5),
			inputHeight:      inputHeight,
			narrow:           true,
		}
	}

	minTopHeight := 10
	minBottomHeight := 7
	if m.embedded && height < 18 {
		minTopHeight = 8
		minBottomHeight = 4
	}
	bottomHeight := 0
	if !m.embedded {
		bottomHeight = clampInt(height/4, 7, 10)
		if height < 24 {
			bottomHeight = clampInt(height/3, 5, 8)
		}
	}
	topHeight := maxInt(1, height-bottomHeight)
	sideWidth := clampInt(width/4, 24, 36)
	if m.embedded {
		sideWidth = clampInt(width/6, 24, 30)
	}
	chatWidth := maxInt(40, width-sideWidth-1)
	deskWidth := clampInt(width/3, 26, width/2)
	notebookWidth := maxInt(24, width-deskWidth-1)
	chatInnerWidth := bossPanelInnerWidth(chatWidth)
	if m.embedded {
		topNeeded := maxInt(minTopHeight, panelHeightForWrappedContent(m.situationContent(), bossPanelInnerWidth(sideWidth)))
		bottomNeeded := maxInt(minBottomHeight, panelHeightForWrappedContent(AttentionText(m.snapshot, m.now()), bossPanelInnerWidth(deskWidth)))
		bottomNeeded = maxInt(bottomNeeded, panelHeightForWrappedContent(NotesText(m.snapshot), bossPanelInnerWidth(notebookWidth)))
		maxBottomHeight := embeddedBottomPanelMaxHeight(height)
		bottomHeight = clampInt(bottomNeeded, minBottomHeight, maxBottomHeight)
		if height-bottomHeight >= topNeeded {
			topHeight = height - bottomHeight
		} else {
			topHeight = maxInt(topNeeded, height-minBottomHeight)
			if topHeight >= height || height-topHeight < minBottomHeight {
				topHeight = height
				bottomHeight = 0
			} else {
				bottomHeight = height - topHeight
			}
		}
	}
	transcriptHeight := maxInt(2, topHeight-inputHeight-5)
	middleGapHeight := 0
	return bossLayout{
		width:            width,
		height:           height,
		topHeight:        topHeight,
		bottomHeight:     bottomHeight,
		middleGapHeight:  middleGapHeight,
		chatWidth:        chatWidth,
		sideWidth:        sideWidth,
		deskWidth:        deskWidth,
		notebookWidth:    notebookWidth,
		chatInnerWidth:   chatInnerWidth,
		transcriptHeight: transcriptHeight,
		inputHeight:      inputHeight,
	}
}

func (m Model) renderChat(layout bossLayout) string {
	hint := "Enter sends | Ctrl+J newline | Ctrl+R refresh | Esc returns"
	if m.sending {
		hint = "Boss chat is thinking " + spinnerDots(m.spinnerFrame)
	}
	input := fitRenderedBlock(renderBossInput(m.input, layout.chatInnerWidth), layout.chatInnerWidth, layout.inputHeight)
	content := strings.Join([]string{
		m.chatViewport.View(),
		bossMutedStyle.Render(fitLine(hint, layout.chatInnerWidth)),
		input,
	}, "\n")
	return m.renderRawPanel("Boss Chat", content, layout.chatWidth, layout.topHeight)
}

func (m Model) renderHeader(width int) string {
	text := " Boss Mode  " + m.StatusText() + "  |  Esc returns  Ctrl+R refresh"
	return bossHeaderStyle.Width(width).Render(fitLine(text, width))
}

func (m Model) renderSituation(width, height int) string {
	return m.renderPanel("Situation", m.situationContent(), width, height)
}

func (m Model) renderAttention(width, height int) string {
	return m.renderPanel("Attention", m.attentionContent(height), width, height)
}

func (m Model) attentionContent(height int) string {
	return AttentionTextWithLimit(m.snapshot, m.now(), attentionProjectLimit(height))
}

func (m Model) situationContent() string {
	status := m.status
	if strings.TrimSpace(status) == "" {
		status = "Boss chat warming up"
	}
	boardState := "calm"
	if m.snapshot.ConflictProjects > 0 {
		boardState = "conflicts need attention"
	} else if m.snapshot.PossiblyStuckProjects > 0 {
		boardState = "some work may be stuck"
	} else if m.snapshot.ActiveProjects > 0 {
		boardState = "active work in progress"
	}
	room := []string{
		"Board: " + boardState,
		"Chat: " + status,
		fmt.Sprintf("Projects: %d total", m.snapshot.TotalProjects),
		fmt.Sprintf("Active: %d  Stuck: %d", m.snapshot.ActiveProjects, m.snapshot.PossiblyStuckProjects),
		fmt.Sprintf("Dirty repos: %d", m.snapshot.DirtyProjects),
		fmt.Sprintf("Conflicts: %d", m.snapshot.ConflictProjects),
	}
	if m.snapshot.PendingClassifications > 0 {
		room = append(room, fmt.Sprintf("Assessments: %d queued/running", m.snapshot.PendingClassifications))
	}
	if m.stateErr != nil {
		room = append(room, "State: "+m.stateErr.Error())
	} else if m.stateLoaded {
		room = append(room, "State: loaded")
	} else {
		room = append(room, "State: loading...")
	}
	return strings.Join(room, "\n")
}

func (m Model) renderPanel(title, content string, width, height int) string {
	width = maxInt(12, width)
	height = maxInt(4, height)
	innerWidth := bossPanelInnerWidth(width)
	innerHeight := maxInt(1, height-2)
	bodyHeight := maxInt(0, innerHeight-2)
	titleLine := panelTitleStyle.Render(fitLine(title, innerWidth))
	body := fitWrappedBlock(content, innerWidth, bodyHeight)
	rendered := panelStyle.Width(bossPanelStyleWidth(width)).Height(innerHeight).Render(titleLine + "\n" + body)
	return fitRenderedBlock(rendered, width, height)
}

func (m Model) renderRawPanel(title, content string, width, height int) string {
	width = maxInt(12, width)
	height = maxInt(4, height)
	innerWidth := bossPanelInnerWidth(width)
	innerHeight := maxInt(1, height-2)
	titleLine := panelTitleStyle.Render(fitLine(title, innerWidth))
	body := fitBlock(content, innerWidth, maxInt(0, innerHeight-2))
	rendered := panelStyle.Width(bossPanelStyleWidth(width)).Height(innerHeight).Render(titleLine + "\n" + body)
	return fitRenderedBlock(rendered, width, height)
}

func (m Model) renderNarrow(layout bossLayout) string {
	chat := m.renderChat(layout)
	remainingHeight := maxInt(0, layout.height-layout.topHeight)
	if remainingHeight < 4 {
		return chat
	}
	if remainingHeight < 8 {
		room := m.renderSituation(layout.sideWidth, remainingHeight)
		return lipgloss.JoinVertical(lipgloss.Left, chat, room)
	}
	roomHeight := remainingHeight / 2
	deskHeight := remainingHeight - roomHeight
	room := m.renderSituation(layout.sideWidth, roomHeight)
	desk := m.renderAttention(layout.deskWidth, deskHeight)
	return lipgloss.JoinVertical(lipgloss.Left, chat, room, desk)
}

func (m Model) renderTranscript(width int) string {
	width = maxInt(12, width)
	var blocks []string
	for _, message := range m.messages {
		if normalizeChatRole(message.Role) == "assistant" {
			blocks = append(blocks, renderAssistantMessage(message.Content, width))
			continue
		}
		blocks = append(blocks, renderUserMessage(message.Content, width))
	}
	return strings.Join(blocks, "\n\n")
}

func (m Model) now() time.Time {
	if m.nowFn != nil {
		return m.nowFn()
	}
	return time.Now()
}

var (
	bossPanelBackground           = lipgloss.Color("#000000")
	bossInputBackground           = lipgloss.Color("#000000")
	bossInputCursorLineBackground = lipgloss.Color("#101010")
	bossPanelAccent               = lipgloss.Color("81")
	bossPanelText                 = lipgloss.Color("252")
	bossHeaderStyle               = lipgloss.NewStyle().
					Foreground(lipgloss.Color("16")).
					Background(bossPanelAccent).
					Bold(true)
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(bossPanelAccent).
			Padding(0, 1).
			Foreground(bossPanelText).
			Background(bossPanelBackground)
	panelTitleStyle = lipgloss.NewStyle().
			Foreground(bossPanelAccent).
			Bold(true)
	bossMutedStyle                  = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Background(bossPanelBackground)
	bossAssistantMessageBackground  = bossPanelBackground
	bossUserMessageBackground       = lipgloss.Color("#101010")
	bossAssistantMessageStyle       = lipgloss.NewStyle().Background(bossAssistantMessageBackground)
	bossUserMessageStyle            = lipgloss.NewStyle().Background(bossUserMessageBackground)
	bossUserPrefixStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Background(bossUserMessageBackground).Bold(true)
	bossUserContinuationPrefixStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Background(bossUserMessageBackground)
	bossInputShellStyle             = lipgloss.NewStyle().Background(bossInputBackground).Foreground(bossPanelText)
)

func bossPanelInnerWidth(width int) int {
	return maxInt(1, width-4)
}

func bossPanelStyleWidth(width int) int {
	return maxInt(1, width-2)
}

func styleBossTextarea(input *textarea.Model) {
	focused := input.FocusedStyle
	focused.Base = focused.Base.Background(bossInputBackground).Foreground(bossPanelText)
	focused.CursorLine = focused.CursorLine.Background(bossInputCursorLineBackground)
	focused.EndOfBuffer = focused.EndOfBuffer.Foreground(lipgloss.Color("238")).Background(bossInputBackground)
	focused.Placeholder = focused.Placeholder.Foreground(lipgloss.Color("240")).Background(bossInputBackground)
	focused.Prompt = focused.Prompt.Foreground(bossPanelAccent).Background(bossInputBackground).Bold(true)
	focused.Text = focused.Text.Foreground(bossPanelText).Background(bossInputBackground)

	blurred := input.BlurredStyle
	blurred.Base = blurred.Base.Background(bossInputBackground).Foreground(bossPanelText)
	blurred.CursorLine = blurred.CursorLine.Background(bossInputBackground)
	blurred.EndOfBuffer = blurred.EndOfBuffer.Foreground(lipgloss.Color("238")).Background(bossInputBackground)
	blurred.Placeholder = blurred.Placeholder.Foreground(lipgloss.Color("240")).Background(bossInputBackground)
	blurred.Prompt = blurred.Prompt.Foreground(lipgloss.Color("244")).Background(bossInputBackground).Bold(true)
	blurred.Text = blurred.Text.Foreground(bossPanelText).Background(bossInputBackground)

	input.FocusedStyle = focused
	input.BlurredStyle = blurred
}

func renderBossInput(input textarea.Model, width int) string {
	return bossInputShellStyle.Width(width).Render(input.View())
}

func panelHeightForRawLines(contentLines int) int {
	return maxInt(4, contentLines+4)
}

func panelHeightForWrappedContent(content string, width int) int {
	return panelHeightForRawLines(countWrappedBlockLines(content, width))
}

func embeddedBottomPanelMaxHeight(height int) int {
	if height < 18 {
		return maxInt(4, height/3)
	}
	return clampInt(height/4, 8, 11)
}

func attentionProjectLimit(height int) int {
	bodyHeight := maxInt(0, height-4)
	if bodyHeight <= 0 {
		return defaultAttentionProjectLimit
	}
	return clampInt(bodyHeight, defaultAttentionProjectLimit, hotProjectLimit)
}

func renderAssistantMessage(content string, width int) string {
	rendered := terminalmd.RenderBody(content, bossPanelText, maxInt(8, width))
	return renderMessageLines(rendered, bossAssistantMessageStyle, width)
}

func renderUserMessage(content string, width int) string {
	prefix := "You> "
	contentWidth := maxInt(8, width-len(prefix))
	rendered := terminalmd.RenderBody(content, bossPanelText, contentWidth)
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	for i, line := range lines {
		if i == 0 {
			lines[i] = bossUserPrefixStyle.Render(prefix) + line
			continue
		}
		lines[i] = bossUserContinuationPrefixStyle.Render(strings.Repeat(" ", len(prefix))) + line
	}
	return renderMessageLines(strings.Join(lines, "\n"), bossUserMessageStyle, width)
}

func renderMessageLines(content string, style lipgloss.Style, width int) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = style.Render(fitStyledLine(line, width))
	}
	return strings.Join(lines, "\n")
}

func spinnerDots(frame int) string {
	switch frame % 4 {
	case 0:
		return "."
	case 1:
		return ".."
	case 2:
		return "..."
	default:
		return ""
	}
}

func fitBlock(content string, width, height int) string {
	if height <= 0 {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, line := range lines {
		lines[i] = fitLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func fitWrappedBlock(content string, width, height int) string {
	if height <= 0 {
		return ""
	}
	lines := wrappedBlockLines(content, width)
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, line := range lines {
		lines[i] = fitLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func countBlockLines(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(strings.ReplaceAll(content, "\r\n", "\n"), "\n") + 1
}

func countWrappedBlockLines(content string, width int) int {
	return len(wrappedBlockLines(content, width))
}

func wrappedBlockLines(content string, width int) []string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, wrapText(line, width)...)
	}
	return lines
}

func fitLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(strings.TrimRight(line, " "))
	if len(runes) > width {
		if width <= 3 {
			return string(runes[:width])
		}
		return string(runes[:width-3]) + "..."
	}
	return string(runes) + strings.Repeat(" ", width-len(runes))
}

func fitStyledLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	line = ansi.Truncate(line, width, "")
	if padding := width - ansi.StringWidth(ansi.Strip(line)); padding > 0 {
		line += strings.Repeat(" ", padding)
	}
	return line
}

func blockWidth(content string) int {
	width := 0
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if w := ansi.StringWidth(ansi.Strip(line)); w > width {
			width = w
		}
	}
	return width
}

func fitRenderedBlock(content string, width, height int) string {
	if height <= 0 {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, line := range lines {
		lines[i] = fitStyledLine(line, width)
	}
	blank := strings.Repeat(" ", maxInt(0, width))
	for len(lines) < height {
		lines = append(lines, blank)
	}
	return strings.Join(lines, "\n")
}

func wrapText(text string, width int) []string {
	width = maxInt(8, width)
	paragraphs := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n")
	if len(paragraphs) == 0 {
		return []string{""}
	}
	var out []string
	for _, paragraph := range paragraphs {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		for _, word := range words {
			if line == "" {
				line = word
				continue
			}
			if len([]rune(line))+1+len([]rune(word)) > width {
				out = append(out, fitLine(line, width))
				line = word
				continue
			}
			line += " " + word
		}
		if line != "" {
			out = append(out, fitLine(line, width))
		}
	}
	return out
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
