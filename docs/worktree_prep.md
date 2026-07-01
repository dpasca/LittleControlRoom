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

## Merge-Back Gitlink Conflicts

When two parent worktrees update the same submodule pointer differently, Git can leave a gitlink conflict in the root checkout during merge-back. LCR now auto-resolves deterministic cases:

- If one side is an ancestor of the other, LCR stages the newer submodule commit.
- If both sides diverged but merge cleanly inside the submodule, LCR creates and pushes an LCR-owned submodule merge branch, then stages the parent gitlink to that merge commit.
- If the submodule content merge conflicts, LCR leaves a temporary submodule merge worktree in place and reports its path, branch, and ours/theirs SHAs for manual resolution.

## Cleanup

When LCR removes a linked worktree, it also prunes stale nested submodule worktree registrations from initialized root submodules. This keeps Git metadata tidy after parent worktrees containing `worktree`-mode submodules are removed.

## Profile Selection

LCR applies the requested profile when one is supplied by the worktree creation request. Otherwise it applies `default_profile` from `.lcroom/worktrees.toml`. If neither is present, it applies `submodules-auto`.

This gives LCR a generic hook for a later model-based chooser: the model can select among declared profile names, and the same deterministic preparer applies the selected profile.

## Future Work

See [`worktree_submodule_worktrees_plan.md`](worktree_submodule_worktrees_plan.md) for the handoff plan to make large submodule-heavy repos faster by reusing initialized root submodules as nested Git worktrees in new task worktrees.
