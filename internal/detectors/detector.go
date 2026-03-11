package detectors

import (
	"context"

	"lcroom/internal/model"
	"lcroom/internal/scanner"
)

type Detector interface {
	Name() string
	Detect(ctx context.Context, scope scanner.PathScope) (map[string]*model.DetectorProjectActivity, error)
}
