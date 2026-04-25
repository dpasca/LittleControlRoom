package boss

import (
	"context"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/service"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	chatWidth        int
	roomWidth        int
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
	input.Placeholder = "Ask Mina what needs attention..."
	input.CharLimit = 6000
	input.ShowLineNumbers = false
	input.SetWidth(72)
	input.SetHeight(3)
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
			Content: "Hi, I am Mina. Ask me what deserves attention, what to do next, or what can safely wait. I will keep a compact view of the project board in mind on every turn.",
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
			m.status = "Mina could not answer"
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
				m.status = "Mina via " + modelName
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
		case "q":
			if strings.TrimSpace(m.input.Value()) == "" {
				return m, m.exitCmd()
			}
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
	if layout.narrow {
		return m.renderNarrow(layout)
	}

	top := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderChat(layout),
		" ",
		m.renderRoom(layout.roomWidth, layout.topHeight),
	)
	bottom := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderPanel("On My Desk", OnMyDeskText(m.snapshot, m.now()), layout.deskWidth, layout.bottomHeight),
		" ",
		m.renderPanel("Notebook", NotebookText(m.snapshot), layout.notebookWidth, layout.bottomHeight),
	)
	return lipgloss.JoinVertical(lipgloss.Left, top, bottom)
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
	m.status = "Mina is thinking..."
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
	height = maxInt(18, height)

	if width < 78 {
		topHeight := maxInt(8, height/2)
		return bossLayout{
			width:            width,
			height:           height,
			topHeight:        topHeight,
			bottomHeight:     maxInt(5, height-topHeight),
			chatWidth:        width,
			roomWidth:        width,
			deskWidth:        width,
			notebookWidth:    width,
			chatInnerWidth:   maxInt(1, width-2),
			transcriptHeight: maxInt(2, topHeight-8),
			inputHeight:      3,
			narrow:           true,
		}
	}

	bottomHeight := clampInt(height/4, 7, 10)
	if height < 24 {
		bottomHeight = clampInt(height/3, 5, 8)
	}
	topHeight := maxInt(10, height-bottomHeight)
	roomWidth := clampInt(width/4, 24, 36)
	chatWidth := maxInt(40, width-roomWidth-1)
	deskWidth := clampInt(width/3, 26, width/2)
	notebookWidth := maxInt(24, width-deskWidth-1)
	chatInnerWidth := maxInt(1, chatWidth-2)
	inputHeight := 3
	transcriptHeight := maxInt(2, topHeight-3-inputHeight-1)
	return bossLayout{
		width:            width,
		height:           height,
		topHeight:        topHeight,
		bottomHeight:     bottomHeight,
		chatWidth:        chatWidth,
		roomWidth:        roomWidth,
		deskWidth:        deskWidth,
		notebookWidth:    notebookWidth,
		chatInnerWidth:   chatInnerWidth,
		transcriptHeight: transcriptHeight,
		inputHeight:      inputHeight,
	}
}

func (m Model) renderChat(layout bossLayout) string {
	hint := "Enter sends | Ctrl+J newline | Ctrl+R refresh | Esc quits"
	if m.sending {
		hint = "Mina is thinking " + spinnerDots(m.spinnerFrame)
	}
	content := strings.Join([]string{
		m.chatViewport.View(),
		fitLine(hint, layout.chatInnerWidth),
		m.input.View(),
	}, "\n")
	return m.renderRawPanel("Chat With Mina", content, layout.chatWidth, layout.topHeight)
}

func (m Model) renderRoom(width, height int) string {
	status := m.status
	if strings.TrimSpace(status) == "" {
		status = "Mina warming up"
	}
	weather := "calm"
	if m.snapshot.ConflictProjects > 0 {
		weather = "stormy"
	} else if m.snapshot.PossiblyStuckProjects > 0 {
		weather = "foggy"
	} else if m.snapshot.ActiveProjects > 0 {
		weather = "busy"
	}
	room := []string{
		"       /\\",
		"  ____/  \\____",
		" |  []    []  |",
		" |     Mina   |",
		" | desk  lamp |",
		" |__log____#__|",
		"",
		"Project weather: " + weather,
		status,
	}
	if m.stateErr != nil {
		room = append(room, "State: "+m.stateErr.Error())
	} else if m.stateLoaded {
		room = append(room, fmt.Sprintf("Watching %d projects", m.snapshot.TotalProjects))
	} else {
		room = append(room, "Loading project board...")
	}
	return m.renderPanel("Little Room", strings.Join(room, "\n"), width, height)
}

func (m Model) renderPanel(title, content string, width, height int) string {
	width = maxInt(12, width)
	height = maxInt(4, height)
	innerWidth := maxInt(1, width-2)
	innerHeight := maxInt(1, height-2)
	bodyHeight := maxInt(0, innerHeight-1)
	titleLine := panelTitleStyle.Render(fitLine(title, innerWidth))
	body := fitBlock(content, innerWidth, bodyHeight)
	return panelStyle.Width(innerWidth).Height(innerHeight).Render(titleLine + "\n" + body)
}

func (m Model) renderRawPanel(title, content string, width, height int) string {
	width = maxInt(12, width)
	height = maxInt(4, height)
	innerWidth := maxInt(1, width-2)
	innerHeight := maxInt(1, height-2)
	titleLine := panelTitleStyle.Render(fitLine(title, innerWidth))
	return panelStyle.Width(innerWidth).Height(innerHeight).Render(titleLine + "\n" + content)
}

func (m Model) renderNarrow(layout bossLayout) string {
	chat := m.renderChat(layout)
	roomHeight := maxInt(7, layout.bottomHeight/2)
	room := m.renderRoom(layout.roomWidth, roomHeight)
	desk := m.renderPanel("On My Desk", OnMyDeskText(m.snapshot, m.now()), layout.deskWidth, roomHeight)
	return lipgloss.JoinVertical(lipgloss.Left, chat, room, desk)
}

func (m Model) renderTranscript(width int) string {
	width = maxInt(12, width)
	var blocks []string
	for _, message := range m.messages {
		label := "You"
		if normalizeChatRole(message.Role) == "assistant" {
			label = "Mina"
		}
		blockLines := []string{label + ":"}
		blockLines = append(blockLines, wrapText(message.Content, width)...)
		blocks = append(blocks, strings.Join(blockLines, "\n"))
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
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("65")).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("235"))
	panelTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("58")).
			Bold(true)
)

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
