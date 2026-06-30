# Worktree Preparation

Little Control Room prepares newly-created linked worktrees by hydrating Git submodules automatically. After `git worktree add`, LCR runs the built-in `recursive-submodules` preparation before launching the embedded engineer session.

No repo-local setup is required for the default behavior. The built-in preparation runs:

```bash
git submodule update --init --recursive
```

inside the new worktree. If the repo has no `.gitmodules`, LCR leaves the worktree alone.

## Opt Out

A repo can disable automatic preparation with:

```text
.lcroom/worktrees.toml
```

```toml
default_profile = "off"
```

The profile-name aliases `"none"`, `"skip"`, `"disabled"`, and `"false"` are also accepted.

## Built-In Profile

The built-in `recursive-submodules` profile can still be named explicitly from a config file or worktree creation request. The aliases `submodules`, `all-submodules`, `hydrate-submodules`, and `recursive` are also accepted.

```toml
default_profile = "recursive-submodules"
```

This is equivalent to omitting the config file.

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

- `recursive-submodules`: built-in profile that runs `git submodule update --init --recursive` for the whole new worktree.
- `checkout`: runs `git submodule update --init --recursive -- <path>` inside the new worktree.
- `worktree`: reuses the initialized root submodule when possible, initializing or fetching only when needed, then creates a nested `git worktree add --detach` checkout at the submodule path inside the new parent worktree.

Use `checkout` for read-only or ordinary dependency submodules. Use `worktree` when the submodule contains large or writable source assets and should share Git object storage with the root checkout while keeping an isolated working tree for the task.

Submodule paths must be relative paths that stay inside the repo. LCR fails closed if a configured submodule worktree target already contains files.

## Cleanup

When LCR removes a linked worktree, it also prunes stale nested submodule worktree registrations from initialized root submodules. This keeps Git metadata tidy after parent worktrees containing `worktree`-mode submodules are removed.

## Profile Selection

LCR applies the requested profile when one is supplied by the worktree creation request. Otherwise it applies `default_profile` from `.lcroom/worktrees.toml`. If neither is present, it applies `recursive-submodules`.

This gives LCR a generic hook for a later model-based chooser: the model can select among declared profile names, and the same deterministic preparer applies the selected profile.
