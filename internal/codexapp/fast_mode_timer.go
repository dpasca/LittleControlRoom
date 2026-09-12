package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const fastModeMaxDuration = 2 * time.Hour
const fastModeTimerFilename = "lcroom-fast-mode-timer.json"

type fastModeTimer struct {
	StartedAt time.Time `json:"started_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (f *fastModeState) currentTime() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

func (f *fastModeState) saveTimerLocked(timer fastModeTimer) error {
	raw, err := json.Marshal(timer)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(f.home, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(f.home, ".lcroom-fast-timer-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(f.home, fastModeTimerFilename))
}

// The deadline is separate from thread history and is never renewed by a read.
// Native fast mode first encountered without a timer gets one bounded window.
func (f *fastModeState) readLocked() fastModeConfig {
	value := readFastModeConfig(f.home)
	if value.Error != "" || !IsFastServiceTier(value.Tier) {
		return value
	}
	raw, err := os.ReadFile(filepath.Join(f.home, fastModeTimerFilename))
	var timer fastModeTimer
	if os.IsNotExist(err) {
		now := f.currentTime().UTC()
		timer = fastModeTimer{StartedAt: now, ExpiresAt: now.Add(fastModeMaxDuration)}
		err = f.saveTimerLocked(timer)
	} else if err == nil {
		err = json.Unmarshal(raw, &timer)
		if err == nil && (timer.StartedAt.IsZero() || timer.ExpiresAt.Sub(timer.StartedAt) != fastModeMaxDuration) {
			err = fmt.Errorf("invalid two-hour fast-mode deadline")
		}
	}
	if err != nil {
		value.Error = "Fast mode timer unavailable: " + err.Error()
		value.Expired = true
		if f.expiryError != "" {
			value.Error = f.expiryError
		}
		return value
	}
	value.ExpiresAt = timer.ExpiresAt
	now := f.currentTime()
	value.Expired = !now.Before(timer.ExpiresAt) || now.Before(timer.StartedAt)
	if value.Expired {
		value.Error = f.expiryError
	}
	return value
}

type fastModeRPCCall func(context.Context, string, any) (json.RawMessage, error)

// A closed engineer must not cancel the shared timer. Reuse a live connection
// when possible, otherwise start an administrative client without a model turn.
func (f *fastModeState) timerCall(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if f.adminCall != nil {
		return f.adminCall(ctx, method, params)
	}
	for session := range f.listeners {
		session.mu.Lock()
		closed := session.closed
		session.mu.Unlock()
		if !closed {
			return session.call(ctx, method, params)
		}
	}
	client, err := startThreadAdminClient(ctx, f.home)
	if err != nil {
		return nil, err
	}
	defer client.close()
	return client.call(ctx, method, params)
}

func (f *fastModeState) expireLocked(ctx context.Context, value fastModeConfig, call fastModeRPCCall) fastModeConfig {
	if !value.Expired {
		return value
	}
	if call == nil {
		call = f.timerCall
	}
	if err := f.writeConfigLocked(ctx, value, "default", call); err != nil {
		f.expiryError = "Fast mode expired; automatic switch-off failed: " + err.Error()
		value.Error = f.expiryError
		f.expiryRetryAfter = f.currentTime().Add(15 * time.Second)
		f.publishLocked(value)
		return value
	}
	f.expiryError = ""
	f.expiryRetryAfter = time.Time{}
	value = f.readLocked()
	f.publishLocked(value)
	for session := range f.listeners {
		session.appendSystemNotice("Fast mode switched off automatically. Use /fast on for another two-hour window. A running fast turn keeps its tier until it finishes; /pause stops it.")
	}
	return value
}

func (f *fastModeState) pollOnce() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	value := f.readLocked()
	if value.Expired && !f.currentTime().Before(f.expiryRetryAfter) {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		value = f.expireLocked(ctx, value, nil)
		cancel()
	}
	f.publishLocked(value)
	if len(f.listeners) == 0 && !IsFastServiceTier(value.Tier) {
		f.polling = false
		return false
	}
	return true
}
