# Verified worktree recovery

The reviewed `/clean` action automatically preserves nested repositories and
ignored working data when removing a live linked worktree. It does not contact
upstreams, ask for credentials, or infer disposability from package/cache names.
The existing primary-checkout, dirty-source, merge, activity, process, and
owned-submodule safeguards still apply. An explicit force request does not
bypass recovery verification or nested ownership checks.

## User flow

The cleanup report distinguishes removal, removal with recovery retained, and
blocked removal. A retained recovery shows its directory, journal phase, and
current allocated bytes. These bytes come from filesystem block counts,
deduplicated by device/inode, and include the manifest and temporary copies
when an operation is incomplete. They are not a logical-size estimate or a
promise about space freed elsewhere on the volume.

- **V — Review** re-verifies the recovery and opens its directory.
- **O — Restore** makes an independent workspace at `<old-path>.restored/tree`,
  with its own copied dependency stores. Existing destinations are refused.
  It does not overwrite live shared Git metadata or register a new LCR project.
- **P — Permanently delete** opens a separate confirmation. Only **Y** starts
  deletion; Escape cancels. Incomplete removals cannot be purged. A small
  idempotency tombstone remains after recovery data has been deleted.
- **R — Retry** resumes remaining operations with fresh checks. An interrupted
  relocation uses its durable journal; it does not merge or complete a TODO
  again. Completed TODO state, branches, and conversation history are retained.

After restarting LCR, `/clean` offers **V — review retained recoveries** from
the audit. Interrupted removal operations also become retry candidates when
their journal and project state allow safe resumption. Backup corruption and
incomplete evidence are reported as blockers; they are never a deletion fallback.

## Durable layout and verification

Recoveries live in `<LCR data directory>/worktree-recoveries/<path-hash>/`,
separate from task workspaces and the removal tree. The directory is private to
the current user. Recovery data is never automatically expired.

`manifest.json` records original paths, source identity, original file
inventories, Git object inventories/refs/HEADs, copied stores, metadata edits
with original and replacement bytes, Git registration moves, verification time,
operation phase, and allocated storage. Git configuration can contain secrets;
treat the recovery as private repository data.

Preservation copies all working files, including untracked/ignored nested data,
and the Git common/administrative stores required by the selected repositories.
Alternate object stores are followed transitively. Overlapping source roots are
collapsed before copying. File hashes, ownership, executable modes, regular-file
modification times, symlink targets, and extended attributes are compared by
reading the copy back. Special files and unsupported metadata stop preservation.

Copied Git pointers, `commondir`, in-tree local origins, `core.worktree`,
alternates, and absolute internal symlinks are repaired with recorded edits.
Copied registrations for unrelated checkouts receive inert local backpointers,
so a restored shared store cannot target those live working directories.
Verification runs with caller Git overrides removed, fsmonitor disabled, and
lazy fetching disabled. It compares every object ID (including unreachable and
borrowed objects), refs and HEAD, then runs full `git fsck`. Original data is
rehashed before relocation. This is a file/store recovery, not a refs-only bundle.

The source directory moves atomically to the journal's `QuarantinePath`.
Only exact, validated Git administrative registrations are relocated; there is
no family-wide prune and no recursive Git removal against the old pathname.
Recreated original paths block retries. The original files are then promoted to
the final `tree/` location, their recorded pointers are repaired, and the
independently verified duplicate is discarded. Thus ordinary cleanup never
recursively deletes the original files, including files held by a late writer.
After completion, the recovery retains one working tree plus required external
Git stores and small registration receipts, rather than two full working trees.

An OS file lease serializes recovery, restore, and purge actions across LCR
processes. Journal writes use fsync plus atomic replacement. File data and
directories are synced before verification is committed. Relocation, exact
registration detachment, original promotion, duplicate disposal, and purge have
durable retry boundaries. A partially copied file that fails verification is
retained and blocks further work; it is not silently accepted or overwritten.

## Conservative limits

- Atomic relocation currently requires the recovery area and source worktree on
  the same filesystem. Cross-filesystem relocation stops with both the source
  and verified copy retained.
- External-consumer checks cover Git linked-worktree registrations, the selected
  repository's containing directory, and LCR's known on-disk projects. Git has
  no global reverse index of alternates. Arbitrary unregistered repositories
  elsewhere on disk cannot be proven absent; this implementation does not claim
  a machine-wide dependency audit. Known outside consumers block removal rather
  than having their metadata silently rewritten.
- Metadata symlinks, quoted alternates, special files, unreadable dependencies,
  ownership that cannot be reproduced, and active/stale locks require review.
  Invalid shared submodule metadata also blocks removal; the preservation path
  does not silently rewrite a live primary submodule's `core.worktree` setting.
  macOS filesystem flags and extended ACLs are explicitly blocked because listxattr does not include
  them; Linux ACL xattrs participate in the normal copy/verification path.
- The recovery includes a full copy of required shared Git stores. It avoids
  overlapping source copies within an operation, but does not deduplicate objects
  across separate recovery operations or optimize the primary store to a minimal
  object pack.
- Legacy orphan directories without a new recovery journal retain the existing
  conservative retained-folder inspection. This change does not automatically
  reconstruct already-missing administrative history. Ordinary clean worktrees
  with neither nested repositories nor ignored files use the existing removal
  path; this change does not archive their otherwise-discarded per-worktree logs.
- Restore creates an independent inspection workspace, not an in-place undo of
  the primary checkout. It does not restore directory timestamps, access times,
  or hard-link relationships. The relocated originals
  retain their original filesystem metadata, but this is not a general-purpose
  filesystem backup tool.

## Validation

Synthetic fixtures exercise offline shared clones, unreachable objects,
uncommitted/untracked nested files, absent origins, multiple blockers, external
borrowers and symlinks, active writers, corruption, insufficient space, exclusive
leases, recreated paths, resumable relocation/promotion/disposal, preserved
branches and submodule registrations, independent restore after purge, restore
collisions, extended attributes, allocated-byte accounting, repeated removal,
and explicit UI purge confirmation. Existing worktree/cleanup safety tests remain
part of `make test`.

No cleanup is tested by deleting user projects. `make scan`, `make doctor`, and
the PTY `/clean` smoke check use a separate validation database. The original
2026-09-16 manual recovery artifacts are read-only implementation evidence.
