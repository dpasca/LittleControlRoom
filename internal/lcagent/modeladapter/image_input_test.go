package modeladapter

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/lcagent/imagemedia"
)

func TestImageCheckpointWireFormatsAndMissingArtifacts(t *testing.T) {
	var pixels bytes.Buffer
	if err := png.Encode(&pixels, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source.png")
	if err := os.WriteFile(source, pixels.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	ref, err := imagemedia.Store(source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// This same JSON representation is used by thread state and context hashes.
	checkpoint, err := json.Marshal([]Message{{Role: "system", Content: "System instructions", CacheControl: &CacheControl{Type: "ephemeral"}}, {Role: "user", Origin: "tool_image", Content: "Inspect these pixels.", Images: []imagemedia.Reference{ref}}})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(checkpoint, []byte("base64")) || !bytes.Contains(checkpoint, []byte(ref.SHA256)) {
		t.Fatal("checkpoint must contain references rather than pixels")
	}
	var messages []Message
	if err := json.Unmarshal(checkpoint, &messages); err != nil {
		t.Fatal(err)
	}
	for _, responses := range []bool{false, true} {
		var wire any
		if responses {
			_, wire, _ = responsesInput(messages, false)
		} else {
			wire = chatWireMessages(withAnthropicPromptCache(messages))
		}
		body, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(body, []byte("data:image/png;base64,")) || bytes.Contains(body, []byte(ref.SHA256)) || bytes.Contains(body, []byte(`"origin"`)) {
			t.Fatalf("bad provider serialization: %s", body)
		}
		if !responses && !bytes.Contains(body, []byte(`"cache_control":{"type":"ephemeral"}`)) {
			t.Fatal("native images lost the system prompt cache breakpoint")
		}
	}
	if err := os.Remove(ref.Path); err != nil {
		t.Fatal(err)
	}
	for _, responses := range []bool{false, true} {
		body, err := json.Marshal(messageInputContent(messages[1], responses))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "data:image") || !strings.Contains(string(body), "Image pixels unavailable") {
			t.Fatalf("missing artifact silently omitted: %s", body)
		}
	}
}

func TestNativeVisionToolsPreferDirectInspection(t *testing.T) {
	opts := ToolOptions{VisionAnalysisEnabled: true, NativeVisionEnabled: true}
	view := toolSpec(t, ToolsWithOptions(opts), "view_image")
	qa := toolSpec(t, ToolsWithOptions(opts), "analyze_image")
	if !strings.Contains(view.Description, "No separate model request") || !strings.Contains(qa.Description, "independent visual QA") {
		t.Fatalf("view=%s QA=%s", view.Description, qa.Description)
	}
	prompt := SystemPromptWithOptions("", "", SystemPromptOptions{VisionAnalysisEnabled: true, NativeVisionEnabled: true})
	if !strings.Contains(prompt, "You can inspect image pixels directly") || strings.Contains(prompt, "Direct image input is not enabled") {
		t.Fatal("prompt contradicts native capability")
	}
}
