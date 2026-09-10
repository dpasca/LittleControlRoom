package imagereview

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/lcagent/modeladapter"
)

func testImage(t *testing.T, dir, name string, c color.Color) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, c)
		}
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReviewSeparateImagesAndTextOnlyResult(t *testing.T) {
	dir := t.TempDir()
	left := testImage(t, dir, "left.png", color.RGBA{R: 255, A: 255})
	right := testImage(t, dir, "right.png", color.RGBA{B: 255, A: 255})
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/responses" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body["model"] != Model || body["store"] != false || body["tools"] != nil || body["previous_response_id"] != nil {
			t.Errorf("incorrect independent review configuration")
		}
		if body["reasoning"].(map[string]any)["effort"] != Reasoning {
			t.Error("missing reasoning")
		}
		content := body["input"].([]any)[0].(map[string]any)["content"].([]any)
		if len(content) != 3 {
			t.Errorf("expected text and 2 images, got %d", len(content))
			return
		}
		prompt := content[0].(map[string]any)["text"].(string)
		if !strings.Contains(prompt, "left.png") || !strings.Contains(prompt, "right.png") || !strings.Contains(prompt, "Red means source A") {
			t.Error("missing labels/legend")
		}
		for _, part := range content[1:] {
			v := part.(map[string]any)
			if v["type"] != "input_image" || !strings.HasPrefix(v["image_url"].(string), "data:image/png;base64,") {
				t.Error("missing image data")
			}
		}
		fmt.Fprint(w, `{"id":"review","model":"gpt-5.6-luna","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Image 1 is red; image 2 is blue. Terrain cannot be judged."}]}],"usage":{"input_tokens":40,"output_tokens":20}}`)
	}))
	defer server.Close()
	client, err := modeladapter.NewOpenAIClient(modeladapter.OpenRouterConfig{APIKey: "test-key", BaseURL: server.URL, Model: Model})
	if err != nil {
		t.Fatal(err)
	}
	result := Review(context.Background(), client, dir, Request{Paths: []string{left, right}, Question: "Compare colors", Context: "Red means source A"})
	if !result.Success || calls != 1 || !strings.Contains(result.Findings, "cannot be judged") || len(result.Usage) == 0 {
		t.Fatalf("unexpected review: %+v", result)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "base64") || strings.Contains(string(raw), "input_image") {
		t.Fatal("result leaked image payload")
	}
}

type failingClient struct {
	calls int
	err   error
}

func (c *failingClient) CompleteImagesWithOptions(context.Context, string, []modeladapter.ImageInput, modeladapter.CompletionOptions) (modeladapter.Completion, error) {
	c.calls++
	return modeladapter.Completion{}, c.err
}

func TestReviewRejectsInvalidInputsBeforeAPI(t *testing.T) {
	dir := t.TempDir()
	outside := testImage(t, t.TempDir(), "outside.png", color.Black)
	if err := os.Symlink(outside, filepath.Join(dir, "link.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.png"), []byte("not an image"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{nil, {outside}, {"link.png"}, {"broken.png"}, {"."}, {"a", "b", "c", "d", "e"}} {
		client := &failingClient{}
		result := Review(context.Background(), client, dir, Request{Paths: paths, Question: "Inspect"})
		if result.Success || result.Error == "" || client.calls != 0 {
			t.Fatalf("unexpected result for %v: %+v", paths, result)
		}
	}
}

func TestReviewFailureAndEmptyResponseStayPending(t *testing.T) {
	dir := t.TempDir()
	path := testImage(t, dir, "test.png", color.Black)
	for _, err := range []error{fmt.Errorf("HTTP 400: Bad Request"), nil} {
		client := &failingClient{err: err}
		result := Review(context.Background(), client, dir, Request{Paths: []string{path}, Question: "Inspect"})
		if result.Success || result.Error == "" || client.calls != 1 {
			t.Fatalf("failure should be returned without retries: %+v", result)
		}
	}
}

// Explicit opt-in only: ordinary test runs never make billable API requests.
func TestLiveImageReview(t *testing.T) {
	if os.Getenv("LCR_TEST_LIVE_IMAGE_REVIEW") != "1" {
		t.Skip("live image review disabled")
	}
	client, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	left := testImage(t, dir, "first.png", color.RGBA{R: 255, A: 255})
	right := testImage(t, dir, "second.png", color.RGBA{B: 255, A: 255})
	for _, paths := range [][]string{{left}, {left, right}} {
		result := Review(context.Background(), client, dir, Request{Paths: paths, Question: "Identify the solid color of each image, in order."})
		if !result.Success {
			t.Fatalf("review failed: %s", result.Error)
		}
		t.Logf("model=%s findings=%s", result.Model, result.Findings)
		if !strings.Contains(strings.ToLower(result.Findings), "red") {
			t.Fatal("first image not identified as red")
		}
		if len(paths) == 2 && !strings.Contains(strings.ToLower(result.Findings), "blue") {
			t.Fatal("second image not identified as blue")
		}
	}
}
