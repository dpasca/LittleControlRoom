package codexapp

import (
	"encoding/json"
	"lcroom/internal/projectrun"
	"strings"
	"testing"
)

func TestImageRecoveryContextAndLaunchAreOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		s := &appServerSession{imageReviewEnabled: enabled, runtimeMCPExpected: true}
		_, found := s.managedTurnContext()["little-control-room/image-review-recovery"]
		if found != enabled {
			t.Fatalf("context should be opt-in: %v", enabled)
		}
		req := LaunchRequest{Provider: ProviderCodex, ProjectPath: "/tmp/project", CLIExecutablePath: "/tmp/lcroom", RuntimeManager: projectrun.NewManager(), ImageReviewEnabled: enabled}
		t.Cleanup(func() { _ = req.RuntimeManager.CloseAll() })
		_, args, ok := runtimeMCPCommand(req)
		if !ok || strings.Contains(strings.Join(args, " "), "--image-review") != enabled {
			t.Fatalf("unexpected MCP launch: %v", args)
		}
		req.ImageReviewAPIKey = "private-review-key"
		overrides := strings.Join(codexRuntimeMCPConfigOverrides(req), "\n")
		if strings.Contains(overrides, "LCR_IMAGE_REVIEW_API_KEY") != enabled || strings.Contains(overrides, "private-review-key") {
			t.Fatal("MCP credentials must be opt-in environment references only")
		}
		if enabled {
			err := s.SubmitInput(Submission{Attachments: []Attachment{{Kind: AttachmentLocalImage, Path: "/tmp/image.png"}}})
			if err == nil || !strings.Contains(err.Error(), "image recovery") {
				t.Fatalf("image attachment not rejected: %v", err)
			}
		}
	}
}

func TestCodexErrorKeepsStructuredDetails(t *testing.T) {
	var e resumedTurnError
	if err := json.Unmarshal([]byte(`{"message":"Bad Request","codexErrorInfo":{"httpConnectionFailed":{"httpStatusCode":400}},"additionalDetails":"unsupported image detail"}`), &e); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Bad Request", "httpStatusCode", "400", "unsupported image detail"} {
		if !strings.Contains(e.diagnosticText(), want) {
			t.Fatalf("missing %s in %s", want, e.diagnosticText())
		}
	}
	if got := (resumedTurnError{Message: "legacy error"}).diagnosticText(); got != "legacy error" {
		t.Fatalf("legacy message changed: %s", got)
	}
	s := &appServerSession{notify: func() {}}
	s.handleNotification("error", json.RawMessage(`{"error":{"message":"Bad Request","codexErrorInfo":"BadRequest","additionalDetails":"unsupported image detail"}}`))
	if !strings.Contains(s.lastError, "unsupported image detail") {
		t.Fatalf("live handler dropped details: %s", s.lastError)
	}
}
