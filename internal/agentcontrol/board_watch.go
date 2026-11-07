package agentcontrol

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"lcroom/internal/boardwatch"
	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

// callerBoardSession returns the provider session id the host has observed for
// this control channel. There is no fallback: an unobserved identity is refused.
func (e *Executor) callerBoardSession(ctx context.Context) (string, error) {
	if e.sessionKey == "" || e.originProjectPath == "" {
		return "", errors.New("this session's identity is unknown, so it cannot bind or stop a board watch")
	}
	id, err := e.store.ResolveEngineerSession(ctx, e.originProjectPath, model.SessionSource(e.provider), e.sessionKey)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", errors.New("the host has not observed this session's provider identity yet, so it cannot bind or stop a board watch; try again after the session's first turn")
	}
	return id, nil
}

// preflightBoardWatchRegister refuses an unauthorized binding at proposal time
// so the agent learns the precise reason before the operator is asked. An
// embedded project-scoped session may register only its own project and its
// own session; the host re-validates everything at execution.
func (e *Executor) preflightBoardWatchRegister(ctx context.Context, invocation control.Invocation) error {
	var input control.EngineerBoardWatchRegisterInput
	if err := json.Unmarshal(invocation.Args, &input); err != nil {
		return err
	}
	if e.scope == control.AuthorityScopeProject {
		if e.originProjectPath == "" || !sameCheckoutPath(input.ProjectPath, e.originProjectPath) {
			return fmt.Errorf("%s with project scope can bind only the calling project; pass project_path %s", control.CapabilityEngineerBoardWatchRegister, e.originProjectPath)
		}
		if control.NormalizeProvider(e.provider) != input.Provider.Normalized() {
			return fmt.Errorf("%s with project scope can bind only the calling session's provider", control.CapabilityEngineerBoardWatchRegister)
		}
		own, err := e.callerBoardSession(ctx)
		if err != nil {
			return err
		}
		if own != input.TargetSessionID {
			return fmt.Errorf("%s with project scope can wake only the calling session; pass target_session_id %s", control.CapabilityEngineerBoardWatchRegister, own)
		}
	}
	if _, err := boardwatch.PrepareRegistration(ctx, e.store, input); err != nil {
		return err
	}
	return nil
}

// unregisterOwnBoardWatch stops a watch without confirmation: it only removes
// authority. A project-scoped session may stop only a watch that currently
// wakes it (directly or through a recorded /handoff successor).
func (e *Executor) unregisterOwnBoardWatch(ctx context.Context, watchID string) (map[string]any, error) {
	watch, err := e.store.GetBoardWatch(ctx, watchID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("board watch %q not found; list watches with the engineer.board_watch_status query", watchID)
	}
	if err != nil {
		return nil, err
	}
	if e.scope == control.AuthorityScopeProject {
		own, err := e.callerBoardSession(ctx)
		if err != nil {
			return nil, err
		}
		current, _, err := e.store.ResolveEngineerSessionSuccessor(ctx, store.EngineerSessionRef{
			ProjectPath: watch.ProjectPath, Provider: model.SessionSource(watch.Provider), SessionID: watch.TargetSessionID,
		})
		if err != nil {
			return nil, err
		}
		if !sameCheckoutPath(watch.ProjectPath, e.originProjectPath) || current.SessionID != own {
			return nil, errors.New("only the session this watch wakes can unregister it; ask the operator otherwise")
		}
	}
	updated, withdrawn, err := e.store.UnregisterBoardWatch(ctx, watch.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"success": true, "terminal": true, "operator_confirmation": false, "requires_new_user_turn": false,
		"watch_id": updated.ID, "state": updated.State, "withdrawn_wakes": len(withdrawn),
		"message": "Board watch unregistered. Nothing is read or sent for it again; undelivered review wakes were withdrawn. Wakes already delivered cannot be recalled.",
	}, nil
}
