package tools

import "time"

type ToolResult struct {
	Success      bool          `json:"success"`
	Output       string        `json:"output,omitempty"`
	Error        string        `json:"error,omitempty"`
	Denied       bool          `json:"denied,omitempty"`
	DenialReason string        `json:"denial_reason,omitempty"`
	ExitCode     int           `json:"exit_code,omitempty"`
	Duration     time.Duration `json:"duration,omitempty"`
	TimedOut     bool          `json:"timed_out,omitempty"`
	Truncated    bool          `json:"truncated,omitempty"`
	Binary       bool          `json:"binary,omitempty"`
	ArtifactPath string        `json:"artifact_path,omitempty"`
	FilesTouched []string      `json:"files_touched,omitempty"`
	DiffSummary  string        `json:"diff_summary,omitempty"`
	PatchSummary *PatchSummary `json:"patch_summary,omitempty"`
}

type WebSearchBackend string

const (
	WebSearchBackendOff     WebSearchBackend = "off"
	WebSearchBackendExa     WebSearchBackend = "exa"
	WebSearchBackendGoogle  WebSearchBackend = "google"
	WebSearchBackendSearXNG WebSearchBackend = "searxng"
)

type PlanItem struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

type PatchSummary struct {
	Files             []FilePatchSummary `json:"files"`
	TotalAddedLines   int                `json:"total_added_lines"`
	TotalDeletedLines int                `json:"total_deleted_lines"`
}

type FilePatchSummary struct {
	Path         string `json:"path"`
	Operation    string `json:"operation"`
	AddedLines   int    `json:"added_lines"`
	DeletedLines int    `json:"deleted_lines"`
}
