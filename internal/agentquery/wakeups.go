package agentquery

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"lcroom/internal/control"
)

const wakeupScanLimit = 200

func engineerWakeupListCapability() Capability {
	return collectionCapability(QueryEngineerWakeupList, DomainEngineer, ScopeProject,
		"List scheduled wakeups for one visible project that have not been delivered, soonest first, with their target session, missed-after time, and unattended grant. Check this before scheduling to avoid duplicates. Pass a wakeup_id to engineer.cancel_wakeup to withdraw one this session scheduled.",
		SensitivityContent,
		pagedSchema(map[string]any{
			"project_path": stringProperty("Exact project path. Defaults to the originating project.", 1),
		}, nil))
}

func (e *Executor) engineerWakeupList(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args struct {
		ProjectPath string `json:"project_path"`
		Limit       int    `json:"limit"`
		Cursor      string `json:"cursor"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	path, project, err := e.resolveProject(ctx, args.ProjectPath)
	if err != nil {
		return nil, err
	}
	reader, ok := e.reader.(interface {
		ListScheduledEngineerMessages(context.Context, int) ([]control.EngineerMessage, error)
	})
	if !ok {
		return nil, fmt.Errorf("scheduled wakeups are not connected")
	}
	messages, err := reader.ListScheduledEngineerMessages(ctx, wakeupScanLimit)
	if err != nil {
		return nil, err
	}
	now := e.nowFn()
	records := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if cleanPath(message.ProjectPath) != path {
			continue
		}
		records = append(records, wakeupRecord(message, now))
	}
	result, err := pageResult("wakeups", records, args.Cursor, args.Limit)
	if err != nil {
		return nil, err
	}
	result["project"] = projectReference(project)
	return result, nil
}

func wakeupRecord(message control.EngineerMessage, now time.Time) map[string]any {
	grant := control.UnattendedGrant{}
	if message.Unattended != nil {
		grant = *message.Unattended
	}
	state := "scheduled"
	switch {
	case message.State == control.EngineerMessageDelivering:
		state = "delivering"
	case !message.DeliverAt.After(now):
		state = "due_waiting_for_session"
	}
	record := map[string]any{
		"wakeup_id":         message.ID,
		"project_path":      message.ProjectPath,
		"provider":          message.Provider,
		"target_session_id": message.TargetSessionID,
		"deliver_at":        formatTime(message.DeliverAt),
		"expires_at":        formatTime(message.ExpiresAt),
		"state":             state,
		"unattended":        grant,
		"authority":         grant.Summary(),
		"prompt":            message.Prompt,
	}
	if message.OperationID != "" {
		record["operation_id"] = message.OperationID
	}
	if message.LastError != "" {
		record["last_error"] = message.LastError
	}
	return record
}
