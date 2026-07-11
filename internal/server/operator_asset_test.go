package server

import (
	"bytes"
	"context"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerServesOperatorStationAsset(t *testing.T) {
	t.Parallel()
	handler := New(nil).Handler(context.Background())

	working := httptest.NewRecorder()
	handler.ServeHTTP(working, httptest.NewRequest(http.MethodGet, "/assets/operator-station.png?state=working", nil))
	if working.Code != http.StatusOK {
		t.Fatalf("GET operator asset status = %d, want 200", working.Code)
	}
	if got, want := working.Header().Get("Content-Type"), "image/png"; got != want {
		t.Fatalf("operator asset content type = %q, want %q", got, want)
	}
	if working.Header().Get("Cache-Control") == "" {
		t.Fatal("operator asset should be cacheable")
	}

	decoded, err := png.Decode(bytes.NewReader(working.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode operator asset: %v", err)
	}
	if got, want := decoded.Bounds().Dx(), operatorAssetWidth; got != want {
		t.Fatalf("operator asset width = %d, want %d", got, want)
	}
	if got, want := decoded.Bounds().Dy(), operatorAssetHeight; got != want {
		t.Fatalf("operator asset height = %d, want %d", got, want)
	}

	offline := httptest.NewRecorder()
	handler.ServeHTTP(offline, httptest.NewRequest(http.MethodGet, "/assets/operator-station.png?state=offline", nil))
	if bytes.Equal(working.Body.Bytes(), offline.Body.Bytes()) {
		t.Fatal("working and offline operator states should render differently")
	}
}

func TestOperatorStationAssetRejectsMutation(t *testing.T) {
	t.Parallel()
	handler := New(nil).Handler(context.Background())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/assets/operator-station.png", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST operator asset status = %d, want 405", response.Code)
	}
	if got, want := response.Header().Get("Allow"), http.MethodGet; got != want {
		t.Fatalf("POST operator asset Allow = %q, want %q", got, want)
	}
}
