package codexapp

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStartWithOwnedOutputPipesSurvivesReaderThatLosesTheRace(t *testing.T) {
	cmd := exec.Command("sh", "-lc", "echo alpha; echo beta >&2; exit 3")
	stdout, stderr, err := startWithOwnedOutputPipes(cmd)
	if err != nil {
		t.Fatalf("startWithOwnedOutputPipes() error = %v", err)
	}
	defer closeFiles(stdout, stderr)

	// Read only after Wait returns, the worst case for a capture goroutine that
	// gets scheduled late. cmd.StdoutPipe would already have closed both pipes
	// here and the output would be gone.
	if err := cmd.Wait(); err == nil {
		t.Fatal("Wait() error = nil, want exit status 3")
	}

	for name, stream := range map[string]*os.File{"alpha": stdout, "beta": stderr} {
		content, err := io.ReadAll(stream)
		if err != nil {
			t.Fatalf("read %s stream: %v", name, err)
		}
		if !strings.Contains(string(content), name) {
			t.Fatalf("%s stream = %q, want it to contain %q", name, content, name)
		}
	}
}

func TestWaitForOutputDrainStopsWaitingForAStalledCapture(t *testing.T) {
	var captured sync.WaitGroup
	captured.Add(1)
	defer captured.Done()

	start := time.Now()
	waitForOutputDrain(&captured, 50*time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waitForOutputDrain blocked for %s, want it bounded by the timeout", elapsed)
	}
}
