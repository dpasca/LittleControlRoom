package boss

import (
	"context"
	"strings"
	"testing"

	"lcroom/internal/model"
)

func TestModelViewRendersBossPanels(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.width = 100
	m.height = 30
	m.stateLoaded = true
	m.snapshot = StateSnapshot{
		TotalProjects:  1,
		ActiveProjects: 1,
		HotProjects: []ProjectBrief{{
			Name:           "Alpha",
			Status:         model.StatusActive,
			AttentionScore: 12,
		}},
	}
	m.syncLayout(true)

	view := m.View()
	for _, want := range []string{"Chat With Mina", "Little Room", "On My Desk", "Notebook", "Alpha"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}
