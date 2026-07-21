package demoedit

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/demorecord"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	defaultSeekStep     = time.Second
	largeSeekStep       = 10 * time.Second
	minuteSeekStep      = time.Minute
	largeTimelineStep   = 10 * time.Minute
	defaultIdleTime     = 2 * time.Second
	editorChromeRows    = 4
	minimumEditorWidth  = 48
	minimumEditorHeight = 12
)

var (
	editorTitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	editorMutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	editorTimeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
	editorInStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	editorOutStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	editorPlayStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	editorErrorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	editorHelpStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
)

type Model struct {
	reader   *demorecord.Reader
	manifest demorecord.Manifest
	edits    demorecord.EditProject

	width  int
	height int

	chunkPosition int
	frames        []demorecord.Frame
	frameIndex    int
	initialMS     int64
	positionMS    int64
	pendingSeekMS int64
	loading       bool
	playWhenReady bool

	inMS         int64
	outMS        int64
	idleLimit    time.Duration
	smartTiming  bool
	smartState   demorecord.SmartTimingState
	selectedClip int

	playing    bool
	playToken  uint64
	fullFrame  bool
	playerOnly bool
	busy       bool

	status string
	err    error
}

type chunkLoadedMsg struct {
	position int
	targetMS int64
	frames   []demorecord.Frame
	play     bool
	err      error
}

type playAdvanceMsg struct {
	token        uint64
	targetMS     int64
	stop         bool
	smartState   demorecord.SmartTimingState
	stateUpdated bool
}

type editsSavedMsg struct {
	project demorecord.EditProject
	status  string
	err     error
}

type exportedMsg struct {
	path string
	err  error
}

func New(reader *demorecord.Reader, edits demorecord.EditProject) (Model, error) {
	if reader == nil {
		return Model{}, fmt.Errorf("demo recording reader is required")
	}
	manifest := reader.Manifest()
	if len(manifest.Chunks) == 0 || manifest.FrameCount == 0 {
		return Model{}, fmt.Errorf("demo recording has no completed frames")
	}
	if edits.Version == 0 {
		edits = demorecord.DefaultEditProject()
	}
	outMS := manifest.DurationMS
	if outMS <= 0 {
		outMS = manifest.Chunks[len(manifest.Chunks)-1].EndMS
	}
	model := Model{
		reader:        reader,
		manifest:      manifest,
		edits:         edits,
		chunkPosition: -1,
		inMS:          0,
		outMS:         outMS,
		idleLimit:     defaultIdleTime,
		smartTiming:   true,
		selectedClip:  -1,
		status:        "Loading the first recorded frame…",
	}
	return model, nil
}

func NewPlayer(reader *demorecord.Reader, clip demorecord.Clip) (Model, error) {
	if reader == nil {
		return Model{}, fmt.Errorf("demo recording reader is required")
	}
	manifest := reader.Manifest()
	if err := clip.Validate(manifest.DurationMS); err != nil {
		return Model{}, err
	}
	model, err := New(reader, demorecord.DefaultEditProject())
	if err != nil {
		return Model{}, err
	}
	model.initialMS = clip.InMS
	model.positionMS = clip.InMS
	model.inMS = clip.InMS
	model.outMS = clip.OutMS
	model.fullFrame = true
	model.playerOnly = true
	model.playing = true
	model.playToken = 1
	model.smartTiming = clip.SmartTiming
	switch {
	case clip.IdleTimeLimitMS > 0:
		model.idleLimit = time.Duration(clip.IdleTimeLimitMS) * time.Millisecond
	case clip.IdleTimeLimitMS < 0:
		model.idleLimit = 0
	default:
		model.idleLimit = defaultIdleTime
	}
	model.status = "Loading playback…"
	return model, nil
}

func (m Model) Init() tea.Cmd {
	position := m.reader.ChunkIndexAt(m.initialMS)
	return loadChunkCmd(m.reader, position, m.initialMS, m.playing)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case chunkLoadedMsg:
		return m.applyChunkLoaded(msg)
	case playAdvanceMsg:
		if msg.token != m.playToken || !m.playing {
			return m, nil
		}
		if msg.stateUpdated {
			m.smartState = msg.smartState
		}
		if msg.stop {
			m.playing = false
			m.positionMS = m.outMS
			m.status = "Selection playback finished"
			return m, nil
		}
		return m.seekTo(msg.targetMS, true)
	case editsSavedMsg:
		m.busy = false
		if msg.err != nil {
			m.err = msg.err
			m.status = "Saving clips failed"
			return m, nil
		}
		m.edits = msg.project
		m.err = nil
		m.status = msg.status
		return m, nil
	case exportedMsg:
		m.busy = false
		if msg.err != nil {
			m.err = msg.err
			m.status = "Export failed"
			return m, nil
		}
		m.err = nil
		m.status = "Exported " + filepath.Base(msg.path)
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "q" || key == "ctrl+c" {
		if m.busy {
			m.status = "Wait for the current save or export to finish"
			return m, nil
		}
		return m, tea.Quit
	}
	if m.loading || m.busy {
		return m, nil
	}
	if m.playerOnly {
		switch key {
		case " ":
			return m.togglePlayback()
		case "left", "h":
			m.stopPlayback()
			return m.seekTo(m.positionMS-defaultSeekStep.Milliseconds(), false)
		case "right", "l":
			m.stopPlayback()
			return m.seekTo(m.positionMS+defaultSeekStep.Milliseconds(), false)
		case "home", "g":
			m.stopPlayback()
			return m.seekTo(m.inMS, false)
		}
		return m, nil
	}

	switch key {
	case " ":
		return m.togglePlayback()
	case "left", "h":
		m.stopPlayback()
		return m.seekTo(m.positionMS-defaultSeekStep.Milliseconds(), false)
	case "right", "l":
		m.stopPlayback()
		return m.seekTo(m.positionMS+defaultSeekStep.Milliseconds(), false)
	case "shift+left", "H":
		m.stopPlayback()
		return m.seekTo(m.positionMS-largeSeekStep.Milliseconds(), false)
	case "shift+right", "L":
		m.stopPlayback()
		return m.seekTo(m.positionMS+largeSeekStep.Milliseconds(), false)
	case "pgup":
		m.stopPlayback()
		return m.seekTo(m.positionMS-minuteSeekStep.Milliseconds(), false)
	case "pgdown":
		m.stopPlayback()
		return m.seekTo(m.positionMS+minuteSeekStep.Milliseconds(), false)
	case "ctrl+left":
		m.stopPlayback()
		return m.seekTo(m.positionMS-largeTimelineStep.Milliseconds(), false)
	case "ctrl+right":
		m.stopPlayback()
		return m.seekTo(m.positionMS+largeTimelineStep.Milliseconds(), false)
	case "[":
		m.stopPlayback()
		return m.jumpInteraction(false)
	case "]":
		m.stopPlayback()
		return m.jumpInteraction(true)
	case "home", "g":
		m.stopPlayback()
		return m.seekTo(0, false)
	case "end", "G":
		m.stopPlayback()
		return m.seekTo(m.manifest.DurationMS, false)
	case "i":
		m.stopPlayback()
		m.inMS = m.positionMS
		if m.outMS <= m.inMS {
			m.outMS = minInt64(m.manifest.DurationMS, m.inMS+30_000)
		}
		m.status = "Set selection in-point"
		return m, nil
	case "o":
		m.stopPlayback()
		if m.positionMS <= m.inMS {
			m.err = fmt.Errorf("out-point must be after the in-point")
			m.status = "Out-point was not changed"
			return m, nil
		}
		m.outMS = m.positionMS
		m.err = nil
		m.status = "Set selection out-point"
		return m, nil
	case "n":
		m.stopPlayback()
		m.selectedClip = -1
		m.smartTiming = true
		m.smartState = demorecord.SmartTimingState{}
		m.inMS = m.positionMS
		m.outMS = minInt64(m.manifest.DurationMS, m.inMS+30_000)
		if m.outMS <= m.inMS {
			m.inMS = maxInt64(0, m.outMS-30_000)
		}
		m.status = "Started a new clip selection"
		return m, nil
	case "s":
		return m.saveSelection()
	case "tab":
		return m.selectNextClip()
	case "backspace", "delete":
		return m.deleteSelectedClip()
	case "e":
		return m.exportSelection()
	case "f":
		m.fullFrame = !m.fullFrame
		if m.fullFrame {
			m.status = "Full-frame preview; press f to restore editor controls"
		} else {
			m.status = "Editor controls restored"
		}
		return m, nil
	case "d":
		m.cycleIdleLimit()
		return m, nil
	case "t":
		m.smartTiming = !m.smartTiming
		m.smartState = demorecord.SmartTimingState{}
		if m.smartTiming {
			m.status = "Smart timing on"
		} else {
			m.status = "Smart timing off"
		}
		return m, nil
	}
	return m, nil
}

func (m Model) togglePlayback() (tea.Model, tea.Cmd) {
	if m.playing {
		m.stopPlayback()
		m.status = "Playback paused"
		return m, nil
	}
	if m.outMS <= m.inMS {
		m.err = fmt.Errorf("set an out-point after the in-point")
		m.status = "Selection cannot play"
		return m, nil
	}
	m.err = nil
	m.playing = true
	m.playToken++
	m.smartState = demorecord.SmartTimingState{}
	if m.positionMS < m.inMS || m.positionMS >= m.outMS {
		return m.seekTo(m.inMS, true)
	}
	m.status = "Playing selection"
	return m, m.scheduleNextFrame()
}

func (m *Model) stopPlayback() {
	m.playing = false
	m.playToken++
	m.playWhenReady = false
	m.smartState = demorecord.SmartTimingState{}
}

func (m Model) seekTo(targetMS int64, continuePlaying bool) (tea.Model, tea.Cmd) {
	low := int64(0)
	high := m.manifest.DurationMS
	if m.playerOnly {
		low = m.inMS
		high = m.outMS
	}
	targetMS = clampInt64(targetMS, low, high)
	chunkPosition := m.reader.ChunkIndexAt(targetMS)
	if chunkPosition < 0 {
		m.err = fmt.Errorf("recording has no frame at %s", formatDurationMS(targetMS))
		return m, nil
	}
	if chunkPosition == m.chunkPosition && len(m.frames) > 0 {
		m.frameIndex = frameIndexAt(m.frames, targetMS)
		m.positionMS = targetMS
		m.loading = false
		if continuePlaying && m.playing {
			m.status = "Playing selection"
			return m, m.scheduleNextFrame()
		}
		return m, nil
	}
	m.loading = true
	m.pendingSeekMS = targetMS
	m.playWhenReady = continuePlaying && m.playing
	m.status = "Loading recorded frames…"
	return m, loadChunkCmd(m.reader, chunkPosition, targetMS, m.playWhenReady)
}

func (m Model) applyChunkLoaded(msg chunkLoadedMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.err = msg.err
		m.playing = false
		m.status = "Loading recorded frames failed"
		return m, nil
	}
	if len(msg.frames) == 0 {
		m.err = fmt.Errorf("recording chunk %d is empty", msg.position)
		m.playing = false
		m.status = "Loading recorded frames failed"
		return m, nil
	}
	m.chunkPosition = msg.position
	m.frames = msg.frames
	m.frameIndex = frameIndexAt(msg.frames, msg.targetMS)
	m.positionMS = msg.targetMS
	m.err = nil
	if msg.play && m.playing {
		m.status = "Playing selection"
		return m, m.scheduleNextFrame()
	}
	m.status = "Ready"
	return m, nil
}

func (m Model) scheduleNextFrame() tea.Cmd {
	if !m.playing || len(m.frames) == 0 {
		return nil
	}
	currentAt := m.positionMS
	nextAt := int64(-1)
	var nextFrame demorecord.Frame
	haveNextFrame := false
	for i := m.frameIndex + 1; i < len(m.frames); i++ {
		if m.frames[i].AtMS > currentAt {
			nextAt = m.frames[i].AtMS
			nextFrame = m.frames[i]
			haveNextFrame = true
			break
		}
	}
	if nextAt < 0 && m.chunkPosition+1 < len(m.manifest.Chunks) {
		nextAt = m.manifest.Chunks[m.chunkPosition+1].StartMS
	}
	stop := nextAt < 0 || nextAt > m.outMS
	if stop {
		nextAt = m.outMS
	}
	sourceDelay := time.Duration(maxInt64(0, nextAt-currentAt)) * time.Millisecond
	delay := sourceDelay
	state := m.smartState
	stateUpdated := false
	if m.smartTiming {
		observeLatestSmartInteraction(&state, m.manifest.InteractionMS, nextAt)
	}
	if m.smartTiming && stop {
		delay = state.SmartExitDelay(nextAt, sourceDelay, m.idleLimit)
		stateUpdated = true
	} else if m.smartTiming && haveNextFrame {
		if currentFrame, ok := m.currentFrame(); ok {
			delay = state.SmartDelay(currentFrame, nextFrame, sourceDelay, m.idleLimit)
			stateUpdated = true
		}
	} else if m.idleLimit > 0 && delay > m.idleLimit {
		delay = m.idleLimit
	}
	if delay < 10*time.Millisecond {
		delay = 10 * time.Millisecond
	}
	token := m.playToken
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return playAdvanceMsg{
			token:        token,
			targetMS:     nextAt,
			stop:         stop,
			smartState:   state,
			stateUpdated: stateUpdated,
		}
	})
}

func observeLatestSmartInteraction(state *demorecord.SmartTimingState, markers []int64, atMS int64) {
	if state == nil || len(markers) == 0 {
		return
	}
	index := sort.Search(len(markers), func(index int) bool { return markers[index] > atMS }) - 1
	if index >= 0 {
		state.ObserveInteraction(markers[index])
	}
}

func (m Model) saveSelection() (tea.Model, tea.Cmd) {
	clip := m.currentSelectionClip()
	if err := clip.Validate(m.manifest.DurationMS); err != nil {
		m.err = err
		m.status = "Selection was not saved"
		return m, nil
	}
	project := cloneEdits(m.edits)
	status := ""
	if m.selectedClip >= 0 && m.selectedClip < len(project.Clips) {
		clip.ID = project.Clips[m.selectedClip].ID
		clip.Name = project.Clips[m.selectedClip].Name
		project.Clips[m.selectedClip] = clip
		status = "Updated " + clip.Name
	} else {
		clip.ID = demorecord.NextClipID(project)
		clip.Name = fmt.Sprintf("Clip %d", len(project.Clips)+1)
		project.Clips = append(project.Clips, clip)
		status = "Saved " + clip.Name
		m.selectedClip = len(project.Clips) - 1
	}
	m.busy = true
	m.err = nil
	m.status = "Saving clip…"
	return m, saveEditsCmd(m.reader.Path(), project, m.manifest.DurationMS, status)
}

func (m Model) deleteSelectedClip() (tea.Model, tea.Cmd) {
	if m.selectedClip < 0 || m.selectedClip >= len(m.edits.Clips) {
		m.status = "No saved clip is selected"
		return m, nil
	}
	project := cloneEdits(m.edits)
	name := project.Clips[m.selectedClip].Name
	project.Clips = append(project.Clips[:m.selectedClip], project.Clips[m.selectedClip+1:]...)
	m.selectedClip = -1
	m.busy = true
	m.err = nil
	m.status = "Deleting clip…"
	return m, saveEditsCmd(m.reader.Path(), project, m.manifest.DurationMS, "Deleted "+name)
}

func (m Model) selectNextClip() (tea.Model, tea.Cmd) {
	if len(m.edits.Clips) == 0 {
		m.selectedClip = -1
		m.status = "No saved clips"
		return m, nil
	}
	m.stopPlayback()
	m.selectedClip = (m.selectedClip + 1) % len(m.edits.Clips)
	clip := m.edits.Clips[m.selectedClip]
	m.inMS = clip.InMS
	m.outMS = clip.OutMS
	if clip.IdleTimeLimitMS > 0 {
		m.idleLimit = time.Duration(clip.IdleTimeLimitMS) * time.Millisecond
	} else if clip.IdleTimeLimitMS < 0 {
		m.idleLimit = 0
	} else {
		m.idleLimit = defaultIdleTime
	}
	m.smartTiming = clip.SmartTiming
	m.smartState = demorecord.SmartTimingState{}
	m.status = "Selected " + clip.Name
	return m.seekTo(clip.InMS, false)
}

func (m Model) jumpInteraction(next bool) (tea.Model, tea.Cmd) {
	markers := m.manifest.InteractionMS
	if len(markers) == 0 {
		m.status = "This recording has no interaction markers"
		return m, nil
	}
	index := sort.Search(len(markers), func(i int) bool {
		return markers[i] >= m.positionMS
	})
	if next {
		for index < len(markers) && markers[index] <= m.positionMS {
			index++
		}
		if index >= len(markers) {
			m.status = "Already at the last interaction"
			return m, nil
		}
		m.status = "Jumped to next interaction"
		return m.seekTo(markers[index], false)
	}
	if index >= len(markers) || markers[index] >= m.positionMS {
		index--
	}
	if index < 0 {
		m.status = "Already at the first interaction"
		return m, nil
	}
	m.status = "Jumped to previous interaction"
	return m.seekTo(markers[index], false)
}

func (m Model) exportSelection() (tea.Model, tea.Cmd) {
	clip := m.currentSelectionClip()
	if err := clip.Validate(m.manifest.DurationMS); err != nil {
		m.err = err
		m.status = "Selection was not exported"
		return m, nil
	}
	if m.selectedClip < 0 {
		clip.ID = "selection"
		clip.Name = "Selection"
	} else if m.selectedClip < len(m.edits.Clips) {
		clip.ID = m.edits.Clips[m.selectedClip].ID
		clip.Name = m.edits.Clips[m.selectedClip].Name
	}
	outputPath := demorecord.DefaultExportPath(m.reader.Path(), clip)
	m.stopPlayback()
	m.busy = true
	m.err = nil
	m.status = "Exporting asciicast…"
	return m, exportCmd(m.reader, clip, outputPath)
}

func (m *Model) cycleIdleLimit() {
	options := []time.Duration{0, 500 * time.Millisecond, time.Second, 2 * time.Second, 5 * time.Second}
	current := 0
	for i, option := range options {
		if option == m.idleLimit {
			current = i
			break
		}
	}
	m.idleLimit = options[(current+1)%len(options)]
	if m.idleLimit == 0 {
		m.status = "Idle-time compression off"
	} else {
		m.status = "Idle gaps limited to " + m.idleLimit.String()
	}
}

func (m Model) currentSelectionClip() demorecord.Clip {
	idleMS := m.idleLimit.Milliseconds()
	if m.idleLimit == 0 {
		idleMS = -1
	}
	return demorecord.Clip{
		ID:              "selection",
		Name:            "Selection",
		InMS:            m.inMS,
		OutMS:           m.outMS,
		IdleTimeLimitMS: idleMS,
		SmartTiming:     m.smartTiming,
	}
}

func (m Model) currentFrame() (demorecord.Frame, bool) {
	if m.frameIndex < 0 || m.frameIndex >= len(m.frames) {
		return demorecord.Frame{}, false
	}
	return m.frames[m.frameIndex], true
}

func (m Model) View() string {
	width := m.width
	height := m.height
	if width <= 0 {
		width = 120
	}
	if height <= 0 {
		height = 36
	}
	if width < minimumEditorWidth || height < minimumEditorHeight {
		return editorErrorStyle.Render(fmt.Sprintf(
			"Demo editor needs at least %dx%d cells; current terminal is %dx%d.",
			minimumEditorWidth,
			minimumEditorHeight,
			width,
			height,
		))
	}

	frame, ok := m.currentFrame()
	if !ok {
		return renderLoadingView(width, height, m.status, m.err)
	}
	if m.fullFrame {
		return fitRecordedFrame(frame.View, width, height)
	}

	contentHeight := maxInt(1, height-editorChromeRows)
	lines := []string{fitRecordedFrame(frame.View, width, contentHeight)}
	lines = append(lines, m.renderInfoLine(width))
	lines = append(lines, m.renderTimeline(width))
	lines = append(lines, m.renderSelectionLine(width))
	lines = append(lines, m.renderHelpLine(width))
	return strings.Join(lines, "\n")
}

func (m Model) renderInfoLine(width int) string {
	state := "PAUSED"
	if m.playing {
		state = editorPlayStyle.Render("PLAY")
	} else if m.loading {
		state = editorMutedStyle.Render("LOAD")
	} else if m.busy {
		state = editorMutedStyle.Render("BUSY")
	}
	title := editorTitleStyle.Render("LCR DEMO") + " " +
		editorTimeStyle.Render(formatDurationMS(m.positionMS)) + " / " +
		formatDurationMS(m.manifest.DurationMS) + "  " + state
	if m.selectedClip >= 0 && m.selectedClip < len(m.edits.Clips) {
		title += "  " + editorMutedStyle.Render(m.edits.Clips[m.selectedClip].Name)
	}
	right := m.status
	if m.err != nil {
		right = editorErrorStyle.Render(m.err.Error())
	}
	return lineWithRight(title, right, width)
}

func (m Model) renderTimeline(width int) string {
	label := " "
	timelineWidth := maxInt(8, width-2)
	cells := make([]rune, timelineWidth)
	for i := range cells {
		cells[i] = '─'
	}
	if m.manifest.DurationMS > 0 {
		inIndex := timelineIndex(m.inMS, m.manifest.DurationMS, timelineWidth)
		outIndex := timelineIndex(m.outMS, m.manifest.DurationMS, timelineWidth)
		for i := inIndex; i <= outIndex && i < len(cells); i++ {
			cells[i] = '═'
		}
		for _, clip := range m.edits.Clips {
			index := timelineIndex(clip.InMS, m.manifest.DurationMS, timelineWidth)
			cells[index] = '│'
		}
		for _, marker := range m.manifest.InteractionMS {
			index := timelineIndex(marker, m.manifest.DurationMS, timelineWidth)
			if cells[index] == '─' {
				cells[index] = '┊'
			}
		}
		cells[inIndex] = 'I'
		cells[outIndex] = 'O'
		playIndex := timelineIndex(m.positionMS, m.manifest.DurationMS, timelineWidth)
		cells[playIndex] = '◆'
	}
	return label + string(cells)
}

func (m Model) renderSelectionLine(width int) string {
	idle := "off"
	if m.idleLimit > 0 {
		idle = m.idleLimit.String()
	}
	timing := "source"
	if m.smartTiming {
		timing = "smart"
	} else if m.idleLimit > 0 {
		timing = "idle-cap"
	}
	left := editorInStyle.Render("IN "+formatDurationMS(m.inMS)) + "  " +
		editorOutStyle.Render("OUT "+formatDurationMS(m.outMS)) + "  " +
		fmt.Sprintf("length %s · timing %s · idle ≤ %s", formatDurationMS(m.outMS-m.inMS), timing, idle)
	bytes := int64(0)
	for _, chunk := range m.manifest.Chunks {
		bytes += chunk.Bytes
	}
	right := fmt.Sprintf("%d frames · %s", m.manifest.FrameCount, formatBytes(bytes))
	if m.manifest.DroppedFrames > 0 {
		right += fmt.Sprintf(" · %d dropped", m.manifest.DroppedFrames)
	}
	return lineWithRight(left, editorMutedStyle.Render(right), width)
}

func (m Model) renderHelpLine(width int) string {
	help := "←/→ 1s  ⇧←/⇧→ 10s  PgUp/PgDn 1m  [/] activity  space play  i/o mark  n new  s save  tab clips  d idle  t timing  e export  f full  q"
	return ansi.Truncate(editorHelpStyle.Render(help), width, "")
}

func loadChunkCmd(reader *demorecord.Reader, position int, targetMS int64, play bool) tea.Cmd {
	return func() tea.Msg {
		frames, err := reader.LoadChunk(position)
		return chunkLoadedMsg{
			position: position,
			targetMS: targetMS,
			frames:   frames,
			play:     play,
			err:      err,
		}
	}
}

func saveEditsCmd(path string, project demorecord.EditProject, durationMS int64, status string) tea.Cmd {
	return func() tea.Msg {
		err := demorecord.SaveEdits(path, project, durationMS)
		return editsSavedMsg{project: project, status: status, err: err}
	}
}

func exportCmd(reader *demorecord.Reader, clip demorecord.Clip, outputPath string) tea.Cmd {
	return func() tea.Msg {
		err := demorecord.ExportAsciicast(reader, clip, outputPath)
		return exportedMsg{path: outputPath, err: err}
	}
}

func cloneEdits(project demorecord.EditProject) demorecord.EditProject {
	project.Clips = append([]demorecord.Clip(nil), project.Clips...)
	return project
}

func frameIndexAt(frames []demorecord.Frame, atMS int64) int {
	if len(frames) == 0 {
		return -1
	}
	index := sort.Search(len(frames), func(i int) bool {
		return frames[i].AtMS > atMS
	}) - 1
	if index < 0 {
		return 0
	}
	return index
}

func fitRecordedFrame(view string, width, height int) string {
	rawLines := strings.Split(view, "\n")
	if len(rawLines) > height {
		rawLines = rawLines[:height]
	}
	lines := make([]string, 0, height)
	for _, line := range rawLines {
		line = ansi.Truncate(line, width, "")
		if padding := width - ansi.StringWidth(line); padding > 0 {
			line += strings.Repeat(" ", padding)
		}
		lines = append(lines, line)
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

func renderLoadingView(width, height int, status string, err error) string {
	text := strings.TrimSpace(status)
	if err != nil {
		text = err.Error()
	}
	if text == "" {
		text = "Loading demo recording…"
	}
	style := editorTitleStyle
	if err != nil {
		style = editorErrorStyle
	}
	top := maxInt(0, height/2)
	lines := make([]string, 0, height)
	for len(lines) < top {
		lines = append(lines, "")
	}
	lines = append(lines, ansi.Truncate(style.Render(text), width, ""))
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func lineWithRight(left, right string, width int) string {
	left = ansi.Truncate(left, width, "")
	rightWidth := ansi.StringWidth(right)
	if rightWidth == 0 {
		return left
	}
	available := width - rightWidth - 2
	if available <= 0 {
		return ansi.Truncate(right, width, "")
	}
	left = ansi.Truncate(left, available, "")
	padding := width - ansi.StringWidth(left) - rightWidth
	if padding < 1 {
		padding = 1
	}
	return left + strings.Repeat(" ", padding) + right
}

func timelineIndex(value, duration int64, width int) int {
	if width <= 1 || duration <= 0 {
		return 0
	}
	value = clampInt64(value, 0, duration)
	return int((value * int64(width-1)) / duration)
}

func formatDurationMS(milliseconds int64) string {
	if milliseconds < 0 {
		milliseconds = 0
	}
	duration := time.Duration(milliseconds) * time.Millisecond
	hours := int64(duration / time.Hour)
	duration -= time.Duration(hours) * time.Hour
	minutes := int64(duration / time.Minute)
	duration -= time.Duration(minutes) * time.Minute
	seconds := int64(duration / time.Second)
	tenths := int64((duration % time.Second) / (100 * time.Millisecond))
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d.%d", hours, minutes, seconds, tenths)
	}
	return fmt.Sprintf("%02d:%02d.%d", minutes, seconds, tenths)
}

func formatBytes(bytes int64) string {
	const (
		kib = int64(1024)
		mib = 1024 * kib
		gib = 1024 * mib
	)
	switch {
	case bytes >= gib:
		return fmt.Sprintf("%.1f GiB", float64(bytes)/float64(gib))
	case bytes >= mib:
		return fmt.Sprintf("%.1f MiB", float64(bytes)/float64(mib))
	case bytes >= kib:
		return fmt.Sprintf("%.1f KiB", float64(bytes)/float64(kib))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func clampInt64(value, low, high int64) int64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
