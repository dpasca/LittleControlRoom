# Embedded Session Restart Continuity

Little Control Room distinguishes **reopening a conversation** from
**continuing interrupted work**.

Provider session artifacts preserve conversation history, partial assistant
output, and recorded tool activity. They do not preserve a helper process's
in-memory model generation, open pipes, pending RPC requests, or every external
tool's transient state. In Codex app-server specifically, `thread/resume`
reopens a thread so a later `turn/start` can append to it; it does not restart
the old turn by itself. See the [Codex app-server API overview](https://learn.chatgpt.com/docs/app-server#api-overview).

## Graceful LCR shutdown

When an exit action (`q`, `Ctrl+C` from the main dashboard, or `/quit`)
closes the TUI, LCR:

1. snapshots embedded sessions outside the Bubble Tea update/render path;
2. records only locally owned in-flight turns in
   `<data-dir>/embedded-sessions/restart-intents.json`;
3. asks each captured provider to mark its current turn interrupted; and
4. closes provider helpers and managed runtimes.

Marking the turn interrupted settles provider state; it does not add an
"aborted" message to the conversation. The helper's in-flight computation
would stop when its process closes either way.

The restart-intent file is written atomically with user-only permissions. It
contains provider, project path, session ID, active turn ID, and capture time;
the provider's own artifact remains the source of truth for conversation
content.

Sessions reported as active in another process (`BusyExternal`) are never
captured or interrupted by this flow.

## Startup restore

At the next launch, the Interrupted Turns dialog merges the restart journal
with artifact-based unfinished-turn detection:

- A `saved` row was owned and captured by LCR. **Continue All** reopens the
  exact provider session and starts a new continuation turn in the background.
- An untagged row comes only from provider artifacts. LCR reopens it for
  inspection but does not inject a continuation prompt or assume ownership of
  a possibly live external process.

Choosing **Skip** defers the saved continuation; the restart intent remains so
LCR can offer it again on a later launch. An intent is removed after its saved
session restores successfully.

LCR starts restored provider helpers one at a time in the background. Codex
thread resume can initialize credentials and configured MCP services before it
answers, so ordered startup avoids a concurrent startup burst while keeping the
TUI responsive. While this is happening, the top bar shows `RESTART x/y` and
each queued project's Agent column shows `<provider> warmup`. LCR asks the user
to wait and prevents a second manual open of that project until its scheduled
restore attempt settles. A final status reports whether every session restored
or some still need attention; failed saved continuations remain journaled for a
later retry.

The continuation prompt tells the engineer to re-check repository and external
tool state before acting and not to repeat side effects that may already have
completed. For Codex, if the captured turn is still reported `inProgress`, LCR
interrupts it only when its turn ID matches the journal, waits for the thread to
become idle, and then calls `turn/start`. If the captured turn completed during
shutdown, LCR leaves it completed and does not create a duplicate turn.

OpenCode and Claude Code reopen their saved session before receiving the
continuation prompt. LCAgent resumes from canonical thread state and starts a
new continuation run.

## Boundary of the guarantee

A graceful restart retains the engineer's conversation, repository state, and
ability to continue the task. It is not a byte-for-byte continuation of model
computation. A continuation may need to reconstruct a pending approval, rerun a
read-only check, or verify whether an external write completed before shutdown.

After a crash, `SIGKILL`, or power loss, LCR may not have written the latest
restart journal. Artifact detection can still offer the session for reopening,
but automatic continuation is intentionally withheld because LCR cannot prove
that it owned the old process.
