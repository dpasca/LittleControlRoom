package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/pixelart"
)

const (
	runtimeFlairMinWidth  = 24
	runtimeFlairMinHeight = 7
	runtimeFlairCycleSize = pixelart.OperatorWalkCycleSize
)

var (
	runtimeFlairOutlineColor      = runtimeFlairRGB(26, 31, 38)
	runtimeFlairDeskColor         = runtimeFlairRGB(107, 79, 56)
	runtimeFlairDeskShadowColor   = runtimeFlairRGB(69, 52, 38)
	runtimeFlairKeyboardColor     = runtimeFlairRGB(110, 122, 132)
	runtimeFlairMonitorColor      = runtimeFlairRGB(79, 214, 203)
	runtimeFlairMonitorDimColor   = runtimeFlairRGB(52, 156, 149)
	runtimeFlairMonitorFrameColor = runtimeFlairRGB(44, 69, 74)
	runtimeFlairFloorColor        = runtimeFlairRGB(50, 56, 66)
	runtimeFlairFloorEdgeColor    = runtimeFlairRGB(84, 94, 107)
	runtimeFlairPanelGlowColor    = runtimeFlairRGB(49, 102, 109)
	runtimeFlairSignalGoodColor   = runtimeFlairRGB(105, 218, 121)
	runtimeFlairSignalWarmColor   = runtimeFlairRGB(244, 196, 88)
	runtimeFlairSignalHotColor    = runtimeFlairRGB(255, 108, 88)
	runtimeFlairMugColor          = runtimeFlairRGB(216, 209, 196)
)

type runtimeFlairColor struct {
	r     uint8
	g     uint8
	b     uint8
	valid bool
}

type runtimeFlairCanvas struct {
	width  int
	height int
	pixels []runtimeFlairColor
}

type runtimeFlairOperatorPose int

const (
	runtimeFlairOperatorInspect runtimeFlairOperatorPose = iota
	runtimeFlairOperatorWalkA
	runtimeFlairOperatorWalkB
	runtimeFlairOperatorTypeA
	runtimeFlairOperatorTypeB
)

type runtimeFlairOperatorState struct {
	x      int
	y      int
	facing int
	pose   runtimeFlairOperatorPose
	blink  bool
}

func runtimeFlairRGB(r, g, b uint8) runtimeFlairColor {
	return runtimeFlairColor{r: r, g: g, b: b, valid: true}
}

func newRuntimeFlairCanvas(width, height int) runtimeFlairCanvas {
	return runtimeFlairCanvas{
		width:  max(0, width),
		height: max(0, height),
		pixels: make([]runtimeFlairColor, max(0, width)*max(0, height)),
	}
}

func (c *runtimeFlairCanvas) set(x, y int, color runtimeFlairColor) {
	if x < 0 || y < 0 || x >= c.width || y >= c.height {
		return
	}
	c.pixels[y*c.width+x] = color
}

func (c *runtimeFlairCanvas) fillRect(x, y, width, height int, color runtimeFlairColor) {
	if width <= 0 || height <= 0 || !color.valid {
		return
	}
	xStart := max(0, x)
	yStart := max(0, y)
	xEnd := min(c.width, x+width)
	yEnd := min(c.height, y+height)
	for py := yStart; py < yEnd; py++ {
		rowOffset := py * c.width
		for px := xStart; px < xEnd; px++ {
			c.pixels[rowOffset+px] = color
		}
	}
}

func (c *runtimeFlairCanvas) drawOutlinedRect(x, y, width, height int, outline, fill runtimeFlairColor) {
	if width <= 0 || height <= 0 {
		return
	}
	if outline.valid {
		c.fillRect(x, y, width, height, outline)
	}
	if fill.valid && width > 2 && height > 2 {
		c.fillRect(x+1, y+1, width-2, height-2, fill)
	}
}

func (c runtimeFlairCanvas) colorAt(x, y int) runtimeFlairColor {
	if x < 0 || y < 0 || x >= c.width || y >= c.height {
		return runtimeFlairColor{}
	}
	return c.pixels[y*c.width+x]
}

func (c runtimeFlairCanvas) render() string {
	if c.width <= 0 || c.height <= 0 {
		return ""
	}

	rows := c.height / 2
	if rows <= 0 {
		return ""
	}

	var builder strings.Builder
	for row := 0; row < rows; row++ {
		if row > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(c.renderRow(row))
	}
	return builder.String()
}

func (c runtimeFlairCanvas) renderRow(row int) string {
	topY := row * 2
	bottomY := topY + 1

	var builder strings.Builder
	var pending strings.Builder
	currentStyle := ""

	flushPending := func() {
		if pending.Len() == 0 {
			return
		}
		text := pending.String()
		pending.Reset()
		if currentStyle == "" {
			builder.WriteString(text)
			return
		}
		builder.WriteString(currentStyle)
		builder.WriteString(text)
		builder.WriteString("\x1b[0m")
	}

	for x := 0; x < c.width; x++ {
		top := c.colorAt(x, topY)
		bottom := c.colorAt(x, bottomY)
		style, glyph := runtimeFlairCell(top, bottom)
		if style != currentStyle {
			flushPending()
			currentStyle = style
		}
		pending.WriteString(glyph)
	}
	flushPending()
	return builder.String()
}

func runtimeFlairCell(top, bottom runtimeFlairColor) (string, string) {
	switch {
	case !top.valid && !bottom.valid:
		return "", " "
	case top.valid && bottom.valid:
		if top == bottom {
			return runtimeFlairANSIStyle(top, runtimeFlairColor{}), "\u2588"
		}
		return runtimeFlairANSIStyle(top, bottom), "\u2580"
	case top.valid:
		return runtimeFlairANSIStyle(top, runtimeFlairColor{}), "\u2580"
	default:
		return runtimeFlairANSIStyle(bottom, runtimeFlairColor{}), "\u2584"
	}
}

func runtimeFlairANSIStyle(fg, bg runtimeFlairColor) string {
	if !fg.valid {
		return ""
	}
	if bg.valid {
		return fmt.Sprintf("\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm", fg.r, fg.g, fg.b, bg.r, bg.g, bg.b)
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", fg.r, fg.g, fg.b)
}

func runtimeFlairPhase(spinnerFrame int) int {
	return (spinnerFrame / 4) % pixelart.OperatorWalkCycleSize
}

func (m Model) renderRuntimeFlairPanel(width, height int) (string, bool) {
	if width < runtimeFlairMinWidth || height < runtimeFlairMinHeight {
		return "", false
	}

	projectPath := m.runtimePanelProjectPath()
	if strings.TrimSpace(projectPath) == "" {
		return "", false
	}

	project, ok := m.projectSummaryByPath(projectPath)
	if !ok {
		return "", false
	}
	snapshot := m.projectRuntimeSnapshot(projectPath)
	if runtimeDetailAvailable(project.RunCommand, snapshot) {
		return "", false
	}

	projectName := strings.TrimSpace(project.Name)
	if projectName == "" {
		projectName = projectPath
	}

	header := fitStyledWidth(detailSectionStyle.Render("Control Room - "+truncateText(projectName, max(8, width-15))), width)
	art := runtimeFlairRenderScene(width, max(4, height-2), runtimeFlairPhase(m.spinnerFrame))
	footerText := "Standby. Use /run or /run-edit to wake the room."
	switch {
	case width < 34:
		footerText = "Standby. Use /run."
	case width < 48:
		footerText = "Standby. Use /run or /run-edit."
	}
	footer := fitStyledWidth(detailMutedStyle.Render(footerText), width)
	return strings.Join([]string{header, art, footer}, "\n"), true
}

func runtimeFlairRenderScene(width, height, phase int) string {
	canvasHeight := max(2, height*2)
	canvas := newRuntimeFlairCanvas(width, canvasHeight)

	floorTop := max(0, canvasHeight-2)
	canvas.fillRect(0, floorTop, width, canvasHeight-floorTop, runtimeFlairFloorColor)
	if floorTop > 0 {
		canvas.fillRect(0, floorTop-1, width, 1, runtimeFlairFloorEdgeColor)
	}

	deskWidth := min(18, max(12, width/3))
	deskX := min(max((width*11)/20, 12), max(2, width-deskWidth-3))
	deskTopY := max(6, canvasHeight-7)
	runtimeFlairDrawDesk(&canvas, deskX, deskTopY, deskWidth, phase)

	panelY := max(1, deskTopY-6)
	hasStatusPanel := width >= 34
	if hasStatusPanel {
		runtimeFlairDrawStatusPanel(&canvas, 2, panelY, phase)
	}
	if width >= 48 && canvasHeight >= 18 {
		runtimeFlairDrawAuxMonitor(&canvas, min(width-10, deskX+deskWidth+2), max(2, deskTopY-4), phase)
	}

	operatorState := runtimeFlairOperatorSceneState(width, floorTop, deskX, phase, hasStatusPanel)
	runtimeFlairDrawOperator(&canvas, operatorState)

	return canvas.render()
}

func runtimeFlairOperatorSceneState(width, floorTop, deskX, phase int, hasStatusPanel bool) runtimeFlairOperatorState {
	baseY := max(1, floorTop-13)
	deskStop := max(2, deskX-10)
	if !hasStatusPanel {
		if phase < pixelart.OperatorWalkCycleSize/2 {
			return runtimeFlairOperatorState{x: deskStop, y: baseY, facing: 1, pose: runtimeFlairOperatorTypeA}
		}
		return runtimeFlairOperatorState{x: deskStop, y: baseY + 1, facing: 1, pose: runtimeFlairOperatorTypeB, blink: phase == 21 || phase == 27}
	}

	leftStop := max(2, min(5, deskStop))
	rightStop := max(leftStop+2, deskStop)
	return runtimeFlairOperatorStateFromPixelArt(pixelart.OperatorWalkCycle(leftStop, rightStop, baseY, phase))
}

func runtimeFlairDrawDesk(canvas *runtimeFlairCanvas, x, topY, width, phase int) {
	if width < 8 {
		return
	}
	beat := phase % pixelart.OperatorWalkCycleSize

	canvas.drawOutlinedRect(x, topY, width, 4, runtimeFlairOutlineColor, runtimeFlairDeskShadowColor)
	canvas.fillRect(x+1, topY+1, max(1, width-2), 2, runtimeFlairDeskColor)
	canvas.fillRect(x+2, topY-1, max(6, width-4), 1, runtimeFlairKeyboardColor)

	monitorWidth := min(10, max(7, width-4))
	monitorX := x + (width-monitorWidth)/2
	monitorY := max(1, topY-5)
	screenColor := runtimeFlairMonitorColor
	if beat == 7 || beat == 15 {
		screenColor = runtimeFlairMonitorDimColor
	}
	canvas.drawOutlinedRect(monitorX, monitorY, monitorWidth, 4, runtimeFlairOutlineColor, runtimeFlairMonitorFrameColor)
	canvas.fillRect(monitorX+1, monitorY+1, max(1, monitorWidth-2), 2, screenColor)
	canvas.fillRect(monitorX+(monitorWidth/2)-1, monitorY+4, 2, 1, runtimeFlairMonitorFrameColor)

	signalColor := runtimeFlairSignalGoodColor
	if beat == 4 || beat == 12 || beat == 16 {
		signalColor = runtimeFlairSignalWarmColor
	}
	if beat == 10 {
		signalColor = runtimeFlairSignalHotColor
	}
	canvas.fillRect(x+2, topY+1, 1, 1, signalColor)
	canvas.fillRect(x+4, topY+1, 1, 1, runtimeFlairSignalWarmColor)
	if width >= 12 {
		canvas.fillRect(x+width-3, topY, 2, 2, runtimeFlairMugColor)
		canvas.set(x+width-2, topY-1, runtimeFlairMugColor)
	}
}

func runtimeFlairDrawStatusPanel(canvas *runtimeFlairCanvas, x, y, phase int) {
	canvas.drawOutlinedRect(x, y, 8, 5, runtimeFlairOutlineColor, runtimeFlairPanelGlowColor)
	colors := []runtimeFlairColor{
		runtimeFlairSignalGoodColor,
		runtimeFlairSignalWarmColor,
		runtimeFlairSignalHotColor,
	}
	beat := (phase / 2) % len(colors)
	for i := 0; i < 3; i++ {
		canvas.fillRect(x+2+i*2, y+2, 1, 1, colors[(beat+i)%len(colors)])
	}
	canvas.fillRect(x+2, y+3, 4, 1, runtimeFlairMonitorDimColor)
}

func runtimeFlairDrawAuxMonitor(canvas *runtimeFlairCanvas, x, y, phase int) {
	canvas.drawOutlinedRect(x, y, 7, 4, runtimeFlairOutlineColor, runtimeFlairMonitorFrameColor)
	color := runtimeFlairMonitorDimColor
	if (phase/2)%2 == 0 {
		color = runtimeFlairMonitorColor
	}
	canvas.fillRect(x+1, y+1, 5, 2, color)
}

func runtimeFlairDrawOperator(canvas *runtimeFlairCanvas, state runtimeFlairOperatorState) {
	pixelart.DrawOperator(func(x, y int, color pixelart.Color) {
		canvas.set(x, y, runtimeFlairColorFromPixelArt(color))
	}, pixelart.OperatorState{
		X:      state.x,
		Y:      state.y,
		Facing: state.facing,
		Pose:   runtimeFlairOperatorPoseToPixelArt(state.pose),
		Blink:  state.blink,
	})
}

func runtimeFlairOperatorPoseToPixelArt(pose runtimeFlairOperatorPose) pixelart.OperatorPose {
	switch pose {
	case runtimeFlairOperatorWalkA:
		return pixelart.OperatorWalkA
	case runtimeFlairOperatorWalkB:
		return pixelart.OperatorWalkB
	case runtimeFlairOperatorTypeA:
		return pixelart.OperatorTypeA
	case runtimeFlairOperatorTypeB:
		return pixelart.OperatorTypeB
	default:
		return pixelart.OperatorInspect
	}
}

func runtimeFlairOperatorPoseFromPixelArt(pose pixelart.OperatorPose) runtimeFlairOperatorPose {
	switch pose {
	case pixelart.OperatorWalkA:
		return runtimeFlairOperatorWalkA
	case pixelart.OperatorWalkB:
		return runtimeFlairOperatorWalkB
	case pixelart.OperatorTypeA:
		return runtimeFlairOperatorTypeA
	case pixelart.OperatorTypeB:
		return runtimeFlairOperatorTypeB
	default:
		return runtimeFlairOperatorInspect
	}
}

func runtimeFlairOperatorStateFromPixelArt(state pixelart.OperatorState) runtimeFlairOperatorState {
	return runtimeFlairOperatorState{
		x:      state.X,
		y:      state.Y,
		facing: state.Facing,
		pose:   runtimeFlairOperatorPoseFromPixelArt(state.Pose),
		blink:  state.Blink,
	}
}

func runtimeFlairColorFromPixelArt(color pixelart.Color) runtimeFlairColor {
	return runtimeFlairRGB(color.R, color.G, color.B)
}
