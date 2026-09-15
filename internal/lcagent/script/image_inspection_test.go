package script

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"lcroom/internal/lcagent/session"
	"lcroom/internal/lcagent/tools"
)

func TestImageInspectionDoesNotCreateOrEraseVerificationEvidence(t *testing.T) {
	var stream bytes.Buffer
	writer, id, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	analyzer := &fakeImageAnalyzer{} // Returns a failing visual verdict.
	runner := Runner{Session: writer, SessionID: id, ImageAnalyzer: analyzer}
	inspect := Action{Type: "tool_call", Tool: "analyze_image", Args: raw(`{"path":"chooser.png","question":"Which folder is selected?"}`)}
	result, err := runner.RunTool(context.Background(), inspect)
	if err != nil || !result.Success || analyzer.request.Purpose != "inspect" {
		t.Fatalf("inspection: result=%+v err=%v request=%+v", result, err, analyzer.request)
	}
	if strings.Contains(result.Output, "verdict:") || strings.Contains(result.Output, "non_passing") || runner.VisualEvidence().HasEvidence() {
		t.Fatalf("inspection became verification: %s; %+v", result.Output, runner.VisualEvidence())
	}
	if audit := runner.FinalResponseAudit(Action{Type: "final_response", Outcome: "completed", Summary: "The chooser is open."}); audit.VerificationStatus == "failed" {
		t.Fatalf("inspection failed the final audit: %+v", audit)
	}
	_, err = runner.RunTool(context.Background(), Action{Type: "tool_call", Tool: "analyze_image", Args: raw(`{"purpose":"verify","path":"result.png","question":"Does the artifact include the requested boardwalk?"}`)})
	if err != nil {
		t.Fatal(err)
	}
	before := runner.VisualEvidence()
	if before.NonPassing != 1 || before.LatestVerdict != ImageAnalysisVerdictFail {
		t.Fatalf("explicit verification lost: %+v", before)
	}
	// A native loader bypasses auxiliary inference, but must preserve the same
	// observation/acceptance boundary as a text-only model's vision fallback.
	runner.ImageViewer = fakeNativeImageViewer{}
	_, err = runner.RunTool(context.Background(), inspect)
	if err != nil || runner.VisualEvidence() != before {
		t.Fatalf("later inspection changed prior verification: before=%+v after=%+v err=%v", before, runner.VisualEvidence(), err)
	}
	_, err = runner.RunTool(context.Background(), Action{Type: "tool_call", Tool: "view_image", Args: raw(`{"path":"chooser.png"}`)})
	if err != nil || runner.VisualEvidence() != before {
		t.Fatalf("native view_image changed verification: %+v err=%v", runner.VisualEvidence(), err)
	}
	for _, line := range strings.Split(strings.TrimSpace(stream.String()), "\n") {
		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if string(event["type"]) == `"image_analysis_result"` && string(event["purpose"]) == `"inspect"` && string(event["verdict"]) != `""` {
			t.Fatalf("inspection trace has a verification verdict: %s", line)
		}
	}
}

type fakeNativeImageViewer struct{}

func (fakeNativeImageViewer) ViewImage(context.Context, ImageAnalysisRequest) (tools.ToolResult, error) {
	return tools.ToolResult{Success: true, Output: "Pixels loaded directly."}, nil
}

func TestImageInspectionFailureDoesNotBecomeAcceptanceFailure(t *testing.T) {
	runner := Runner{ImageAnalyzer: &fakeFailingImageAnalyzer{}}
	result, _ := runner.RunTool(context.Background(), Action{Type: "tool_call", Tool: "analyze_image", Args: raw(`{"purpose":"inspect","path":"chooser.png","question":"What is visible?"}`)})
	if result.Success || runner.VisualEvidence().HasEvidence() {
		t.Fatalf("failure: result=%+v evidence=%+v", result, runner.VisualEvidence())
	}
}
