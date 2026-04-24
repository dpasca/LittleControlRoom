# Embedded Codex Slash TODO

Implemented already:

- `/new`
- `/model`
- `/status`

Next embedded slash-command backlog:

- `/fork`: wire to `thread/fork` and switch the pane to the new forked thread.
- `/resume`: expose recent/resumable threads directly from the embedded composer flow instead of requiring a separate picker step.
- `/permissions`: add an embedded preset/approval/sandbox picker that mirrors Codex expectations.
- `/personality`: add a local persona picker if the embedded transport exposes enough configuration to support it cleanly.
- `/apps`: show installed/available apps from `app/list`.
- `/mention`: add mention-aware suggestions for files, apps, and skills from the embedded composer.
- `/clear`: add a local transcript/composer clear action.
- `/copy`: add a local copy action for the latest response or selected transcript block.
- `/statusline`: add a configurable embedded footer/status-line view if we want closer Codex parity.

Autocomplete follow-up:

- Keep slash suggestions in sync with the implemented embedded commands so the visible completion list remains trustworthy.
- Add short per-command descriptions in the suggestion list once more commands land.
