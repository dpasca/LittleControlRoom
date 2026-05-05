package pixelart

const (
	OperatorWidth  = 10
	OperatorHeight = 13
)

const OperatorWalkCycleSize = 32

type Color struct {
	R uint8
	G uint8
	B uint8
}

type OperatorPose int

const (
	OperatorInspect OperatorPose = iota
	OperatorWalkA
	OperatorWalkB
	OperatorTypeA
	OperatorTypeB
)

type OperatorState struct {
	X      int
	Y      int
	Facing int
	Pose   OperatorPose
	Blink  bool
}

var (
	OperatorOutlineColor     = RGB(26, 31, 38)
	OperatorSkinColor        = RGB(240, 198, 136)
	OperatorHairColor        = RGB(123, 75, 41)
	OperatorShirtColor       = RGB(75, 142, 194)
	OperatorShirtAccentColor = RGB(141, 203, 232)
	OperatorPantsColor       = RGB(76, 54, 42)
	OperatorBootColor        = RGB(48, 35, 31)
)

func RGB(r, g, b uint8) Color {
	return Color{R: r, G: g, B: b}
}

func OperatorWalkCycle(leftStop, rightStop, baseY, phase int) OperatorState {
	if rightStop < leftStop {
		leftStop, rightStop = rightStop, leftStop
	}
	if rightStop < leftStop+2 {
		rightStop = leftStop + 2
	}
	phase %= OperatorWalkCycleSize
	if phase < 0 {
		phase += OperatorWalkCycleSize
	}

	span := max(2, rightStop-leftStop)
	stepOne := min(rightStop, leftStop+max(1, span/4))
	stepTwo := min(rightStop, leftStop+max(2, span/2))
	stepThree := min(rightStop, leftStop+max(3, (3*span)/4))

	switch phase {
	case 0, 1, 2, 3, 4, 5:
		return OperatorState{X: leftStop, Y: baseY + 1, Facing: -1, Pose: OperatorInspect, Blink: phase == 2}
	case 6:
		return OperatorState{X: stepOne, Y: baseY, Facing: 1, Pose: OperatorWalkA}
	case 7:
		return OperatorState{X: stepOne, Y: baseY + 1, Facing: 1, Pose: OperatorWalkB}
	case 8:
		return OperatorState{X: stepTwo, Y: baseY, Facing: 1, Pose: OperatorWalkA}
	case 9:
		return OperatorState{X: stepTwo, Y: baseY + 1, Facing: 1, Pose: OperatorWalkB}
	case 10:
		return OperatorState{X: stepThree, Y: baseY, Facing: 1, Pose: OperatorWalkA}
	case 11:
		return OperatorState{X: stepThree, Y: baseY + 1, Facing: 1, Pose: OperatorWalkB}
	case 12, 13, 14, 15, 16, 17, 18, 19, 20, 21:
		return OperatorState{X: rightStop, Y: baseY, Facing: 1, Pose: OperatorTypeA, Blink: phase == 16}
	case 22:
		return OperatorState{X: stepThree, Y: baseY, Facing: -1, Pose: OperatorWalkA}
	case 23:
		return OperatorState{X: stepThree, Y: baseY + 1, Facing: -1, Pose: OperatorWalkB}
	case 24:
		return OperatorState{X: stepTwo, Y: baseY, Facing: -1, Pose: OperatorWalkA}
	case 25:
		return OperatorState{X: stepTwo, Y: baseY + 1, Facing: -1, Pose: OperatorWalkB}
	case 26:
		return OperatorState{X: stepOne, Y: baseY, Facing: -1, Pose: OperatorWalkA}
	case 27:
		return OperatorState{X: stepOne, Y: baseY + 1, Facing: -1, Pose: OperatorWalkB}
	case 28, 29, 30, 31:
		return OperatorState{X: leftStop, Y: baseY + 1, Facing: -1, Pose: OperatorInspect, Blink: phase == 30}
	default:
		return OperatorState{X: leftStop, Y: baseY + 1, Facing: -1, Pose: OperatorInspect}
	}
}

func DrawOperator(set func(x, y int, color Color), state OperatorState) {
	if set == nil {
		return
	}
	facing := state.Facing
	if facing == 0 {
		facing = 1
	}

	px := func(dx int) int {
		if facing < 0 {
			return state.X + (OperatorWidth - 1 - dx)
		}
		return state.X + dx
	}
	setPixel := func(dx, dy int, color Color) {
		set(px(dx), state.Y+dy, color)
	}
	fillRect := func(dx, dy, width, height int, color Color) {
		for row := 0; row < height; row++ {
			for col := 0; col < width; col++ {
				setPixel(dx+col, dy+row, color)
			}
		}
	}
	fillHLine := func(dx, dy, width int, color Color) {
		fillRect(dx, dy, width, 1, color)
	}
	fillVLine := func(dx, dy, height int, color Color) {
		fillRect(dx, dy, 1, height, color)
	}

	fillHLine(1, 0, 8, OperatorHairColor)
	fillHLine(0, 1, 10, OperatorHairColor)
	fillRect(0, 2, 10, 2, OperatorSkinColor)
	fillRect(1, 4, 8, 1, OperatorSkinColor)
	fillRect(4, 5, 2, 1, OperatorSkinColor)

	if state.Blink {
		fillHLine(3, 3, 1, OperatorOutlineColor)
		fillHLine(6, 3, 1, OperatorOutlineColor)
	} else {
		setPixel(3, 3, OperatorOutlineColor)
		setPixel(6, 3, OperatorOutlineColor)
	}

	fillRect(2, 6, 6, 4, OperatorShirtColor)
	fillHLine(3, 7, 4, OperatorShirtAccentColor)
	fillHLine(2, 9, 6, OperatorOutlineColor)

	switch state.Pose {
	case OperatorInspect:
		fillVLine(1, 7, 2, OperatorShirtColor)
		setPixel(1, 9, OperatorSkinColor)
		setPixel(0, 6, OperatorShirtColor)
		fillHLine(1, 6, 2, OperatorShirtColor)
		setPixel(8, 7, OperatorShirtColor)
		setPixel(8, 8, OperatorSkinColor)
	case OperatorWalkA:
		fillVLine(1, 7, 2, OperatorShirtColor)
		setPixel(1, 9, OperatorSkinColor)
		fillVLine(8, 6, 2, OperatorShirtColor)
		setPixel(8, 8, OperatorSkinColor)
	case OperatorWalkB:
		fillVLine(1, 6, 2, OperatorShirtColor)
		setPixel(1, 8, OperatorSkinColor)
		fillVLine(8, 7, 2, OperatorShirtColor)
		setPixel(8, 9, OperatorSkinColor)
	case OperatorTypeA:
		fillVLine(1, 7, 2, OperatorShirtColor)
		setPixel(1, 9, OperatorSkinColor)
		fillHLine(8, 6, 2, OperatorShirtColor)
		setPixel(9, 7, OperatorSkinColor)
	case OperatorTypeB:
		fillVLine(1, 7, 2, OperatorShirtColor)
		setPixel(1, 9, OperatorSkinColor)
		fillHLine(7, 7, 2, OperatorShirtColor)
		setPixel(9, 6, OperatorShirtColor)
		setPixel(9, 7, OperatorSkinColor)
	}

	switch state.Pose {
	case OperatorWalkA:
		fillRect(2, 10, 2, 3, OperatorPantsColor)
		fillRect(6, 10, 2, 2, OperatorPantsColor)
		fillRect(2, 12, 2, 1, OperatorBootColor)
		fillRect(7, 12, 2, 1, OperatorBootColor)
	case OperatorWalkB:
		fillRect(2, 10, 2, 2, OperatorPantsColor)
		fillRect(6, 10, 2, 3, OperatorPantsColor)
		fillRect(1, 12, 2, 1, OperatorBootColor)
		fillRect(6, 12, 2, 1, OperatorBootColor)
	default:
		fillRect(2, 10, 2, 3, OperatorPantsColor)
		fillRect(6, 10, 2, 3, OperatorPantsColor)
		fillRect(2, 12, 2, 1, OperatorBootColor)
		fillRect(6, 12, 2, 1, OperatorBootColor)
	}
}
