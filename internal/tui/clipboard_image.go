package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"lcroom/internal/codexapp"
)

var (
	errClipboardHasNoImage       = errors.New("the clipboard does not contain an image")
	errClipboardImageUnsupported = errors.New("image attachment from the clipboard is not supported on this platform")
)

var clipboardImageExporter = exportClipboardImage

func clipboardImageAttachment() (codexapp.Attachment, error) {
	path, err := clipboardImageExporter()
	if err != nil {
		return codexapp.Attachment{}, err
	}
	return codexapp.Attachment{
		Kind: codexapp.AttachmentLocalImage,
		Path: strings.TrimSpace(path),
	}, nil
}

func durableClipboardImageAttachment(dataDir string) (codexapp.Attachment, error) {
	attachment, err := clipboardImageAttachment()
	if err != nil {
		return codexapp.Attachment{}, err
	}
	if strings.TrimSpace(attachment.Path) == "" {
		return codexapp.Attachment{}, fmt.Errorf("clipboard image export returned an empty path")
	}
	dir := filepath.Join(strings.TrimSpace(dataDir), "todo-attachments")
	if strings.TrimSpace(dataDir) == "" {
		dir = filepath.Join(os.TempDir(), "lcroom-todo-attachments")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return codexapp.Attachment{}, err
	}
	tmp, err := os.CreateTemp(dir, "todo-image-*.png")
	if err != nil {
		return codexapp.Attachment{}, err
	}
	destPath := tmp.Name()
	if err := copyFileToOpenFile(attachment.Path, tmp); err != nil {
		_ = tmp.Close()
		_ = os.Remove(destPath)
		return codexapp.Attachment{}, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(destPath)
		return codexapp.Attachment{}, err
	}
	attachment.Path = destPath
	return attachment, nil
}

func copyFileToOpenFile(sourcePath string, dest *os.File) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	if _, err := io.Copy(dest, source); err != nil {
		return err
	}
	return nil
}
