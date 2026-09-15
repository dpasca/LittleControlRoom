// Package imagemedia stores image pixels separately from conversation JSON.
package imagemedia

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const MaxBytes = 25 * 1024 * 1024

// Reference identifies the exact pixels viewed, even if the source later changes.
// Conversation checkpoints and tool events contain references, never base64 data.
type Reference struct {
	Path     string `json:"path"`
	Source   string `json:"source"`
	MIMEType string `json:"mime_type"`
	SHA256   string `json:"sha256"`
	Bytes    int64  `json:"bytes"`
}

func readBounded(path string) ([]byte, error) {
	// Reject devices and pipes before opening; opening a FIFO can block.
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("image must be a regular file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxBytes {
		return nil, fmt.Errorf("image must be a nonempty regular file of at most %d bytes: %s", MaxBytes, path)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > MaxBytes {
		return nil, fmt.Errorf("image size changed while reading: %s", path)
	}
	return data, nil
}

// Store copies a previously authorized source into the session artifact directory.
func Store(source, artifactDir string) (Reference, error) {
	if artifactDir == "" {
		return Reference{}, fmt.Errorf("image artifact directory is not configured")
	}
	data, err := readBounded(source)
	if err != nil {
		return Reference{}, err
	}
	mimeType := http.DetectContentType(data)
	ext := ""
	switch mimeType {
	case "image/png":
		ext = ".png"
	case "image/jpeg":
		ext = ".jpg"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	default:
		return Reference{}, fmt.Errorf("unsupported image content %q; use PNG, JPEG, GIF, or WebP", mimeType)
	}
	artifactDir, err = filepath.Abs(artifactDir)
	if err != nil {
		return Reference{}, err
	}
	if err := os.MkdirAll(artifactDir, 0700); err != nil {
		return Reference{}, err
	}
	f, err := os.CreateTemp(artifactDir, "view-image-*"+ext)
	if err != nil {
		return Reference{}, err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(f.Name())
		return Reference{}, fmt.Errorf("save image artifact: write=%v close=%v", writeErr, closeErr)
	}
	return Reference{Path: f.Name(), Source: source, MIMEType: mimeType, SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Bytes: int64(len(data))}, nil
}

// DataURL validates the saved pixels before sending them to a provider. A missing
// or modified artifact must be reported, never silently replaced with the source.
func (r Reference) DataURL() (string, error) {
	data, err := readBounded(r.Path)
	if err != nil {
		return "", err
	}
	if int64(len(data)) != r.Bytes || fmt.Sprintf("%x", sha256.Sum256(data)) != r.SHA256 || http.DetectContentType(data) != r.MIMEType {
		return "", fmt.Errorf("saved image artifact has changed: %s", r.Path)
	}
	return "data:" + r.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}
