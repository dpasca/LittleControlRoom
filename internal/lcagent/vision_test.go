package lcagent

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"lcroom/internal/lcagent/script"
)

func TestComposeVisionComparisonImage(t *testing.T) {
	left := testVisionPNG(t, "left.png", 3, 2, color.RGBA{R: 255, A: 255})
	right := testVisionPNG(t, "right.png", 5, 4, color.RGBA{G: 255, A: 255})

	combined, err := composeVisionComparisonImage(left, right)
	if err != nil {
		t.Fatalf("composeVisionComparisonImage() error = %v", err)
	}
	if combined.MIMEType != "image/png" {
		t.Fatalf("combined MIMEType = %q, want image/png", combined.MIMEType)
	}
	decoded, _, err := image.Decode(bytes.NewReader(combined.Data))
	if err != nil {
		t.Fatalf("decode combined image: %v", err)
	}
	if got, want := decoded.Bounds().Dx(), 3+8+5; got != want {
		t.Fatalf("combined width = %d, want %d", got, want)
	}
	if got, want := decoded.Bounds().Dy(), 4; got != want {
		t.Fatalf("combined height = %d, want %d", got, want)
	}

	prompt := buildVisionPrompt(script.ImageAnalysisRequest{
		Path:           "left.png",
		ComparisonPath: "right.png",
		Question:       "Is the visual state stable?",
	}, combined)
	for _, want := range []string{"side-by-side temporal comparison", "Left image path: left.png", "Right image path: right.png"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func testVisionPNG(t *testing.T, path string, width, height int, c color.Color) visionImage {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return visionImage{
		Path:     path,
		MIMEType: "image/png",
		Data:     buf.Bytes(),
		Bytes:    int64(buf.Len()),
	}
}
