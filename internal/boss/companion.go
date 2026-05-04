package boss

import (
	"fmt"
	"strings"

	"lcroom/internal/pixelart"
)

const (
	bossCompanionReset = "\x1b[0m"
)

type bossCompanionMood int

const (
	bossCompanionIdle bossCompanionMood = iota
	bossCompanionThinking
	bossCompanionStreaming
)

type bossCompanionColor struct {
	r uint8
	g uint8
	b uint8
}

type bossCompanionCell struct {
	color  bossCompanionColor
	filled bool
}

type bossCompanionSprite struct {
	width  int
	height int
	cells  []bossCompanionCell
}

var (
	bossCompanionSignalColor    = bossCompanionRGB(79, 214, 203)
	bossCompanionSignalDimColor = bossCompanionRGB(52, 156, 149)
	bossCompanionPanelColor     = bossCompanionRGB(0, 0, 0)
)

func bossCompanionRGB(r, g, b uint8) bossCompanionColor {
	return bossCompanionColor{r: r, g: g, b: b}
}

func (m Model) bossCompanionMood() bossCompanionMood {
	if m.sending {
		if strings.TrimSpace(m.streamingAssistantText) != "" {
			return bossCompanionStreaming
		}
		return bossCompanionThinking
	}
	if len(m.activeEngineerActivities()) > 0 {
		return bossCompanionThinking
	}
	return bossCompanionIdle
}

func newBossCompanionSprite(width, height int) bossCompanionSprite {
	return bossCompanionSprite{
		width:  maxInt(0, width),
		height: maxInt(0, height),
		cells:  make([]bossCompanionCell, maxInt(0, width)*maxInt(0, height)),
	}
}

func (s *bossCompanionSprite) set(x, y int, color bossCompanionColor) {
	if x < 0 || y < 0 || x >= s.width || y >= s.height {
		return
	}
	s.cells[y*s.width+x] = bossCompanionCell{color: color, filled: true}
}

func (s *bossCompanionSprite) fillRect(x, y, width, height int, color bossCompanionColor) {
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			s.set(x+col, y+row, color)
		}
	}
}

func (s bossCompanionSprite) cell(x, y int) bossCompanionCell {
	if x < 0 || y < 0 || x >= s.width || y >= s.height {
		return bossCompanionCell{}
	}
	return s.cells[y*s.width+x]
}

func renderBossCompanionSprite(mood bossCompanionMood, frame, width int) bossCompanionSprite {
	width = maxInt(pixelart.OperatorStationWidth, width)
	sprite := newBossCompanionSprite(width, pixelart.OperatorStationHeight)
	beat := frame % 4
	pixelart.DrawOperatorStation(func(x, y int, color pixelart.Color) {
		sprite.set(x, y, bossCompanionColorFromPixelArt(color))
	}, pixelart.OperatorStationState{
		Width: width,
		Pose:  bossCompanionOperatorPose(mood, beat),
		Blink: frame%13 == 7,
		Phase: frame,
	})
	if mood == bossCompanionThinking {
		sprite.set(0, 0, bossCompanionSignalColor)
		sprite.set(0, 1, bossCompanionSignalDimColor)
		sprite.set(1, 0, bossCompanionSignalDimColor)
	}
	return sprite
}

func bossCompanionOperatorPose(mood bossCompanionMood, beat int) pixelart.OperatorPose {
	switch mood {
	case bossCompanionThinking:
		if beat == 0 || beat == 2 {
			return pixelart.OperatorTypeA
		}
		return pixelart.OperatorTypeB
	case bossCompanionStreaming:
		if beat == 0 || beat == 1 {
			return pixelart.OperatorTypeA
		}
		return pixelart.OperatorTypeB
	default:
		return pixelart.OperatorInspect
	}
}

func bossCompanionColorFromPixelArt(color pixelart.Color) bossCompanionColor {
	return bossCompanionRGB(color.R, color.G, color.B)
}

func (s bossCompanionSprite) renderHalfRows() []string {
	if s.width <= 0 || s.height <= 0 {
		return nil
	}
	rows := make([]string, 0, (s.height+1)/2)
	for y := 0; y < s.height; y += 2 {
		var builder strings.Builder
		for x := 0; x < s.width; x++ {
			top := s.cell(x, y)
			bottom := s.cell(x, y+1)
			builder.WriteString(renderBossCompanionHalfCell(top, bottom))
		}
		rows = append(rows, builder.String())
	}
	return rows
}

func renderBossCompanionHalfCell(top, bottom bossCompanionCell) string {
	switch {
	case top.filled && bottom.filled:
		return renderBossCompanionHalfBlock(top.color, bottom.color)
	case top.filled:
		return renderBossCompanionUpperHalfBlock(top.color)
	case bottom.filled:
		return renderBossCompanionLowerHalfBlock(bottom.color)
	default:
		return bossCompanionPanelSpaces(1)
	}
}

func renderBossCompanionHalfBlock(top, bottom bossCompanionColor) string {
	return fmt.Sprintf(
		"\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm\u2580%s",
		top.r, top.g, top.b,
		bottom.r, bottom.g, bottom.b,
		bossCompanionReset,
	)
}

func renderBossCompanionUpperHalfBlock(color bossCompanionColor) string {
	return fmt.Sprintf(
		"\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm\u2580%s",
		color.r, color.g, color.b,
		bossCompanionPanelColor.r, bossCompanionPanelColor.g, bossCompanionPanelColor.b,
		bossCompanionReset,
	)
}

func renderBossCompanionLowerHalfBlock(color bossCompanionColor) string {
	return fmt.Sprintf(
		"\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm\u2584%s",
		color.r, color.g, color.b,
		bossCompanionPanelColor.r, bossCompanionPanelColor.g, bossCompanionPanelColor.b,
		bossCompanionReset,
	)
}

func bossCompanionPanelSpaces(count int) string {
	if count <= 0 {
		return ""
	}
	return fmt.Sprintf(
		"\x1b[48;2;%d;%d;%dm%s%s",
		bossCompanionPanelColor.r,
		bossCompanionPanelColor.g,
		bossCompanionPanelColor.b,
		strings.Repeat(" ", count),
		bossCompanionReset,
	)
}
