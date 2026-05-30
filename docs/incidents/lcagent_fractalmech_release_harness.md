# LCAgent FractalMech Release Harness Incident

## Summary

During a FractalMech release/verification session, LCAgent failed as an operational harness. The active user objective was to promote or upload the game to Play Store production and verify the production release, with browser verification specifically requested. The harness mixed stale compacted context with the latest request, substituted CLI/API and web evidence for the requested browser check, ran a long Play Store promotion through bounded `run_command`, and reported the result with ambiguous confidence after the command timed out.

## Bad Behavior To Prevent

- Stale compacted context competed with the latest user objective.
- Browser verification was requested, but no browser capability was reported as unavailable.
- CLI/API checks and web search were presented too close to browser verification.
- A publish/promotion operation ran through short bounded command execution and was killed on timeout.
- Final text blurred confirmed facts, attempted actions, timeouts, inferences, and blockers.
- A later phone-install request was mixed back into the Play Store release thread instead of becoming the active objective.
- Harmless inspection using `2>/dev/null` was denied as workspace-writing.

## Expected Harness Behavior

- Persist and trace the latest active objective for every fresh human turn.
- Treat compacted and resumed context as background unless it supports the latest active objective.
- Report unavailable capabilities, especially browser control, before using alternatives.
- Route deploy, publish, promote, upload, release, and rollout operations through long-running/managed process support when available.
- Verify production state after the operational action exits before claiming success.
- In final responses, separate confirmed facts, attempted work, failures/timeouts, inferences, and blockers.
- Allow stderr discard redirections to `/dev/null` while continuing to deny workspace file writes.

## Regression Coverage

This incident is covered by focused harness tests for active-objective persistence in thread state, capability/operational guidance in the system prompt, final-response evidence discipline in tool descriptions, and the command guard's handling of stderr discard versus real file redirection.
