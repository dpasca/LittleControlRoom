package imagemedia

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSavedPixelsAreIndependentAndValidated(t *testing.T) {
	var pixels bytes.Buffer
	if err := png.Encode(&pixels, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "screenshot.png")
	if err := os.WriteFile(source, pixels.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	ref, err := Store(source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want, err := ref.DataURL()
	if err != nil || !strings.HasPrefix(want, "data:image/png;base64,") {
		t.Fatalf("URL=%q err=%v", want, err)
	}
	if err := os.WriteFile(source, []byte("later screenshot"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ref.DataURL(); err != nil || got != want {
		t.Fatalf("saved pixels changed: %v", err)
	}
	changed := append([]byte(nil), pixels.Bytes()...)
	changed[len(changed)-1] ^= 1
	if err := os.WriteFile(ref.Path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ref.DataURL(); err == nil || !strings.Contains(err.Error(), "has changed") {
		t.Fatalf("modified artifact accepted: %v", err)
	}
}

func TestStoreRejectsInvalidAndOversizedFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"empty.png", "fake.png"} {
		var data []byte
		if name == "fake.png" {
			data = []byte("This file is text despite its extension.")
		}
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	large := filepath.Join(root, "large.png")
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, filepath.Join(root, "missing"), filepath.Join(root, "empty.png"), filepath.Join(root, "fake.png"), large} {
		if _, err := Store(path, t.TempDir()); err == nil {
			t.Errorf("accepted invalid image: %s", path)
		}
	}
}
