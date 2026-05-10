package session

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWriterWritesJSONLToFileAndStream(t *testing.T) {
	var stream bytes.Buffer
	now := time.Date(2026, 5, 9, 1, 2, 3, 0, time.UTC)
	writer, id, err := NewWriter(t.TempDir(), now, &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if !strings.HasPrefix(id, "lca_") {
		t.Fatalf("id = %q", id)
	}
	if err := writer.Write(Meta(id, "/tmp/repo", "low", "scripted", "scripted", "test", now)); err != nil {
		t.Fatal(err)
	}
	if stream.Len() == 0 {
		t.Fatal("stream is empty")
	}
	data, err := os.ReadFile(writer.Path())
	if err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &event); err != nil {
		t.Fatal(err)
	}
	if event["type"] != "session_meta" || event["id"] != id {
		t.Fatalf("event = %#v", event)
	}
}
