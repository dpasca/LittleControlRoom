# OpenAI Build Week demo profile

Run the demo with a temporary OpenAI-only configuration and database:

```sh
OPENAI_API_KEY=... make build-week-demo
```

The launcher does not load the normal Little Control Room config. It selects GPT-5.6 for Chat and the embedded LCAgent, GPT-5.6 Luna for project reports and utility inference, disables the unfinished mobile surface, and removes model/provider overrides from the launched process environment. The temporary config, API key copy, and database are removed when the TUI exits.

By default, only this repository is included. For a multi-project recording, point the launcher at a comma-separated set of curated, presentation-safe roots:

```sh
LCROOM_BUILD_WEEK_INCLUDE_PATHS="/path/to/demo-projects,/path/to/another-safe-root" \
OPENAI_API_KEY=... \
make build-week-demo
```

Use only roots whose project names, Codex sessions, prompts, branches, TODOs, and diffs are safe to show on camera. The launcher isolates LCR's config and database, but deliberately reads the normal Codex artifact home so it can surface existing sessions under the selected roots.

In the setup/status view, the Project reports card names `OpenAI API / gpt-5.6-luna`, making the background model split visible before the workflow demo begins.

## Capture ordinary daily work

Run the normal TUI directly from the current source tree with recording enabled:

```sh
make tui-record
```

This uses the same config, database, project scope, Codex home, and interval flags as `make tui`; it does not depend on an installed or prebuilt binary. Before launching it, use `/settings` in the normal LCR instance to select OpenAI/GPT-5.6 for the main agent surfaces, GPT-5.6 Luna for background and utility inference, and disable the unfinished mobile surface. Then quit LCR normally so the recorder can acquire the normal database without enabling multi-instance mode.

By default, recordings are timestamped and stored under `~/.little-control-room/demo-recordings/`. Terminal and tmux resize events are retained. Override the directory or exact destination when needed:

```sh
make tui-record DEMO_RECORDING_DIR=/path/to/recordings
make tui-record DEMO_RECORDING_PATH=/path/to/session.lcrdemo
```

Recording is app-aware for categories marked private. Selecting a private
category tab, or showing an embedded agent session whose project is private,
stores a fixed `PRIVATE VIEW — NOT RECORDED` frame instead of the rendered
content. This substitution happens before capture and does not alter the live
TUI. It is a focused safeguard rather than a general redactor; public tabs and
projects can still display sensitive paths, prompts, diffs, or identifiers.

The GPT-5.6 Luna privacy reviewer remains a separate local prototype for now and is not automatically invoked by `make tui-record`. Promote it into a source-backed `lcroom demo audit` command before treating review as part of the durable recording workflow.

The automated pass is screening, not final approval. Every clip selected for export still needs a last human check at full resolution.
