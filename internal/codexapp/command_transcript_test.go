package codexapp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodexCommandTranscriptPreservesInputMetadata(t *testing.T) {
	command := "python3 - <<'PY'\ntext = '[preview](preview.wav)'\nprint('Wrote README')\nPY"
	rawCommand, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	item := map[string]json.RawMessage{
		"id":               json.RawMessage(`"command"`),
		"type":             json.RawMessage(`"commandExecution"`),
		"command":          rawCommand,
		"aggregatedOutput": json.RawMessage(`"Wrote README"`),
		"status":           json.RawMessage(`"completed"`),
	}
	turn := resumedTurn{ID: "turn", Status: "completed", Items: []map[string]json.RawMessage{item}}
	notification, err := json.Marshal(map[string]any{"item": item})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"started", "completed", "resumed", "older", "reconnected", "replayed"} {
		t.Run(mode, func(t *testing.T) {
			s := &appServerSession{entryIndex: make(map[string]int), notify: func() {}}
			switch mode {
			case "started":
				s.handleItemStarted(notification)
			case "completed":
				s.handleItemCompleted(notification)
			case "resumed", "reconnected":
				s.hydrateResumedThread(resumedThread{ID: "thread", Turns: []resumedTurn{turn}})
			case "older":
				s.prependHistoryTurnsLocked([]resumedTurn{turn})
			case "replayed":
				call := codexReplayToolCall{name: "exec_command", args: map[string]string{"cmd": command}}
				s.entries = reconnectTranscriptEntries([]TranscriptEntry{call.transcriptEntry("command", "completed")})
			}
			entries := s.Snapshot().Entries
			if mode == "reconnected" {
				entries = mergeReconnectTranscriptSnapshots([]TranscriptEntry{{ItemID: "command", Kind: TranscriptCommand, Text: "$ python3"}}, entries)
			}
			if len(entries) != 1 || entries[0].CommandText != command {
				t.Fatalf("command metadata lost: %#v", entries)
			}
			if !strings.HasPrefix(entries[0].Text, "$ "+entries[0].CommandText) {
				t.Fatalf("command input does not match rendered prefix: %#v", entries[0])
			}
		})
	}
}

func TestCodexCommandMetadataStaysBoundedInSnapshots(t *testing.T) {
	command := "python3 - <<'PY'\n" + strings.Repeat("# script body\n", maxExportedTranscriptEntryBytes) + "PY"
	entry, ok := exportTranscriptEntry(transcriptEntry{Kind: TranscriptCommand, Text: "$ " + command, CommandText: command})
	if !ok || entry.CommandText == "" || len(entry.CommandText) > maxExportedTranscriptEntryBytes {
		t.Fatalf("exported command metadata length = %d, exported = %t", len(entry.CommandText), ok)
	}
	if got := transcriptEntryExportBytes(entry); got != len(entry.Text)+len(entry.CommandText) {
		t.Fatalf("export byte budget omitted command metadata: %d", got)
	}
}
