# Nested Submodule Worktrees Plan

This is a planning and implementation note for making TODO worktree creation faster for large repos, especially repos like okmain where game source assets live in Git submodules.

Current implementation status:

- `submodules-auto` is the no-config default.
- Auto preparation creates nested submodule worktrees when the root checkout has, or can fetch, the pinned submodule commit.
- Auto preparation falls back to ordinary checkout hydration when the root submodule repo is not initialized or cannot provide the commit.
- Explicit `recursive-submodules` still forces the previous full recursive checkout behavior.
- Branch/push policy for dirty detached nested submodule worktrees, merge-back validation, and gitlink conflict resolution remain future phases.

## Current Baseline

LCR has generic worktree preparation:

- `Service.CreateTodoWorktree` creates a parent linked worktree, then calls `worktreeprep.Prepare`.
- With no repo config, the default profile is `submodules-auto`.
- The auto profile tries nested submodule worktrees first and falls back per path to:

  ```bash
  git -c protocol.file.allow=always submodule update --init --recursive -- "$submodule_path"
  ```

  inside the new parent worktree.
- The explicit `recursive-submodules` profile still runs full recursive checkout hydration for the new parent worktree.
- The TUI shows an active preparation status while this happens:

  ```text
  Creating dedicated worktree; preparing submodules if needed...
  ```

- `Service.RemoveWorktree` calls `worktreeprep.PruneSubmoduleWorktrees` so nested submodule worktree registrations can be cleaned up.
- `Service.CommitAndMergeWorktreeBack` already uses `ResolveSubmodulesAndPrepareCommit` before merging a dirty linked worktree back.
- `ResolveSubmodulesAndPrepareCommit` walks dirty submodules recursively, commits and pushes submodule changes, then prepares the parent commit that records the new gitlink SHA.
- After a parent worktree branch is merged back, `MergeWorktreeBack` runs recursive submodule update in the root checkout so root submodule working trees match the merged gitlink SHAs.

This baseline should stay. It is the safe generic fallback.

## Problem

Recursive submodule hydration can take 1-5 minutes for large repos with asset submodules. A new parent worktree only needs the submodule at the commit recorded by the parent gitlink. Re-cloning or re-checking out full submodule contents per task can be wasteful when the root checkout already has the submodule repository initialized locally.

Raw file copying is not a good solution:

- Copying only files does not create a valid submodule Git repository.
- Copying `.git` metadata can accidentally share an index/gitdir between checkouts.
- Symlinked submodule paths are not Git-clean and are unsafe when multiple worktrees may edit the same submodule.

The cleaner optimization is to use Git's own linked worktree machinery inside the submodule repository.

## Target Model

For each parent LCR worktree:

```text
okmain--task/
  Apps/TheFractalX/Assets/  # a nested Git worktree of the TFX_Assets submodule repo
```

The nested submodule working tree:

- is a real independent working tree and index,
- shares Git object storage with the initialized root submodule repo,
- starts at the exact submodule commit pinned by the parent repo,
- avoids fetching or hydrating the submodule from scratch when the commit is already available locally.

Conceptually:

```bash
parent_commit=$(git -C /path/to/okmain--task rev-parse HEAD:Apps/TheFractalX/Assets)
git -C /path/to/okmain/Apps/TheFractalX/Assets worktree add \
  --detach \
  /path/to/okmain--task/Apps/TheFractalX/Assets \
  "$parent_commit"
```

The exact branch mode needs design; see "Branch and push policy" below.

## Proposed Default Strategy

Do not remove the current recursive hydration fallback. Add an automatic strategy that tries nested submodule worktrees first and falls back to checkout hydration.

Suggested built-in profile naming:

- Keep `recursive-submodules` as the literal current behavior: run `git submodule update --init --recursive`.
- Add a new built-in default such as `submodules-auto`.
- Change the no-config default from `recursive-submodules` to `submodules-auto`.
- Keep aliases carefully:
  - `recursive-submodules` should remain literal for users who ask for exact Git submodule update behavior.
  - `submodules`, `all-submodules`, or `hydrate-submodules` can either remain recursive for compatibility or move to auto after a doc update. Prefer minimizing surprises.

Automatic strategy for each top-level submodule path from `.gitmodules`:

1. Resolve the pinned submodule commit in the new parent worktree:

   ```bash
   git -C "$new_parent_worktree" rev-parse "HEAD:$submodule_path"
   ```

2. Check whether the corresponding root submodule path is already an initialized Git repo:

   ```bash
   git -C "$root_path/$submodule_path" rev-parse --is-inside-work-tree
   ```

3. If the root submodule repo exists and has the required commit, create a nested submodule worktree at the submodule path inside the new parent worktree.

4. If the root submodule repo exists but lacks the required commit, fetch inside the root submodule repo and retry:

   ```bash
   git -C "$root_path/$submodule_path" fetch --all --tags
   ```

5. If the root submodule repo is not initialized, or the commit cannot be obtained, fall back to ordinary checkout hydration for that path:

   ```bash
   git -C "$new_parent_worktree" \
     -c protocol.file.allow=always \
     submodule update --init --recursive -- "$submodule_path"
   ```

6. Record preparation mode per path in `worktreeprep.Result`, for example `worktree` or `checkout`.

This keeps the default generic and safe:

- fast when the root checkout is already hydrated,
- still functional from a cold clone,
- no per-project config required.

## Branch And Push Policy

This is the main design point to settle before implementation.

The existing `worktree` profile creates nested submodule worktrees with `git worktree add --detach`. Detached worktrees are fine for read-only use, but they can make LCR's auto-commit-and-push flow harder:

- `ResolveSubmodulesAndPrepareCommit` commits inside dirty submodules and then calls plain `git push`.
- `pushAvailability` currently requires a remote and an upstream tracking branch.
- A detached submodule worktree usually has no branch and no upstream, so automatic push can fail.

Options:

1. **Detached by default, branch on first dirty resolution**
   - Create nested submodule worktree detached at the pinned commit.
   - If LCR later needs to auto-commit dirty changes inside that submodule, create a branch then.
   - Push with explicit refspec and set upstream:

     ```bash
     git -C "$submodule_worktree" switch -c "lcroom/<parent-branch>/<submodule-slug>"
     git -C "$submodule_worktree" push -u origin HEAD
     ```

   - This requires enhancing `resolveSubmoduleRepoAndPush` / `gitops.Push` to handle detached or no-upstream submodule worktrees.

2. **Create an LCR branch during preparation**
   - Create each nested submodule worktree on a unique branch immediately.
   - Branch name derived from parent branch plus submodule path.
   - This makes later auto-commit/push simpler, but creates remote branches even for read-only tasks if pushed later.

3. **Checkout the submodule default branch**
   - Not recommended as a generic default.
   - Git normally refuses checking out the same branch in multiple worktrees.
   - Using `--ignore-other-worktrees` risks concurrent edits on the same branch.

Recommendation:

- Start detached for correctness and minimal side effects.
- Add explicit support in the submodule resolver for creating a pushable LCR branch only when dirty changes actually need to be committed.
- Make the generated branch name deterministic, sanitized, and collision-resistant.
- Store or report the submodule branch name in warnings so users can find it later.

Open question:

- For okmain asset repos, decide whether LCR-created submodule branches are acceptable, or whether the workflow should push directly to the asset repo's main branch. Direct main-branch work is simpler for existing release flows, but less safe with parallel worktrees.

## Commit And Merge Semantics

Nested submodule worktrees do not change Git's parent/submodule model:

- Submodule file changes live in the submodule repo.
- The parent repo records only the submodule commit SHA as a gitlink.
- The parent merge carries the gitlink change, not the submodule file diff.

Desired LCR flow remains:

1. User edits parent files and/or submodule files in the task worktree.
2. User runs LCR merge-back, usually capital `M`.
3. `CommitAndMergeWorktreeBack` sees the source worktree is dirty.
4. It calls `ResolveSubmodulesAndPrepareCommit`.
5. Dirty nested submodules are recursively committed and pushed first.
6. Parent worktree commit records the resulting submodule SHA.
7. Parent branch is merged into root.
8. Root checkout runs submodule update so its submodule working trees match the merged SHAs.

Implementation must preserve this order.

Important failure cases:

- Dirty submodule cannot be pushed: stop before parent commit.
- Detached submodule has no branch/upstream: create a pushable branch or stop with clear guidance.
- Submodule commit succeeds but parent gitlink staging fails: stop and show exact path/command context.
- Root parent checkout is dirty before merge: keep existing block.
- Two parent worktrees update the same submodule pointer differently: may produce a gitlink conflict; see next section.

## Gitlink Conflict Resolution

A gitlink conflict means the parent repo has conflicting submodule commit pointers:

```text
base  = B
ours  = O  # root/target side
theirs = T # task/source side
```

Automatic resolver plan:

1. Detect unmerged submodule paths:

   ```bash
   git -C "$parent_repo" ls-files -u
   ```

   Filter entries with mode `160000`.

2. Extract stage SHAs:

   - stage 1 = base
   - stage 2 = ours
   - stage 3 = theirs

3. Ensure the submodule repository has all required commits.

4. Fast-forward cases:

   ```bash
   git -C "$submodule_repo" merge-base --is-ancestor "$ours" "$theirs"
   git -C "$submodule_repo" merge-base --is-ancestor "$theirs" "$ours"
   ```

   - If ours is ancestor of theirs, resolve parent gitlink to theirs.
   - If theirs is ancestor of ours, resolve parent gitlink to ours.

5. Divergent case:

   - Create a temporary submodule merge worktree at ours.
   - Merge theirs inside the submodule worktree.
   - If clean, commit and push the submodule merge commit.
   - Stage the parent gitlink to that merge commit.

6. If the submodule merge has content conflicts:

   - Launch or offer `/resolve` against the submodule merge worktree.
   - Prompt the engineer session with:
     - parent repo path,
     - submodule path,
     - base/ours/theirs SHAs,
     - instruction that this is a submodule-content merge,
     - instruction to produce a valid submodule commit.
   - After resolution, LCR commits/pushes the submodule merge commit.
   - LCR stages the parent gitlink to the new submodule merge commit.
   - LCR continues or instructs the user to continue the parent merge.

Do not automatically "take ours" or "take theirs" for divergent gitlink conflicts. That silently discards one side's asset/data update.

## `/resolve` Integration

LCR already has `/resolve` for selected repo merge conflicts and a submodule "resolve and continue" path for dirty submodules during commit preview.

Future integration should reuse that mental model:

- If the selected repo has normal file conflicts, keep current `/resolve` behavior.
- If the selected repo has gitlink conflicts, run deterministic gitlink resolver first.
- If deterministic resolution needs a submodule content merge and that merge conflicts, start a fresh engineer session in the temporary submodule merge worktree.
- Show the parent/submodule relationship in the prompt and UI status.
- After the engineer session resolves files, LCR should own the mechanical commit/push/stage-parent steps.

The AI should resolve semantic file conflicts inside the submodule, not decide parent gitlink policy from scratch.

## Cleanup

Nested submodule worktrees create metadata under the root submodule repo's `.git` directory, typically under a `worktrees/` area.

Current cleanup:

- `PruneSubmoduleWorktrees(ctx, rootPath)` lists root `.gitmodules` paths and runs `git worktree prune` inside initialized root submodules.

Future cleanup should:

- Keep the existing prune on parent worktree removal.
- Recurse into initialized submodules when nested submodules are supported.
- Prune temporary submodule merge worktrees created for gitlink conflict resolution.
- Optionally remove empty temp directories under an LCR-owned temp root.
- Never remove a submodule worktree that is dirty or not LCR-owned without explicit confirmation.

## Warm Worktree Pool

This is a separate but compatible optimization.

Instead of reusing arbitrary task worktrees, LCR could keep a small pool of LCR-owned prepared parent worktrees per repo root:

- clean only,
- no active session,
- no dirty parent or submodule state,
- reset/clean before assignment,
- TTL or disk budget,
- visible enough that users are not surprised.

This can coexist with nested submodule worktrees:

- warm parent worktree avoids parent setup cost,
- nested submodule worktrees avoid submodule hydration cost.

Do not silently repurpose normal user task worktrees as cache entries.

## Implementation Phases

### Phase 1: Auto Strategy Skeleton (implemented)

- Add a built-in profile for `submodules-auto`.
- Make no-config default use `submodules-auto`.
- Keep `recursive-submodules` as literal full hydration.
- Add result metadata that distinguishes `checkout` from `worktree`.
- Update docs and TUI status copy from "hydrating" to "preparing" where appropriate.

Tests:

- no `.gitmodules` is a no-op,
- cold root submodule falls back to checkout,
- initialized root submodule creates nested worktree,
- missing commit fetches then creates nested worktree or falls back cleanly,
- explicit `recursive-submodules` still runs checkout hydration.

### Phase 2: Nested Worktree Preparation (implemented for top-level prep)

- Generalize `prepareSubmoduleWorktree` for automatic top-level submodules.
- Ensure target paths are empty or absent before nested worktree creation.
- Preserve parent repo cleanliness after prep.
- Record prepared mode and commit per submodule.

Tests:

- parent worktree status remains clean after nested submodule worktree creation,
- submodule path is its own Git top-level,
- submodule gitdir points to a nested worktree under the root submodule repo,
- root submodule branch is not disturbed.

### Phase 3: Dirty Submodule Commit/Push Branch Policy

- Decide branch policy for dirty detached nested submodule worktrees.
- Enhance `resolveSubmoduleRepoAndPush` and/or `gitops.Push` to support the chosen policy.
- Add clear warnings when LCR creates/pushes submodule branches.

Tests:

- dirty detached nested submodule can be resolved into a pushable commit,
- parent commit records new gitlink SHA,
- push failure blocks parent commit,
- no-upstream behavior is clear and non-destructive.

### Phase 4: Merge-Back And Root Sync Validation

- Verify `CommitAndMergeWorktreeBack` with nested submodule worktrees.
- Ensure root submodule working tree syncs to merged SHA.
- Ensure removal prunes nested submodule worktree registrations.

Tests:

- edit nested submodule in task worktree, capital-M merge back, root submodule HEAD matches merged SHA,
- parent root repo is clean after merge,
- removed task worktree leaves no stale nested worktree registration after prune.

### Phase 5: Gitlink Conflict Resolver

- Add service-level detection for unmerged gitlink paths.
- Implement ancestry resolver.
- Implement clean submodule merge resolver.
- Add `/resolve` integration for conflicted submodule merge worktrees.

Tests:

- ours ancestor of theirs resolves to theirs,
- theirs ancestor of ours resolves to ours,
- divergent clean submodule merge creates/pushes merge commit and stages parent gitlink,
- divergent conflicted merge surfaces a resolve workflow,
- missing/unfetchable commits produce clear errors.

## Files To Start From

- `internal/worktreeprep/config.go`
  - `Prepare`
  - `prepareRecursiveSubmodules`
  - `prepareSubmoduleWorktree`
  - `PruneSubmoduleWorktrees`
- `internal/service/worktree.go`
  - `CreateTodoWorktree`
  - `CommitAndMergeWorktreeBack`
  - `MergeWorktreeBack`
  - `gitSubmoduleUpdateInitRecursive`
- `internal/service/submodule_resolve.go`
  - `ResolveSubmodulesAndPrepareCommit`
  - `resolveSubmoduleRepoAndPush`
- `internal/tui/todo_dialog.go`
  - worktree launch status and preparation handoff
- `internal/tui/app_actions.go`
  - submodule resolve-and-continue command path
- `internal/tui/worktree_ui.go`
  - capital-M merge-back action
- `docs/worktree_prep.md`
  - user-facing prep behavior

## Non-Goals

- Do not copy submodule working files as the primary optimization.
- Do not symlink submodule paths between parent worktrees.
- Do not auto-resolve divergent gitlink conflicts by always taking one side.
- Do not silently reuse arbitrary user task worktrees as cache entries.
- Do not require okmain-specific config for the generic path.
