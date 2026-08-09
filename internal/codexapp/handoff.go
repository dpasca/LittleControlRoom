package codexapp

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	sessionHandoffDirName             = "handoffs"
	maxSessionHandoffEntries          = 48
	maxSessionHandoffTranscriptBytes  = 96 * 1024
	maxSessionHandoffConversationText = 12 * 1024
	maxSessionHandoffActivityText     = 6 * 1024
	maxSessionHandoffDetailText       = 8 * 1024
	maxSessionHandoffLatestUserText   = 16 * 1024
)

var sessionHandoffSequence atomic.Uint64

// SessionHandoff is a host-generated, bounded continuation brief. It only
// uses state already cached by Little Control Room, so creating it does not
// depend on the embedded provider or on another model turn succeeding.
type SessionHandoff struct {
	path       string
	snapshot   Snapshot
	note       string
	capturedAt time.Time
}

// NewSessionHandoff plans a unique handoff artifact without touching disk.
// Write performs the filesystem work and is intended to run off the TUI's
// Update path.
func NewSessionHandoff(dataDir string, snapshot Snapshot, note string, capturedAt time.Time) (SessionHandoff, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return SessionHandoff{}, fmt.Errorf("app data directory required for embedded session handoff")
	}
	snapshot.ProjectPath = strings.TrimSpace(snapshot.ProjectPath)
	if snapshot.ProjectPath == "" {
		return SessionHandoff{}, fmt.Errorf("project path required for embedded session handoff")
	}
	snapshot.Provider = snapshot.Provider.Normalized()
	if snapshot.Provider == "" {
		return SessionHandoff{}, fmt.Errorf("embedded provider required for session handoff")
	}
	if capturedAt.IsZero() {
		capturedAt = time.Now()
	}
	capturedAt = capturedAt.UTC()

	projectDigest := sha256.Sum256([]byte(snapshot.ProjectPath))
	projectKey := fmt.Sprintf("%x", projectDigest[:6])
	sourceDigest := sha256.Sum256([]byte(snapshot.ThreadID + "\x00" + snapshot.ActiveTurnID))
	sourceKey := fmt.Sprintf("%x", sourceDigest[:4])
	sequence := sessionHandoffSequence.Add(1)
	filename := fmt.Sprintf(
		"%s-%s-%s-%06d.md",
		capturedAt.Format("20060102T150405.000000000Z"),
		snapshot.Provider,
		sourceKey,
		sequence,
	)

	return SessionHandoff{
		path: filepath.Join(
			filepath.Clean(dataDir),
			restartIntentDirName,
			sessionHandoffDirName,
			projectKey,
			filename,
		),
		snapshot:   snapshot,
		note:       strings.TrimSpace(note),
		capturedAt: capturedAt,
	}, nil
}

func (h SessionHandoff) Path() string {
	return h.path
}

// LaunchPrompt gives a fresh provider session the minimum host-authored
// instruction needed to consume the durable brief. The detailed context stays
// in the file rather than being duplicated into another large prompt.
func (h SessionHandoff) LaunchPrompt() string {
	return fmt.Sprintf(
		"Continue work from a failed or unavailable embedded session.\n\n"+
			"Little Control Room created a mechanical continuation brief at this exact local path:\n%s\n\n"+
			"Read it before proceeding. It may be incomplete and is not proof that the prior turn finished. "+
			"First inspect the current repository state and any relevant running processes, then continue the latest unresolved user request. "+
			"Do not repeat completed work; verify the current state before changing files.",
		strconv.Quote(h.path),
	)
}

// Write persists the handoff atomically with private file permissions.
func (h SessionHandoff) Write() error {
	if strings.TrimSpace(h.path) == "" {
		return fmt.Errorf("embedded session handoff path required")
	}
	dir := filepath.Dir(h.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create embedded session handoff directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure embedded session handoff directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".handoff-*.md")
	if err != nil {
		return fmt.Errorf("create embedded session handoff temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure embedded session handoff: %w", err)
	}
	if _, err := tmp.WriteString(h.render()); err != nil {
		return fmt.Errorf("write embedded session handoff: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync embedded session handoff: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close embedded session handoff: %w", err)
	}
	if err := os.Rename(tmpPath, h.path); err != nil {
		return fmt.Errorf("install embedded session handoff: %w", err)
	}
	return nil
}

func (h SessionHandoff) render() string {
	snapshot := h.snapshot
	var out strings.Builder
	out.WriteString("# Embedded session handoff\n\n")
	out.WriteString("> Generated mechanically by Little Control Room from cached local state. ")
	out.WriteString("It may be incomplete, and it does not prove that the final provider turn completed.\n\n")
	out.WriteString("## Source\n\n")
	writeSessionHandoffField(&out, "Captured", h.capturedAt.Format(time.RFC3339Nano))
	writeSessionHandoffField(&out, "Provider", snapshot.Provider.Label())
	writeSessionHandoffField(&out, "Project", snapshot.ProjectPath)
	writeSessionHandoffField(&out, "Current working directory", snapshot.CurrentCWD)
	writeSessionHandoffField(&out, "Source session", snapshot.ThreadID)
	writeSessionHandoffField(&out, "Active turn", snapshot.ActiveTurnID)
	writeSessionHandoffField(&out, "Phase", string(snapshot.Phase))
	writeSessionHandoffField(&out, "Model", snapshot.Model)
	writeSessionHandoffField(&out, "Reasoning effort", snapshot.ReasoningEffort)
	writeSessionHandoffField(&out, "Preset", string(snapshot.Preset))
	writeSessionHandoffField(&out, "Last activity", formatSessionHandoffTime(snapshot.LastActivityAt))
	fmt.Fprintf(
		&out,
		"- State flags: busy=%t, busy_external=%t, closed=%t, latest_turn_state_known=%t, latest_turn_completed=%t\n",
		snapshot.Busy,
		snapshot.BusyExternal,
		snapshot.Closed,
		snapshot.LatestTurnStateKnown,
		snapshot.LatestTurnCompleted,
	)
	if len(snapshot.BackgroundTasks) > 0 {
		fmt.Fprintf(&out, "- Provider background tasks at capture: %d\n", len(snapshot.BackgroundTasks))
	}

	writeSessionHandoffSection(&out, "Handoff note", h.note, maxSessionHandoffDetailText)
	if snapshot.Goal != nil {
		out.WriteString("\n## Source goal\n\n")
		writeSessionHandoffField(&out, "Status", string(snapshot.Goal.Status))
		writeSessionHandoffQuotedText(&out, snapshot.Goal.Objective, maxSessionHandoffDetailText)
	}
	writeSessionHandoffSection(&out, "Status at capture", snapshot.Status, maxSessionHandoffDetailText)
	writeSessionHandoffSection(&out, "Last provider error", snapshot.LastError, maxSessionHandoffDetailText)
	writeSessionHandoffSection(&out, "Last system notice", snapshot.LastSystemNotice, maxSessionHandoffDetailText)

	out.WriteString("\n## Recovery checklist\n\n")
	out.WriteString("- Inspect the current Git status and diff before editing.\n")
	out.WriteString("- Check relevant managed or background processes before starting replacements.\n")
	out.WriteString("- Treat prior assistant claims as context, not as proof that work completed.\n")
	out.WriteString("- Continue the latest unresolved user request without redoing verified completed work.\n")
	out.WriteString("- Run the project-appropriate verification before reporting completion.\n")

	entries := sessionHandoffSourceEntries(snapshot)
	latestUser := latestSessionHandoffUserText(entries)
	writeSessionHandoffSection(&out, "Latest cached user message", latestUser, maxSessionHandoffLatestUserText)

	out.WriteString("\n## Recent structured transcript\n\n")
	out.WriteString("Private reasoning entries are intentionally omitted. Older and oversized entries are truncated deterministically.\n")
	selected := selectSessionHandoffEntries(entries)
	if len(selected) == 0 {
		out.WriteString("\n_No structured transcript entries were available in the cached snapshot._\n")
		return out.String()
	}
	for _, selectedEntry := range selected {
		entry := selectedEntry.entry
		fmt.Fprintf(&out, "\n### %s\n\n", sessionHandoffKindLabel(entry.Kind))
		writeSessionHandoffField(&out, "Turn", entry.TurnID)
		writeSessionHandoffField(&out, "Item", entry.ItemID)
		writeSessionHandoffField(&out, "Tool", entry.ToolName)
		writeSessionHandoffField(&out, "Path", entry.ToolPath)
		writeSessionHandoffQuotedText(&out, selectedEntry.text, len(selectedEntry.text))
	}
	return out.String()
}

type selectedSessionHandoffEntry struct {
	entry TranscriptEntry
	text  string
}

func sessionHandoffSourceEntries(snapshot Snapshot) []TranscriptEntry {
	if len(snapshot.Entries) > 0 {
		return snapshot.Entries
	}
	return snapshot.ActivityPreview
}

func latestSessionHandoffUserText(entries []TranscriptEntry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Kind == TranscriptUser {
			return sessionHandoffEntryText(entries[i])
		}
	}
	return ""
}

func selectSessionHandoffEntries(entries []TranscriptEntry) []selectedSessionHandoffEntry {
	remaining := maxSessionHandoffTranscriptBytes
	reversed := make([]selectedSessionHandoffEntry, 0, min(len(entries), maxSessionHandoffEntries))
	for i := len(entries) - 1; i >= 0 && len(reversed) < maxSessionHandoffEntries && remaining > 0; i-- {
		entry := entries[i]
		if entry.Kind == TranscriptReasoning {
			continue
		}
		text := sessionHandoffEntryText(entry)
		if strings.TrimSpace(text) == "" {
			continue
		}
		limit := min(sessionHandoffEntryLimit(entry.Kind), remaining)
		text = truncateSessionHandoffText(text, limit)
		if text == "" {
			continue
		}
		reversed = append(reversed, selectedSessionHandoffEntry{entry: entry, text: text})
		remaining -= len(text)
	}

	selected := make([]selectedSessionHandoffEntry, len(reversed))
	for i := range reversed {
		selected[len(reversed)-1-i] = reversed[i]
	}
	return selected
}

func sessionHandoffEntryLimit(kind TranscriptKind) int {
	switch kind {
	case TranscriptUser, TranscriptAgent, TranscriptPlan:
		return maxSessionHandoffConversationText
	case TranscriptCommand, TranscriptFileChange, TranscriptTool:
		return maxSessionHandoffActivityText
	default:
		return maxSessionHandoffDetailText
	}
}

func sessionHandoffEntryText(entry TranscriptEntry) string {
	text := strings.TrimSpace(entry.DisplayText)
	if text == "" {
		text = strings.TrimSpace(entry.Text)
	}
	return strings.ToValidUTF8(text, "�")
}

func sessionHandoffKindLabel(kind TranscriptKind) string {
	switch kind {
	case TranscriptUser:
		return "User"
	case TranscriptAgent:
		return "Assistant"
	case TranscriptSystem:
		return "System"
	case TranscriptStatus:
		return "Status"
	case TranscriptError:
		return "Error"
	case TranscriptPlan:
		return "Plan"
	case TranscriptCommand:
		return "Command"
	case TranscriptFileChange:
		return "File change"
	case TranscriptTool:
		return "Tool activity"
	default:
		return "Other activity"
	}
}

func writeSessionHandoffField(out *strings.Builder, label, value string) {
	value = sessionHandoffSingleLine(value)
	if value == "" {
		return
	}
	fmt.Fprintf(out, "- %s: %s\n", label, strconv.Quote(value))
}

func writeSessionHandoffSection(out *strings.Builder, title, text string, limit int) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	fmt.Fprintf(out, "\n## %s\n\n", title)
	writeSessionHandoffQuotedText(out, text, limit)
}

func writeSessionHandoffQuotedText(out *strings.Builder, text string, limit int) {
	text = truncateSessionHandoffText(text, limit)
	if text == "" {
		return
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	for _, line := range strings.Split(text, "\n") {
		out.WriteString(">")
		if line != "" {
			out.WriteString(" ")
			out.WriteString(line)
		}
		out.WriteString("\n")
	}
}

func truncateSessionHandoffText(text string, limit int) string {
	text = strings.ToValidUTF8(strings.TrimSpace(text), "�")
	if limit <= 0 || text == "" {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	const suffix = "\n… [truncated by Little Control Room]"
	if limit <= len(suffix) {
		return validSessionHandoffPrefix(text, limit)
	}
	prefix := validSessionHandoffPrefix(text, limit-len(suffix))
	prefix = strings.TrimRight(prefix, " \t\r\n")
	return prefix + suffix
}

func validSessionHandoffPrefix(text string, limit int) string {
	if limit >= len(text) {
		return text
	}
	if limit <= 0 {
		return ""
	}
	for limit > 0 && !utf8.ValidString(text[:limit]) {
		limit--
	}
	return text[:limit]
}

func sessionHandoffSingleLine(value string) string {
	value = strings.ToValidUTF8(strings.TrimSpace(value), "�")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return truncateSessionHandoffText(value, 4*1024)
}

func formatSessionHandoffTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
