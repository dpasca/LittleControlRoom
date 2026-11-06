package tui

import (
	"context"
	"errors"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
	"lcroom/internal/service"
)

func stopAgentTaskWorker(ctx context.Context, svc *service.Service, manager *codexapp.Manager, runtimes *projectrun.Manager, task model.AgentTask) error {
	cancelRun := func() error {
		_, err := svc.CancelAgentTaskRun(ctx, task.ID)
		return err
	}
	if manager != nil {
		if err := manager.StopProjectSession(task.WorkspacePath, func(snapshot codexapp.Snapshot) error {
			return svc.VerifyAgentTaskWorker(ctx, task.ID, snapshot)
		}, cancelRun, 10*time.Second); err != nil {
			return err
		}
	} else if err := cancelRun(); err != nil {
		return err
	}
	if runtimes == nil {
		return nil
	}
	if err := runtimes.StopProject(task.WorkspacePath); err != nil && !errors.Is(err, projectrun.ErrNotRunning) {
		return err
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		running := false
		for _, snapshot := range runtimes.SnapshotsForProject(task.WorkspacePath) {
			running = running || snapshot.Running
		}
		if !running {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
