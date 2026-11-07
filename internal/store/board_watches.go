package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

// ErrBoardWatchCursorMoved reports that the watch was unregistered, put in
// attention, or advanced by another writer since the caller read its cursor.
var ErrBoardWatchCursorMoved = errors.New("board watch cursor or state changed")

const boardWatchSelect = `
	SELECT id, board_id, project_path, provider, requested_target_session_id, target_session_id, outbox_path,
		state, cursor_offset, prefix_len, prefix_hash, malformed_count, last_error, last_scan_at,
		created_at, updated_at, unregistered_at
	FROM board_watches
`

type boardWatchScanner interface{ Scan(dest ...any) error }

func scanBoardWatch(row boardWatchScanner) (control.BoardWatch, error) {
	var w control.BoardWatch
	var provider, state string
	var lastScan, created, updated, unregistered int64
	if err := row.Scan(&w.ID, &w.BoardID, &w.ProjectPath, &provider, &w.RequestedTargetSessionID, &w.TargetSessionID, &w.OutboxPath,
		&state, &w.CursorOffset, &w.PrefixLen, &w.PrefixHash, &w.MalformedCount, &w.LastError, &lastScan,
		&created, &updated, &unregistered); err != nil {
		return control.BoardWatch{}, err
	}
	w.Provider = control.Provider(provider)
	w.State = control.BoardWatchState(state)
	w.LastScanAt = unixTime(lastScan)
	w.CreatedAt = unixTime(created)
	w.UpdatedAt = unixTime(updated)
	w.UnregisteredAt = unixTime(unregistered)
	return w, nil
}

func unixTime(v int64) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	return time.Unix(v, 0)
}

func (s *Store) GetBoardWatch(ctx context.Context, id string) (control.BoardWatch, error) {
	if s == nil || s.db == nil {
		return control.BoardWatch{}, errors.New("store unavailable")
	}
	return scanBoardWatch(s.db.QueryRowContext(ctx, boardWatchSelect+` WHERE id = ?`, strings.TrimSpace(id)))
}

// ListBoardWatches returns registrations for display, optionally limited to one
// project, newest first and capped at 100 rows. It is never an execution
// source: polling uses ListScanningBoardWatchesAfter, and an exact watch is
// read with GetBoardWatch. Unregistered rows are history and appear only on
// request.
func (s *Store) ListBoardWatches(ctx context.Context, projectPath string, includeUnregistered bool) ([]control.BoardWatch, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store unavailable")
	}
	query := boardWatchSelect + ` WHERE 1 = 1`
	var args []any
	if path := strings.TrimSpace(projectPath); path != "" {
		query += ` AND project_path = ?`
		args = append(args, filepath.Clean(path))
	}
	if !includeUnregistered {
		query += ` AND state <> ?`
		args = append(args, string(control.BoardWatchUnregistered))
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT 100`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list board watches: %w", err)
	}
	defer rows.Close()
	var out []control.BoardWatch
	for rows.Next() {
		w, err := scanBoardWatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ListScanningBoardWatchesAfter returns up to limit active watches whose id is
// greater than afterID, in id order. Polling pages through it until it is
// exhausted, so execution never depends on the display limit of
// ListBoardWatches.
func (s *Store) ListScanningBoardWatchesAfter(ctx context.Context, afterID string, limit int) ([]control.BoardWatch, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store unavailable")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, boardWatchSelect+` WHERE state = ? AND id > ? ORDER BY id ASC LIMIT ?`,
		string(control.BoardWatchActive), afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list scanning board watches: %w", err)
	}
	defer rows.Close()
	var out []control.BoardWatch
	for rows.Next() {
		w, err := scanBoardWatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// RegisterBoardWatch records an opt-in. The caller has already validated the
// project, the exact session and the outbox, and supplies the baseline cursor
// (the outbox's current end). An identical active registration is returned
// unchanged, so a repeated confirmation is idempotent. A different active
// registration for the same project and board must be unregistered first. A
// registration in attention is replaced, which starts a new baseline.
func (s *Store) RegisterBoardWatch(ctx context.Context, watch control.BoardWatch) (control.BoardWatch, bool, error) {
	if s == nil || s.db == nil {
		return control.BoardWatch{}, false, errors.New("store unavailable")
	}
	watch.BoardID = strings.TrimSpace(watch.BoardID)
	watch.ProjectPath = filepath.Clean(strings.TrimSpace(watch.ProjectPath))
	watch.Provider = watch.Provider.Normalized()
	watch.TargetSessionID = strings.TrimSpace(watch.TargetSessionID)
	watch.OutboxPath = filepath.Clean(strings.TrimSpace(watch.OutboxPath))
	if !control.ValidBoardIdentifier(watch.BoardID) || watch.ProjectPath == "" || watch.OutboxPath == "" ||
		watch.TargetSessionID == "" || watch.Provider == "" || watch.Provider == control.ProviderAuto || watch.CursorOffset < 0 {
		return control.BoardWatch{}, false, errors.New("board watch needs a valid board, project, explicit provider, exact session, outbox and baseline")
	}
	watch.RequestedTargetSessionID = watch.TargetSessionID
	current, pending, err := s.ResolveEngineerSessionSuccessor(ctx, EngineerSessionRef{
		ProjectPath: watch.ProjectPath, Provider: model.SessionSource(watch.Provider), SessionID: watch.TargetSessionID,
	})
	if err != nil {
		return control.BoardWatch{}, false, fmt.Errorf("resolve engineer session successor: %w", err)
	}
	if pending {
		return control.BoardWatch{}, false, errors.New("the target session is being replaced through /handoff; register again once its successor is running")
	}
	if current.SessionID != watch.TargetSessionID || current.ProjectPath != watch.ProjectPath || current.Provider != model.SessionSource(watch.Provider) {
		return control.BoardWatch{}, false, fmt.Errorf("the target session was replaced through /handoff; register the successor session %s", current.SessionID)
	}
	id, err := control.NewBoardWatchID()
	if err != nil {
		return control.BoardWatch{}, false, err
	}
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return control.BoardWatch{}, false, err
	}
	defer tx.Rollback()
	existing, err := scanBoardWatch(tx.QueryRowContext(ctx, boardWatchSelect+` WHERE project_path = ? AND board_id = ? AND state IN ('active', 'attention')`,
		watch.ProjectPath, watch.BoardID))
	switch {
	case err == nil:
		same := existing.OutboxPath == watch.OutboxPath && existing.Provider == watch.Provider && existing.TargetSessionID == watch.TargetSessionID
		if existing.State == control.BoardWatchActive && same {
			return existing, false, nil
		}
		if existing.State == control.BoardWatchActive {
			return control.BoardWatch{}, false, fmt.Errorf("board %s is already registered as %s with a different outbox or session; unregister it first", watch.BoardID, existing.ID)
		}
		if err := unregisterBoardWatchTx(ctx, tx, existing.ID, "replaced by a new registration", now); err != nil {
			return control.BoardWatch{}, false, err
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return control.BoardWatch{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO board_watches(
			id, board_id, project_path, provider, requested_target_session_id, target_session_id, outbox_path, state,
			cursor_offset, prefix_len, prefix_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, watch.BoardID, watch.ProjectPath, string(watch.Provider), watch.RequestedTargetSessionID, watch.TargetSessionID, watch.OutboxPath,
		string(control.BoardWatchActive), watch.CursorOffset, watch.PrefixLen, watch.PrefixHash, now.Unix(), now.Unix()); err != nil {
		return control.BoardWatch{}, false, fmt.Errorf("register board watch: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return control.BoardWatch{}, false, err
	}
	created, err := s.GetBoardWatch(ctx, id)
	return created, err == nil, err
}

func unregisterBoardWatchTx(ctx context.Context, tx *sql.Tx, id, note string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE board_watches SET state = ?, unregistered_at = ?, updated_at = ? WHERE id = ?`,
		string(control.BoardWatchUnregistered), now.Unix(), now.Unix(), id); err != nil {
		return fmt.Errorf("unregister board watch: %w", err)
	}
	// Pending and queued events are canceled only while their wake is still
	// waiting in the mailbox (or was never created); a wake that is delivering
	// or delivered keeps its record. The message is found by its deterministic
	// operation id, not by the event's link, so a wake created just before a
	// crash and never linked is covered too.
	if _, err := tx.ExecContext(ctx, `UPDATE board_watch_events SET state = ?, note = ?
		WHERE watch_id = ? AND state IN (?, ?) AND NOT EXISTS (
			SELECT 1 FROM engineer_messages m
			WHERE m.operation_id = `+boardWatchOperationIDSQL("board_watch_events")+` AND m.state <> ?)`,
		string(control.BoardWatchEventCanceled), note, id, string(control.BoardWatchEventPending),
		string(control.BoardWatchEventQueued), string(control.EngineerMessageQueued)); err != nil {
		return fmt.Errorf("cancel board watch events: %w", err)
	}
	return nil
}

// UnregisterBoardWatch is the explicit stop boundary. It is idempotent. Events
// not yet queued are canceled, and review wakes still waiting in the mailbox
// are withdrawn; a wake that already reached the director cannot be recalled.
func (s *Store) UnregisterBoardWatch(ctx context.Context, id string) (control.BoardWatch, []string, error) {
	if s == nil || s.db == nil {
		return control.BoardWatch{}, nil, errors.New("store unavailable")
	}
	id = strings.TrimSpace(id)
	watch, err := s.GetBoardWatch(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return control.BoardWatch{}, nil, fmt.Errorf("board watch %q not found", id)
		}
		return control.BoardWatch{}, nil, err
	}
	if watch.State != control.BoardWatchUnregistered {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return control.BoardWatch{}, nil, err
		}
		defer tx.Rollback()
		if err := unregisterBoardWatchTx(ctx, tx, id, "board watch unregistered", time.Now()); err != nil {
			return control.BoardWatch{}, nil, err
		}
		if err := tx.Commit(); err != nil {
			return control.BoardWatch{}, nil, err
		}
	}
	withdrawn, err := s.withdrawQueuedBoardWatchMessages(ctx, id, "Board watch unregistered; this review wake was withdrawn.")
	if err != nil {
		return control.BoardWatch{}, withdrawn, err
	}
	watch, err = s.GetBoardWatch(ctx, id)
	return watch, withdrawn, err
}

// boardWatchOperationIDSQL builds control.BoardReviewOperationID in SQL for the
// event table alias, so wakes are matched to events by identity and not by a
// link that a crash may have left unwritten.
func boardWatchOperationIDSQL(alias string) string {
	return `('board-review:' || ` + alias + `.watch_id || ':' || ` + alias + `.event_id)`
}

// withdrawQueuedBoardWatchMessages fails every still-queued mailbox wake whose
// event is canceled, reviewed or expired, including those of unregistered
// watches. Matching is by deterministic operation id, so it also finds a wake
// created before a crash that never got linked to its event, and a wake whose
// event reached a terminal state before the host could withdraw it. It is safe
// to repeat and is the crash-recovery sweep of every poll.
func (s *Store) withdrawQueuedBoardWatchMessages(ctx context.Context, watchID, reason string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.id FROM engineer_messages m
		WHERE m.state = ? AND m.operation_id >= 'board-review:' AND m.operation_id < 'board-review;'
		AND EXISTS (SELECT 1 FROM board_watch_events e
			WHERE `+boardWatchOperationIDSQL("e")+` = m.operation_id AND (? = '' OR e.watch_id = ?)
			AND e.state IN (?, ?, ?))`,
		string(control.EngineerMessageQueued), watchID, watchID,
		string(control.BoardWatchEventCanceled), string(control.BoardWatchEventReviewed), string(control.BoardWatchEventExpired))
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var withdrawn []string
	for _, id := range ids {
		ok, err := s.failQueuedEngineerMessage(ctx, id, reason)
		if err != nil {
			return withdrawn, err
		}
		if ok {
			withdrawn = append(withdrawn, id)
		}
	}
	return withdrawn, nil
}

// failQueuedEngineerMessage withdraws a message only while it is still queued.
// The conditional update loses cleanly to a concurrent delivery claim.
func (s *Store) failQueuedEngineerMessage(ctx context.Context, id, reason string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE engineer_messages SET state = ?, last_error = ?, updated_at = ?
		WHERE id = ? AND state = ?`,
		string(control.EngineerMessageFailed), reason, time.Now().Unix(), id, string(control.EngineerMessageQueued))
	if err != nil {
		return false, fmt.Errorf("withdraw engineer message: %w", err)
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

// WithdrawCanceledBoardWatchMessages is the crash-recovery sweep run by every
// poll: any message still queued for a canceled event is withdrawn.
func (s *Store) WithdrawCanceledBoardWatchMessages(ctx context.Context) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store unavailable")
	}
	return s.withdrawQueuedBoardWatchMessages(ctx, "", "Review wake withdrawn: the request was canceled, already reviewed, or its board watch was unregistered.")
}

// BoardWatchIngest is one atomic step: events read from the outbox, the new
// cursor, and the counters. Either all of it is stored or none of it is.
type BoardWatchIngest struct {
	WatchID        string
	ExpectedOffset int64
	NewOffset      int64
	PrefixLen      int
	PrefixHash     string
	Events         []control.BoardReviewEvent
	Malformed      int64
	LastError      string
	Now            time.Time
}

type BoardWatchIngestResult struct {
	Inserted   int
	Duplicates int
	Expired    int
	Canceled   int
	Reviewed   int
	Withdrawn  int
}

// IngestBoardWatchBatch persists parsed events and advances the cursor in one
// transaction guarded by the previous offset. A crash before the commit
// re-reads the same bytes; after it, the events are durable and the mailbox
// message is created from them idempotently.
func (s *Store) IngestBoardWatchBatch(ctx context.Context, in BoardWatchIngest) (BoardWatchIngestResult, error) {
	var result BoardWatchIngestResult
	if s == nil || s.db == nil {
		return result, errors.New("store unavailable")
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	moved, err := tx.ExecContext(ctx, `UPDATE board_watches
		SET cursor_offset = ?, prefix_len = ?, prefix_hash = ?, malformed_count = malformed_count + ?,
			last_error = ?, last_scan_at = ?, updated_at = ?
		WHERE id = ? AND state = ? AND cursor_offset = ?`,
		in.NewOffset, in.PrefixLen, in.PrefixHash, in.Malformed, in.LastError, now.Unix(), now.Unix(),
		in.WatchID, string(control.BoardWatchActive), in.ExpectedOffset)
	if err != nil {
		return result, fmt.Errorf("advance board watch cursor: %w", err)
	}
	if changed, err := moved.RowsAffected(); err != nil || changed != 1 {
		if err != nil {
			return result, err
		}
		return result, ErrBoardWatchCursorMoved
	}
	for _, event := range in.Events {
		switch event.Kind {
		case control.BoardReviewRequested:
			state, note := control.BoardWatchEventPending, ""
			if now.After(event.CreatedAt.Add(control.BoardReviewDeliveryDeadline)) {
				state, note = control.BoardWatchEventExpired, "older than the 24-hour delivery deadline when read"
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO board_watch_events(watch_id, event_id, item_id, event_created_at, ingested_at, state, note)
				VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(watch_id, event_id) DO NOTHING`,
				in.WatchID, event.EventID, event.ItemID, event.CreatedAt.Unix(), now.Unix(), string(state), note)
			if err != nil {
				return result, fmt.Errorf("store board watch event: %w", err)
			}
			if n, _ := res.RowsAffected(); n == 1 {
				if state == control.BoardWatchEventExpired {
					result.Expired++
				} else {
					result.Inserted++
				}
			} else {
				result.Duplicates++
			}
		case control.BoardReviewCanceled, control.BoardReviewResolved:
			target := control.BoardWatchEventCanceled
			note := "withdrawn by the board before delivery"
			if event.Kind == control.BoardReviewResolved {
				target, note = control.BoardWatchEventReviewed, "marked reviewed by the board"
			}
			var state string
			err := tx.QueryRowContext(ctx, `SELECT state FROM board_watch_events WHERE watch_id = ? AND event_id = ?`,
				in.WatchID, event.TargetEventID).Scan(&state)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				// A tombstone: a request carrying this id that is read later is a duplicate.
				if _, err := tx.ExecContext(ctx, `INSERT INTO board_watch_events(watch_id, event_id, item_id, event_created_at, ingested_at, state, note, reviewed_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
					in.WatchID, event.TargetEventID, event.ItemID, event.CreatedAt.Unix(), now.Unix(), string(target), note, reviewedAtFor(target, now)); err != nil {
					return result, fmt.Errorf("store board watch tombstone: %w", err)
				}
			case err != nil:
				return result, err
			case state == string(control.BoardWatchEventPending) || state == string(control.BoardWatchEventQueued):
				if target == control.BoardWatchEventCanceled {
					// Too late to withdraw once the wake is delivering or delivered.
					// The wake is found by operation id, so an unlinked one counts.
					var messageState string
					err := tx.QueryRowContext(ctx, `SELECT state FROM engineer_messages WHERE operation_id = ?`,
						control.BoardReviewOperationID(in.WatchID, event.TargetEventID)).Scan(&messageState)
					if err != nil && !errors.Is(err, sql.ErrNoRows) {
						return result, err
					}
					if err == nil && messageState != string(control.EngineerMessageQueued) {
						continue
					}
				}
				if _, err := tx.ExecContext(ctx, `UPDATE board_watch_events SET state = ?, note = ?, reviewed_at = ? WHERE watch_id = ? AND event_id = ?`,
					string(target), note, reviewedAtFor(target, now), in.WatchID, event.TargetEventID); err != nil {
					return result, fmt.Errorf("update board watch event: %w", err)
				}
			default:
				continue
			}
			if target == control.BoardWatchEventCanceled {
				result.Canceled++
			} else {
				result.Reviewed++
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	// A canceled or reviewed request no longer needs its undelivered wake. The
	// sweep is idempotent and also runs on every poll, so a crash right after
	// the commit above is repaired by the next poll.
	withdrawn, err := s.withdrawQueuedBoardWatchMessages(ctx, in.WatchID, "Review wake withdrawn: the board reported the request canceled or already reviewed.")
	result.Withdrawn = len(withdrawn)
	return result, err
}

func reviewedAtFor(state control.BoardWatchEventState, now time.Time) int64 {
	if state == control.BoardWatchEventReviewed {
		return now.Unix()
	}
	return 0
}

// SetBoardWatchAttention stops scanning after an integrity problem. Nothing is
// guessed or reset; the operator or director re-registers to continue.
func (s *Store) SetBoardWatchAttention(ctx context.Context, id, reason string) error {
	if s == nil || s.db == nil {
		return errors.New("store unavailable")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE board_watches SET state = ?, last_error = ?, updated_at = ?, last_scan_at = ?
		WHERE id = ? AND state = ?`,
		string(control.BoardWatchAttention), reason, time.Now().Unix(), time.Now().Unix(), id, string(control.BoardWatchActive))
	return err
}

// RecordBoardWatchScanError notes a transient problem without stopping the watch.
func (s *Store) RecordBoardWatchScanError(ctx context.Context, id, reason string, now time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("store unavailable")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE board_watches SET last_error = ?, last_scan_at = ?, updated_at = ? WHERE id = ? AND state = ?`,
		reason, now.Unix(), now.Unix(), id, string(control.BoardWatchActive))
	return err
}

type PendingBoardWatchEvent struct {
	Watch control.BoardWatch
	Event control.BoardWatchEvent
}

// PendingBoardWatchEvents lists stored events of active watches that still
// need their mailbox message. This is the restart catch-up for the gap between
// reading an event and queueing it.
func (s *Store) PendingBoardWatchEvents(ctx context.Context, limit int) ([]PendingBoardWatchEvent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store unavailable")
	}
	if limit <= 0 {
		limit = 32
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.watch_id, e.event_id, e.item_id, e.event_created_at, e.ingested_at FROM board_watch_events e
		JOIN board_watches w ON w.id = e.watch_id
		WHERE e.state = ? AND w.state = ? ORDER BY e.ingested_at ASC, e.event_id ASC LIMIT ?`,
		string(control.BoardWatchEventPending), string(control.BoardWatchActive), limit)
	if err != nil {
		return nil, err
	}
	var keys []PendingBoardWatchEvent
	for rows.Next() {
		var ev control.BoardWatchEvent
		var created, ingested int64
		if err := rows.Scan(&ev.WatchID, &ev.EventID, &ev.ItemID, &created, &ingested); err != nil {
			rows.Close()
			return nil, err
		}
		ev.EventCreatedAt, ev.IngestedAt, ev.State = unixTime(created), unixTime(ingested), control.BoardWatchEventPending
		keys = append(keys, PendingBoardWatchEvent{Event: ev})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range keys {
		keys[i].Watch, err = s.GetBoardWatch(ctx, keys[i].Event.WatchID)
		if err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// MarkBoardWatchEventQueued links the mailbox message. It reports true when the
// event is queued with exactly this message, including when another poll
// linked the same deterministic message first. It reports false when the event
// reached a terminal state meanwhile; the caller then withdraws the message.
func (s *Store) MarkBoardWatchEventQueued(ctx context.Context, watchID, eventID, messageID string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("store unavailable")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE board_watch_events SET state = ?, message_id = ?
		WHERE watch_id = ? AND event_id = ? AND state = ?`,
		string(control.BoardWatchEventQueued), messageID, watchID, eventID, string(control.BoardWatchEventPending))
	if err != nil {
		return false, err
	}
	if changed, err := result.RowsAffected(); err != nil || changed == 1 {
		return changed == 1, err
	}
	var state, linked string
	if err := s.db.QueryRowContext(ctx, `SELECT state, message_id FROM board_watch_events WHERE watch_id = ? AND event_id = ?`,
		watchID, eventID).Scan(&state, &linked); err != nil {
		return false, err
	}
	return state == string(control.BoardWatchEventQueued) && linked == messageID, nil
}

// ExpireBoardWatchEvent settles a pending event whose deadline passed before a
// message could be queued.
func (s *Store) ExpireBoardWatchEvent(ctx context.Context, watchID, eventID, note string) error {
	if s == nil || s.db == nil {
		return errors.New("store unavailable")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE board_watch_events SET state = ?, note = ? WHERE watch_id = ? AND event_id = ? AND state = ?`,
		string(control.BoardWatchEventExpired), note, watchID, eventID, string(control.BoardWatchEventPending))
	return err
}

// FailQueuedEngineerMessage exposes the conditional withdrawal to the poller.
func (s *Store) FailQueuedEngineerMessage(ctx context.Context, id, reason string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("store unavailable")
	}
	return s.failQueuedEngineerMessage(ctx, id, reason)
}

// ListBoardWatchEventStatuses returns the newest events of one watch with the
// progress of their mailbox messages. Wakes are matched by deterministic
// operation id, so one created just before a crash and not yet linked still
// reports its real delivery.
func (s *Store) ListBoardWatchEventStatuses(ctx context.Context, watchID string, limit int) ([]control.BoardWatchEventStatus, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store unavailable")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.watch_id, e.event_id, e.item_id, e.event_created_at, e.ingested_at, e.state, COALESCE(m.id, e.message_id), e.note, e.reviewed_at,
			COALESCE(m.state, ''), COALESCE(m.last_error, ''), COALESCE(m.expires_at, 0), COALESCE(m.delivered_at, 0), COALESCE(m.attempt_count, 0)
		FROM board_watch_events e LEFT JOIN engineer_messages m ON m.operation_id = `+boardWatchOperationIDSQL("e")+`
		WHERE e.watch_id = ? ORDER BY e.ingested_at DESC, e.event_id DESC LIMIT ?`, strings.TrimSpace(watchID), limit)
	if err != nil {
		return nil, fmt.Errorf("list board watch events: %w", err)
	}
	defer rows.Close()
	var out []control.BoardWatchEventStatus
	for rows.Next() {
		var st control.BoardWatchEventStatus
		var created, ingested, reviewed, expires, delivered int64
		var state, messageState string
		if err := rows.Scan(&st.WatchID, &st.EventID, &st.ItemID, &created, &ingested, &state, &st.MessageID, &st.Note, &reviewed,
			&messageState, &st.MessageError, &expires, &delivered, &st.AttemptCount); err != nil {
			return nil, err
		}
		st.EventCreatedAt, st.IngestedAt, st.ReviewedAt = unixTime(created), unixTime(ingested), unixTime(reviewed)
		st.State = control.BoardWatchEventState(state)
		st.MessageState = control.EngineerMessageState(messageState)
		st.MessageExpiresAt, st.DeliveredAt = unixTime(expires), unixTime(delivered)
		out = append(out, st)
	}
	return out, rows.Err()
}
