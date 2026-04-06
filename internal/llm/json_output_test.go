package llm

import (
	"strings"
	"testing"
)

func TestDecodeJSONObjectOutput(t *testing.T) {
	t.Parallel()

	type payload struct {
		Message string `json:"message"`
	}

	tests := []struct {
		name string
		text string
	}{
		{
			name: "plain json",
			text: `{"message":"Improve commit message parsing"}`,
		},
		{
			name: "json fenced output",
			text: "```json\n{\"message\":\"Improve commit message parsing\"}\n```",
		},
		{
			name: "json embedded in prose",
			text: "Here is the payload:\n{\"message\":\"Improve commit message parsing\"}",
		},
		{
			name: "json with trailing prose",
			text: "{\"message\":\"Improve commit message parsing\"}\nThat is the result.",
		},
		{
			name: "thinking block before json",
			text: "<think>\nNeed to think.\n</think>\n{\"message\":\"Improve commit message parsing\"}",
		},
		{
			name: "thinking block before fenced json",
			text: "<think>\nNeed to think.\n</think>\n```json\n{\"message\":\"Improve commit message parsing\"}\n```",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var decoded payload
			if err := DecodeJSONObjectOutput(tc.text, &decoded); err != nil {
				t.Fatalf("DecodeJSONObjectOutput() error = %v", err)
			}
			if decoded.Message != "Improve commit message parsing" {
				t.Fatalf("message = %q, want recovered subject", decoded.Message)
			}
		})
	}
}

func TestDecodeJSONObjectOutputIncludesPreviewOnFailure(t *testing.T) {
	t.Parallel()

	var decoded struct {
		Message string `json:"message"`
	}
	err := DecodeJSONObjectOutput("classification=completed", &decoded)
	if err == nil {
		t.Fatalf("expected DecodeJSONObjectOutput() to fail")
	}
	if !strings.Contains(err.Error(), `preview="classification=completed"`) {
		t.Fatalf("error = %q, want preview", err.Error())
	}
	if !strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("error = %q, want JSON parse cause", err.Error())
	}
}

func TestStripMarkdownCodeBlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain json",
			input: `{"category": "completed", "summary": "done"}`,
			want:  `{"category": "completed", "summary": "done"}`,
		},
		{
			name:  "json in code block",
			input: "```json\n{\"category\": \"completed\", \"summary\": \"done\"}\n```",
			want:  `{"category": "completed", "summary": "done"}`,
		},
		{
			name:  "json in code block without language",
			input: "```\n{\"category\": \"completed\", \"summary\": \"done\"}\n```",
			want:  `{"category": "completed", "summary": "done"}`,
		},
		{
			name:  "json in code block with extra whitespace",
			input: "```json\n  {\"category\": \"completed\", \"summary\": \"done\"}  \n```",
			want:  `{"category": "completed", "summary": "done"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripMarkdownCodeBlock(tt.input)
			if got != tt.want {
				t.Errorf("StripMarkdownCodeBlock() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStripThinkingBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain json",
			input: `{"category": "completed", "summary": "done"}`,
			want:  `{"category": "completed", "summary": "done"}`,
		},
		{
			name:  "leading thinking block",
			input: "<think>\ninternal reasoning\n</think>\n{\"category\": \"completed\", \"summary\": \"done\"}",
			want:  `{"category": "completed", "summary": "done"}`,
		},
		{
			name:  "multiple leading thinking blocks",
			input: "<think>\nfirst\n</think>\n<think>\nsecond\n</think>\n{\"category\": \"completed\", \"summary\": \"done\"}",
			want:  `{"category": "completed", "summary": "done"}`,
		},
		{
			name:  "missing closing tag leaves text alone",
			input: "<think>\ninternal reasoning\n{\"category\": \"completed\", \"summary\": \"done\"}",
			want:  "<think>\ninternal reasoning\n{\"category\": \"completed\", \"summary\": \"done\"}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripThinkingBlocks(tt.input)
			if got != tt.want {
				t.Errorf("StripThinkingBlocks() = %q, want %q", got, tt.want)
			}
		})
	}
}
