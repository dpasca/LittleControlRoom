package boardwatch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

type fixture struct {
	t       *testing.T
	st      *store.Store
	project string
	outbox  string
	owner   string
	watch   control.BoardWatch
	now     time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	project := t.TempDir()
	if err := st.UpsertProjectState(context.Background(), model.ProjectState{Path: project, Name: "tracker", PresentOnDisk: true, InScope: true}); err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		t: t, st: st, project: project,
		outbox: filepath.Join(project, "docs", "review-events.jsonl"),
		owner:  filepath.Join(project, "docs", "owner-answers.json"),
		now:    time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC),
	}
	if err := os.MkdirAll(filepath.Dir(f.outbox), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) register(session string) control.BoardWatch {
	f.t.Helper()
	prepared, err := PrepareRegistration(context.Background(), f.st, control.EngineerBoardWatchRegisterInput{
		BoardID: "board-1", ProjectPath: f.project, Provider: control.ProviderClaudeCode, TargetSessionID: session, OutboxPath: f.outbox,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	watch, _, err := f.st.RegisterBoardWatch(context.Background(), prepared)
	if err != nil {
		f.t.Fatal(err)
	}
	f.watch = watch
	return watch
}

func (f *fixture) line(kind control.BoardReviewEventKind, id, item, target string, at time.Time) string {
	event := control.BoardReviewEvent{Schema: control.BoardReviewEventSchema, Kind: kind, EventID: id, BoardID: "board-1", ItemID: item, TargetEventID: target, CreatedAt: at}
	data, err := json.Marshal(event)
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data) + "\n"
}

func (f *fixture) append(text string) {
	f.t.Helper()
	file, err := os.OpenFile(f.outbox, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		f.t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(text); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) request(id string) {
	f.append(f.line(control.BoardReviewRequested, id, "LP-203", "", f.now))
}

func (f *fixture) poll() Report {
	f.t.Helper()
	report := Poll(context.Background(), f.st, f.now)
	if len(report.Errors) > 0 {
		f.t.Fatalf("poll errors: %v", report.Errors)
	}
	return report
}

func (f *fixture) messages() []control.EngineerMessage {
	f.t.Helper()
	var out []control.EngineerMessage
	statuses, err := f.st.ListBoardWatchEventStatuses(context.Background(), f.watch.ID, 100)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, s := range statuses {
		if s.MessageID == "" {
			continue
		}
		m, err := f.st.GetEngineerMessage(context.Background(), s.MessageID)
		if err != nil {
			f.t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func (f *fixture) allMessages() int {
	f.t.Helper()
	all, err := f.st.ListQueuedEngineerMessages(context.Background(), 100)
	if err != nil {
		f.t.Fatal(err)
	}
	return len(all)
}

func (f *fixture) status(eventID string) control.BoardWatchEventStatus {
	f.t.Helper()
	statuses, err := f.st.ListBoardWatchEventStatuses(context.Background(), f.watch.ID, 100)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, s := range statuses {
		if s.EventID == eventID {
			return s
		}
	}
	f.t.Fatalf("no status for event %s in %#v", eventID, statuses)
	return control.BoardWatchEventStatus{}
}

func TestExplicitEventQueuesExactlyOneWakeWithOriginalDeadline(t *testing.T) {
	f := newFixture(t)
	f.register("director-1")
	created := f.now.Add(-2 * time.Hour)
	f.append(f.line(control.BoardReviewRequested, "ev-1", "LP-203", "", created))

	report := f.poll()
	if report.Ingested != 1 || report.Enqueued != 1 {
		t.Fatalf("report = %#v", report)
	}
	messages := f.messages()
	if len(messages) != 1 {
		t.Fatalf("messages = %#v", messages)
	}
	m := messages[0]
	if m.TargetSessionID != "director-1" || m.Provider != control.ProviderClaudeCode || m.ProjectPath != f.project || m.State != control.EngineerMessageQueued {
		t.Fatalf("message routing = %#v", m)
	}
	if m.OperationID != control.BoardReviewOperationID(f.watch.ID, "ev-1") {
		t.Fatalf("operation id = %q", m.OperationID)
	}
	if !m.ExpiresAt.Equal(created.Add(24 * time.Hour)) {
		t.Fatalf("expiry = %s, want event time + 24h", m.ExpiresAt)
	}
	for _, want := range []string{"ev-1", "board-1", "LP-203", "Reread the actual saved owner state", "grants no publication, browser or other external-write authority", "not an approval"} {
		if !strings.Contains(m.Prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, m.Prompt)
		}
	}
	if st := f.status("ev-1"); st.Delivery(f.now) != "queued" || st.State != control.BoardWatchEventQueued {
		t.Fatalf("status = %#v delivery=%s", st, st.Delivery(f.now))
	}
	// Polling again must neither re-read nor re-queue.
	if again := f.poll(); again.Ingested != 0 || again.Enqueued != 0 || f.allMessages() != 1 {
		t.Fatalf("second poll = %#v, queued=%d", again, f.allMessages())
	}
}

func TestNoteEditsAndUnrelatedChangesNeverWake(t *testing.T) {
	f := newFixture(t)
	f.register("director-1")
	// The board autosaves notes and rewrites owner answers constantly; LCR does
	// not read those files, and the outbox only counts well-formed events.
	if err := os.WriteFile(f.owner, []byte(`{"items":{"LP-203":{"choice":"Approve","note":"remove this first"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f.poll()
	if err := os.WriteFile(f.owner, []byte(`{"items":{"LP-203":{"choice":"Approve","note":"remove this first, please"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(f.project, "docs", "launch-plan.json")
	if err := os.WriteFile(other, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := []string{
		"not json\n",
		`{"schema":"lcr.board_review_event.v1","kind":"review_requested","event_id":"x","board_id":"other-board","item_id":"LP-1","created_at":"2026-11-07T12:00:00Z"}` + "\n",
		`{"schema":"lcr.board_review_event.v2","kind":"review_requested","event_id":"x","board_id":"board-1","item_id":"LP-1","created_at":"2026-11-07T12:00:00Z"}` + "\n",
		`{"schema":"lcr.board_review_event.v1","kind":"note_saved","event_id":"x","board_id":"board-1","item_id":"LP-1","created_at":"2026-11-07T12:00:00Z"}` + "\n",
		`{"schema":"lcr.board_review_event.v1","kind":"review_requested","event_id":"x","board_id":"board-1","item_id":"LP-1","created_at":"2026-11-07T12:00:00Z","note":"free text"}` + "\n",
		`{"schema":"lcr.board_review_event.v1","kind":"review_requested","event_id":"has space","board_id":"board-1","item_id":"LP-1","created_at":"2026-11-07T12:00:00Z"}` + "\n",
		`{"schema":"lcr.board_review_event.v1","kind":"review_requested","event_id":"x","board_id":"board-1","created_at":"2026-11-07T12:00:00Z"}` + "\n",
		`{"schema":"lcr.board_review_event.v1","kind":"review_requested","event_id":"x","board_id":"board-1","item_id":"LP-1"}` + "\n",
		`{"schema":"lcr.board_review_event.v1","kind":"review_requested","event_id":"x","board_id":"board-1","item_id":"LP-1","created_at":"2027-01-01T00:00:00Z"}` + "\n",
		"\n",
	}
	f.append(strings.Join(bad, ""))
	report := f.poll()
	if report.Ingested != 0 || report.Enqueued != 0 || f.allMessages() != 0 {
		t.Fatalf("report = %#v queued=%d", report, f.allMessages())
	}
	if report.Malformed != int64(len(bad)-1) {
		t.Fatalf("malformed = %d, want %d", report.Malformed, len(bad)-1)
	}
	got, _ := f.st.GetBoardWatch(context.Background(), f.watch.ID)
	if got.State != control.BoardWatchActive || got.MalformedCount != int64(len(bad)-1) || !strings.Contains(got.LastError, "malformed") {
		t.Fatalf("watch = %#v", got)
	}
	// A partial line (writer mid-append) waits for its newline.
	f.append(strings.TrimSuffix(f.line(control.BoardReviewRequested, "ev-partial", "LP-203", "", f.now), "\n"))
	if f.poll(); f.allMessages() != 0 {
		t.Fatal("a partial line woke the director")
	}
	f.append("\n")
	if f.poll(); f.allMessages() != 1 {
		t.Fatal("completed line did not wake once")
	}
}

func TestRepeatedEventsAreDeduplicatedAcrossPollsAndRestarts(t *testing.T) {
	f := newFixture(t)
	f.register("director-1")
	f.request("ev-1")
	f.request("ev-1")
	f.poll()
	f.request("ev-1") // a retried click appended again later
	f.poll()
	if f.allMessages() != 1 {
		t.Fatalf("queued = %d", f.allMessages())
	}
	got, _ := f.st.ListBoardWatchEventStatuses(context.Background(), f.watch.ID, 10)
	if len(got) != 1 {
		t.Fatalf("events = %#v", got)
	}
	// A fresh process sees the same store: nothing is re-read or re-queued.
	if report := Poll(context.Background(), f.st, f.now.Add(time.Minute)); report.Ingested != 0 || report.Enqueued != 0 || f.allMessages() != 1 {
		t.Fatalf("restart poll = %#v queued=%d", report, f.allMessages())
	}
	f.request("ev-2")
	f.poll()
	if f.allMessages() != 2 {
		t.Fatalf("distinct event should add one wake, queued = %d", f.allMessages())
	}
}

func TestRestartCatchUpBeforeAndAfterEnqueue(t *testing.T) {
	f := newFixture(t)
	f.register("director-1")
	f.request("ev-before")
	f.request("ev-after")

	// Crash after the event and cursor were stored but before any message.
	var w control.BoardWatch
	w, _ = f.st.GetBoardWatch(context.Background(), f.watch.ID)
	var report Report
	scanWatch(context.Background(), f.st, w, f.now, &report)
	if len(report.Errors) > 0 || report.Ingested != 2 || f.allMessages() != 0 {
		t.Fatalf("scan only = %#v queued=%d", report, f.allMessages())
	}
	pending, err := f.st.PendingBoardWatchEvents(context.Background(), 10)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending = %#v err=%v", pending, err)
	}

	// Crash after one message was created but before it was linked to its event.
	orphan, err := f.st.CreateEngineerMessage(context.Background(), control.EngineerMessage{
		OperationID: control.BoardReviewOperationID(f.watch.ID, "ev-after"), ProjectPath: f.project, Provider: control.ProviderClaudeCode,
		SessionMode: control.SessionModeResumeOrNew, RequestedTargetSessionID: "director-1", TargetSessionID: "director-1",
		Prompt:    control.BoardReviewWakePrompt(f.watch, control.BoardReviewEvent{EventID: "ev-after", ItemID: "LP-203"}),
		ExpiresAt: f.now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	// "Restart": the next poll finishes both without duplicating the orphan.
	f.poll()
	if f.allMessages() != 2 {
		t.Fatalf("queued = %d, want exactly two", f.allMessages())
	}
	if got := f.status("ev-after"); got.MessageID != orphan.ID {
		t.Fatalf("ev-after linked to %s, want the pre-existing message %s", got.MessageID, orphan.ID)
	}
	if got := f.status("ev-before"); got.MessageID == "" || got.MessageID == orphan.ID {
		t.Fatalf("ev-before link = %#v", got)
	}
}

func TestOriginalDeadlineSurvivesRestartAndStaleEventsExpireWithoutWaking(t *testing.T) {
	f := newFixture(t)
	f.register("director-1")
	f.append(f.line(control.BoardReviewRequested, "ev-old", "LP-203", "", f.now.Add(-25*time.Hour)))
	f.append(f.line(control.BoardReviewRequested, "ev-late", "LP-203", "", f.now.Add(-23*time.Hour)))
	report := f.poll()
	if report.Expired != 1 || report.Enqueued != 1 {
		t.Fatalf("report = %#v", report)
	}
	if got := f.status("ev-old"); got.State != control.BoardWatchEventExpired || got.Delivery(f.now) != "expired" || got.MessageID != "" {
		t.Fatalf("old event = %#v", got)
	}
	// An event stored before the deadline but still unqueued when it passes is expired, not woken.
	f.append(f.line(control.BoardReviewRequested, "ev-pending", "LP-203", "", f.now.Add(-time.Hour)))
	var w control.BoardWatch
	w, _ = f.st.GetBoardWatch(context.Background(), f.watch.ID)
	var scan Report
	scanWatch(context.Background(), f.st, w, f.now, &scan)
	later := f.now.Add(25 * time.Hour)
	if report := Poll(context.Background(), f.st, later); report.Expired != 1 || report.Enqueued != 0 {
		t.Fatalf("late poll = %#v", report)
	}
	if got := f.status("ev-pending"); got.State != control.BoardWatchEventExpired {
		t.Fatalf("ev-pending = %#v", got)
	}
	// The queued late-but-valid wake keeps its original deadline (event + 24h).
	m, _ := f.st.GetEngineerMessage(context.Background(), f.status("ev-late").MessageID)
	if !m.ExpiresAt.Equal(f.now.Add(-23 * time.Hour).Add(24 * time.Hour)) {
		t.Fatalf("expiry = %s", m.ExpiresAt)
	}
	// And the mailbox itself expires it once the deadline passes.
	expired, err := f.st.ExpireOverdueEngineerMessages(context.Background(), later)
	if err != nil || len(expired) != 1 {
		t.Fatalf("expired = %#v err=%v", expired, err)
	}
	if got := f.status("ev-late"); got.Delivery(later) != "expired" {
		t.Fatalf("delivery = %s", got.Delivery(later))
	}
}

func TestStaleOrReplacedTargets(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// Registering a session that /handoff already replaced is refused.
	if _, err := f.st.RecordEngineerSessionSuccession(ctx, store.EngineerSessionSuccession{
		ProjectPath: f.project, Provider: model.SessionSourceClaudeCode, SessionID: "gone", SessionKey: "gone-key",
		SuccessorProvider: model.SessionSourceCodex, SuccessorSessionKey: "launch-key", Reason: "handoff",
	}); err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareRegistration(ctx, f.st, control.EngineerBoardWatchRegisterInput{
		BoardID: "board-1", ProjectPath: f.project, Provider: control.ProviderClaudeCode, TargetSessionID: "gone", OutboxPath: f.outbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.st.RegisterBoardWatch(ctx, prepared); err == nil || !strings.Contains(err.Error(), "/handoff") {
		t.Fatalf("register to replaced session err = %v", err)
	}

	// A watch whose director is replaced later is held, not delivered to the old session.
	f2 := newFixture(t)
	f2.register("director-1")
	if _, err := f2.st.RecordEngineerSessionSuccession(ctx, store.EngineerSessionSuccession{
		ProjectPath: f2.project, Provider: model.SessionSourceClaudeCode, SessionID: "director-1", SessionKey: "k1",
		SuccessorProvider: model.SessionSourceCodex, SuccessorSessionKey: "launch-key", Reason: "handoff",
	}); err != nil {
		t.Fatal(err)
	}
	f2.request("ev-1")
	f2.poll()
	if f2.allMessages() != 0 {
		t.Fatal("wake was released to a replaced session before its successor announced itself")
	}
	held := f2.messages()
	if len(held) != 1 || !strings.Contains(held[0].LastError, "replaced director-1") {
		t.Fatalf("held = %#v", held)
	}
	if got := f2.status("ev-1"); got.Delivery(f2.now) != "queued_waiting" {
		t.Fatalf("delivery = %s", got.Delivery(f2.now))
	}
}

func TestHandoffForwardsQueuedAndFutureWakesToSuccessor(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.register("director-1")
	f.request("ev-queued")
	f.poll()
	if _, err := f.st.RecordEngineerSessionSuccession(ctx, store.EngineerSessionSuccession{
		ProjectPath: f.project, Provider: model.SessionSourceClaudeCode, SessionID: "director-1", SessionKey: "k1",
		SuccessorProvider: model.SessionSourceCodex, SuccessorSessionKey: "launch-key", Reason: "handoff",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CompleteEngineerSessionSuccessions(ctx, f.project, model.SessionSourceCodex, "launch-key", "director-2"); err != nil {
		t.Fatal(err)
	}
	f.request("ev-after-handoff")
	f.poll()
	for _, id := range []string{"ev-queued", "ev-after-handoff"} {
		m, err := f.st.GetEngineerMessage(ctx, f.status(id).MessageID)
		if err != nil {
			t.Fatal(err)
		}
		if m.TargetSessionID != "director-2" || m.Provider != control.ProviderCodex || m.RequestedTargetSessionID != "director-1" {
			t.Fatalf("%s routed to %#v", id, m)
		}
	}
	if f.allMessages() != 2 {
		t.Fatalf("queued = %d", f.allMessages())
	}
}

func TestCancelResolveAndUnregisterBoundaries(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.register("director-1")

	// A cancel that arrives with its request never produces a wake.
	f.request("ev-canceled-early")
	f.append(f.line(control.BoardReviewCanceled, "cx-1", "LP-203", "ev-canceled-early", f.now))
	// Resolved before the wake was queued: the director already read the state.
	f.request("ev-resolved-early")
	f.append(f.line(control.BoardReviewResolved, "rs-1", "LP-203", "ev-resolved-early", f.now))
	f.request("ev-live")
	report := f.poll()
	if report.Enqueued != 1 || f.allMessages() != 1 {
		t.Fatalf("report = %#v queued=%d", report, f.allMessages())
	}
	if got := f.status("ev-canceled-early"); got.State != control.BoardWatchEventCanceled || got.Delivery(f.now) != "canceled" {
		t.Fatalf("canceled early = %#v", got)
	}
	if got := f.status("ev-resolved-early"); got.State != control.BoardWatchEventReviewed || got.Delivery(f.now) != "not_sent" {
		t.Fatalf("resolved early = %#v", got)
	}

	// A cancel after queueing withdraws the undelivered wake from the mailbox.
	f.append(f.line(control.BoardReviewCanceled, "cx-2", "LP-203", "ev-live", f.now))
	f.poll()
	live, _ := f.st.GetEngineerMessage(ctx, f.status("ev-live").MessageID)
	if live.State != control.EngineerMessageFailed || f.allMessages() != 0 {
		t.Fatalf("live wake after cancel = %#v queued=%d", live, f.allMessages())
	}

	// A cancel for an id not seen yet leaves a tombstone: the later request is a duplicate.
	f.append(f.line(control.BoardReviewCanceled, "cx-3", "LP-203", "ev-future", f.now))
	f.request("ev-future")
	f.poll()
	if f.allMessages() != 0 {
		t.Fatal("request after its own cancel woke the director")
	}

	// Too late: a delivered wake keeps its delivered state; reviewed is recorded separately.
	f.request("ev-delivered")
	f.poll()
	id := f.status("ev-delivered").MessageID
	if _, claimed, err := f.st.ClaimEngineerMessage(ctx, id, "director-1"); err != nil || !claimed {
		t.Fatalf("claim = %t %v", claimed, err)
	}
	if _, err := f.st.RecordEngineerMessageState(ctx, id, control.EngineerMessageDelivered, "director-1", "delivered", nil); err != nil {
		t.Fatal(err)
	}
	f.append(f.line(control.BoardReviewCanceled, "cx-4", "LP-203", "ev-delivered", f.now))
	f.poll()
	if got := f.status("ev-delivered"); got.State != control.BoardWatchEventQueued || got.Delivery(f.now) != "delivered" {
		t.Fatalf("delivered then canceled = %#v delivery=%s", got, got.Delivery(f.now))
	}
	f.append(f.line(control.BoardReviewResolved, "rs-2", "LP-203", "ev-delivered", f.now))
	f.poll()
	if got := f.status("ev-delivered"); got.State != control.BoardWatchEventReviewed || got.Delivery(f.now) != "delivered" {
		t.Fatalf("delivered then reviewed = %#v", got)
	}

	// Unregister: pending and queued wakes are canceled, nothing is read again.
	f.request("ev-queued")
	f.poll()
	f.request("ev-pending")
	var w control.BoardWatch
	w, _ = f.st.GetBoardWatch(ctx, f.watch.ID)
	var scan Report
	scanWatch(ctx, f.st, w, f.now, &scan) // stored, but no message yet
	f.request("ev-unread")
	if _, withdrawn, err := f.st.UnregisterBoardWatch(ctx, f.watch.ID); err != nil || len(withdrawn) != 1 {
		t.Fatalf("unregister withdrawn=%v err=%v", withdrawn, err)
	}
	if again, withdrawn, err := f.st.UnregisterBoardWatch(ctx, f.watch.ID); err != nil || len(withdrawn) != 0 || again.State != control.BoardWatchUnregistered {
		t.Fatalf("second unregister = %#v %v %v", again, withdrawn, err)
	}
	after := f.poll()
	if after.Watches != 0 || after.Enqueued != 0 || f.allMessages() != 0 {
		t.Fatalf("poll after unregister = %#v queued=%d", after, f.allMessages())
	}
	for _, id := range []string{"ev-pending", "ev-queued"} {
		if got := f.status(id); got.State != control.BoardWatchEventCanceled {
			t.Fatalf("%s = %#v", id, got)
		}
	}
	if m, _ := f.st.GetEngineerMessage(ctx, f.status("ev-queued").MessageID); m.State != control.EngineerMessageFailed {
		t.Fatalf("queued wake after unregister = %#v", m)
	}
	// Re-registering starts at the outbox's current end: the saved click never revives.
	again := f.register("director-1")
	if again.ID == f.watch.ID && again.State != control.BoardWatchActive {
		t.Fatalf("re-register = %#v", again)
	}
	f.poll()
	if f.allMessages() != 0 {
		t.Fatal("stale saved events woke the director after re-registering")
	}
	f.request("ev-fresh")
	f.poll()
	if f.allMessages() != 1 {
		t.Fatalf("fresh event after re-register: queued=%d", f.allMessages())
	}
}

func TestDeliveryStatesAreDistinguishable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.register("director-1")
	for _, id := range []string{"ev-queued", "ev-delivered", "ev-failed", "ev-waiting"} {
		f.request(id)
	}
	f.poll()
	if _, err := f.st.RecordEngineerMessageWaiting(ctx, f.status("ev-waiting").MessageID, "recipient-busy", "Recipient busy: waiting for the originating session to become idle; delivery retries automatically."); err != nil {
		t.Fatal(err)
	}
	for id, state := range map[string]control.EngineerMessageState{"ev-delivered": control.EngineerMessageDelivered, "ev-failed": control.EngineerMessageFailed} {
		message := f.status(id).MessageID
		if _, claimed, err := f.st.ClaimEngineerMessage(ctx, message, "director-1"); err != nil || !claimed {
			t.Fatalf("claim %s = %t %v", id, claimed, err)
		}
		var cause error
		if state == control.EngineerMessageFailed {
			cause = errors.New("session changed")
		}
		if _, err := f.st.RecordEngineerMessageState(ctx, message, state, "director-1", string(state), cause); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]string{"ev-queued": "queued", "ev-delivered": "delivered", "ev-failed": "failed", "ev-waiting": "queued_waiting"}
	for id, delivery := range want {
		if got := f.status(id).Delivery(f.now); got != delivery {
			t.Errorf("%s delivery = %s, want %s", id, got, delivery)
		}
	}
}

func TestOutboxIntegrityFailuresStopWithoutGuessing(t *testing.T) {
	cases := map[string]func(f *fixture){
		"truncated": func(f *fixture) {
			if err := os.WriteFile(f.outbox, nil, 0o644); err != nil {
				f.t.Fatal(err)
			}
		},
		"rewritten head": func(f *fixture) {
			data, _ := os.ReadFile(f.outbox)
			data[2] ^= 0x01
			if err := os.WriteFile(f.outbox, append(data, []byte("\n")...), 0o644); err != nil {
				f.t.Fatal(err)
			}
		},
		"removed": func(f *fixture) {
			if err := os.Remove(f.outbox); err != nil {
				f.t.Fatal(err)
			}
		},
		"replaced by symlink": func(f *fixture) {
			target := filepath.Join(f.project, "docs", "elsewhere.jsonl")
			if err := os.Rename(f.outbox, target); err != nil {
				f.t.Fatal(err)
			}
			if err := os.Symlink(target, f.outbox); err != nil {
				f.t.Fatal(err)
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.request("ev-1")
			f.register("director-1") // baseline is after ev-1: it must never wake
			mutate(f)
			report := Poll(context.Background(), f.st, f.now)
			if len(report.Errors) == 0 {
				t.Fatalf("expected an attention error: %#v", report)
			}
			got, _ := f.st.GetBoardWatch(context.Background(), f.watch.ID)
			if got.State != control.BoardWatchAttention || got.LastError == "" || f.allMessages() != 0 {
				t.Fatalf("watch = %#v queued=%d", got, f.allMessages())
			}
			// Attention stays stopped on later polls, even when the file looks fine again.
			f.append(f.line(control.BoardReviewRequested, "ev-2", "LP-203", "", f.now))
			if after := Poll(context.Background(), f.st, f.now); after.Watches != 0 || f.allMessages() != 0 {
				t.Fatalf("scanned a watch in attention: %#v", after)
			}
		})
	}
	t.Run("oversized line", func(t *testing.T) {
		f := newFixture(t)
		f.register("director-1")
		f.append(strings.Repeat("x", control.BoardReviewMaxLineBytes+10))
		Poll(context.Background(), f.st, f.now)
		if got, _ := f.st.GetBoardWatch(context.Background(), f.watch.ID); got.State != control.BoardWatchAttention {
			t.Fatalf("watch = %#v", got)
		}
	})
}

func TestRegistrationRejectsUnauthorizedBindings(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	outside := t.TempDir()
	link := filepath.Join(f.project, "docs", "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.project, ".git-notes"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.project, "docs", "owner-answers.json"), filepath.Join(f.project, "docs", "sym.jsonl")); err != nil {
		t.Fatal(err)
	}
	base := control.EngineerBoardWatchRegisterInput{BoardID: "board-1", ProjectPath: f.project, Provider: control.ProviderClaudeCode, TargetSessionID: "director-1", OutboxPath: f.outbox}
	cases := map[string]func(in *control.EngineerBoardWatchRegisterInput){
		"untracked project": func(in *control.EngineerBoardWatchRegisterInput) {
			in.ProjectPath = t.TempDir()
			in.OutboxPath = filepath.Join(in.ProjectPath, "e.jsonl")
		},
		"outbox outside project": func(in *control.EngineerBoardWatchRegisterInput) { in.OutboxPath = filepath.Join(outside, "e.jsonl") },
		"dotdot escape":          func(in *control.EngineerBoardWatchRegisterInput) { in.OutboxPath = f.project + "/docs/../../e.jsonl" },
		"symlinked directory":    func(in *control.EngineerBoardWatchRegisterInput) { in.OutboxPath = filepath.Join(link, "e.jsonl") },
		"symlinked file": func(in *control.EngineerBoardWatchRegisterInput) {
			in.OutboxPath = filepath.Join(f.project, "docs", "sym.jsonl")
		},
		"inside .git": func(in *control.EngineerBoardWatchRegisterInput) {
			in.OutboxPath = filepath.Join(f.project, ".git", "e.jsonl")
		},
		"not jsonl": func(in *control.EngineerBoardWatchRegisterInput) { in.OutboxPath = f.owner },
		"a directory": func(in *control.EngineerBoardWatchRegisterInput) {
			in.OutboxPath = filepath.Join(f.project, "docs") + ".jsonl"
			_ = os.Mkdir(in.OutboxPath, 0o755)
		},
		"relative outbox":  func(in *control.EngineerBoardWatchRegisterInput) { in.OutboxPath = "docs/e.jsonl" },
		"relative project": func(in *control.EngineerBoardWatchRegisterInput) { in.ProjectPath = "tracker" },
		"auto provider":    func(in *control.EngineerBoardWatchRegisterInput) { in.Provider = control.ProviderAuto },
		"no session":       func(in *control.EngineerBoardWatchRegisterInput) { in.TargetSessionID = " " },
		"bad board id":     func(in *control.EngineerBoardWatchRegisterInput) { in.BoardID = "board one" },
	}
	for name, mutate := range cases {
		in := base
		mutate(&in)
		if _, err := PrepareRegistration(ctx, f.st, in); err == nil {
			t.Errorf("%s: registration was accepted", name)
		}
	}
	// A missing outbox in an existing directory is fine: the board creates it later.
	if _, err := PrepareRegistration(ctx, f.st, base); err != nil {
		t.Fatalf("valid registration rejected: %v", err)
	}
}

func TestRegistrationIsIdempotentAndConflictsAreExplicit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.register("director-1")
	prepared, _ := PrepareRegistration(ctx, f.st, control.EngineerBoardWatchRegisterInput{
		BoardID: "board-1", ProjectPath: f.project, Provider: control.ProviderClaudeCode, TargetSessionID: "director-1", OutboxPath: f.outbox,
	})
	same, created, err := f.st.RegisterBoardWatch(ctx, prepared)
	if err != nil || created || same.ID != first.ID {
		t.Fatalf("repeat registration = %#v created=%t err=%v", same, created, err)
	}
	prepared.TargetSessionID = "director-2"
	if _, _, err := f.st.RegisterBoardWatch(ctx, prepared); err == nil || !strings.Contains(err.Error(), "unregister it first") {
		t.Fatalf("conflicting registration err = %v", err)
	}
	// Existing lines at registration time are the baseline, not events.
	f2 := newFixture(t)
	f2.request("ev-before-registration")
	f2.register("director-1")
	f2.poll()
	if f2.allMessages() != 0 {
		t.Fatal("an event written before registration woke the director")
	}
}

func TestConcurrentCursorAdvanceIsRejected(t *testing.T) {
	f := newFixture(t)
	f.register("director-1")
	f.request("ev-1")
	_, err := f.st.IngestBoardWatchBatch(context.Background(), store.BoardWatchIngest{WatchID: f.watch.ID, ExpectedOffset: 9999, NewOffset: 10000, Now: f.now})
	if !errors.Is(err, store.ErrBoardWatchCursorMoved) {
		t.Fatalf("stale cursor err = %v", err)
	}
}

// The documented adapter fixture must stay a valid protocol example.
func TestSampleOutboxFixtureMatchesTheProtocol(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "review-events.sample.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 7, 11, 0, 0, 0, time.UTC)
	kinds := []control.BoardReviewEventKind{control.BoardReviewRequested, control.BoardReviewCanceled, control.BoardReviewRequested, control.BoardReviewResolved}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != len(kinds) {
		t.Fatalf("fixture has %d lines", len(lines))
	}
	for i, line := range lines {
		event, err := control.ParseBoardReviewEvent([]byte(line), "air-crisis-store", now)
		if err != nil || event.Kind != kinds[i] {
			t.Fatalf("line %d = %#v err=%v", i+1, event, err)
		}
	}
}
