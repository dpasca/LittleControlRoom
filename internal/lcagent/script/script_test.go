package script

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/lcagent/policy"
	"lcroom/internal/lcagent/session"
	skillcatalog "lcroom/internal/lcagent/skills"
	"lcroom/internal/lcagent/tools"
)

func TestRunnerExecutesScriptedMiniSession(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(root, ".agents", "skills", "demo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("---\nname: demo\ndescription: Demo skill\n---\n# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := skillcatalog.Discover(context.Background(), skillcatalog.Options{WorkspaceRoot: w.Root})
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Prompt:    "patch readme",
		Command:   tools.CommandRunner{Workspace: w, ArtifactDir: t.TempDir()},
		Patch:     tools.PatchApplier{Workspace: w},
		Files:     tools.FileTools{Workspace: w},
		Skills:    catalog,
	}
	actions := []Action{
		{Type: "tool_call", Tool: "list_files", Args: raw(`{"path":".","glob":"*.md","max_entries":10}`)},
		{Type: "tool_call", Tool: "read_file", Args: raw(`{"path":"README.md","limit":20}`)},
		{Type: "tool_call", Tool: "search", Args: raw(`{"query":"old","path":".","file_glob":"*.md","max_matches":10}`)},
		{Type: "tool_call", Tool: "file_outline", Args: raw(`{"path":"README.md"}`)},
		{Type: "tool_call", Tool: "module_outline", Args: raw(`{"path":".","file_glob":"*.md","max_files":10}`)},
		{Type: "tool_call", Tool: "load_skill", Args: raw(`{"name":"demo"}`)},
		{Type: "tool_call", Tool: "run_command", Args: raw(`{"argv":["cat","README.md"],"timeout_ms":1000}`)},
		{Type: "tool_call", Tool: "update_plan", Args: raw(`{"items":[{"step":"Inspect","status":"completed"},{"step":"Patch","status":"in_progress"}]}`)},
		{Type: "tool_call", Tool: "apply_patch", Args: raw(`{"patch":"*** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** End Patch\n"}`)},
		{Type: "final_response", Summary: "done", FilesChanged: []string{"README.md"}, Verification: []string{"script"}},
	}
	if err := runner.Run(context.Background(), actions); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new\n" {
		t.Fatalf("README = %q", data)
	}
	text := stream.String()
	for _, eventType := range []string{"user_message", "tool_call", "tool_result", "skill_loaded", "plan_update", "files_touched", "patch_diff_summary", "verification_summary", "turn_complete"} {
		if !strings.Contains(text, `"type":"`+eventType+`"`) {
			t.Fatalf("stream missing %s:\n%s", eventType, text)
		}
	}
	if !strings.Contains(text, `"verification_status":"reported"`) || !strings.Contains(text, `"summary":"patch diff summary:`) {
		t.Fatalf("stream missing verification status or patch summary:\n%s", text)
	}
}

func TestRunnerEmitsPermissionDeniedEvent(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Prompt:    "try denied patch",
		Patch:     tools.PatchApplier{Workspace: w},
	}
	actions := []Action{
		{Type: "tool_call", Tool: "apply_patch", Args: raw(`{"patch":"*** Begin Patch\n*** Add File: denied.txt\n+nope\n*** End Patch\n"}`)},
	}
	if err := runner.Run(context.Background(), actions); err == nil {
		t.Fatal("Run succeeded, want denied tool failure")
	}
	text := stream.String()
	for _, want := range []string{`"type":"permission_denied"`, `"tool":"apply_patch"`, `"denied":true`, `"type":"turn_aborted"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
}

func TestRunnerFinalMarksMissingVerificationAfterChanges(t *testing.T) {
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	runner := Runner{Session: writer, SessionID: sessionID}
	if err := runner.Final(Action{
		Type:         "final_response",
		Summary:      "changed without checks",
		FilesChanged: []string{"README.md"},
	}); err != nil {
		t.Fatal(err)
	}
	text := stream.String()
	for _, want := range []string{`"type":"verification_summary"`, `"status":"missing_after_changes"`, `"verification_status":"missing_after_changes"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
}

func raw(value string) json.RawMessage {
	return json.RawMessage(value)
}
