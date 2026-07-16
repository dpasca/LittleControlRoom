package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/detectors"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
)

type schedulerDetector struct {
	called         chan struct{}
	waitForContext bool
}

func (d schedulerDetector) Name() string {
	return "scheduler-test"
}

func (d schedulerDetector) Detect(ctx context.Context, _ scanner.PathScope) (map[string]*model.DetectorProjectActivity, error) {
	select {
	case d.called <- struct{}{}:
	default:
	}
	if d.waitForContext {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return map[string]*model.DetectorProjectActivity{}, nil
}

func TestSchedulerAppliesLiveIntervalUpdates(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	called := make(chan struct{}, 1)
	cfg := config.Default()
	cfg.IncludePaths = nil
	cfg.ScanInterval = 0
	svc := &Service{
		cfg:       cfg,
		store:     st,
		bus:       events.NewBus(),
		detectors: []detectors.Detector{schedulerDetector{called: called}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.StartScheduler(ctx)
		close(done)
	}()

	settings := config.EditableSettingsFromAppConfig(cfg)
	settings.ScanInterval = 10 * time.Millisecond
	svc.ApplyEditableSettings(settings)

	select {
	case <-called:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("scheduler did not apply the enabled interval without a restart")
	}

	settings.ScanInterval = time.Hour
	svc.ApplyEditableSettings(settings)
	drainSignalChannel(called)
	select {
	case <-called:
		cancel()
		t.Fatal("scheduler did not reset to the longer live interval")
	case <-time.After(50 * time.Millisecond):
	}

	settings.ScanInterval = 10 * time.Millisecond
	svc.ApplyEditableSettings(settings)
	select {
	case <-called:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("scheduler did not reset to the shorter live interval")
	}

	settings.ScanInterval = 0
	svc.ApplyEditableSettings(settings)
	drainSignalChannel(called)
	select {
	case <-called:
		cancel()
		t.Fatal("scheduler did not disable live")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop after cancellation")
	}
}

func drainSignalChannel(ch <-chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func TestSchedulerSkipsTickWhileFullScanIsRunning(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	cfg := config.Default()
	cfg.IncludePaths = nil
	cfg.ScanInterval = 10 * time.Millisecond
	svc := &Service{
		cfg:       cfg,
		store:     st,
		bus:       events.NewBus(),
		detectors: []detectors.Detector{blockingDetector{started: started, release: release}},
	}

	fullScanDone := make(chan error, 1)
	go func() {
		_, scanErr := svc.ScanOnce(context.Background())
		fullScanDone <- scanErr
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("full scan did not reach blocking detector")
	}

	schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
	schedulerDone := make(chan struct{})
	go func() {
		svc.StartScheduler(schedulerCtx)
		close(schedulerDone)
	}()
	time.Sleep(60 * time.Millisecond)
	select {
	case <-started:
		t.Fatal("scheduled scan overlapped the running full scan")
	default:
	}
	cancelScheduler()
	select {
	case <-schedulerDone:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop after cancellation")
	}

	close(release)
	select {
	case err := <-fullScanDone:
		if err != nil {
			t.Fatalf("full scan error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("full scan did not finish after release")
	}
}

func TestScheduledScanTimeoutPublishesFailureAndReleasesGate(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	bus := events.NewBus()
	eventCh, unsubscribe := bus.Subscribe(8)
	defer unsubscribe()
	cfg := config.Default()
	cfg.IncludePaths = nil
	cfg.ScanInterval = 5 * time.Millisecond
	svc := &Service{
		cfg:                  cfg,
		store:                st,
		bus:                  bus,
		detectors:            []detectors.Detector{schedulerDetector{called: make(chan struct{}, 1), waitForContext: true}},
		scheduledScanTimeout: 20 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.StartScheduler(ctx)
		close(done)
	}()

	var failure events.Event
	select {
	case failure = <-eventCh:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("scheduled scan timeout did not publish a failure event")
	}
	if failure.Type != events.ScanFailed {
		t.Fatalf("event type = %s, want %s", failure.Type, events.ScanFailed)
	}
	if failure.Payload["source"] != "scheduled" || failure.Payload["error_kind"] != "timeout" {
		t.Fatalf("failure payload = %#v, want scheduled timeout", failure.Payload)
	}
	if !strings.Contains(failure.Payload["error"], "deadline exceeded") {
		t.Fatalf("failure error = %q, want deadline detail", failure.Payload["error"])
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop after cancellation")
	}
	unlock, ok := svc.tryLockFullScan()
	if !ok {
		t.Fatal("full-scan gate remained locked after scheduled timeout")
	}
	unlock()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer waitCancel()
	if _, err := svc.ScanOnce(waitCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("follow-up scan error = %v, want only its own cooperative timeout", err)
	}
}
