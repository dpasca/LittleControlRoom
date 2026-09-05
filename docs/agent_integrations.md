# Agent integrations

LCR manages integrations through one UI-independent native-configuration service.
Help Chat, embedded engineers, and `/skills` (also `/integrations`) use the same
typed operations. Skills are instruction/script folders, MCP registrations
connect callable tools, and plugins are provider-specific packages. Installing
one does not imply installing its dependencies or connecting an account.

## Asking an agent

For example:

- “What MCP servers are configured for Claude Code in this project?”
- “Find a PDF skill, then install it for Codex at user scope.”
- “Add this documented MCP command to OpenCode for this project.”
- “Disable that skill for LCAgent.”
- “Check whether that MCP server starts and lists tools.”

The agent discovers the `integrations` query/control domains using LCR's existing
progressive catalogs. `integrations.list` returns provider/scope, source paths,
supported actions, warnings, a revision, and a bounded page of entries.
`integrations.catalog` finds curated skill sources or the installed Codex CLI's
available plugin catalog. It does not promise cross-agent compatibility. MCPs
are added from their documented command or URL, not an invented universal catalog.

The agent then proposes `integrations.manage` with the inspected revision. Each
proposal requires an explicit provider and user/project scope; existing entries
also require the exact `entry_id` and matching `entry_name`. Supported actions:
`install_skill`, `install_plugin`, `add_mcp`, `set_enabled`, `remove`, `check_mcp`.
Use the discovered schema, not a copy of example arguments, as the API contract.

LCR displays a confirmation. No installation, configuration write, or MCP probe
runs merely because the agent proposed it. Embedded proposals are durable,
idempotent control operations; the originating engineer can inspect their
results on a later turn with `get_control_operation`. Help Chat receives its
result directly in Chat. Receipts include activation instructions and recovery
paths. The proposing agent need not be the provider being configured.

## Provider support

| Target agent | Skills | MCP registrations | Plugins |
| --- | --- | --- | --- |
| Codex | Inspect, install, toggle, recoverable removal | Add, toggle, remove, probe | Native install/remove, configuration toggle, catalog |
| Claude Code | Inspect, install, toggle, recoverable removal | Add, project toggle, remove, probe | Native install/remove, configuration toggle |
| OpenCode | Inspect, install, permission toggle, recoverable removal | Add, toggle, remove, probe | Inspect native JavaScript package configuration only |
| LCAgent | Inspect, install, visibility toggle, recoverable removal | General configurable MCPs not yet supported | No native plugin installer |

All four embedded providers and Help Chat can request supported management
operations for any target provider. LCAgent retains its existing native LCR tools
and LCR-managed browser; this feature does not add a general MCP client to it.

System skills, plugin-bundled skills, and the reserved `playwright` and
`lcr_runtime` connections are not directly editable. Manage a bundled skill via
its plugin. Cached Codex bundles are labeled `cached`/`bundled`, not asserted to
be installed or usable. OpenCode plugins are not treated as Codex/Claude bundles.
Native CLI versions must support the requested plugin command. Extra native
confirmation, missing dependencies, or sign-in requirements produce an explicit
failure/next step; LCR does not silently answer unseen native prompts.

## Sources and scope

User scope uses the canonical native home, not a per-launch overlay. Project
scope requires a loaded, visible project. User configuration can affect native
agent sessions outside LCR. Shared skill directories may be read by several
agents; removing their files affects every reader.

- Codex: user/project `.agents/skills`, legacy `CODEX_HOME/skills`, system and
  cached plugin skills; `CODEX_HOME/config.toml` and project `.codex/config.toml`.
  Skill visibility uses `skills.config`. Native plugin installs are user-scoped.
- Claude Code: `.claude/skills`, `skillOverrides` in settings, user MCPs in
  `~/.claude.json`, and project `.mcp.json`. Existing local project MCPs in
  `~/.claude.json` are inspected too. MCP enablement is a per-project disabled
  server list, not a global user toggle. New project skill/plugin settings use
  `.claude/settings.local.json`; existing project-scoped plugin removals retain
  their actual native scope. Native Claude removal preserves plugin data.
- OpenCode: user config/skills under `$XDG_CONFIG_HOME/opencode` (default
  `~/.config/opencode`), project `.opencode/skills`, and shared `.claude/skills`
  and `.agents/skills`. MCPs use `mcp` in `opencode.json[c]`; skill toggles use
  exact `permission.skill` entries. LCR launch overlays record their source root
  so the isolated MCP query process and TUI host inspect the same native files.
- LCAgent: shared `.agents/skills` and legacy Codex skill roots. LCR-owned
  visibility settings live in the app data directory at
  `lcagent/integrations.json`, or project `.lcagent/integrations.json`.

This is an inventory of standard user and selected-project sources, not a full
effective configuration resolver. Ancestor directories, administrator policy,
custom config overrides, native permission patterns/precedence, and LCR-injected
connections can change actual availability. Warnings say this explicitly.
Inherited entries generally require switching to user scope to mutate; Claude
allows project-local toggles of inherited MCPs.

## Activation, checks, and safety

Saved configuration is never reported as proof of a running session's tools.
Reconnect the affected engineer with `/reconnect` when it is idle. LCR does not
interrupt work, automatically restart sessions, or claim hot reload. Native
project trust and OAuth sign-in remain native/human steps.

An explicit MCP check starts a separate connection and performs initialization
and `tools/list` only. Local probes execute the configured server command, so
they require confirmation too. Checks support stdio and Streamable HTTP, are
time/output bounded, do not call tools, and clean up their process/session.
They return at most 100 tool names from the first page, not a full tool schema.
Legacy SSE transport and native OAuth credential-store reuse are not supported.
A probe requiring authentication does not establish that the engineer's existing
authenticated connection is broken.

No credential values are accepted in environment/header fields: specify names
of existing host environment variables. Do not put secrets in command arguments
or URLs. Native MCP arguments, credential values, and installer output are not
returned by inventory/receipts. Configuration backups may contain existing
secrets and are written with private permissions.

Configuration edits preserve unrelated settings, retain exact recovery copies,
and atomically replace the file after checking for concurrent edits. JSONC
comments are retained; the modified top-level TOML group is reserialized, while
unrelated groups are preserved. Unsupported layouts fail without overwriting
the original. Recovery files are kept beside the config as `*.lcr-*.bak` until
the operator removes them. Revisions reject stale proposals; unrelated Claude
usage metadata does not invalidate the integration revision.

Skill installation copies a self-contained local directory or an explicit Git
revision, with bounded file counts/bytes and no packaged symlinks. It does not
execute skill scripts or overwrite an existing skill directory. Git provenance
includes the resolved commit. Skill removal moves the folder to the app data
directory's `integration-backups`; moving it back restores its files. Visibility
settings may still need re-enabling. Native plugin removal uses its installer;
reinstall from the marketplace to restore the bundle.

## Dialog

Open `/skills` or `/integrations` in the dashboard or engineer pane. `Tab` changes
kind, `p` changes provider, and `s` changes scope. `a` opens an add/install form,
Space toggles, `d` removes, and `t` probes an MCP. Every action gets a separate
review/confirmation step. `v` opens the last full result, including recovery
paths; Enter opens full entry details and warnings, `c` copies the selected path,
and `r` refreshes. Busy dialogs ignore repeat
submission; Esc can hide a running operation without canceling it.

## Native references

Implementation follows the native formats and installed CLI help, rather than
introducing a second registry of enabled integrations:

- [Codex skills](https://learn.chatgpt.com/docs/build-skills)
- [Codex MCP](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)
- [Codex plugins](https://learn.chatgpt.com/docs/plugins)
- [Claude Code skills](https://code.claude.com/docs/en/skills)
- [Claude Code MCP](https://code.claude.com/docs/en/mcp)
- [OpenCode skills](https://opencode.ai/docs/skills/)
- [OpenCode MCP](https://opencode.ai/docs/mcp-servers/)
