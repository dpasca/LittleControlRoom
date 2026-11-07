# Owner-board click-to-wake

Status: implemented in this branch, uncommitted. TODO 1926 tracks follow-ups.

One explicit owner review or decision click on a registered owner board becomes
one durable engineer-mailbox message for one exact director session. Nothing
else wakes anyone: LCR never reads owner-answer files, so note autosaves,
answer rewrites and unrelated edits are invisible to it.

## Protocol

The board appends one JSON object per line to a registered **review outbox**
(an append-only `*.jsonl` file). Each line is strict: unknown fields, other
boards, other schemas, bad identifiers, a missing or far-future `created_at`
(more than 5 minutes ahead) and lines over 4096 bytes are ignored and counted,
never acted on. Identifiers use ASCII letters, digits and `. _ : -`, at most
128 characters. No free text from the file reaches the director; the wake text
is fixed and carries only `event_id`, `board_id` and `item_id`.

| `kind` | extra fields | effect |
| --- | --- | --- |
| `review_requested` | `item_id` | stores the event and queues one wake. Duplicate `event_id`s are ignored. |
| `review_canceled` | `target_event_id` | withdraws the request if its wake is not yet delivering. |
| `review_resolved` | `target_event_id` | records the board-reported review outcome; an undelivered wake is withdrawn. |

All lines carry `schema` = `lcr.board_review_event.v1`, `kind`, `event_id`,
`board_id` (must equal the registration) and `created_at` (RFC 3339).
`testdata/` has a sample outbox in `internal/boardwatch/testdata/`, and a test
keeps it valid.

The board must write a `review_requested` line only for an explicit decision or
review click, after the answer is saved, in a single `write` ending in `\n`
(LCR waits for the newline). It must never write one for a note autosave.

## Registration and control

- `engineer.board_watch_register` (operator confirmation required): explicit
  `board_id`, `project_path`, `provider`, `target_session_id`, `outbox_path`.
  An embedded session may bind only its own project and its own host-observed
  session. The outbox must be a regular `*.jsonl` file (or not exist yet)
  inside the project with real-path containment, no symlink, not under `.git`.
  The cursor starts at the file's current end, so earlier lines never wake
  anyone. An identical repeat is idempotent; a different binding for the same
  board must be unregistered first.
- `engineer.board_watch_unregister` (immediate; only removes authority): the
  explicit stop. Pending events are canceled, undelivered wakes withdrawn, and
  nothing is read again. Re-registering starts at the then-current end.
- `engineer.board_watch_status` query: watches, persisted cursor, integrity
  state, counters, last error, and the 20 most recent events per watch. The
  list is newest first and capped at 100 rows for display; pass an exact
  `watch_id` (from the registration receipt) to inspect one registration of the
  project in any state without paging. A watch of another project and a missing
  one give the same "not found" error. Polling never uses the display list: it
  pages through every active watch.

## Durability and delivery

`board_watches` holds the cursor (byte offset plus a head fingerprint) and
`board_watch_events` holds each event. Reading an outbox stores the events and
advances the cursor in one transaction guarded by the previous offset. Each
poll then queues a mailbox message for stored events that have none, using the
operation id `board-review:<watch>:<event>`, so a crash between any steps is
repaired by the next poll without a second message. Wakes are matched to events
by that operation id, not only by the stored link: every poll withdraws any
still-queued wake whose event is canceled, reviewed or expired, including a wake
created just before a crash and never linked, and unregistering or a cancel
never hides a wake that is already delivering. Linking is idempotent, so two
overlapping polls cannot cancel or duplicate a valid wake. The poll runs in the same
background command as the mailbox refresh (about once a second, and first thing
after startup), so a click recorded while LCR was stopped is queued on the next
start. It does not run while LCR is stopped or the Mac is asleep.

The message uses the ordinary mailbox: exact-session delivery, busy waiting,
`/handoff` successor forwarding (also for wakes already queued), and the
24-hour deadline, which is measured from the event's own `created_at` so a
restart does not extend it. A shorter outbox, a rewritten head, a deleted or
oversized-line outbox puts the watch in `attention` instead of guessing; it
stops until unregistered and registered again.

Status separates `state` (pending, queued, expired, canceled, reviewed) from
`delivery` (pending_enqueue, queued, queued_waiting, delivering, delivered,
failed, expired, canceled, not_sent). Registration awaiting confirmation is an
ordinary control operation (`get_control_operation`). Review completion is
only what the board reports with `review_resolved`.

## Authority

A wake requests review only. The director must reread the actual saved owner
state, including conditional notes. The wake grants no publication, browser or
other external-write authority, and an explicit stop or cancellation still
holds.

## Board adapter recipe (not applied; tracker is outside this change)

In the tracker's review bridge (`Tools/acceptance/review_requests.py` and the
board server), for a **decision click or explicit review button only**:

1. Keep a gitignored `<queue-stem>-review-events.jsonl` next to the queue file.
2. After the answer is durably saved, append one `review_requested` line
   (`event_id` = the existing review request id, so retries reuse it; `item_id`
   = the board item) with a single `O_APPEND` write.
3. On the existing `resolve` command append `review_resolved`; when the owner
   withdraws a pending request, append `review_canceled`.
4. Register once from the director session:
   `engineer.board_watch_register` with the board id, the project path, the
   director's provider and session id, and the outbox path. After `/handoff` no
   re-registration is needed: wakes follow the recorded successor.
5. Keep `wait_for_answer.py` only as an optional fallback; it is session-bound.

## Limits

LCR processes events only while it is running; a sleeping Mac or stopped LCR
catches up on the next start, within the 24-hour deadline. Only one outbox
writer format is supported. There is no operator dialog for watches yet (use
the status query), and boards that cannot append the outbox have no native
durable path.
