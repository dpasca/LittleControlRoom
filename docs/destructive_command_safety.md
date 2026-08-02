# Destructive Command Safety

## Purpose

Little Control Room deliberately preserves broad, cross-directory agent access.
That access is increasingly important for real work across repositories, shared
assets, task folders, and user-managed tooling. Read and ordinary write access
therefore remain governed by each provider's normal permission mode.

The safety policy adds a narrower invariant: an LCR-managed agent should not be
able to launch a direct `rm` command as an ordinary model action, except for a
validated embedded-Codex cleanup rooted below `/tmp`. The primary failure being
addressed is a well-intentioned agent expanding the wrong path in a recursive
forced deletion, not a malicious process trying to escape containment.

## Threat Model

The guard assumes the agent is useful and fallible:

- It may form the wrong absolute path or expand a variable unexpectedly.
- It may put a cleanup command behind `sudo`, `env`, `command`, or a shell.
- It may launch cleanup through a managed background-process path instead of
  the ordinary bounded-command path.
- It is not assumed to be adversarial. A determined program with normal user
  permissions can use another filesystem API or deliberately bypass a wrapper.

Backups, snapshots, version control, and operating-system permissions remain
the recovery and containment layers. This feature is an additional seatbelt.

## Current Policy

| Provider path | Enforcement | Result |
| --- | --- | --- |
| Embedded Codex, PATH-resolved named `rm` | LCR-owned `rm` shim pinned through `shell_environment_policy.set.PATH` | Plain recursive forced cleanup is allowed only when every operand is a validated descendant spelled `/tmp/...`; every other invocation is rejected. |
| Embedded Codex, absolute executable or common simple wrapper | LCR-owned Codex `prefix_rule` with `decision = "forbidden"` | Forms that can bypass the guarded executable, including `/bin/rm`, `/usr/bin/rm`, `sudo rm`, and `env rm`, are rejected without an approval escape hatch. |
| Embedded Claude Code `Bash` | LCR-owned `PreToolUse` command hook backed by structural Bash/Zsh parsing | Direct `rm` is denied before execution in every permission mode, including YOLO's `bypassPermissions`. |
| LCAgent `run_command` | Structural Bash/Zsh parsing before command policy and execution | Direct `rm` is denied at every autonomy level. |
| LCAgent `start_process` | The same structural parsing before approval and process-broker launch | A Low approval or switch to Medium cannot bypass the denial. |

The Codex and Claude Code launch presets are otherwise unchanged. In
particular, the default `yolo` preset keeps its cross-directory read/write reach
and does not acquire a workspace-only sandbox.

## Codex Design

Every LCR-managed embedded Codex helper starts with a generated `CODEX_HOME`
overlay. Existing configuration, credentials, state, user rules, and
non-shadowed skills are preserved through the overlay, while LCR owns two
entries:

- `rules/lcroom-no-direct-rm.rules` contains the forbidden exec-policy rules.
- `bin/rm` contains the recursive-force guard shim.

The overlay bin directory is prepended to the helper environment and also set
through `shell_environment_policy.set.PATH`. The explicit Codex setting matters
because login-shell initialization and shell snapshots can otherwise reorder an
inherited `PATH`.

Codex command rules match argument prefixes and apply the most restrictive
matching decision, so they cannot express a path-aware exception beneath a
blanket `rm` prohibition. Named `rm` is therefore intentionally left to the
PATH shim, while native forbidden rules remain in place for absolute executable
paths and common wrapper forms that may bypass it.

The shim delegates to the system executable only when all of these conditions
hold:

- Both recursive and force options are present.
- At least one target is present, and every target is an absolute path spelled
  as a child of `/tmp`; `/tmp` itself is never accepted.
- No target has a trailing slash or `.` / `..` component.
- The nearest existing ancestor of every target's parent resolves physically to
  the canonical `/tmp` tree. This rejects escape through a symlinked parent
  while still allowing a nonexistent final target.

Mixed safe and unsafe operands reject the entire invocation before deletion.
Absolute executable paths and wrapper forms do not receive the exception. A
normal `rm -rf /tmp/<name>` command, including arguments expanded by the shell
before the shim runs, does.

Relevant implementation:

- [`internal/codexapp/codex_home_overlay.go`](../internal/codexapp/codex_home_overlay.go)
- [`internal/codexapp/session_runtime.go`](../internal/codexapp/session_runtime.go)

## Claude Code Design

Every LCR-managed embedded Claude Code process receives additive launch settings
through Claude's `--settings` option. Those settings register an LCR-owned
`PreToolUse` hook for the `Bash` tool. The hook uses exec-form arguments to call
the same running `lcroom` executable through a private CLI subcommand, avoiding
shell interpretation of the executable path.

The same per-process settings disable Claude Code's automatic commit and pull
request attribution. Git commits made from an LCR-managed Claude session therefore
retain the authorship supplied by the repository operator without an automatic
Claude `Co-Authored-By` trailer.

Claude sends the proposed Bash command to the hook as structured JSON before
permission processing. The hook applies the shared structural parser in
[`internal/commandguard/rm.go`](../internal/commandguard/rm.go). A direct `rm`
returns Claude's blocking exit code 2 with an actionable reason; ordinary
commands return success. Malformed or unexpected hook input fails closed.
Because `PreToolUse` runs independently of Claude's normal permission decision,
the guard also applies when LCR maps YOLO to `bypassPermissions`.

The settings are added only to the LCR-launched process. LCR does not edit the
user's `~/.claude` files or the repository's `.claude` settings. Existing user
hooks continue to load alongside the launch settings.

Relevant implementation:

- [`internal/claudehook/hook.go`](../internal/claudehook/hook.go)
- [`internal/codexapp/claude_safety.go`](../internal/codexapp/claude_safety.go)
- [`internal/codexapp/claude_session.go`](../internal/codexapp/claude_session.go)

## LCAgent Design

LCAgent uses the shared parser in
[`internal/commandguard/rm.go`](../internal/commandguard/rm.go). It walks a real
shell syntax tree rather than scanning text with keyword or regular-expression
heuristics. This distinction keeps commands such as
`printf '%s\n' 'rm -rf /'` usable while recognizing command chains,
substitutions, common execution wrappers, and literal nested shell scripts.

The guard is applied independently to:

- [`run_command`](../internal/lcagent/tools/command.go), including argv and
  shell forms.
- [`start_process`](../internal/lcagent/script/script.go), before any approval
  or managed-process launch.

Targeted file and patch tools remain the expected way for LCAgent to remove or
edit known files.

## Why DCG Is Not the Sole Codex Layer

[Destructive Command Guard](https://github.com/Dicklesworthstone/destructive_command_guard)
is a useful broader project with rules for destructive Git, filesystem,
database, cloud, container, and infrastructure commands. It is a strong
candidate for future provider-wide protection.

Its Codex integration currently uses `PreToolUse` hooks. OpenAI documents those
hooks as a guardrail rather than a complete enforcement boundary because
interception of `unified_exec` shell calls is incomplete. DCG documents the same
fail-open limitation in its
[Codex integration notes](https://github.com/Dicklesworthstone/destructive_command_guard/blob/main/docs/codex-integration.md).
For the narrow invariant here, LCR therefore uses native Codex command rules as
the primary layer instead of relying on a hook alone.

Primary references:

- [Codex command rules](https://learn.chatgpt.com/docs/agent-configuration/rules)
- [Codex hooks](https://developers.openai.com/codex/hooks)

## Guarantees and Known Bypasses

The implementation is intended to stop the common accidental command, including
the direct shape that motivated it. It does not guarantee that no deletion can
occur.

Known out-of-scope paths include:

- `find -delete`, language APIs such as `os.RemoveAll` or `shutil.rmtree`, and
  application-specific deletion tools.
- An absolute `rm` executable or dynamic command-name indirection hidden inside
  a complex script that the relevant structural or native policy layer cannot
  safely resolve.
- A command that deliberately replaces the guarded `PATH` before resolving
  `rm`.
- A Claude Code installation or configuration that disables hooks or fails to
  start the LCR hook executable. Claude hooks are a guardrail, not a separate
  operating-system enforcement boundary.
- Programs that unlink files internally rather than launching `rm`.
- Remote machines and external MCP tools unless those systems enforce their own
  policies.

Do not describe this feature as a sandbox or malicious-code boundary.

## Maintenance and Verification

When changing this policy:

1. Keep cross-directory access independent from the direct-command guard.
2. Preserve user Codex home entries; never overwrite the user's real `rules`,
   `bin`, or `skills` contents while building an overlay.
3. Keep the Codex `/tmp` exception fail-closed: require recursive force, reject
   the root and ambiguous paths, validate every operand, and resolve parent
   directories physically before delegating.
4. Use structural command parsing for Claude Code and LCAgent. Do not replace
   it with textual regex or keyword detection.
5. Test positive cases, wrappers, nested shells, dynamic targets, quoted
   examples that must remain allowed, mixed operands, and symlinked parents.
6. Validate the generated rule with `codex execpolicy check` when Codex is
   available.
7. Validate the generated Claude launch settings against an installed Claude
   Code CLI when available.
8. Run `make test`, `make scan`, and `make doctor` before merging.

The focused tests live beside the implementation in
`internal/claudehook`, `internal/commandguard`, `internal/codexapp`,
`internal/lcagent/tools`, and `internal/lcagent/script`.

The overlay or hook settings are created when the embedded provider starts.
After deploying a change, reconnect or start a new embedded Codex or Claude
Code session; an already-running helper retains the launch environment and
settings it started with.

## Future Extension

The next useful expansion should remain provider-aware:

1. Evaluate DCG or an equivalent native OpenCode integration as the secondary
   provider path.
2. Replace or supplement the Claude Code hook if a stronger native
   execution-policy surface becomes available.
3. Consider additional narrowly defined invariants such as destructive Git
   history rewrites, but measure workflow friction before enabling broad rule
   packs by default.
4. Add visible diagnostics showing which safety layers are active for a running
   embedded session.

Any broader policy should retain an honest threat model and should not imply
that command interception replaces backups or operating-system isolation.
