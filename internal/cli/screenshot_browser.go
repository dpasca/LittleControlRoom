package cli

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func resolveScreenshotBrowser(configuredPath string) (string, error) {
	candidates := []string{
		strings.TrimSpace(os.Getenv("LCROOM_SCREENSHOT_BROWSER")),
		strings.TrimSpace(configuredPath),
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"google-chrome",
		"chromium",
		"chromium-browser",
		"brave-browser",
		"msedge",
		"microsoft-edge",
	}

	checked := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		checked = append(checked, candidate)
		if path, ok := resolveScreenshotBrowserCandidate(candidate); ok {
			return path, nil
		}
	}
	if len(checked) == 0 {
		return "", fmt.Errorf("no browser candidate configured")
	}
	return "", fmt.Errorf("no Chrome/Chromium browser found; checked %s; set browser_path in screenshots.local.toml or LCROOM_SCREENSHOT_BROWSER", strings.Join(checked, ", "))
}

func resolveScreenshotBrowserCandidate(candidate string) (string, bool) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "", false
	}
	if strings.HasPrefix(candidate, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			candidate = filepath.Join(home, strings.TrimPrefix(candidate, "~/"))
		}
	}
	if filepath.IsAbs(candidate) || strings.Contains(candidate, string(filepath.Separator)) {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			return "", false
		}
		return candidate, true
	}
	path, err := exec.LookPath(candidate)
	if err != nil {
		return "", false
	}
	return path, true
}

func captureBrowserScreenshot(ctx context.Context, browserPath, htmlPath, outputPath string, width, height int, captureScale float64) error {
	htmlPath, err := filepath.Abs(htmlPath)
	if err != nil {
		return fmt.Errorf("resolve html path: %w", err)
	}
	outputPath, err = filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}

	fileURL := (&url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(htmlPath),
	}).String()

	args := browserScreenshotArgs(fileURL, outputPath, width, height, captureScale)

	cmd := exec.CommandContext(ctx, browserPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return fmt.Errorf("capture browser screenshot: %w: %s", err, message)
		}
		return fmt.Errorf("capture browser screenshot: %w", err)
	}
	if err := cropBrowserScreenshot(outputPath, scaleScreenshotCropPadding(screenshotCropPadding{
		left:   10,
		top:    10,
		right:  10,
		bottom: 18,
	}, captureScale)); err != nil {
		return fmt.Errorf("crop browser screenshot: %w", err)
	}
	return nil
}

func browserScreenshotArgs(fileURL, outputPath string, width, height int, captureScale float64) []string {
	captureScale = normalizeScreenshotCaptureScale(captureScale)
	return []string{
		"--headless=new",
		"--disable-gpu",
		"--hide-scrollbars",
		fmt.Sprintf("--force-device-scale-factor=%.2f", captureScale),
		"--force-color-profile=srgb",
		"--run-all-compositor-stages-before-draw",
		"--virtual-time-budget=1500",
		fmt.Sprintf("--window-size=%d,%d", width, height),
		fmt.Sprintf("--screenshot=%s", outputPath),
		fileURL,
	}
}

func normalizeScreenshotCaptureScale(scale float64) float64 {
	if scale < 1 {
		return 1
	}
	return scale
}

func scaleScreenshotCropPadding(padding screenshotCropPadding, captureScale float64) screenshotCropPadding {
	captureScale = normalizeScreenshotCaptureScale(captureScale)
	if captureScale == 1 {
		return padding
	}
	return screenshotCropPadding{
		left:   scaleScreenshotCropValue(padding.left, captureScale),
		top:    scaleScreenshotCropValue(padding.top, captureScale),
		right:  scaleScreenshotCropValue(padding.right, captureScale),
		bottom: scaleScreenshotCropValue(padding.bottom, captureScale),
	}
}

func scaleScreenshotCropValue(value int, captureScale float64) int {
	if value <= 0 {
		return 0
	}
	return int(math.Ceil(float64(value) * captureScale))
}

type screenshotCropPadding struct {
	left   int
	top    int
	right  int
	bottom int
}

func cropBrowserScreenshot(path string, padding screenshotCropPadding) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open screenshot: %w", err)
	}
	img, err := png.Decode(file)
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("decode screenshot: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close screenshot: %w", closeErr)
	}

	bounds := img.Bounds()
	if bounds.Empty() {
		return nil
	}
	background := color.NRGBAModel.Convert(img.At(bounds.Min.X, bounds.Min.Y)).(color.NRGBA)
	cropBounds, ok := detectScreenshotBounds(img, background)
	if !ok {
		return nil
	}
	cropBounds = expandBounds(cropBounds, bounds, padding)
	if cropBounds == bounds {
		return nil
	}

	dst := image.NewNRGBA(image.Rect(0, 0, cropBounds.Dx(), cropBounds.Dy()))
	draw.Draw(dst, dst.Bounds(), img, cropBounds.Min, draw.Src)

	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("rewrite screenshot: %w", err)
	}
	if err := png.Encode(out, dst); err != nil {
		_ = out.Close()
		return fmt.Errorf("encode cropped screenshot: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close cropped screenshot: %w", err)
	}
	return nil
}

func detectScreenshotBounds(img image.Image, background color.NRGBA) (image.Rectangle, bool) {
	bounds := img.Bounds()
	found := false
	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if screenshotPixelMatches(img.At(x, y), background) {
				continue
			}
			if !found {
				minX, maxX = x, x+1
				minY, maxY = y, y+1
				found = true
				continue
			}
			if x < minX {
				minX = x
			}
			if x+1 > maxX {
				maxX = x + 1
			}
			if y < minY {
				minY = y
			}
			if y+1 > maxY {
				maxY = y + 1
			}
		}
	}
	if !found {
		return image.Rectangle{}, false
	}
	return image.Rect(minX, minY, maxX, maxY), true
}

func expandBounds(rect, outer image.Rectangle, padding screenshotCropPadding) image.Rectangle {
	if padding.left <= 0 && padding.top <= 0 && padding.right <= 0 && padding.bottom <= 0 {
		return rect
	}
	return image.Rect(
		maxInt(outer.Min.X, rect.Min.X-padding.left),
		maxInt(outer.Min.Y, rect.Min.Y-padding.top),
		minInt(outer.Max.X, rect.Max.X+padding.right),
		minInt(outer.Max.Y, rect.Max.Y+padding.bottom),
	)
}

func screenshotPixelMatches(value color.Color, background color.NRGBA) bool {
	pixel := color.NRGBAModel.Convert(value).(color.NRGBA)
	return pixel.R == background.R && pixel.G == background.G && pixel.B == background.B
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
