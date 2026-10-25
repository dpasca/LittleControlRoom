# Worktree removal and retained disk data

Removing a Git registration does not prove the checkout directory was removed.
The September 8, 2026 evidence report recorded two former okmain checkouts
occupying about 12 GiB together, with neither outer `.git` file nor outer
registration. Eight clean, registered asset-submodule worktrees still existed
inside them, alongside build and artifact output. The originals were manually
removed before this change; all regression fixtures use temporary repositories.

## Findings

Commit `6055a4b5` introduced a missing-checkout reconciliation path that pruned
the outer registration and recorded `PresentOnDisk=false` even if descendants
kept the directory occupied. Subsequent verification retained this exception.
This is a reproducible false-success/hidden-residue path, not proof of which
historical operation created the reported folders. Earlier partial-removal
handling already verified residual tracked files. Interrupted Git operations
and later process output remain separate possibilities.

## Removal contract

- Completion requires the outer registration and directory to be absent, with
  targeted child registrations checked before and after outer removal.
- Before Git mutations, `worktree_removal_started` events preserve parent path,
  repository, commit and branch evidence. A child-removal receipt adds each
  child's exact repository, path, administrative directory, commit and branch.
  These are provenance receipts, not backups of uncommitted or ignored files.
- Owned children require a gitlink in the preserved parent tree, a matching
  canonical module object store, reciprocal pointer identity, an exact unlocked
  live registration, and clean status including untracked files. Force removal
  does not bypass child checks. Children are removed with Git without force.
- Empty submodule placeholders are restored before normal outer removal so Git
  can still reject concurrent parent changes. Shared module stores and branch
  refs are preserved. Pruning remains administrative reconciliation; it cannot
  delete present children. Pruning is blocked when private module stores exist
  inside worktree administrative directories.
- Absent outer metadata does not authorize recursive deletion. A retained path
  remains a forgotten linked-worktree record with physical presence true, so
  the orphaned row stays visible. Errors include the retained path, logical
  bytes (explicitly incomplete if measurement failed), reason and cleanup route.
  `worktree_removal_incomplete` records persist failures with remaining residue.
- Scanning also rediscovers physical residue at previously removed paths without
  reviving it as an ordinary project. A later writer cannot retroactively alter
  a successful removal receipt, but its new files become visible residue.
- Ordinary symlinks inside a checkout, including pnpm dependency links and
  links inside owned child worktrees, are leaf entries. Inspection does not
  follow them, and removal unlinks them without deleting their targets. External
  and dangling targets are supported. Symlinks at the worktree root or in
  `.git` metadata still block removal.

## What counts as orphaned

The store keeps one coarse predicate: a forgotten linked-worktree record whose
path still exists. Some healthy paths match it, so the service drops them before
the list is built (`orphanedWorktreeSummaries`), using one `git worktree list`
per repository:

- A live, registered checkout inside the workspace of a task that is not in
  Trash. LCR force-forgets everything under its internal workspace root, and
  agents may create linked worktrees inside their own task workspace; the task
  row already represents that checkout. Once the task is in Trash the checkout
  is listed so it can be deleted.
- A plain subfolder of a live checkout that Git does not register separately,
  such as a session `cwd` that was recorded as a derived project.

Unreadable Git state keeps a row listed. Real orphans come from checkouts
removed outside LCR's delete flow (Finder, `rm`, an interrupted delete): Git
still lists the outer path as `prunable`, and what is left on disk is some mix
of `.DS_Store`, ignored output and tracked files. A `.DS_Store` can also be
re-created by Finder after the checkout is gone.

A hand-deleted checkout also strands the registrations of its nested submodule
worktrees in each submodule's shared store. During residue inspection such a
registration is ignored when Git reports it prunable, nothing exists at its
path, it is unlocked, and it still points at the commit the parent pinned
(`staleChildRegistration`). Any other nested registration still blocks cleanup.
Ignored registrations stay in the submodule store as inert metadata for Git to
prune.

## Reviewed cleanup

Select an orphaned row and use **x inspect** or `/remove`. Nonempty residue is
inspected asynchronously before **Clear residue** is offered. The dialog shows
logical bytes, child paths/commits, and the deletion policy. Ordinary removal
and batch cleanup do not implicitly authorize this broader cleanup.

The separate `CleanupRetainedWorktree` action verifies tracked contents against
preserved commit evidence, including the target text of tracked symlinks. It
also accepts regular output and symlinks ignored by the root
repository's current Git ignore rules, only after this explicit cleanup choice.
Untracked source or links, changed tracked files or links, unrelated repositories,
bare repository markers, metadata symlinks and special files block it. Missing
root module metadata and deeper nested repositories require manual review rather
than inferred ownership.

Cleanup snapshots entries, rechecks directory and file identity, pins filesystem
access with `os.Root`, removes only inspected entries, and requires empty
directories when removing ancestors. New output or changed files stop cleanup.
Partial progress is retained and retryable, including failures after some or
all child checkouts were removed.

`lsof` checks for open files and cwd holders before destructive checkout removal;
an unavailable or failed process check blocks it. Processes are reported, never
killed. The check is a point-in-time guard: it cannot prevent an independent
process from starting later. Git operations and subsequent scans still enforce
and report their respective postconditions. Sizes are logical file bytes, not
allocated blocks, which matters for sparse files.

## Regression coverage

The four-app asset fixture exercises normal, force and merge-finalization
removal, absent/prunable outer metadata, ignored build output, a sparse 3 GiB
artifact, dirty/untracked children, unrelated and bare repositories, dependency
and metadata symlinks, untracked/modified parent source, active cwd holders, permission failures,
mid-removal lock changes, ancestor replacement, new/changed output, and a later
scan after output recreation. Assertions check exact target registrations,
other live checkouts, preserved branches, durable receipts, physical absence or
visible retained state, and retry behavior. TUI tests cover inspection routing,
retained-size copy, and repeat-activation blocking.
Dedicated symlink fixtures cover pnpm paths, external and dangling targets,
preserved tracked links, modified or untracked links, stale/missing outer
pointers, and link replacement between inspection and deletion.
