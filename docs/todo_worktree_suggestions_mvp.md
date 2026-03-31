# TODO Worktree Suggestions MVP

This document defines the first implementation pass for TODO-driven worktree suggestions in Little Control Room.

The goal is to let a user start parallel work on the same repo without forcing manual branch naming every time, while preserving the current clean repo-centric UI.

## Goals

- Generate branch and worktree naming suggestions for TODO items using model-based structured output.
- Cache suggestions once requested so repeated dedicated-worktree launches are usually instant without speculatively generating names for every TODO.
- Keep the top-level dashboard repo-centric instead of turning each worktree into a noisy duplicate project row.
- Let the user inspect, edit, accept, or regenerate the suggested names before creating a new worktree.
- Keep local validation limited to safety and normalization, not semantic intent inference.

## Non-goals

- Automatic finish-and-cleanup flow that merges and removes a worktree in one step.
- Automatic worktree creation for every TODO.
- Regex or keyword heuristics that decide whether a task is a fix, feature, chore, or docs task.
- A large repo/worktree hierarchy redesign in the first pass.

## Product Model

Little Control Room should treat `repo` and `worktree` as different concepts.

- Repo: the stable user-facing project.
- Worktree: an execution lane for a specific task and branch.

For this MVP:

- TODOs stay repo-scoped.
- Notes, pinning, and snoozing stay repo-scoped.
- Worktree naming suggestions are stored per TODO.
- The existing TODO flow remains the main entry point for creating a worktree.

This keeps the user mental model simple: "I have one project with several possible parallel task lanes."

## User Flow

### 1. TODO creation or edit

When a TODO is created:

- do not generate a worktree suggestion yet

When a TODO text changes:

- clear any cached suggestion for that TODO
- wait until the user explicitly asks for a dedicated worktree again

### 2. On-demand suggestion generation

When the user switches the launch dialog to `Dedicated worktree` or explicitly presses `regenerate`, Little Control Room queues suggestion generation for that TODO.

Suggestions should be generated:

- asynchronously
- only for explicitly requested TODOs
- with enough project context to avoid vague names

### 3. TODO launch

When the user selects a TODO and chooses `Start in new worktree`, the dialog should display:

- suggested branch name
- suggested worktree folder suffix
- source state (`cached`, `generating`, `failed`)
- optional confidence and short reason

The user can then:

- accept
- edit
- regenerate
- cancel

### 4. Worktree creation

Once confirmed, Little Control Room creates the new worktree and starts the chosen embedded provider inside that worktree.

### 5. Worktree finish flow

When the task is done, Little Control Room should support a simple local-only finish path:

- record the parent branch that the worktree was created from
- let the user explicitly merge the linked worktree branch back into that recorded parent branch
- require the root checkout to already be on the recorded parent branch
- require both the root checkout and the linked worktree to be clean before merging
- allow unrelated sibling worktrees in the same repo family to stay dirty without blocking that merge
- keep worktree removal as a separate explicit action after merge

This keeps the workflow transparent: the worktree is just a lane, and the merge target is still an ordinary Git branch.

## AI Contract

This feature must follow the repo's policy against regex/keyword heuristics as primary logic.

The model is responsible for semantic interpretation of the TODO text. Local code is only responsible for:

- validating output
- normalizing unsafe characters
- enforcing length limits
- ensuring uniqueness

### Structured Output

Suggested schema:

```json
{
  "branch_name": "fix/diff-pane-empty-preview-scroll",
  "worktree_suffix": "fix-diff-pane-empty-preview-scroll",
  "kind": "bugfix",
  "reason": "The task describes correcting broken scrolling behavior in an existing UI flow.",
  "confidence": 0.91
}
```

Field expectations:

- `branch_name`: semantic identity for the new task lane
- `worktree_suffix`: folder-safe suffix for the worktree path
- `kind`: model-chosen coarse task type
- `reason`: short explanation suitable for UI/debugging
- `confidence`: model confidence from `0` to `1`

### Prompt Inputs

Recommended inputs:

- project name
- repo path basename
- TODO text
- a small set of sibling open TODOs for context
- optional current default branch when known

The prompt should ask for:

- short, readable names
- concrete task-oriented naming
- branch/worktree suggestions that are safe to further normalize locally

## Data Model

Add a new table for cached TODO worktree suggestions.

Suggested schema:

```sql
CREATE TABLE todo_worktree_suggestions (
  todo_id INTEGER PRIMARY KEY,
  status TEXT NOT NULL,
  todo_text_hash TEXT NOT NULL,
  branch_name TEXT NOT NULL DEFAULT '',
  worktree_suffix TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  confidence REAL NOT NULL DEFAULT 0,
  model TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL,
  FOREIGN KEY(todo_id) REFERENCES project_todos(id) ON DELETE CASCADE
);
```

Suggested status values:

- `queued`
- `running`
- `ready`
- `failed`

Add a matching model type, for example:

- `model.TodoWorktreeSuggestion`

## Invalidation Rules

A cached suggestion should be discarded when:

- the TODO text changes
- the user explicitly requests regeneration

A suggestion is still valid when:

- time passes but the TODO text is unchanged
- the repo remains the same

This keeps the cache useful without generating names for TODOs that may never be launched into dedicated worktrees.

## Background Worker

Use a dedicated background manager rather than folding this into session classification.

Responsibilities:

- process explicitly queued TODO suggestions
- persist `ready` or `failed` results
- avoid duplicate concurrent work on the same TODO

Suggested behavior:

- avoid startup backfills for every open TODO
- retry on explicit user request via `regenerate`

## Service Layer

Suggested service/store capabilities:

- load a TODO suggestion by TODO id
- clear a cached suggestion when the TODO text changes
- queue suggestion generation for one TODO on explicit demand
- ensure a valid suggestion exists when launching into a new worktree

Possible service entry points:

- `GetTodoWorktreeSuggestion(todoID int64)`
- `QueueTodoWorktreeSuggestion(todoID int64)`
- `DeleteTodoWorktreeSuggestion(todoID int64)`
- `EnsureTodoWorktreeSuggestion(ctx, todoID int64)`

`Ensure...` should prefer cached results and only wait synchronously when the user explicitly needs a suggestion now.

## TUI Behavior

### TODO list rows

The TODO dialog should only show a compact cached branch label under or beside the TODO when a ready suggestion exists:

- `branch: fix/diff-pane-empty-preview-scroll`

### Launch dialog

When starting a TODO into a new worktree, show:

```text
Start TODO — LittleControlRoom

TODO:
fix scroll weirdness in diff pane when preview is empty

Launch mode:
(•) Start in new worktree
( ) Start in current worktree
( ) Start in existing worktree

Suggested branch:   fix/diff-pane-empty-preview-scroll
Suggested folder:   fix-diff-pane-empty-preview-scroll
Source:             cached AI suggestion
Confidence:         0.91

Actions:
Enter start   e edit names   r regenerate   Esc cancel
```

Behavior notes:

- If cached suggestion is `ready`, use it immediately.
- If suggestion is `queued` or `running`, show progress and allow manual edit.
- If suggestion is `failed`, show the error state and allow retry or manual edit.

## Local Guardrails

Local code may:

- strip or replace invalid path characters
- collapse repeated separators
- trim leading or trailing punctuation or separators
- enforce max length for branch and folder suffix
- append uniqueness suffixes such as `-2`

Local code must not:

- infer task type from regex or keyword matching
- silently override a semantically clear model suggestion with hand-written rules

## Failure Handling

If the model call fails:

- keep the TODO usable
- store `failed` plus `last_error`
- let the user retry generation
- let the user manually edit a branch and folder name

If the model returns invalid output:

- attempt safe normalization when the intent remains clear
- otherwise mark the suggestion failed and expose retry/manual edit

The user should never be blocked from creating a worktree because suggestion generation failed.

## Rollout Plan

Recommended implementation sequence:

1. Add the new store table, model type, and stale/ready/failed persistence.
2. Add a dedicated background manager that generates cached suggestions.
3. Update TODO create/edit flows so creation stays cheap and text edits invalidate cached suggestions without speculatively enqueueing work.
4. Surface suggestion state in the TODO dialog.
5. Add the launch dialog path for `Start in new worktree` with accept/edit/regenerate.
6. Add actual worktree creation and embedded provider launch using the accepted names.

This keeps the first PRs small and makes it possible to test the naming/caching flow before wiring full worktree creation.

## Verification

Minimum verification for implementation PRs:

- store migration tests for the new table
- invalidation tests when TODO text changes
- on-demand queue tests for queued to ready or failed transitions
- TUI tests for ready, queued, and failed suggestion states
- launch dialog tests covering accept, edit, and regenerate

## Open Questions

- Should the launch dialog show the model's `kind`, or keep that internal?
- Should low-confidence suggestions be visually marked, or only exposed in details?
- Should the initial batching strategy be global or per project?
- Should duplicate TODOs across projects share prompt context rules, or remain fully isolated?
- When the worktree UI arrives later, should inactive clean worktrees remain hidden by default?
