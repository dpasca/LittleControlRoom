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
const threadDeleteFailEnv = "LCROOM_THREAD_DELETE_FAIL"

func TestMain(m *testing.M) {
	// The cleanup client probes its executable before starting app-server.
	// Answer that probe instead of recursively running this test suite.
	if os.Getenv(threadDeleteHelperEnv) == "1" && len(os.Args) > 2 && os.Args[1] == "features" && os.Args[2] == "list" {
		fmt.Println("code_mode_host experimental false")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

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
	if detail := os.Getenv(threadDeleteFailEnv); detail != "" {
		fmt.Fprintln(os.Stderr, detail)
		os.Exit(1)
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
		if request.Method == "thread/delete" && request.Params["threadId"] == "thread-fail" {
			_ = encoder.Encode(map[string]any{"id": request.ID, "error": map[string]any{"code": -32000, "message": "delete rejected"}})
			continue
		}
		if request.Method == "thread/delete" && os.Getenv(threadDeleteBlockEnv) == "1" {
			continue
		}
		if request.Method == "thread/delete" && os.Getenv("LCROOM_THREAD_DELETE_BARRIER") == "1" {
			deadline := time.Now().Add(5 * time.Second)
			for {
				data, _ := os.ReadFile(logPath)
				if strings.Count(string(data), "thread/delete:") >= 4 {
					break
				}
				if time.Now().After(deadline) {
					os.Exit(2)
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		_ = encoder.Encode(map[string]interface{}{
			"id":     json.RawMessage(request.ID),
			"result": map[string]interface{}{},
		})
	}
}

func TestDeleteThreadsConcurrentProgressAndClientReuse(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "requests.log")
	original := newThreadAdminCommand
	newThreadAdminCommand = func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestThreadDeleteHelperProcess")
		cmd.Env = append(os.Environ(), threadDeleteHelperEnv+"=1", "LCROOM_THREAD_DELETE_BARRIER=1", "LCROOM_THREAD_DELETE_LOG="+logPath)
		return cmd
	}
	t.Cleanup(func() { newThreadAdminCommand = original })
	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	active, peak, completed := 0, 0, 0
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	deleted, err := DeleteThreadsWithProgress(ctx, t.TempDir(), ids, func(p ThreadDeleteProgress) {
		if p.Completed {
			active--
			completed++
		} else {
			active++
			peak = max(peak, active)
		}
		if active < 0 || active > 4 {
			t.Errorf("active roots = %d", active)
		}
	})
	if err != nil || strings.Join(deleted, ",") != strings.Join(ids, ",") || peak != 4 || completed != len(ids) || active != 0 {
		t.Fatalf("deleted=%v error=%v peak=%d completed=%d active=%d", deleted, err, peak, completed, active)
	}
	data, err := os.ReadFile(logPath)
	if err != nil || strings.Count(string(data), "initialize\n") != 4 {
		t.Fatalf("expected four reused clients: %s (%v)", data, err)
	}
}

func TestDeleteThreadsConcurrentCancellationStopsQueuedRoots(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "requests.log")
	original := newThreadAdminCommand
	newThreadAdminCommand = func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestThreadDeleteHelperProcess")
		cmd.Env = append(os.Environ(), threadDeleteHelperEnv+"=1", threadDeleteBlockEnv+"=1", "LCROOM_THREAD_DELETE_LOG="+logPath)
		return cmd
	}
	t.Cleanup(func() { newThreadAdminCommand = original })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := 0
	deleted, err := DeleteThreadsWithProgress(ctx, t.TempDir(), []string{"a", "b", "c", "d", "queued-e", "queued-f"}, func(p ThreadDeleteProgress) {
		started++
		if started == 4 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || len(deleted) != 0 || started != 4 {
		t.Fatalf("deleted=%v error=%v started=%d", deleted, err, started)
	}
	data, _ := os.ReadFile(logPath)
	if strings.Contains(string(data), "queued-") {
		t.Fatalf("queued deletion started after cancellation: %s", data)
	}
}

func TestDeleteThreadsWorkerErrorCancelsSiblings(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "requests.log")
	original := newThreadAdminCommand
	newThreadAdminCommand = func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestThreadDeleteHelperProcess")
		cmd.Env = append(os.Environ(), threadDeleteHelperEnv+"=1", threadDeleteBlockEnv+"=1", "LCROOM_THREAD_DELETE_LOG="+logPath)
		return cmd
	}
	t.Cleanup(func() { newThreadAdminCommand = original })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	deleted, err := deleteThreads(ctx, t.TempDir(), []string{"blocked", "thread-fail", "queued"}, 2, nil)
	if err == nil || !strings.Contains(err.Error(), "delete rejected") || errors.Is(err, context.Canceled) || len(deleted) != 0 {
		t.Fatalf("deleted=%v error=%v", deleted, err)
	}
	data, _ := os.ReadFile(logPath)
	if strings.Contains(string(data), "thread/delete:queued") {
		t.Fatalf("queued request started after failure: %s", data)
	}
}

func TestDeleteThreadsReportsStderrFromImmediateAppServerExit(t *testing.T) {
	const detail = "codex: no authentication configured"
	original := newThreadAdminCommand
	newThreadAdminCommand = func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestThreadDeleteHelperProcess")
		cmd.Env = append(os.Environ(), threadDeleteHelperEnv+"=1", threadDeleteFailEnv+"="+detail)
		return cmd
	}
	t.Cleanup(func() { newThreadAdminCommand = original })

	// The app-server exits before answering initialize, so its stderr is the
	// only explanation available; cmd.Wait must not close the pipe first.
	deleted, err := DeleteThreads(context.Background(), t.TempDir(), []string{"thread-a"})
	if err == nil {
		t.Fatalf("DeleteThreads() error = nil, want failure; deleted=%#v", deleted)
	}
	if len(deleted) != 0 {
		t.Fatalf("deleted ids = %#v, want none", deleted)
	}
	if !strings.Contains(err.Error(), detail) {
		t.Fatalf("error = %v, want it to carry app-server stderr %q", err, detail)
	}
}
