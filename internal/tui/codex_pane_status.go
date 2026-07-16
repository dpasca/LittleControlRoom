package tui

import (
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"strings"
	"time"
)

func (m *Model) refreshCodexSubmitSnapshot(projectPath string, snapshot codexapp.Snapshot) (codexapp.Snapshot, bool, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || !codexSnapshotNeedsSubmitRefresh(snapshot) {
		return snapshot, true, false
	}
	return m.refreshCodexSnapshot(projectPath)
}

func codexSnapshotNeedsSubmitRefresh(snapshot codexapp.Snapshot) bool {
	if snapshot.BusyExternal {
		return true
	}
	switch snapshot.Phase {
	case codexapp.SessionPhaseFinishing, codexapp.SessionPhaseReconciling, codexapp.SessionPhaseStalled:
		return true
	default:
		return false
	}
}

func codexSnapshotCanSteer(snapshot codexapp.Snapshot) bool {
	if embeddedProvider(snapshot) == codexapp.ProviderLCAgent {
		return codexSnapshotBrowserWaitingForUser(snapshot)
	}
	return codexSnapshotCanInterruptActiveTurn(snapshot)
}

func codexSnapshotQueuesBusyInput(snapshot codexapp.Snapshot) bool {
	if embeddedProvider(snapshot) != codexapp.ProviderLCAgent {
		return false
	}
	return snapshot.Busy && !snapshot.BusyExternal && !codexSnapshotBrowserWaitingForUser(snapshot)
}

func codexSnapshotCanInterruptActiveTurn(snapshot codexapp.Snapshot) bool {
	if codexSnapshotGoalPausesOnPrompt(snapshot) {
		return false
	}
	switch snapshot.Phase {
	case "", codexapp.SessionPhaseRunning:
		if !snapshot.Busy || snapshot.BusyExternal {
			return false
		}
		if embeddedProvider(snapshot) == codexapp.ProviderCodex {
			return strings.TrimSpace(snapshot.ActiveTurnID) != ""
		}
		return true
	default:
		return false
	}
}

func codexSnapshotCanSubmitBusyInput(snapshot codexapp.Snapshot) bool {
	if !snapshot.Busy {
		return true
	}
	if snapshot.BusyExternal {
		return false
	}
	if embeddedProvider(snapshot) == codexapp.ProviderLCAgent {
		return snapshot.Phase == codexapp.SessionPhaseRunning || codexSnapshotBrowserWaitingForUser(snapshot)
	}
	return embeddedProvider(snapshot) != codexapp.ProviderLCAgent
}

func codexSnapshotBrowserWaitingForUser(snapshot codexapp.Snapshot) bool {
	return snapshot.BrowserActivity.Normalize().State == browserctl.SessionActivityStateWaitingForUser
}

func codexFooterStatus(snapshot codexapp.Snapshot, now time.Time) string {
	switch {
	case snapshot.PendingApproval != nil:
		return "Waiting for approval"
	case snapshot.PendingToolInput != nil:
		return "Waiting for your answer"
	case snapshot.PendingElicitation != nil && snapshot.PendingElicitation.Mode == codexapp.ElicitationModeURL:
		return "Waiting for browser input"
	case snapshot.PendingElicitation != nil && codexElicitationNeedsComposer(*snapshot.PendingElicitation):
		return "Waiting for tool input"
	case snapshot.PendingElicitation != nil:
		return "Waiting for your decision"
	}
	if !snapshot.Busy {
		switch {
		case snapshot.HistoryLoading:
			return "Loading older turns"
		case strings.TrimSpace(snapshot.HistoryLoadError) != "":
			return "Older history load failed"
		}
	}
	switch snapshot.Phase {
	case codexapp.SessionPhaseReconciling:
		if codexStatusIsCompacting(snapshot.Status) {
			return "Compacting conversation"
		}
		return "Rechecking turn; /reconnect if stuck"
	case codexapp.SessionPhaseStalled:
		return "Stalled; use /reconnect"
	case codexapp.SessionPhaseFinishing:
		if !snapshot.BusySince.IsZero() {
			return "Finishing " + formatRunningDuration(now.Sub(snapshot.BusySince))
		}
		return "Finishing"
	case codexapp.SessionPhaseExternal:
		if !snapshot.BusySince.IsZero() {
			return "Working elsewhere " + formatRunningDuration(now.Sub(snapshot.BusySince))
		}
		return "Working elsewhere"
	}
	if codexSnapshotBrowserWaitingForUser(snapshot) {
		return "Waiting for browser input"
	}
	if snapshot.Busy {
		if !snapshot.BusySince.IsZero() {
			return "Working " + formatRunningDuration(now.Sub(snapshot.BusySince))
		}
		return "Working"
	}
	return normalizedCodexStatus(snapshot.Status)
}

func formatRunningDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	totalSeconds := int64(d / time.Second)
	days := totalSeconds / (24 * 60 * 60)
	hours := (totalSeconds % (24 * 60 * 60)) / (60 * 60)
	minutes := (totalSeconds % (60 * 60)) / 60
	seconds := totalSeconds % 60

	switch {
	case days > 0:
		if hours > 0 {
			return fmt.Sprintf("%dd %02dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	case totalSeconds >= int64(time.Hour/time.Second):
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	default:
		return fmt.Sprintf("%02d:%02d", minutes, seconds)
	}
}

func numericOptionSelection(key string) (int, bool) {
	if len(key) != 1 {
		return 0, false
	}
	if key[0] < '1' || key[0] > '9' {
		return 0, false
	}
	return int(key[0] - '1'), true
}

func encodeElicitationComposerInput(text string) json.RawMessage {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if json.Valid([]byte(text)) {
		return json.RawMessage(text)
	}
	encoded, err := json.Marshal(text)
	if err != nil {
		return nil
	}
	return json.RawMessage(encoded)
}

func countRenderedBlockLines(blocks []string) int {
	total := 0
	for _, block := range blocks {
		if block == "" {
			total++
			continue
		}
		total += strings.Count(block, "\n") + 1
	}
	return total
}

func codexMouseWheelPress(msg tea.MouseMsg) bool {
	return msg.Action == tea.MouseActionPress && tea.MouseEvent(msg).IsWheel()
}

func codexHorizontalMouseWheel(msg tea.MouseMsg) bool {
	return msg.Button == tea.MouseButtonWheelLeft || msg.Button == tea.MouseButtonWheelRight
}

// codexViewportScreenTop returns the screen Y where the viewport content area
// begins (banner line + top border of the framed pane).
const codexViewportScreenTop = 2

// finalizeCodexSelection copies the selected text to the clipboard and
// clears the dragging state. It is called on mouse release and also as a
// fallback when a release event is missed (e.g. released over a non-tracked
// area like the banner).
func (m *Model) finalizeCodexSelection() {
	m.codexSelection.dragging = false
	if m.codexSelection.hasRange() {
		text := cleanCopiedText(m.codexSelection.extractText(m.codexTranscriptCache.rendered))
		if text != "" {
			if err := clipboardTextWriter(text); err == nil {
				m.status = "Copied selection to clipboard"
			} else {
				m.reportError("Selection copy failed", err, m.codexVisibleProject)
			}
		}
	}
}

// handleCodexMouseSelection processes left-button press/drag/release for text
// selection in the codex viewport. Returns (cmd, true) if the event was
// consumed, or (nil, false) to let the viewport handle it (e.g. scroll wheel).
func (m *Model) handleCodexMouseSelection(msg tea.MouseMsg) (tea.Cmd, bool) {
	switch msg.Action {
	case tea.MouseActionPress:
		// If we're still dragging from a previous cycle (missed release),
		// finalize that selection first.
		if m.codexSelection.dragging {
			m.finalizeCodexSelection()
		}
		if msg.Button != tea.MouseButtonLeft {
			m.codexSelection = textSelection{}
			return nil, false
		}
		row, col, ok := m.codexMouseToContent(msg.X, msg.Y)
		if !ok {
			m.codexSelection = textSelection{}
			return nil, false
		}
		m.codexSelection = textSelection{
			anchorRow:  row,
			anchorCol:  col,
			currentRow: row,
			currentCol: col,
			dragging:   true,
		}
		return nil, true

	case tea.MouseActionMotion:
		if !m.codexSelection.dragging {
			return nil, false
		}
		row, col, ok := m.codexMouseToContent(msg.X, msg.Y)
		if ok {
			m.codexSelection.currentRow = row
			m.codexSelection.currentCol = col
		}
		// Always consume motion during drag to prevent fallthrough from
		// clearing the selection when the mouse leaves the viewport area.
		return nil, true

	case tea.MouseActionRelease:
		if !m.codexSelection.dragging {
			return nil, false
		}
		row, col, ok := m.codexMouseToContent(msg.X, msg.Y)
		if ok {
			m.codexSelection.currentRow = row
			m.codexSelection.currentCol = col
		}
		m.finalizeCodexSelection()
		return nil, true
	}
	return nil, false
}

// codexMouseToContent converts screen mouse coordinates to content row/col.
func (m *Model) codexMouseToContent(screenX, screenY int) (row, col int, ok bool) {
	visLine := screenY - codexViewportScreenTop
	if visLine < 0 || visLine >= m.codexViewport.Height {
		return 0, 0, false
	}
	contentRow := visLine + m.codexViewport.YOffset
	if contentRow >= m.codexViewport.TotalLineCount() {
		return 0, 0, false
	}
	col = max(0, screenX)
	return contentRow, col, true
}
