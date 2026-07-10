package codexapp

import (
	"strings"
	"testing"
)

func TestCodexReplayToolCallRendersCodeModeCommandDetails(t *testing.T) {
	input := `const r = await tools.exec_command({
  cmd: "python3 - <<'PY'\nprint(\"tools.view_image({fake: true})\")\nPY",
  workdir: "/tmp/project with spaces",
  yield_time_ms: 10000,
  max_output_tokens: 5000
});
text(r.output);
`

	call := newCodexReplayToolCall("exec", input, "")
	entry := call.transcriptEntry("ctc-command", "completed")
	if entry.Kind != TranscriptCommand {
		t.Fatalf("entry kind = %q, want command", entry.Kind)
	}
	for _, want := range []string{
		"$ python3 - <<'PY'",
		`print("tools.view_image({fake: true})")`,
		"# cwd: /tmp/project with spaces",
		"[command completed]",
	} {
		if !strings.Contains(entry.Text, want) {
			t.Fatalf("command entry missing %q: %q", want, entry.Text)
		}
	}
}

func TestCodexReplayToolCallRendersCodeModeImageDetails(t *testing.T) {
	input := `// tools.exec_command({cmd: "not a real call"})
const r = await tools.view_image({
  path: "/tmp/document scan.png",
  detail: "original"
});
image(r.image_url);
`

	call := newCodexReplayToolCall("exec", input, "")
	entry := call.transcriptEntry("ctc-image", "completed")
	if entry.Kind != TranscriptTool || entry.Text != "Tool view completed: /tmp/document scan.png" {
		t.Fatalf("image entry = %#v", entry)
	}
}

func TestCodexReplayToolCallRendersFunctionArguments(t *testing.T) {
	call := newCodexReplayToolCall("wait", "", `{"cell_id":"17","yield_time_ms":30000}`)
	entry := call.transcriptEntry("fc-wait", "completed")
	if entry.Kind != TranscriptTool || entry.Text != "Tool wait completed: cell 17" {
		t.Fatalf("wait entry = %#v", entry)
	}
}

func TestCodexReplayToolCallFallsBackToCodeModeSource(t *testing.T) {
	input := `const matches = ALL_TOOLS.filter(x =>
  x.name.includes("playwright")
);`
	call := newCodexReplayToolCall("exec", input, "")
	entry := call.transcriptEntry("ctc-code", "completed")
	if entry.Text != "Tool exec completed: const matches = ALL_TOOLS.filter(x =>" {
		t.Fatalf("fallback entry = %#v", entry)
	}
}

func TestParseCodexCodeModeToolCallsFindsMultipleStructuredCalls(t *testing.T) {
	input := `const [one, two] = await Promise.all([
  tools.exec_command({cmd: "make test", workdir: "/tmp/demo"}),
  tools.view_image({path: "/tmp/result.png", detail: "high"})
]);`
	calls := parseCodexCodeModeToolCalls(input)
	if len(calls) != 2 {
		t.Fatalf("calls = %#v, want two", calls)
	}
	if calls[0].name != "exec_command" || calls[0].args["cmd"] != "make test" {
		t.Fatalf("first call = %#v", calls[0])
	}
	if calls[1].name != "view_image" || calls[1].args["path"] != "/tmp/result.png" {
		t.Fatalf("second call = %#v", calls[1])
	}
}
