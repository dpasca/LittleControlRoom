package tools

import "time"

type ToolResult struct {
	Success      bool          `json:"success"`
	Output       string        `json:"output,omitempty"`
	Error        string        `json:"error,omitempty"`
	ExitCode     int           `json:"exit_code,omitempty"`
	Duration     time.Duration `json:"duration,omitempty"`
	TimedOut     bool          `json:"timed_out,omitempty"`
	Truncated    bool          `json:"truncated,omitempty"`
	Binary       bool          `json:"binary,omitempty"`
	ArtifactPath string        `json:"artifact_path,omitempty"`
	FilesTouched []string      `json:"files_touched,omitempty"`
}

type PlanItem struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}
