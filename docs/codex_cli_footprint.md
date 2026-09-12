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

Project runtime discovery excludes LCR executables running the `playwright-mcp`
subcommand, including pinned `embedded-helpers/lcroom-<SHA-256>` binaries. Their
browser audio bridge listens on a loopback TCP port while inheriting the embedded
session's project CWD; that listener is internal infrastructure, not a project
runtime. Actual `lcroom serve`/`tui` listeners and project servers launched by
embedded engineers remain discoverable. Helper processes remain eligible for
CPU/orphan diagnostics and expected-port conflict checks.

Primary (filesystem-first):

1. Parse `~/.codex/sessions/**/*.jsonl` for `(session_id, cwd, started_at)`.
2. Canonicalize Git-backed `cwd` values to the containing worktree top-level for project ownership, while keeping the raw `cwd` as the detected path.
3. Use session file mtime as last activity signal.
4. Parse structured `event_msg.payload.type` lifecycle markers (`task_started`, `task_complete`, `turn_aborted`) to infer whether the latest turn completed.
5. For latest-session classification, read only a bounded tail of recent conversational events from the JSONL instead of reparsing full history.
6. Optionally scan recent output text for non-zero process exit markers.

Linked worktrees discovered through Git can have no session recorded against
their own `cwd`, for example when an agent in another project creates and works
on them. For these checkouts, LCR uses the newest modification time of the
checkout's `.git` pointer and its private Git `HEAD`, `index`, and `logs/HEAD`
files as the last-activity fallback. This date is labeled Git in project detail;
it does not fabricate an engineer session or assessment. Shared repository
metadata and the admin `gitdir` back-pointer are excluded, and LCR's read-only
Git commands disable optional index refreshes so polling does not renew the age.
Missing required or unreadable metadata leaves the age unknown.

Git worktree expansion does not register submodule checkouts or their retained
merge worktrees as independent projects. Submodules are identified from Git's
superproject output and common metadata directory, including submodules of linked
parent worktrees. Previously auto-added, sessionless submodule rows are hidden on
the next scan without deleting their files. Manual registration, recorded or
currently detected sessions, pins, TODO history/origin, and run commands preserve
independently tracked submodule projects. Ordinary repositories named `Assets`
are unaffected.

`/clean` accepts these sessionless worktrees only when present, unpinned, merged
into the recorded parent, conflict-free, clean, and older than 24 hours. Git age
is refreshed before removal, and the host still excludes active engineers,
runtimes, and Git actions. A checkout with recorded session evidence continues
to require a completed latest turn and a completed `done` assessment.

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
cleanup across those warnings. Cleanup rechecks every directory and uses
non-recursive removal for the `.DS_Store` followed by the now-empty directory.
A symlink or any additional entry keeps this simple cleanup path blocked.

The same guard handles a Finder race during normal removal. If
`git worktree remove` unregisters the checkout but reports an error because its
final directory deletion encountered a newly created `.DS_Store`, LCR verifies
that the path is no longer registered and that the sole remaining entry is one
regular `.DS_Store` before finishing cleanup.

Finder can also create `.DS_Store` inside a nested directory while Git is
walking the checkout. Git may then unregister the worktree and stop partway
through deletion, leaving a stale root `.git` pointer plus an arbitrary subset
of tracked files. LCR exposes `x` and `/remove` for this partial-removal shape,
but performs the expensive verification only after confirmation and off the UI
thread. Under the repository Git-write lock it requires the stale `.git` target
to be a now-missing direct child of the expected common Git `worktrees`
directory, resolves the recorded current or initial branch, and recursively
checks every remaining regular project file and executable bit against that
commit. Missing tracked files are expected; nested regular `.DS_Store` files
and empty directories are allowed.

Cleanup remains fail-closed. An untracked, changed, unreadable, symlinked, or
special entry blocks removal, as do a missing branch and a foreign or live Git
pointer. After verification, LCR rechecks each filesystem entry and removes it
bottom-up with individual non-recursive calls. A new entry appearing during
cleanup makes directory removal fail instead of being traversed or deleted.
Repository-level cleanup keeps every unverified orphan untouched and reports
the number retained.

## 10. Deleted-worktree session cleanup

LCR's `/codex-gc` workflow audits global Codex storage without using a missing
directory alone as deletion authority. A read-only audit runs when the TUI or
server starts and then once per day; it only refreshes an in-memory report and
never deletes a thread. Opening `/codex-gc` runs another fresh audit unless it
is reopening an active background deletion or its unread completion report.

The cleanup form lists aligned project, remove-count, keep-count, and free-space
columns, with largest groups first by default. The Sort dropdown offers largest
first, oldest first, and name while preserving the highlighted row and selection. A background
filesystem inventory reports logical bytes for the whole Codex home, sessions
(including archived sessions), and other files. Session bytes outside eligibility
remain visible even when no worktrees qualify. This inventory does not follow
symlinks and labels incomplete reads as partial; logical sizes can differ from
allocated disk usage. Inventorying other files does not make them deletion candidates.

The **Clean up** dropdown chooses **Orphaned worktrees** or **Stale sessions**.
Stale cleanup adds an **Inactive for** dropdown: 7 (default), 14, 30, or 90 days.
Tab/Shift-Tab move keyboard focus, arrows navigate, Space selects rows, and Enter
activates the focused control. Focus has a blue accent and a visible cursor, so
it remains identifiable without color. Mouse clicks operate the same controls.
The previous numbered category and view-toggle shortcuts are removed. Selecting
the current option preserves selection; changing category or age runs a fresh
audit and clears selection. Closing a scan cancels it; superseded results cannot
replace a newer preview.

The separate **Storage breakdown** action shows whole-home totals, the amount
eligible under the current policy, and storage outside that policy, largest first.
It is read-only; Back/Esc returns to cleanup without losing selections. It
attributes inventoried session files through the thread index's exact rollout path
and `cwd`, groups them by working directory, and shows the retained repository root
when LCR has a deleted-worktree record. It excludes eligible rollout paths and counts
each remaining file once. Files without unique indexed ownership appear as
unattributed; non-session files are grouped by their top-level Codex-home entry.
This view does not infer ownership from filenames or make retained files selectable.

The default **Orphaned worktrees** category requires all of the following:

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

The orphaned audit queries the complete thread index, but bounds rollout-file I/O to
threads whose `cwd` exactly matches a retained deletion record and descendants
identified by structured `agent_role` or `source.subagent.thread_spawn`
evidence. Indexed parent chains are cross-checked against rollout lineage;
uncertainty blocks the related candidate tree rather than forcing unrelated
root rollouts to be opened.

Choosing **Stale sessions** runs a fresh read-only audit and clears
selection. This category covers existing local folders, including primary
checkouts that never used linked worktrees. It does not require a worktree
deletion record. Every tree member must meet the selected inactivity threshold
in both the thread index and rollout modification time, known unpinned state,
no LCR-loaded thread, and verified lineage and rollout paths. The newest root
session in each folder is always retained, even when old. Missing folders stay
outside this category. External volumes, ambiguous ownership, and uncertain
lineage remain excluded. The broader audit reads metadata for existing-folder
threads and indexed descendants, entirely off the UI path.

Stale rows show one folder each, with recoverable size and **REMOVE / KEEP**
counts including spawned sessions. Details and the permanent-deletion warning
show how many of the folder's indexed sessions will be removed; no hundreds-row
session list is required. Category identity and the total session count are
included in the preview revision, along with the inactivity threshold, and
revalidated before deletion. The selected threshold is applied again during
the repeat audit and the final index check, not just to the displayed estimate.
Immediately before a stale deletion, LCR checks the current session index and
rollout sizes/modification times again, and the TUI refreshes its managed loaded
thread IDs. This rejects activity, pins, or loads that changed during the audit.

Eligible roots are grouped by working directory. The preview reports remove/keep
counts including descendants, last activity, the exact highlighted folder path,
and logical rollout bytes that can be recovered. The preview revision includes the
selected tree identities plus rollout paths, sizes, and modification times.

Deletion is reachable only after selecting one or more worktree groups with
Space, the select-all control (or `A` while the table is focused), and opening
**Review cleanup**. The separate permanent-deletion review reports exact remove
and keep counts and defaults to **Back**. The user must explicitly focus and
activate **Delete N sessions permanently**, or click that button. LCR repeats the
complete audit and compares the preview revision before each group. It then
calls Codex app-server `thread/delete` for each selected root through at most
four persistent cleanup clients. Independent root trees can make progress
concurrently; each client handles one request at a time. The Codex API
performs the root-and-descendant cascade. Direct SQLite or rollout-file
deletion is not used.

After each successful root response, LCR checks that tree’s selected index keys
and previewed rollout files, then publishes reclaimed bytes within the current
group. Index verification uses bounded read-only primary-key queries rather
than loading the full thread inventory. A final pass checks the entire selected
group, including requests whose responses were lost. Missing or unreadable
indexes fail verification. Progress shows the current stage, completed and
in-flight root counts, verified reclaimed bytes, elapsed time, and time since
the last update. The background footer includes root counts and reclaimed bytes.
Snapshots are atomically published by the worker and rendering performs no I/O.
Reclaimed space is reported as verified logical rollout bytes only after those absence checks; it
does not claim filesystem block-level savings on sparse, compressed, or
copy-on-write storage. The deletion job runs off the TUI update path: `B` hides
it while `/codex-gc` reopens its progress or report. Esc cancels the active
app-server clients and prevents queued roots and later groups from starting.
An error in any worker also cancels the remaining workers and queue.
A cancellation cannot restore a thread already deleted, so LCR gives the in-flight group a
separate bounded post-cancel verification pass and reports only bytes it can
still prove were reclaimed. Each destructive group also shares the repository
family's worktree-operation lock, preventing an in-process create or restore
from changing the missing-path evidence between the repeat audit and deletion.

## Embedded fast-mode policy

LCR reads the shared speed policy from the resolved native Codex home's
`config.toml` (`service_tier`, plus a selected user profile override). Per-launch
home overlays and historical thread service tiers are not independent defaults.
The shared two-hour deadline is stored in `lcroom-fast-mode-timer.json` beside
that native config, independently of session history and home overlays.
Native config writes do not hot-reload loaded threads' service tiers; LCR explicitly
synchronizes them and passes the policy on new turns. This does not change artifact
session discovery. See [Codex fast mode](codex_fast_mode.md).
