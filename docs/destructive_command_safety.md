# Destructive Command Safety

## Purpose

Little Control Room deliberately preserves broad, cross-directory agent access.
That access is increasingly important for real work across repositories, shared
assets, task folders, and user-managed tooling. Read and ordinary write access
therefore remain governed by each provider's normal permission mode.

The safety policy adds a narrower invariant: an LCR-managed agent should not be
able to launch a direct `rm` command as an ordinary model action. The primary
failure being addressed is a well-intentioned agent expanding the wrong path in
a recursive forced deletion, not a malicious process trying to escape
containment.

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
| Embedded Codex, direct shell command | LCR-owned Codex `prefix_rule` with `decision = "forbidden"` | Direct `rm`, `/bin/rm`, `/usr/bin/rm`, and common simple wrapper forms are rejected without an approval escape hatch. |
| Embedded Codex, PATH-resolved child/wrapper command | LCR-owned `rm` shim pinned through `shell_environment_policy.set.PATH` | Recursive forced deletion is rejected; non-recursive uses delegate to the system executable so ordinary scripts retain more compatibility. |
| LCAgent `run_command` | Structural Bash/Zsh parsing before command policy and execution | Direct `rm` is denied at every autonomy level. |
| LCAgent `start_process` | The same structural parsing before approval and process-broker launch | A Low approval or switch to Medium cannot bypass the denial. |

The Codex launch preset is otherwise unchanged. In particular, the default
`yolo` preset keeps its cross-directory read/write reach and does not acquire a
workspace-only sandbox.

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

Codex command rules are the primary layer. Codex can structurally split simple
shell chains and apply the most restrictive matching rule to each command. The
PATH shim covers useful secondary cases such as a child process resolving
`rm -rf` by name. It intentionally delegates non-recursive invocations made by
child scripts; direct model-issued `rm` remains blocked by exec-policy.

Relevant implementation:

- [`internal/codexapp/codex_home_overlay.go`](../internal/codexapp/codex_home_overlay.go)
- [`internal/codexapp/session_runtime.go`](../internal/codexapp/session_runtime.go)

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
  a complex script that Codex cannot safely split.
- A command that deliberately replaces the guarded `PATH` before resolving
  `rm`.
- Programs that unlink files internally rather than launching `rm`.
- Remote machines and external MCP tools unless those systems enforce their own
  policies.

Do not describe this feature as a sandbox or malicious-code boundary.

## Maintenance and Verification

When changing this policy:

1. Keep cross-directory access independent from the direct-command guard.
2. Preserve user Codex home entries; never overwrite the user's real `rules`,
   `bin`, or `skills` contents while building an overlay.
3. Use structural command parsing for LCAgent. Do not replace it with textual
   regex or keyword detection.
4. Test positive cases, wrappers, nested shells, dynamic targets, and quoted
   examples that must remain allowed.
5. Validate the generated rule with `codex execpolicy check` when Codex is
   available.
6. Run `make test`, `make scan`, and `make doctor` before merging.

The focused tests live beside the implementation in
`internal/commandguard`, `internal/codexapp`, `internal/lcagent/tools`, and
`internal/lcagent/script`.

The overlay is created when an embedded Codex helper starts. After deploying a
change, reconnect or start a new embedded Codex session; an already-running
helper retains the environment it started with.

## Future Extension

The next useful expansion should remain provider-aware:

1. Evaluate DCG or an equivalent native OpenCode integration as the secondary
   provider path.
2. Keep Claude Code support tertiary and hook-based unless a stronger native
   execution-policy surface is available.
3. Consider additional narrowly defined invariants such as destructive Git
   history rewrites, but measure workflow friction before enabling broad rule
   packs by default.
4. Add visible diagnostics showing which safety layers are active for a running
   embedded session.

Any broader policy should retain an honest threat model and should not imply
that command interception replaces backups or operating-system isolation.
