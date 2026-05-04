package boss

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const (
	bossCompanionMinWidth  = 56
	bossCompanionMinHeight = 6
	bossCompanionGlyph     = "\u2588"
	bossCompanionReset     = "\x1b[0m"
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
	bossCompanionHairColor       = bossCompanionRGB(123, 75, 41)
	bossCompanionSkinColor       = bossCompanionRGB(240, 198, 136)
	bossCompanionOutlineColor    = bossCompanionRGB(26, 31, 38)
	bossCompanionShirtColor      = bossCompanionRGB(75, 142, 194)
	bossCompanionShirtLightColor = bossCompanionRGB(141, 203, 232)
	bossCompanionPantsColor      = bossCompanionRGB(76, 54, 42)
	bossCompanionBootColor       = bossCompanionRGB(48, 35, 31)
	bossCompanionHeadsetColor    = bossCompanionRGB(244, 196, 88)
	bossCompanionSignalColor     = bossCompanionRGB(79, 214, 203)
	bossCompanionSignalDimColor  = bossCompanionRGB(52, 156, 149)
	bossCompanionPanelColor      = bossCompanionRGB(0, 0, 0)
)

func bossCompanionRGB(r, g, b uint8) bossCompanionColor {
	return bossCompanionColor{r: r, g: g, b: b}
}

func (m Model) renderChatCompanion(transcript string, layout bossLayout) string {
	if layout.chatInnerWidth < bossCompanionMinWidth || layout.transcriptHeight < bossCompanionMinHeight {
		return transcript
	}

	mood := m.bossCompanionMood()
	sprite := renderBossCompanionSprite(mood, m.spinnerFrame)
	if sprite.width == 0 || sprite.height == 0 {
		return transcript
	}

	x := layout.chatInnerWidth - sprite.width - 1
	y := layout.transcriptHeight - sprite.height
	if mood == bossCompanionStreaming {
		y = maxInt(0, layout.transcriptHeight-sprite.height-1)
	}
	return overlayBossCompanion(transcript, layout.chatInnerWidth, layout.transcriptHeight, x, y, sprite)
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

func renderBossCompanionSprite(mood bossCompanionMood, frame int) bossCompanionSprite {
	sprite := newBossCompanionSprite(11, 6)
	beat := frame % 4
	blink := frame%13 == 7

	sprite.fillRect(3, 0, 5, 1, bossCompanionHairColor)
	sprite.fillRect(2, 1, 7, 1, bossCompanionHairColor)
	sprite.fillRect(2, 2, 7, 2, bossCompanionSkinColor)
	sprite.set(1, 2, bossCompanionHeadsetColor)
	sprite.set(9, 2, bossCompanionHeadsetColor)
	sprite.set(9, 3, bossCompanionHeadsetColor)

	if blink {
		sprite.set(4, 3, bossCompanionOutlineColor)
		sprite.set(6, 3, bossCompanionOutlineColor)
	} else {
		sprite.set(4, 2, bossCompanionOutlineColor)
		sprite.set(6, 2, bossCompanionOutlineColor)
	}

	sprite.fillRect(3, 4, 5, 1, bossCompanionShirtColor)
	sprite.fillRect(4, 4, 3, 1, bossCompanionShirtLightColor)
	sprite.fillRect(3, 5, 2, 1, bossCompanionPantsColor)
	sprite.fillRect(6, 5, 2, 1, bossCompanionPantsColor)
	sprite.set(2, 5, bossCompanionBootColor)
	sprite.set(8, 5, bossCompanionBootColor)

	switch mood {
	case bossCompanionThinking:
		if beat == 0 || beat == 2 {
			sprite.set(1, 4, bossCompanionShirtColor)
			sprite.set(2, 4, bossCompanionShirtColor)
			sprite.set(8, 4, bossCompanionShirtColor)
			sprite.set(9, 4, bossCompanionSkinColor)
		} else {
			sprite.set(1, 5, bossCompanionShirtColor)
			sprite.set(2, 5, bossCompanionSkinColor)
			sprite.set(8, 5, bossCompanionShirtColor)
			sprite.set(9, 5, bossCompanionSkinColor)
		}
		sprite.set(0, 1, bossCompanionSignalDimColor)
		sprite.set(0, 0, bossCompanionSignalColor)
	case bossCompanionStreaming:
		if beat == 0 || beat == 1 {
			sprite.set(1, 4, bossCompanionShirtColor)
			sprite.set(2, 4, bossCompanionSkinColor)
			sprite.set(8, 4, bossCompanionShirtColor)
			sprite.set(9, 4, bossCompanionSkinColor)
			sprite.set(10, 3, bossCompanionSignalColor)
			sprite.set(10, 4, bossCompanionSignalDimColor)
		} else {
			sprite.set(1, 5, bossCompanionShirtColor)
			sprite.set(2, 5, bossCompanionSkinColor)
			sprite.set(8, 5, bossCompanionShirtColor)
			sprite.set(9, 5, bossCompanionSkinColor)
			sprite.set(10, 2, bossCompanionSignalColor)
			sprite.set(10, 3, bossCompanionSignalDimColor)
		}
	default:
		sprite.set(1, 5, bossCompanionShirtColor)
		sprite.set(2, 5, bossCompanionSkinColor)
		sprite.set(8, 5, bossCompanionShirtColor)
		sprite.set(9, 5, bossCompanionSkinColor)
	}

	return sprite
}

func overlayBossCompanion(base string, width, height, x, y int, sprite bossCompanionSprite) string {
	if width <= 0 || height <= 0 || sprite.width <= 0 || sprite.height <= 0 {
		return base
	}
	if x < 0 || y < 0 || x+sprite.width > width || y+sprite.height > height {
		return base
	}

	lines := strings.Split(strings.ReplaceAll(base, "\r\n", "\n"), "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = fitStyledLine(lines[i], width)
	}

	for sy := 0; sy < sprite.height; sy++ {
		lineIndex := y + sy
		for sx := 0; sx < sprite.width; sx++ {
			col := x + sx
			if !bossCompanionCellIsBlank(lines[lineIndex], col) {
				return base
			}
		}
	}

	for sy := 0; sy < sprite.height; sy++ {
		lineIndex := y + sy
		lines[lineIndex] = fitStyledLine(
			ansi.Cut(lines[lineIndex], 0, x)+sprite.renderRow(sy)+ansi.Cut(lines[lineIndex], x+sprite.width, width),
			width,
		)
	}

	return strings.Join(lines, "\n")
}

func bossCompanionCellIsBlank(line string, col int) bool {
	if col < 0 {
		return false
	}
	plain := ansi.Strip(line)
	if col >= ansi.StringWidth(plain) {
		return true
	}
	return strings.TrimSpace(ansi.Cut(plain, col, col+1)) == ""
}

func (cell bossCompanionCell) render() string {
	if !cell.filled {
		return bossCompanionPanelSpaces(1)
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s%s", cell.color.r, cell.color.g, cell.color.b, bossCompanionGlyph, bossCompanionReset)
}

func (s bossCompanionSprite) renderRow(y int) string {
	var builder strings.Builder
	for x := 0; x < s.width; x++ {
		builder.WriteString(s.cell(x, y).render())
	}
	return builder.String()
}

func (s bossCompanionSprite) renderRowsScaled(xScale, yScale int) []string {
	if s.width <= 0 || s.height <= 0 {
		return nil
	}
	xScale = maxInt(1, xScale)
	yScale = maxInt(1, yScale)
	rows := make([]string, 0, s.height*yScale)
	for y := 0; y < s.height; y++ {
		var builder strings.Builder
		for x := 0; x < s.width; x++ {
			cell := s.cell(x, y).render()
			for i := 0; i < xScale; i++ {
				builder.WriteString(cell)
			}
		}
		row := builder.String()
		for i := 0; i < yScale; i++ {
			rows = append(rows, row)
		}
	}
	return rows
}

func (s bossCompanionSprite) renderHalfRows() []string {
	return s.renderHalfRowsScaled(1, 1)
}

func (s bossCompanionSprite) renderHalfRowsScaled(xScale, yScale int) []string {
	if s.width <= 0 || s.height <= 0 {
		return nil
	}
	xScale = maxInt(1, xScale)
	yScale = maxInt(1, yScale)
	scaledWidth := s.width * xScale
	scaledHeight := s.height * yScale
	rows := make([]string, 0, (scaledHeight+1)/2)
	for y := 0; y < scaledHeight; y += 2 {
		var builder strings.Builder
		for x := 0; x < scaledWidth; x++ {
			top := s.scaledCell(x, y, xScale, yScale)
			bottom := s.scaledCell(x, y+1, xScale, yScale)
			builder.WriteString(renderBossCompanionHalfCell(top, bottom))
		}
		rows = append(rows, builder.String())
	}
	return rows
}

func (s bossCompanionSprite) scaledCell(x, y, xScale, yScale int) bossCompanionCell {
	if x < 0 || y < 0 || xScale <= 0 || yScale <= 0 {
		return bossCompanionCell{}
	}
	sourceX := x / xScale
	sourceY := y / yScale
	return s.cell(sourceX, sourceY)
}

func renderBossCompanionHalfCell(top, bottom bossCompanionCell) string {
	switch {
	case top.filled && bottom.filled:
		return renderBossCompanionHalfBlock(top.color, bottom.color)
	case top.filled:
		return renderBossCompanionHalfBlock(top.color, bossCompanionPanelColor)
	case bottom.filled:
		return renderBossCompanionHalfBlock(bossCompanionPanelColor, bottom.color)
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
