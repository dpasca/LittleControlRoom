//go:build !darwin

package tui

func exportClipboardImage() (string, error) {
	return "", errClipboardImageUnsupported
}
