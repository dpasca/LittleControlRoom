package modeladapter

import (
	"fmt"
	"strings"
)

type ToolOptions struct {
	ToolProfile             string
	DefaultReadLineLimit    int
	MaxReadLineLimit        int
	DefaultListEntryLimit   int
	MaxListEntryLimit       int
	DefaultSearchMaxMatch   int
	MaxSearchMaxMatch       int
	MaxSearchContextLines   int
	DefaultOutlineFileLimit int
	MaxOutlineFileLimit     int
	MaxModuleOutlineChars   int
	WebSearchEnabled        bool
}

func Tools() []ToolDefinition {
	return ToolsWithOptions(ToolOptions{})
}

func ToolsWithOptions(opts ToolOptions) []ToolDefinition {
	opts = opts.withDefaults()
	readDescription := "Read a bounded line range from a text file. Prefer targeted ranges after search or file_outline; use broad reads only when the whole range is relevant."
	limitDescription := fmt.Sprintf("Maximum lines to read. For scouting, prefer 40-100 lines; larger ranges are allowed when needed. Defaults to %d.", opts.DefaultReadLineLimit)
	if strings.EqualFold(opts.ToolProfile, "generous") {
		readDescription = "Read a bounded line range from a text file. Larger read limits are available in this run: after search or file_outline identifies a central file, prefer contiguous evidence-complete ranges over tiny samples."
		limitDescription = fmt.Sprintf("Maximum lines to read. For central files, prefer 120-300 line ranges and continue with next_offset when useful. Defaults to %d.", opts.DefaultReadLineLimit)
	}
	inspectionPathDescription := "Workspace-relative path, or absolute path for read-only inspection outside the workspace."
	defs := []ToolDefinition{
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "read_file",
				Description: readDescription,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":   map[string]any{"type": "string", "description": inspectionPathDescription},
						"offset": map[string]any{"type": "integer", "minimum": 1, "description": "1-based starting line. Defaults to 1."},
						"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": opts.MaxReadLineLimit, "description": limitDescription},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "file_outline",
				Description: "Summarize structure for a source or Markdown file before reading raw text, returning headings or symbols with line ranges. Supports Go with parser-backed symbols, Markdown headings, and lightweight symbol outlines for Python, JavaScript/TypeScript, Rust, C/C++, C#, Java, Kotlin, and Swift.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "Workspace-relative path, or absolute path, to a supported source or Markdown file for read-only inspection."},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "module_outline",
				Description: "Summarize structure for many supported source or Markdown files under a workspace directory before broad review. Hidden/generated directories are skipped by default but reported as skipped; set include_hidden=true only when the task truly needs them.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":           map[string]any{"type": "string", "description": "Workspace-relative directory or file, or absolute path for read-only inspection. Defaults to workspace root."},
						"file_glob":      map[string]any{"type": "string", "description": "Optional filepath glob matched against relative path or basename, for example *.go."},
						"max_files":      map[string]any{"type": "integer", "minimum": 1, "maximum": opts.MaxOutlineFileLimit, "description": fmt.Sprintf("Maximum files to outline. Defaults to %d.", opts.DefaultOutlineFileLimit)},
						"include_hidden": map[string]any{"type": "boolean", "description": "Descend into normally hidden/generated directories such as .git, .venv, node_modules, vendor, dist, or build. Defaults to false; only enable when those contents are directly relevant."},
					},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "repo_overview",
				Description: "Get a quick deterministic repository overview before broad exploration: Git branch/status, shallow tree, skipped hidden/generated directories, important manifests, project hints, and a representative file sample. Use include_hidden=true only when hidden/generated contents are directly relevant.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":           map[string]any{"type": "string", "description": "Workspace-relative directory or file, or absolute path for read-only inspection. Defaults to workspace root."},
						"max_files":      map[string]any{"type": "integer", "minimum": 1, "maximum": 500, "description": "Maximum representative files to include. Defaults to 120."},
						"include_hidden": map[string]any{"type": "boolean", "description": "Descend into normally hidden/generated directories such as .git, .venv, node_modules, vendor, dist, or build. Defaults to false; skipped directories are reported."},
					},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "list_files",
				Description: "List files under a workspace path, optionally filtered by a simple glob. Hidden/generated directories are listed as placeholders but not descended into by default; set include_hidden=true only when those contents are directly relevant.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":           map[string]any{"type": "string", "description": "Workspace-relative directory or file to list, or absolute path for read-only inspection. Defaults to workspace root."},
						"glob":           map[string]any{"type": "string", "description": "Optional filepath glob matched against relative path or basename."},
						"max_entries":    map[string]any{"type": "integer", "minimum": 1, "maximum": opts.MaxListEntryLimit},
						"include_hidden": map[string]any{"type": "boolean", "description": "Descend into normally hidden/generated directories such as .git, .venv, node_modules, vendor, dist, or build. Defaults to false."},
					},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "search",
				Description: "Search text files in the workspace with case-insensitive literal substring matching, optionally returning a small context window around each match. The query is not a regex, glob, or alternation pattern.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"query":          map[string]any{"type": "string", "description": "Case-insensitive literal substring to find. Do not use regex syntax; use separate calls for separate identifiers or phrases."},
						"path":           map[string]any{"type": "string", "description": "Workspace-relative directory or file to search, or absolute path for read-only inspection. Defaults to workspace root."},
						"file_glob":      map[string]any{"type": "string", "description": "Optional filepath glob matched against relative path or basename."},
						"max_matches":    map[string]any{"type": "integer", "minimum": 1, "maximum": opts.MaxSearchMaxMatch},
						"context_before": map[string]any{"type": "integer", "minimum": 0, "maximum": opts.MaxSearchContextLines, "description": "Lines of context before each match. Defaults to 1 in the harness."},
						"context_after":  map[string]any{"type": "integer", "minimum": 0, "maximum": opts.MaxSearchContextLines, "description": "Lines of context after each match. Defaults to 2 in the harness."},
						"include_hidden": map[string]any{"type": "boolean", "description": "Search normally hidden/generated directories such as .git, .venv, node_modules, vendor, dist, or build. Defaults to false; skipped directories are reported in the result."},
					},
					"required": []string{"query"},
				},
			},
		},
	}
	if opts.WebSearchEnabled {
		defs = append(defs, ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "web_search",
				Description: "Search the public web for current external information and return concise source results with URLs. Use this for discovery; use run_command or file tools for workspace-local evidence.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"query":        map[string]any{"type": "string", "description": "Natural-language search query."},
						"max_results":  map[string]any{"type": "integer", "minimum": 1, "maximum": 10, "description": "Maximum results to return. Defaults to 5."},
						"site":         map[string]any{"type": "string", "description": "Optional domain to restrict results, for example docs.example.com."},
						"recency_days": map[string]any{"type": "integer", "minimum": 1, "maximum": 365, "description": "Optional recency window in days."},
					},
					"required": []string{"query"},
				},
			},
		})
	}
	defs = append(defs,
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "load_skill",
				Description: "Load the full body of an available skill by name. Use only after checking the skill metadata in the system prompt.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"name": map[string]any{"type": "string"},
					},
					"required": []string{"name"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "run_command",
				Description: "Run a bounded command in the workspace. Prefer argv. Use shell command strings only when shell behavior is genuinely needed.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"argv":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Command argv, for example [\"go\",\"test\",\"./...\"]"},
						"command":    map[string]any{"type": "string", "description": "Legacy shell command string. Prefer argv unless shell syntax is required."},
						"shell":      map[string]any{"type": "boolean", "description": "Set true when using command as a shell string."},
						"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
						"purpose":    map[string]any{"type": "string", "enum": []string{"inspect", "verify"}, "description": "Use verify for tests, linters, typechecks, builds, or other commands intended to verify the final answer. Use inspect for read-only exploration."},
					},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "apply_patch",
				Description: "Apply a Codex apply_patch patch. The patch must use the exact envelope: *** Begin Patch, then *** Update File: path or *** Add File: path, hunks with @@ and +/- lines, then *** End Patch. Successful patches return a diff summary; failed stale hunks may return suggested read_file ranges to refresh before retrying. Example: *** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** End Patch",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"patch": map[string]any{"type": "string"},
					},
					"required": []string{"patch"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "replace_text",
				Description: "Replace an exact literal text span in one existing workspace file. Use this as a fallback for small edits when apply_patch syntax is failing and you can provide a unique old_text copied from read_file output. This is not regex-based; old_text must match exactly. Defaults to exactly one replacement.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":                  map[string]any{"type": "string", "description": "Workspace-relative path to an existing text file. Absolute paths are denied."},
						"old_text":              map[string]any{"type": "string", "description": "Exact current text to replace. Re-read the target range first if unsure."},
						"new_text":              map[string]any{"type": "string", "description": "Replacement text."},
						"expected_replacements": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Required number of occurrences. Defaults to 1 so accidental broad edits fail."},
					},
					"required": []string{"path", "old_text", "new_text"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "update_plan",
				Description: "Publish the current short plan with statuses pending, in_progress, or completed.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"items": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type":                 "object",
								"additionalProperties": false,
								"properties": map[string]any{
									"step":   map[string]any{"type": "string"},
									"status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}},
								},
								"required": []string{"step", "status"},
							},
						},
					},
					"required": []string{"items"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "final_response",
				Description: "Finish the session. The summary must be the complete user-facing answer, including findings, caveats, changed files, verification outcome, and next steps; do not put essential answer content only in verification.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"summary":       map[string]any{"type": "string", "description": "Complete user-facing answer. Include the actual findings or outcome here, not only a preamble."},
						"files_changed": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Workspace files changed by this session, or an empty array."},
						"verification":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Checks run, evidence reviewed, or an explicit not-run reason. This supports the summary; it is not a substitute for the answer."},
					},
					"required": []string{"summary", "files_changed", "verification"},
				},
			},
		},
	)
	return defs
}

func (o ToolOptions) withDefaults() ToolOptions {
	if o.DefaultReadLineLimit <= 0 {
		o.DefaultReadLineLimit = 200
	}
	if o.MaxReadLineLimit <= 0 {
		o.MaxReadLineLimit = 1000
	}
	if o.DefaultListEntryLimit <= 0 {
		o.DefaultListEntryLimit = 200
	}
	if o.MaxListEntryLimit <= 0 {
		o.MaxListEntryLimit = 1000
	}
	if o.DefaultSearchMaxMatch <= 0 {
		o.DefaultSearchMaxMatch = 50
	}
	if o.MaxSearchMaxMatch <= 0 {
		o.MaxSearchMaxMatch = 200
	}
	if o.MaxSearchContextLines <= 0 {
		o.MaxSearchContextLines = 8
	}
	if o.DefaultOutlineFileLimit <= 0 {
		o.DefaultOutlineFileLimit = 30
	}
	if o.MaxOutlineFileLimit <= 0 {
		o.MaxOutlineFileLimit = 80
	}
	if o.MaxModuleOutlineChars <= 0 {
		o.MaxModuleOutlineChars = 24000
	}
	if o.MaxReadLineLimit < o.DefaultReadLineLimit {
		o.MaxReadLineLimit = o.DefaultReadLineLimit
	}
	if o.MaxListEntryLimit < o.DefaultListEntryLimit {
		o.MaxListEntryLimit = o.DefaultListEntryLimit
	}
	if o.MaxSearchMaxMatch < o.DefaultSearchMaxMatch {
		o.MaxSearchMaxMatch = o.DefaultSearchMaxMatch
	}
	if o.MaxOutlineFileLimit < o.DefaultOutlineFileLimit {
		o.MaxOutlineFileLimit = o.DefaultOutlineFileLimit
	}
	return o
}
