package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/claudestyle"
	"lcroom/internal/codexapp"
	"lcroom/internal/config"
)

func TestClaudeOutputStyleSidebarLabelHiddenForOtherProviders(t *testing.T) {
	snapshot := codexapp.Snapshot{Provider: codexapp.ProviderCodex, OutputStyle: "Terse"}
	if label, _ := claudeOutputStyleSidebarLabel(snapshot); label != "" {
		t.Fatalf("label = %q, want no style row for a non-Claude provider", label)
	}
}

// The default style carries no information, so the row stays out of the
// sidebar rather than adding a line that always reads "default".
func TestClaudeOutputStyleSidebarLabelHiddenForDefault(t *testing.T) {
	snapshot := codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode}
	if label, _ := claudeOutputStyleSidebarLabel(snapshot); label != "" {
		t.Fatalf("label = %q, want no row for the default style", label)
	}
}

func TestClaudeOutputStyleSidebarLabelShowsCurrentStyle(t *testing.T) {
	snapshot := codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode, OutputStyle: "Terse"}
	label, pending := claudeOutputStyleSidebarLabel(snapshot)
	if label != "Terse" {
		t.Fatalf("label = %q, want %q", label, "Terse")
	}
	if pending {
		t.Fatal("pending = true, want false when no change is staged")
	}
}

// A staged style only applies on the next prompt, so the sidebar must show
// both values rather than claiming the new one is already in effect.
func TestClaudeOutputStyleSidebarLabelShowsStagedTransition(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider:           codexapp.ProviderClaudeCode,
		OutputStyle:        "Terse",
		PendingOutputStyle: "Explanatory",
	}
	label, pending := claudeOutputStyleSidebarLabel(snapshot)
	if label != "Terse → Explanatory" {
		t.Fatalf("label = %q, want the current and staged style", label)
	}
	if !pending {
		t.Fatal("pending = false, want the staged style highlighted")
	}
}

func TestClaudeOutputStyleSidebarLabelShowsTransitionFromDefault(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider:           codexapp.ProviderClaudeCode,
		PendingOutputStyle: "Terse",
	}
	label, _ := claudeOutputStyleSidebarLabel(snapshot)
	if label != "default → Terse" {
		t.Fatalf("label = %q, want the default named explicitly", label)
	}
}

func TestClaudeOutputStyleStatusLineNamesStylesDirWhenEmpty(t *testing.T) {
	snapshot := codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode}
	options := []claudestyle.Option{{Name: claudestyle.DefaultName, Source: claudestyle.SourceBuiltIn}}

	line := claudeOutputStyleStatusLine(snapshot, options)
	if !strings.Contains(line, claudestyle.StylesDirName) {
		t.Fatalf("status = %q, want the directory to create styles in", line)
	}
}

func TestClaudeOutputStyleStatusLineListsAvailableStyles(t *testing.T) {
	snapshot := codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode, OutputStyle: "Terse"}
	options := []claudestyle.Option{
		{Name: claudestyle.DefaultName, Source: claudestyle.SourceBuiltIn},
		{Name: "Terse", Source: claudestyle.SourceUser},
	}

	line := claudeOutputStyleStatusLine(snapshot, options)
	if !strings.Contains(line, "Output style: Terse") {
		t.Fatalf("status = %q, want the current style", line)
	}
	if !strings.Contains(line, "available: default, Terse") {
		t.Fatalf("status = %q, want the discovered styles listed", line)
	}
}

func TestClaudeOutputStyleStatusLineReportsStagedStyle(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider:           codexapp.ProviderClaudeCode,
		OutputStyle:        "Terse",
		PendingOutputStyle: "Explanatory",
	}
	options := []claudestyle.Option{
		{Name: claudestyle.DefaultName, Source: claudestyle.SourceBuiltIn},
		{Name: "Terse", Source: claudestyle.SourceUser},
	}

	line := claudeOutputStyleStatusLine(snapshot, options)
	if !strings.Contains(line, "next: Explanatory") {
		t.Fatalf("status = %q, want the staged style reported", line)
	}
}

// The sidebar hint is how the command is discovered, so /style must appear for
// Claude Code and stay out of the other providers' hints.
func TestSidebarCommandsHintIncludesStyleOnlyForClaude(t *testing.T) {
	claude := embeddedSidebarModelCommands(codexapp.Snapshot{
		Provider: codexapp.ProviderClaudeCode,
		Model:    "claude-opus-5",
	})
	if claude != "/model /style" {
		t.Fatalf("Claude commands hint = %q, want %q", claude, "/model /style")
	}
	codex := embeddedSidebarModelCommands(codexapp.Snapshot{
		Provider: codexapp.ProviderCodex,
		Model:    "gpt-5",
	})
	if codex != "/model" {
		t.Fatalf("Codex commands hint = %q, want %q", codex, "/model")
	}
}

func TestSidebarRendersStyleRow(t *testing.T) {
	rows := embeddedSidebarModelRows(codexapp.Snapshot{
		Provider:    codexapp.ProviderClaudeCode,
		Model:       "claude-opus-5",
		OutputStyle: "Terse",
	}, 60)

	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "Style") || !strings.Contains(joined, "Terse") {
		t.Fatalf("sidebar rows = %q, want a Style row", joined)
	}
}

// The Settings screen rebuilds EditableSettings from its own fields, so a
// preference it does not edit must be carried over. Without this, saving any
// unrelated setting would silently clear the style chosen with /style.
func TestSettingsSavePreservesOutputStyle(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.EmbeddedClaudeOutputStyle = "Terse"
	m := Model{
		settingsBaseline:   &settings,
		settingsFields:     newSettingsFields(settings),
		settingsConfigPath: filepath.Join(t.TempDir(), "config.toml"),
	}
	m.settingsFields[settingsFieldZaiModel].input.SetValue("glm-5.3")

	next, cmd := m.saveSettingsFromFields()
	if cmd == nil {
		t.Fatalf("save not scheduled: %s", next.(Model).status)
	}
	msg := cmd().(settingsSavedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if msg.settings.EmbeddedClaudeOutputStyle != "Terse" {
		t.Fatalf("EmbeddedClaudeOutputStyle = %q, want it preserved across an unrelated save", msg.settings.EmbeddedClaudeOutputStyle)
	}
}

func TestSaveClaudeOutputStyleCmdWritesConfig(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	path := filepath.Join(t.TempDir(), "config.toml")
	m := Model{settingsBaseline: &settings, settingsConfigPath: path}

	cmd := m.saveClaudeOutputStyleCmd("Terse")
	if cmd == nil {
		t.Fatal("saveClaudeOutputStyleCmd() = nil, want a save command")
	}
	msg, ok := cmd().(claudeOutputStyleSavedMsg)
	if !ok {
		t.Fatalf("message type = %T, want claudeOutputStyleSavedMsg", cmd())
	}
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if msg.style != "Terse" {
		t.Fatalf("saved style = %q, want %q", msg.style, "Terse")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `embedded_claude_output_style = "Terse"`) {
		t.Fatalf("config = %q, want the output style persisted", data)
	}
}

// Returning to the default clears the saved key rather than writing the word
// "default", so Claude Code resolves the style from the user's own settings.
func TestSaveClaudeOutputStyleCmdClearsOnDefault(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.EmbeddedClaudeOutputStyle = "Terse"
	m := Model{settingsBaseline: &settings, settingsConfigPath: filepath.Join(t.TempDir(), "config.toml")}

	cmd := m.saveClaudeOutputStyleCmd("default")
	if cmd == nil {
		t.Fatal("saveClaudeOutputStyleCmd(default) = nil, want a save command")
	}
	msg := cmd().(claudeOutputStyleSavedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if msg.style != "" {
		t.Fatalf("saved style = %q, want it cleared", msg.style)
	}
}

func TestSaveClaudeOutputStyleCmdSkipsUnchangedValue(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.EmbeddedClaudeOutputStyle = "Terse"
	m := Model{settingsBaseline: &settings, settingsConfigPath: filepath.Join(t.TempDir(), "config.toml")}

	if cmd := m.saveClaudeOutputStyleCmd("Terse"); cmd != nil {
		t.Fatal("saveClaudeOutputStyleCmd() scheduled a write for an unchanged value")
	}
}
