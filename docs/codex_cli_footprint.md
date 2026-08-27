# Codex CLI Footprint Discovery (Phase 0)

Date observed: 2026-03-05 (Asia/Tokyo)
Host: macOS user-home environment

This document summarizes *observed* Codex CLI on-disk artifacts from a real environment.

## 1. Storage locations

### User-home global state (primary)

Main directory:

- `~/.codex`

Observed key paths:

- `~/.codex/sessions/**/**/*.jsonl`
- `~/.codex/archived_sessions/*.jsonl`
- `~/.codex/history.jsonl`
- `~/.codex/log/codex-tui.log`
- `~/.codex/state_5.sqlite` (+ `-wal`, `-shm`)
- `~/.codex/sqlite/codex-dev.db`
- `~/.codex/shell_snapshots/*.sh`
- `~/.codex/worktrees/*/...`

### Project-local state

No project-local `.codex` directory was observed under scanned project roots in this machine.

Codex project association is still discoverable from global artifacts (mainly session logs containing `cwd`).

## 2. Session file formats observed

### Format A: modern JSONL (`session_meta`)

Observed in most files (1913/2053 sampled files):

- First line has `{"type":"session_meta", ...}`
- Stable fields in `payload`:
  - `id` (session/thread id)
  - `timestamp` (session start)
  - `cwd` (project working directory)
  - `cli_version`

Other line types include `response_item`, `event_msg`, `turn_context`.

Observed structured turn lifecycle markers under `event_msg.payload.type`:

- `task_started`
- `task_complete`
- `turn_aborted` (observed with `reason: "interrupted"`)

These are usable as a best-effort "latest turn completed" signal without parsing natural-language content. In particular, interrupted turns may end with `turn_aborted` and no later `task_complete`.

Embedded cold-resume also treats the latest matching `task_complete` or
`turn_aborted` marker as the durable lifecycle state for that turn. This keeps
a resumed app-server response or notification replay from temporarily
reclassifying the same settled turn as active; a different turn id still starts
a new lifecycle normally.

Observed recent conversational text usable for model-based "where was work left off?" classification:

- `response_item.payload.type == "message"` with assistant text parts
- `event_msg.payload.type == "user_message"` (`message`)
- `event_msg.payload.type == "agent_message"` (`message`)
- `event_msg.payload.type == "task_complete"` (`last_agent_message`)

`response_item` messages with `role == "user"` are model-context inputs, not
user-visible transcript events. They can contain injected `AGENTS.md`, skill,
permission, or environment context alongside the real prompt. User-facing
transcripts and classification input therefore take user turns from structured
`event_msg.payload.type == "user_message"` records instead.

Classification reads both a bounded head and tail of modern Codex rollouts when
the tail has no user turn. The head preserves the initial user-visible prompt
and early assistant updates when multi-megabyte structured tool results push
all conversational events outside the tail window; any newer conversational
tail events still take precedence.

### Embedded transcript link evidence

The embedded `Open Links` picker combines structured generated-image, viewed-image,
and file-tool paths with explicit Markdown links and concrete path-shaped text.
Conversational text may advertise paths in inline-code spans. Raw command results
may contribute explicit Markdown links, but standalone artifact paths printed by a
command do not become picker rows. Those paths are retained only as supporting
evidence for resolving an explicitly mentioned project-relative path to a known
absolute path. Language-level backticks in command results are not interpreted as
Markdown, and the command input itself is never scanned. Unexpanded template paths
such as `${fileName}`, comment-shaped lines, and absolute lines whose terminal
component contains only punctuation are source-code syntax rather than openable-link
evidence and are excluded.

Full-transcript discovery runs progressively outside the TUI render path. While
that scan is incomplete, its occurrences are reconciled one-for-one with links
already found in the visible viewport. This avoids showing the same transcript
occurrence twice without collapsing separate mentions that may carry different
labels or chronology.

### Format B: legacy JSONL

Observed in older files (140/2053 sampled files):

- First line has top-level fields like `id`, `timestamp`, `git` (no `type=session_meta`)
- `cwd` appears in early user `message` content under environment text:
  - `Current working directory: /path/...`

## 3. Files that update during active sessions

Observed to update while this active session was running:

- Active session JSONL file under `~/.codex/sessions/.../*.jsonl` (mtime increased between interactions)
- `~/.codex/state_5.sqlite-wal`
- `~/.codex/log/codex-tui.log`

Observed *not* to update for every tool call in this run:

- `~/.codex/history.jsonl` (updated on user prompt submission, not each tool command)

## 4. Parseable formats and stable identifiers

- JSONL
  - Session logs (`sessions`, `archived_sessions`)
  - History (`history.jsonl` with `ts`, `session_id`, `text`)
- SQLite
  - `state_5.sqlite` contains a `threads` table with:
    - `id` (thread/session id)
    - `cwd`
    - `rollout_path`
    - `title`
    - `git_sha`, `git_branch`, and `git_origin_url`
    - `agent_role` for spawned-thread evidence
    - `is_pinned`
    - `updated_at` / `recency_at` timestamp variants
    - `cli_version`
    - additional metadata columns
- Text logs
  - `log/codex-tui.log` (high-volume, useful as secondary signal)

Recommended stable identifiers for Little Control Room:

- `session_id` / thread id (from `session_meta.payload.id` or legacy first-line `id`)
- `cwd` (raw detected working directory; preserve this as provenance)
- Git top-level path for project ownership when `cwd` is inside a Git worktree
- file mtime for active session files

## 5. Practical detection strategy

Primary (filesystem-first):

1. Parse `~/.codex/sessions/**/*.jsonl` for `(session_id, cwd, started_at)`.
2. Canonicalize Git-backed `cwd` values to the containing worktree top-level for project ownership, while keeping the raw `cwd` as the detected path.
3. Use session file mtime as last activity signal.
4. Parse structured `event_msg.payload.type` lifecycle markers (`task_started`, `task_complete`, `turn_aborted`) to infer whether the latest turn completed.
5. For latest-session classification, read only a bounded tail of recent conversational events from the JSONL instead of reparsing full history.
6. Optionally scan recent output text for non-zero process exit markers.

Optional secondary accelerator:

- Read `~/.codex/state_5.sqlite` `threads` rows for quick latest `cwd` activity snapshots and recovery-oriented Git identity. LCR inspects the table schema before selecting optional columns so older Codex databases remain compatible.

## 6. Notes

- Session data volume can be large; avoid full-file deep parsing every poll.
- A compatibility parser should support both modern and legacy session JSONL layouts.
- Codex runs launched from repository subdirectories should not create separate LCR projects when Git identifies the same worktree top-level.

## 7. Runtime companion compatibility

On 2026-07-10, Codex CLI `0.144.0` was observed exposing the stable `code_mode_host` feature as enabled while the Homebrew cask installed only the main `codex` binary. The upstream release publishes `codex-code-mode-host` separately, and the mismatch causes tool calls to fail with `failed to spawn code-mode host` even though `codex app-server` itself starts normally.

For embedded Codex sessions, LCR performs a bounded startup preflight outside the TUI update/render path:

1. Check whether `codex-code-mode-host` is executable on `PATH` or beside the resolved Codex binary.
2. If it is absent, read `codex features list` and confirm that `code_mode_host` is both available and enabled.
3. Add `--disable code_mode_host` only to that LCR-managed app-server process and show a compatibility notice.

This fallback does not edit `~/.codex/config.toml`. Healthy installs keep the feature enabled, older CLIs without the feature are left unchanged, and raw host-spawn failures receive an actionable diagnosis instead of only opaque stderr.

The compatibility result is reused while both the resolved Codex executable and `config.toml` fingerprints remain unchanged. LCR also repairs stale `state_5.sqlite` rollout paths once after a successful pass per Codex home and per LCR runtime, rather than rescanning the complete thread table for every embedded session. Failed cleanup attempts are not cached, so transient SQLite locks can recover on a later launch.

## 8. LCR-managed workspace context

LCR-managed embedded Codex app-server sessions receive an application-context
entry on every turn. The entry records the assigned workspace, canonical
repository root, and trusted home branch for the primary checkout, and asks Codex
to request permission before crossing checkout boundaries. The home branch is
derived independently from a linked worktree's merge target, preferring
`origin/HEAD` unless the user explicitly chooses another policy. This context is
advisory and is not inferred from natural-language transcript text.

When a session assigned to a linked worktree emits a structured command item
whose `cwd` is inside the canonical root but outside the assigned worktree, LCR
adds a transcript warning and persists a repository incident event. The detector
uses the app-server command item's structured `cwd`; it does not claim coverage
for standalone Codex processes, direct filesystem tools, or commands whose
working directory is not reported. See
[`repository_root_integrity.md`](repository_root_integrity.md) for the warning and
repair workflow.

## 9. Deleted-worktree session recovery

Codex's global artifacts can outlive the checkout recorded in a thread's `cwd`.
LCR uses that fact for `/wt restore` without treating the SQLite index as the
conversation itself:

1. Retain missing linked-worktree rows that still have recorded project-session
   evidence instead of expiring those recovery tombstones.
2. Join those rows with Codex `threads` metadata by thread id. Also admit a
   missing `root-name--suffix` sibling when its path has no retained LCR row,
   keeping recovery scoped to the selected repository family.
3. Exclude rollout metadata that identifies a forked or sub-agent thread. Older
   or unavailable rollout metadata falls back conservatively to the indexed
   direct thread.
4. Require the exact recorded path to be absent. Reuse an existing local branch,
   or recreate a missing branch only when `git_sha` is a valid commit still
   present in the repository.
5. Refuse recovery when the branch is checked out elsewhere or a stale Git
   worktree registration is locked or names a different branch. A stale
   registration for the same path and branch may be repaired by Git's forced
   `worktree add` path.

After checkout creation, LCR runs its standard worktree-preparation profile,
restores retained branch/parent/TODO metadata, and asks embedded Codex to resume
the original thread id. The rollout JSONL remains the durable conversation
source of truth; `state_5.sqlite` supplies the path, recency, title, and Git
identity needed for recovery discovery and reconstruction.

Recovery cannot reconstruct uncommitted files that existed only in the deleted
checkout. The resumed conversation may still contain enough context to recreate
that work, but LCR does not present conversation context as a filesystem backup.

### Residual checkout directories

Removing a worktree can leave its former folder behind when Finder later writes
a `.DS_Store` file into the otherwise-empty directory. The path still exists,
but it has no `.git` entry and no longer appears in `git worktree list`.

LCR treats a `root-name--suffix` sibling containing exactly one regular
`.DS_Store` file as a residual linked-worktree directory rather than a
standalone project. It remains attached to the repository family as an orphaned
checkout warning. From the repository root, `x` or `/wt remove` offers guarded
cleanup across those warnings. Cleanup rechecks every directory and only calls
non-recursive file removal for `.DS_Store`, followed by non-recursive removal of
the now-empty directory. A symlink, an empty folder, or any additional entry
causes that folder to be kept untouched.

The same guard handles a Finder race during normal removal. If
`git worktree remove` unregisters the checkout but reports an error because its
final directory deletion encountered a newly created `.DS_Store`, LCR verifies
that the path is no longer registered and that the sole remaining entry is one
regular `.DS_Store` before finishing cleanup. A still-registered worktree or any
other residue preserves the original Git failure and remains untouched.

## 10. Deleted-worktree session cleanup

LCR's `/codex-gc` workflow audits global Codex storage without using a missing
directory alone as deletion authority. A read-only audit runs when the TUI or
server starts and then once per day; it only refreshes an in-memory report and
never deletes a thread. Opening `/codex-gc` runs another fresh audit unless it
is reopening an active background deletion or its unread completion report.

A root thread is eligible only when all of the following can be established:

1. Its saved absolute `cwd` is absent, and an exact retained LCR project row
   says that LCR forgot the same linked worktree after it disappeared.
2. The retained repository root still exists, neither the LCR record nor any
   member of the Codex thread tree is pinned, and no tree member is loaded by an
   LCR-managed Codex app-server. Archived LCR records and worktrees with an open
   project TODO are also excluded.
3. Every root and descendant has at least 7 days of inactivity, while the LCR
   worktree tombstone has been missing for at least 7 days.
4. The worktree is not on a conventional external-volume path or a different
   mounted filesystem from the configured Codex home.
5. Every indexed descendant has readable `session_meta` lineage, the same
   missing worktree `cwd`, and one regular rollout under `sessions/` or
   `archived_sessions/`. Ambiguous lineage, paths, files, roots, or database
   state exclude the whole tree.

The Codex index's `has_user_event` value is not cleanup authority. Current
Codex databases can leave that column at zero even when the rollout contains
structured user messages; cleanup instead establishes root and descendant
identity from the rollout's `session_meta` lineage.

The audit queries the complete thread index, but bounds rollout-file I/O to
threads whose `cwd` exactly matches a retained deletion record and descendants
identified by structured `agent_role` or `source.subagent.thread_spawn`
evidence. Indexed parent chains are cross-checked against rollout lineage;
uncertainty blocks the related candidate tree rather than forcing unrelated
root rollouts to be opened.

Eligible roots are grouped by deleted worktree. The preview reports thread and
worktree age, retained branch/parent metadata, Codex Git branch and commit,
spawned-descendant counts, the eligibility reason, and the logical byte size of
the rollout files that can be recovered. The preview revision includes the
selected tree identities plus rollout paths, sizes, and modification times.

Deletion is reachable only after selecting one or more worktree groups with
Space, or explicitly toggling all groups with `A`, opening a separate
permanent-deletion warning with Enter, and pressing `D`. LCR repeats the
complete audit and compares the preview revision before each group. It then
calls Codex app-server `thread/delete` for each selected root; the Codex API
performs the root-and-descendant cascade. Direct SQLite or rollout-file
deletion is not used.

After every app-server response, LCR verifies that every selected root and
descendant row is absent from `state_5.sqlite` and that each previewed rollout
file is absent. Progress and partial failures remain visible. Reclaimed space is
reported as verified logical rollout bytes only after those absence checks; it
does not claim filesystem block-level savings on sparse, compressed, or
copy-on-write storage. The deletion job runs off the TUI update path: `B` hides
it while `/codex-gc` reopens its progress or report. Esc cancels the active
app-server client and prevents later queued groups from starting. A cancellation
cannot restore a thread already deleted, so LCR gives the in-flight group a
separate bounded post-cancel verification pass and reports only bytes it can
still prove were reclaimed. Each destructive group also shares the repository
family's worktree-operation lock, preventing an in-process create or restore
from changing the missing-path evidence between the repeat audit and deletion.
