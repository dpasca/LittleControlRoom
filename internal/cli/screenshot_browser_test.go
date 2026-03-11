package cli

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveScreenshotBrowserCandidateAbsolutePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "browser")
	if err := os.WriteFile(path, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write browser stub: %v", err)
	}

	got, ok := resolveScreenshotBrowserCandidate(path)
	if !ok {
		t.Fatalf("expected absolute browser path to resolve")
	}
	if got != path {
		t.Fatalf("resolveScreenshotBrowserCandidate() = %q, want %q", got, path)
	}
}

func TestResolveScreenshotBrowserPrefersEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "browser")
	if err := os.WriteFile(path, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write browser stub: %v", err)
	}

	t.Setenv("LCROOM_SCREENSHOT_BROWSER", path)

	got, err := resolveScreenshotBrowser("")
	if err != nil {
		t.Fatalf("resolveScreenshotBrowser() error = %v", err)
	}
	if got != path {
		t.Fatalf("resolveScreenshotBrowser() = %q, want %q", got, path)
	}
}

func TestCropBrowserScreenshotTrimsUniformBackground(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")

	bg := color.NRGBA{R: 0x0b, G: 0x10, B: 0x20, A: 0xff}
	fg := color.NRGBA{R: 0x22, G: 0x88, B: 0xff, A: 0xff}
	img := image.NewNRGBA(image.Rect(0, 0, 24, 18))
	for y := 0; y < 18; y++ {
		for x := 0; x < 24; x++ {
			img.Set(x, y, bg)
		}
	}
	for y := 4; y < 12; y++ {
		for x := 6; x < 17; x++ {
			img.Set(x, y, fg)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create png: %v", err)
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		t.Fatalf("encode png: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close png: %v", err)
	}

	if err := cropBrowserScreenshot(path, screenshotCropPadding{
		left:   2,
		top:    2,
		right:  2,
		bottom: 2,
	}); err != nil {
		t.Fatalf("cropBrowserScreenshot() error = %v", err)
	}

	croppedFile, err := os.Open(path)
	if err != nil {
		t.Fatalf("open cropped png: %v", err)
	}
	cropped, err := png.Decode(croppedFile)
	closeErr := croppedFile.Close()
	if err != nil {
		t.Fatalf("decode cropped png: %v", err)
	}
	if closeErr != nil {
		t.Fatalf("close cropped png: %v", closeErr)
	}
	if got, want := cropped.Bounds().Dx(), 15; got != want {
		t.Fatalf("cropped width = %d, want %d", got, want)
	}
	if got, want := cropped.Bounds().Dy(), 12; got != want {
		t.Fatalf("cropped height = %d, want %d", got, want)
	}
}
