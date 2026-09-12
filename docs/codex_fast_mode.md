# Codex fast mode

In an embedded Codex engineer, `/fast` or `/fast status` reports the shared setting.
Use `/fast on` to enable it explicitly and `/fast off` to select standard speed.
Fast mode uses credits/limits more quickly; the command does not change the model
or reasoning effort. Availability and rates depend on the model and account;
see [OpenAI's speed documentation](https://learn.chatgpt.com/docs/agent-configuration/speed).

The sidebar shows a red **FAST** row adjacent to model/reasoning. The engineer
header also shows the warning, including when the sidebar is hidden. Standard
speed is explicitly labeled **fast OFF**. Pending changes and unknown/error states
remain visible. A fast turn already running retains **FAST finishing · next OFF**
after disabling; `/pause` can stop that turn. Enabling during a standard turn shows
**FAST next · current OFF**.

## Shared state and persistence

The source of truth is `service_tier` in the resolved native Codex home's
`config.toml`, including the selected user profile's override. This is normally
`~/.codex/config.toml`, even when the helper uses an LCR home overlay. Writes use
Codex's `config/batchWrite` API against that original file, followed by a fresh
read to verify persistence. Enabling also persists `features.fast_mode = true`.
Disabling writes `service_tier = "default"`; omitting a tier is insufficient to
clear a loaded thread's previous selection.

All LCR Codex engineers using that home share this policy, including parallel
engineers. New and resumed threads receive the current policy explicitly;
historical thread settings never become the new shared default. A single
background poll per home notices native CLI/config changes, normally within a
second. Each helper synchronizes its loaded thread through `thread/settings/update`
and listens for `thread/settings/updated`. Config saves alone do not reload a
loaded Codex thread's service tier. Each new prompt refreshes and explicitly sends
the tier in `turn/start`, ordered against LCR setting changes. Review, compaction,
and goal activation also synchronize before starting inference.

Project-local service-tier overrides do not override this shared LCR policy.
Other native Codex processes can retain their own active-thread settings; LCR
cannot change or certify those running turns. Externally owned turns display an
unknown-state warning. Native processes opened later read the saved default.

The sidebar uses cached state only. RPC/disk failures remain visible and new
inference is blocked when synchronization cannot establish the selected tier.
Existing work is not interrupted automatically. After a failure, `/fast status`
retries synchronization; `/status` includes the shared tier and error details.

## Compatibility and validation

The app-server protocol was checked against locally installed Codex CLI 0.154.0,
including a credential-free probe of config writes, explicit standard/fast tiers,
and thread settings updates. This requires an app-server supporting these APIs;
unsupported settings calls surface as errors instead of silently leaving a tier
unchanged. The feature flag is enabled for LCR helpers to expose fast-mode support;
the shared tier still determines whether fast mode is requested.

Regression tests cover multiple loaded sessions, restart/resume with stale fast
history, disabling during an active fast turn, explicit standard-tier requests,
external config changes, malformed config, rejected writes and RPCs, nonblocking
snapshots, command routing, duplicate activation, and narrow sidebar/header labels.
These checks do not measure inference speed or account billing.
