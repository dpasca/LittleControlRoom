package codexstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReadThreadLineage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		threadID string
		payload  map[string]any
		want     ThreadLineage
	}{
		{
			name:     "direct root",
			threadID: "root",
			payload:  map[string]any{"id": "root"},
			want:     ThreadLineage{ThreadID: "root", RootID: "root", IsRoot: true, Known: true},
		},
		{
			name:     "spawned descendant with root and parent",
			threadID: "child",
			payload: map[string]any{
				"id":         "child",
				"session_id": "root",
				"agent_role": "worker",
				"source": map[string]any{
					"subagent": map[string]any{
						"thread_spawn": map[string]any{"parent_thread_id": "root"},
					},
				},
			},
			want: ThreadLineage{ThreadID: "child", RootID: "root", ParentID: "root", Known: true},
		},
		{
			name:     "ambiguous fork is not assumed to be a root",
			threadID: "fork",
			payload: map[string]any{
				"id":             "fork",
				"forked_from_id": "parent",
			},
			want: ThreadLineage{ThreadID: "fork"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "rollout.jsonl")
			line, err := json.Marshal(map[string]any{"type": "session_meta", "payload": test.payload})
			if err != nil {
				t.Fatalf("encode metadata: %v", err)
			}
			if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
				t.Fatalf("write metadata: %v", err)
			}
			got, err := ReadThreadLineage(path, test.threadID)
			if err != nil {
				t.Fatalf("ReadThreadLineage() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("ReadThreadLineage() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestThreadSourceParentID(t *testing.T) {
	t.Parallel()

	source := `{"subagent":{"thread_spawn":{"parent_thread_id":"root-thread"}}}`
	if got := ThreadSourceParentID(source); got != "root-thread" {
		t.Fatalf("ThreadSourceParentID() = %q, want root-thread", got)
	}
	if got := ThreadSourceParentID("vscode"); got != "" {
		t.Fatalf("direct ThreadSourceParentID() = %q, want empty", got)
	}
}
