package demorecord

import (
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

const (
	smartTextBaseDelay       = 45 * time.Millisecond
	smartTextPerChangeDelay  = 10 * time.Millisecond
	smartQuietMinimumDelay   = 25 * time.Millisecond
	smartQuietMaximumDelay   = 100 * time.Millisecond
	smartMeaningfulDelayCap  = 350 * time.Millisecond
	smartLargeChangeDelayCap = 550 * time.Millisecond
	smartFinalTextHold       = 700 * time.Millisecond
	smartRecentTextWindow    = 60 * time.Second
	smartInteractionWindow   = 2500 * time.Millisecond
	smartMinimumDelay        = 10 * time.Millisecond
)

// SmartTimingState carries only geometric screen-change history. It never
// records a key identity or performs language/content classification.
type SmartTimingState struct {
	lastTextAtMS            int64
	hasText                 bool
	lastInteractionAtMS     int64
	hasInteraction          bool
	lastObservedInteraction int64
	hasObservedInteraction  bool
}

// ObserveInteraction adds one coarse recorder interaction marker. Markers do
// not contain the key, mouse button, pasted text, or any other input value.
func (s *SmartTimingState) ObserveInteraction(atMS int64) {
	if s.hasObservedInteraction && atMS <= s.lastObservedInteraction {
		return
	}
	s.lastObservedInteraction = atMS
	s.hasObservedInteraction = true
	s.lastInteractionAtMS = atMS
	s.hasInteraction = true
}

// SmartDelay returns the edited delay before next is displayed. SourceDelay is
// the original timeline gap. IdleLimit remains a user-selected upper bound for
// ordinary meaningful changes; the post-input readability hold is deliberate
// and may be longer.
func (s *SmartTimingState) SmartDelay(current, next Frame, sourceDelay, idleLimit time.Duration) time.Duration {
	if sourceDelay < 0 {
		sourceDelay = 0
	}
	change := analyzeSmartFrameChange(current, next)
	recentText := s.hasRecentText(next.AtMS)

	switch change.kind {
	case smartChangeText:
		if s.hasRecentInteraction(next.AtMS) {
			s.hasText = true
			s.lastTextAtMS = next.AtMS
		}
		units := clampInt(change.nonSpaceEdits, 1, 8)
		return smartTextBaseDelay + time.Duration(units-1)*smartTextPerChangeDelay
	case smartChangeQuiet:
		return clampDuration(sourceDelay/8, smartQuietMinimumDelay, smartQuietMaximumDelay)
	case smartChangeLarge:
		s.hasText = false
		s.hasInteraction = false
		delay := capSmartDelay(sourceDelay, smartLargeChangeDelayCap, idleLimit)
		if recentText && delay < smartFinalTextHold {
			return smartFinalTextHold
		}
		return delay
	default:
		return capSmartDelay(sourceDelay, smartMeaningfulDelayCap, idleLimit)
	}
}

func (s SmartTimingState) hasRecentInteraction(atMS int64) bool {
	if !s.hasInteraction {
		return false
	}
	elapsed := time.Duration(atMS-s.lastInteractionAtMS) * time.Millisecond
	return elapsed >= 0 && elapsed <= smartInteractionWindow
}

// SmartExitDelay keeps a just-finished text entry readable when a clip ends
// immediately afterward; otherwise it applies the normal meaningful-gap cap.
func (s *SmartTimingState) SmartExitDelay(atMS int64, sourceDelay, idleLimit time.Duration) time.Duration {
	delay := capSmartDelay(sourceDelay, smartMeaningfulDelayCap, idleLimit)
	if s.hasRecentText(atMS) && delay < smartFinalTextHold {
		return smartFinalTextHold
	}
	return delay
}

func (s SmartTimingState) hasRecentText(atMS int64) bool {
	if !s.hasText {
		return false
	}
	elapsed := time.Duration(atMS-s.lastTextAtMS) * time.Millisecond
	return elapsed >= 0 && elapsed <= smartRecentTextWindow
}

type smartChangeKind uint8

const (
	smartChangeMeaningful smartChangeKind = iota
	smartChangeQuiet
	smartChangeText
	smartChangeLarge
)

type smartFrameChange struct {
	kind          smartChangeKind
	changedRows   int
	changedChars  int
	nonSpaceEdits int
}

func analyzeSmartFrameChange(current, next Frame) smartFrameChange {
	if current.Width != next.Width || current.Height != next.Height {
		return smartFrameChange{kind: smartChangeLarge}
	}
	before := normalizedSmartLines(current.View)
	after := normalizedSmartLines(next.View)
	lineCount := max(len(before), len(after))
	firstChanged := -1
	lastChanged := -1
	change := smartFrameChange{kind: smartChangeMeaningful}
	for row := 0; row < lineCount; row++ {
		oldLine := smartLineAt(before, row)
		newLine := smartLineAt(after, row)
		if oldLine == newLine {
			continue
		}
		if firstChanged < 0 {
			firstChanged = row
		}
		lastChanged = row
		change.changedRows++
		change.changedChars += visibleEditSpan(oldLine, newLine)
		change.nonSpaceEdits += absInt(countNonSpace(newLine) - countNonSpace(oldLine))
	}

	if change.changedRows == 0 {
		change.kind = smartChangeQuiet
		return change
	}
	span := lastChanged - firstChanged + 1
	width := max(1, max(current.Width, next.Width))
	rows := max(1, max(max(current.Height, next.Height), lineCount))
	localizedText := change.changedRows <= 6 &&
		span <= 8 &&
		change.nonSpaceEdits > 0 &&
		change.changedChars <= max(1024, width*6)
	if localizedText {
		change.kind = smartChangeText
		return change
	}
	quiet := change.changedRows <= 2 &&
		change.nonSpaceEdits == 0 &&
		change.changedChars <= max(100, width)
	if quiet {
		change.kind = smartChangeQuiet
		return change
	}
	large := change.changedRows >= max(4, rows/6) ||
		change.changedChars >= max(160, width*2)
	if large {
		change.kind = smartChangeLarge
	}
	return change
}

func normalizedSmartLines(view string) []string {
	lines := strings.Split(ansi.Strip(view), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " \t\r")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func smartLineAt(lines []string, row int) string {
	if row < 0 || row >= len(lines) {
		return ""
	}
	return lines[row]
}

func visibleEditSpan(before, after string) int {
	left := []rune(before)
	right := []rune(after)
	prefix := 0
	for prefix < len(left) && prefix < len(right) && left[prefix] == right[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(left)-prefix && suffix < len(right)-prefix &&
		left[len(left)-1-suffix] == right[len(right)-1-suffix] {
		suffix++
	}
	return max(len(left)-prefix-suffix, len(right)-prefix-suffix)
}

func countNonSpace(value string) int {
	count := 0
	for _, r := range value {
		if !unicode.IsSpace(r) {
			count++
		}
	}
	return count
}

func capSmartDelay(source, cap, idleLimit time.Duration) time.Duration {
	if idleLimit > 0 && idleLimit < cap {
		cap = idleLimit
	}
	if source > cap {
		source = cap
	}
	if source < smartMinimumDelay {
		return smartMinimumDelay
	}
	return source
}

func clampDuration(value, low, high time.Duration) time.Duration {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
