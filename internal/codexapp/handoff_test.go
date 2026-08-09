package codexapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexcli"
)

func TestSessionHandoffWritesPrivateMechanicalBrief(t *testing.T) {
	dataDir := t.TempDir()
	capturedAt := time.Date(2026, time.August, 9, 3, 30, 45, 123456789, time.FixedZone("JST", 9*60*60))
	goalBudget := int64(50_000)
	handoff, err := NewSessionHandoff(dataDir, Snapshot{
		Provider:             ProviderCodex,
		ProjectPath:          "/tmp/demo-project",
		CurrentCWD:           "/tmp/demo-project",
		ThreadID:             "thread-source-123",
		ActiveTurnID:         "turn-failed-456",
		Preset:               codexcli.PresetYolo,
		Phase:                SessionPhaseStalled,
		Started:              true,
		Busy:                 true,
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  false,
		Status:               "Reconnecting... 5/5",
		LastError:            "response stream disconnected before completion",
		LastSystemNotice:     "The provider helper is still reachable.",
		Model:                "gpt-test",
		ReasoningEffort:      "high",
		LastActivityAt:       capturedAt.Add(-time.Minute),
		Goal: &ThreadGoal{
			Objective:   "Finish the dialog frame fix",
			Status:      ThreadGoalStatusActive,
			TokenBudget: &goalBudget,
		},
		Entries: []TranscriptEntry{
			{Kind: TranscriptUser, TurnID: "turn-1", Text: "Fix the shared dialog centering."},
			{Kind: TranscriptReasoning, TurnID: "turn-1", Text: "private-reasoning-must-not-be-copied"},
			{Kind: TranscriptTool, TurnID: "turn-1", ToolName: "read_file", ToolPath: "/tmp/demo-project/dialog.go", Text: "Inspected the shared layout."},
			{Kind: TranscriptAgent, TurnID: "turn-1", Text: "The frame and content use different origins."},
			{Kind: TranscriptUser, TurnID: "turn-2", Text: "Please try again without losing the investigation."},
		},
	}, "Preserve the centering diagnosis and continue in the same worktree.", capturedAt)
	if err != nil {
		t.Fatalf("NewSessionHandoff() error = %v", err)
	}

	expectedRoot := filepath.Join(dataDir, restartIntentDirName, sessionHandoffDirName)
	if !strings.HasPrefix(handoff.Path(), expectedRoot+string(os.PathSeparator)) {
		t.Fatalf("handoff path = %q, want it below %q", handoff.Path(), expectedRoot)
	}
	if err := handoff.Write(); err != nil {
		t.Fatalf("handoff.Write() error = %v", err)
	}

	info, err := os.Stat(handoff.Path())
	if err != nil {
		t.Fatalf("stat handoff: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("handoff mode = %#o, want 0600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(handoff.Path()))
	if err != nil {
		t.Fatalf("stat handoff directory: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("handoff directory mode = %#o, want 0700", got)
	}

	raw, err := os.ReadFile(handoff.Path())
	if err != nil {
		t.Fatalf("read handoff: %v", err)
	}
	brief := string(raw)
	for _, want := range []string{
		"Generated mechanically by Little Control Room",
		"Preserve the centering diagnosis",
		"response stream disconnected before completion",
		"Finish the dialog frame fix",
		"Fix the shared dialog centering.",
		"Inspected the shared layout.",
		"The frame and content use different origins.",
		"Please try again without losing the investigation.",
		"busy=true",
		"latest_turn_completed=false",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("handoff brief should contain %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "private-reasoning-must-not-be-copied") {
		t.Fatalf("handoff brief copied a private reasoning entry:\n%s", brief)
	}

	prompt := handoff.LaunchPrompt()
	if !strings.Contains(prompt, handoff.Path()) {
		t.Fatalf("launch prompt should name handoff path: %q", prompt)
	}
	if !strings.Contains(prompt, "not proof that the prior turn finished") {
		t.Fatalf("launch prompt should require verification: %q", prompt)
	}
}

func TestSessionHandoffBoundsTranscriptAndUsesUniquePaths(t *testing.T) {
	dataDir := t.TempDir()
	capturedAt := time.Date(2026, time.August, 9, 0, 0, 0, 0, time.UTC)
	entries := make([]TranscriptEntry, 0, 80)
	entries = append(entries, TranscriptEntry{Kind: TranscriptUser, Text: "oldest-entry-must-fall-out"})
	for i := 1; i < 79; i++ {
		entries = append(entries, TranscriptEntry{
			Kind: TranscriptTool,
			Text: strings.Repeat("large tool output ", 4_000),
		})
	}
	entries = append(entries, TranscriptEntry{
		Kind: TranscriptUser,
		Text: "latest-user-marker " + strings.Repeat("x", 64*1024),
	})
	snapshot := Snapshot{
		Provider:    ProviderOpenCode,
		ProjectPath: "/tmp/bounded-handoff",
		ThreadID:    "source-thread",
		Entries:     entries,
	}

	first, err := NewSessionHandoff(dataDir, snapshot, "", capturedAt)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSessionHandoff(dataDir, snapshot, "", capturedAt)
	if err != nil {
		t.Fatal(err)
	}
	if first.Path() == second.Path() {
		t.Fatalf("two handoffs planned at the same instant should have unique paths: %q", first.Path())
	}
	if err := first.Write(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(first.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 140*1024 {
		t.Fatalf("bounded handoff size = %d bytes, want at most %d", len(raw), 140*1024)
	}
	brief := string(raw)
	if !strings.Contains(brief, "latest-user-marker") {
		t.Fatalf("bounded handoff should retain the latest user message")
	}
	if strings.Contains(brief, "oldest-entry-must-fall-out") {
		t.Fatalf("bounded handoff should discard old entries once its tail limit is reached")
	}
	if !strings.Contains(brief, "truncated by Little Control Room") {
		t.Fatalf("bounded handoff should disclose deterministic truncation")
	}
}

func TestNewSessionHandoffRequiresHostPaths(t *testing.T) {
	if _, err := NewSessionHandoff("", Snapshot{ProjectPath: "/tmp/demo"}, "", time.Time{}); err == nil {
		t.Fatal("NewSessionHandoff() should reject an empty app data directory")
	}
	if _, err := NewSessionHandoff(t.TempDir(), Snapshot{}, "", time.Time{}); err == nil {
		t.Fatal("NewSessionHandoff() should reject an empty project path")
	}
}
