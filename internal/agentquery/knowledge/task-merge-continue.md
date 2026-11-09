# Merging an accepted worker result, then continuing the same worker

Workers created with `repository_write=true` never commit. Their edits stay
uncommitted in the task's own checkout (branch `agent-task/<id>`) until the
caller merges them. This topic is the supported way to merge an accepted result
and still reopen the same worker session, with its context, for follow-up work.

## Supported flow

1. Review the result revision independently and record
   `agent_task.review_result` with `decision: accept`. The host recorded the
   handoff when the worker went idle: `repository.handoff_fingerprint` (exact
   state, including HEAD and index) and `repository.handoff_content` (the
   checkout's visible content relative to `repository.base_head`, including
   submodule checkouts and untracked files).
2. Make sure the worker is idle and `repository.state` is not `held`. Never
   commit in a checkout a worker currently holds.
3. In the worker checkout, commit exactly the accepted edits:
   - Commit inside each changed submodule worktree first. The submodule may
     move to a new branch; its commits must live in the shared module store so
     the parent's gitlink resolves.
   - Then commit the parent on the task branch it already has, including the
     new gitlinks.
   - Untracked files that were part of the handoff may stay untracked or be
     committed; either way their content is unchanged.
4. Merge the task branch into your own branch from your own checkout.
5. Propose `agent_task.continue` for the same task. LCR resumes the same worker
   if the checkout is either exactly the accepted handoff, or:
   - still on `repository.base_branch`,
   - with HEAD equal to or descending from `repository.base_head` (commits were
     only added, never amended, rebased or reset away), and
   - with visible content equal to `repository.handoff_content`.

   Index, `git status` and HEAD may differ. A checkout with no remaining
   changes at all is treated as a clean start and is not compared.

## What is still refused

Any content the reviewer did not accept fails closed: an extra or changed file,
in the parent or in a submodule, committed or not; a switched branch; rewritten
history. The failure names the reason, for example `content differs from the
accepted handoff` or `branch changed`, and as a failed operation it
requires a new user turn. Do not retry blindly or revert edits you did not
make. Either restore the checkout to exactly the accepted content and ask
the owner to continue again, or stop the task and brief a replacement.

Handoffs recorded before LCR captured `handoff_content` (the field is empty)
can be continued only in their exact uncommitted state. Merge those results
without committing in the worker checkout (for example, copy or apply the diff
into your own checkout), or accept that the next round needs a replacement task.

## Do not

- Commit, amend or reset while the worker holds the checkout.
- Run `git checkout` of a different commit in a shared submodule worktree to
  make the content match. To bring a reused submodule worktree up to the
  parent's pin, propose `git.submodule_align` (see the submodule-worktrees
  topic).
- Treat a refused continuation as permission to start a fresh task over the same
  checkout. Replacement tasks need `after_task_stopped` and a stopped
  predecessor.
