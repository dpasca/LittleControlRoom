package runtimemcp

import (
	"context"
	"encoding/json"
	"lcroom/internal/imagereview"
)

func (s *Server) tools() []mcpTool {
	tools := runtimeTools(s.todoMode, s.supportsStructuredTools(), s.claudeApprovalSocket != "")
	if s.imageReviewEnabled {
		tools = append(tools, mcpTool{
			Name:        "inspect_images",
			Description: "External API image review, explicitly enabled by the operator for this session as recovery from native image failures. Use only for necessary visual checks. Returns text findings, never images. Sends 1–4 workspace images to gpt-5.6-luna via the configured OpenAI API; API usage is billed separately. Supply a focused question and any diagnostic legends. Do not treat a successful call as visual acceptance: read its findings and uncertainties.",
			InputSchema: map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"paths":    map[string]any{"type": "array", "minItems": 1, "maxItems": imagereview.MaxImages, "items": map[string]any{"type": "string"}},
					"question": map[string]any{"type": "string", "minLength": 1, "maxLength": 8000},
					"context":  map[string]any{"type": "string", "maxLength": 16000},
				},
				"required": []string{"paths", "question"},
			},
		})
	}
	return tools
}

func (s *Server) inspectImages(ctx context.Context, args json.RawMessage) (toolCallResult, error) {
	if !s.imageReviewEnabled {
		return s.jsonToolResult(map[string]any{"success": false, "error": "External image review is not enabled for this session"}, true)
	}
	var req imagereview.Request
	if err := decodeStrictToolArgs(args, &req); err != nil {
		return s.jsonToolResult(map[string]any{"success": false, "error": err.Error()}, true)
	}
	client, err := imagereview.NewClient()
	if err != nil {
		return s.jsonToolResult(map[string]any{"success": false, "error": err.Error()}, true)
	}
	result := imagereview.Review(ctx, client, s.projectPath, req)
	return s.jsonToolResult(result, !result.Success)
}
