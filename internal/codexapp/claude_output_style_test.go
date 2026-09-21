package codexapp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/claudestyle"
)

func newOutputStyleSession(t *testing.T, claudeHome, projectPath string) *claudeCodeSession {
	t.Helper()
	return &claudeCodeSession{
		projectPath:      projectPath,
		claudeHome:       claudeHome,
		safetyExecutable: "/tmp/lcroom",
		safetySettings:   `{"hooks":{}}`,
		availableStyles:  claudestyle.Discover(claudeHome, projectPath),
		closedCh:         make(chan struct{}),
	}
}

func writeUserStyle(t *testing.T, claudeHome, file, name string) {
	t.Helper()
	dir := filepath.Join(claudeHome, claudestyle.StylesDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir styles: %v", err)
	}
	body := "---\nname: " + name + "\ndescription: test style\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		t.Fatalf("write style: %v", err)
	}
}

func TestStageOutputStyleRejectsUnknownName(t *testing.T) {
	home := t.TempDir()
	writeUserStyle(t, home, "terse.md", "Terse")
	s := newOutputStyleSession(t, home, t.TempDir())

	err := s.StageOutputStyle("Nonexistent")
	if err == nil {
		t.Fatal("StageOutputStyle() error = nil, want rejection of an unknown style")
	}
	if !strings.Contains(err.Error(), "Terse") {
		t.Fatalf("error %q should list the available styles", err)
	}
	if s.pendingOutputStyle != "" {
		t.Fatalf("pendingOutputStyle = %q, want no staged style", s.pendingOutputStyle)
	}
}

// Claude Code matches style names case-sensitively and accepts a wrong-case
// name without applying anything. LCR resolves the casing itself so a loosely
// typed name works instead of silently doing nothing.
func TestStageOutputStyleAcceptsAnyCasing(t *testing.T) {
	home := t.TempDir()
	writeUserStyle(t, home, "terse.md", "Terse")
	s := newOutputStyleSession(t, home, t.TempDir())

	if err := s.StageOutputStyle("terse"); err != nil {
		t.Fatalf("StageOutputStyle(%q) error = %v, want the casing resolved", "terse", err)
	}
	if s.pendingOutputStyle != "Terse" {
		t.Fatalf("pendingOutputStyle = %q, want the exact stored name %q", s.pendingOutputStyle, "Terse")
	}
	if !strings.Contains(s.lastSystemNotice, "Terse") {
		t.Fatalf("notice = %q, want it to name the style actually selected", s.lastSystemNotice)
	}
}

func TestStageOutputStyleRejectsCaseOnlyAmbiguity(t *testing.T) {
	home := t.TempDir()
	writeUserStyle(t, home, "a.md", "Terse")
	writeUserStyle(t, home, "b.md", "terse")
	s := newOutputStyleSession(t, home, t.TempDir())

	err := s.StageOutputStyle("TERSE")
	if err == nil {
		t.Fatal("StageOutputStyle() error = nil, want an ambiguity rejection")
	}
	if !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("error %q should report the ambiguity", err)
	}
	if s.pendingOutputStyle != "" {
		t.Fatalf("pendingOutputStyle = %q, want nothing staged", s.pendingOutputStyle)
	}
}

func TestSnapshotCarriesAvailableStyleNamesForCompletion(t *testing.T) {
	home := t.TempDir()
	writeUserStyle(t, home, "terse.md", "Terse")
	s := newOutputStyleSession(t, home, t.TempDir())

	styles := s.stateSnapshotLocked().AvailableOutputStyles
	names := claudestyle.Names(styles)
	if len(names) != 2 || names[0] != "default" || names[1] != "Terse" {
		t.Fatalf("AvailableOutputStyles = %v, want the discovered names for completion", names)
	}
	if styles[1].Description != "test style" {
		t.Fatalf("description = %q, want it carried for completion hints", styles[1].Description)
	}
}

func TestStageOutputStyleAcceptsDiscoveredStyle(t *testing.T) {
	home := t.TempDir()
	writeUserStyle(t, home, "terse.md", "Terse")
	s := newOutputStyleSession(t, home, t.TempDir())

	if err := s.StageOutputStyle("Terse"); err != nil {
		t.Fatalf("StageOutputStyle() error = %v", err)
	}
	if s.pendingOutputStyle != "Terse" {
		t.Fatalf("pendingOutputStyle = %q, want %q", s.pendingOutputStyle, "Terse")
	}
	if s.outputStyle != "" {
		t.Fatalf("outputStyle = %q, want the change to wait for the next turn", s.outputStyle)
	}
}

// A style file created while the session is open must become selectable
// without reconnecting, because Discover runs per call rather than at launch.
func TestStageOutputStyleSeesStyleAddedAfterLaunch(t *testing.T) {
	home := t.TempDir()
	s := newOutputStyleSession(t, home, t.TempDir())
	if err := s.StageOutputStyle("Later"); err == nil {
		t.Fatal("expected the style to be unknown before its file exists")
	}
	writeUserStyle(t, home, "later.md", "Later")
	if err := s.StageOutputStyle("Later"); err != nil {
		t.Fatalf("StageOutputStyle() after creating the file error = %v", err)
	}
}

func TestApplyPendingOutputStyleRewritesTurnSettings(t *testing.T) {
	home := t.TempDir()
	writeUserStyle(t, home, "terse.md", "Terse")
	s := newOutputStyleSession(t, home, t.TempDir())
	if err := s.StageOutputStyle("Terse"); err != nil {
		t.Fatalf("StageOutputStyle() error = %v", err)
	}

	s.applyPendingOutputStyleLocked()

	if s.outputStyle != "Terse" || s.pendingOutputStyle != "" {
		t.Fatalf("after apply outputStyle=%q pending=%q, want Terse and empty", s.outputStyle, s.pendingOutputStyle)
	}
	var document struct {
		OutputStyle string                       `json:"outputStyle"`
		Hooks       map[string][]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(s.safetySettings), &document); err != nil {
		t.Fatalf("unmarshal turn settings: %v", err)
	}
	if document.OutputStyle != "Terse" {
		t.Fatalf("turn settings outputStyle = %q, want %q", document.OutputStyle, "Terse")
	}
	if len(document.Hooks["PreToolUse"]) != 1 {
		t.Fatal("selecting a style must not drop the destructive-command hook")
	}
}

func TestApplyPendingDefaultOutputStyleClearsOverride(t *testing.T) {
	home := t.TempDir()
	writeUserStyle(t, home, "terse.md", "Terse")
	s := newOutputStyleSession(t, home, t.TempDir())
	if err := s.StageOutputStyle("Terse"); err != nil {
		t.Fatalf("StageOutputStyle() error = %v", err)
	}
	s.applyPendingOutputStyleLocked()

	if err := s.StageOutputStyle("default"); err != nil {
		t.Fatalf("StageOutputStyle(default) error = %v", err)
	}
	s.applyPendingOutputStyleLocked()

	if s.outputStyle != "" {
		t.Fatalf("outputStyle = %q, want empty for the built-in default", s.outputStyle)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s.safetySettings), &document); err != nil {
		t.Fatalf("unmarshal turn settings: %v", err)
	}
	if _, ok := document["outputStyle"]; ok {
		t.Fatal("returning to the default must omit outputStyle so Claude Code resolves it normally")
	}
}

// Claude Code reports back whatever style name it was handed, so a value LCR
// never selected means the user's own settings chose it.
func TestObserveOutputStyleRecordsExternalSelection(t *testing.T) {
	home := t.TempDir()
	writeUserStyle(t, home, "terse.md", "Terse")
	s := newOutputStyleSession(t, home, t.TempDir())

	s.observeOutputStyleLocked("Terse")
	if s.outputStyle != "Terse" {
		t.Fatalf("outputStyle = %q, want the reported style", s.outputStyle)
	}
	if s.lastSystemNotice != "" {
		t.Fatalf("notice = %q, want none for a style that exists", s.lastSystemNotice)
	}
}

func TestObserveOutputStyleWarnsWhenNoStyleFileExists(t *testing.T) {
	s := newOutputStyleSession(t, t.TempDir(), t.TempDir())

	s.observeOutputStyleLocked("Ghost")
	if !strings.Contains(s.lastSystemNotice, "Ghost") {
		t.Fatalf("notice = %q, want a warning naming the missing style", s.lastSystemNotice)
	}
}

func TestObserveDefaultOutputStyleClearsValue(t *testing.T) {
	s := newOutputStyleSession(t, t.TempDir(), t.TempDir())
	s.outputStyle = "Terse"

	s.observeOutputStyleLocked("default")
	if s.outputStyle != "" {
		t.Fatalf("outputStyle = %q, want empty after Claude reported the default", s.outputStyle)
	}
}

func TestSnapshotReportsOutputStyle(t *testing.T) {
	home := t.TempDir()
	writeUserStyle(t, home, "terse.md", "Terse")
	s := newOutputStyleSession(t, home, t.TempDir())
	if err := s.StageOutputStyle("Terse"); err != nil {
		t.Fatalf("StageOutputStyle() error = %v", err)
	}

	snapshot := s.stateSnapshotLocked()
	if snapshot.PendingOutputStyle != "Terse" {
		t.Fatalf("snapshot PendingOutputStyle = %q, want %q", snapshot.PendingOutputStyle, "Terse")
	}
	if snapshot.OutputStyle != "" {
		t.Fatalf("snapshot OutputStyle = %q, want empty before the next turn", snapshot.OutputStyle)
	}
}

// Claude Code puts output_style at the top level of its init event, not inside
// a nested "message" object. Reading only the nested field meant the reported
// style was never observed at all.
func TestInitEventTopLevelOutputStyleIsObserved(t *testing.T) {
	home := t.TempDir()
	writeUserStyle(t, home, "terse.md", "Terse")
	s := newOutputStyleSession(t, home, t.TempDir())
	s.sessionID = "session-1"

	s.handleClaudeStdoutLine(`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-opus-5","output_style":"Terse"}`)

	if s.outputStyle != "Terse" {
		t.Fatalf("outputStyle = %q, want the style reported at the top level of init", s.outputStyle)
	}
}

func TestInitEventDefaultOutputStyleClearsOverride(t *testing.T) {
	s := newOutputStyleSession(t, t.TempDir(), t.TempDir())
	s.sessionID = "session-1"
	s.outputStyle = "Terse"

	s.handleClaudeStdoutLine(`{"type":"system","subtype":"init","session_id":"session-1","output_style":"default"}`)

	if s.outputStyle != "" {
		t.Fatalf("outputStyle = %q, want it cleared when Claude reports the default", s.outputStyle)
	}
}
