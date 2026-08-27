# Repository Home Branch

Little Control Room calls the original Git worktree the **primary checkout** and
groups it with its linked worktrees as one repository family. LCR can keep a
saved **home branch** for that primary checkout and warn when it moves to another
branch. The default is deliberately warn-only: detection does not block Git or
silently change branches.

The home branch is not a linked worktree's merge target. A stacked worktree may
correctly merge back into another feature branch while the primary checkout's
home remains `master`, `main`, or another repository default.

## Establishing the home branch

The home branch is stored as durable LCR policy. When `origin/HEAD` is available,
LCR prefers that repository-provided default. It does not assume that every
repository uses `master` or `main`. If no remote default is available, LCR can
fall back to the primary branch seen before it creates a linked TODO worktree or,
for older repository families, unanimous linked-worktree parent metadata.

Older inferred policies can contain a feature branch because LCR previously
treated a worktree's merge target as the primary checkout's expected branch. If
such an inferred policy produces a mismatch and `origin/HEAD` is available, LCR
upgrades the policy to that remote default. An explicit user choice is never
replaced automatically.

The dashboard compares that policy with the scanned primary checkout. A mismatch
adds a persistent home-branch warning to the whole repository family, including
its linked-worktree rows. Dismissing the warning suppresses attention only for
that exact home/current branch, dirty, and conflict state. A changed state warns
again.

## Incident dialog

Select any member of the repository family and press `I`, or run `/integrity`,
to open the incident dialog. The dialog shows the primary checkout path, saved
home and current branches, policy evidence, dirty/conflict state, proposed repair
path, and any recently detected workspace crossings.

The available responses are:

- **Ask Engineer** creates a durable task and starts a fresh embedded engineer
  using the project's preferred provider. Its first instruction is
  investigation-only: explain the cause and safest repair, then request explicit
  user confirmation before mutating files, branches, worktrees, Git metadata, or
  LCR state.
- **Restore Home** restores the saved home branch in the primary checkout and
  moves the current branch into a new linked worktree. LCR does not rename or
  delete either branch. It then applies normal worktree preparation, registers
  the new worktree, and inherits the root's saved run command.
- **Keep Current** explicitly changes the saved policy so the branch currently in
  the primary checkout becomes its home branch. It does not change Git.
- **Dismiss** acknowledges only the exact incident snapshot. This is the default
  selection, so opening the dialog and pressing Enter cannot mutate Git.

Pressing `Esc` closes the dialog and leaves the warning active.

## Safe-repair conditions

Automatic repair is offered only when LCR can verify all of the following:

- the primary checkout is still away from its home branch;
- the root has no uncommitted changes or unresolved conflicts;
- both the home and current branches exist locally;
- the home branch is not already checked out in another linked worktree;
- no relevant Git index or module lock is present;
- an unused sibling worktree path can be allocated; and
- no member of the repository family has a live embedded engineer or managed
  runtime in the current TUI.

If any check fails, repair stays unavailable and the dialog explains why. The
Ask Engineer handoff remains available for ambiguous or manually recoverable
cases.

## Embedded Codex workspace reminders

Every turn in an LCR-managed embedded Codex app-server session receives a
structured workspace contract containing the assigned path, canonical root, and
saved home branch. It reminds a primary-checkout session to propose a linked
worktree for isolated feature work, and reminds a linked-worktree session to ask
before crossing into another checkout.

For a Codex session assigned to a linked worktree, LCR also inspects structured
command items. If a command runs from the canonical root, LCR adds a visible
warning to the transcript and records an incident event for later explanation.
This remains advisory: the command is not blocked.

The reminder and structured command-crossing detector currently apply only to
LCR-managed embedded Codex app-server sessions. Standalone provider sessions,
other embedded providers, direct filesystem APIs, and activity between project
scans are not hard-enforced. Repository backups and ordinary Git care remain
important.
