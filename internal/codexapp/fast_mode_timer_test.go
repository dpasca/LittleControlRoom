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

func TestFastModeTimerSharedPersistentAndExplicitRenewal(t *testing.T) {
	state := newFastModeTestState(t)
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return now }
	first, _ := newFastModeTestSession(t, state, "first")
	older, _ := newFastModeTestSession(t, state, "older")
	if err := first.FastMode("on"); err != nil {
		t.Fatal(err)
	}
	deadline := now.Add(2 * time.Hour)
	if got := first.StateSnapshot().FastMode.ExpiresAt; !got.Equal(deadline) {
		t.Fatalf("deadline=%s", got)
	}
	now = now.Add(time.Hour)
	if err := older.FastMode("on"); err != nil {
		t.Fatal(err)
	}
	if got := older.StateSnapshot().FastMode.ExpiresAt; !got.Equal(deadline) {
		t.Fatal("another engineer extended the timer")
	}
	restarted := &fastModeState{home: state.home, now: state.now, listeners: make(map[*appServerSession]chan struct{})}
	reopened, _ := newFastModeTestSession(t, restarted, "reopened")
	if err := reopened.syncFastMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reopened.StateSnapshot().FastMode.ExpiresAt; !got.Equal(deadline) {
		t.Fatal("restart reset the timer")
	}
	first.busy = true
	first.activeTurnID = "running-fast"
	now = deadline
	if err := first.syncFastMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := first.StateSnapshot()
	if got.FastMode.Tier != "default" || got.ServiceTier != "fast" {
		t.Fatalf("expiry failed to preserve running warning: %#v", got)
	}
	if err := older.syncFastMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	if older.StateSnapshot().ServiceTier != "default" {
		t.Fatal("older idle session stayed fast")
	}
	now = now.Add(time.Hour)
	if err := reopened.syncFastMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reopened.StateSnapshot().ServiceTier != "default" {
		t.Fatal("expired fast mode re-enabled itself on reopen")
	}
	if err := reopened.FastMode("on"); err != nil {
		t.Fatal(err)
	}
	if !reopened.StateSnapshot().FastMode.ExpiresAt.Equal(now.Add(2 * time.Hour)) {
		t.Fatal("explicit re-enable did not start a fresh window")
	}
}

func TestFastModeTimerExpiresWithoutOpenEngineers(t *testing.T) {
	state := newFastModeTestState(t)
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return now }
	s, rpc := newFastModeTestSession(t, state, "closed")
	if err := s.FastMode("on"); err != nil {
		t.Fatal(err)
	}
	s.closed = true
	state.adminCall = s.rpcCallHook
	if !state.pollOnce() {
		t.Fatal("closing all engineers canceled the timer")
	}
	now = now.Add(2 * time.Hour)
	if state.pollOnce() {
		t.Fatal("empty home kept polling after successful expiry")
	}
	if got := readFastModeConfig(state.home); got.Tier != "default" {
		t.Fatalf("native default remained fast: %#v", got)
	}
	if len(rpc.turns) != 0 {
		t.Fatal("timer started a model turn")
	}
}

func TestFastModeTimerExpiredWhileLCRWasClosed(t *testing.T) {
	state := newFastModeTestState(t)
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return now }
	s, _ := newFastModeTestSession(t, state, "first")
	if err := s.FastMode("on"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Hour)
	restarted := &fastModeState{home: state.home, now: state.now, listeners: make(map[*appServerSession]chan struct{})}
	restarted.refresh()
	resumed, rpc := newFastModeTestSession(t, restarted, "resumed")
	if got := resumed.sharedServiceTier(); got != "default" {
		t.Fatalf("resume would inherit expired tier %q", got)
	}
	if err := resumed.Submit("continue"); err != nil {
		t.Fatal(err)
	}
	if len(rpc.turns) != 1 || rpc.turns[0] != "default" {
		t.Fatalf("resumed request used expired fast: %v", rpc.turns)
	}
}

func TestFastModeTimerExpiryFailureBlocksInferenceAndRetries(t *testing.T) {
	state := newFastModeTestState(t)
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return now }
	s, rpc := newFastModeTestSession(t, state, "one")
	state.adminCall = s.rpcCallHook
	if err := s.FastMode("on"); err != nil {
		t.Fatal(err)
	}
	deadline := s.StateSnapshot().FastMode.ExpiresAt
	now = deadline
	rpc.writeErr = errors.New("disk full")
	if !state.pollOnce() {
		t.Fatal("stopped retrying failed expiry")
	}
	snapshot := s.StateSnapshot()
	if !snapshot.FastMode.Expired || !strings.Contains(snapshot.FastMode.Error, "automatic switch-off failed") {
		t.Fatalf("expiry failure hidden: %#v", snapshot.FastMode)
	}
	if err := s.Submit("must not run fast"); err == nil {
		t.Fatal("started inference after failed expiry")
	}
	if len(rpc.turns) != 0 {
		t.Fatal("sent a model request")
	}
	if !state.readLocked().ExpiresAt.Equal(deadline) {
		t.Fatal("failed expiry renewed the timer")
	}
	rpc.writeErr = nil
	now = now.Add(15 * time.Second)
	state.pollOnce()
	if err := s.syncFastMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.StateSnapshot().ServiceTier != "default" {
		t.Fatal("recovered expiry did not turn fast off")
	}
}

func TestFastModeTimerMustPersistBeforeEnabling(t *testing.T) {
	state := newFastModeTestState(t)
	s, _ := newFastModeTestSession(t, state, "one")
	if err := os.Mkdir(filepath.Join(state.home, fastModeTimerFilename), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.FastMode("on"); err == nil {
		t.Fatal("enabled without a persisted deadline")
	}
	if readFastModeConfig(state.home).Tier != "default" {
		t.Fatal("failed timer persistence enabled fast")
	}
}

func TestFastModeTimerMalformedStateDisablesFast(t *testing.T) {
	state := newFastModeTestState(t)
	s, rpc := newFastModeTestSession(t, state, "one")
	if err := s.FastMode("on"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state.home, fastModeTimerFilename), []byte(`{"expires_at":"invalid"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Submit("use standard when the timer is invalid"); err != nil {
		t.Fatal(err)
	}
	if len(rpc.turns) != 1 || rpc.turns[0] != "default" {
		t.Fatal("malformed timer permitted fast inference")
	}
	if err := s.FastMode("off"); err != nil {
		t.Fatal(err)
	}
	if s.StateSnapshot().ServiceTier != "default" {
		t.Fatal("could not disable after malformed timer")
	}
}

func TestFastModeTimerStartsAdministrativeConnectionWithoutEngineer(t *testing.T) {
	state := newFastModeTestState(t)
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return now }
	s, _ := newFastModeTestSession(t, state, "closed")
	if err := s.FastMode("on"); err != nil {
		t.Fatal(err)
	}
	s.closed = true
	original := newThreadAdminCommand
	newThreadAdminCommand = func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestFastModeTimerAdminHelper$")
		cmd.Env = append(os.Environ(), threadDeleteHelperEnv+"=1", "LCROOM_FAST_TIMER_HELPER=1")
		return cmd
	}
	t.Cleanup(func() { newThreadAdminCommand = original })
	now = now.Add(2 * time.Hour)
	if state.pollOnce() {
		t.Fatalf("administrative expiry failed: %#v", state.value.Load())
	}
	if readFastModeConfig(state.home).Tier != "default" {
		t.Fatal("administrative connection did not persist standard speed")
	}
}

func TestFastModeTimerAdminHelper(t *testing.T) {
	if os.Getenv("LCROOM_FAST_TIMER_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req struct {
			ID     json.RawMessage            `json:"id"`
			Method string                     `json:"method"`
			Params map[string]json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			os.Exit(2)
		}
		switch req.Method {
		case "initialize", "initialized":
		case "config/batchWrite":
			var path string
			if json.Unmarshal(req.Params["filePath"], &path) != nil || path != filepath.Join(os.Getenv("CODEX_HOME"), "config.toml") {
				os.Exit(3)
			}
			if err := os.WriteFile(path, []byte("service_tier='default'\n"), 0600); err != nil {
				os.Exit(4)
			}
		default:
			os.Exit(5) // No thread or model requests are allowed in this connection.
		}
		if len(req.ID) > 0 {
			fmt.Printf("{\"id\":%s,\"result\":{}}\n", req.ID)
		}
	}
	os.Exit(0)
}
