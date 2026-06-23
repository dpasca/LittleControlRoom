package present

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCommandTruncatesAndPersistsLargeOutput(t *testing.T) {
	dir := t.TempDir()
	p := Command(CommandOutput{
		Stdout:      bytes.Repeat([]byte("x"), maxInlineBytes+10),
		ExitCode:    0,
		Duration:    time.Millisecond,
		ArtifactDir: dir,
	})
	if !p.Truncated {
		t.Fatal("Truncated = false, want true")
	}
	if p.ArtifactPath == "" {
		t.Fatal("ArtifactPath empty")
	}
	if _, err := os.Stat(p.ArtifactPath); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Text, "output truncated") {
		t.Fatalf("Text missing truncation note: %q", p.Text)
	}
}

func TestCommandSuppressesBinaryOutput(t *testing.T) {
	p := Command(CommandOutput{
		Stdout:   []byte{0, 1, 2},
		ExitCode: 1,
		Duration: time.Millisecond,
	})
	if !p.Binary {
		t.Fatal("Binary = false, want true")
	}
	if !strings.Contains(p.Text, "binary command output suppressed") {
		t.Fatalf("Text = %q", p.Text)
	}
}

func TestCommandOutputAllowsUTF8RuneAtSampleBoundary(t *testing.T) {
	p := Command(CommandOutput{
		Stdout:   []byte(strings.Repeat("a", 4095) + "┌ ok\n"),
		ExitCode: 0,
		Duration: time.Millisecond,
	})
	if p.Binary {
		t.Fatalf("Binary = true, want valid UTF-8 output to remain visible: %q", p.Text)
	}
	if !strings.Contains(p.Text, "┌ ok") {
		t.Fatalf("Text missing UTF-8 output: %q", p.Text)
	}
}

func TestCommandTimeoutSaysProcessGroupWasTerminated(t *testing.T) {
	p := Command(CommandOutput{
		Stdout:   []byte("ready on localhost\n"),
		ExitCode: -1,
		Duration: time.Second,
		TimedOut: true,
	})
	for _, want := range []string{"ready on localhost", "timeout", "process group", "assume long-running servers or watchers from this command are stopped"} {
		if !strings.Contains(p.Text, want) {
			t.Fatalf("Text missing %q: %q", want, p.Text)
		}
	}
}
