// Package boardwatch implements durable owner-board click-to-wake.
//
// A registered watch names exactly one JSON Lines outbox file, one project and
// one engineer session. Poll reads only that file, from a persisted byte
// cursor, accepts only strictly typed explicit review events, stores them, and
// queues one ordinary engineer-mailbox message per event. Everything slow runs
// in a tea.Cmd or background caller; nothing here touches the UI thread.
package boardwatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/store"
)

const (
	// maxReadBytes bounds one scan of one outbox.
	maxReadBytes = 64 * 1024
	// maxEventsPerScan bounds the events stored by one scan of one outbox.
	maxEventsPerScan = 100
	// prefixBytes is how much of the file head fingerprints it, so a replaced
	// or rewritten outbox is noticed instead of being read from a stale cursor.
	prefixBytes = 256
	// OutboxSuffix is required of every registered outbox file name.
	OutboxSuffix = ".jsonl"
	// watchPageSize is how many registrations one retrieval page returns.
	watchPageSize = 50
	// enqueueBatch bounds the mailbox messages queued per poll.
	enqueueBatch = 32
)

// Report counts what one Poll did.
type Report struct {
	Watches    int
	Ingested   int
	Duplicates int
	Expired    int
	Malformed  int64
	Canceled   int
	Reviewed   int
	Enqueued   int
	Withdrawn  int
	Errors     []error
}

// CheckOutbox verifies that outboxPath may be watched for projectPath and
// returns its resolved path. The file may not exist yet. Containment is
// decided on real paths, a symlink outbox is refused, and only a regular
// *.jsonl file outside any .git directory is accepted, so a watch cannot be
// aimed at arbitrary files.
func CheckOutbox(projectPath, outboxPath string) (string, error) {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	outboxPath = filepath.Clean(strings.TrimSpace(outboxPath))
	if !filepath.IsAbs(projectPath) || !filepath.IsAbs(outboxPath) {
		return "", errors.New("project and outbox paths must be absolute")
	}
	if !strings.HasSuffix(filepath.Base(outboxPath), OutboxSuffix) || filepath.Base(outboxPath) == OutboxSuffix {
		return "", fmt.Errorf("the outbox must be a named %s file", OutboxSuffix)
	}
	realProject, err := filepath.EvalSymlinks(projectPath)
	if err != nil {
		return "", fmt.Errorf("project path is not readable: %w", err)
	}
	if info, err := os.Stat(realProject); err != nil || !info.IsDir() {
		return "", errors.New("project path is not a directory")
	}
	realParent, err := filepath.EvalSymlinks(filepath.Dir(outboxPath))
	if err != nil {
		return "", fmt.Errorf("the outbox directory must already exist inside the project: %w", err)
	}
	real := filepath.Join(realParent, filepath.Base(outboxPath))
	rel, err := filepath.Rel(realProject, real)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("the outbox must be inside the project")
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == ".git" {
			return "", errors.New("the outbox must not be inside a .git directory")
		}
	}
	info, err := os.Lstat(real)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return "", fmt.Errorf("outbox is not readable: %w", err)
	case info.Mode()&os.ModeSymlink != 0:
		return "", errors.New("the outbox must not be a symlink")
	case !info.Mode().IsRegular():
		return "", errors.New("the outbox must be a regular file")
	}
	return real, nil
}

// PrepareRegistration validates a registration request against the store and
// the filesystem and returns the watch to persist, with its baseline cursor at
// the outbox's current end: events written before registration never wake
// anyone, so a stale saved click cannot revive earlier work.
func PrepareRegistration(ctx context.Context, st *store.Store, in control.EngineerBoardWatchRegisterInput) (control.BoardWatch, error) {
	in, err := control.NormalizeEngineerBoardWatchRegisterInput(in)
	if err != nil {
		return control.BoardWatch{}, err
	}
	if st == nil {
		return control.BoardWatch{}, errors.New("store unavailable")
	}
	if _, err := st.GetProjectSummary(ctx, in.ProjectPath, false); err != nil {
		return control.BoardWatch{}, fmt.Errorf("project %s is not a tracked project: %w", in.ProjectPath, err)
	}
	real, err := CheckOutbox(in.ProjectPath, in.OutboxPath)
	if err != nil {
		return control.BoardWatch{}, err
	}
	watch := control.BoardWatch{
		BoardID: in.BoardID, ProjectPath: in.ProjectPath, Provider: in.Provider,
		TargetSessionID: in.TargetSessionID, OutboxPath: in.OutboxPath,
	}
	if info, err := os.Stat(real); err == nil {
		watch.CursorOffset = info.Size()
		watch.PrefixLen, watch.PrefixHash, err = fingerprint(real, info.Size())
		if err != nil {
			return control.BoardWatch{}, err
		}
	}
	return watch, nil
}

func fingerprint(path string, offset int64) (int, string, error) {
	n := prefixBytes
	if offset < int64(n) {
		n = int(offset)
	}
	if n <= 0 {
		return 0, "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	buf := make([]byte, n)
	if _, err := io.ReadFull(io.NewSectionReader(f, 0, int64(n)), buf); err != nil {
		return 0, "", fmt.Errorf("outbox changed while reading: %w", err)
	}
	sum := sha256.Sum256(buf)
	return n, hex.EncodeToString(sum[:]), nil
}

// Poll scans every active watch, then sweeps crash leftovers and queues
// mailbox messages for stored events. It is safe to call repeatedly and after
// a restart: every step is idempotent and ordered so that a crash between any
// two of them is repaired by the next call.
func Poll(ctx context.Context, st *store.Store, now time.Time) Report {
	var report Report
	if st == nil {
		report.Errors = append(report.Errors, errors.New("store unavailable"))
		return report
	}
	// Every active watch is scanned, in id order and in bounded pages. Each
	// scan reads at most maxReadBytes, so a poll's work grows only with the
	// number of registrations, never with the size of any outbox.
	afterID := ""
	for ctx.Err() == nil {
		watches, err := st.ListScanningBoardWatchesAfter(ctx, afterID, watchPageSize)
		if err != nil {
			report.Errors = append(report.Errors, err)
			break
		}
		for _, watch := range watches {
			afterID = watch.ID
			report.Watches++
			scanWatch(ctx, st, watch, now, &report)
		}
		if len(watches) < watchPageSize {
			break
		}
	}
	if withdrawn, err := st.WithdrawCanceledBoardWatchMessages(ctx); err != nil {
		report.Errors = append(report.Errors, err)
	} else {
		report.Withdrawn += len(withdrawn)
	}
	enqueuePending(ctx, st, now, &report)
	return report
}

func scanWatch(ctx context.Context, st *store.Store, watch control.BoardWatch, now time.Time, report *Report) {
	fail := func(err error) {
		report.Errors = append(report.Errors, fmt.Errorf("board watch %s: %w", watch.ID, err))
		_ = st.RecordBoardWatchScanError(ctx, watch.ID, err.Error(), now)
	}
	attention := func(reason string) {
		report.Errors = append(report.Errors, fmt.Errorf("board watch %s needs attention: %s", watch.ID, reason))
		_ = st.SetBoardWatchAttention(ctx, watch.ID, reason)
	}
	real, err := CheckOutbox(watch.ProjectPath, watch.OutboxPath)
	if err != nil {
		attention("outbox is no longer an acceptable file: " + err.Error() + "; unregister and register again")
		return
	}
	before, err := os.Lstat(real)
	if errors.Is(err, os.ErrNotExist) {
		if watch.CursorOffset > 0 {
			attention("the outbox disappeared after events were read; unregister and register again")
		}
		return
	}
	if err != nil {
		fail(err)
		return
	}
	f, err := os.Open(real)
	if err != nil {
		fail(err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		fail(err)
		return
	}
	if !os.SameFile(before, info) {
		fail(errors.New("outbox changed while opening; retrying"))
		return
	}
	if info.Size() < watch.CursorOffset {
		attention("the outbox is shorter than the saved cursor (truncated or replaced); unregister and register again")
		return
	}
	if watch.PrefixLen > 0 {
		n, hash, err := fingerprint(real, int64(watch.PrefixLen))
		if err != nil || n != watch.PrefixLen || hash != watch.PrefixHash {
			attention("the outbox start no longer matches what was read (rewritten or replaced); unregister and register again")
			return
		}
	}
	if info.Size() == watch.CursorOffset {
		if watch.LastError != "" {
			_ = st.RecordBoardWatchScanError(ctx, watch.ID, "", now)
		}
		return
	}
	window := info.Size() - watch.CursorOffset
	if window > maxReadBytes {
		window = maxReadBytes
	}
	buf := make([]byte, window)
	if _, err := io.ReadFull(io.NewSectionReader(f, watch.CursorOffset, window), buf); err != nil {
		fail(fmt.Errorf("read outbox: %w", err))
		return
	}
	var events []control.BoardReviewEvent
	var malformed int64
	lastError := ""
	consumed := 0
	for consumed < len(buf) && len(events) < maxEventsPerScan {
		newline := indexByte(buf[consumed:], '\n')
		if newline < 0 {
			if len(buf)-consumed >= control.BoardReviewMaxLineBytes {
				attention("an outbox line exceeds the size limit; unregister and register again")
				return
			}
			break // a partial line: the writer is mid-append
		}
		line := buf[consumed : consumed+newline]
		consumed += newline + 1
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		event, err := control.ParseBoardReviewEvent(line, watch.BoardID, now)
		if err != nil {
			malformed++
			lastError = "ignored a malformed outbox line: " + err.Error()
			continue
		}
		events = append(events, event)
	}
	if consumed == 0 {
		return
	}
	newOffset := watch.CursorOffset + int64(consumed)
	prefixLen, prefixHash, err := fingerprint(real, newOffset)
	if err != nil {
		fail(err)
		return
	}
	result, err := st.IngestBoardWatchBatch(ctx, store.BoardWatchIngest{
		WatchID: watch.ID, ExpectedOffset: watch.CursorOffset, NewOffset: newOffset,
		PrefixLen: prefixLen, PrefixHash: prefixHash, Events: events, Malformed: malformed, LastError: lastError, Now: now,
	})
	if err != nil {
		if !errors.Is(err, store.ErrBoardWatchCursorMoved) {
			fail(err)
		}
		return
	}
	report.Ingested += result.Inserted
	report.Duplicates += result.Duplicates
	report.Expired += result.Expired
	report.Canceled += result.Canceled
	report.Reviewed += result.Reviewed
	report.Malformed += malformed
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

// enqueuePending queues the mailbox message of every stored event that has
// none. The operation id is derived from watch and event, so a retry after an
// ambiguous failure or a crash returns the same message instead of a second.
func enqueuePending(ctx context.Context, st *store.Store, now time.Time, report *Report) {
	pending, err := st.PendingBoardWatchEvents(ctx, enqueueBatch)
	if err != nil {
		report.Errors = append(report.Errors, err)
		return
	}
	enqueueEvents(ctx, st, pending, now, report)
}

// enqueueEvents works from a possibly stale list: another poll may already
// have linked or settled any of these events. Creating the message is
// idempotent by operation id, and a link that is already in place for the same
// message is success, so a second pass never cancels a valid wake.
func enqueueEvents(ctx context.Context, st *store.Store, pending []store.PendingBoardWatchEvent, now time.Time, report *Report) {
	for _, item := range pending {
		watch, event := item.Watch, item.Event
		deadline := event.EventCreatedAt.Add(control.BoardReviewDeliveryDeadline)
		if !now.Before(deadline) {
			if err := st.ExpireBoardWatchEvent(ctx, watch.ID, event.EventID, "delivery deadline passed before the wake could be queued"); err != nil {
				report.Errors = append(report.Errors, err)
			} else {
				report.Expired++
			}
			continue
		}
		message, err := st.CreateEngineerMessage(ctx, control.EngineerMessage{
			OperationID:              control.BoardReviewOperationID(watch.ID, event.EventID),
			ProjectPath:              watch.ProjectPath,
			Provider:                 watch.Provider,
			SessionMode:              control.SessionModeResumeOrNew,
			RequestedTargetSessionID: watch.TargetSessionID,
			TargetSessionID:          watch.TargetSessionID,
			Prompt:                   control.BoardReviewWakePrompt(watch, control.BoardReviewEvent{EventID: event.EventID, ItemID: event.ItemID}),
			State:                    control.EngineerMessageQueued,
			ExpiresAt:                deadline,
		})
		if err != nil {
			report.Errors = append(report.Errors, fmt.Errorf("board watch %s event %s: queue wake: %w", watch.ID, event.EventID, err))
			_ = st.RecordBoardWatchScanError(ctx, watch.ID, "could not queue a review wake; retrying: "+err.Error(), now)
			continue
		}
		linked, err := st.MarkBoardWatchEventQueued(ctx, watch.ID, event.EventID, message.ID)
		if err != nil {
			report.Errors = append(report.Errors, err)
			continue
		}
		if !linked {
			// Canceled or unregistered while the message was being created.
			if _, err := st.FailQueuedEngineerMessage(ctx, message.ID, "Review wake withdrawn: the request was canceled before it was linked."); err != nil {
				report.Errors = append(report.Errors, err)
			}
			report.Withdrawn++
			continue
		}
		report.Enqueued++
	}
}
