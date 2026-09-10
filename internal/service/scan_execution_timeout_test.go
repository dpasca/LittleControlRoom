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

type scanBudgetDetector struct {
	budget         chan time.Duration
	waitForContext bool
}

func (d scanBudgetDetector) Name() string { return "scan-budget" }

func (d scanBudgetDetector) Detect(ctx context.Context, _ scanner.PathScope) (map[string]*model.DetectorProjectActivity, error) {
	deadline, _ := ctx.Deadline()
	d.budget <- time.Until(deadline)
	if d.waitForContext {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, nil
}

func TestScanExecutionTimeoutStartsAfterWaitingForFullScan(t *testing.T) {
	for _, timesOut := range []bool{false, true} {
		name := "success"
		if timesOut {
			name = "execution timeout"
		}
		t.Run(name, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "scan.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			cfg := config.Default()
			cfg.IncludePaths = nil
			budget := make(chan time.Duration, 1)
			svc := &Service{
				cfg:       cfg,
				store:     st,
				bus:       events.NewBus(),
				detectors: []detectors.Detector{scanBudgetDetector{budget: budget, waitForContext: timesOut}},
			}

			// Simulate another full scan owning the gate for longer than the
			// queued scan's entire execution budget.
			unlock, ok := svc.tryLockFullScan()
			if !ok {
				t.Fatal("could not acquire scan gate")
			}
			defer func() {
				if unlock != nil {
					unlock()
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			const timeout = 100 * time.Millisecond
			go func() {
				_, err := svc.ScanWithOptions(ctx, ScanOptions{Timeout: timeout})
				done <- err
			}()
			select {
			case err := <-done:
				t.Fatalf("queued scan returned before acquiring gate: %v", err)
			case <-time.After(2 * timeout):
			}
			select {
			case <-budget:
				t.Fatal("queued scan started a detector while the gate was held")
			default:
			}
			unlock()
			unlock = nil

			select {
			case err := <-done:
				if timesOut {
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("scan error = %v, want execution deadline", err)
					}
					if detail, ok := ScanTimeoutDetail(err); !ok || !strings.Contains(detail, "scan-budget") {
						t.Fatalf("timeout detail = %q, %v; want detector phase", detail, ok)
					}
				} else if err != nil {
					t.Fatalf("queued scan failed: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("scan did not complete")
			}
			select {
			case remaining := <-budget:
				if remaining < timeout/2 || remaining > timeout {
					t.Fatalf("execution budget = %s, want a fresh %s budget", remaining, timeout)
				}
			default:
				t.Fatal("scan did not reach detector")
			}
			unlock, ok = svc.tryLockFullScan()
			if !ok {
				t.Fatal("scan did not release gate")
			}
		})
	}
}

func TestScanExecutionTimeoutPreservesCallerCancellationWhileQueued(t *testing.T) {
	svc := &Service{}
	unlock, _ := svc.tryLockFullScan()
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.ScanWithOptions(ctx, ScanOptions{Timeout: time.Minute})
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued scan error = %v, want caller cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued scan ignored caller cancellation")
	}
}
