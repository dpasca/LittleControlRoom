# TUI Design Rules

Little Control Room's terminal UI should stay consistent across the classic dashboard, embedded session panes, and boss mode.

## Dialog Actions

Dialog and picker action chips use shared tones from `internal/uistyle`:

- Primary: green. Use for the main committing action, usually `Enter`.
- Navigate: blue. Use for movement, focus changes, autocomplete, copy, or inspect actions.
- Secondary: yellow. Use for optional apply/toggle/push-style actions.
- Cancel: red. Use for `Esc`, close, cancel, dismiss, delete, and interrupt actions.
- Disabled: gray. Use for unavailable actions that remain visible for orientation.

Do not create one-off action colors in feature code. Use `uistyle.RenderDialogActionTone` outside the `tui` package, or the existing `renderDialogAction` wrappers inside `internal/tui`.

Footer hints should mirror these same semantic tones in their compact form. For example, `Esc close` should use the cancel tone, not a neutral hint tone.

## Modal Shape

- Prefer the existing dialog panel treatment for command palettes, pickers, setup prompts, and confirmations.
- Keep action hints in the same order where possible: primary, navigation/secondary, cancel.
- Use `Esc` consistently for closing or backing out of a modal, and render it with the cancel tone.

## Rows

- Pickers should keep selection visually distinct without changing row height.
- Keep row metadata muted and aligned to the right when there is room.
- Avoid adding explanatory prose inside dense picker rows; put context in an `About` section or status line.
