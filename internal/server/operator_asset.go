package server

import (
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"

	"lcroom/internal/pixelart"
)

const (
	operatorAssetWidth  = 36
	operatorAssetHeight = pixelart.OperatorStationHeight
)

func handleOperatorStationAsset(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}

	stationState := operatorAssetState(r.URL.Query().Get("state"))
	canvas := image.NewNRGBA(image.Rect(0, 0, operatorAssetWidth, operatorAssetHeight))
	pixelart.DrawOperatorStation(func(x, y int, pixel pixelart.Color) {
		if image.Pt(x, y).In(canvas.Bounds()) {
			canvas.SetNRGBA(x, y, color.NRGBA{R: pixel.R, G: pixel.G, B: pixel.B, A: 255})
		}
	}, stationState)

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if r.Method == http.MethodHead {
		return
	}
	if err := png.Encode(w, canvas); err != nil {
		http.Error(w, "encode operator asset", http.StatusInternalServerError)
	}
}

func operatorAssetState(name string) pixelart.OperatorStationState {
	state := pixelart.OperatorStationState{
		Width:  operatorAssetWidth,
		Pose:   pixelart.OperatorInspect,
		Facing: -1,
	}

	switch strings.ToLower(strings.TrimSpace(name)) {
	case "attention":
		state.Phase = 2
	case "working":
		state.Pose = pixelart.OperatorTypeA
		state.Facing = 1
		state.Phase = 12
	case "approval":
		state.Pose = pixelart.OperatorTypeB
		state.Facing = 1
		state.Phase = 17
	case "connecting":
		state.WalkCycle = true
		state.Phase = 7
	case "offline":
		state.Blink = true
		state.Phase = 1
	default:
		state.Phase = 0
	}

	return state
}
