package service

import (
	"context"
	"fmt"
	"strings"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

const (
	engineerDispatchCallerPending = "Waiting for the dispatching engineer's provider session identity; the report has not been sent."
	engineerDispatchTodoTextLimit = 240
)

// EngineerDispatchLaunch describes an engineer that was just launched into a
// TODO worktree on behalf of a control operation.
type EngineerDispatchLaunch struct {
	OperationID       string
	TodoID            int64
	TodoText          string
	WorkerProjectPath string
	WorkerProvider    model.SessionSource
	WorkerSessionID   string
}

// EngineerTurnCompletion is a settled embedded turn. Summary is the worker's
// bounded final message; Problem explains a turn that ended without one.
type EngineerTurnCompletion struct {
	ProjectPath string
	Provider    model.SessionSource
	SessionID   string
	Summary     string
	Problem     string
}

// RecordEngineerDispatch gives a launched engineer a return address when an
// embedded session requested the launch. Launches from the operator or Chat
// report false: those surfaces already show engineer completion.
func (s *Service) RecordEngineerDispatch(ctx context.Context, launch EngineerDispatchLaunch) (model.EngineerDispatch, bool, error) {
	if s == nil || s.store == nil || !control.IsExternalOperationID(launch.OperationID) {
		return model.EngineerDispatch{}, false, nil
	}
	operation, err := s.store.GetControlOperation(ctx, launch.OperationID)
	if err != nil {
		return model.EngineerDispatch{}, false, fmt.Errorf("load dispatching operation: %w", err)
	}
	callerProvider := sessionSourceFromControlProvider(control.NormalizeProvider(operation.Provider))
	callerKey := strings.TrimSpace(operation.SessionKey)
	callerPath := strings.TrimSpace(operation.ProjectPath)
	if callerKey == "" || callerPath == "" || callerProvider == model.SessionSourceUnknown {
		return model.EngineerDispatch{}, false, nil
	}
	callerSessionID, err := s.store.ResolveEngineerSession(ctx, callerPath, callerProvider, callerKey)
	if err != nil {
		return model.EngineerDispatch{}, false, fmt.Errorf("resolve dispatching engineer: %w", err)
	}
	dispatch, err := s.store.CreateEngineerDispatch(ctx, model.EngineerDispatch{
		OriginOperationID: operation.ID,
		TodoID:            launch.TodoID,
		TodoText:          launch.TodoText,
		WorkerProjectPath: launch.WorkerProjectPath,
		WorkerProvider:    launch.WorkerProvider,
		WorkerSessionID:   launch.WorkerSessionID,
		CallerProjectPath: callerPath,
		CallerProvider:    callerProvider,
		CallerSessionKey:  callerKey,
		CallerSessionID:   callerSessionID,
	})
	if err != nil {
		return model.EngineerDispatch{}, false, err
	}
	return dispatch, true, nil
}

// RouteEngineerTurnCompletion reports an ended worker turn to the session that
// requested it. It reports false when no request is waiting on this worker.
func (s *Service) RouteEngineerTurnCompletion(ctx context.Context, completion EngineerTurnCompletion) (model.EngineerDispatch, bool, error) {
	if s == nil || s.store == nil {
		return model.EngineerDispatch{}, false, nil
	}
	dispatch, found, err := s.store.FindAwaitingEngineerDispatch(ctx, completion.ProjectPath, completion.Provider, completion.SessionID)
	if err != nil || !found {
		return model.EngineerDispatch{}, false, err
	}
	sessionID := firstNonEmpty(completion.SessionID, dispatch.WorkerSessionID)
	branch := ""
	if summary, err := s.store.GetProjectSummary(ctx, dispatch.WorkerProjectPath, true); err == nil {
		branch = strings.TrimSpace(summary.RepoBranch)
	}
	prompt := engineerDispatchReplyPrompt(dispatch, sessionID, branch, completion)
	dispatch, claimed, err := s.store.MarkEngineerDispatchReplyReady(ctx, dispatch.ID, dispatch.ReplySeq, sessionID, prompt)
	if err != nil || !claimed {
		return dispatch, false, err
	}
	dispatch, err = s.QueueEngineerDispatchReply(ctx, dispatch.ID)
	return dispatch, true, err
}

// QueueEngineerDispatchReply moves a stored reply into the exact caller's
// mailbox. Without a bound caller identity it records why it is waiting.
func (s *Service) QueueEngineerDispatchReply(ctx context.Context, id int64) (model.EngineerDispatch, error) {
	dispatch, err := s.store.GetEngineerDispatch(ctx, id)
	if err != nil || dispatch.ReplyState != model.EngineerDispatchReplyReady {
		return dispatch, err
	}
	dispatch, err = s.store.ResolveEngineerDispatchCaller(ctx, dispatch)
	if err != nil {
		return dispatch, err
	}
	if dispatch.CallerSessionID == "" {
		if dispatch.ReplyError == engineerDispatchCallerPending {
			return dispatch, nil
		}
		return s.store.RecordEngineerDispatchReplyError(ctx, dispatch.ID, dispatch.ReplySeq, engineerDispatchCallerPending)
	}
	created, err := s.store.CreateEngineerMessage(ctx, control.EngineerMessage{
		OperationID:     fmt.Sprintf("engineer-dispatch-reply:%d:%d", dispatch.ID, dispatch.ReplySeq),
		ProjectPath:     dispatch.CallerProjectPath,
		Provider:        agentTaskResultCallbackProvider(dispatch.CallerProvider),
		SessionMode:     control.SessionModeResumeOrNew,
		TargetSessionID: dispatch.CallerSessionID,
		Prompt:          dispatch.ReplyPrompt,
		State:           control.EngineerMessageQueued,
	})
	if err != nil {
		updated, recordErr := s.store.RecordEngineerDispatchReplyError(ctx, dispatch.ID, dispatch.ReplySeq, err.Error())
		if recordErr != nil {
			return dispatch, fmt.Errorf("queue dispatch report: %v; persist report failure: %w", err, recordErr)
		}
		return updated, fmt.Errorf("queue dispatch report: %w", err)
	}
	return s.store.RecordEngineerDispatchReplySent(ctx, dispatch.ID, dispatch.ReplySeq, created.ID)
}

func engineerDispatchReplyPrompt(dispatch model.EngineerDispatch, sessionID, branch string, completion EngineerTurnCompletion) string {
	provider := agentTaskResultCallbackProvider(dispatch.WorkerProvider)
	lines := []string{"An engineer you dispatched through Little Control Room finished the turn you requested."}
	if dispatch.TodoID > 0 {
		todo := fmt.Sprintf("TODO #%d", dispatch.TodoID)
		if text := strings.Join(strings.Fields(dispatch.TodoText), " "); text != "" {
			if truncated := truncateRunes(text, engineerDispatchTodoTextLimit); truncated != text {
				text = strings.TrimSpace(truncated) + "…"
			}
			todo += ": " + text
		}
		lines = append(lines, todo)
	}
	worktree := "Worktree: " + dispatch.WorkerProjectPath
	if branch != "" {
		worktree += " (branch " + branch + ")"
	}
	lines = append(lines, worktree)
	worker := "Worker: " + provider.Label()
	if sessionID != "" {
		worker += " session " + sessionID
	}
	lines = append(lines, worker, "")
	if summary := strings.TrimSpace(completion.Summary); summary != "" {
		lines = append(lines, "Worker's final message:", summary)
	} else {
		ending := "The worker ended its turn without a final message LCR could summarize."
		if problem := strings.TrimSpace(completion.Problem); problem != "" {
			ending += " Last error: " + problem
		}
		lines = append(lines, ending)
	}
	followUp := fmt.Sprintf("To send follow-up work, propose engineer.send_prompt with project_path %s and provider %s", dispatch.WorkerProjectPath, provider)
	if sessionID != "" {
		followUp += " and target_session_id " + sessionID
	}
	lines = append(lines,
		"",
		"Inspect the worktree's commits and diff before relying on this summary. "+followUp+"; LCR reports that turn back to you as well. This notice does not authorize merges, pushes or worktree removal.",
	)
	return strings.Join(lines, "\n")
}

func sessionSourceFromControlProvider(provider control.Provider) model.SessionSource {
	switch provider.Normalized() {
	case control.ProviderCodex:
		return model.SessionSourceCodex
	case control.ProviderOpenCode:
		return model.SessionSourceOpenCode
	case control.ProviderClaudeCode:
		return model.SessionSourceClaudeCode
	case control.ProviderLCAgent:
		return model.SessionSourceLCAgent
	default:
		return model.SessionSourceUnknown
	}
}
