package runtimemcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestImageReviewRequiresSessionOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		s := &Server{imageReviewEnabled: enabled}
		found := false
		for _, tool := range s.tools() {
			if tool.Name == "inspect_images" {
				found = true
			}
		}
		if found != enabled {
			t.Fatalf("tool exposure: enabled=%v found=%v", enabled, found)
		}
		if !enabled {
			result, err := s.inspectImages(context.Background(), json.RawMessage(`{"paths":["a.png"],"question":"Inspect"}`))
			raw, _ := json.Marshal(result)
			if err != nil || !strings.Contains(string(raw), "not enabled") {
				t.Fatalf("disabled call did not fail closed: %s, %v", raw, err)
			}
		}
	}
}

func TestImageReviewMissingKeyReturnsToolError(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("LCR_IMAGE_REVIEW_API_KEY", "")
	s := &Server{imageReviewEnabled: true}
	result, err := s.inspectImages(context.Background(), json.RawMessage(`{"paths":["a.png"],"question":"Inspect"}`))
	raw, _ := json.Marshal(result)
	if err != nil || !strings.Contains(string(raw), "OPENAI_API_KEY") || !strings.Contains(string(raw), `"isError":true`) {
		t.Fatalf("missing key should be a readable tool failure: %s, %v", raw, err)
	}
}
