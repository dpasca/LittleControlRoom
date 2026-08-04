package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const threadDeleteHelperEnv = "LCROOM_THREAD_DELETE_HELPER"

func TestDeleteThreadsUsesAppServerThreadDelete(t *testing.T) {
	codexHome := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "requests.log")
	original := newThreadAdminCommand
	newThreadAdminCommand = func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestThreadDeleteHelperProcess")
		cmd.Env = append(os.Environ(), threadDeleteHelperEnv+"=1", "LCROOM_THREAD_DELETE_LOG="+logPath)
		return cmd
	}
	t.Cleanup(func() { newThreadAdminCommand = original })

	deleted, err := DeleteThreads(context.Background(), codexHome, []string{"thread-a", "thread-b"})
	if err != nil {
		t.Fatalf("DeleteThreads() error = %v", err)
	}
	if strings.Join(deleted, ",") != "thread-a,thread-b" {
		t.Fatalf("deleted ids = %#v", deleted)
	}
	requests, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read helper requests: %v", err)
	}
	got := strings.Fields(string(requests))
	want := []string{"initialize", "initialized", "thread/delete:thread-a", "thread/delete:thread-b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("app-server requests = %#v, want %#v", got, want)
	}
}

func TestThreadDeleteHelperProcess(t *testing.T) {
	if os.Getenv(threadDeleteHelperEnv) != "1" {
		return
	}
	logPath := os.Getenv("LCROOM_THREAD_DELETE_LOG")
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage        `json:"id"`
			Method string                 `json:"method"`
			Params map[string]interface{} `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			continue
		}
		entry := request.Method
		if request.Method == "thread/delete" {
			entry += ":" + fmt.Sprint(request.Params["threadId"])
		}
		file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintln(file, entry)
			_ = file.Close()
		}
		if len(request.ID) == 0 {
			continue
		}
		_ = encoder.Encode(map[string]interface{}{
			"id":     json.RawMessage(request.ID),
			"result": map[string]interface{}{},
		})
	}
}
