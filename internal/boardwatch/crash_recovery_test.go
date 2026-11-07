package boardwatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

// storedPendingEvent persists an event the way a poll does before queueing.
func storedPendingEvent(t *testing.T, f *fixture, w control.BoardWatch, id string) control.BoardReviewEvent {
	t.Helper()
	event := control.BoardReviewEvent{Schema: control.BoardReviewEventSchema, Kind: control.BoardReviewRequested, EventID: id, BoardID: w.BoardID, ItemID: "ST-8", CreatedAt: f.now}
	if _, err := f.st.IngestBoardWatchBatch(context.Background(), store.BoardWatchIngest{
		WatchID: w.ID, ExpectedOffset: w.CursorOffset, NewOffset: w.CursorOffset, Events: []control.BoardReviewEvent{event}, Now: f.now,
	}); err != nil {
		t.Fatal(err)
	}
	return event
}

// orphanWake is the crash boundary after mailbox creation and before linking.
func orphanWake(t *testing.T, f *fixture, w control.BoardWatch, event control.BoardReviewEvent) control.EngineerMessage {
	t.Helper()
	message, err := f.st.CreateEngineerMessage(context.Background(), control.EngineerMessage{
		OperationID: control.BoardReviewOperationID(w.ID, event.EventID), ProjectPath: w.ProjectPath, Provider: w.Provider,
		SessionMode: control.SessionModeResumeOrNew, RequestedTargetSessionID: w.TargetSessionID, TargetSessionID: w.TargetSessionID,
		Prompt: control.BoardReviewWakePrompt(w, event), State: control.EngineerMessageQueued, ExpiresAt: f.now.Add(control.BoardReviewDeliveryDeadline),
	})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func (f *fixture) messageState(id string) control.EngineerMessageState {
	f.t.Helper()
	m, err := f.st.GetEngineerMessage(context.Background(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return m.State
}

func TestUnregisterAfterCrashBeforeMessageLinkWithdrawsOrphanWake(t *testing.T) {
	f := newFixture(t)
	w := f.register("director")
	event := storedPendingEvent(t, f, w, "crash-event")
	orphan := orphanWake(t, f, w, event)
	if _, _, err := f.st.UnregisterBoardWatch(context.Background(), w.ID); err != nil {
		t.Fatal(err)
	}
	f.poll()
	if got := f.messageState(orphan.ID); got != control.EngineerMessageFailed {
		t.Fatalf("orphan wake state = %s, want failed", got)
	}
	if st := f.status("crash-event"); st.State != control.BoardWatchEventCanceled || st.Delivery(f.now) != "canceled" {
		t.Fatalf("status = %#v delivery=%s", st, st.Delivery(f.now))
	}
	if f.allMessages() != 0 {
		t.Fatal("a wake stayed deliverable after unregister")
	}
}

func TestCancelAndResolveBeforeLinkWithdrawOrphanWake(t *testing.T) {
	for name, kind := range map[string]control.BoardReviewEventKind{"canceled": control.BoardReviewCanceled, "resolved": control.BoardReviewResolved} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			w := f.register("director")
			event := storedPendingEvent(t, f, w, "crash-event")
			orphan := orphanWake(t, f, w, event)
			// The board's cancel or resolve line is read after the crash.
			f.append(f.line(kind, "follow-up", "ST-8", "crash-event", f.now))
			f.poll()
			if got := f.messageState(orphan.ID); got != control.EngineerMessageFailed {
				t.Fatalf("orphan wake state = %s", got)
			}
			want := control.BoardWatchEventCanceled
			if kind == control.BoardReviewResolved {
				want = control.BoardWatchEventReviewed
			}
			if st := f.status("crash-event"); st.State != want {
				t.Fatalf("event state = %s, want %s", st.State, want)
			}
			if f.allMessages() != 0 {
				t.Fatal("a wake stayed deliverable")
			}
		})
	}
}

func TestCancelDoesNotHideAnOrphanWakeThatAlreadyReachedTheDirector(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w := f.register("director")
	event := storedPendingEvent(t, f, w, "crash-event")
	orphan := orphanWake(t, f, w, event)
	if _, claimed, err := f.st.ClaimEngineerMessage(ctx, orphan.ID, "director"); err != nil || !claimed {
		t.Fatalf("claim = %t %v", claimed, err)
	}
	f.append(f.line(control.BoardReviewCanceled, "cx", "ST-8", "crash-event", f.now))
	f.poll()
	// Too late to cancel: the event is linked to the real message, not canceled.
	if st := f.status("crash-event"); st.State != control.BoardWatchEventQueued || st.MessageID != orphan.ID || st.Delivery(f.now) != "delivering" {
		t.Fatalf("status = %#v delivery=%s", st, st.Delivery(f.now))
	}
	if _, _, err := f.st.UnregisterBoardWatch(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if st := f.status("crash-event"); st.State != control.BoardWatchEventQueued {
		t.Fatalf("unregister rewrote a delivering wake's record: %#v", st)
	}
}

// The crash boundary between the ingest commit (event reviewed) and the
// post-commit withdrawal of its still-queued, already linked wake.
func TestReviewedWakeIsWithdrawnWhenWithdrawalWasInterrupted(t *testing.T) {
	f := newFixture(t)
	dbPath := filepath.Join(t.TempDir(), "crash.sqlite")
	replacement, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { replacement.Close() })
	f.st = replacement
	if err := f.st.UpsertProjectState(context.Background(), model.ProjectState{Path: f.project, Name: "tracker", PresentOnDisk: true, InScope: true}); err != nil {
		t.Fatal(err)
	}
	f.register("director")
	f.request("reviewed-event")
	f.poll()
	messages := f.messages()
	if len(messages) != 1 {
		t.Fatalf("messages = %d", len(messages))
	}
	f.append(f.line(control.BoardReviewResolved, "resolve-event", "ST-8", "reviewed-event", f.now))
	f.poll()
	if got := f.messageState(messages[0].ID); got != control.EngineerMessageFailed {
		t.Fatalf("normal path left the reviewed wake %s", got)
	}
	// Restore the persisted state a crash would have left: reviewed, wake queued.
	db, err := sql.Open("lcroom-sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE engineer_messages SET state = ?, last_error = ? WHERE id = ?", string(control.EngineerMessageQueued), "crash fixture", messages[0].ID); err != nil {
		t.Fatal(err)
	}
	if f.allMessages() != 1 {
		t.Fatal("fixture did not restore the queued wake")
	}
	f.poll()
	if got := f.messageState(messages[0].ID); got != control.EngineerMessageFailed {
		t.Fatalf("already-reviewed wake remains deliverable after recovery: %s", got)
	}
	// Review and delivery stay truthfully separate.
	if st := f.status("reviewed-event"); st.State != control.BoardWatchEventReviewed || st.Delivery(f.now) != "failed" {
		t.Fatalf("status = %#v delivery=%s", st, st.Delivery(f.now))
	}
}

func TestReviewedAfterDeliveryKeepsDeliveredWake(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.register("director")
	f.request("ev")
	f.poll()
	id := f.status("ev").MessageID
	if _, claimed, err := f.st.ClaimEngineerMessage(ctx, id, "director"); err != nil || !claimed {
		t.Fatalf("claim = %t %v", claimed, err)
	}
	if _, err := f.st.RecordEngineerMessageState(ctx, id, control.EngineerMessageDelivered, "director", "delivered", nil); err != nil {
		t.Fatal(err)
	}
	f.append(f.line(control.BoardReviewResolved, "rs", "ST-8", "ev", f.now))
	f.poll()
	if st := f.status("ev"); st.State != control.BoardWatchEventReviewed || st.Delivery(f.now) != "delivered" {
		t.Fatalf("status = %#v delivery=%s", st, st.Delivery(f.now))
	}
}

// A second pass over a stale pending list must neither duplicate nor cancel a
// wake that another poll already linked.
func TestStalePendingListDoesNotCancelAnAlreadyLinkedWake(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w := f.register("director")
	storedPendingEvent(t, f, w, "ev")
	stale, err := f.st.PendingBoardWatchEvents(ctx, 10)
	if err != nil || len(stale) != 1 {
		t.Fatalf("pending = %#v err=%v", stale, err)
	}
	var first, second Report
	enqueueEvents(ctx, f.st, stale, f.now, &first)
	enqueueEvents(ctx, f.st, stale, f.now, &second) // the other poller's identical pass
	if len(first.Errors)+len(second.Errors) > 0 {
		t.Fatalf("errors: %v %v", first.Errors, second.Errors)
	}
	if second.Withdrawn != 0 || f.allMessages() != 1 {
		t.Fatalf("second pass withdrew=%d queued=%d", second.Withdrawn, f.allMessages())
	}
	if st := f.status("ev"); st.State != control.BoardWatchEventQueued || st.Delivery(f.now) != "queued" {
		t.Fatalf("status = %#v", st)
	}

	// A stale pass after the event was canceled must not resurrect a wake.
	storedPendingEvent(t, f, w, "ev-2")
	stale, _ = f.st.PendingBoardWatchEvents(ctx, 10)
	f.append(f.line(control.BoardReviewCanceled, "cx", "ST-8", "ev-2", f.now))
	var scan Report
	current, _ := f.st.GetBoardWatch(ctx, w.ID)
	scanWatch(ctx, f.st, current, f.now, &scan)
	var third Report
	enqueueEvents(ctx, f.st, stale, f.now, &third)
	f.poll()
	if f.allMessages() != 1 {
		t.Fatalf("a canceled event's wake was resurrected: queued=%d", f.allMessages())
	}
}

func TestConcurrentPollsQueueOneWakeAndKeepItDeliverable(t *testing.T) {
	f := newFixture(t)
	f.register("director")
	f.request("ev")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Poll(context.Background(), f.st, f.now)
		}()
	}
	wg.Wait()
	f.poll()
	if f.allMessages() != 1 {
		t.Fatalf("queued = %d, want exactly one deliverable wake", f.allMessages())
	}
	if st := f.status("ev"); st.State != control.BoardWatchEventQueued || st.Delivery(f.now) != "queued" {
		t.Fatalf("status = %#v", st)
	}
}

// More registrations than the display cap must all be polled, including the
// oldest ones that a newest-first 100-row list would omit.
func TestPollCoversMoreActiveWatchesThanTheDisplayLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	const count = 101
	outboxes := make([]string, count)
	boards := make([]string, count)
	ids := make([]string, count)
	for i := 0; i < count; i++ {
		boards[i] = fmt.Sprintf("board-%03d", i)
		outboxes[i] = filepath.Join(f.project, "docs", boards[i]+".jsonl")
		w, _, err := f.st.RegisterBoardWatch(ctx, control.BoardWatch{
			BoardID: boards[i], ProjectPath: f.project, Provider: control.ProviderClaudeCode,
			TargetSessionID: fmt.Sprintf("director-%03d", i), OutboxPath: outboxes[i],
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = w.ID
	}
	if shown, _ := f.st.ListBoardWatches(ctx, "", false); len(shown) != 100 {
		t.Fatalf("display list = %d rows; the regression needs it capped below %d", len(shown), count)
	}
	// The first-registered watch is the one the capped list omits.
	omitted := 0
	for i := range ids {
		shown, _ := f.st.ListBoardWatches(ctx, "", false)
		found := false
		for _, w := range shown {
			if w.ID == ids[i] {
				found = true
			}
		}
		if !found {
			omitted = i
			break
		}
	}
	event := control.BoardReviewEvent{Schema: control.BoardReviewEventSchema, Kind: control.BoardReviewRequested, EventID: "ev-omitted", BoardID: boards[omitted], ItemID: "ST-8", CreatedAt: f.now}
	line, _ := json.Marshal(event)
	if err := os.WriteFile(outboxes[omitted], append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	last := count - 1
	event = control.BoardReviewEvent{Schema: control.BoardReviewEventSchema, Kind: control.BoardReviewRequested, EventID: "ev-newest", BoardID: boards[last], ItemID: "ST-9", CreatedAt: f.now}
	line, _ = json.Marshal(event)
	if err := os.WriteFile(outboxes[last], append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	report := f.poll()
	if report.Watches != count || report.Enqueued != 2 {
		t.Fatalf("report = %#v, want %d watches polled and both wakes queued", report, count)
	}
	statuses, err := f.st.ListBoardWatchEventStatuses(ctx, ids[omitted], 10)
	if err != nil || len(statuses) != 1 || statuses[0].MessageID == "" {
		t.Fatalf("omitted watch events = %#v err=%v", statuses, err)
	}
	// Polling again is idempotent: no duplicate wakes, still every watch covered.
	if again := f.poll(); again.Watches != count || again.Enqueued != 0 || f.allMessages() != 2 {
		t.Fatalf("second poll = %#v queued=%d", again, f.allMessages())
	}
}

// Cancellation can commit before its post-commit withdrawal and a delivery
// claim can win that race. Status must keep both facts: the request stays
// canceled while the actually claimed wake reports delivering, then delivered.
func TestCanceledRequestWithClaimedWakeReportsActualDelivery(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "race.sqlite")
	replacement, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { replacement.Close() })
	f.st = replacement
	if err := f.st.UpsertProjectState(ctx, model.ProjectState{Path: f.project, Name: "tracker", PresentOnDisk: true, InScope: true}); err != nil {
		t.Fatal(err)
	}
	f.register("director")
	f.request("ev")
	f.poll()
	id := f.status("ev").MessageID
	if _, claimed, err := f.st.ClaimEngineerMessage(ctx, id, "director"); err != nil || !claimed {
		t.Fatalf("claim = %t %v", claimed, err)
	}
	// The persisted state a lost withdrawal race leaves behind: canceled review, claimed wake.
	db, err := sql.Open("lcroom-sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE board_watch_events SET state = ?, note = ? WHERE event_id = 'ev'", string(control.BoardWatchEventCanceled), "withdrawal lost the race"); err != nil {
		t.Fatal(err)
	}
	f.poll() // the recovery sweep must not touch a claimed wake
	if got := f.status("ev"); got.State != control.BoardWatchEventCanceled || got.Delivery(f.now) != "delivering" {
		t.Fatalf("status = review %s, delivery %s", got.State, got.Delivery(f.now))
	}
	if _, err := f.st.RecordEngineerMessageState(ctx, id, control.EngineerMessageDelivered, "director", "delivered", nil); err != nil {
		t.Fatal(err)
	}
	if got := f.status("ev"); got.State != control.BoardWatchEventCanceled || got.Delivery(f.now) != "delivered" {
		t.Fatalf("status = review %s, delivery %s", got.State, got.Delivery(f.now))
	}
}

// A wake created before a crash and not yet linked to its event must report
// its real delivery, also after the watch was unregistered (which never links it).
func TestUnlinkedWakeReportsActualDeliveryBeforeAndAfterUnregister(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w := f.register("director")
	event := storedPendingEvent(t, f, w, "crash-event")
	orphan := orphanWake(t, f, w, event)

	if got := f.status("crash-event"); got.MessageID != orphan.ID || got.State != control.BoardWatchEventPending || got.Delivery(f.now) != "queued" {
		t.Fatalf("unclaimed unlinked wake: message=%q review=%s delivery=%s", got.MessageID, got.State, got.Delivery(f.now))
	}
	if _, claimed, err := f.st.ClaimEngineerMessage(ctx, orphan.ID, "director"); err != nil || !claimed {
		t.Fatalf("claim = %t %v", claimed, err)
	}
	if got := f.status("crash-event"); got.MessageID != orphan.ID || got.Delivery(f.now) != "delivering" {
		t.Fatalf("claimed unlinked wake: message=%q review=%s delivery=%s", got.MessageID, got.State, got.Delivery(f.now))
	}

	// Cancellation semantics are unchanged: the claimed wake keeps its record.
	if _, _, err := f.st.UnregisterBoardWatch(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	f.poll()
	if got := f.status("crash-event"); got.MessageID != orphan.ID || got.Delivery(f.now) != "delivering" {
		t.Fatalf("after unregister: message=%q review=%s delivery=%s", got.MessageID, got.State, got.Delivery(f.now))
	}
	if _, err := f.st.RecordEngineerMessageState(ctx, orphan.ID, control.EngineerMessageDelivered, "director", "delivered", nil); err != nil {
		t.Fatal(err)
	}
	if got := f.status("crash-event"); got.MessageID != orphan.ID || got.Delivery(f.now) != "delivered" {
		t.Fatalf("after delivery: message=%q review=%s delivery=%s", got.MessageID, got.State, got.Delivery(f.now))
	}
}

// A withdrawn unlinked wake reads as canceled, and a tombstone still reads as not sent.
func TestUnlinkedStatusKeepsCancellationAndTombstoneSemantics(t *testing.T) {
	f := newFixture(t)
	w := f.register("director")
	event := storedPendingEvent(t, f, w, "crash-event")
	orphan := orphanWake(t, f, w, event)
	f.append(f.line(control.BoardReviewCanceled, "cx", "ST-8", "crash-event", f.now))
	f.append(f.line(control.BoardReviewCanceled, "cx2", "ST-8", "never-seen", f.now))
	f.poll()
	if got := f.messageState(orphan.ID); got != control.EngineerMessageFailed {
		t.Fatalf("orphan state = %s", got)
	}
	if got := f.status("crash-event"); got.State != control.BoardWatchEventCanceled || got.Delivery(f.now) != "canceled" {
		t.Fatalf("withdrawn unlinked wake: review=%s delivery=%s", got.State, got.Delivery(f.now))
	}
	if got := f.status("never-seen"); got.MessageID != "" || got.Delivery(f.now) != "canceled" {
		t.Fatalf("tombstone: message=%q delivery=%s", got.MessageID, got.Delivery(f.now))
	}
}
