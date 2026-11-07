package agentquery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"lcroom/internal/control"
)

const boardWatchEventLimit = 20

func engineerBoardWatchStatusCapability() Capability {
	return collectionCapability(QueryEngineerBoardWatchStatus, DomainEngineer, ScopeProject,
		"List the owner-board click-to-wake registrations of one visible project with their persisted cursor, last scan, integrity state, counters and recent events. Each event reports its review outcome (state: pending, queued, expired, canceled, reviewed) separately from delivery of its mailbox wake (delivery: pending_enqueue, queued, queued_waiting, delivering, delivered, failed, expired, canceled, not_sent). Registration still waiting for operator confirmation is a control operation: inspect it with get_control_operation. Processing happens only while Little Control Room is running.",
		SensitivityMetadata,
		pagedSchema(map[string]any{
			"project_path":         stringProperty("Exact project path. Defaults to the originating project.", 1),
			"include_unregistered": map[string]any{"type": "boolean", "description": "Also list stopped registrations kept as history."},
			"watch_id":             stringProperty("Optional exact watch id (from the registration receipt) to inspect one registration of the resolved project, in any state, without paging through the project's list. A watch of another project is reported as not found.", 1),
		}, nil))
}

type boardWatchReader interface {
	GetBoardWatch(context.Context, string) (control.BoardWatch, error)
	ListBoardWatches(context.Context, string, bool) ([]control.BoardWatch, error)
	ListBoardWatchEventStatuses(context.Context, string, int) ([]control.BoardWatchEventStatus, error)
}

func (e *Executor) engineerBoardWatchStatus(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args struct {
		ProjectPath         string `json:"project_path"`
		IncludeUnregistered bool   `json:"include_unregistered"`
		WatchID             string `json:"watch_id"`
		Limit               int    `json:"limit"`
		Cursor              string `json:"cursor"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	path, project, err := e.resolveProject(ctx, args.ProjectPath)
	if err != nil {
		return nil, err
	}
	reader, ok := e.reader.(boardWatchReader)
	if !ok {
		return nil, fmt.Errorf("owner board watches are not connected")
	}
	var watches []control.BoardWatch
	if id := strings.TrimSpace(args.WatchID); id != "" {
		// An exact lookup is not subject to the list cap. A watch that belongs
		// to another project is indistinguishable from a missing one.
		watch, err := reader.GetBoardWatch(ctx, id)
		switch {
		case errors.Is(err, sql.ErrNoRows) || err == nil && cleanPath(watch.ProjectPath) != path:
			return nil, fmt.Errorf("board watch %q was not found in this project", id)
		case err != nil:
			return nil, err
		}
		watches = []control.BoardWatch{watch}
	} else {
		var err error
		watches, err = reader.ListBoardWatches(ctx, path, args.IncludeUnregistered)
		if err != nil {
			return nil, err
		}
	}
	now := e.nowFn()
	records := make([]map[string]any, 0, len(watches))
	for _, watch := range watches {
		events, err := reader.ListBoardWatchEventStatuses(ctx, watch.ID, boardWatchEventLimit)
		if err != nil {
			return nil, err
		}
		recent := make([]map[string]any, 0, len(events))
		for _, event := range events {
			record := map[string]any{
				"event_id":    event.EventID,
				"item_id":     event.ItemID,
				"state":       event.State,
				"delivery":    event.Delivery(now),
				"event_time":  formatTime(event.EventCreatedAt),
				"ingested_at": formatTime(event.IngestedAt),
			}
			if event.MessageID != "" {
				record["message_id"] = event.MessageID
				record["attempt_count"] = event.AttemptCount
				record["deadline"] = formatTime(event.MessageExpiresAt)
			}
			if !event.DeliveredAt.IsZero() {
				record["delivered_at"] = formatTime(event.DeliveredAt)
			}
			if event.MessageError != "" {
				record["delivery_note"] = event.MessageError
			}
			if event.Note != "" {
				record["note"] = event.Note
			}
			if !event.ReviewedAt.IsZero() {
				record["reviewed_at"] = formatTime(event.ReviewedAt)
			}
			recent = append(recent, record)
		}
		record := map[string]any{
			"watch_id":          watch.ID,
			"board_id":          watch.BoardID,
			"project_path":      watch.ProjectPath,
			"provider":          watch.Provider,
			"target_session_id": watch.TargetSessionID,
			"outbox_path":       watch.OutboxPath,
			"state":             watch.State,
			"cursor_offset":     watch.CursorOffset,
			"malformed_lines":   watch.MalformedCount,
			"registered_at":     formatTime(watch.CreatedAt),
			"last_scan_at":      formatTime(watch.LastScanAt),
			"recent_events":     recent,
		}
		if watch.LastError != "" {
			record["last_error"] = watch.LastError
		}
		if !watch.UnregisteredAt.IsZero() {
			record["unregistered_at"] = formatTime(watch.UnregisteredAt)
		}
		records = append(records, record)
	}
	result, err := pageResult("board_watches", records, args.Cursor, args.Limit)
	if err != nil {
		return nil, err
	}
	result["project"] = projectReference(project)
	return result, nil
}
