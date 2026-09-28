package lcagent

import (
	"context"

	"lcroom/internal/lcagent/modeladapter"
)

type contextModelLister interface {
	ListModels(context.Context) ([]modeladapter.ListedModel, error)
}

func loadModelContextWindows(ctx context.Context, client contextModelLister) (map[string]int64, error) {
	models, err := client.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	windows := make(map[string]int64, len(models))
	for _, model := range models {
		// Reject nonsensical capacities before multiplying into packing budgets.
		if model.ContextLength > 0 && model.ContextLength <= 1_000_000_000 {
			windows[model.ID] = model.ContextLength
		}
	}
	return windows, nil
}
