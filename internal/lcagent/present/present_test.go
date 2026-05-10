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
