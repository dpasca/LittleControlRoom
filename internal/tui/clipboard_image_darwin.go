//go:build darwin

package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func exportClipboardImage() (string, error) {
	tmp, err := os.CreateTemp("", "lcroom-codex-clipboard-*.png")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}

	script := `import AppKit
import Foundation
let path = CommandLine.arguments[1]
let pasteboard = NSPasteboard.general
guard let image = NSImage(pasteboard: pasteboard) else {
    fputs("no image\n", stderr)
    exit(2)
}
guard let tiff = image.tiffRepresentation,
      let bitmap = NSBitmapImageRep(data: tiff),
      let png = bitmap.representation(using: .png, properties: [:]) else {
    fputs("encode failed\n", stderr)
    exit(3)
}
try png.write(to: URL(fileURLWithPath: path))
print(path)
`

	cmd := exec.Command("swift", "-e", script, path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(path)
		if strings.Contains(string(output), "no image") {
			return "", errClipboardHasNoImage
		}
		return "", fmt.Errorf("export clipboard image: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if info.Size() == 0 {
		_ = os.Remove(path)
		return "", fmt.Errorf("clipboard image export produced an empty file")
	}
	return path, nil
}
