# TUI Design Rules

Little Control Room's terminal UI should stay consistent across the main dashboard, embedded session panes, and the centered Chat overlay.

## Dialog Actions

Dialog and picker action chips use shared tones from `internal/uistyle`:

- Primary: green. Use for the main committing action, usually `Enter`.
- Navigate: blue. Use for movement, focus changes, autocomplete, copy, or inspect actions.
- Secondary: yellow. Use for optional apply/toggle/push-style actions.
- Cancel: red. Use for `Esc`, close, cancel, dismiss, delete, and interrupt actions.
- Disabled: gray. Use for unavailable actions that remain visible for orientation.

Do not create one-off action colors in feature code. Use `uistyle.RenderDialogActionTone` outside the `tui` package, or the existing `renderDialogAction` wrappers inside `internal/tui`.

Footer hints should mirror these same semantic tones in their compact form. For example, `Esc close` should use the cancel tone, not a neutral hint tone.

Shortcut labels must match the actual binding, including case. Show unshifted letter keys in lowercase (`a`, `c`); reserve uppercase labels (`A`) for actual uppercase/Shift bindings. Do not case-fold input handlers to make uppercase and lowercase interchangeable. Write modifiers in lowercase, for example `ctrl+s` and `alt+enter`, so labels do not imply Shift. Keep this spelling consistent between action chips, footers, and status text. Named standalone keys such as `Enter`, `Esc`, and `Tab` retain their conventional spelling.

## Modal Shape

- Prefer the existing dialog panel treatment for command palettes, pickers, setup prompts, and confirmations.
- Keep action hints in the same order where possible: primary, navigation/secondary, cancel.
- Use `Esc` consistently for closing or backing out of a modal, and render it with the cancel tone.
- Do not use plain `Enter` as a hidden save alias in broad settings forms. Reserve it for row choice, picker/sub-dialog entry, or an explicitly shown primary action.
- Optional external paths may warn when missing, but should not prevent unrelated settings from being saved. Record the warning in `/errors` when available; the feature that actually uses the path should fail clearly at launch/use time.

## Rows

- Pickers should keep selection visually distinct without changing row height.
- Keep row metadata muted and aligned to the right when there is room.
- Avoid adding explanatory prose inside dense picker rows; put context in an `About` section or status line.
