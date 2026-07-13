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
	ManagedProcessesEnabled bool
	AdminWrite              bool
	BrowserAvailable        bool
	VisionAnalysisEnabled   bool
	WorkspaceOnlyReads      bool
	ReadOnly                bool
}

func Tools() []ToolDefinition {
	return ToolsWithOptions(ToolOptions{})
}

func ToolsWithOptions(opts ToolOptions) []ToolDefinition {
	opts = opts.withDefaults()
	readDescription := "Read a bounded text-file line range. Prefer targeted ranges after search or file_outline."
	limitDescription := fmt.Sprintf("Max lines. Defaults to %d; scout with 40-100 unless more is relevant.", opts.DefaultReadLineLimit)
	if strings.EqualFold(opts.ToolProfile, "generous") {
		readDescription = "Read a bounded text-file line range. Larger read limits are available; after search/file_outline, read contiguous central context."
		limitDescription = fmt.Sprintf("Max lines. Defaults to %d; central files often need 120-300 plus next_offset.", opts.DefaultReadLineLimit)
	}
	inspectionPathDescription := "Workspace-relative path, or absolute path for read-only inspection outside the workspace."
	if opts.WorkspaceOnlyReads {
		inspectionPathDescription = "Workspace-relative path, or absolute path inside the workspace. Reads outside the workspace are denied for this run."
	}
	writePathDescription := "Workspace-relative path, or absolute path inside the workspace; outside requires --admin-write."
	patchPathDescription := "Patch paths may be workspace-relative or inside-workspace absolute paths; outside requires --admin-write."
	if opts.AdminWrite {
		writePathDescription = "Workspace-relative path, or absolute path for explicit admin-write edits outside the workspace."
		patchPathDescription = "Patch file paths may be workspace-relative or absolute because admin-write mode is enabled."
	}
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
						"offset": map[string]any{"type": "integer", "minimum": 1, "description": "1-based start line. Defaults to 1."},
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
				Description: "Outline a supported source or Markdown file, returning symbols/headings with line ranges.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": inspectionPathDescription},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "module_outline",
				Description: "Outline many supported source or Markdown files under a directory. Skips hidden/generated dirs unless include_hidden=true.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":           map[string]any{"type": "string", "description": "Directory/file path. Defaults to workspace root; absolute allowed for read-only inspection."},
						"file_glob":      map[string]any{"type": "string", "description": "Optional filepath glob, e.g. *.go."},
						"max_files":      map[string]any{"type": "integer", "minimum": 1, "maximum": opts.MaxOutlineFileLimit, "description": fmt.Sprintf("Max files. Defaults to %d.", opts.DefaultOutlineFileLimit)},
						"include_hidden": map[string]any{"type": "boolean", "description": "Descend hidden/generated dirs. Defaults false."},
					},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "repo_overview",
				Description: "Get a deterministic repository overview: branch/status, shallow tree, skipped dirs, manifests, hints, file sample.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":           map[string]any{"type": "string", "description": "Directory/file path. Defaults to workspace root; absolute allowed for read-only inspection."},
						"max_files":      map[string]any{"type": "integer", "minimum": 1, "maximum": 500, "description": "Max representative files. Defaults to 120."},
						"include_hidden": map[string]any{"type": "boolean", "description": "Descend hidden/generated dirs. Defaults false."},
					},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "list_files",
				Description: "List files under a path, optionally glob-filtered. Case-sensitive by default; include_hidden descends into skipped dirs.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":           map[string]any{"type": "string", "description": "Directory/file path. Defaults to workspace root; absolute allowed for read-only inspection."},
						"glob":           map[string]any{"type": "string", "description": "Optional filepath glob; ** is recursive."},
						"max_entries":    map[string]any{"type": "integer", "minimum": 1, "maximum": opts.MaxListEntryLimit},
						"case_sensitive": map[string]any{"type": "boolean", "description": "Glob case sensitivity. Defaults true."},
						"include_hidden": map[string]any{"type": "boolean", "description": "Descend hidden/generated dirs. Defaults false."},
					},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "search",
				Description: "Search text files by case-insensitive literal substring; query is not a regex. Use compact+intent for broad/noisy searches.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"query":          map[string]any{"type": "string", "description": "Literal substring. Do not use regex syntax."},
						"path":           map[string]any{"type": "string", "description": "Directory/file path. Defaults to workspace root; absolute allowed for read-only inspection."},
						"file_glob":      map[string]any{"type": "string", "description": "Optional filepath glob."},
						"max_matches":    map[string]any{"type": "integer", "minimum": 1, "maximum": opts.MaxSearchMaxMatch},
						"context_before": map[string]any{"type": "integer", "minimum": 0, "maximum": opts.MaxSearchContextLines, "description": "Context lines before matches. Defaults to 1."},
						"context_after":  map[string]any{"type": "integer", "minimum": 0, "maximum": opts.MaxSearchContextLines, "description": "Context lines after matches. Defaults to 2."},
						"include_hidden": map[string]any{"type": "boolean", "description": "Search hidden/generated dirs. Defaults false."},
						"output_mode":    map[string]any{"type": "string", "enum": []string{"full", "compact"}, "description": "compact returns match lines only. Defaults full."},
						"intent":         map[string]any{"type": "string", "description": "Search purpose; helps compact result refinement."},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "scout_files",
				Description: "Ask the utility/scout model to inspect a bounded file pack and suggest relevant files/ranges. This is read-only and advisory; verify with read_file.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"question":           map[string]any{"type": "string", "description": "Question for the scout."},
						"path":               map[string]any{"type": "string", "description": "Directory/file path to pack. Defaults to workspace root."},
						"file_glob":          map[string]any{"type": "string", "description": "Optional filepath glob."},
						"max_files":          map[string]any{"type": "integer", "minimum": 1, "maximum": 40, "description": "Max files. Defaults to 12."},
						"max_lines_per_file": map[string]any{"type": "integer", "minimum": 20, "maximum": opts.MaxReadLineLimit, "description": "Max leading lines per file. Defaults to 120."},
						"include_hidden":     map[string]any{"type": "boolean", "description": "Descend hidden/generated dirs. Defaults false."},
					},
					"required": []string{"question"},
				},
			},
		},
	}
	if opts.VisionAnalysisEnabled {
		defs = append(defs, ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "capture_screenshot",
				Description: "Capture a native desktop screenshot artifact; call analyze_image on it when visual evidence is required. May fail if desktop/screen permission is unavailable.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":     map[string]any{"type": "string", "description": "Optional artifact filename/path. Defaults timestamped PNG."},
						"delay_ms": map[string]any{"type": "integer", "minimum": 0, "maximum": 5000, "description": "Delay before capture. Defaults 0."},
					},
				},
			},
		})
		defs = append(defs, ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "analyze_image",
				Description: "Use the vision model for screenshot/image QA or comparison_path checks, returning pass/fail/uncertain pixel-level evidence.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":            map[string]any{"type": "string", "description": "Image path; workspace-relative or read-only absolute artifact path."},
						"comparison_path": map[string]any{"type": "string", "description": "Optional second image for side-by-side/temporal comparison."},
						"question":        map[string]any{"type": "string", "maxLength": 1200, "description": "Focused vision question."},
						"context":         map[string]any{"type": "string", "maxLength": 4000, "description": "Optional expected visual state/context."},
						"checks":          map[string]any{"type": "array", "maxItems": 10, "items": map[string]any{"type": "string", "maxLength": 100}, "description": "Optional concrete visual checks."},
					},
					"required": []string{"path", "question"},
				},
			},
		})
	}
	if opts.WebSearchEnabled {
		defs = append(defs, ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "web_search",
				Description: "Search the public web for current external information; returns concise URL sources.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"query":        map[string]any{"type": "string", "description": "Natural-language search query."},
						"max_results":  map[string]any{"type": "integer", "minimum": 1, "maximum": 10, "description": "Max results. Defaults to 5."},
						"site":         map[string]any{"type": "string", "description": "Optional domain filter."},
						"recency_days": map[string]any{"type": "integer", "minimum": 1, "maximum": 365, "description": "Optional recency window in days."},
					},
					"required": []string{"query"},
				},
			},
		})
	}
	if opts.BrowserAvailable {
		defs = append(defs, browserToolDefinitions()...)
	}
	defs = append(defs,
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "load_skill",
				Description: "Load the full body of an available skill by name after checking prompt metadata.",
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
				Description: "Run a bounded workspace command, max 60000 ms. Choose exactly one form: argv for simple commands, or command for shell syntax. Do not edit files here; use start_process for long-running/managed operational work.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"oneOf": []any{
						map[string]any{"required": []string{"argv"}},
						map[string]any{"required": []string{"command"}},
					},
					"properties": map[string]any{
						"argv":               map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}, "description": "Complete command argv, e.g. [\"go\",\"test\",\"./...\"]. Omit command when using argv."},
						"command":            map[string]any{"type": "string", "minLength": 1, "description": "Shell command string for shell syntax. Omit argv when using command."},
						"cwd":                map[string]any{"type": "string", "description": "Optional workspace-relative working directory."},
						"timeout_ms":         map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
						"purpose":            map[string]any{"type": "string", "enum": []string{"inspect", "verify"}, "description": "verify for tests/builds/checks; inspect for exploration."},
						"admin_scope":        map[string]any{"type": "string", "enum": []string{"system"}, "description": "system only for explicit persistent user/system mutations; requires admin write."},
						"allowed_exit_codes": map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 0, "maximum": 255}, "description": "Explicit success codes for probes where nonzero is expected evidence."},
					},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "start_process",
				Description: "Start/reuse a long-running managed background process for dev servers, watchers, video/export jobs, Dropbox/external sync, or deploy/publish/promote/upload/release work. Use when work should keep running or may exceed 60000 ms.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"command":          map[string]any{"type": "string", "description": "Foreground command, e.g. \"pnpm dev\"."},
						"project_path":     map[string]any{"type": "string", "description": "Optional project root for cross-project process."},
						"cwd":              map[string]any{"type": "string", "description": "Optional working directory within project/workspace."},
						"name":             map[string]any{"type": "string", "description": "Optional short process label."},
						"create_new":       map[string]any{"type": "boolean", "description": "True only for intentional duplicate concurrent copy."},
						"replace_existing": map[string]any{"type": "boolean", "description": "Stop matching running process before starting fresh."},
					},
					"required": []string{"command"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "list_processes",
				Description: "List managed background processes, including state, id, PID, ports, URLs, and recent output.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"project_path": map[string]any{"type": "string", "description": "Optional project root matching start_process."},
						"purpose":      map[string]any{"type": "string", "enum": []string{"inspect", "verify"}, "description": "verify for runtime liveness checks."},
					},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "stop_process",
				Description: "Stop an LCR-owned managed background process.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"project_path": map[string]any{"type": "string", "description": "Optional project root matching start_process."},
						"process_id":   map[string]any{"type": "string", "description": "Optional id from list_processes."},
					},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "create_file",
				Description: "Create one brand-new text file with exact content, without patch syntax; fails if it exists. " + writePathDescription + " Returns diff and sha256.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":    map[string]any{"type": "string", "description": writePathDescription},
						"content": map[string]any{"type": "string", "description": "Complete text content."},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "replace_file",
				Description: "Replace one existing text file with complete content, without patch syntax. Requires expected_sha256 copied from latest read_file; Do not calculate it yourself. " + writePathDescription + " Returns diff and sha256.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":            map[string]any{"type": "string", "description": writePathDescription},
						"content":         map[string]any{"type": "string", "description": "Complete replacement text."},
						"expected_sha256": map[string]any{"type": "string", "description": "sha256 copied from latest read_file. Do not calculate it yourself."},
					},
					"required": []string{"path", "content", "expected_sha256"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "apply_patch",
				Description: "Apply a Codex apply_patch patch: *** Begin Patch, *** Update File/Add File, @@ hunks, *** End Patch. " + patchPathDescription + " Returns diff; stale hunks may suggest read_file ranges. Example: *** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** End Patch",
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
				Description: "Replace an exact literal text span in one existing file; not regex-based. old_text must match exactly. Defaults to one replacement.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":                  map[string]any{"type": "string", "description": writePathDescription},
						"old_text":              map[string]any{"type": "string", "description": "Exact current text to replace."},
						"new_text":              map[string]any{"type": "string", "description": "Replacement text."},
						"expected_replacements": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Required occurrences. Defaults 1."},
					},
					"required": []string{"path", "old_text", "new_text"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "replace_lines",
				Description: "Replace/delete an inclusive 1-based line range in one file; good for larger sections after read_file gives exact line numbers.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":                map[string]any{"type": "string", "description": writePathDescription},
						"start_line":          map[string]any{"type": "integer", "minimum": 1, "description": "First line, inclusive."},
						"end_line":            map[string]any{"type": "integer", "minimum": 1, "description": "Last line, inclusive; empty new_text deletes range."},
						"new_text":            map[string]any{"type": "string", "description": "Replacement text; empty deletes range."},
						"expected_first_line": map[string]any{"type": "string", "description": "Optional current start-line guard."},
						"expected_last_line":  map[string]any{"type": "string", "description": "Optional current end-line guard."},
					},
					"required": []string{"path", "start_line", "end_line", "new_text"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "update_plan",
				Description: `Publish a short execution plan with pending/in_progress/completed. Use {"items":[{"step":"Inspect files","status":"completed"},{"step":"Patch code","status":"in_progress"}]}. After updating, continue with the in_progress step unless blocked.`,
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
				Name:        "update_quality_plan",
				Description: "Publish/refresh a phased quality plan for nontrivial artifact work. Phases are sequential gates: one active phase, planned tail, advance only after evidence. Use phases[].acceptance for concrete checks. Do not mark visual/user-facing phases verified from code alone; evidence must show behavior/visible result. A completed final_response is audited against verified/skipped phases plus required runtime, analyze_image, and comparison_path evidence. One focused visual check is usually enough.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"artifact_type":                         map[string]any{"type": "string", "enum": []string{"game", "ui", "cli", "library", "doc", "other", "unknown"}, "description": "Primary artifact."},
						"requires_runtime_verification":         map[string]any{"type": "boolean", "description": "True when build/run/test/exercise is needed."},
						"requires_visual_verification":          map[string]any{"type": "boolean", "description": "True when completion needs analyze_image evidence."},
						"requires_temporal_visual_verification": map[string]any{"type": "boolean", "description": "True when completion needs comparison_path evidence over time."},
						"phases": map[string]any{
							"type":        "array",
							"minItems":    1,
							"maxItems":    12,
							"description": "Implementation/refinement phases: completed prefix, one active phase, planned tail.",
							"items": map[string]any{
								"type":                 "object",
								"additionalProperties": false,
								"properties": map[string]any{
									"name":       map[string]any{"type": "string", "maxLength": 240, "description": "Short phase name."},
									"status":     map[string]any{"type": "string", "enum": []string{"planned", "in_progress", "implemented", "verified", "needs_repair", "skipped"}, "description": "verified needs evidence; skipped needs reason."},
									"acceptance": map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string", "maxLength": 240}, "description": "Concrete phase checks."},
									"evidence":   map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string", "maxLength": 240}, "description": "Tool-backed evidence."},
									"notes":      map[string]any{"type": "string", "maxLength": 240, "description": "Caveat/blocker/skip reason."},
								},
								"required": []string{"name", "status"},
							},
						},
						"notes": map[string]any{"type": "string", "maxLength": 240, "description": "Optional whole-plan caveat."},
					},
					"required": []string{"artifact_type", "requires_runtime_verification", "requires_visual_verification", "phases"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "final_response",
				Description: "Finish the session. summary must be the complete user-facing answer, including findings, caveats, changed files, verification outcome, and next steps.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"summary":       map[string]any{"type": "string", "description": "Complete user-facing answer."},
						"outcome":       map[string]any{"type": "string", "enum": []string{"completed", "blocked", "failed", "partial"}, "description": "completed only when work is complete and evidence did not fail."},
						"files_changed": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Workspace files changed, or empty."},
						"verification":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Checks/evidence, or not-run reason."},
					},
					"required": []string{"summary", "outcome", "files_changed", "verification"},
				},
			},
		},
	)
	if !opts.ManagedProcessesEnabled {
		filtered := defs[:0]
		for _, def := range defs {
			switch def.Function.Name {
			case "start_process", "list_processes", "stop_process":
				continue
			default:
				filtered = append(filtered, def)
			}
		}
		defs = filtered
	}
	if opts.ReadOnly {
		filtered := defs[:0]
		for _, def := range defs {
			switch def.Function.Name {
			case "read_file", "file_outline", "module_outline", "repo_overview", "list_files", "search", "scout_files", "final_response":
				filtered = append(filtered, def)
			}
		}
		defs = filtered
	}
	return defs
}

func browserToolDefinitions() []ToolDefinition {
	descSuffix := " Use this instead of terminal Playwright commands or MCP wrappers."
	return []ToolDefinition{
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "browser_navigate",
				Description: "Navigate the managed browser to a URL and return page state." + descSuffix,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"url": map[string]any{"type": "string", "description": "Absolute URL to open."},
					},
					"required": []string{"url"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "browser_snapshot",
				Description: "Capture an accessibility-style page snapshot with element refs." + descSuffix,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"max_chars": map[string]any{"type": "integer", "minimum": 1, "maximum": 50000, "description": "Optional max snapshot chars."},
					},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "browser_click",
				Description: "Click an element by latest browser_snapshot ref." + descSuffix,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"ref": map[string]any{"type": "string", "description": "Element ref."},
					},
					"required": []string{"ref"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "browser_fill",
				Description: "Fill an input element by latest browser_snapshot ref." + descSuffix,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"ref":   map[string]any{"type": "string", "description": "Input ref."},
						"value": map[string]any{"type": "string", "description": "Text to enter."},
					},
					"required": []string{"ref", "value"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "browser_press",
				Description: "Press a key in the managed browser page." + descSuffix,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"key": map[string]any{"type": "string", "description": "Key name, e.g. Enter, Escape, Tab, ArrowDown."},
					},
					"required": []string{"key"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "browser_file_upload",
				Description: "Upload local files into the open managed-browser file chooser." + descSuffix,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"paths": map[string]any{
							"type":        "array",
							"description": "Absolute local file paths.",
							"items":       map[string]any{"type": "string"},
							"minItems":    1,
						},
					},
					"required": []string{"paths"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "browser_screenshot",
				Description: "Save a managed-browser screenshot artifact and return its path." + descSuffix,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "Optional artifact path/filename."},
					},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "browser_current_page",
				Description: "Return current managed-browser URL, title, and freshness state." + descSuffix,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties":           map[string]any{},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "browser_wait_for_user",
				Description: "Pause for login, MFA, CAPTCHA, payment, or human judgment; resumes after the user replies." + descSuffix,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"message": map[string]any{"type": "string", "description": "Short user-facing instruction."},
						"url":     map[string]any{"type": "string", "description": "Optional current browser URL if known."},
					},
					"required": []string{"message"},
				},
			},
		},
	}
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
		o.DefaultSearchMaxMatch = 25
	}
	if o.MaxSearchMaxMatch <= 0 {
		o.MaxSearchMaxMatch = 100
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
