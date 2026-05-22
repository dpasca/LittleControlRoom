package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Writer struct {
	mu      sync.Mutex
	file    *os.File
	stream  io.Writer
	encoder *json.Encoder
	path    string
}

type Event map[string]any

func NewWriter(dataDir string, now time.Time, stream io.Writer) (*Writer, string, error) {
	id, err := NewID()
	if err != nil {
		return nil, "", err
	}
	path := SessionPath(dataDir, now, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, "", err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", err
	}
	return &Writer{
		file:    file,
		stream:  stream,
		encoder: json.NewEncoder(file),
		path:    path,
	}, id, nil
}

func SessionPath(dataDir string, t time.Time, id string) string {
	return filepath.Join(dataDir, "lcagent", "sessions", t.Format("2006"), t.Format("01"), t.Format("02"), id+".jsonl")
}

func NewID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "lca_" + hex.EncodeToString(b[:]), nil
}

func (w *Writer) Path() string {
	if w == nil {
		return ""
	}
	return w.path
}

func (w *Writer) Close() error {
	if w == nil || w.file == nil {
		return nil
	}
	return w.file.Close()
}

func (w *Writer) Write(event Event) error {
	return w.write(event, true)
}

func (w *Writer) WritePrivate(event Event) error {
	return w.write(event, false)
}

func (w *Writer) write(event Event, stream bool) error {
	if w == nil {
		return fmt.Errorf("session writer is nil")
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, ok := event["event_id"]; !ok {
		id, err := NewID()
		if err != nil {
			return err
		}
		event["event_id"] = id
	}
	if _, ok := event["timestamp"]; !ok {
		event["timestamp"] = time.Now().Format(time.RFC3339Nano)
	}

	line, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := w.file.Write(append(line, '\n')); err != nil {
		return err
	}
	if stream && w.stream != nil {
		if _, err := w.stream.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func Meta(id, cwd, auto, provider, model, version string, startedAt time.Time) Event {
	return Event{
		"type":        "session_meta",
		"id":          id,
		"started_at":  startedAt.Format(time.RFC3339Nano),
		"cwd":         cwd,
		"auto":        auto,
		"provider":    provider,
		"model":       model,
		"cli_version": version,
	}
}
