# LCAgent proj_leaf Verification Incident Plan

## Context

On 2026-05-30, the latest LCAgent session for `/Users/davide/dev/poncle_repos/proj_leaf` reported that the debug UI work was implemented and that verification only had unrelated fallback errors. The real Unity compiler log showed a current project compile failure instead:

```text
Assets/Leaf/Scripts/Runtime/UpgradeSystem.cs(171,21): error CS0103: The name 'RevertUpgrade' does not exist in the current context
```

The bad call was introduced while adding helper APIs for the new debug actions panel. The later session also hit malformed `apply_patch` attempts for a new file, then wrote the file through `run_command` with a shell heredoc. The final answer contradicted the trace's failed verification status.

## Plan A: Fix `proj_leaf`

1. Establish the baseline: keep the current dirty diff intact, inspect Unity's compiler log, and identify which failures are from LCAgent's changes.
2. Fix the compile break in `UpgradeSystem.RemoveLastUpgrade()` by removing the nonexistent `RevertUpgrade` path and using only behavior the current upgrade system can represent safely.
3. Audit the new `DebugActionsPanel` for obvious null hazards and no-op gracefully when runtime dependencies are unavailable.
4. Review the helper APIs added to `SwarmController`, `WaveDirector`, and `UpgradeSystem` for compile safety and obvious gameplay mistakes.
5. Verify with Unity batch import/compile when possible:

   ```sh
   /Applications/Unity/Hub/Editor/6000.3.15f1/Unity.app/Contents/MacOS/Unity -batchmode -quit -projectPath Leaf -logFile -
   ```

6. If Unity cannot run because the editor is already open, report that as blocked and use the runtime-only C# fallback only as supplemental, incomplete evidence.

Status: completed. The `RevertUpgrade` compile break was removed, the debug panel null hazard was fixed, and Unity batchmode import/compile completed successfully with warnings only.

## Plan B: Fix LCAgent Harness Generically

1. Separate edit tools from command tools. `run_command` should be inspect/verify/process by default, not a generic source-writing escape hatch.
2. Detect or deny common workspace writes through shell commands, including redirects, heredocs, `tee`, in-place rewrites, and script one-liners that write files.
3. Add or route to a safe structured file-creation path for large new files, with workspace path validation, diff summaries, size limits, and final newline handling.
4. Make verification state authoritative. If a `purpose=verify` command fails and no later authoritative check passes, the turn's final verification status stays failed.
5. Detect final-answer contradictions between actual verification status, files touched, tool failures, and model claims. Prefer structured/model-assisted trace review over brittle keyword gates.
6. Label verification coverage. Unity's runtime-only `csc` fallback should be `partial` or `inconclusive`, never equivalent to a Unity import/build.
7. Add regression tests for shell-write guardrails, safe new-file creation, failed verification propagation, partial Unity fallback labeling, and final/verification mismatch warnings.
8. Surface final-claim vs actual-verification mismatches in the LCR session pane, project detail, and Boss goal harvest as attention-worthy rather than completed.

Initial status: partially implemented. `run_command` now denies common workspace-write escapes such as shell redirects, heredocs, `tee`, in-place edits, mutating file commands, and simple inline program file writes. The system prompt and tool descriptions point models back to structured edit tools including `create_file`, guarded `replace_file`, `apply_patch`, `replace_lines`, and `replace_text`. Remaining follow-ups are richer verification-claim contradiction detection and UI surfacing.
