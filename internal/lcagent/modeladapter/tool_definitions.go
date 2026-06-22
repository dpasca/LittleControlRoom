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
	writePathDescription := "Workspace-relative path to an existing text file, or an absolute path that resolves inside the workspace. Absolute paths outside the workspace require --admin-write."
	patchPathDescription := "Patch file paths may be workspace-relative, or absolute paths that resolve inside the workspace. Absolute paths outside the workspace require --admin-write."
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
				Description: "List files under a workspace path, optionally filtered by a simple glob. Glob matching is case-sensitive by default; set case_sensitive=false when a user-provided filename may differ in case. Hidden/generated directories are listed as placeholders but not descended into by default; set include_hidden=true only when those contents are directly relevant.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":           map[string]any{"type": "string", "description": "Workspace-relative directory or file to list, or absolute path for read-only inspection. Defaults to workspace root."},
						"glob":           map[string]any{"type": "string", "description": "Optional filepath glob matched against relative path or basename. Supports ** as a recursive path segment."},
						"max_entries":    map[string]any{"type": "integer", "minimum": 1, "maximum": opts.MaxListEntryLimit},
						"case_sensitive": map[string]any{"type": "boolean", "description": "Whether glob matching is case-sensitive. Defaults to true; set false for forgiving filename lookup."},
						"include_hidden": map[string]any{"type": "boolean", "description": "Descend into normally hidden/generated directories such as .git, .venv, node_modules, vendor, dist, or build. Defaults to false."},
					},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "search",
				Description: "Search text files in the workspace with case-insensitive literal substring matching, optionally returning a small context window around each match. The query is not a regex, glob, or alternation pattern. For broad or common terms, set output_mode=compact and provide intent so oversized results can be condensed around the task. Broad home-directory searches require a narrower path or file_glob.",
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
						"output_mode":    map[string]any{"type": "string", "enum": []string{"full", "compact"}, "description": "Use compact for broad searches: match lines only, no per-match context. Defaults to full."},
						"intent":         map[string]any{"type": "string", "description": "Natural-language purpose of the search, for example 'find checkout/payment code paths relevant to performance review'. Useful when compact or oversized results are refined by a utility model."},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "scout_files",
				Description: "Ask the configured utility/scout model to inspect a bounded pack of matching files and rank likely relevant files, line ranges, and next reads. Use this when a symbol name is unknown, broad literal search would be noisy, or a directory/glob is probably relevant but you need help routing attention. This is read-only and advisory; read_file must still verify evidence before final claims.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"question":           map[string]any{"type": "string", "description": "Natural-language question for the scout, for example 'Where is Enter handled for embedded LCAgent sessions?'"},
						"path":               map[string]any{"type": "string", "description": "Workspace-relative directory or file to pack for scouting. Defaults to workspace root."},
						"file_glob":          map[string]any{"type": "string", "description": "Optional filepath glob matched against relative path or basename, for example *.go or internal/tui/*.go."},
						"max_files":          map[string]any{"type": "integer", "minimum": 1, "maximum": 40, "description": "Maximum files to include in the scout pack. Defaults to 12."},
						"max_lines_per_file": map[string]any{"type": "integer", "minimum": 20, "maximum": opts.MaxReadLineLimit, "description": "Maximum leading lines to include for each file. Defaults to 120."},
						"include_hidden":     map[string]any{"type": "boolean", "description": "Descend into normally hidden/generated directories. Defaults to false; only enable when directly relevant."},
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
				Name:        "analyze_image",
				Description: "Ask the configured vision model to inspect image or screenshot pixels and return a structured pass/fail/uncertain visual verdict. Use this when pixel-level evidence would materially improve verification or the answer, including direct visual QA, image contents, or side-by-side comparison with comparison_path. A fail or uncertain verdict is not completion evidence.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":            map[string]any{"type": "string", "description": "Workspace-relative image path, or absolute path for read-only inspection when produced by browser_screenshot or another tool."},
						"comparison_path": map[string]any{"type": "string", "description": "Optional second image path to compare against path. Use this for temporal/stateful visual verification with two observations separated in time; the vision model receives a side-by-side comparison. One paired comparison is usually enough unless a material visual defect was fixed."},
						"question":        map[string]any{"type": "string", "maxLength": 1200, "description": "Focused question for the vision model, for example 'Does this screenshot show the expected surface, background, and player correctly?'"},
						"context":         map[string]any{"type": "string", "maxLength": 4000, "description": "Optional task context, expected visual state, or specific UI/game details to inspect."},
						"checks":          map[string]any{"type": "array", "maxItems": 10, "items": map[string]any{"type": "string", "maxLength": 100}, "description": "Optional visual checks, for example wrong window, missing surfaces, floating/clipped objects, bad layering, overlapping text, unstable state, or color contrast."},
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
	if opts.BrowserAvailable {
		defs = append(defs, browserToolDefinitions()...)
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
				Description: "Run a bounded command in the workspace. Prefer argv. Use shell command strings only when shell behavior is genuinely needed. Do not use this to edit files through redirects, heredocs, tee, in-place rewrites, or mutating file commands; use create_file, replace_file, apply_patch, replace_lines, or replace_text. Do not use bounded run_command for deploy/publish/promote/upload/release operations that may exceed the timeout when managed process support is available.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"argv":               map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Command argv, for example [\"go\",\"test\",\"./...\"]"},
						"command":            map[string]any{"type": "string", "description": "Legacy shell command string. Prefer argv unless shell syntax is required."},
						"cwd":                map[string]any{"type": "string", "description": "Optional working directory for the command. Prefer workspace-relative subdirectories such as \"frontend\". Absolute or parent-directory cwd values outside the workspace require approval."},
						"shell":              map[string]any{"type": "boolean", "description": "Set true when using command as a shell string."},
						"timeout_ms":         map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
						"purpose":            map[string]any{"type": "string", "enum": []string{"inspect", "verify"}, "description": "Use verify for tests, linters, typechecks, builds, or other commands intended to verify the final answer. Use inspect for read-only exploration."},
						"admin_scope":        map[string]any{"type": "string", "enum": []string{"system"}, "description": "Set to system only for persistent user/system configuration mutations explicitly requested by the user. System-scope commands also require LCAgent admin write to be enabled."},
						"allowed_exit_codes": map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 0, "maximum": 255}, "description": "Optional explicit successful exit codes. Use only for probes where a nonzero exit is expected evidence, for example [0,1] for command/path existence checks. Do not use for tests or verification unless the nonzero exit is intentionally acceptable."},
					},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "start_process",
				Description: "Start a long-running managed background process through Little Control Room, for dev servers, watchers, and long deploy/publish/promote/upload/release operations that should remain inspectable after the tool returns. Use this instead of run_command for processes expected to keep running or exceed bounded command timeouts. By default, if the same command is already running in the same cwd, the existing process is reused instead of launching a duplicate.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"command":          map[string]any{"type": "string", "description": "Shell command to run as the managed process, for example \"pnpm dev\". Keep it to the foreground server/watch command; LCR owns backgrounding and stopping."},
						"cwd":              map[string]any{"type": "string", "description": "Optional workspace-relative working directory for the process, for example \"frontend\". Absolute or parent-directory cwd values outside the workspace are rejected."},
						"name":             map[string]any{"type": "string", "description": "Optional short label for this process, such as \"frontend\" or \"emulators\"."},
						"create_new":       map[string]any{"type": "boolean", "description": "Set true only when the user needs another concurrent copy of the same command in the same cwd. Leave false for ordinary launch/relaunch and screenshot/verification workflows."},
						"replace_existing": map[string]any{"type": "boolean", "description": "Set true to stop running managed processes with the same command and cwd before starting a fresh one. Do not combine with create_new."},
					},
					"required": []string{"command"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "list_processes",
				Description: "List managed background processes for this workspace, including running state, process id, PID, ports, URLs, and recent output. Use this to verify whether a dev server is actually still running.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties":           map[string]any{},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "stop_process",
				Description: "Stop a managed background process through Little Control Room. This only affects LCR-owned managed runtimes. Use process_id from list_processes when more than one process is active.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"process_id": map[string]any{"type": "string", "description": "Optional managed process id from list_processes. If omitted, LCR stops the selected/default process for this workspace."},
					},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "create_file",
				Description: "Create one brand-new text file with exact content, without patch syntax. Use this for initial writes or generated files when the target must not already exist. Fails if the file exists. " + writePathDescription + " Successful writes return a diff summary and the new sha256.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":    map[string]any{"type": "string", "description": writePathDescription},
						"content": map[string]any{"type": "string", "description": "Complete text content to write exactly as provided."},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "replace_file",
				Description: "Replace one existing text file with complete new content, without patch syntax. Use this when a whole-file rewrite is simpler than a patch. Requires expected_sha256 copied from the latest read_file output for that file; LCAgent calculates and verifies the current file hash before writing. Do not calculate it yourself. " + writePathDescription + " Successful writes return a diff summary and the new sha256.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":            map[string]any{"type": "string", "description": writePathDescription},
						"content":         map[string]any{"type": "string", "description": "Complete replacement text content to write exactly as provided."},
						"expected_sha256": map[string]any{"type": "string", "description": "The exact sha256 value copied from the latest read_file result for this file. Do not calculate it yourself."},
					},
					"required": []string{"path", "content", "expected_sha256"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "apply_patch",
				Description: "Apply a Codex apply_patch patch. The patch must use the exact envelope: *** Begin Patch, then *** Update File: path or *** Add File: path, hunks with @@ and +/- lines, then *** End Patch. " + patchPathDescription + " Successful patches return a diff summary; failed stale hunks may return suggested read_file ranges to refresh before retrying. Example: *** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** End Patch",
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
						"path":                  map[string]any{"type": "string", "description": writePathDescription},
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
				Name:        "replace_lines",
				Description: "Replace or delete an inclusive 1-based line range in one existing workspace text file. Use this after read_file gives exact current line numbers, especially for deleting stale blocks or replacing larger sections where replace_text would be brittle. Optional first/last line guards should be copied from the current file when available.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":                map[string]any{"type": "string", "description": writePathDescription},
						"start_line":          map[string]any{"type": "integer", "minimum": 1, "description": "First line to replace, inclusive, using read_file line numbers."},
						"end_line":            map[string]any{"type": "integer", "minimum": 1, "description": "Last line to replace, inclusive, using read_file line numbers. Use an empty new_text to delete the range."},
						"new_text":            map[string]any{"type": "string", "description": "Replacement text. Empty string deletes the selected range."},
						"expected_first_line": map[string]any{"type": "string", "description": "Optional guard: exact current contents of start_line, without the line number prefix."},
						"expected_last_line":  map[string]any{"type": "string", "description": "Optional guard: exact current contents of end_line, without the line number prefix."},
					},
					"required": []string{"path", "start_line", "end_line", "new_text"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "update_plan",
				Description: `Publish the current short plan with statuses pending, in_progress, or completed. Use {"items":[{"step":"Inspect files","status":"completed"},{"step":"Patch code","status":"in_progress"}]}. This is progress tracking for execution work; after updating a plan, continue with the in_progress step unless tools, autonomy, or a concrete blocker prevent it.`,
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
				Description: "Publish or refresh a phased quality plan for nontrivial artifact work. Use this before or during implementation to make expected phases, acceptance checks, and evidence explicit. Phases are sequential execution gates: keep at most one active phase, leave later phases planned, and advance at most one phase after concrete evidence. Implement the current phase's realistic slice before later-phase systems, even when the final deliverable is a single file. Do not mark visual, interactive, or user-facing phases verified merely because code for them exists; evidence must show the requested behavior or visible result actually works or appears. A completed final_response is audited against this plan: phases must be verified or skipped with evidence, runtime verification must have a passing purpose=verify check when required, visual verification must have a passing analyze_image verdict when required, and temporal visual verification must have a passing analyze_image verdict with comparison_path when required. Visual evidence is a bounded gate, not a loop; one focused visual check, or one paired temporal check for the phase that needs it, is normally enough.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"artifact_type":                         map[string]any{"type": "string", "enum": []string{"game", "ui", "cli", "library", "doc", "other", "unknown"}, "description": "Primary artifact being produced or changed."},
						"requires_runtime_verification":         map[string]any{"type": "boolean", "description": "True when the artifact should be built, run, tested, or otherwise exercised before completed."},
						"requires_visual_verification":          map[string]any{"type": "boolean", "description": "True when screenshot/image appearance matters enough that completed requires analyze_image evidence."},
						"requires_temporal_visual_verification": map[string]any{"type": "boolean", "description": "True only when completion depends on visible change over time and requires analyze_image evidence comparing two observations via comparison_path. Keep false when temporal checks are only phase-local acceptance details."},
						"phases": map[string]any{
							"type":        "array",
							"minItems":    1,
							"maxItems":    12,
							"description": "Small implementation/refinement phases. Keep a completed prefix, at most one active phase, and a planned tail; refresh statuses as each phase gets evidence.",
							"items": map[string]any{
								"type":                 "object",
								"additionalProperties": false,
								"properties": map[string]any{
									"name":       map[string]any{"type": "string", "maxLength": 240, "description": "Short phase name, such as core movement or HUD."},
									"status":     map[string]any{"type": "string", "enum": []string{"planned", "in_progress", "implemented", "verified", "needs_repair", "skipped"}, "description": "Use verified only when evidence shows the phase is done. Use skipped only with a reason in notes or evidence."},
									"acceptance": map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string", "maxLength": 240}, "description": "Concrete checks for this phase."},
									"evidence":   map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string", "maxLength": 240}, "description": "Tool-backed evidence for this phase, such as compile command, test, screenshot path, or vision analysis."},
									"notes":      map[string]any{"type": "string", "maxLength": 240, "description": "Short caveat, blocker, or skip reason."},
								},
								"required": []string{"name", "status"},
							},
						},
						"notes": map[string]any{"type": "string", "maxLength": 240, "description": "Optional concise whole-plan caveat."},
					},
					"required": []string{"artifact_type", "requires_runtime_verification", "requires_visual_verification", "phases"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "final_response",
				Description: "Finish the session. The summary must be the complete user-facing answer, including findings, caveats, changed files, verification outcome, and next steps; do not put essential answer content only in verification. For operational tasks, separate confirmed facts, attempted actions, failures/timeouts, inferences, and blockers when they differ.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"summary":       map[string]any{"type": "string", "description": "Complete user-facing answer. Include the actual findings or outcome here, not only a preamble."},
						"outcome":       map[string]any{"type": "string", "enum": []string{"completed", "blocked", "failed", "partial"}, "description": "Structured final state. Use completed only when requested work is actually complete and verification/evidence did not fail; use blocked for unresolved external/process blockers, failed when attempted work failed, and partial when some requested work remains. When an active quality plan exists, completed is audited against verified/skipped phases; partial is allowed as an honest handoff when work remains."},
						"files_changed": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Workspace files changed by this session, or an empty array."},
						"verification":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Checks run, evidence reviewed, or an explicit not-run reason. This supports the summary; it is not a substitute for the answer."},
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
	return defs
}

func browserToolDefinitions() []ToolDefinition {
	descSuffix := " Use this LCAgent browser tool instead of terminal Playwright commands, npx, playwright-mcp, or MCP server commands."
	return []ToolDefinition{
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "browser_navigate",
				Description: "Navigate the managed browser page to a URL and return current page state." + descSuffix,
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
				Description: "Capture an accessibility-style snapshot of the current managed browser page with stable element refs." + descSuffix,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"max_chars": map[string]any{"type": "integer", "minimum": 1, "maximum": 50000, "description": "Optional maximum snapshot characters."},
					},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "browser_click",
				Description: "Click an element by ref from the latest browser_snapshot. If refs are stale, take a fresh snapshot." + descSuffix,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"ref": map[string]any{"type": "string", "description": "Element ref from the latest browser_snapshot."},
					},
					"required": []string{"ref"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "browser_fill",
				Description: "Fill an input element by ref from the latest browser_snapshot. If refs are stale, take a fresh snapshot." + descSuffix,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"ref":   map[string]any{"type": "string", "description": "Input element ref from the latest browser_snapshot."},
						"value": map[string]any{"type": "string", "description": "Text value to enter."},
					},
					"required": []string{"ref", "value"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "browser_press",
				Description: "Press a keyboard key in the managed browser page." + descSuffix,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"key": map[string]any{"type": "string", "description": "Key name, for example Enter, Escape, Tab, or ArrowDown."},
					},
					"required": []string{"key"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "browser_screenshot",
				Description: "Save a screenshot artifact from the managed browser page and return the artifact path." + descSuffix,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "Optional artifact path or filename. Defaults to the managed browser session output directory."},
					},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "browser_current_page",
				Description: "Return the current managed browser page URL, title, and freshness state." + descSuffix,
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
				Description: "Pause browser automation when login, MFA, CAPTCHA, payment, or human judgment is needed. This keeps the managed browser open, asks the user to finish the step in Little Control Room, and resumes after the user replies." + descSuffix,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"message": map[string]any{"type": "string", "description": "Short user-facing instruction, for example: Finish login in the managed browser, then tell me to continue."},
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
