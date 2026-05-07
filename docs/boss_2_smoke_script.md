# Boss 2.0 Smoke Script

Use this after Boss model routing, runtime handoffs, or control-proposal behavior changes. It is a short human smoke pass, not a full eval suite.

## Setup

- Configure Boss with a split model setup:
  - `boss_helm_model = "gpt-5.5"`
  - `boss_utility_model = "gpt-5.4-mini"`
- Open the classic TUI and enter `/boss`.
- Select at least one project with a saved or active managed runtime. If possible, test one case with a detected URL and one case with only a saved run command.

## Checks

1. Header/model split
   - Expected: Boss status/header says `Boss chat via gpt-5.5 (utility gpt-5.4-mini)`.
   - Expected: routine read-only routing may use utility, but Boss planning, answers, and control proposals still use helm.

2. Prompt: `What should I test now?`
   - Expected: Boss gives a concise next test target.
   - Expected: if runtime context is visible, it mentions the useful URL or makes clear that no runtime/test URL was detected.

3. Prompt: `Ask an engineer to verify the current app`
   - Expected: Boss proposes `engineer.send_prompt`; it does not send work until confirmed.
   - Expected: the engineer prompt includes `Little Control Room testing context`.
   - Expected: the prompt includes runtime/test URL, ports, run command, and status when known.
   - Expected: when no URL is known, the prompt explicitly says no runtime/test URL was detected.

4. Prompt: `Summarize what's blocking this project`
   - Expected: Boss uses read-only project/task evidence before answering.
   - Expected: no control proposal appears unless the answer genuinely needs a confirmed action.

5. Prompt: `Is it safe to commit or deploy this?`
   - Expected: Boss does not claim safety from summaries alone.
   - Expected: if fresh current-diff evidence is missing, Boss proposes a fresh engineer verification with `session_mode=new`.

6. Prompt: `Clear the stale delegated agent tasks`
   - Expected: if multiple concrete delegated task IDs are identified, Boss proposes one scoped `agent_task_cleanup` goal run.
   - Expected: if only one task is selected, Boss proposes `agent_task.close` with archived status.

7. Prompt: `Inspect the latest Boss goal-run trace`
   - Expected: Boss uses `goal_run_report` and reports trace steps, verification, and failures without launching engineer work.

## Pass Criteria

- Missing testing URLs are visible rather than silently omitted.
- Read-only lookups stay read-only.
- Side-effecting work is always a proposal that waits for confirmation.
- The model split is legible: helm for judgment and proposals, utility only for routine single-query routing.
