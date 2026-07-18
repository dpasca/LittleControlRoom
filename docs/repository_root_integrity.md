# Repository Root Integrity

Little Control Room treats a repository's canonical checkout and its linked
worktrees as one repository family. For families with a trusted expected root
branch, LCR warns when the canonical checkout moves to a different branch.
The default is deliberately warn-only: detection does not block Git or silently
change branches.

## Establishing the expected branch

The expected root branch is stored as durable LCR policy. LCR records it before
creating a linked TODO worktree. For existing repository families, LCR can also
bootstrap the policy from unanimous linked-worktree parent metadata, or from
`origin/HEAD` when linked worktrees exist and no stronger evidence is available.
It does not assume that every repository uses `master` or `main`.

The dashboard compares that policy with the scanned root checkout. A mismatch
adds a persistent root-checkout warning to the whole repository family, including
its linked-worktree rows. Acknowledging the warning suppresses attention only for
that exact expected/current branch, dirty, and conflict state. A changed state
warns again.

## Incident dialog

Select any member of the repository family and press `I`, or run `/integrity`,
to open the incident dialog. The dialog shows the canonical root, expected and
current branches, policy evidence, dirty/conflict state, proposed repair path,
and any recently detected workspace crossings.

The available responses are:

- **Ask Engineer** creates a durable task and starts a fresh embedded engineer
  using the project's preferred provider. Its first instruction is
  investigation-only: explain the cause and safest repair, then request explicit
  user confirmation before mutating files, branches, worktrees, Git metadata, or
  LCR state.
- **Repair Safely** restores the expected branch in the canonical checkout and
  moves the current branch into a new linked worktree. LCR does not rename or
  delete either branch. It then applies normal worktree preparation, registers
  the new worktree, and inherits the root's saved run command.
- **Use Current** explicitly changes the saved policy so the branch currently in
  the canonical checkout becomes the expected branch.
- **Keep** acknowledges only the exact incident snapshot. This is the default
  selection, so opening the dialog and pressing Enter cannot mutate Git.

Pressing `Esc` closes the dialog and leaves the warning active.

## Safe-repair conditions

Automatic repair is offered only when LCR can verify all of the following:

- the canonical root is still on the unexpected branch;
- the root has no uncommitted changes or unresolved conflicts;
- both the expected and current branches exist locally;
- the expected branch is not already checked out in another linked worktree;
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
expected root branch. It reminds a root session to propose a linked worktree for
isolated feature work, and reminds a linked-worktree session to ask before
crossing into another checkout.

For a Codex session assigned to a linked worktree, LCR also inspects structured
command items. If a command runs from the canonical root, LCR adds a visible
warning to the transcript and records an incident event for later explanation.
This remains advisory: the command is not blocked.

The reminder and structured command-crossing detector currently apply only to
LCR-managed embedded Codex app-server sessions. Standalone provider sessions,
other embedded providers, direct filesystem APIs, and activity between project
scans are not hard-enforced. Repository backups and ordinary Git care remain
important.
