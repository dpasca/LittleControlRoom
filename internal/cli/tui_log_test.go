package cli

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureTUILogKeepsWarningsOffTerminalAndRestoresWriter(t *testing.T) {
	var terminal bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&terminal)
	t.Cleanup(func() { log.SetOutput(previous) })
	dir := t.TempDir()
	closeLog, err := captureTUILog(dir)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer closeLog()
		log.Print("WARN codexapp: recover Codex rollout state")
		if terminal.Len() != 0 {
			t.Fatalf("background warning reached terminal: %q", terminal.String())
		}
	}()
	if log.Writer() != &terminal {
		t.Fatal("original log writer was not restored")
	}
	files, err := filepath.Glob(filepath.Join(dir, "crash-dumps", "*-tui-*.log"))
	if err != nil || len(files) != 1 {
		t.Fatalf("diagnostic files = %v, err = %v", files, err)
	}
	contents, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "WARN codexapp: recover Codex rollout state") {
		t.Fatalf("warning missing from log: %q", contents)
	}
	log.Print("after TUI shutdown")
	if !strings.Contains(terminal.String(), "after TUI shutdown") {
		t.Fatalf("logging not restored after TUI: %q", terminal.String())
	}
}

func TestCaptureTUILogRemovesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	previous := log.Writer()
	closeLog, err := captureTUILog(dir)
	if err != nil {
		t.Fatal(err)
	}
	closeLog()
	if log.Writer() != previous {
		t.Fatal("original log writer was not restored")
	}
	files, err := os.ReadDir(filepath.Join(dir, "crash-dumps"))
	if err != nil || len(files) != 0 {
		t.Fatalf("empty diagnostic files = %v, err = %v", files, err)
	}
}

func TestCaptureTUILogSetupFailurePreservesWriter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "crash-dumps"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	previous := log.Writer()
	closeLog, err := captureTUILog(dir)
	if err == nil || closeLog != nil {
		t.Fatalf("captureTUILog() = (%v, %v), want setup failure", closeLog != nil, err)
	}
	if log.Writer() != previous {
		t.Fatal("failed setup changed the log writer")
	}
}
