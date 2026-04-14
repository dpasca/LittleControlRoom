package gitops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPushTimesOutHungGitProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake git timeout test uses a POSIX shell script")
	}

	oldTimeout := defaultPushTimeout
	defaultPushTimeout = 50 * time.Millisecond
	defer func() {
		defaultPushTimeout = oldTimeout
	}()

	binDir := t.TempDir()
	gitPath := filepath.Join(binDir, "git")
	script := "#!/bin/sh\nsleep 1\n"
	if err := os.WriteFile(gitPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := Push(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("Push() error = nil, want timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Push() error = %v, want deadline exceeded", err)
	}
	if !strings.Contains(err.Error(), "timed out after 50ms") {
		t.Fatalf("Push() error = %q, want timeout text", err)
	}
}
