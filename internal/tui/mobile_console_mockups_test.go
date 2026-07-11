package tui

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"strings"
	"testing"

	"lcroom/internal/pixelart"
)

func TestGeneratePocketControlRoomMockupAssets(t *testing.T) {
	t.Parallel()

	assets := generatePocketControlRoomMockupAssets()
	if got, want := len(assets), 3; got != want {
		t.Fatalf("asset count = %d, want %d", got, want)
	}

	wantNames := []string{
		"mobile-pocket-switchboard",
		"mobile-pocket-project",
		"mobile-pocket-session",
	}
	for i, asset := range assets {
		if asset.Name != wantNames[i] {
			t.Fatalf("asset %d name = %q, want %q", i, asset.Name, wantNames[i])
		}
		if asset.Width != mobileMockupPhoneWidth+mobileMockupCropLeft+mobileMockupCropRight {
			t.Fatalf("asset %q width = %d", asset.Name, asset.Width)
		}
		if asset.Height != mobileMockupPhoneHeight+mobileMockupCropTop+mobileMockupCropBottom {
			t.Fatalf("asset %q height = %d", asset.Name, asset.Height)
		}
		if !strings.Contains(asset.HTML, "Pocket Control Room") {
			t.Fatalf("asset %q is missing its accessible title", asset.Name)
		}
		if !strings.Contains(asset.HTML, "data:image/png;base64,") {
			t.Fatalf("asset %q is missing the shared pixel operator bitmap", asset.Name)
		}
	}
}

func TestPocketOperatorStationDataURLContainsPNG(t *testing.T) {
	t.Parallel()

	url := pocketOperatorStationDataURL(pixelart.OperatorStationState{
		Width: 36,
		Pose:  pixelart.OperatorInspect,
		Phase: 2,
	})
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("operator data URL prefix = %q", url)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, prefix))
	if err != nil {
		t.Fatalf("decode operator PNG: %v", err)
	}
	image, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse operator PNG: %v", err)
	}
	if got, want := image.Bounds().Dx(), 36; got != want {
		t.Fatalf("operator width = %d, want %d", got, want)
	}
	if got, want := image.Bounds().Dy(), 15; got != want {
		t.Fatalf("operator height = %d, want %d", got, want)
	}
}
