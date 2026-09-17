# Worktree Preparation

Little Control Room prepares newly-created linked worktrees automatically before launching the embedded engineer session. After `git worktree add`, LCR runs the built-in `submodules-auto` preparation unless the repo config or worktree creation request selects another profile.

No repo-local setup is required for the default behavior. The built-in auto preparation checks each top-level submodule from `.gitmodules`:

- If the root checkout already has an initialized submodule repo with the pinned commit, LCR creates a nested `git worktree add --detach` checkout at that submodule path in the new task worktree.
- If the root submodule is initialized but lacks the pinned commit, LCR fetches in the root submodule repo and then creates the nested worktree when the commit becomes available.
- If the root submodule repo is not initialized or cannot provide the commit, LCR falls back to ordinary checkout hydration for that path:

  ```bash
  git submodule update --init --recursive -- <path>
  ```

If the repo has no `.gitmodules`, LCR leaves the worktree alone.

## Opt Out

A repo can disable automatic preparation with:

```text
.lcroom/worktrees.toml
```

```toml
default_profile = "off"
```

The profile-name aliases `"none"`, `"skip"`, `"disabled"`, and `"false"` are also accepted.

## Built-In Profiles

The built-in `submodules-auto` profile is the default when no profile is selected. The aliases `auto-submodules` and `auto` are also accepted.

The built-in `recursive-submodules` profile can still be named explicitly from a config file or worktree creation request to force the previous full checkout behavior. The aliases `submodules`, `all-submodules`, `hydrate-submodules`, and `recursive` are also accepted.

```toml
default_profile = "recursive-submodules"
```

## Custom Profiles

Custom profiles are optional. They exist for repos that want a narrower preparation step or want selected submodules checked out as nested Git worktrees.

```toml
default_profile = "minimal"

[profiles.minimal]
description = "Source checkout only; no optional submodules."
submodules = []

[profiles.assets]
description = "Prepare the asset submodule as a separate nested Git worktree."
submodules = [
  { path = "Apps/Demo/Assets", mode = "worktree" },
]

[profiles.vendor]
description = "Initialize ordinary read-only dependency submodules."
submodules = [
  { path = "externals/zlib", mode = "checkout" },
]
```

## Submodule Modes

- `submodules-auto`: built-in default profile that tries nested submodule worktrees first and falls back to checkout hydration per top-level submodule.
- `recursive-submodules`: built-in profile that runs `git submodule update --init --recursive` for the whole new worktree.
- `checkout`: runs `git submodule update --init --recursive -- <path>` inside the new worktree.
- `worktree`: reuses the initialized root submodule when possible, initializing or fetching only when needed, then creates a nested `git worktree add --detach` checkout at the submodule path inside the new parent worktree.

Use `checkout` for read-only or ordinary dependency submodules. Use `worktree` when the submodule contains large or writable source assets and should share Git object storage with the root checkout while keeping an isolated working tree for the task.

Submodule paths must be relative paths that stay inside the repo. LCR fails closed if a configured submodule worktree target already contains files.

Nested submodule worktrees start detached at the parent repo's pinned gitlink commit. If LCR later resolves dirty changes inside one of those detached submodules during commit-and-merge, it creates an LCR-owned branch such as `lcroom/<parent-branch>/<submodule>-<base-sha>` and pushes that branch with upstream tracking before preparing the parent gitlink commit.

When `/wt update` advances a parent linked worktree, LCR updates an existing nested submodule worktree directly to the new gitlink commit. It does not run the ordinary submodule checkout path against that nested worktree, because Git would otherwise rewrite the shared submodule `core.worktree` metadata and make the canonical checkout unreadable.

Agents can discover `knowledge.list` and `knowledge.get` in the LCR query catalog's
`knowledge` domain. The built-in `submodule-worktrees` topic explains this layout,
read-only diagnostics, and configuration scope. The versioned source is
[submodule-worktrees.md](../internal/agentquery/knowledge/submodule-worktrees.md).
Runtime MCP instructions and managed Codex, Claude Code, and LCAgent context point
agents to it before diagnosing submodule dirtiness or editing Git configuration.

When `extensions.worktreeConfig` is enabled, the canonical submodule's
`core.worktree` belongs in its own `config.worktree`, not the shared `config`.
A leftover shared value can make a linked checkout report false deletions even
when the canonical checkout remains clean. LCR's metadata repair writes or
verifies the canonical override before removing the shared value, preserves
sibling overrides, and does not enable or disable the extension. Nested
submodule preparation runs this repair before creating another linked checkout.
With the extension disabled, a correct relative shared value is left intact.
Canonical submodule hydration also runs the repair after Git updates the files:
Git itself can reintroduce the shared value when advancing a submodule commit.

If a clean linked worktree already records a detached nested submodule commit that is not reachable from a remote branch or tag, merge-back publishes that commit on an LCR-owned submodule branch before merging the parent worktree. This keeps the root checkout's post-merge submodule sync from failing on a locally-created gitlink commit.

If an LCR-generated remote branch already exists with divergent history, merge-back preserves that branch and automatically retries the publication under a collision-free branch suffixed with the intended submodule commit. User-owned branches are never forked this way. If the submodule remote still rejects publication—for example because authentication or write access is unavailable—merge-back stops before changing the root checkout and reports a submodule publish blocker. The blocker dialog offers a separate tracked engineer repair task with the full Git failure and merge context; before launching it, you can choose the engineer plus its model and reasoning preference. The task stays in the source project's category and remains linked to both the worktree and repository root. Selecting the generated `[A]` task and pressing `Enter` opens its engineer. Selecting either linked project shows a **Merge recovery** line and an `e recovery` footer action, so `e` reopens the same tracked session even when the generated task row is hidden by the current list filter. A `review` task state means the engineer returned and its result needs inspection; after confirming the blocker is resolved, select the linked worktree and press `M` to retry merge-back. The task is instructed to preserve the linked worktree and leave the root checkout unchanged for that retry. You can also push the submodule commit to a writable remote branch, point the parent worktree at a commit already available from the submodule remote, or configure a writable submodule remote before retrying manually.

## Merge-Back Gitlink Conflicts

When two parent worktrees update the same submodule pointer differently, Git can leave a gitlink conflict in the root checkout during merge-back. LCR now auto-resolves deterministic cases:

- If one side is an ancestor of the other, LCR stages the newer submodule commit.
- If both sides diverged but merge cleanly inside the submodule, LCR creates and pushes an LCR-owned submodule merge branch, then stages the parent gitlink to that merge commit.
- If the submodule content merge conflicts, LCR leaves a temporary submodule merge worktree in place and reports its path, branch, and ours/theirs SHAs. Running `/resolve` on the parent repo launches a separate background engineer session in that submodule merge worktree with instructions to resolve, verify, commit and push the submodule merge branch, and stage the parent gitlink.

## Cleanup

When LCR removes a linked worktree, it also prunes stale nested submodule worktree registrations from initialized root submodules. Merge-back and worktree update also repair canonical submodule `core.worktree` values recursively if an older sync path left one pointing at a removed linked checkout. This includes submodules inside submodules, and keeps a stale nested path from making root `git status` fail.

Ignored dependency clones and bare caches (for example, SwiftPM's `.build/checkouts/FluidAudio` and `.build/repositories`) can also be removed. This is verified from Git evidence, not a build-directory allowlist: the directory must be ignored by the parent repository, contain no preserved parent source or gitlink, have self-contained metadata and no linked worktrees, and have no modified, untracked, or ignored checkout files. Metadata symlinks, locks, custom hooks, and index flags that can hide edits block cleanup.

LCR checks the entire object inventory against objects reachable from currently advertised upstream refs. This protects unpublished commits, stashes, reflog-only history, annotated tags, and dangling blobs; local remote-tracking refs are not accepted as proof of publication. The inspection uses Git's [remote ref advertisement](https://git-scm.com/docs/git-ls-remote), [object inventory](https://git-scm.com/docs/git-cat-file), and [reachability traversal](https://git-scm.com/docs/git-rev-list). An origin pointing into the selected worktree is followed through its cache to an independent upstream. Missing or unverifiable upstreams stop cleanup, including force removal, with a retryable explanation. Remote probes are non-interactive, cancellable, limited to 20 seconds each, and shared by checkouts using the same cache during an inspection. No fetch or push occurs. An upstream whose newer refs are absent locally may require refreshing the dependency before retrying.

Before deletion, LCR records the clone HEAD and refs in the removal receipt and rechecks the complete filesystem snapshot for changes. The same verification applies to the separately confirmed retained-folder cleanup. Repositories with local work must be preserved or published before retrying; force removal does not bypass these checks.

Codex's curated plugin snapshot is a disposable cache with a different provenance contract: `HEAD`, its single shallow boundary, `refs/codex/curated-sync`, and its direct-commit `FETCH_HEAD` record must agree, and the recorded source must be `https://github.com/openai/plugins`. Only branch refs at that same commit are allowed alongside the sync ref, and every stored Git object must belong to the fetched snapshot. The other checkout, ownership, and revalidation checks above still apply. Such a cache is removed without contacting GitHub or preserving a backup; it does not need an `origin` remote or a currently advertised upstream tip. Additional local work or missing provenance still blocks deletion. A `.tmp` or `dist` name by itself never grants permission to discard a repository.

Ignored empty Git initializations are also removable without an upstream: they must have an unborn symbolic `HEAD`, no refs or object files, and pass the same ownership and clean-checkout checks. Staged files, dangling objects, and unfinished object/pack files prevent them from being treated as empty. Removal receipts identify both empty repositories and disposable Codex snapshots explicitly.

If merge-back succeeds but its selected cleanup fails, the UI reports the successful merge and incomplete cleanup separately, records the cleanup cause in `/errors`, and closes the merge confirmation instead of offering to repeat the completed merge.

## Git Lock Handling

Before write-side worktree operations, LCR checks for existing `index.lock` files. Merge-back checks the root and source checkouts and their populated submodules, including nested submodules; an unrelated sibling submodule worktree's private index lock does not block the merge. Both merge-back and its post-merge submodule sync wait up to three seconds for relevant locks to clear automatically. After the preflight wait, merge-back rechecks branch and dirty state so another Git writer's changes cannot invalidate the earlier clean-checkout checks. The wait runs in the background action and respects cancellation.

A persistent lock stops the action and reports its exact path. LCR leaves lock files untouched: neither a lock's age nor the absence of an open file descriptor proves its owner has exited. Remove a stale lock only after confirming the repo is idle. Other worktree operations retain their existing immediate lock checks. Git also enforces its own locks during writes, including when initializing a previously unpopulated submodule or when another writer starts after preflight.

Merge-back lock failures reopen the merge dialog with **Ask Engineer** selected, including locks encountered during post-merge submodule sync. The dialog preserves the chosen merge and cleanup options. Choose the provider, model, and reasoning preference to launch a tracked recovery task linked to the worktree and root project. The engineer receives the exact lock path and full failure, checks for active owners, proposes a targeted graceful stop when needed, and preserves a recoverable backup before clearing a verified stale lock. If ownership cannot be established, the task must present the operator with a concrete decision and backup plan rather than generic manual-removal advice. It must account for a merge that already landed, preserve staged work, and leave merge-back and cleanup to the operator. Reopen the task through the project's **Merge recovery** entry (`e`); after reviewing the repair, press `M` on the linked worktree to retry. Failed task creation leaves the recovery choices available, and repeated activation during submission cannot create duplicate tasks.

## Profile Selection

LCR applies the requested profile when one is supplied by the worktree creation request. Otherwise it applies `default_profile` from `.lcroom/worktrees.toml`. If neither is present, it applies `submodules-auto`.

This gives LCR a generic hook for a later model-based chooser: the model can select among declared profile names, and the same deterministic preparer applies the selected profile.

### Recovering failed stale cleanup

The `/clean` report keeps concise results and offers **↑↓** to select a worktree, **D** for full diagnostics (**PgUp/PgDn** to scroll), **E — Ask Engineer** for a failed removal, and **R** to retry remaining items with fresh eligibility and live-state checks. Completed removals are retained as receipts. Closing the report hides it; `/clean` reopens it. When all items are removed, **R** starts a new audit.

Ask Engineer uses the existing provider/model/reasoning picker and creates a tracked repair task linked to the worktree and repository root. The full failure and partial cleanup state are persisted with the task, so a completed TODO is not mistaken for an incomplete merge. Repeated handoffs reopen the existing task. **Cleanup recovery** in the project details and the **e recovery** footer reopen the engineer; after reviewing the repair, return to `/clean` and press **R**.

The engineer investigates nested dependency repositories and generated caches without assuming they are disposable. It can repair verifiable upstream access or prepare a durable, verified preservation plan for local files and Git objects, including unreachable history and borrowed objects. It leaves deletion to `/clean` and presents a concrete decision if credentials, process shutdown, or relocating preserved data require the operator's help. The removal safety checks remain in effect.
