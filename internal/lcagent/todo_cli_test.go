package lcagent

import (
	"bytes"
	"strings"
	"testing"

	"lcroom/internal/todocapture"
)

func TestRunExecRejectsTodoCaptureOutsideEmbeddedHostBroker(t *testing.T) {
	isolateSkillHomes(t)
	var stdout bytes.Buffer
	err := runExec([]string{
		"--cwd", t.TempDir(),
		"--data-dir", t.TempDir(),
		"--lcr-todo-capture-mode", string(todocapture.ModeExplicit),
		"remember this",
	}, &stdout)
	if err == nil || !strings.Contains(err.Error(), "requires --approval-mode ask from a Little Control Room host") {
		t.Fatalf("runExec() error = %v", err)
	}
}
