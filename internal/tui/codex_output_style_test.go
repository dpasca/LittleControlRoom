package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/claudestyle"
	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/slashcmd"

	"github.com/charmbracelet/lipgloss"
)

func TestClaudeOutputStyleSidebarLabelHiddenForOtherProviders(t *testing.T) {
	snapshot := codexapp.Snapshot{Provider: codexapp.ProviderCodex, OutputStyle: "Terse"}
	if label, _ := claudeOutputStyleSidebarLabel(snapshot); label != "" {
		t.Fatalf("label = %q, want no style row for a non-Claude provider", label)
	}
}

// The row is how output styles are discovered, so the default is named rather
// than hidden. A blank row would read the same as the feature not existing.
func TestClaudeOutputStyleSidebarLabelNamesDefault(t *testing.T) {
	snapshot := codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode}
	label, pending := claudeOutputStyleSidebarLabel(snapshot)
	if label != claudestyle.DefaultName {
		t.Fatalf("label = %q, want %q so the row is always visible", label, claudestyle.DefaultName)
	}
	if pending {
		t.Fatal("pending = true, want false with nothing staged")
	}
}

func TestSidebarRendersStyleRowForDefault(t *testing.T) {
	rows := embeddedSidebarModelRows(codexapp.Snapshot{
		Provider: codexapp.ProviderClaudeCode,
		Model:    "claude-opus-5",
	}, 60)

	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "Style") || !strings.Contains(joined, claudestyle.DefaultName) {
		t.Fatalf("sidebar rows = %q, want a Style row naming the default", joined)
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

// Completion must come from the snapshot, never from a disk read on the
// render path, so a pane with no live Claude session offers no style names.
func TestVisibleClaudeOutputStyleNamesEmptyWithoutClaudeSession(t *testing.T) {
	m := Model{}
	if names := m.visibleClaudeOutputStyleNames(); len(names) != 0 {
		t.Fatalf("names = %v, want none without a visible Claude session", names)
	}
}

// Typing /style and pressing Tab must offer the style names directly. A user
// who does not already know a style name has no other way to learn one.
func TestCodexSlashSuggestionsCompleteStyleNamesWithoutTrailingSpace(t *testing.T) {
	styles := []slashcmd.Choice{
		slashcmd.NewChoice("default", "Claude Code's standard responses"),
		slashcmd.NewChoice("Terse", "Answer first, minimal prose"),
	}

	got := codexSlashSuggestionsForInputWithStyles("/style", styles)
	inserts := make([]string, 0, len(got))
	for _, suggestion := range got {
		inserts = append(inserts, suggestion.Insert)
	}
	if len(inserts) < 2 || inserts[0] != "/style default" || inserts[1] != "/style Terse" {
		t.Fatalf("suggestions = %v, want the style names offered for a bare /style", inserts)
	}
}

// Tab cycling through concrete names must not strand the status form.
func TestCodexSlashSuggestionsKeepBareStyleReachable(t *testing.T) {
	styles := []slashcmd.Choice{slashcmd.NewChoice("Terse", "Answer first")}

	got := codexSlashSuggestionsForInputWithStyles("/style", styles)
	found := false
	for _, suggestion := range got {
		if suggestion.Insert == "/style" {
			found = true
		}
	}
	if !found {
		t.Fatalf("suggestions = %+v, want the bare /style status form still reachable", got)
	}
}

func TestCodexSlashSuggestionsCompleteStyleNames(t *testing.T) {
	styles := []slashcmd.Choice{
		slashcmd.NewChoice("default", "Claude Code's standard responses"),
		slashcmd.NewChoice("Terse", "Answer first, minimal prose"),
	}

	got := codexSlashSuggestionsForInputWithStyles("/style te", styles)
	if len(got) == 0 {
		t.Fatalf("suggestions = %+v, want the Terse completion", got)
	}
	if got[0].Insert != "/style Terse" {
		t.Fatalf("insert = %q, want the exact-cased name", got[0].Insert)
	}
	if got[0].Summary != "Answer first, minimal prose" {
		t.Fatalf("summary = %q, want the style description as a hint", got[0].Summary)
	}
}

// Without discovered styles the pane keeps the ordinary command list, so a
// Codex or OpenCode pane is unaffected.
func TestCodexSlashSuggestionsUnchangedWithoutStyles(t *testing.T) {
	withStyles := codexSlashSuggestionsForInputWithStyles("/style ", nil)
	plain := codexSlashSuggestionsForInput("/style ")
	if len(withStyles) != len(plain) {
		t.Fatalf("suggestions changed without discovered styles: %+v vs %+v", withStyles, plain)
	}
}

func styleSnapshot(current, pending string) codexapp.Snapshot {
	return codexapp.Snapshot{
		Provider:           codexapp.ProviderClaudeCode,
		OutputStyle:        current,
		PendingOutputStyle: pending,
		AvailableOutputStyles: []claudestyle.Option{
			{Name: claudestyle.DefaultName, Verbosity: claudestyle.VerbosityVerbose},
			{Name: "Terse", Verbosity: claudestyle.VerbosityConcise},
			{Name: "Explanatory", Verbosity: claudestyle.VerbosityVerbose},
		},
	}
}

// The colour is the signal read at a glance: green only for a style known to
// constrain output, red for anything expected to be lengthy. A named style is
// not automatically safe.
func TestClaudeOutputStyleSidebarValueStyleColorsByVerbosity(t *testing.T) {
	green := claudeOutputStyleTerseStyle.GetForeground()
	red := claudeOutputStyleVerboseStyle.GetForeground()
	if green == red {
		t.Fatal("concise and verbose colors must be visually distinct")
	}

	for _, tc := range []struct {
		style string
		want  lipgloss.TerminalColor
		why   string
	}{
		{style: "", want: red, why: "the unconstrained default"},
		{style: "Terse", want: green, why: "a style known to be concise"},
		{style: "Explanatory", want: red, why: "a named but verbose style"},
		{style: "Unheard", want: red, why: "an unclassifiable style must not look safe"},
	} {
		got := claudeOutputStyleSidebarValueStyle(styleSnapshot(tc.style, "")).GetForeground()
		if got != tc.want {
			t.Fatalf("style %q colored %v, want %v (%s)", tc.style, got, tc.want, tc.why)
		}
	}
}

// A staged change should read as the state being moved to, so switching away
// from default turns green immediately rather than after the next prompt.
func TestClaudeOutputStyleSidebarValueStyleFollowsStagedStyle(t *testing.T) {
	if got := claudeOutputStyleSidebarValueStyle(styleSnapshot("", "Terse")).GetForeground(); got != claudeOutputStyleTerseStyle.GetForeground() {
		t.Fatalf("staging a concise style should color green, got %v", got)
	}
	if got := claudeOutputStyleSidebarValueStyle(styleSnapshot("Terse", claudestyle.DefaultName)).GetForeground(); got != claudeOutputStyleVerboseStyle.GetForeground() {
		t.Fatalf("staging the default should color red, got %v", got)
	}
	// Switching from a concise style to a verbose one is the case most worth
	// noticing before the prompt is sent.
	if got := claudeOutputStyleSidebarValueStyle(styleSnapshot("Terse", "Explanatory")).GetForeground(); got != claudeOutputStyleVerboseStyle.GetForeground() {
		t.Fatalf("staging a verbose style should color red, got %v", got)
	}
}

// The output style is a global preference, so it must be applied by the shared
// launch normalizer. Wiring it into a single call site left sessions opened
// from the picker, /new, a TODO dialog, or restart recovery starting unstyled.
func TestEveryClaudeLaunchInheritsSavedOutputStyle(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.EmbeddedClaudeOutputStyle = "Terse"
	m := Model{settingsBaseline: &settings}

	req := m.enrichEmbeddedLaunchRequestBase(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderClaudeCode,
		ProjectPath: t.TempDir(),
	})
	if req.ClaudeOutputStyle != "Terse" {
		t.Fatalf("ClaudeOutputStyle = %q, want the saved global preference", req.ClaudeOutputStyle)
	}
}

// An explicit per-launch choice still wins over the saved preference.
func TestExplicitLaunchOutputStyleIsNotOverwritten(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.EmbeddedClaudeOutputStyle = "Terse"
	m := Model{settingsBaseline: &settings}

	req := m.enrichEmbeddedLaunchRequestBase(codexapp.LaunchRequest{
		Provider:          codexapp.ProviderClaudeCode,
		ProjectPath:       t.TempDir(),
		ClaudeOutputStyle: "Explanatory",
	})
	if req.ClaudeOutputStyle != "Explanatory" {
		t.Fatalf("ClaudeOutputStyle = %q, want the explicit launch choice preserved", req.ClaudeOutputStyle)
	}
}

func TestNonClaudeLaunchGetsNoOutputStyle(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.EmbeddedClaudeOutputStyle = "Terse"
	m := Model{settingsBaseline: &settings}

	req := m.enrichEmbeddedLaunchRequestBase(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderCodex,
		ProjectPath: t.TempDir(),
	})
	if req.ClaudeOutputStyle != "" {
		t.Fatalf("ClaudeOutputStyle = %q, want none for a non-Claude provider", req.ClaudeOutputStyle)
	}
}
