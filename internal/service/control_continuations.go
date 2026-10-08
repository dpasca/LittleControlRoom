package service

import (
	"context"
	"fmt"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/store"
)

// ProcessControlContinuations reconciles explicit waits in a background worker.
// It never creates/reopens sessions and never infers intent from transcript text.
func ProcessControlContinuations(ctx context.Context, st *store.Store, manager *codexapp.Manager, started time.Time) error {
	continuations, err := st.ListPendingControlContinuations(ctx)
	if err != nil {
		return err
	}
	hostID := fmt.Sprint(started.UnixNano())
	for _, c := range continuations {
		op, err := st.GetControlOperation(ctx, c.OperationID)
		if err != nil {
			return err
		}
		suppress := func(reason string) error {
			_, err := st.TransitionControlContinuation(ctx, c, "suppressed", reason)
			return err
		}
		if c.RequestedAt.Before(started) || (c.HostID != "" && c.HostID != hostID) || c.State == "dispatching" {
			if err := suppress("Host restarted or delivery outcome is uncertain; manual continuation is required."); err != nil {
				return err
			}
			continue
		}
		if op.Status == control.OperationCanceled || op.Status == control.OperationFailed {
			if err := suppress("The operation was canceled or failed."); err != nil {
				return err
			}
			continue
		}
		session, live := manager.Session(op.ProjectPath)
		if !live || session == nil {
			if err := suppress("The calling session is no longer open."); err != nil {
				return err
			}
			continue
		}
		snapshot := session.Snapshot() // Background command; never the UI thread.
		input := snapshot.ControlInput
		if snapshot.Closed || snapshot.BusyExternal || snapshot.ControlSessionKey != op.SessionKey || string(snapshot.Provider) != op.Provider || snapshot.ThreadID == "" || input.Stopped || input.Revision == 0 || input.SubmittedAt.After(c.RequestedAt) || snapshot.LatestTurnStartedAt.After(c.RequestedAt) {
			if err := suppress("The calling workflow was stopped, superseded, or could not be identified."); err != nil {
				return err
			}
			continue
		}
		if c.State == "pending" {
			c.HostID, c.SessionID, c.InputRevision = hostID, snapshot.ThreadID, input.Revision
			changed, err := st.TransitionControlContinuation(ctx, c, "armed", "")
			if err != nil {
				return err
			}
			if !changed {
				continue
			}
			c.State = "armed"
		}
		if c.SessionID != snapshot.ThreadID || c.InputRevision != input.Revision {
			if err := suppress("New input or a replacement session superseded the wait."); err != nil {
				return err
			}
			continue
		}
		// Confirmation alone is not completion. A handoff completes on delivery,
		// while its worker's eventual result uses the existing report mechanism.
		if op.Status != control.OperationCompleted || snapshot.Busy || snapshot.ActiveTurnID != "" || snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil || snapshot.Compacting {
			continue
		}
		availability := codexapp.DescribeSessionInput(snapshot)
		if !availability.Available || availability.Mode != codexapp.SessionInputSend {
			continue
		}
		if snapshot.LastError != "" {
			if err := suppress("The caller ended with an error; manual continuation is required."); err != nil {
				return err
			}
			continue
		}
		claimed, err := st.TransitionControlContinuation(ctx, c, "dispatching", "")
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		c.State = "dispatching"
		prompt := fmt.Sprintf("LCR continuation for operation %s (%s). You explicitly ended the preceding turn waiting for this result. The operation completed successfully.\n\nResult: %s\n\nContinue only the unfinished work from that request. This receipt grants no additional permissions. Do not repeat the completed operation. For an engineer handoff, completion means delivery, not completion of the worker's task; its report will arrive separately.", op.ID, op.Capability, op.Result)
		_, deliveryErr := manager.SubmitSessionInput(op.ProjectPath, c.SessionID, codexapp.Submission{Text: prompt, RequireIdle: true, ExpectedControlInput: &input})
		next, reason := "delivered", ""
		if deliveryErr != nil {
			next, reason = "suppressed", deliveryErr.Error()
		}
		if _, err := st.TransitionControlContinuation(ctx, c, next, reason); err != nil {
			return err
		}
	}
	return nil
}
