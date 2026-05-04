package pixelart

const (
	OperatorStationWidth  = 22
	OperatorStationHeight = 15
)

type OperatorStationState struct {
	Width  int
	Pose   OperatorPose
	Blink  bool
	Facing int
	Phase  int
}

var (
	StationDeskColor         = RGB(107, 79, 56)
	StationDeskShadowColor   = RGB(69, 52, 38)
	StationKeyboardColor     = RGB(110, 122, 132)
	StationMonitorColor      = RGB(79, 214, 203)
	StationMonitorDimColor   = RGB(52, 156, 149)
	StationMonitorFrameColor = RGB(44, 69, 74)
	StationFloorColor        = RGB(50, 56, 66)
	StationFloorEdgeColor    = RGB(84, 94, 107)
	StationPanelGlowColor    = RGB(49, 102, 109)
	StationSignalGoodColor   = RGB(105, 218, 121)
	StationSignalWarmColor   = RGB(244, 196, 88)
	StationSignalHotColor    = RGB(255, 108, 88)
	StationMugColor          = RGB(216, 209, 196)
)

func DrawOperatorStation(set func(x, y int, color Color), state OperatorStationState) {
	if set == nil {
		return
	}
	stationWidth := state.Width
	if stationWidth < OperatorStationWidth {
		stationWidth = OperatorStationWidth
	}
	operatorX := 8
	panelX := 17
	if stationWidth > OperatorStationWidth {
		panelX = stationWidth - 5
		operatorX = (stationWidth - OperatorWidth) / 2
		if operatorX < 8 {
			operatorX = 8
		}
		if operatorX+OperatorWidth >= panelX {
			operatorX = panelX - OperatorWidth - 1
		}
		if operatorX < 8 {
			operatorX = 8
		}
	}

	fillRect := func(x, y, width, height int, color Color) {
		for row := 0; row < height; row++ {
			for col := 0; col < width; col++ {
				set(x+col, y+row, color)
			}
		}
	}
	drawOutlinedRect := func(x, y, width, height int, outline, fill Color) {
		if width <= 0 || height <= 0 {
			return
		}
		fillRect(x, y, width, height, outline)
		if width > 2 && height > 2 {
			fillRect(x+1, y+1, width-2, height-2, fill)
		}
	}

	fillRect(0, 14, stationWidth, 1, StationFloorColor)
	fillRect(0, 13, stationWidth, 1, StationFloorEdgeColor)
	fillRect(1, 12, max(7, operatorX-1), 1, StationDeskShadowColor)
	fillRect(operatorX+6, 12, max(4, stationWidth-operatorX-8), 1, StationDeskShadowColor)

	drawOutlinedRect(1, 4, 6, 4, OperatorOutlineColor, StationMonitorFrameColor)
	fillRect(2, 5, 4, 2, StationMonitorColor)
	if (state.Phase/2)%2 == 1 {
		fillRect(2, 5, 4, 2, StationMonitorDimColor)
	}
	fillRect(3, 8, 2, 2, StationMonitorFrameColor)
	fillRect(1, 10, 6, 1, StationKeyboardColor)
	fillRect(2, 11, 4, 1, StationDeskColor)

	drawOutlinedRect(panelX, 3, 4, 5, OperatorOutlineColor, StationPanelGlowColor)
	signals := []Color{StationSignalGoodColor, StationSignalWarmColor, StationSignalHotColor}
	beat := state.Phase % len(signals)
	set(panelX+1, 5, signals[beat])
	set(panelX+2, 5, signals[(beat+1)%len(signals)])
	fillRect(panelX+1, 6, 2, 1, StationMonitorDimColor)
	fillRect(panelX+1, 10, 2, 2, StationMugColor)
	set(panelX+2, 9, StationMugColor)

	DrawOperator(set, OperatorState{
		X:      operatorX,
		Y:      1,
		Facing: state.Facing,
		Pose:   state.Pose,
		Blink:  state.Blink,
	})
}
