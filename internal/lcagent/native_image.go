package lcagent

import (
	"context"
	"fmt"
	"strings"

	"lcroom/internal/lcagent/imagemedia"
	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/policy"
	"lcroom/internal/lcagent/script"
	"lcroom/internal/lcagent/tools"
)

const toolImageOrigin = "tool_image"
const maxConversationImages = 4

type nativeImageViewer struct {
	workspace   policy.Workspace
	artifactDir string
}

func (v nativeImageViewer) ViewImage(ctx context.Context, request script.ImageAnalysisRequest) (tools.ToolResult, error) {
	paths := []string{request.Path}
	if request.ComparisonPath != "" {
		paths = append(paths, request.ComparisonPath)
	}
	result := tools.ToolResult{Success: true}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return tools.ToolResult{}, err
		}
		resolved, err := v.workspace.ResolveRead(path)
		if err != nil {
			return tools.ToolResult{}, err
		}
		ref, err := imagemedia.Store(resolved, v.artifactDir)
		if err != nil {
			return tools.ToolResult{}, err
		}
		result.Images = append(result.Images, ref)
	}
	result.Output = "Image pixels will be attached to the ongoing conversation. Inspect them directly; loading an image does not verify acceptance criteria."
	if request.Question != "" {
		result.Output += "\nQuestion: " + request.Question
	}
	if request.Context != "" {
		result.Output += "\nContext: " + request.Context
	}
	if len(request.Checks) > 0 {
		result.Output += "\nObservation checks: " + strings.Join(request.Checks, "; ")
	}
	return result, nil
}

func toolImageMessage(callID string, result tools.ToolResult) modeladapter.Message {
	var caption strings.Builder
	fmt.Fprintf(&caption, "Image output from tool call %s. This is not a new user request.\n%s", callID, result.Output)
	for i, ref := range result.Images {
		fmt.Fprintf(&caption, "\nImage %d: %s (saved pixels: %s)", i+1, ref.Source, ref.Path)
	}
	return modeladapter.Message{Role: "user", Origin: toolImageOrigin, Content: caption.String(), Images: append([]imagemedia.Reference(nil), result.Images...)}
}

// Bound both pixels per request and encoded payload size. Keep text references
// to evicted images so the agent can reload one if it becomes relevant again.
// The caller resets provider continuation when this changes retained pixels.
func boundConversationImages(messages []modeladapter.Message, enabled bool) bool {
	count := 0
	var bytes int64
	changed := false
	for i := len(messages) - 1; i >= 0; i-- {
		msg := &messages[i]
		var retained []imagemedia.Reference
		for _, ref := range msg.Images {
			if enabled && count < maxConversationImages && ref.Bytes > 0 && bytes+ref.Bytes <= imagemedia.MaxBytes {
				retained = append(retained, ref)
				count++
				bytes += ref.Bytes
				continue
			}
			msg.Content += fmt.Sprintf("\nPixels for %s are no longer attached; use view_image on %s to inspect them again.", ref.Source, ref.Path)
			changed = true
		}
		msg.Images = retained
	}
	return changed
}

func retainedImageMessages(messages []modeladapter.Message) []modeladapter.Message {
	var out []modeladapter.Message
	for _, msg := range messages {
		if len(msg.Images) > 0 {
			out = append(out, msg)
		}
	}
	return cloneModelMessages(out)
}
