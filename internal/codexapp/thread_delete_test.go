package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const threadDeleteHelperEnv = "LCROOM_THREAD_DELETE_HELPER"
const threadDeleteBlockEnv = "LCROOM_THREAD_DELETE_BLOCK"

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

func TestDeleteThreadsCancellationStopsBlockedAppServer(t *testing.T) {
	codexHome := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "requests.log")
	original := newThreadAdminCommand
	newThreadAdminCommand = func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestThreadDeleteHelperProcess")
		cmd.Env = append(os.Environ(),
			threadDeleteHelperEnv+"=1",
			threadDeleteBlockEnv+"=1",
			"LCROOM_THREAD_DELETE_LOG="+logPath,
		)
		return cmd
	}
	t.Cleanup(func() { newThreadAdminCommand = original })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type deleteResponse struct {
		deleted []string
		err     error
	}
	done := make(chan deleteResponse, 1)
	go func() {
		deleted, err := DeleteThreads(ctx, codexHome, []string{"thread-blocked"})
		done <- deleteResponse{deleted: deleted, err: err}
	}()

	requestDeadline := time.Now().Add(3 * time.Second)
	for {
		requests, _ := os.ReadFile(logPath)
		if strings.Contains(string(requests), "thread/delete:thread-blocked") {
			break
		}
		if time.Now().After(requestDeadline) {
			cancel()
			t.Fatal("blocked app-server did not receive thread/delete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	select {
	case response := <-done:
		if !errors.Is(response.err, context.Canceled) {
			t.Fatalf("DeleteThreads() cancellation error = %v", response.err)
		}
		if len(response.deleted) != 0 {
			t.Fatalf("deleted ids before canceled response = %#v", response.deleted)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("DeleteThreads() did not stop after cancellation")
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
		if request.Method == "thread/delete" && os.Getenv(threadDeleteBlockEnv) == "1" {
			continue
		}
		_ = encoder.Encode(map[string]interface{}{
			"id":     json.RawMessage(request.ID),
			"result": map[string]interface{}{},
		})
	}
}
