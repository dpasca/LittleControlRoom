# Submodules in LCR worktrees

This is built-in operating guidance, not a diagnosis of the current checkout.
Verify the live Git configuration and files before changing anything.

## Expected layout

LCR's default `submodules-auto` preparation reuses initialized root submodules
with `git worktree add --detach` at the parent's pinned gitlink commit. This
shares objects, refs, and repository configuration while keeping separate HEAD,
index, and working files. If reuse is unavailable, LCR can fall back to ordinary
`git submodule update --init --recursive` hydration.

A `.git` file pointing into `<root>/.git/modules/<module>/worktrees/<id>` is
therefore expected. It is not evidence of broken wiring. Check `commondir` and
the reciprocal `gitdir` pointer as well as configuration origins.

## Read-only diagnosis

Run commands from the actual submodule checkout, using `git -C <submodule>`:

```sh
git -C <submodule> rev-parse --show-toplevel --absolute-git-dir --git-common-dir
git -C <submodule> config --show-origin --show-scope --get-all core.worktree
git -C <submodule> config --show-origin --get extensions.worktreeConfig
git -C <submodule> status --porcelain=v2 --untracked-files=all
git -C <parent> ls-tree HEAD -- <submodule-relative-path>
git -C <parent> ls-files --stage -- <submodule-relative-path>
git -C <submodule> rev-parse HEAD
```

An absent optional config key exits 1. If the top-level directory is wrong,
compare with an explicit `--work-tree=<actual-submodule-path>` override. Using
`--git-dir` alone can inspect the wrong working directory and report false
deletions. A clean override is evidence about those files, not proof that the
normal checkout wiring is healthy. Check unstaged, staged, untracked, and gitlink
changes separately. Preserve real changes and do not stage apparent mass deletions.

## Configuration rules and repair

With `extensions.worktreeConfig` disabled, `core.worktree` in the common config
applies only to the canonical worktree. A relative value such as
`../../../asset-source` can be correct; do not resolve it against a linked
worktree's private administrative directory and call it corrupt.

With `extensions.worktreeConfig=true`, that exception no longer applies.
`core.worktree` must be moved out of the shared config into the canonical
worktree's `config.worktree`. Otherwise a linked checkout without its own
override may inspect the wrong directory. An empty linked `config.worktree`
does not override an incorrect shared value.

`git --git-dir=<linked-admin-dir> config core.worktree <task-path>` writes the
shared repository config. It is not a task-only fix. `git config --worktree`
also falls back to shared config unless the extension is enabled.

For an authorized repair, verify canonical ownership, reciprocal pointers,
extension state, config origins, and the actual files first. Preserve a backup
of affected configuration and check for concurrent Git writers/locks. When the
extension is already enabled, set or verify the canonical worktree's own
`core.worktree` before removing the stale shared value. Preserve sibling
overrides and validate normal status and top-level resolution in both the
canonical checkout and affected linked checkouts. Do not rewrite shared config
to a task path, disable the extension blindly, or manufacture per-task overrides
to hide a shared configuration mistake.

## Update and merge

LCR updates a reused submodule worktree directly to the parent's new gitlink,
rather than applying ordinary submodule hydration to that linked checkout.
Blind `git submodule update` on reused worktrees can rewrite shared metadata.
Even canonical submodule hydration can reintroduce a shared `core.worktree`
after a correct migration; recheck configuration scope after updating its commit.
Prefer LCR's worktree update/merge flow when available. A repair must preserve
gitlink commits, unpublished work, and all unrelated checkouts. Equal parent
gitlinks do not by themselves establish clean submodule contents.

Sources: `docs/worktree_prep.md`, `internal/worktreeprep/config.go`, and the Git
[worktree configuration documentation](https://git-scm.com/docs/git-worktree#_configuration_file).
