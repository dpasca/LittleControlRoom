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

The local Build Week workspace also contains a recorder-enabled binary and a one-command launcher:

```sh
./dist/build-week-demo/start-daily-recording.sh
```

Before launching it, use `/settings` in the normal LCR instance to select OpenAI/GPT-5.6 for the main agent surfaces, GPT-5.6 Luna for background and utility inference, and disable the unfinished mobile surface. Then quit LCR normally so the recorder can acquire the normal database without enabling multi-instance mode.

The launcher stores a timestamped compact recording under `dist/daily-recordings/`. Terminal and tmux resize events are retained. After LCR exits, it automatically reviews every newly visible or changed text line with GPT-5.6 Luna using Structured Outputs and API storage disabled. Reports and proposed review/exclusion time ranges are written inside the `.lcrdemo/privacy-review/` directory. Set `LCR_SKIP_PRIVACY_AUDIT=1` only when a recording should remain local and unaudited.

The automated pass is screening, not final approval. Every clip selected for export still needs a last human check at full resolution.
