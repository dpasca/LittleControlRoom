# Repository evidence in session assessments

Session assessments combine the engineer transcript with current Git evidence.
LCR's commit, merge, and push actions refresh that evidence and queue the existing
background classifier when the snapshot changes. Periodic scans cover operations
performed outside LCR as well. The category and dashboard summary are regenerated
together; the original engineer conversation remains unchanged.

For linked worktrees, the classifier receives the recorded merge target and LCR's
integration status. Once integrated, it also receives that target branch's
ahead/behind state against its configured upstream. This lookup uses the target
ref even if the primary checkout is on a different branch. A source branch without
an upstream does not imply that integrated work still needs publication.

Remote evidence comes from local tracking refs; assessment does not fetch. Missing
upstreams, unavailable refs, and failed or timed-out Git reads leave target
publication unknown. Merged does not imply pushed. A specifically requested push
of the source branch is separate from publishing the merge target.

The model reconciles old commit/merge/push handoffs with these current facts using
the existing structured category, summary, and confidence schema. There are no
text-matching rules that mark a follow-up done. Explicitly requested rebuilds,
restarts, deployments, acceptance checks, and remaining implementation work
survive a merge, as do known defects and failed checks.

Routine closing reminders to restart the parent application or try the UI do not
keep an otherwise finished implementation task open. In particular, clean,
merged and published worktrees should not each retain a follow-up for the same
parent-application activation. The model distinguishes required work from these
reminders using task context, not phrases such as "still needs" or "must restart".
Optional offers to do extra work also do not require a user reply. A completed
assessment describes the delivered task; it does not claim that an unobserved
restart, deployment, or manual check happened. Genuine unfinished implementation
milestones and required user decisions retain their existing attention states.

Integration and target publication participate in the assessment snapshot hash.
Unchanged snapshots reuse the completed assessment, while changed evidence queues
a new attempt. Classifier policy versions also invalidate cached assessments, so
this policy applies to existing sessions without editing their transcripts or
manually clearing their assessments. If inference is unavailable or fails,
existing classification failure handling applies; Git actions do not fabricate a
completed assessment.

Regression coverage includes real local Git merge/push transitions and refresh /
scan hash consistency. Live semantic cases pair routine restart/visual-check
reminders with explicit requirements and cover unfinished labels, failed checks,
optional offers, required approvals, and provider blockers. Run them with:

```sh
LCROOM_RUN_LIVE_CODEX_HELPER_TEST=1 go test ./internal/sessionclassify -run '^TestCodexClassifierPostMergeFollowupsLive$' -count=1 -v
```
