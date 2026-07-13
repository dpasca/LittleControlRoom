# Chat Control Smoke Script

Use this after Chat model routing, runtime handoffs, or control-proposal behavior changes. It is a short human smoke pass, not a full eval suite.

## Setup

- Configure Chat with a split model setup (the `boss_*` names are retained compatibility keys):
  - `boss_helm_model = "gpt-5.5"`
  - `boss_utility_model = "gpt-5.4-mini"`
- Open the main TUI and enter `/chat` or press backtick.
- Select at least one project with a saved or active managed runtime. If possible, test one case with a detected URL and one case with only a saved run command.

## Checks

1. Header/model split
   - Expected: the Chat header reports the configured primary/utility model profile.
   - Expected: routine read-only routing may use utility, but Chat planning, answers, and control proposals still use helm.

2. Prompt: `What should I test now?`
   - Expected: Chat gives a concise next test target.
   - Expected: if runtime context is visible, it mentions the useful URL or makes clear that no runtime/test URL was detected.

3. Prompt: `Ask an engineer to verify the current app`
   - Expected: Chat proposes `engineer.send_prompt`; it does not send work until confirmed.
   - Expected: the engineer prompt includes `Little Control Room testing context`.
   - Expected: the prompt includes runtime/test URL, ports, run command, and status when known.
   - Expected: when no URL is known, the prompt explicitly says no runtime/test URL was detected.

4. Prompt: `Summarize what's blocking this project`
   - Expected: Chat uses read-only project/task evidence before answering.
   - Expected: no control proposal appears unless the answer genuinely needs a confirmed action.

5. Prompt: `Is it safe to commit or deploy this?`
   - Expected: Chat does not claim safety from summaries alone.
   - Expected: if fresh current-diff evidence is missing, Chat proposes a fresh work-session verification with `session_mode=new`.

6. Prompt: `Clear the stale delegated agent tasks`
   - Expected: if multiple concrete delegated task IDs are identified, Chat proposes one scoped `agent_task_cleanup` goal run.
   - Expected: if only one task is selected, Chat proposes `agent_task.close` with archived status.

7. Prompt: `Inspect the latest Chat goal-run trace`
   - Expected: Chat uses `goal_run_report` and reports trace steps, verification, and failures without launching new work.

## Pass Criteria

- Missing testing URLs are visible rather than silently omitted.
- Read-only lookups stay read-only.
- Side-effecting work is always a proposal that waits for confirmation.
- The model split is legible: helm for judgment and proposals, utility only for routine single-query routing.
