package lcagent

import (
	"bytes"
	"context"
	"encoding/json"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/lcagent/imagemedia"
	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/policy"
	"lcroom/internal/lcagent/script"
	"lcroom/internal/lcagent/session"
	"lcroom/internal/lcagent/tools"
)

func imageHarnessRunner(t *testing.T) (script.Runner, *bytes.Buffer) {
	t.Helper()
	stream := &bytes.Buffer{}
	writer, id, err := session.NewWriter(t.TempDir(), time.Now(), stream)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	workspace, err := policy.NewWorkspace(t.TempDir(), policy.AutonomyMedium)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"page.png", "other.png"} {
		img := testVisionPNG(t, name, 2, 2, color.RGBA{R: 255, A: 255})
		if err := os.WriteFile(filepath.Join(workspace.Root, name), img.Data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return script.Runner{Session: writer, SessionID: id, Prompt: "Read both pages and identify the selected destination.", ArtifactsDir: t.TempDir(),
		Files:   tools.FileTools{Workspace: workspace, Limits: tools.FileLimitsForProfile(tools.FileProfileBalanced)},
		Command: tools.CommandRunner{Workspace: workspace}, Patch: tools.PatchApplier{Workspace: workspace}}, stream
}

func runImageHarness(t *testing.T, runner script.Runner, provider, url, visionModel string, resume *resumeContext) error {
	t.Helper()
	return runChatLoop(context.Background(), runner.Session, runner, nil, "", resume,
		modeladapter.OpenRouterConfig{APIKey: "test-key", BaseURL: url, Model: "test-model", MaxTurns: 10},
		modeladapter.OpenRouterConfig{}, modeladapter.OpenRouterConfig{Model: visionModel}, provider, "off", "main", script.DefaultSearchRefineMinBytes,
		tools.FileProfileBalanced, runner.Files.Limits, openRouterContextOptionsForProfileAndModel(openRouterContextProfileLarge, provider, "test-model"), true, false, false)
}

// Inspect actual wire content, including tool ordering, rather than decoding it
// into the internal durable Message format (whose content intentionally stays text).
func requestImageCount(t *testing.T, body map[string]json.RawMessage, provider string) int {
	t.Helper()
	key := "messages"
	if provider == "openai" {
		key = "input"
	}
	var items []struct {
		Role      string                  `json:"role"`
		Type      string                  `json:"type"`
		Content   json.RawMessage         `json:"content"`
		ToolCalls []modeladapter.ToolCall `json:"tool_calls"`
	}
	if err := json.Unmarshal(body[key], &items); err != nil {
		t.Fatal(err)
	}
	count, pending := 0, 0
	for _, item := range items {
		if item.Role == "tool" {
			pending--
		}
		if item.Role == "user" && pending > 0 {
			t.Error("image/user message interrupted a tool-result batch")
		}
		pending += len(item.ToolCalls)
		if len(item.Content) == 0 || item.Content[0] != '[' {
			continue
		}
		var parts []struct {
			Type     string          `json:"type"`
			ImageURL json.RawMessage `json:"image_url"`
		}
		if err := json.Unmarshal(item.Content, &parts); err != nil {
			t.Fatal(err)
		}
		for _, part := range parts {
			if part.Type == "input_image" || part.Type == "image_url" {
				count++
				if !bytes.Contains(part.ImageURL, []byte("data:image/png;base64,")) {
					t.Errorf("invalid image content: %s", part.ImageURL)
				}
			}
		}
	}
	if bytes.Contains(body[key], []byte(`"sha256":`)) {
		t.Error("internal image reference leaked into provider schema")
	}
	return count
}

func TestNativeImageConversationAcrossProviders(t *testing.T) {
	for _, provider := range []string{"openai", "deepseek", "openrouter", "moonshot", "xiaomi", "ollama", "mlx"} {
		t.Run(provider, func(t *testing.T) {
			runner, trace := imageHarnessRunner(t)
			mainRequests, qaRequests := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if len(body["tools"]) == 0 {
					qaRequests++
					if mainRequests != 3 || requestImageCount(t, body, provider) != 1 {
						t.Error("ordinary inspection made an auxiliary vision request")
					}
					replyHarnessTest(w, provider, 99, `{"verdict":"pass","summary":"Both sides are legible.","observations":["Text is visible."],"blocking_issues":[]}`)
					return
				}
				mainRequests++
				switch mainRequests {
				case 1:
					if !bytes.Contains(body["tools"], []byte(`"view_image"`)) {
						t.Error("view_image not exposed")
					}
					replyHarnessTest(w, provider, mainRequests, "",
						harnessTestCall("view_image", map[string]any{"path": "page.png"}),
						harnessTestCall("read_file", map[string]any{"path": "absent.txt"}))
				case 2:
					if got := requestImageCount(t, body, provider); got != 1 {
						t.Errorf("image count=%d, want 1", got)
					}
					if provider == "openai" && string(body["previous_response_id"]) != `"resp_1"` {
						t.Error("lost initial Responses continuation")
					}
					replyHarnessTest(w, provider, mainRequests, "The destination is visible.", harnessTestCall("analyze_image", map[string]any{
						"path": "page.png", "comparison_path": "other.png", "purpose": "inspect", "question": "Compare the visible content."}))
				case 3:
					want := 3
					if provider == "openai" {
						want = 2
					} // Only new content is sent with previous_response_id.
					if got := requestImageCount(t, body, provider); got != want {
						t.Errorf("image count=%d, want %d", got, want)
					}
					replyHarnessTest(w, provider, mainRequests, "", harnessTestCall("analyze_image", map[string]any{
						"path": "page.png", "purpose": "verify", "question": "Is the scan legible?"}))
				case 4:
					if provider == "openai" && string(body["previous_response_id"]) != `"resp_3"` {
						t.Error("independent QA replaced the main conversation's response ID")
					}
					replyHarnessTest(w, provider, mainRequests, "", harnessTestCall("final_response", map[string]any{
						"summary": "The destination is visible and the scan is legible.", "outcome": "completed", "files_changed": []string{}, "verification": []string{"Visual QA passed."}}))
				default:
					t.Errorf("unexpected main request %d", mainRequests)
					http.Error(w, "unexpected", 400)
				}
			}))
			defer server.Close()
			if err := runImageHarness(t, runner, provider, server.URL, "", nil); err != nil {
				t.Fatalf("loop: %v\n%s", err, trace)
			}
			if mainRequests != 4 || qaRequests != 1 {
				t.Fatalf("main=%d QA=%d", mainRequests, qaRequests)
			}
			if !strings.Contains(trace.String(), `"native_input":true`) || strings.Count(trace.String(), `"type":"image_analysis_started"`) != 1 {
				t.Fatalf("unexpected image routing trace:\n%s", trace)
			}
			if strings.Contains(trace.String(), "data:image/") {
				t.Error("base64 leaked into session events")
			}
		})
	}
}

func TestViewImageFallsBackForSeparateVisionModel(t *testing.T) {
	for _, provider := range []string{"openai", "openrouter"} {
		t.Run(provider, func(t *testing.T) {
			runner, trace := imageHarnessRunner(t)
			mainRequests, visionRequests := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&body)
				if string(body["model"]) == `"vision-only"` {
					visionRequests++
					if requestImageCount(t, body, provider) != 1 {
						t.Error("fallback did not receive pixels")
					}
					replyHarnessTest(w, provider, 99, `{"summary":"Documents is selected.","observations":["A chooser is open."],"limitations":[]}`)
					return
				}
				mainRequests++
				if requestImageCount(t, body, provider) != 0 {
					t.Error("text-only main received image pixels")
				}
				if mainRequests == 1 {
					replyHarnessTest(w, provider, mainRequests, "", harnessTestCall("view_image", map[string]any{"path": "page.png", "question": "What is selected?"}))
				} else {
					all, _ := json.Marshal(body)
					if !bytes.Contains(all, []byte("Documents is selected.")) {
						t.Error("fallback observations lost")
					}
					replyHarnessTest(w, provider, mainRequests, "Documents is selected.")
				}
			}))
			defer server.Close()
			if err := runImageHarness(t, runner, provider, server.URL, "vision-only", nil); err != nil {
				t.Fatalf("loop: %v\n%s", err, trace)
			}
			if mainRequests != 2 || visionRequests != 1 || !strings.Contains(trace.String(), `"native_input":false`) {
				t.Fatalf("main=%d vision=%d trace=%s", mainRequests, visionRequests, trace)
			}
		})
	}
}

func TestNativeImageHistorySurvivesCheckpointAndCompaction(t *testing.T) {
	runner, _ := imageHarnessRunner(t)
	viewer := nativeImageViewer{workspace: runner.Files.Workspace, artifactDir: runner.ArtifactsDir}
	result, err := viewer.ViewImage(context.Background(), script.ImageAnalysisRequest{Path: "page.png"})
	if err != nil {
		t.Fatal(err)
	}
	messages := []modeladapter.Message{{Role: "system", Content: "Test harness"}, {Role: "user", Content: runner.Prompt},
		{Role: "assistant", Content: strings.Repeat("old transcript ", 20000)}, toolImageMessage("view", result)}
	store := newThreadStateStore(t.TempDir(), "image-test", runner.Files.Workspace.Root, "run-test", time.Now())
	if err := store.SaveCheckpoint("image_result", messages, false); err != nil {
		t.Fatal(err)
	}
	resumed, err := loadResumeContext(store.DataDir, store.ThreadID, runner.Files.Workspace.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed.ExactMessages) != len(messages) || len(resumed.ExactMessages[3].Images) != 1 {
		t.Fatalf("resume lost image: %+v", resumed)
	}
	// A mutable screenshot is overwritten after viewing. The saved pixels survive.
	if err := os.WriteFile(filepath.Join(runner.Files.Workspace.Root, "page.png"), []byte("overwritten"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resumed.ExactMessages[3].Images[0].DataURL(); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"openai", "openrouter"} {
		for _, native := range []bool{true, false} {
			visionModel := ""
			wantImages := 1
			if !native {
				visionModel, wantImages = "vision-only", 0
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&body)
				if got := requestImageCount(t, body, provider); got != wantImages {
					t.Errorf("resumed %s native=%v: images=%d, want %d", provider, native, got, wantImages)
				}
				replyHarnessTest(w, provider, 1, "The saved observations remain available.")
			}))
			if err := runImageHarness(t, runner, provider, server.URL, visionModel, resumed); err != nil {
				t.Errorf("resumed %s native=%v: %v", provider, native, err)
			}
			server.Close()
		}
	}
	packed, _, compacted := compactOpenRouterLoopMessages(resumed.ExactMessages)
	if !compacted || len(retainedImageMessages(packed)) != 1 {
		t.Fatal("compaction lost image pixels")
	}
	_, request, _ := openRouterCurrentTaskMessageAnchors(packed)
	if request != runner.Prompt {
		t.Fatalf("image replaced user objective: %q", request)
	}
	final, _ := compactOpenRouterFinalMessages(packed, "Finish")
	if len(retainedImageMessages(final)) != 0 || !strings.Contains(final[len(final)-1].Content, "Image pixels are omitted") {
		t.Fatal("final handoff must state the visual limitation")
	}
	cloned := cloneModelMessages(packed)
	if !boundConversationImages(cloned, false) || len(retainedImageMessages(cloned)) != 0 || len(retainedImageMessages(packed)) != 1 {
		t.Fatal("model switch did not isolate image history")
	}
	if err := os.Remove(result.Images[0].Path); err != nil {
		t.Fatal(err)
	}
	if _, err := result.Images[0].DataURL(); err == nil {
		t.Fatal("missing saved pixels were not detected")
	}
}

func TestNativeImageBoundsAndWorkspaceScope(t *testing.T) {
	runner, _ := imageHarnessRunner(t)
	workspace := runner.Files.Workspace
	workspace.WorkspaceOnlyReads = true
	viewer := nativeImageViewer{workspace: workspace, artifactDir: runner.ArtifactsDir}
	outside := filepath.Join(t.TempDir(), "outside.png")
	img := testVisionPNG(t, outside, 1, 1, color.White)
	if err := os.WriteFile(outside, img.Data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := viewer.ViewImage(context.Background(), script.ImageAnalysisRequest{Path: outside}); !policy.IsDenied(err) {
		t.Fatalf("outside read: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace.Root, "link.png")); err != nil {
		t.Fatal(err)
	}
	if _, err := viewer.ViewImage(context.Background(), script.ImageAnalysisRequest{Path: "link.png"}); !policy.IsDenied(err) {
		t.Fatalf("symlink read: %v", err)
	}
	result, err := viewer.ViewImage(context.Background(), script.ImageAnalysisRequest{Path: "page.png"})
	if err != nil {
		t.Fatal(err)
	}
	var messages []modeladapter.Message
	for i := 0; i < 6; i++ {
		messages = append(messages, toolImageMessage("view", result))
	}
	if !boundConversationImages(messages, true) || len(retainedImageMessages(messages)) != maxConversationImages || len(messages[0].Images) != 0 {
		t.Fatal("image count was not bounded to the latest images")
	}
	messages = []modeladapter.Message{{Images: []imagemedia.Reference{{Bytes: imagemedia.MaxBytes}}}, {Images: []imagemedia.Reference{{Bytes: 1}}}}
	if !boundConversationImages(messages, true) || len(messages[0].Images) != 0 || len(messages[1].Images) != 1 {
		t.Fatal("image bytes were not bounded")
	}
}
