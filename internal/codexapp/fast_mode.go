package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// FastModeSnapshot separates the shared next-turn policy from a running turn.
// Unknown/error states must never be presented as fast being disabled.
type FastModeSnapshot struct {
	Managed   bool
	Tier      string
	Error     string
	Pending   bool
	ExpiresAt time.Time
	Expired   bool
}

type fastModeConfig struct {
	Tier      string
	Profile   string
	ExpiresAt time.Time
	Expired   bool
	Error     string
}

type fastModeState struct {
	home             string
	mu               sync.Mutex // disk reads and native config writes; never acquired by snapshots
	value            atomic.Pointer[fastModeConfig]
	listeners        map[*appServerSession]chan struct{}
	polling          bool
	now              func() time.Time
	adminCall        fastModeRPCCall
	expiryError      string
	expiryRetryAfter time.Time
}

var fastModeHomes = struct {
	sync.Mutex
	states map[string]*fastModeState
}{states: make(map[string]*fastModeState)}

func acquireFastModeState(home string) *fastModeState {
	fastModeHomes.Lock()
	defer fastModeHomes.Unlock()
	if state := fastModeHomes.states[home]; state != nil {
		state.refresh()
		return state
	}
	state := &fastModeState{home: home, listeners: make(map[*appServerSession]chan struct{})}
	state.refresh()
	fastModeHomes.states[home] = state
	return state
}

func readFastModeConfig(home string) fastModeConfig {
	raw, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if os.IsNotExist(err) {
		return fastModeConfig{Tier: "default"}
	}
	if err != nil {
		return fastModeConfig{Error: err.Error()}
	}
	var config struct {
		ServiceTier string `toml:"service_tier"`
		Profile     string `toml:"profile"`
		Profiles    map[string]struct {
			ServiceTier *string `toml:"service_tier"`
		} `toml:"profiles"`
	}
	if err := toml.Unmarshal(raw, &config); err != nil {
		return fastModeConfig{Error: err.Error()}
	}
	tier := config.ServiceTier
	if profile, ok := config.Profiles[config.Profile]; ok && profile.ServiceTier != nil {
		tier = *profile.ServiceTier
	}
	tier = strings.ToLower(strings.TrimSpace(tier))
	if tier == "" {
		tier = "default"
	}
	switch tier {
	case "default", "fast", "priority", "flex":
		return fastModeConfig{Tier: tier, Profile: config.Profile}
	default:
		return fastModeConfig{Error: fmt.Sprintf("unrecognized Codex service tier %q", tier)}
	}
}

func (f *fastModeState) publishLocked(value fastModeConfig) {
	previous := f.value.Load()
	if previous != nil && *previous == value {
		return
	}
	f.value.Store(&value)
	for s, updates := range f.listeners {
		select {
		case updates <- struct{}{}:
		default:
		}
		s.notify()
	}
}

func (f *fastModeState) refresh() fastModeConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	value := f.readLocked()
	f.publishLocked(value)
	return value
}

func (s *appServerSession) watchFastMode() {
	f := s.fastMode
	updates := make(chan struct{}, 1)
	f.mu.Lock()
	f.listeners[s] = updates
	if !f.polling {
		f.polling = true
		go f.poll()
	}
	f.mu.Unlock()
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		defer func() { f.mu.Lock(); delete(f.listeners, s); f.mu.Unlock() }()
		for {
			select {
			case <-s.exitCh:
				return
			case <-ticker.C:
			case <-updates:
			}
			ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
			_ = s.syncFastMode(ctx)
			cancel()
		}
	}()
}

// syncFastMode updates loaded thread defaults as well as explicit turn requests.
// Codex config writes intentionally do not hot-reload service tiers.
func (s *appServerSession) syncFastMode(ctx context.Context) error {
	if s.fastMode == nil {
		return nil
	}
	s.fastModeMu.Lock()
	defer s.fastModeMu.Unlock()
	return s.syncFastModeLocked(ctx)
}

func (s *appServerSession) syncFastModeLocked(ctx context.Context) error {
	if s.fastMode == nil {
		return nil
	}
	s.fastMode.mu.Lock()
	value := s.fastMode.readLocked()
	value = s.fastMode.expireLocked(ctx, value, s.call)
	s.fastMode.publishLocked(value)
	s.fastMode.mu.Unlock()
	if value.Error != "" {
		return fmt.Errorf("Codex fast mode status unknown: %s", value.Error)
	}
	s.mu.Lock()
	threadID, closed, external := s.threadID, s.closed, s.busyExternal
	applied := s.fastModeApplied
	revision := s.fastModeSettingsRevision
	s.mu.Unlock()
	if closed {
		return fmt.Errorf("Codex session is closed")
	}
	if external {
		return fmt.Errorf("fast mode of a turn owned by another process is unknown")
	}
	if sameFastModeTier(applied, value.Tier) {
		s.mu.Lock()
		hadError := s.fastModeError != ""
		s.fastModeError = ""
		s.mu.Unlock()
		if hadError {
			s.notify()
		}
		return nil
	}
	_, err := s.call(ctx, "thread/settings/update", map[string]any{"threadId": threadID, "serviceTier": value.Tier})
	s.mu.Lock()
	if err == nil && s.fastModeSettingsRevision != revision && !sameFastModeTier(s.fastModeApplied, value.Tier) {
		err = fmt.Errorf("Codex reported service tier %q after requesting %q", s.fastModeApplied, value.Tier)
	}
	if err != nil {
		s.fastModeError = "Could not apply fast mode: " + err.Error()
	} else {
		if s.fastModeSettingsRevision == revision {
			s.fastModeApplied = value.Tier
		}
		s.fastModeError = ""
		if !s.busy && s.activeTurnID == "" {
			s.serviceTier = value.Tier
		}
	}
	s.mu.Unlock()
	s.notify()
	return err
}

// FastMode is a local operator command, never a prompt sent to the model.
// A bare /fast shows status; enabling always requires the explicit 'on' form.
func (s *appServerSession) FastMode(mode string) error {
	if s.fastMode == nil {
		return fmt.Errorf("shared Codex fast mode is unavailable; reconnect this session")
	}
	if mode != "on" && mode != "off" && mode != "status" {
		return fmt.Errorf("usage: /fast [on|off|status]")
	}
	s.mu.Lock()
	unavailable := s.closed || s.busyExternal
	s.mu.Unlock()
	if unavailable {
		return fmt.Errorf("fast mode controls require an open LCR-owned Codex session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	if mode != "status" {
		f := s.fastMode
		if !f.mu.TryLock() {
			return fmt.Errorf("Codex fast mode update already in progress; try again")
		}
		value := readFastModeConfig(f.home)
		if value.Error != "" {
			f.publishLocked(value)
			f.mu.Unlock()
			return fmt.Errorf("read Codex fast mode: %s", value.Error)
		}
		tier := "default"
		if mode == "on" {
			tier = "fast"
			// Repeating /fast on during a window does not extend its deadline.
			current := f.readLocked()
			if IsFastServiceTier(value.Tier) && current.Error != "" && !current.Expired {
				f.publishLocked(current)
				f.mu.Unlock()
				return fmt.Errorf("%s; use /fast off to recover", current.Error)
			}
			if !IsFastServiceTier(value.Tier) || current.Expired {
				now := f.currentTime().UTC()
				if err := f.saveTimerLocked(fastModeTimer{StartedAt: now, ExpiresAt: now.Add(fastModeMaxDuration)}); err != nil {
					f.mu.Unlock()
					return fmt.Errorf("cannot enable fast mode without a saved timer: %w", err)
				}
			}
		}
		err := f.writeConfigLocked(ctx, value, tier, s.call)
		updated := f.readLocked()
		f.publishLocked(updated)
		f.mu.Unlock()
		if err != nil {
			return err
		}

	}
	if err := s.syncFastMode(ctx); err != nil {
		return err
	}
	snapshot := s.StateSnapshot()
	text := "Codex fast mode OFF (standard)."
	if snapshot.FastMode.Tier == "flex" {
		text = "Codex fast mode OFF (flex tier)."
	}
	if IsFastServiceTier(snapshot.FastMode.Tier) {
		text = "Codex FAST ON — increased usage limits/credits consumption."
		if !snapshot.FastMode.ExpiresAt.IsZero() {
			text += " Automatically switches off at " + snapshot.FastMode.ExpiresAt.Local().Format("15:04:05 MST") + "."
		}
	}
	text += " Shared across LCR Codex engineers, including new and resumed sessions. /fast off disables it."
	if snapshot.Busy {
		text += " A running turn keeps its original tier until it finishes; /pause stops it."
	}
	s.appendSystemNotice(text)
	return nil
}

func IsFastServiceTier(tier string) bool {
	return tier == "fast" || tier == "priority"
}

func (s *appServerSession) fastModeSnapshotLocked() FastModeSnapshot {
	if s.fastMode == nil {
		return FastModeSnapshot{}
	}
	value := s.fastMode.value.Load()
	result := FastModeSnapshot{ExpiresAt: value.ExpiresAt, Expired: value.Expired, Managed: true, Tier: value.Tier, Error: value.Error, Pending: !sameFastModeTier(s.fastModeApplied, value.Tier)}
	if s.closed {
		result.Pending = false
		return result
	}
	if result.Error == "" {
		result.Error = s.fastModeError
	}
	if s.busyExternal {
		result.Error = "Active turn owned by another process; fast mode unknown"
	}
	return result
}

func (s *appServerSession) sharedServiceTier() string {
	if s.fastMode == nil {
		return ""
	}
	value := s.fastMode.value.Load()
	if value.Expired || value.Error != "" {
		return "default"
	}
	return value.Tier
}

// One disk poll per Codex home, regardless of the number of loaded engineers.
func (f *fastModeState) poll() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if !f.pollOnce() {
			return
		}
	}
}

func sameFastModeTier(a, b string) bool {
	return a == b || (IsFastServiceTier(a) && IsFastServiceTier(b))
}

func (s *appServerSession) handleFastModeSettingsUpdated(params json.RawMessage) {
	var message struct {
		ThreadID string `json:"threadId"`
		Settings struct {
			ServiceTier json.RawMessage `json:"serviceTier"`
		} `json:"threadSettings"`
	}
	if json.Unmarshal(params, &message) != nil || len(message.Settings.ServiceTier) == 0 {
		return
	}
	var tier *string
	if json.Unmarshal(message.Settings.ServiceTier, &tier) != nil {
		return
	}
	value := "default"
	if tier != nil && strings.TrimSpace(*tier) != "" {
		value = strings.TrimSpace(*tier)
	}
	s.mu.Lock()
	if !s.notificationMatchesThreadLocked(message.ThreadID) {
		s.mu.Unlock()
		return
	}
	s.fastModeApplied = value
	s.fastModeSettingsRevision++
	if !s.busy && s.activeTurnID == "" {
		s.serviceTier = value
	}
	s.mu.Unlock()
	s.notify()
}

func (f *fastModeState) writeConfigLocked(ctx context.Context, value fastModeConfig, tier string, call fastModeRPCCall) error {
	if strings.ContainsAny(value.Profile, ".\"[]") {
		return fmt.Errorf("cannot safely edit fast mode for profile %q; change service_tier in Codex config", value.Profile)
	}
	edits := []map[string]any{{"keyPath": "service_tier", "value": tier, "mergeStrategy": "replace"}}
	if value.Profile != "" {
		edits = append(edits, map[string]any{"keyPath": "profiles." + value.Profile + ".service_tier", "value": tier, "mergeStrategy": "replace"})
	}
	if IsFastServiceTier(tier) {
		edits = append(edits, map[string]any{"keyPath": "features.fast_mode", "value": true, "mergeStrategy": "replace"})
	}
	_, err := call(ctx, "config/batchWrite", map[string]any{"filePath": filepath.Join(f.home, "config.toml"), "edits": edits})
	if err != nil {
		return fmt.Errorf("save Codex fast mode: %w", err)
	}
	updated := readFastModeConfig(f.home)
	if updated.Error != "" || !sameFastModeTier(updated.Tier, tier) {
		return fmt.Errorf("Codex fast mode save could not be verified; use /fast status")
	}
	return nil
}
