package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReadFastModeConfig(t *testing.T) {
	for _, tt := range []struct {
		name, raw, tier string
		invalid         bool
	}{
		{"missing", "", "default", false},
		{"fast", "service_tier='fast'", "fast", false},
		{"priority", "service_tier='priority'", "priority", false},
		{"off", "service_tier='default'", "default", false},
		{"profile", "service_tier='fast'\nprofile='work'\n[profiles.work]\nservice_tier='default'", "default", false},
		{"malformed", "service_tier=[", "", true},
		{"unknown", "service_tier='unexpected'", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.raw != "" {
				if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tt.raw), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got := readFastModeConfig(dir)
			if got.Tier != tt.tier || (got.Error != "") != tt.invalid {
				t.Fatalf("got %#v", got)
			}
		})
	}
}

type fastModeTestRPC struct {
	mu          sync.Mutex
	settings    []string
	turns       []string
	writeErr    error
	settingsErr error
}

func newFastModeTestSession(t *testing.T, state *fastModeState, id string) (*appServerSession, *fastModeTestRPC) {
	t.Helper()
	rpc := &fastModeTestRPC{}
	s := &appServerSession{threadID: id, projectPath: t.TempDir(), fastMode: state, serviceTier: "default", notify: func() {}, exitCh: make(chan struct{}), entryIndex: make(map[string]int)}
	s.rpcCallHook = func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		rpc.mu.Lock()
		defer rpc.mu.Unlock()
		switch method {
		case "config/batchWrite":
			if rpc.writeErr != nil {
				return nil, rpc.writeErr
			}
			p := params.(map[string]any)
			if p["filePath"] != filepath.Join(state.home, "config.toml") {
				t.Errorf("write targeted overlay or wrong home: %#v", p)
			}
			tier := p["edits"].([]map[string]any)[0]["value"].(string)
			return json.RawMessage(`{}`), os.WriteFile(filepath.Join(state.home, "config.toml"), []byte(fmt.Sprintf("service_tier=%q\n", tier)), 0600)
		case "thread/settings/update":
			if rpc.settingsErr != nil {
				return nil, rpc.settingsErr
			}
			rpc.settings = append(rpc.settings, params.(map[string]any)["serviceTier"].(string))
			return json.RawMessage(`{}`), nil
		case "turn/start":
			p := params.(turnStartParams)
			rpc.turns = append(rpc.turns, p.ServiceTier)
			return json.RawMessage(`{"turn":{"id":"new-turn"}}`), nil
		default:
			return nil, fmt.Errorf("unexpected method %s", method)
		}
	}
	t.Cleanup(func() { close(s.exitCh) })
	return s, rpc
}

func newFastModeTestState(t *testing.T) *fastModeState {
	t.Helper()
	state := &fastModeState{home: t.TempDir(), listeners: make(map[*appServerSession]chan struct{})}
	state.refresh()
	return state
}

func waitFastModeTier(t *testing.T, s *appServerSession, tier string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap := s.StateSnapshot()
		if snap.FastMode.Tier == tier && !snap.FastMode.Pending && snap.FastMode.Error == "" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("tier did not converge: %#v", s.StateSnapshot().FastMode)
}

func TestFastModeSharedSessionsPersistenceAndRunningTurn(t *testing.T) {
	state := newFastModeTestState(t)
	first, _ := newFastModeTestSession(t, state, "first")
	older, olderRPC := newFastModeTestSession(t, state, "older")
	older.watchFastMode()
	if err := first.FastMode("on"); err != nil {
		t.Fatal(err)
	}
	waitFastModeTier(t, older, "fast")
	if got := older.StateSnapshot().ServiceTier; got != "fast" {
		t.Fatalf("older idle session tier=%s", got)
	}
	older.mu.Lock()
	older.busy = true
	older.activeTurnID = "fast-turn"
	older.mu.Unlock()
	if err := first.FastMode("off"); err != nil {
		t.Fatal(err)
	}
	waitFastModeTier(t, older, "default")
	if got := older.StateSnapshot().ServiceTier; got != "fast" {
		t.Fatalf("lost active fast warning: %s", got)
	}
	older.mu.Lock()
	older.busy = false
	older.activeTurnID = ""
	older.mu.Unlock()
	if got := older.StateSnapshot().ServiceTier; got != "default" {
		t.Fatalf("idle resumed tier=%s", got)
	}
	// A fresh controller simulates restarting LCR; historical session state cannot re-enable fast.
	restarted := &fastModeState{home: state.home, listeners: make(map[*appServerSession]chan struct{})}
	restarted.refresh()
	reopened, _ := newFastModeTestSession(t, restarted, "reopened-old")
	reopened.serviceTier = "fast"
	if err := reopened.syncFastMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reopened.StateSnapshot().ServiceTier; got != "default" {
		t.Fatalf("reopened old session tier=%s", got)
	}
	if err := older.Submit("continue"); err != nil {
		t.Fatal(err)
	}
	olderRPC.mu.Lock()
	defer olderRPC.mu.Unlock()
	if len(olderRPC.turns) != 1 || olderRPC.turns[0] != "default" {
		t.Fatalf("new turn did not explicitly clear fast: %v", olderRPC.turns)
	}
}

func TestFastModeFailuresNeverClaimDisabled(t *testing.T) {
	state := newFastModeTestState(t)
	s, rpc := newFastModeTestSession(t, state, "one")
	if err := s.FastMode("on"); err != nil {
		t.Fatal(err)
	}
	rpc.writeErr = errors.New("disk full")
	if err := s.FastMode("off"); err == nil {
		t.Fatal("expected write error")
	}
	if got := s.StateSnapshot(); got.FastMode.Tier != "fast" || got.ServiceTier != "fast" {
		t.Fatalf("failed write hid fast: %#v", got.FastMode)
	}
	rpc.writeErr = nil
	rpc.settingsErr = errors.New("settings RPC rejected")
	if err := s.FastMode("off"); err == nil {
		t.Fatal("expected settings error")
	}
	snap := s.StateSnapshot()
	if snap.ServiceTier != "fast" || !snap.FastMode.Pending || snap.FastMode.Error == "" {
		t.Fatalf("failed apply hidden: %#v", snap)
	}
	if err := s.Submit("must not run"); err == nil {
		t.Fatal("ran with unknown tier")
	}
	if len(rpc.turns) != 0 {
		t.Fatal("sent model request despite failed synchronization")
	}
	rpc.settingsErr = nil
	if err := s.FastMode("status"); err != nil {
		t.Fatal(err)
	}
	if s.StateSnapshot().FastMode.Error != "" {
		t.Fatal("error did not recover")
	}
}

func TestFastModeExternalConfigAndNonblockingSnapshot(t *testing.T) {
	state := newFastModeTestState(t)
	s, _ := newFastModeTestSession(t, state, "one")
	s.watchFastMode()
	if err := os.WriteFile(filepath.Join(state.home, "config.toml"), []byte("service_tier='priority'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	waitFastModeTier(t, s, "priority")
	state.mu.Lock()
	s.fastModeMu.Lock()
	_, ok := s.TryStateSnapshot()
	s.fastModeMu.Unlock()
	state.mu.Unlock()
	if !ok {
		t.Fatal("snapshot blocked behind fast-mode I/O")
	}
	if err := os.WriteFile(filepath.Join(state.home, "config.toml"), []byte("service_tier=["), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Submit("do not guess"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("malformed config error=%v", err)
	}
	if s.StateSnapshot().FastMode.Error == "" {
		t.Fatal("unknown config displayed as off")
	}
}

func TestFastModeStatusDoesNotToggleAndDuplicateWriteRejected(t *testing.T) {
	state := newFastModeTestState(t)
	s, rpc := newFastModeTestSession(t, state, "one")
	if err := s.FastMode("status"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state.home, "config.toml")); !os.IsNotExist(err) {
		t.Fatal("status wrote config")
	}
	state.mu.Lock()
	err := s.FastMode("on")
	state.mu.Unlock()
	if err == nil {
		t.Fatal("duplicate operation not rejected")
	}
	if len(rpc.turns) != 0 {
		t.Fatal("slash command sent to model")
	}
}

func TestFastModeSettingsNotificationsPreserveActiveTurn(t *testing.T) {
	state := newFastModeTestState(t)
	s, _ := newFastModeTestSession(t, state, "one")
	s.serviceTier = "fast"
	s.fastModeApplied = "fast"
	s.busy = true
	s.activeTurnID = "active-fast"
	s.handleNotification("thread/settings/updated", json.RawMessage(`{"threadId":"one","threadSettings":{"serviceTier":"default"}}`))
	if s.fastModeApplied != "default" || s.StateSnapshot().ServiceTier != "fast" {
		t.Fatal("settings update erased running fast tier")
	}
	s.handleNotification("thread/settings/updated", json.RawMessage(`{"threadId":"other","threadSettings":{"serviceTier":"priority"}}`))
	if s.fastModeApplied != "default" {
		t.Fatal("foreign notification changed tier")
	}
	s.handleNotification("thread/settings/updated", json.RawMessage(`{"threadId":"one","threadSettings":{}}`))
	if s.fastModeApplied != "default" {
		t.Fatal("omitted tier changed state")
	}
	s.busy = false
	s.activeTurnID = ""
	s.handleNotification("thread/settings/updated", json.RawMessage(`{"threadId":"one","threadSettings":{"serviceTier":null}}`))
	if s.StateSnapshot().ServiceTier != "default" {
		t.Fatal("explicit null did not clear tier")
	}
}

func TestFastModeHonorsServerConfirmedTier(t *testing.T) {
	for _, reported := range []string{"priority", "default"} {
		t.Run(reported, func(t *testing.T) {
			state := newFastModeTestState(t)
			if err := os.WriteFile(filepath.Join(state.home, "config.toml"), []byte("service_tier='fast'\n"), 0600); err != nil {
				t.Fatal(err)
			}
			s, _ := newFastModeTestSession(t, state, "one")
			calls := 0
			s.rpcCallHook = func(_ context.Context, method string, _ any) (json.RawMessage, error) {
				if method != "thread/settings/update" {
					return nil, fmt.Errorf("unexpected method %s", method)
				}
				calls++
				s.handleNotification("thread/settings/updated", json.RawMessage(fmt.Sprintf(`{"threadId":"one","threadSettings":{"serviceTier":%q}}`, reported)))
				return json.RawMessage(`{}`), nil
			}
			err := s.syncFastMode(context.Background())
			if s.fastModeApplied != reported {
				t.Fatalf("overwrote confirmed %s with %s", reported, s.fastModeApplied)
			}
			if reported == "default" {
				if err == nil || s.StateSnapshot().FastMode.Error == "" {
					t.Fatal("server tier mismatch was hidden")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if err := s.syncFastMode(context.Background()); err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatalf("fast/priority normalization caused %d settings writes", calls)
				}
			}
		})
	}
}

func TestClosedFastSessionShowsSharedOffSetting(t *testing.T) {
	state := newFastModeTestState(t)
	older, _ := newFastModeTestSession(t, state, "older")
	if err := older.FastMode("on"); err != nil {
		t.Fatal(err)
	}
	older.closed = true
	other, _ := newFastModeTestSession(t, state, "other")
	if err := other.FastMode("off"); err != nil {
		t.Fatal(err)
	}
	got := older.StateSnapshot()
	if got.ServiceTier != "default" || got.FastMode.Pending || got.FastMode.Error != "" {
		t.Fatalf("closed old session retained stale fast indication: tier=%q state=%#v", got.ServiceTier, got.FastMode)
	}
}
