package tui

import "errors"

var (
	errClipboardHasNoImage       = errors.New("the clipboard does not contain an image")
	errClipboardImageUnsupported = errors.New("image attachment from the clipboard is not supported on this platform")
)

var clipboardImageExporter = exportClipboardImage
