# Mobile Preview

A monitor-first web client for Little Control Room, meant for checking on running
agents from your phone while you are away from the terminal.

This is a **preview**. It is read-only by default, it does not replace the TUI,
and higher-authority controls stay on the desktop.

## Quick start

The mobile client starts with the main TUI by default:

```bash
lcroom tui
```

Then open <http://127.0.0.1:7777> on the same machine.

For phone access, run `/mobile` in the TUI. That panel shows the active listener,
the detected LAN URL, the current pairing code, and phone-control state. Press
`c` to copy the phone URL, or `Enter` to jump to Mobile setup.

## Reaching it from a phone

1. Open the Mobile card in `/setup`, or the Mobile section in `/settings`.
2. Choose `Phones on this LAN`.
3. Restart LCR. Listener changes only apply on the next launch.
4. Run `/mobile`, open the shown URL on the phone, and enter the six-digit code.

The three address modes are:

| Mode | Derived listener | Use for |
| --- | --- | --- |
| `This computer only` | `127.0.0.1:<port>` | Local browser only (default) |
| `Phones on this LAN` | `0.0.0.0:<port>` | Phone access over your own network |
| `Custom address` | exactly what you type | Advanced setups |

For a single run without changing saved settings:

```bash
lcroom tui --listen 192.168.0.6:7777
```

An explicit `--listen` also starts the mobile client for that run even when
auto-start is disabled.

Pairing grants that browser a 30-day HTTP-only device pass that survives LCR
restarts. The signing key is stored as `mobile-auth.key` beside the active
database, with owner-only permissions. Loopback listeners do not require pairing.

## What you can do

**Dashboard** — the portrait project list follows the main TUI's project-first
scan pattern: project name and summary stay prominent, with narrow assessment,
agent, and flag columns beside them. Categories are included.

**Live engineer channels** — TUI-hosted dashboards add a channel rack for jumping
straight into working, waiting, stalled, or input-needed sessions.

**Transcripts** — rendered as Markdown, with `Conversation` and `All activity`
modes. Live revisions arrive over a dedicated event stream and update entries in
place; periodic refresh is kept only as a connection fallback. Live-follow state
survives scrolling back through older entries.

**Sending messages** — off by default. A live channel shows a disabled composer
that points at `Session messages` in Mobile settings. Enabling that setting
unlocks the draft-preserving composer for live channels; the permission applies
immediately after saving.

Recorded sessions, approvals, interrupts, model changes, and session creation
remain read-only.

## Status indicator

The top-right TUI indicator advertises `/mobile` and, when there is room, its
state:

| State | Meaning |
| --- | --- |
| `LAN` | Reachable from phones on the network |
| `RESTART` | Saved listener setup differs from the running listener |
| `SETUP` | Not configured yet |
| `OFF` | Disabled |
| `ERR` | The listener failed, e.g. the port is already in use |

A failed listener does not stop the TUI; the failure is reported in the top
status line and in the Mobile panel.

## Security

Pairing authenticates the browser. It **does not encrypt traffic** — this is
plain HTTP.

Keep direct LAN exposure on a network you trust. If you need protection against
local traffic interception, put it behind transport encryption or a private
overlay network yourself.

## Standalone server

```bash
lcroom serve --listen 192.168.0.6:7777
```

`lcroom serve` runs the client without the TUI and prints the LAN pairing code at
startup. It can read recorded engineer transcripts from detected artifacts, but
only the TUI-hosted client can overlay the richer in-memory live transcript. A
standalone preview also needs its own database runtime lease.

## Settings

Mobile settings live in `/settings` → Mobile, and in the Mobile card in `/setup`:

- TUI auto-start
- `Session messages` (monitor-only vs. live-session message access)
- Address mode and port
- Optional advanced custom address

Config keys: `mobile_enabled`, `mobile_input_enabled`, `mobile_listen_address`.
See [`reference.md`](reference.md) for the full config file.
