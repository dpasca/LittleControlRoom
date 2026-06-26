package tools

import "time"

type ToolResult struct {
	Success          bool                     `json:"success"`
	Output           string                   `json:"output,omitempty"`
	Error            string                   `json:"error,omitempty"`
	Denied           bool                     `json:"denied,omitempty"`
	DenialReason     string                   `json:"denial_reason,omitempty"`
	Command          string                   `json:"command,omitempty"`
	Argv             []string                 `json:"argv,omitempty"`
	CWD              string                   `json:"cwd,omitempty"`
	Purpose          string                   `json:"purpose,omitempty"`
	AdminScope       string                   `json:"admin_scope,omitempty"`
	SystemMutation   bool                     `json:"system_mutation,omitempty"`
	AllowedExitCodes []int                    `json:"allowed_exit_codes,omitempty"`
	ExitCode         int                      `json:"exit_code,omitempty"`
	Duration         time.Duration            `json:"duration,omitempty"`
	TimedOut         bool                     `json:"timed_out,omitempty"`
	Truncated        bool                     `json:"truncated,omitempty"`
	Binary           bool                     `json:"binary,omitempty"`
	ArtifactPath     string                   `json:"artifact_path,omitempty"`
	FilesTouched     []string                 `json:"files_touched,omitempty"`
	DiffSummary      string                   `json:"diff_summary,omitempty"`
	PatchSummary     *PatchSummary            `json:"patch_summary,omitempty"`
	PatchFailure     *PatchFailure            `json:"patch_failure,omitempty"`
	ManagedProcess   *ManagedProcessEvidence  `json:"managed_process,omitempty"`
	ManagedProcesses []ManagedProcessEvidence `json:"managed_processes,omitempty"`
}

type ManagedProcessEvidence struct {
	Action        string   `json:"action,omitempty"`
	ProjectPath   string   `json:"project_path,omitempty"`
	ProcessID     string   `json:"process_id,omitempty"`
	Name          string   `json:"name,omitempty"`
	Command       string   `json:"command,omitempty"`
	CWD           string   `json:"cwd,omitempty"`
	PID           int      `json:"pid,omitempty"`
	PGID          int      `json:"pgid,omitempty"`
	Running       bool     `json:"running"`
	ExitCode      int      `json:"exit_code,omitempty"`
	ExitCodeKnown bool     `json:"exit_code_known,omitempty"`
	Ports         []int    `json:"ports,omitempty"`
	URLs          []string `json:"urls,omitempty"`
	RecentOutput  []string `json:"recent_output,omitempty"`
	Error         string   `json:"error,omitempty"`
}

type WebSearchBackend string

const (
	WebSearchBackendOff     WebSearchBackend = "off"
	WebSearchBackendExa     WebSearchBackend = "exa"
	WebSearchBackendGoogle  WebSearchBackend = "google"
	WebSearchBackendSearXNG WebSearchBackend = "searxng"
	WebSearchBackendBrowser WebSearchBackend = "browser"
)

type PlanItem struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

const (
	CommandPurposeInspect = "inspect"
	CommandPurposeVerify  = "verify"

	VerificationStatusPassed   = "passed"
	VerificationStatusFailed   = "failed"
	VerificationStatusTimedOut = "timed_out"
	VerificationStatusDenied   = "denied"
)

type VerificationCheck struct {
	Command          string        `json:"command,omitempty"`
	Argv             []string      `json:"argv,omitempty"`
	CWD              string        `json:"cwd,omitempty"`
	Purpose          string        `json:"purpose,omitempty"`
	AllowedExitCodes []int         `json:"allowed_exit_codes,omitempty"`
	Status           string        `json:"status"`
	Success          bool          `json:"success"`
	ExitCode         int           `json:"exit_code,omitempty"`
	Duration         time.Duration `json:"duration,omitempty"`
	TimedOut         bool          `json:"timed_out,omitempty"`
	Denied           bool          `json:"denied,omitempty"`
	Error            string        `json:"error,omitempty"`
}

func VerificationCheckFromResult(result ToolResult) VerificationCheck {
	status := VerificationStatusFailed
	switch {
	case result.Denied:
		status = VerificationStatusDenied
	case result.TimedOut:
		status = VerificationStatusTimedOut
	case result.Success:
		status = VerificationStatusPassed
	}
	return VerificationCheck{
		Command:          result.Command,
		Argv:             append([]string(nil), result.Argv...),
		CWD:              result.CWD,
		Purpose:          result.Purpose,
		AllowedExitCodes: append([]int(nil), result.AllowedExitCodes...),
		Status:           status,
		Success:          result.Success,
		ExitCode:         result.ExitCode,
		Duration:         result.Duration,
		TimedOut:         result.TimedOut,
		Denied:           result.Denied,
		Error:            result.Error,
	}
}

type PatchSummary struct {
	Files             []FilePatchSummary `json:"files"`
	TotalAddedLines   int                `json:"total_added_lines"`
	TotalDeletedLines int                `json:"total_deleted_lines"`
}

type PatchFailure struct {
	Stage          string           `json:"stage"`
	Path           string           `json:"path,omitempty"`
	Message        string           `json:"message"`
	Hint           string           `json:"hint,omitempty"`
	SuggestedReads []ReadSuggestion `json:"suggested_reads,omitempty"`
}

type ReadSuggestion struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
	Reason string `json:"reason,omitempty"`
}

type FilePatchSummary struct {
	Path         string `json:"path"`
	Operation    string `json:"operation"`
	AddedLines   int    `json:"added_lines"`
	DeletedLines int    `json:"deleted_lines"`
}
