package script

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"lcroom/internal/lcagent/policy"
	"lcroom/internal/lcagent/session"
	skillcatalog "lcroom/internal/lcagent/skills"
	"lcroom/internal/lcagent/tools"
	lcrmodel "lcroom/internal/model"
)

const (
	DefaultSearchRefineMinBytes      = 12000
	maxCriticConsultQuestionChars    = 1200
	maxCriticConsultContextChars     = 6000
	maxCriticConsultCandidateChars   = 18000
	maxCriticConsultFiles            = 8
	maxCriticConsultLinesPerFile     = 220
	defaultCriticConsultLinesPerFile = 160
	maxCriticConsultFileChars        = 7000
	maxCriticConsultTotalFileChars   = 24000
	maxImageAnalysisQuestionChars    = 1200
	maxImageAnalysisContextChars     = 4000
	maxImageAnalysisChecks           = 10
	maxQualityPlanPhases             = 12
	maxQualityPlanAcceptanceItems    = 8
	maxQualityPlanEvidenceItems      = 8
	maxQualityPlanTextChars          = 240
	maxPhaseWriteGateArgsChars       = 30000
	maxChangeReviewFiles             = 8
	maxChangeReviewLinesPerFile      = 260
	maxChangeReviewTotalChars        = 36000
)

type Runner struct {
	Session                      *session.Writer
	Command                      tools.CommandRunner
	Patch                        tools.PatchApplier
	Files                        tools.FileTools
	WebSearch                    tools.WebSearchRunner
	WebSearchOn                  bool
	BrowserAvailable             bool
	Browser                      BrowserRunner
	SearchRefiner                SearchRefiner
	CodeScout                    CodeScout
	CriticConsultant             CriticConsultant
	ImageAnalyzer                ImageAnalyzer
	PhaseWriteGate               PhaseWriteGate
	SearchRefineMinBytes         int
	Approvals                    ApprovalBroker
	Processes                    ProcessBroker
	Skills                       skillcatalog.Catalog
	SessionID                    string
	Prompt                       string
	ArtifactsDir                 string
	SteerMessages                <-chan string
	QualityPlanRequired          bool
	QualityPlanRequirementReason string
	QualityPlanRequirementScope  string

	verificationChecks     []tools.VerificationCheck
	operationalActions     []OperationalAction
	filesTouched           []string
	fileTouchEvents        int
	commandApprovalGrants  []commandApprovalGrant
	toolFailures           []tools.ToolResult
	browserToolsUsed       bool
	browserWaitForUserUsed bool
	imageAnalyses          int
	temporalImageAnalyses  int
	inspectionEvidence     int
	qualityPlan            *QualityPlan
	qualityPlanUpdates     int
	phaseWriteGateAdmitted string
	editSummaries          []tools.PatchSummary
}

type SearchRefiner interface {
	RefineSearch(context.Context, SearchRefineRequest) (SearchRefineResult, error)
}

type CodeScout interface {
	ScoutFiles(context.Context, ScoutFilesRequest) (ScoutFilesResult, error)
}

type CriticConsultant interface {
	ConsultCritic(context.Context, CriticConsultRequest) (CriticConsultResult, error)
}

type ImageAnalyzer interface {
	AnalyzeImage(context.Context, ImageAnalysisRequest) (ImageAnalysisResult, error)
}

type PhaseWriteGate interface {
	EvaluatePhaseWrite(context.Context, PhaseWriteGateRequest) (PhaseWriteGateResult, error)
}

type BrowserRunner interface {
	RunBrowserTool(context.Context, string, json.RawMessage) tools.ToolResult
}

type SearchRefineRequest struct {
	Query               string
	Intent              string
	Path                string
	FileGlob            string
	MaxMatches          int
	SearchOutput        string
	OriginalOutputBytes int
	CompactOutputBytes  int
	Truncated           bool
}

type ScoutFilesRequest struct {
	Question        string
	Path            string
	FileGlob        string
	MaxFiles        int
	MaxLinesPerFile int
	FilePack        string
	PackBytes       int
	Truncated       bool
}

type ScoutFilesResult struct {
	Output       string
	Provider     string
	Model        string
	Usage        json.RawMessage
	UsageSummary lcrmodel.LLMUsage
}

type SearchRefineResult struct {
	Output       string
	Provider     string
	Model        string
	Usage        json.RawMessage
	UsageSummary lcrmodel.LLMUsage
}

type CriticConsultRequest struct {
	SessionID   string
	UserRequest string
	Kind        string
	Question    string
	Context     string
	Candidate   string
	Checks      []string
	Files       []CriticConsultFile
}

type CriticConsultFile struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Role      string `json:"role,omitempty"`
	Excerpt   string `json:"excerpt,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type CriticConsultResult struct {
	Status              string
	Summary             string
	Findings            []CriticConsultFinding
	LeadInstruction     string
	HumanPrompt         string
	ProposedUserMessage string
	Provider            string
	Model               string
	PacketHash          string
	Usage               json.RawMessage
	UsageSummary        lcrmodel.LLMUsage
}

type ImageAnalysisRequest struct {
	SessionID      string
	UserRequest    string
	Path           string
	ComparisonPath string
	Question       string
	Context        string
	Checks         []string
}

type ImageAnalysisResult struct {
	Output       string
	Provider     string
	Model        string
	Usage        json.RawMessage
	UsageSummary lcrmodel.LLMUsage
}

type PhaseWriteGateRequest struct {
	SessionID           string
	UserRequest         string
	Tool                string
	ToolArgs            string
	ToolArgsTruncated   bool
	QualityPlan         QualityPlan
	ActivePhaseIndex    int
	ActivePhase         QualityPlanPhase
	CompletedPhaseNames []string
	RemainingPhaseNames []string
}

type PhaseWriteGateResult struct {
	Allow                  bool
	FitsActivePhase        bool
	ContainsLaterPhaseWork bool
	TooMuchAtOnce          bool
	Reason                 string
	SuggestedSmallerSlice  string
	Provider               string
	Model                  string
	Usage                  json.RawMessage
	UsageSummary           lcrmodel.LLMUsage
}

type CriticConsultFinding struct {
	Severity          string `json:"severity"`
	Materiality       string `json:"materiality,omitempty"`
	Claim             string `json:"claim"`
	EvidenceSource    string `json:"evidence_source"`
	Evidence          string `json:"evidence"`
	UserImpact        string `json:"user_impact,omitempty"`
	SuggestedFollowup string `json:"suggested_followup"`
}

type VerificationFeedback struct {
	Status  string `json:"status"`
	Command string `json:"command,omitempty"`
	Message string `json:"message"`
}

type FinalResponseAudit struct {
	Outcome            string `json:"outcome"`
	FinalOutcome       string `json:"final_outcome,omitempty"`
	VerificationStatus string `json:"verification_status,omitempty"`
	Code               string `json:"code,omitempty"`
	Message            string `json:"message"`
	Blocking           bool   `json:"blocking,omitempty"`
	ToolFailures       int    `json:"tool_failures,omitempty"`
	OperationalActions int    `json:"operational_actions,omitempty"`
}

type QualityPlan struct {
	ArtifactType                       string             `json:"artifact_type"`
	RequiresRuntimeVerification        bool               `json:"requires_runtime_verification"`
	RequiresVisualVerification         bool               `json:"requires_visual_verification"`
	RequiresTemporalVisualVerification bool               `json:"requires_temporal_visual_verification,omitempty"`
	Phases                             []QualityPlanPhase `json:"phases"`
	Notes                              string             `json:"notes,omitempty"`
}

type QualityPlanPhase struct {
	Name       string   `json:"name"`
	Status     string   `json:"status"`
	Acceptance []string `json:"acceptance,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
	Notes      string   `json:"notes,omitempty"`
}

type ChangeReviewEvidence struct {
	FileTouchEvents int                       `json:"file_touch_events"`
	FilesTouched    []string                  `json:"files_touched,omitempty"`
	PatchSummaries  []tools.PatchSummary      `json:"patch_summaries,omitempty"`
	Files           []ChangeReviewFile        `json:"files,omitempty"`
	Verification    []tools.VerificationCheck `json:"verification,omitempty"`
	QualityPlan     *QualityPlan              `json:"quality_plan,omitempty"`
	Truncated       bool                      `json:"truncated,omitempty"`
}

type ChangeReviewFile struct {
	Path              string `json:"path"`
	Exists            bool   `json:"exists"`
	Snapshot          string `json:"snapshot,omitempty"`
	SnapshotTruncated bool   `json:"snapshot_truncated,omitempty"`
	Error             string `json:"error,omitempty"`
	Binary            bool   `json:"binary,omitempty"`
}

type OperationalAction struct {
	Action                   string `json:"action"`
	ProcessID                string `json:"process_id,omitempty"`
	Name                     string `json:"name,omitempty"`
	Command                  string `json:"command,omitempty"`
	CWD                      string `json:"cwd,omitempty"`
	Success                  bool   `json:"success"`
	Denied                   bool   `json:"denied,omitempty"`
	Error                    string `json:"error,omitempty"`
	VerificationChecksBefore int    `json:"verification_checks_before"`
}

type PatchFeedback struct {
	Stage          string                 `json:"stage"`
	Path           string                 `json:"path,omitempty"`
	Message        string                 `json:"message"`
	SuggestedReads []tools.ReadSuggestion `json:"suggested_reads,omitempty"`
}

type Action struct {
	Type         string          `json:"type"`
	Tool         string          `json:"tool,omitempty"`
	Args         json.RawMessage `json:"args,omitempty"`
	Summary      string          `json:"summary,omitempty"`
	Outcome      string          `json:"outcome,omitempty"`
	FilesChanged []string        `json:"files_changed,omitempty"`
	Verification []string        `json:"verification,omitempty"`
}

type replaceTextArgs struct {
	Path                 string `json:"path"`
	OldText              string `json:"old_text"`
	NewText              string `json:"new_text"`
	ExpectedReplacements int    `json:"expected_replacements,omitempty"`
}

type replaceLinesArgs struct {
	Path              string `json:"path"`
	StartLine         int    `json:"start_line"`
	EndLine           int    `json:"end_line"`
	NewText           string `json:"new_text"`
	ExpectedFirstLine string `json:"expected_first_line,omitempty"`
	ExpectedLastLine  string `json:"expected_last_line,omitempty"`
}

type createFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type replaceFileArgs struct {
	Path           string `json:"path"`
	Content        string `json:"content"`
	ExpectedSHA256 string `json:"expected_sha256"`
}

type consultCriticArgs struct {
	Kind      string                  `json:"kind"`
	Question  string                  `json:"question"`
	Context   string                  `json:"context,omitempty"`
	Candidate string                  `json:"candidate,omitempty"`
	Checks    []string                `json:"checks,omitempty"`
	Files     []consultCriticFileArgs `json:"files,omitempty"`
}

type analyzeImageArgs struct {
	Path           string   `json:"path"`
	ComparisonPath string   `json:"comparison_path,omitempty"`
	Question       string   `json:"question"`
	Context        string   `json:"context,omitempty"`
	Checks         []string `json:"checks,omitempty"`
}

type qualityPlanArgs QualityPlan

type consultCriticFileArgs struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Role      string `json:"role,omitempty"`
}

type finalResponseArgs struct {
	Summary      string   `json:"summary"`
	Outcome      string   `json:"outcome"`
	FilesChanged []string `json:"files_changed"`
	Verification []string `json:"verification"`
}

func VerificationFeedbackForResult(result tools.ToolResult) (VerificationFeedback, bool) {
	return verificationFeedbackForResult(result, "")
}

func (r *Runner) VerificationFeedbackForResult(result tools.ToolResult) (VerificationFeedback, bool) {
	hint := ""
	if r != nil {
		hint = r.packageScriptCWDHint(result)
	}
	return verificationFeedbackForResult(result, hint)
}

func verificationFeedbackForResult(result tools.ToolResult, hint string) (VerificationFeedback, bool) {
	if !strings.EqualFold(result.Purpose, tools.CommandPurposeVerify) || result.Success {
		return VerificationFeedback{}, false
	}
	check := tools.VerificationCheckFromResult(result)
	command := firstNonEmpty(verificationCheckDisplayLabel(check), "verification check")
	status := firstNonEmpty(check.Status, "failed")
	var next string
	switch status {
	case tools.VerificationStatusDenied:
		next = "Choose an approved argv-only verification command, or explain clearly why verification is blocked."
	case tools.VerificationStatusTimedOut:
		next = "Narrow the verification command, inspect the timeout, or explain clearly why verification is blocked."
	default:
		next = "Inspect the failure, fix the issue if it is caused by your changes, and rerun a purpose=verify check."
	}
	detail := firstNonEmpty(check.Error, result.Error)
	message := fmt.Sprintf("Verification feedback: %s %s. %s", command, verificationStatusPhrase(status), next)
	if detail != "" {
		message += " Error: " + detail
	}
	if hint = strings.TrimSpace(hint); hint != "" {
		message += " " + hint
	}
	return VerificationFeedback{Status: status, Command: command, Message: message}, true
}

func verificationStatusPhrase(status string) string {
	switch status {
	case tools.VerificationStatusDenied:
		return "was denied"
	case tools.VerificationStatusTimedOut:
		return "timed out"
	case tools.VerificationStatusPassed:
		return "passed"
	case tools.VerificationStatusFailed:
		return "failed"
	default:
		return "finished with status " + status
	}
}

func (r *Runner) VerificationFeedbackForFinal(action Action) (VerificationFeedback, bool) {
	audit := r.FinalResponseAudit(action)
	return audit.VerificationFeedback()
}

func (r *Runner) FinalResponseAudit(action Action) FinalResponseAudit {
	action.FilesChanged = cleanStringList(action.FilesChanged)
	action.Verification = cleanStringList(action.Verification)
	finalOutcome := normalizeFinalResponseOutcome(action.Outcome)
	var verificationChecks []tools.VerificationCheck
	var operationalActions []OperationalAction
	toolFailures := 0
	if r != nil {
		verificationChecks = r.verificationChecks
		operationalActions = append([]OperationalAction(nil), r.operationalActions...)
		toolFailures = len(r.toolFailures)
	}
	verificationStatus, verificationMessage := finalVerificationStatus(action.FilesChanged, action.Verification, verificationChecks)
	audit := FinalResponseAudit{
		Outcome:            "pass",
		FinalOutcome:       finalOutcome,
		VerificationStatus: verificationStatus,
		Message:            "Final response audit passed.",
		ToolFailures:       toolFailures,
		OperationalActions: len(operationalActions),
	}
	if len(action.FilesChanged) > 0 && len(verificationChecks) == 0 && (verificationStatus == "missing_after_changes" || verificationStatus == "reported_only") {
		audit.Outcome = "block"
		audit.Blocking = true
		audit.Message = finalAuditBlockingMessage(verificationStatus)
		return audit
	}
	if verificationStatus == "failed" {
		audit.Outcome = "warn"
		audit.Message = "Final response audit warning: verification evidence did not pass. The final response must present this as failed, timed out, denied, or blocked rather than completed. " + verificationMessage
		if finalOutcome == "completed" {
			audit.Outcome = "block"
			audit.Blocking = true
			audit.Message = "final_response outcome was completed, but verification evidence did not pass. Set outcome to failed, blocked, or partial and explain the failed, timed-out, or denied verification before calling final_response again. " + verificationMessage
		}
		return audit
	}
	if toolFailures > 0 {
		audit.Outcome = "warn"
		audit.Message = fmt.Sprintf("Final response audit warning: %d tool failure(s) occurred in this turn; the final response must not imply failed actions succeeded.", toolFailures)
	}
	if finalOutcome == "unknown" && r != nil && r.BrowserAvailable && r.browserToolsUsed && !r.browserWaitForUserUsed {
		audit.Outcome = "block"
		audit.Blocking = true
		audit.Code = "browser_wait_required"
		audit.Message = "final_response outcome was unknown after using the managed browser. If login, MFA, CAPTCHA, payment, or human judgment may unblock the browser task, call browser_wait_for_user instead of ending the turn. Otherwise set outcome to blocked, failed, partial, or completed and explain the concrete browser evidence."
		return audit
	}
	if finalOutcome == "completed" || finalOutcome == "partial" || finalOutcome == "unknown" {
		if blocking := r.qualityPlanTerminalBlock(finalOutcome, verificationChecks); blocking != nil {
			audit.Outcome = "block"
			audit.Blocking = true
			audit.Code = blocking.Code
			audit.Message = blocking.Message
			return audit
		}
	}
	if finalOutcome == "completed" {
		if op, ok := latestOperationalActionRequiringVerification(operationalActions); ok && len(verificationChecks) <= op.VerificationChecksBefore {
			audit.Outcome = "block"
			audit.Blocking = true
			audit.Message = fmt.Sprintf("final_response outcome was completed, but managed process action %q has no later run_command check marked purpose=verify. Run a separate verification probe after the operation, or set outcome to blocked, failed, or partial and explain why verification is blocked before calling final_response again.", op.Action)
		}
	}
	return audit
}

func (r *Runner) qualityPlanTerminalBlock(finalOutcome string, verificationChecks []tools.VerificationCheck) *FinalResponseAudit {
	finalOutcome = normalizeFinalResponseOutcome(finalOutcome)
	if finalOutcome == "completed" {
		return r.qualityPlanCompletionBlock(verificationChecks)
	}
	if finalOutcome != "partial" && finalOutcome != "unknown" {
		return nil
	}
	if r == nil {
		return nil
	}
	if r.QualityPlanRequired && r.qualityPlan == nil {
		scope := strings.TrimSpace(r.QualityPlanRequirementScope)
		if scope == "" {
			scope = "sizable"
		}
		reason := strings.TrimSpace(r.QualityPlanRequirementReason)
		if reason != "" {
			reason = " Preflight reason: " + reason
		}
		return &FinalResponseAudit{
			Code:    "quality_plan_partial_unplanned",
			Message: fmt.Sprintf("final_response outcome was %s, but planning preflight classified this as %s phased work and no quality plan has been recorded. Call update_quality_plan and start the first phase, or use outcome blocked/failed only for a concrete stop condition.%s", finalOutcome, scope, reason),
		}
	}
	if r.qualityPlan == nil {
		return nil
	}
	if phase := firstUnverifiedQualityPlanPhase(r.qualityPlan.Phases); phase.Name != "" {
		return &FinalResponseAudit{
			Code:    "quality_plan_partial_unfinished",
			Message: fmt.Sprintf("final_response outcome was %s, but required quality plan phase %q is still %s. Continue with that phase now; use outcome blocked or failed only when work cannot continue for a concrete reason.", finalOutcome, phase.Name, qualityPlanPhaseStatusForMessage(phase.Status)),
		}
	}
	return nil
}

func (r *Runner) qualityPlanCompletionBlock(verificationChecks []tools.VerificationCheck) *FinalResponseAudit {
	if r == nil {
		return nil
	}
	if r.QualityPlanRequired && r.qualityPlan == nil {
		scope := strings.TrimSpace(r.QualityPlanRequirementScope)
		if scope == "" {
			scope = "sizable"
		}
		reason := strings.TrimSpace(r.QualityPlanRequirementReason)
		if reason != "" {
			reason = " Preflight reason: " + reason
		}
		return &FinalResponseAudit{
			Code:    "quality_plan_required_missing",
			Message: fmt.Sprintf("final_response outcome was completed, but planning preflight classified this as %s work and no quality plan has been recorded. Call update_quality_plan with concrete phases and evidence requirements, or set outcome to partial/blocked/failed if a phased plan is not appropriate.%s", scope, reason),
		}
	}
	if r.qualityPlan == nil {
		return nil
	}
	plan := r.qualityPlan
	if phase := firstUnverifiedQualityPlanPhase(plan.Phases); phase.Name != "" {
		return &FinalResponseAudit{
			Code:    "quality_plan_phase_unverified",
			Message: fmt.Sprintf("final_response outcome was completed, but quality plan phase %q is still %s. Mark planned phases verified with concrete evidence, skip them with a reason, or set outcome to partial/blocked/failed before calling final_response again.", phase.Name, qualityPlanPhaseStatusForMessage(phase.Status)),
		}
	}
	if phase := firstQualityPlanPhaseMissingEvidence(plan.Phases); phase.Name != "" {
		return &FinalResponseAudit{
			Code:    "quality_plan_phase_evidence_missing",
			Message: fmt.Sprintf("final_response outcome was completed, but quality plan phase %q is marked %s without evidence or a note. Add concrete phase evidence, add a skip reason, or set outcome to partial/blocked/failed before calling final_response again.", phase.Name, qualityPlanPhaseStatusForMessage(phase.Status)),
		}
	}
	if plan.RequiresRuntimeVerification && len(passedVerificationChecks(verificationChecks)) == 0 {
		return &FinalResponseAudit{
			Code:    "quality_plan_runtime_evidence_missing",
			Message: "final_response outcome was completed, but the quality plan requires runtime verification and no passing run_command check marked purpose=verify is recorded. Run an appropriate runtime/build/test check, or set outcome to partial/blocked/failed and explain why runtime verification is unavailable.",
		}
	}
	if plan.RequiresTemporalVisualVerification && r.temporalImageAnalyses == 0 {
		return &FinalResponseAudit{
			Code:    "quality_plan_temporal_visual_evidence_missing",
			Message: "final_response outcome was completed, but the quality plan requires temporal visual verification and no successful analyze_image result with comparison_path is recorded. Capture or locate two observations separated in time, run one focused analyze_image with path and comparison_path, or set outcome to partial/blocked/failed and explain why temporal visual verification is unavailable.",
		}
	}
	if plan.RequiresVisualVerification && r.imageAnalyses == 0 {
		return &FinalResponseAudit{
			Code:    "quality_plan_visual_evidence_missing",
			Message: "final_response outcome was completed, but the quality plan requires visual verification and no successful analyze_image result is recorded. Capture or locate a screenshot/image and run one focused analyze_image check, or set outcome to partial/blocked/failed and explain why visual verification is unavailable.",
		}
	}
	return nil
}

func (r *Runner) qualityPlanWriteToolBlock(tool string) *tools.ToolResult {
	if r == nil || !r.QualityPlanRequired || r.qualityPlan != nil {
		return nil
	}
	if !isWorkspaceWriteTool(tool) {
		return nil
	}
	scope := strings.TrimSpace(r.QualityPlanRequirementScope)
	if scope == "" {
		scope = "sizable"
	}
	reason := strings.TrimSpace(r.QualityPlanRequirementReason)
	if reason != "" {
		reason = " Preflight reason: " + reason
	}
	return &tools.ToolResult{
		Success: false,
		Error:   fmt.Sprintf("planning preflight classified this as %s work, so update_quality_plan must be called before write tools such as %s. Publish concrete phases first, then retry the write.%s", scope, tool, reason),
	}
}

func (r *Runner) phaseWriteGateBlock(ctx context.Context, tool string, rawArgs json.RawMessage) (*tools.ToolResult, error) {
	if r == nil || r.PhaseWriteGate == nil || !r.QualityPlanRequired || r.qualityPlan == nil || !isWorkspaceWriteTool(tool) {
		return nil, nil
	}
	if len(r.qualityPlan.Phases) <= 1 {
		return nil, nil
	}
	activePhase, activeIndex, ok := activeQualityPlanPhase(r.qualityPlan)
	if !ok {
		return nil, nil
	}
	phaseKey := phaseWriteGateAdmissionKey(r.qualityPlan, activeIndex, activePhase)
	if phaseKey != "" && phaseKey == r.phaseWriteGateAdmitted {
		return nil, nil
	}
	argsText, truncated := boundedPhaseWriteGateArgs(rawArgs)
	request := PhaseWriteGateRequest{
		SessionID:           r.SessionID,
		UserRequest:         strings.TrimSpace(r.Prompt),
		Tool:                tool,
		ToolArgs:            argsText,
		ToolArgsTruncated:   truncated,
		QualityPlan:         *r.qualityPlan,
		ActivePhaseIndex:    activeIndex,
		ActivePhase:         activePhase,
		CompletedPhaseNames: completedQualityPlanPhaseNames(r.qualityPlan.Phases),
		RemainingPhaseNames: remainingQualityPlanPhaseNames(r.qualityPlan.Phases, activeIndex),
	}
	result, err := r.PhaseWriteGate.EvaluatePhaseWrite(ctx, request)
	if err != nil {
		if writeErr := r.writePhaseWriteGateFailedEvent(request, err); writeErr != nil {
			return nil, writeErr
		}
		r.phaseWriteGateAdmitted = phaseKey
		return nil, nil
	}
	if err := r.writePhaseWriteGateResultEvent(request, result); err != nil {
		return nil, err
	}
	if result.Allow && result.FitsActivePhase && !result.ContainsLaterPhaseWork && !result.TooMuchAtOnce {
		r.phaseWriteGateAdmitted = phaseKey
		return nil, nil
	}
	message := formatPhaseWriteGateBlockMessage(request, result)
	return &tools.ToolResult{Success: false, Error: message}, nil
}

func phaseWriteGateAdmissionKey(plan *QualityPlan, activeIndex int, activePhase QualityPlanPhase) string {
	if plan == nil || activeIndex < 0 {
		return ""
	}
	return fmt.Sprintf("%d:%s:%s", activeIndex, strings.TrimSpace(activePhase.Name), strings.TrimSpace(activePhase.Status))
}

func isWorkspaceWriteTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "apply_patch", "create_file", "replace_file", "replace_text", "replace_lines":
		return true
	default:
		return false
	}
}

func activeQualityPlanPhase(plan *QualityPlan) (QualityPlanPhase, int, bool) {
	if plan == nil {
		return QualityPlanPhase{}, -1, false
	}
	for i, phase := range plan.Phases {
		switch lcQualityPlanStatusKey(phase.Status) {
		case "in_progress", "implemented", "needs_repair":
			return phase, i, true
		}
	}
	for i, phase := range plan.Phases {
		switch lcQualityPlanStatusKey(phase.Status) {
		case "verified", "skipped":
			continue
		default:
			return phase, i, true
		}
	}
	return QualityPlanPhase{}, -1, false
}

func completedQualityPlanPhaseNames(phases []QualityPlanPhase) []string {
	var out []string
	for _, phase := range phases {
		switch lcQualityPlanStatusKey(phase.Status) {
		case "verified", "skipped":
			if name := strings.TrimSpace(phase.Name); name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

func remainingQualityPlanPhaseNames(phases []QualityPlanPhase, activeIndex int) []string {
	if activeIndex < 0 {
		activeIndex = 0
	}
	var out []string
	for i := activeIndex; i < len(phases); i++ {
		if name := strings.TrimSpace(phases[i].Name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func lcQualityPlanStatusKey(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	status = strings.ReplaceAll(status, "-", "_")
	status = strings.ReplaceAll(status, " ", "_")
	return status
}

func boundedPhaseWriteGateArgs(raw json.RawMessage) (string, bool) {
	text := strings.TrimSpace(string(raw))
	var compact bytes.Buffer
	if len(raw) > 0 && json.Compact(&compact, raw) == nil {
		text = compact.String()
	}
	return boundedCriticConsultText(text, maxPhaseWriteGateArgsChars)
}

func formatPhaseWriteGateBlockMessage(request PhaseWriteGateRequest, result PhaseWriteGateResult) string {
	phaseName := firstNonEmpty(strings.TrimSpace(request.ActivePhase.Name), fmt.Sprintf("phase %d", request.ActivePhaseIndex+1))
	reason := strings.TrimSpace(result.Reason)
	if reason == "" {
		reason = "the proposed write does not fit the current active phase"
	}
	message := fmt.Sprintf("phase write gate blocked %s: current active phase is %q, but %s.", request.Tool, phaseName, reason)
	if suggestion := strings.TrimSpace(result.SuggestedSmallerSlice); suggestion != "" {
		message += " Suggested smaller slice: " + suggestion
	}
	return message
}

func (r *Runner) validateQualityPlanProgression(plan QualityPlan) tools.ToolResult {
	if phase, ok := firstOutOfOrderQualityPlanPhase(plan.Phases); ok {
		message := fmt.Sprintf("quality plan phases must advance one at a time; phase %q is %s before the previous phase is verified or skipped", phase.Name, qualityPlanPhaseStatusForMessage(phase.Status))
		return tools.ToolResult{Success: false, Error: message}
	}
	newCompleted := qualityPlanCompletedPrefix(plan.Phases)
	if r == nil || r.qualityPlan == nil {
		if newCompleted > 0 {
			message := "initial quality plan cannot start with verified or skipped phases; start with the first phase in_progress and keep later phases planned, then advance phases after concrete evidence"
			return tools.ToolResult{Success: false, Error: message}
		}
		return tools.ToolResult{Success: true}
	}
	if len(plan.Phases) < len(r.qualityPlan.Phases) {
		message := fmt.Sprintf("quality plan cannot shrink from %d phases to %d while work is active; keep the phase list stable and advance phases one at a time", len(r.qualityPlan.Phases), len(plan.Phases))
		return tools.ToolResult{Success: false, Error: message}
	}
	if r.qualityPlan.RequiresRuntimeVerification && !plan.RequiresRuntimeVerification {
		message := "quality plan cannot turn off runtime verification after it was required; keep the requirement or finish partial/blocked/failed if evidence is unavailable"
		return tools.ToolResult{Success: false, Error: message}
	}
	if r.qualityPlan.RequiresVisualVerification && !plan.RequiresVisualVerification {
		message := "quality plan cannot turn off visual verification after it was required; keep the requirement or finish partial/blocked/failed if evidence is unavailable"
		return tools.ToolResult{Success: false, Error: message}
	}
	if r.qualityPlan.RequiresTemporalVisualVerification && !plan.RequiresTemporalVisualVerification {
		message := "quality plan cannot turn off temporal visual verification after it was required; keep the requirement or finish partial/blocked/failed if evidence is unavailable"
		return tools.ToolResult{Success: false, Error: message}
	}
	oldCompleted := qualityPlanCompletedPrefix(r.qualityPlan.Phases)
	if newCompleted > oldCompleted+1 {
		message := fmt.Sprintf("quality plan advanced %d phases at once; advance at most one phase per update after concrete evidence", newCompleted-oldCompleted)
		return tools.ToolResult{Success: false, Error: message}
	}
	return tools.ToolResult{Success: true}
}

func (r *Runner) QualityPlanCompletedPrefix() int {
	if r == nil || r.qualityPlan == nil {
		return 0
	}
	return qualityPlanCompletedPrefix(r.qualityPlan.Phases)
}

func firstOutOfOrderQualityPlanPhase(phases []QualityPlanPhase) (QualityPlanPhase, bool) {
	stage := 0 // verified/skipped prefix, then at most one active phase, then planned tail.
	for _, phase := range phases {
		status := normalizeQualityPlanPhaseStatus(phase.Status)
		if status == "" {
			status = "planned"
		}
		switch stage {
		case 0:
			switch {
			case qualityPlanPhaseCompleteStatus(status):
				continue
			case qualityPlanPhaseActiveStatus(status):
				stage = 1
			case status == "planned":
				stage = 2
			}
		case 1:
			if status == "planned" {
				stage = 2
				continue
			}
			return phase, true
		default:
			if status != "planned" {
				return phase, true
			}
		}
	}
	return QualityPlanPhase{}, false
}

func qualityPlanCompletedPrefix(phases []QualityPlanPhase) int {
	completed := 0
	for _, phase := range phases {
		if !qualityPlanPhaseCompleteStatus(normalizeQualityPlanPhaseStatus(phase.Status)) {
			break
		}
		completed++
	}
	return completed
}

func qualityPlanPhaseCompleteStatus(status string) bool {
	return status == "verified" || status == "skipped"
}

func qualityPlanPhaseActiveStatus(status string) bool {
	switch status {
	case "in_progress", "implemented", "needs_repair":
		return true
	default:
		return false
	}
}

func firstUnverifiedQualityPlanPhase(phases []QualityPlanPhase) QualityPlanPhase {
	for _, phase := range phases {
		switch normalizeQualityPlanPhaseStatus(phase.Status) {
		case "verified", "skipped":
			continue
		default:
			if strings.TrimSpace(phase.Name) != "" {
				return phase
			}
		}
	}
	return QualityPlanPhase{}
}

func firstQualityPlanPhaseMissingEvidence(phases []QualityPlanPhase) QualityPlanPhase {
	for _, phase := range phases {
		switch normalizeQualityPlanPhaseStatus(phase.Status) {
		case "verified", "skipped":
			if len(cleanStringList(phase.Evidence)) == 0 && strings.TrimSpace(phase.Notes) == "" {
				if strings.TrimSpace(phase.Name) != "" {
					return phase
				}
			}
		}
	}
	return QualityPlanPhase{}
}

func qualityPlanPhaseStatusForMessage(status string) string {
	status = normalizeQualityPlanPhaseStatus(status)
	if status == "" {
		return "unverified"
	}
	return strings.ReplaceAll(status, "_", " ")
}

func latestOperationalActionRequiringVerification(actions []OperationalAction) (OperationalAction, bool) {
	for i := len(actions) - 1; i >= 0; i-- {
		action := actions[i]
		switch strings.TrimSpace(action.Action) {
		case string(ProcessActionStart), string(ProcessActionStop):
			return action, true
		}
	}
	return OperationalAction{}, false
}

func normalizeFinalResponseOutcome(outcome string) string {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "completed", "blocked", "failed", "partial":
		return strings.ToLower(strings.TrimSpace(outcome))
	default:
		return "unknown"
	}
}

func (a FinalResponseAudit) VerificationFeedback() (VerificationFeedback, bool) {
	if !a.Blocking {
		return VerificationFeedback{}, false
	}
	message := strings.TrimSpace(a.Message)
	if !strings.HasPrefix(message, "Verification feedback:") {
		message = "Verification feedback: " + message
	}
	return VerificationFeedback{Status: a.VerificationStatus, Message: message}, true
}

func finalAuditBlockingMessage(status string) string {
	switch status {
	case "reported_only":
		return "final_response reported verification, but no run_command check marked purpose=verify has run. Run an appropriate verification command, or explain clearly why verification is blocked, then call final_response again."
	default:
		return "final_response listed changed files, but no run_command check marked purpose=verify has run. Run an appropriate verification command, or explain clearly why verification is blocked, then call final_response again."
	}
}

func (f VerificationFeedback) ModelMessage() string {
	return strings.TrimSpace(f.Message)
}

func (r *Runner) WriteVerificationFeedback(feedback VerificationFeedback) error {
	return r.Session.Write(verificationFeedbackEvent(r.SessionID, feedback))
}

func (r *Runner) WriteFinalResponseAudit(audit FinalResponseAudit) error {
	if r == nil || r.Session == nil {
		return nil
	}
	return r.Session.Write(finalResponseAuditEvent(r.SessionID, audit))
}

func PatchFeedbackForResult(result tools.ToolResult) (PatchFeedback, bool) {
	if result.Success || result.PatchFailure == nil {
		return PatchFeedback{}, false
	}
	failure := result.PatchFailure
	target := firstNonEmpty(failure.Path, "patch")
	message := "Patch feedback: " + target + " failed during " + firstNonEmpty(failure.Stage, "apply_patch") + ": " + firstNonEmpty(failure.Message, result.Error)
	if hint := strings.TrimSpace(failure.Hint); hint != "" {
		message += ". " + hint
	}
	return PatchFeedback{
		Stage:          failure.Stage,
		Path:           failure.Path,
		Message:        message,
		SuggestedReads: append([]tools.ReadSuggestion(nil), failure.SuggestedReads...),
	}, true
}

func (f PatchFeedback) ModelMessage() string {
	return strings.TrimSpace(f.Message)
}

func PatchRetryGuidance(feedback PatchFeedback, repeatCount int) string {
	message := strings.TrimSpace(feedback.Message)
	if message == "" || repeatCount < 2 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Patch retry guidance: the same patch feedback has repeated %d times. Stop retrying the same patch unchanged.", repeatCount)
	if len(feedback.SuggestedReads) > 0 {
		b.WriteString(" First call ")
		b.WriteString(formatReadSuggestionsForModel(feedback.SuggestedReads))
		b.WriteString(" to refresh exact current lines.")
	} else {
		b.WriteString(" Re-read the affected file before another patch attempt.")
	}
	b.WriteString(" Then retry with a smaller hunk that preserves current unchanged context, use replace_lines when exact current line numbers are known, or use replace_text with exact old_text copied from the current file for a small literal edit.")
	return b.String()
}

func (r *Runner) WritePatchFeedback(feedback PatchFeedback) error {
	return r.Session.Write(patchFeedbackEvent(r.SessionID, feedback))
}

func formatReadSuggestionsForModel(suggestions []tools.ReadSuggestion) string {
	calls := make([]string, 0, len(suggestions))
	for _, suggestion := range suggestions {
		if strings.TrimSpace(suggestion.Path) == "" || suggestion.Offset <= 0 || suggestion.Limit <= 0 {
			continue
		}
		calls = append(calls, fmt.Sprintf(`read_file {"path":%q,"offset":%d,"limit":%d}`, suggestion.Path, suggestion.Offset, suggestion.Limit))
		if len(calls) >= 2 {
			break
		}
	}
	if len(calls) == 0 {
		return "read_file on the affected range"
	}
	return strings.Join(calls, " and ")
}

type commandArgs struct {
	Command          string   `json:"command"`
	Argv             []string `json:"argv"`
	CWD              string   `json:"cwd"`
	Shell            bool     `json:"shell"`
	TimeoutMS        int      `json:"timeout_ms"`
	Purpose          string   `json:"purpose"`
	AdminScope       string   `json:"admin_scope"`
	AllowedExitCodes []int    `json:"allowed_exit_codes"`
}

type processArgs struct {
	Command   string `json:"command"`
	CWD       string `json:"cwd"`
	ProcessID string `json:"process_id"`
	Name      string `json:"name"`
}

type patchArgs struct {
	Patch string `json:"patch"`
}

type planArgs struct {
	Items []tools.PlanItem `json:"items"`
}

func (p *planArgs) UnmarshalJSON(raw []byte) error {
	var args struct {
		Items json.RawMessage `json:"items"`
		Todos json.RawMessage `json:"todos"`
		Plan  json.RawMessage `json:"plan"`
	}
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}

	var provided []string
	if len(args.Items) > 0 {
		items, err := parsePlanItemsJSON(args.Items, "items")
		if err != nil {
			return err
		}
		provided = append(provided, "items")
		p.Items = items
	}
	if len(args.Todos) > 0 {
		items, err := parsePlanItemsJSON(args.Todos, "todos")
		if err != nil {
			return err
		}
		provided = append(provided, "todos")
		p.Items = items
	}
	if len(args.Plan) > 0 {
		items, err := parsePlanItemsJSON(args.Plan, "plan")
		if err != nil {
			return err
		}
		provided = append(provided, "plan")
		p.Items = items
	}
	switch len(provided) {
	case 0:
		return fmt.Errorf(`missing plan items; expected {"items":[{"step":"Inspect","status":"in_progress"}]}`)
	case 1:
		return nil
	default:
		return fmt.Errorf(`provide only one of "items", "todos", or "plan" for update_plan; got %s`, strings.Join(provided, ", "))
	}
}

func parsePlanItemsJSON(raw json.RawMessage, field string) ([]tools.PlanItem, error) {
	raw = bytes.TrimSpace(raw)
	var items []tools.PlanItem
	if len(raw) > 0 && raw[0] == '"' {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return nil, fmt.Errorf("%s must be an array of plan items or a JSON-encoded array string: %w", field, err)
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(encoded)), &items); err != nil {
			return nil, fmt.Errorf("%s string must contain a JSON array of plan items: %w", field, err)
		}
		return items, nil
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("%s must be an array of plan items: %w", field, err)
	}
	return items, nil
}

type readFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

type listFilesArgs struct {
	Path          string `json:"path"`
	Glob          string `json:"glob"`
	MaxEntries    int    `json:"max_entries"`
	IncludeHidden bool   `json:"include_hidden"`
	CaseSensitive *bool  `json:"case_sensitive"`
}

type repoOverviewArgs struct {
	Path          string `json:"path"`
	MaxFiles      int    `json:"max_files"`
	IncludeHidden bool   `json:"include_hidden"`
}

type searchArgs struct {
	Query         string `json:"query"`
	Path          string `json:"path"`
	FileGlob      string `json:"file_glob"`
	MaxMatches    int    `json:"max_matches"`
	ContextBefore *int   `json:"context_before"`
	ContextAfter  *int   `json:"context_after"`
	IncludeHidden bool   `json:"include_hidden"`
	OutputMode    string `json:"output_mode"`
	Intent        string `json:"intent"`
}

type webSearchArgs struct {
	Query       string `json:"query"`
	MaxResults  int    `json:"max_results"`
	Site        string `json:"site"`
	RecencyDays int    `json:"recency_days"`
}

type browserNavigateArgs struct {
	URL string `json:"url"`
}

type browserSnapshotArgs struct {
	MaxChars int `json:"max_chars"`
}

type browserRefArgs struct {
	Ref string `json:"ref"`
}

type browserFillArgs struct {
	Ref   string `json:"ref"`
	Value string `json:"value"`
}

type browserPressArgs struct {
	Key string `json:"key"`
}

type browserScreenshotArgs struct {
	Path string `json:"path"`
}

type browserWaitForUserArgs struct {
	Message string `json:"message"`
	URL     string `json:"url"`
}

type fileOutlineArgs struct {
	Path string `json:"path"`
}

type moduleOutlineArgs struct {
	Path          string `json:"path"`
	FileGlob      string `json:"file_glob"`
	MaxFiles      int    `json:"max_files"`
	IncludeHidden bool   `json:"include_hidden"`
}

type scoutFilesArgs struct {
	Question        string `json:"question"`
	Path            string `json:"path"`
	FileGlob        string `json:"file_glob"`
	MaxFiles        int    `json:"max_files"`
	MaxLinesPerFile int    `json:"max_lines_per_file"`
	IncludeHidden   bool   `json:"include_hidden"`
}

type loadSkillArgs struct {
	Name string `json:"name"`
}

func DecodeFinalResponseArgs(raw json.RawMessage) (Action, error) {
	var args finalResponseArgs
	if err := decodeStrictJSON(raw, &args); err != nil {
		return Action{}, err
	}
	return Action{
		Type:         "final_response",
		Summary:      args.Summary,
		Outcome:      args.Outcome,
		FilesChanged: args.FilesChanged,
		Verification: args.Verification,
	}, nil
}

func decodeToolArgs(tool string, raw json.RawMessage, dst any) (tools.ToolResult, bool) {
	if err := decodeStrictJSON(raw, dst); err != nil {
		return tools.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("invalid %s arguments: %v", firstNonEmpty(tool, "tool"), toolArgumentErrorHint(tool, err)),
		}, false
	}
	return tools.ToolResult{}, true
}

func toolArgumentErrorHint(tool string, err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch tool {
	case "update_plan":
		if strings.Contains(msg, "unknown field") || strings.Contains(msg, "missing plan items") {
			return msg + `; expected {"items":[{"step":"Inspect","status":"pending|in_progress|completed"}]}; legacy aliases "todos" and "plan" are also accepted`
		}
	}
	return msg
}

func decodeStrictJSON(raw json.RawMessage, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func Load(path string) ([]Action, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var actions []Action
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var action Action
		if err := json.Unmarshal(line, &action); err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return actions, nil
}

func (r *Runner) Run(ctx context.Context, actions []Action) error {
	if err := r.Session.Write(session.Event{
		"type":       "user_message",
		"session_id": r.SessionID,
		"origin":     "initial_prompt",
		"message":    r.Prompt,
	}); err != nil {
		return err
	}
	if objective := strings.TrimSpace(r.Prompt); objective != "" {
		if err := r.Session.Write(session.Event{
			"type":       "active_objective",
			"session_id": r.SessionID,
			"objective":  objective,
		}); err != nil {
			return err
		}
	}
	for _, action := range actions {
		switch action.Type {
		case "tool_call":
			result, err := r.RunTool(ctx, action)
			if err != nil {
				if feedback, ok := PatchFeedbackForResult(result); ok {
					_ = r.WritePatchFeedback(feedback)
				}
				if feedback, ok := r.VerificationFeedbackForResult(result); ok {
					_ = r.WriteVerificationFeedback(feedback)
				}
				_ = r.Session.Write(session.Event{
					"type":       "turn_aborted",
					"session_id": r.SessionID,
					"reason":     err.Error(),
				})
				return err
			}
		case "final_response":
			return r.Final(action)
		default:
			err := fmt.Errorf("unsupported script action type: %s", action.Type)
			_ = r.Session.Write(session.Event{
				"type":       "turn_aborted",
				"session_id": r.SessionID,
				"reason":     err.Error(),
			})
			return err
		}
	}
	err := fmt.Errorf("script ended without final_response")
	_ = r.Session.Write(session.Event{
		"type":       "turn_aborted",
		"session_id": r.SessionID,
		"reason":     err.Error(),
	})
	return err
}

func (r *Runner) RunTool(ctx context.Context, action Action) (tools.ToolResult, error) {
	if err := r.Session.Write(session.Event{
		"type":       "tool_call",
		"session_id": r.SessionID,
		"tool":       action.Tool,
		"args":       json.RawMessage(action.Args),
	}); err != nil {
		return tools.ToolResult{}, err
	}

	var result tools.ToolResult
	browserTool := isBrowserTool(action.Tool)
	if browserTool {
		r.browserToolsUsed = true
		if err := r.writeBrowserActivityEvent("browser_activity_started", action.Tool, nil); err != nil {
			return tools.ToolResult{}, err
		}
	}
	switch action.Tool {
	case "read_file":
		var args readFileArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		result = r.Files.Read(args.Path, args.Offset, args.Limit)
	case "list_files":
		var args listFilesArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		result = r.Files.ListWithOptions(args.Path, args.Glob, args.MaxEntries, tools.ListOptions{IncludeHidden: args.IncludeHidden, CaseSensitive: args.CaseSensitive})
	case "repo_overview":
		var args repoOverviewArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		result = r.Files.RepoOverview(args.Path, tools.RepoOverviewOptions{MaxFiles: args.MaxFiles, IncludeHidden: args.IncludeHidden})
	case "search":
		var args searchArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		var err error
		result, err = r.runSearch(ctx, args)
		if err != nil {
			return tools.ToolResult{}, err
		}
	case "scout_files":
		var args scoutFilesArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		var err error
		result, err = r.runScoutFiles(ctx, args)
		if err != nil {
			return tools.ToolResult{}, err
		}
	case "consult_critic":
		var args consultCriticArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		var err error
		result, err = r.runConsultCritic(ctx, args)
		if err != nil {
			return tools.ToolResult{}, err
		}
	case "analyze_image":
		var args analyzeImageArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		var err error
		result, err = r.runAnalyzeImage(ctx, args)
		if err != nil {
			return tools.ToolResult{}, err
		}
		if result.Success {
			r.imageAnalyses++
		}
	case "update_quality_plan":
		var args qualityPlanArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		plan, planResult := normalizeQualityPlan(QualityPlan(args))
		if !planResult.Success {
			result = planResult
			break
		}
		if planResult := r.validateQualityPlanProgression(plan); !planResult.Success {
			result = planResult
			break
		}
		r.qualityPlan = &plan
		r.qualityPlanUpdates++
		result = tools.ToolResult{Success: true, Output: formatQualityPlanResult(plan)}
		if err := r.Session.Write(session.Event{
			"type":                                  "quality_plan_update",
			"session_id":                            r.SessionID,
			"update":                                r.qualityPlanUpdates,
			"artifact_type":                         plan.ArtifactType,
			"requires_runtime_verification":         plan.RequiresRuntimeVerification,
			"requires_visual_verification":          plan.RequiresVisualVerification,
			"requires_temporal_visual_verification": plan.RequiresTemporalVisualVerification,
			"phases":                                plan.Phases,
			"notes":                                 plan.Notes,
		}); err != nil {
			return tools.ToolResult{}, err
		}
	case "web_search":
		var args webSearchArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		if !r.WebSearchOn {
			result = tools.ToolResult{Success: false, Error: "web_search is not configured for this LCAgent run"}
			break
		}
		result = r.WebSearch.Search(ctx, args.Query, args.MaxResults, args.Site, args.RecencyDays)
	case "browser_navigate", "browser_snapshot", "browser_click", "browser_fill", "browser_press", "browser_screenshot", "browser_current_page":
		if invalid, ok := validateBrowserToolArgs(action.Tool, action.Args); !ok {
			result = invalid
			break
		}
		if !r.BrowserAvailable {
			result = tools.ToolResult{Success: false, Error: "browser tools are not available for this LCAgent run"}
			break
		}
		if r.Browser == nil {
			result = tools.ToolResult{Success: false, Error: "managed browser runtime is not configured for this LCAgent run"}
			break
		}
		result = r.Browser.RunBrowserTool(ctx, action.Tool, action.Args)
	case "browser_wait_for_user":
		r.browserWaitForUserUsed = true
		var args browserWaitForUserArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		result = r.runBrowserWaitForUser(ctx, args)
	case "file_outline":
		var args fileOutlineArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		result = r.Files.Outline(args.Path)
	case "module_outline":
		var args moduleOutlineArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		result = r.Files.ModuleOutlineWithOptions(args.Path, args.FileGlob, args.MaxFiles, tools.ModuleOutlineOptions{IncludeHidden: args.IncludeHidden})
	case "load_skill":
		var args loadSkillArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		loaded, err := r.Skills.Load(args.Name)
		if err != nil {
			result = tools.ToolResult{Success: false, Error: err.Error()}
			break
		}
		result = tools.ToolResult{Success: true, Output: formatLoadedSkill(loaded), Truncated: loaded.Truncated}
		if err := r.Session.Write(session.Event{
			"type":        "skill_loaded",
			"session_id":  r.SessionID,
			"name":        loaded.Skill.Name,
			"source":      loaded.Skill.Source,
			"path":        loaded.Skill.Path,
			"description": loaded.Skill.Description,
			"truncated":   loaded.Truncated,
		}); err != nil {
			return tools.ToolResult{}, err
		}
	case "run_command":
		var args commandArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		spec := tools.CommandSpec{
			Command:          args.Command,
			Argv:             args.Argv,
			CWD:              args.CWD,
			Shell:            args.Shell || args.Command != "",
			TimeoutMS:        args.TimeoutMS,
			Purpose:          args.Purpose,
			AdminScope:       args.AdminScope,
			AllowedExitCodes: args.AllowedExitCodes,
		}
		result = r.runCommandWithApproval(ctx, spec)
		if strings.EqualFold(result.Purpose, tools.CommandPurposeVerify) {
			check := tools.VerificationCheckFromResult(result)
			r.verificationChecks = append(r.verificationChecks, check)
			if err := r.Session.Write(verificationCheckEvent(r.SessionID, check)); err != nil {
				return tools.ToolResult{}, err
			}
		}
	case "start_process":
		var args processArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		spec := tools.CommandSpec{
			Command: strings.TrimSpace(args.Command),
			CWD:     strings.TrimSpace(args.CWD),
			Shell:   true,
		}
		if spec.Command == "" {
			result = tools.ToolResult{Success: false, Error: "start_process command is required"}
			break
		}
		result = r.runProcessWithApproval(ctx, ProcessRequest{
			SessionID: r.SessionID,
			Action:    ProcessActionStart,
			Command:   spec.Command,
			CWD:       spec.CWD,
			Name:      strings.TrimSpace(args.Name),
		}, spec, "start_process")
	case "list_processes":
		result = r.runProcess(ctx, ProcessRequest{
			SessionID: r.SessionID,
			Action:    ProcessActionList,
		})
	case "stop_process":
		var args processArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		result = r.runProcess(ctx, ProcessRequest{
			SessionID: r.SessionID,
			Action:    ProcessActionStop,
			ProcessID: strings.TrimSpace(args.ProcessID),
		})
	case "apply_patch":
		if blocked := r.qualityPlanWriteToolBlock(action.Tool); blocked != nil {
			result = *blocked
			break
		}
		var args patchArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		if blocked, err := r.phaseWriteGateBlock(ctx, action.Tool, action.Args); err != nil {
			return tools.ToolResult{}, err
		} else if blocked != nil {
			result = *blocked
			break
		}
		result = r.Patch.Apply(args.Patch)
	case "create_file":
		if blocked := r.qualityPlanWriteToolBlock(action.Tool); blocked != nil {
			result = *blocked
			break
		}
		var args createFileArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		if blocked, err := r.phaseWriteGateBlock(ctx, action.Tool, action.Args); err != nil {
			return tools.ToolResult{}, err
		} else if blocked != nil {
			result = *blocked
			break
		}
		result = tools.TextEditor{Workspace: r.Patch.Workspace}.CreateFile(tools.CreateFileSpec{
			Path:    args.Path,
			Content: args.Content,
		})
	case "replace_file":
		if blocked := r.qualityPlanWriteToolBlock(action.Tool); blocked != nil {
			result = *blocked
			break
		}
		var args replaceFileArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		if blocked, err := r.phaseWriteGateBlock(ctx, action.Tool, action.Args); err != nil {
			return tools.ToolResult{}, err
		} else if blocked != nil {
			result = *blocked
			break
		}
		result = tools.TextEditor{Workspace: r.Patch.Workspace}.ReplaceFile(tools.ReplaceFileSpec{
			Path:           args.Path,
			Content:        args.Content,
			ExpectedSHA256: args.ExpectedSHA256,
		})
	case "replace_text":
		if blocked := r.qualityPlanWriteToolBlock(action.Tool); blocked != nil {
			result = *blocked
			break
		}
		var args replaceTextArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		if blocked, err := r.phaseWriteGateBlock(ctx, action.Tool, action.Args); err != nil {
			return tools.ToolResult{}, err
		} else if blocked != nil {
			result = *blocked
			break
		}
		result = tools.TextEditor{Workspace: r.Patch.Workspace}.ReplaceText(tools.ReplaceTextSpec{
			Path:                 args.Path,
			OldText:              args.OldText,
			NewText:              args.NewText,
			ExpectedReplacements: args.ExpectedReplacements,
		})
	case "replace_lines":
		if blocked := r.qualityPlanWriteToolBlock(action.Tool); blocked != nil {
			result = *blocked
			break
		}
		var args replaceLinesArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		if blocked, err := r.phaseWriteGateBlock(ctx, action.Tool, action.Args); err != nil {
			return tools.ToolResult{}, err
		} else if blocked != nil {
			result = *blocked
			break
		}
		result = tools.TextEditor{Workspace: r.Patch.Workspace}.ReplaceLines(tools.ReplaceLinesSpec{
			Path:              args.Path,
			StartLine:         args.StartLine,
			EndLine:           args.EndLine,
			NewText:           args.NewText,
			ExpectedFirstLine: args.ExpectedFirstLine,
			ExpectedLastLine:  args.ExpectedLastLine,
		})
	case "update_plan":
		var args planArgs
		if invalid, ok := decodeToolArgs(action.Tool, action.Args, &args); !ok {
			result = invalid
			break
		}
		result = tools.ToolResult{Success: true, Output: "plan updated"}
		if err := r.Session.Write(session.Event{
			"type":       "plan_update",
			"session_id": r.SessionID,
			"items":      args.Items,
		}); err != nil {
			return tools.ToolResult{}, err
		}
	default:
		result = tools.ToolResult{Success: false, Error: "unsupported tool: " + action.Tool}
	}

	if action.Tool == "apply_patch" || action.Tool == "create_file" || action.Tool == "replace_file" || action.Tool == "replace_text" || action.Tool == "replace_lines" {
		r.recordEditSummary(result.PatchSummary)
		if len(result.FilesTouched) > 0 {
			r.fileTouchEvents++
			r.filesTouched = appendCleanUniqueStrings(r.filesTouched, result.FilesTouched...)
			if err := r.Session.Write(session.Event{
				"type":       "files_touched",
				"session_id": r.SessionID,
				"files":      result.FilesTouched,
			}); err != nil {
				return tools.ToolResult{}, err
			}
		}
		if result.PatchSummary != nil || strings.TrimSpace(result.DiffSummary) != "" {
			if err := r.Session.Write(session.Event{
				"type":          "patch_diff_summary",
				"session_id":    r.SessionID,
				"files":         result.FilesTouched,
				"summary":       result.DiffSummary,
				"patch_summary": result.PatchSummary,
			}); err != nil {
				return tools.ToolResult{}, err
			}
		}
	}

	if result.Denied {
		if err := r.Session.Write(session.Event{
			"type":       "permission_denied",
			"session_id": r.SessionID,
			"tool":       action.Tool,
			"reason":     firstNonEmpty(result.DenialReason, result.Error),
		}); err != nil {
			return tools.ToolResult{}, err
		}
	}

	if browserTool {
		if page := browserPageFromToolResult(result); page != nil {
			if err := r.Session.Write(page.event(r.SessionID)); err != nil {
				return tools.ToolResult{}, err
			}
		}
		if err := r.writeBrowserActivityEvent("browser_activity_finished", action.Tool, &result); err != nil {
			return tools.ToolResult{}, err
		}
	}
	if result.Success && isInspectionEvidenceTool(action.Tool) {
		r.inspectionEvidence++
	}

	if err := r.Session.Write(session.Event{
		"type":       "tool_result",
		"session_id": r.SessionID,
		"tool":       action.Tool,
		"result":     result,
	}); err != nil {
		return tools.ToolResult{}, err
	}
	if !result.Success {
		r.toolFailures = append(r.toolFailures, result)
		return result, fmt.Errorf("%s failed: %s", action.Tool, result.Error)
	}
	return result, nil
}

func (r *Runner) runBrowserWaitForUser(ctx context.Context, args browserWaitForUserArgs) tools.ToolResult {
	message := strings.TrimSpace(args.Message)
	if message == "" {
		return tools.ToolResult{Success: false, Error: "browser_wait_for_user message is required"}
	}
	if !r.BrowserAvailable {
		return tools.ToolResult{Success: false, Error: "browser tools are not available for this LCAgent run"}
	}
	if r.SteerMessages == nil {
		return tools.ToolResult{Success: false, Error: "browser user handoff is not available for this LCAgent run"}
	}
	page := browserPageEvent{URL: strings.TrimSpace(args.URL), Fresh: true}
	if page.URL == "" && r.Browser != nil {
		current := r.Browser.RunBrowserTool(ctx, "browser_current_page", json.RawMessage(`{}`))
		if parsed := browserPageFromToolResult(current); parsed != nil {
			page = *parsed
		}
	}
	event := session.Event{
		"type":        "browser_waiting_for_user",
		"session_id":  r.SessionID,
		"server_name": "playwright",
		"tool":        "browser_wait_for_user",
		"message":     message,
	}
	if page.URL != "" {
		event["url"] = page.URL
		event["title"] = page.Title
		event["fresh"] = page.Fresh
	}
	if err := r.Session.Write(event); err != nil {
		return tools.ToolResult{Success: false, Error: err.Error()}
	}
	for {
		select {
		case <-ctx.Done():
			_ = r.writeBrowserActivityEvent("browser_activity_finished", "browser_wait_for_user", &tools.ToolResult{Success: false, Error: ctx.Err().Error()})
			return tools.ToolResult{Success: false, Error: ctx.Err().Error()}
		case message, ok := <-r.SteerMessages:
			if !ok {
				_ = r.writeBrowserActivityEvent("browser_activity_finished", "browser_wait_for_user", &tools.ToolResult{Success: false, Error: "browser user handoff channel closed"})
				return tools.ToolResult{Success: false, Error: "browser user handoff channel closed"}
			}
			message = strings.TrimSpace(message)
			if message == "" {
				continue
			}
			if err := r.Session.Write(session.Event{
				"type":       "user_message",
				"session_id": r.SessionID,
				"origin":     "browser_handoff",
				"message":    message,
			}); err != nil {
				return tools.ToolResult{Success: false, Error: err.Error()}
			}
			if err := r.writeBrowserActivityEvent("browser_activity_finished", "browser_wait_for_user", &tools.ToolResult{Success: true}); err != nil {
				return tools.ToolResult{Success: false, Error: err.Error()}
			}
			return tools.ToolResult{
				Success: true,
				Output:  "User responded after browser handoff.\nuser_message: " + message,
			}
		}
	}
}

func isBrowserTool(tool string) bool {
	switch tool {
	case "browser_navigate", "browser_snapshot", "browser_click", "browser_fill", "browser_press", "browser_screenshot", "browser_current_page":
		return true
	default:
		return false
	}
}

func isInspectionEvidenceTool(tool string) bool {
	switch tool {
	case "read_file",
		"list_files",
		"repo_overview",
		"search",
		"scout_files",
		"file_outline",
		"module_outline",
		"analyze_image",
		"web_search",
		"browser_snapshot",
		"browser_current_page",
		"browser_screenshot":
		return true
	default:
		return false
	}
}

func (r *Runner) writeBrowserActivityEvent(eventType, tool string, result *tools.ToolResult) error {
	event := session.Event{
		"type":        eventType,
		"session_id":  r.SessionID,
		"server_name": "playwright",
		"tool":        strings.TrimSpace(tool),
	}
	if result != nil {
		event["success"] = result.Success
		if strings.TrimSpace(result.Error) != "" {
			event["error"] = strings.TrimSpace(result.Error)
		}
	}
	return r.Session.Write(event)
}

type browserPageEvent struct {
	URL   string
	Title string
	Fresh bool
}

func (p browserPageEvent) event(sessionID string) session.Event {
	return session.Event{
		"type":       "browser_page",
		"session_id": sessionID,
		"url":        p.URL,
		"title":      p.Title,
		"fresh":      p.Fresh,
	}
}

func browserPageFromToolResult(result tools.ToolResult) *browserPageEvent {
	if !result.Success {
		return nil
	}
	var page browserPageEvent
	page.Fresh = true
	for _, line := range strings.Split(result.Output, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "url":
			page.URL = strings.TrimSpace(value)
		case "title":
			page.Title = strings.TrimSpace(value)
		case "fresh":
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "false", "no", "0":
				page.Fresh = false
			case "true", "yes", "1":
				page.Fresh = true
			}
		}
	}
	if page.URL == "" {
		return nil
	}
	return &page
}

func validateBrowserToolArgs(tool string, raw json.RawMessage) (tools.ToolResult, bool) {
	switch tool {
	case "browser_navigate":
		var args browserNavigateArgs
		if invalid, ok := decodeToolArgs(tool, raw, &args); !ok {
			return invalid, false
		}
		if strings.TrimSpace(args.URL) == "" {
			return tools.ToolResult{Success: false, Error: "browser_navigate url is required"}, false
		}
	case "browser_snapshot":
		var args browserSnapshotArgs
		if invalid, ok := decodeToolArgs(tool, raw, &args); !ok {
			return invalid, false
		}
	case "browser_click":
		var args browserRefArgs
		if invalid, ok := decodeToolArgs(tool, raw, &args); !ok {
			return invalid, false
		}
		if strings.TrimSpace(args.Ref) == "" {
			return tools.ToolResult{Success: false, Error: "browser_click ref is required"}, false
		}
	case "browser_fill":
		var args browserFillArgs
		if invalid, ok := decodeToolArgs(tool, raw, &args); !ok {
			return invalid, false
		}
		if strings.TrimSpace(args.Ref) == "" {
			return tools.ToolResult{Success: false, Error: "browser_fill ref is required"}, false
		}
	case "browser_press":
		var args browserPressArgs
		if invalid, ok := decodeToolArgs(tool, raw, &args); !ok {
			return invalid, false
		}
		if strings.TrimSpace(args.Key) == "" {
			return tools.ToolResult{Success: false, Error: "browser_press key is required"}, false
		}
	case "browser_screenshot":
		var args browserScreenshotArgs
		if invalid, ok := decodeToolArgs(tool, raw, &args); !ok {
			return invalid, false
		}
	case "browser_current_page":
		var args struct{}
		if invalid, ok := decodeToolArgs(tool, raw, &args); !ok {
			return invalid, false
		}
	}
	return tools.ToolResult{}, true
}

func (r *Runner) runCommandWithApproval(ctx context.Context, spec tools.CommandSpec) tools.ToolResult {
	result := r.Command.RunSpec(ctx, spec)
	if tools.IsWorkspaceWriteCommandDenied(result) {
		return result
	}
	if tools.IsSystemMutationCommandDenied(result) {
		return result
	}
	if !result.Denied || r.Approvals == nil || r.Command.Workspace.Auto != policy.AutonomyLow {
		return result
	}
	if r.commandApprovalGranted(spec, "run_command") {
		return r.runCommandAtMedium(ctx, spec)
	}
	grant := newCommandApprovalGrant(r.Command.Workspace.Root, spec, "run_command")
	request := CommandApprovalRequest{
		SessionID: r.SessionID,
		Tool:      "run_command",
		Command:   firstNonEmpty(result.Command, commandLabelForApproval(spec)),
		CWD:       firstNonEmpty(result.CWD, commandCWDForApproval(r.Command.Workspace.Root, spec)),
		Reason:    firstNonEmpty(result.DenialReason, result.Error),
		Scope:     grant.ScopeText(),
	}
	decision, err := r.Approvals.RequestCommandApproval(ctx, request)
	if err != nil {
		return result
	}
	switch decision {
	case DecisionAccept:
		return r.runCommandAtMedium(ctx, spec)
	case DecisionAcceptForSession:
		r.promoteCommandAutonomyForSession(grant)
		return r.runCommandAtMedium(ctx, spec)
	default:
		return result
	}
}

func (r *Runner) runCommandAtMedium(ctx context.Context, spec tools.CommandSpec) tools.ToolResult {
	approved := r.Command
	approved.Workspace.Auto = policy.AutonomyMedium
	return approved.RunSpec(ctx, spec)
}

func (r *Runner) runProcessWithApproval(ctx context.Context, request ProcessRequest, spec tools.CommandSpec, tool string) tools.ToolResult {
	if r == nil {
		return tools.ToolResult{Success: false, Error: "runner unavailable"}
	}
	if r.Command.Workspace.Auto == policy.AutonomyLow {
		if !r.processApprovalGranted(ctx, spec, tool) {
			result := tools.ToolResult{
				Success:      false,
				Denied:       true,
				DenialReason: "managed background process requires approval at low autonomy",
				Error:        "managed background process requires approval at low autonomy",
				Command:      commandLabelForApproval(spec),
				CWD:          commandCWDForApproval(r.Command.Workspace.Root, spec),
			}
			_ = r.writeOperationalAction(request, result)
			return result
		}
	}
	return r.runProcess(ctx, request)
}

func (r *Runner) runProcess(ctx context.Context, request ProcessRequest) tools.ToolResult {
	if r == nil || r.Processes == nil {
		result := tools.ToolResult{Success: false, Error: "managed background process tools are unavailable outside an embedded LCR session"}
		if r != nil {
			_ = r.writeOperationalAction(request, result)
		}
		return result
	}
	request.SessionID = firstNonEmpty(strings.TrimSpace(request.SessionID), r.SessionID)
	result, err := r.Processes.RequestProcess(ctx, request)
	if err != nil {
		result = tools.ToolResult{Success: false, Error: err.Error()}
	}
	_ = r.writeOperationalAction(request, result)
	return result
}

func (r *Runner) writeOperationalAction(request ProcessRequest, result tools.ToolResult) error {
	if r == nil {
		return nil
	}
	processID := strings.TrimSpace(request.ProcessID)
	if processID == "" && result.ManagedProcess != nil {
		processID = strings.TrimSpace(result.ManagedProcess.ProcessID)
	}
	name := strings.TrimSpace(request.Name)
	if name == "" && result.ManagedProcess != nil {
		name = strings.TrimSpace(result.ManagedProcess.Name)
	}
	action := OperationalAction{
		Action:                   strings.TrimSpace(string(request.Action)),
		ProcessID:                processID,
		Name:                     name,
		Command:                  strings.TrimSpace(firstNonEmpty(result.Command, request.Command)),
		CWD:                      strings.TrimSpace(firstNonEmpty(result.CWD, request.CWD)),
		Success:                  result.Success,
		Denied:                   result.Denied,
		Error:                    strings.TrimSpace(result.Error),
		VerificationChecksBefore: len(r.verificationChecks),
	}
	r.operationalActions = append(r.operationalActions, action)
	if r.Session == nil {
		return nil
	}
	event := session.Event{
		"type":       "operational_action",
		"session_id": r.SessionID,
		"action":     action.Action,
		"process_id": action.ProcessID,
		"name":       action.Name,
		"command":    action.Command,
		"cwd":        action.CWD,
		"success":    action.Success,
		"denied":     action.Denied,
		"error":      action.Error,
	}
	if result.ExitCode != 0 {
		event["exit_code"] = result.ExitCode
	}
	if artifactPath := strings.TrimSpace(result.ArtifactPath); artifactPath != "" {
		event["artifact_path"] = artifactPath
	}
	if result.ManagedProcess != nil {
		event["managed_process"] = result.ManagedProcess
	}
	if len(result.ManagedProcesses) > 0 {
		event["managed_processes"] = result.ManagedProcesses
	}
	return r.Session.Write(event)
}

func (r *Runner) processApprovalGranted(ctx context.Context, spec tools.CommandSpec, tool string) bool {
	if r == nil || r.Approvals == nil {
		return false
	}
	if r.commandApprovalGranted(spec, tool) {
		return true
	}
	grant := newCommandApprovalGrant(r.Command.Workspace.Root, spec, tool)
	request := CommandApprovalRequest{
		SessionID: r.SessionID,
		Tool:      firstNonEmpty(strings.TrimSpace(tool), "start_process"),
		Command:   firstNonEmpty(commandLabelForApproval(spec), "managed process"),
		CWD:       firstNonEmpty(commandCWDForApproval(r.Command.Workspace.Root, spec), r.Command.Workspace.Root),
		Reason:    "managed background process requires approval at low autonomy",
		Scope:     grant.ScopeText(),
	}
	decision, err := r.Approvals.RequestCommandApproval(ctx, request)
	if err != nil {
		return false
	}
	switch decision {
	case DecisionAccept:
		return true
	case DecisionAcceptForSession:
		r.promoteCommandAutonomyForSession(grant)
		return true
	default:
		return false
	}
}

func (r *Runner) promoteCommandAutonomyForSession(grant commandApprovalGrant) {
	if r == nil {
		return
	}
	if r.Command.Workspace.Auto == policy.AutonomyMedium {
		return
	}
	r.Command.Workspace.Auto = policy.AutonomyMedium
	r.commandApprovalGrants = append(r.commandApprovalGrants, grant)
	if r.Session != nil {
		_ = r.Session.Write(session.Event{
			"type":       "permission_level_changed",
			"session_id": r.SessionID,
			"from":       string(policy.AutonomyLow),
			"to":         string(policy.AutonomyMedium),
			"reason":     "approval accepted for session",
		})
	}
}

func (r *Runner) commandApprovalGranted(spec tools.CommandSpec, tool string) bool {
	if r == nil || len(r.commandApprovalGrants) == 0 {
		return false
	}
	for _, grant := range r.commandApprovalGrants {
		if grant.Matches(r.Command.Workspace.Root, spec, tool) {
			return true
		}
	}
	return false
}

func commandLabelForApproval(spec tools.CommandSpec) string {
	if len(spec.Argv) > 0 {
		return strings.Join(spec.Argv, " ")
	}
	return strings.TrimSpace(spec.Command)
}

func commandCWDForApproval(root string, spec tools.CommandSpec) string {
	cwd := strings.TrimSpace(spec.CWD)
	if cwd == "" {
		return root
	}
	if filepath.IsAbs(cwd) {
		return filepath.Clean(cwd)
	}
	return filepath.Clean(filepath.Join(root, cwd))
}

type commandApprovalGrant struct {
	Tool           string
	Command        string
	CWD            string
	Scope          string
	PackageManager string
}

const (
	commandApprovalScopeExact       = "exact_command"
	commandApprovalScopePackageDeps = "package_dependency_family"
)

func newCommandApprovalGrant(root string, spec tools.CommandSpec, tool string) commandApprovalGrant {
	grant := commandApprovalGrant{
		Tool:    firstNonEmpty(strings.TrimSpace(tool), "run_command"),
		Command: commandLabelForApproval(spec),
		CWD:     commandCWDForApproval(root, spec),
		Scope:   commandApprovalScopeExact,
	}
	if manager, ok := packageManagerDependencyCommand(spec); ok {
		grant.Scope = commandApprovalScopePackageDeps
		grant.PackageManager = manager
	}
	return grant
}

func (g commandApprovalGrant) Matches(root string, spec tools.CommandSpec, tool string) bool {
	next := newCommandApprovalGrant(root, spec, tool)
	if g.Tool != "" && next.Tool != "" && g.Tool != next.Tool {
		return false
	}
	if g.CWD != next.CWD {
		return false
	}
	if g.Scope == commandApprovalScopePackageDeps && next.Scope == commandApprovalScopePackageDeps {
		return g.PackageManager == next.PackageManager
	}
	return g.Scope == commandApprovalScopeExact && g.Command == next.Command
}

func (g commandApprovalGrant) ScopeText() string {
	switch g.Scope {
	case commandApprovalScopePackageDeps:
		return strings.TrimSpace(strings.Join(nonEmptyStrings([]string{
			g.PackageManager,
			"dependency commands in",
			g.CWD,
		}), " "))
	default:
		if g.CWD != "" {
			return "this exact command in " + g.CWD
		}
		return "this exact command"
	}
}

func packageManagerDependencyCommand(spec tools.CommandSpec) (string, bool) {
	argv := cleanCommandArgv(spec.Argv)
	if len(argv) == 0 {
		if strings.ContainsAny(spec.Command, "|;&<>$`") {
			return "", false
		}
		argv = simpleCommandFields(spec.Command)
	}
	if len(argv) < 2 {
		return "", false
	}
	manager := strings.ToLower(filepath.Base(argv[0]))
	subcommand := strings.ToLower(strings.TrimSpace(argv[1]))
	switch manager {
	case "npm":
		switch subcommand {
		case "install", "i", "add", "uninstall", "remove", "rm", "update", "upgrade", "ci", "dedupe", "prune":
			return manager, true
		}
	case "pnpm":
		switch subcommand {
		case "install", "i", "add", "remove", "rm", "update", "upgrade", "up", "import", "dedupe", "prune":
			return manager, true
		}
	case "yarn":
		switch subcommand {
		case "install", "add", "remove", "upgrade", "up", "dedupe":
			return manager, true
		}
	case "bun":
		switch subcommand {
		case "install", "i", "add", "remove", "rm", "update", "upgrade":
			return manager, true
		}
	}
	return "", false
}

func cleanCommandArgv(argv []string) []string {
	out := make([]string, 0, len(argv))
	for _, value := range argv {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func simpleCommandFields(command string) []string {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	command = normalizedVerificationCommand(command)
	if strings.ContainsAny(command, "|;&<>$`") {
		return nil
	}
	return strings.Fields(command)
}

func (r *Runner) packageScriptCWDHint(result tools.ToolResult) string {
	if r == nil || r.Command.Workspace.Root == "" || result.Success {
		return ""
	}
	if !strings.EqualFold(result.Purpose, tools.CommandPurposeVerify) {
		return ""
	}
	scriptName := packageScriptNameFromResult(result)
	if scriptName == "" || !resultLooksLikeMissingPackageScript(result, scriptName) {
		return ""
	}
	dirs := packageScriptDirs(r.Command.Workspace.Root, scriptName, result.CWD)
	if len(dirs) == 0 {
		return ""
	}
	dir := dirs[0]
	packagePath := filepath.ToSlash(filepath.Join(dir, "package.json"))
	return fmt.Sprintf("Try rerunning with run_command cwd set to %q because %s defines script %q.", dir, packagePath, scriptName)
}

func packageScriptNameFromResult(result tools.ToolResult) string {
	if scriptName := packageScriptNameFromArgv(cleanCommandArgv(result.Argv)); scriptName != "" {
		return scriptName
	}
	return packageScriptNameFromArgv(simpleCommandFields(result.Command))
}

func packageScriptNameFromArgv(argv []string) string {
	if len(argv) < 2 {
		return ""
	}
	manager := strings.ToLower(filepath.Base(argv[0]))
	subcommand := strings.ToLower(strings.TrimSpace(argv[1]))
	switch manager {
	case "npm", "pnpm":
		if len(argv) >= 3 && (subcommand == "run" || subcommand == "run-script") {
			return strings.TrimSpace(argv[2])
		}
		if manager == "npm" && subcommand == "test" {
			return "test"
		}
	case "yarn":
		if subcommand == "run" && len(argv) >= 3 {
			return strings.TrimSpace(argv[2])
		}
		if packageScriptNameLooksLikeScript(subcommand) {
			return strings.TrimSpace(argv[1])
		}
	case "bun":
		if subcommand == "run" && len(argv) >= 3 {
			return strings.TrimSpace(argv[2])
		}
	}
	return ""
}

func packageScriptNameLooksLikeScript(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") {
		return false
	}
	switch strings.ToLower(value) {
	case "install", "add", "remove", "rm", "update", "upgrade", "up", "exec", "dlx", "publish", "node":
		return false
	default:
		return true
	}
}

func resultLooksLikeMissingPackageScript(result tools.ToolResult, scriptName string) bool {
	text := strings.ToLower(result.Output + "\n" + result.Error)
	scriptName = strings.ToLower(strings.TrimSpace(scriptName))
	if text == "" || scriptName == "" {
		return false
	}
	markers := []string{
		"missing script",
		"no script named " + scriptName,
		"couldn't find a script named",
		"could not find a script named",
		"script not found",
		"err_pnpm_no_script",
	}
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func packageScriptDirs(root, scriptName, currentCWD string) []string {
	root = filepath.Clean(root)
	currentCWD = filepath.Clean(firstNonEmpty(currentCWD, root))
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if path != root {
				name := entry.Name()
				if name == "node_modules" || name == ".git" || name == "dist" || name == "build" || strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				rel, relErr := filepath.Rel(root, path)
				if relErr == nil && pathDepth(rel) > 3 {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if entry.Name() != "package.json" {
			return nil
		}
		dir := filepath.Dir(path)
		if filepath.Clean(dir) == currentCWD {
			return nil
		}
		if packageJSONDefinesScript(path, scriptName) {
			if rel, relErr := filepath.Rel(root, dir); relErr == nil && rel != "." {
				dirs = append(dirs, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	return dirs
}

func packageJSONDefinesScript(path, scriptName string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]json.RawMessage `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	_, ok := pkg.Scripts[scriptName]
	return ok
}

func pathDepth(rel string) int {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." || rel == "" {
		return 0
	}
	return strings.Count(rel, "/") + 1
}

func (r *Runner) runSearch(ctx context.Context, args searchArgs) (tools.ToolResult, error) {
	contextBefore := 1
	contextAfter := 2
	outputMode := strings.ToLower(strings.TrimSpace(args.OutputMode))
	if outputMode == "compact" || outputMode == "summary" {
		contextBefore = 0
		contextAfter = 0
	}
	if args.ContextBefore != nil {
		contextBefore = *args.ContextBefore
	}
	if args.ContextAfter != nil {
		contextAfter = *args.ContextAfter
	}
	result := r.Files.SearchContextWithOptions(args.Query, args.Path, args.FileGlob, args.MaxMatches, contextBefore, contextAfter, tools.SearchOptions{
		IncludeHidden: args.IncludeHidden,
		OutputMode:    outputMode,
		Intent:        args.Intent,
	})
	if !result.Success {
		return result, nil
	}
	refined, err := r.maybeRefineSearch(ctx, args, result, outputMode)
	if err != nil {
		return tools.ToolResult{}, err
	}
	return refined, nil
}

func (r *Runner) maybeRefineSearch(ctx context.Context, args searchArgs, result tools.ToolResult, outputMode string) (tools.ToolResult, error) {
	if r.SearchRefiner == nil {
		return result, nil
	}
	minBytes := r.SearchRefineMinBytes
	if minBytes <= 0 {
		minBytes = DefaultSearchRefineMinBytes
	}
	originalBytes := len(result.Output)
	if originalBytes < minBytes {
		return result, nil
	}

	compact := result
	normalizedMode := strings.ToLower(strings.TrimSpace(outputMode))
	if normalizedMode != "compact" && normalizedMode != "summary" {
		compact = r.Files.SearchContextWithOptions(args.Query, args.Path, args.FileGlob, args.MaxMatches, 0, 0, tools.SearchOptions{
			IncludeHidden: args.IncludeHidden,
			OutputMode:    "compact",
			Intent:        args.Intent,
		})
		if !compact.Success {
			return result, nil
		}
	}
	compactBytes := len(compact.Output)
	if compactBytes < minBytes/2 {
		compact.Output = annotateSearchOutput(compact.Output,
			"search_compacted: true",
			fmt.Sprintf("original_output_bytes: %d", originalBytes),
			fmt.Sprintf("compact_output_bytes: %d", compactBytes),
			"compact_note: The original contextual search result was large; this compact result omits per-match context. Use read_file on relevant lines before making final claims.",
		)
		compact.Truncated = true
		return compact, nil
	}

	if err := r.writeSearchRefineEvent(args, originalBytes, compactBytes); err != nil {
		return tools.ToolResult{}, err
	}
	refined, err := r.SearchRefiner.RefineSearch(ctx, SearchRefineRequest{
		Query:               args.Query,
		Intent:              args.Intent,
		Path:                args.Path,
		FileGlob:            args.FileGlob,
		MaxMatches:          args.MaxMatches,
		SearchOutput:        compact.Output,
		OriginalOutputBytes: originalBytes,
		CompactOutputBytes:  compactBytes,
		Truncated:           result.Truncated || compact.Truncated,
	})
	if err != nil || strings.TrimSpace(refined.Output) == "" {
		message := "search refinement failed"
		if err != nil {
			message = err.Error()
		}
		if writeErr := r.writeSearchRefineResultEvent(false, message, refined); writeErr != nil {
			return tools.ToolResult{}, writeErr
		}
		compact.Output = annotateSearchOutput(compact.Output,
			"search_refine_failed: true",
			fmt.Sprintf("original_output_bytes: %d", originalBytes),
			fmt.Sprintf("compact_output_bytes: %d", compactBytes),
			"refine_note: Returning compact matches because the utility refiner was unavailable. Use read_file on relevant lines before making final claims.",
		)
		compact.Truncated = true
		return compact, nil
	}
	if err := r.writeUtilityModelResponseEvent("search_refine", refined); err != nil {
		return tools.ToolResult{}, err
	}
	if err := r.writeSearchRefineResultEvent(true, "", refined); err != nil {
		return tools.ToolResult{}, err
	}
	result.Output = strings.TrimSpace(refined.Output) + "\n"
	result.Truncated = true
	return result, nil
}

func (r *Runner) runScoutFiles(ctx context.Context, args scoutFilesArgs) (tools.ToolResult, error) {
	question := strings.TrimSpace(args.Question)
	if question == "" {
		return tools.ToolResult{Success: false, Error: "question is required"}, nil
	}
	pack := r.Files.ScoutPack(args.Path, tools.ScoutPackOptions{
		FileGlob:        args.FileGlob,
		MaxFiles:        args.MaxFiles,
		MaxLinesPerFile: args.MaxLinesPerFile,
		IncludeHidden:   args.IncludeHidden,
		Question:        question,
	})
	if !pack.Success {
		return pack, nil
	}
	if r.CodeScout == nil {
		pack.Output = annotateSearchOutput(pack.Output,
			"scout_model_unavailable: true",
			"scout_note: Returning the deterministic file pack because no utility scout model is configured. Use file_outline, search, or read_file to inspect likely relevant files.",
		)
		pack.Truncated = true
		return pack, nil
	}
	if err := r.writeScoutFilesEvent(args, len(pack.Output), pack.Truncated); err != nil {
		return tools.ToolResult{}, err
	}
	scouted, err := r.CodeScout.ScoutFiles(ctx, ScoutFilesRequest{
		Question:        question,
		Path:            args.Path,
		FileGlob:        args.FileGlob,
		MaxFiles:        args.MaxFiles,
		MaxLinesPerFile: args.MaxLinesPerFile,
		FilePack:        pack.Output,
		PackBytes:       len(pack.Output),
		Truncated:       pack.Truncated,
	})
	if err != nil || strings.TrimSpace(scouted.Output) == "" {
		message := "scout failed"
		if err != nil {
			message = err.Error()
		}
		if writeErr := r.writeScoutFilesResultEvent(false, message, scouted); writeErr != nil {
			return tools.ToolResult{}, writeErr
		}
		pack.Output = annotateSearchOutput(pack.Output,
			"scout_failed: true",
			"scout_note: Returning the deterministic file pack because the utility scout was unavailable. Use read_file on relevant lines before making final claims.",
		)
		pack.Truncated = true
		return pack, nil
	}
	if err := r.writeUtilityModelResponseEvent("scout_files", ScoutFilesResult{
		Provider:     scouted.Provider,
		Model:        scouted.Model,
		Usage:        scouted.Usage,
		UsageSummary: scouted.UsageSummary,
	}); err != nil {
		return tools.ToolResult{}, err
	}
	if err := r.writeScoutFilesResultEvent(true, "", scouted); err != nil {
		return tools.ToolResult{}, err
	}
	return tools.ToolResult{Success: true, Output: strings.TrimSpace(scouted.Output) + "\n", Truncated: true}, nil
}

func (r *Runner) runConsultCritic(ctx context.Context, args consultCriticArgs) (tools.ToolResult, error) {
	if r.CriticConsultant == nil {
		return tools.ToolResult{Success: false, Error: "consult_critic is not available for this LCAgent run"}, nil
	}
	question, questionTruncated := boundedCriticConsultText(args.Question, maxCriticConsultQuestionChars)
	if question == "" {
		return tools.ToolResult{Success: false, Error: "consult_critic question is required"}, nil
	}
	contextText, contextTruncated := boundedCriticConsultText(args.Context, maxCriticConsultContextChars)
	candidate, candidateTruncated := boundedCriticConsultText(args.Candidate, maxCriticConsultCandidateChars)
	files, filesTruncated, failure := r.criticConsultFiles(args.Files)
	if failure.Error != "" {
		return failure, nil
	}
	request := CriticConsultRequest{
		SessionID:   r.SessionID,
		UserRequest: strings.TrimSpace(r.Prompt),
		Kind:        normalizeCriticConsultKind(args.Kind),
		Question:    question,
		Context:     contextText,
		Candidate:   candidate,
		Checks:      cleanCriticConsultChecks(args.Checks),
		Files:       files,
	}
	inputTruncated := questionTruncated || contextTruncated || candidateTruncated || filesTruncated
	if err := r.writeCriticConsultStartedEvent(request, inputTruncated); err != nil {
		return tools.ToolResult{}, err
	}
	consulted, err := r.CriticConsultant.ConsultCritic(ctx, request)
	if err != nil {
		message := err.Error()
		failureKind := ""
		if strings.Contains(message, "invalid JSON") {
			failureKind = "invalid_json"
		}
		if writeErr := r.writeCriticConsultFailedEvent(request, message, failureKind); writeErr != nil {
			return tools.ToolResult{}, writeErr
		}
		return tools.ToolResult{Success: false, Error: "critic consultation failed: " + message}, nil
	}
	if err := r.writeCriticConsultResultEvent(request, consulted); err != nil {
		return tools.ToolResult{}, err
	}
	return tools.ToolResult{Success: true, Output: formatCriticConsultResult(consulted), Truncated: inputTruncated}, nil
}

func (r *Runner) runAnalyzeImage(ctx context.Context, args analyzeImageArgs) (tools.ToolResult, error) {
	if r.ImageAnalyzer == nil {
		return tools.ToolResult{Success: false, Error: "analyze_image is not available for this LCAgent run"}, nil
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		return tools.ToolResult{Success: false, Error: "analyze_image path is required"}, nil
	}
	comparisonPath := strings.TrimSpace(args.ComparisonPath)
	question, questionTruncated := boundedCriticConsultText(args.Question, maxImageAnalysisQuestionChars)
	if question == "" {
		return tools.ToolResult{Success: false, Error: "analyze_image question is required"}, nil
	}
	contextText, contextTruncated := boundedCriticConsultText(args.Context, maxImageAnalysisContextChars)
	checks := cleanImageAnalysisChecks(args.Checks)
	request := ImageAnalysisRequest{
		SessionID:      r.SessionID,
		UserRequest:    strings.TrimSpace(r.Prompt),
		Path:           path,
		ComparisonPath: comparisonPath,
		Question:       question,
		Context:        contextText,
		Checks:         checks,
	}
	inputTruncated := questionTruncated || contextTruncated || len(args.Checks) > len(checks)
	if err := r.writeImageAnalysisStartedEvent(request, inputTruncated); err != nil {
		return tools.ToolResult{}, err
	}
	analyzed, err := r.ImageAnalyzer.AnalyzeImage(ctx, request)
	if err != nil {
		message := err.Error()
		if writeErr := r.writeImageAnalysisFailedEvent(request, message); writeErr != nil {
			return tools.ToolResult{}, writeErr
		}
		return tools.ToolResult{Success: false, Error: "image analysis failed: " + message}, nil
	}
	if err := r.writeImageAnalysisResultEvent(request, analyzed); err != nil {
		return tools.ToolResult{}, err
	}
	if comparisonPath != "" {
		r.temporalImageAnalyses++
	}
	return tools.ToolResult{Success: true, Output: formatImageAnalysisResult(analyzed), Truncated: inputTruncated}, nil
}

func (r *Runner) criticConsultFiles(files []consultCriticFileArgs) ([]CriticConsultFile, bool, tools.ToolResult) {
	if len(files) == 0 {
		return nil, false, tools.ToolResult{}
	}
	truncated := false
	if len(files) > maxCriticConsultFiles {
		files = files[:maxCriticConsultFiles]
		truncated = true
	}
	out := make([]CriticConsultFile, 0, len(files))
	totalChars := 0
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			return nil, truncated, tools.ToolResult{Success: false, Error: "consult_critic file path is required"}
		}
		startLine := file.StartLine
		if startLine <= 0 {
			startLine = 1
		}
		limit := defaultCriticConsultLinesPerFile
		endLine := file.EndLine
		if endLine > 0 {
			if endLine < startLine {
				return nil, truncated, tools.ToolResult{Success: false, Error: fmt.Sprintf("consult_critic file range for %s has end_line before start_line", path)}
			}
			limit = endLine - startLine + 1
		}
		if limit > maxCriticConsultLinesPerFile {
			limit = maxCriticConsultLinesPerFile
			endLine = startLine + limit - 1
			truncated = true
		}
		result := r.Files.Read(path, startLine, limit)
		if !result.Success {
			return nil, truncated, tools.ToolResult{Success: false, Error: fmt.Sprintf("consult_critic could not read %s: %s", path, result.Error)}
		}
		excerpt, excerptTruncated := boundedCriticConsultText(result.Output, maxCriticConsultFileChars)
		if excerptTruncated || result.Truncated {
			truncated = true
		}
		excerptChars := len([]rune(excerpt))
		if totalChars+excerptChars > maxCriticConsultTotalFileChars {
			remaining := maxCriticConsultTotalFileChars - totalChars
			if remaining < 64 {
				truncated = true
				break
			}
			excerpt, _ = boundedCriticConsultText(excerpt, remaining)
			excerptChars = len([]rune(excerpt))
			truncated = true
		}
		totalChars += excerptChars
		out = append(out, CriticConsultFile{
			Path:      path,
			StartLine: startLine,
			EndLine:   endLine,
			Role:      strings.TrimSpace(file.Role),
			Excerpt:   excerpt,
			Truncated: excerptTruncated || result.Truncated,
		})
	}
	return out, truncated, tools.ToolResult{}
}

func normalizeCriticConsultKind(kind string) string {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(kind), "-", "_")) {
	case "plan", "code", "patch", "debug", "final_claims", "other":
		return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(kind), "-", "_"))
	default:
		return "other"
	}
}

func cleanCriticConsultChecks(checks []string) []string {
	out := make([]string, 0, len(checks))
	seen := map[string]struct{}{}
	for _, check := range checks {
		check, _ = boundedCriticConsultText(check, 80)
		if check == "" {
			continue
		}
		key := strings.ToLower(check)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, check)
		if len(out) >= 8 {
			break
		}
	}
	return out
}

func cleanImageAnalysisChecks(checks []string) []string {
	out := make([]string, 0, len(checks))
	seen := map[string]struct{}{}
	for _, check := range checks {
		check, _ = boundedCriticConsultText(check, 100)
		if check == "" {
			continue
		}
		key := strings.ToLower(check)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, check)
		if len(out) >= maxImageAnalysisChecks {
			break
		}
	}
	return out
}

func normalizeQualityPlan(plan QualityPlan) (QualityPlan, tools.ToolResult) {
	plan.ArtifactType = normalizeQualityPlanArtifactType(plan.ArtifactType)
	if plan.RequiresTemporalVisualVerification {
		plan.RequiresVisualVerification = true
	}
	if qualityPlanArtifactRequiresVisualVerification(plan.ArtifactType) {
		plan.RequiresVisualVerification = true
	}
	plan.Notes, _ = boundedCriticConsultText(plan.Notes, maxQualityPlanTextChars)
	if len(plan.Phases) == 0 {
		return QualityPlan{}, tools.ToolResult{Success: false, Error: "update_quality_plan requires at least one phase"}
	}
	if len(plan.Phases) > maxQualityPlanPhases {
		plan.Phases = plan.Phases[:maxQualityPlanPhases]
	}
	out := make([]QualityPlanPhase, 0, len(plan.Phases))
	for _, phase := range plan.Phases {
		phase.Name, _ = boundedCriticConsultText(phase.Name, maxQualityPlanTextChars)
		if phase.Name == "" {
			return QualityPlan{}, tools.ToolResult{Success: false, Error: "update_quality_plan phase name is required"}
		}
		phase.Status = normalizeQualityPlanPhaseStatus(phase.Status)
		if phase.Status == "" {
			phase.Status = "planned"
		}
		phase.Acceptance = cleanQualityPlanTextList(phase.Acceptance, maxQualityPlanAcceptanceItems)
		phase.Evidence = cleanQualityPlanTextList(phase.Evidence, maxQualityPlanEvidenceItems)
		phase.Notes, _ = boundedCriticConsultText(phase.Notes, maxQualityPlanTextChars)
		out = append(out, phase)
	}
	plan.Phases = out
	return plan, tools.ToolResult{Success: true}
}

func normalizeQualityPlanArtifactType(value string) string {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", "_")) {
	case "game", "ui", "cli", "library", "doc", "other", "unknown":
		return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", "_"))
	default:
		return "unknown"
	}
}

func qualityPlanArtifactRequiresVisualVerification(value string) bool {
	switch normalizeQualityPlanArtifactType(value) {
	case "game", "ui":
		return true
	default:
		return false
	}
}

func normalizeQualityPlanPhaseStatus(value string) string {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", "_")) {
	case "planned", "in_progress", "implemented", "verified", "needs_repair", "skipped":
		return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", "_"))
	default:
		return ""
	}
}

func cleanQualityPlanTextList(values []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	capacity := len(values)
	if capacity > limit {
		capacity = limit
	}
	out := make([]string, 0, capacity)
	seen := map[string]struct{}{}
	for _, value := range values {
		value, _ = boundedCriticConsultText(value, maxQualityPlanTextChars)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func formatQualityPlanResult(plan QualityPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "quality_plan: %s\n", plan.ArtifactType)
	fmt.Fprintf(&b, "requires_runtime_verification: %t\n", plan.RequiresRuntimeVerification)
	fmt.Fprintf(&b, "requires_visual_verification: %t\n", plan.RequiresVisualVerification)
	fmt.Fprintf(&b, "requires_temporal_visual_verification: %t\n", plan.RequiresTemporalVisualVerification)
	b.WriteString("phases:\n")
	for _, phase := range plan.Phases {
		fmt.Fprintf(&b, "- [%s] %s\n", firstNonEmpty(phase.Status, "planned"), phase.Name)
		if len(phase.Evidence) > 0 {
			fmt.Fprintf(&b, "  evidence: %s\n", strings.Join(phase.Evidence, "; "))
		}
	}
	return b.String()
}

func boundedCriticConsultText(value string, limit int) (string, bool) {
	value = strings.TrimSpace(value)
	if limit <= 0 || value == "" {
		return "", value != ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value, false
	}
	if limit <= 24 {
		return string(runes[:limit]), true
	}
	return strings.TrimSpace(string(runes[:limit-24])) + "\n...[truncated]...", true
}

func formatImageAnalysisResult(result ImageAnalysisResult) string {
	output := strings.TrimSpace(result.Output)
	if output == "" {
		output = "vision model returned no substantive image analysis"
	}
	return "image_analysis:\n" + output + "\n"
}

func formatCriticConsultResult(result CriticConsultResult) string {
	status := strings.TrimSpace(result.Status)
	if status == "" {
		status = "concerns"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "critic_consultation: %s\n", status)
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		fmt.Fprintf(&b, "summary: %s\n", summary)
	}
	if instruction := strings.TrimSpace(result.LeadInstruction); instruction != "" {
		fmt.Fprintf(&b, "suggested_next_step: %s\n", instruction)
	}
	if len(result.Findings) > 0 {
		b.WriteString("findings:\n")
		for _, finding := range result.Findings {
			claim := strings.TrimSpace(finding.Claim)
			if claim == "" {
				claim = strings.TrimSpace(finding.Evidence)
			}
			if claim == "" {
				continue
			}
			severity := firstNonEmpty(strings.TrimSpace(finding.Severity), "medium")
			materiality := strings.TrimSpace(finding.Materiality)
			if materiality != "" {
				fmt.Fprintf(&b, "- [%s/%s] %s\n", severity, materiality, claim)
			} else {
				fmt.Fprintf(&b, "- [%s] %s\n", severity, claim)
			}
			if evidence := strings.TrimSpace(finding.Evidence); evidence != "" {
				fmt.Fprintf(&b, "  evidence: %s\n", evidence)
			}
			if followup := strings.TrimSpace(finding.SuggestedFollowup); followup != "" {
				fmt.Fprintf(&b, "  suggested_followup: %s\n", followup)
			}
		}
	}
	if strings.TrimSpace(result.Summary) == "" && len(result.Findings) == 0 {
		b.WriteString("summary: critic returned no substantive advisory content\n")
	}
	return b.String()
}

func (r *Runner) writeCriticConsultStartedEvent(request CriticConsultRequest, inputTruncated bool) error {
	if r == nil || r.Session == nil {
		return nil
	}
	return r.Session.Write(session.Event{
		"type":            "critic_consult_started",
		"session_id":      r.SessionID,
		"kind":            request.Kind,
		"question":        request.Question,
		"checks":          request.Checks,
		"file_count":      len(request.Files),
		"input_truncated": inputTruncated,
	})
}

func (r *Runner) writeCriticConsultResultEvent(request CriticConsultRequest, result CriticConsultResult) error {
	if r == nil || r.Session == nil {
		return nil
	}
	return r.Session.Write(session.Event{
		"type":                  "critic_consult_result",
		"session_id":            r.SessionID,
		"kind":                  request.Kind,
		"question":              request.Question,
		"provider":              strings.TrimSpace(result.Provider),
		"model":                 strings.TrimSpace(result.Model),
		"packet_hash":           strings.TrimSpace(result.PacketHash),
		"status":                strings.TrimSpace(result.Status),
		"summary":               strings.TrimSpace(result.Summary),
		"findings":              result.Findings,
		"lead_instruction":      strings.TrimSpace(result.LeadInstruction),
		"human_prompt":          strings.TrimSpace(result.HumanPrompt),
		"proposed_user_message": strings.TrimSpace(result.ProposedUserMessage),
		"usage":                 json.RawMessage(result.Usage),
		"usage_summary":         result.UsageSummary,
	})
}

func (r *Runner) writeCriticConsultFailedEvent(request CriticConsultRequest, message, failureKind string) error {
	if r == nil || r.Session == nil {
		return nil
	}
	event := session.Event{
		"type":       "critic_consult_failed",
		"session_id": r.SessionID,
		"kind":       request.Kind,
		"question":   request.Question,
		"message":    strings.TrimSpace(message),
	}
	if strings.TrimSpace(failureKind) != "" {
		event["failure_kind"] = strings.TrimSpace(failureKind)
	}
	return r.Session.Write(event)
}

func (r *Runner) writeImageAnalysisStartedEvent(request ImageAnalysisRequest, inputTruncated bool) error {
	if r == nil || r.Session == nil {
		return nil
	}
	return r.Session.Write(session.Event{
		"type":            "image_analysis_started",
		"session_id":      r.SessionID,
		"path":            request.Path,
		"comparison_path": request.ComparisonPath,
		"temporal":        strings.TrimSpace(request.ComparisonPath) != "",
		"question":        request.Question,
		"checks":          request.Checks,
		"input_truncated": inputTruncated,
	})
}

func (r *Runner) writeImageAnalysisResultEvent(request ImageAnalysisRequest, result ImageAnalysisResult) error {
	if r == nil || r.Session == nil {
		return nil
	}
	return r.Session.Write(session.Event{
		"type":            "image_analysis_result",
		"session_id":      r.SessionID,
		"path":            request.Path,
		"comparison_path": request.ComparisonPath,
		"temporal":        strings.TrimSpace(request.ComparisonPath) != "",
		"question":        request.Question,
		"provider":        strings.TrimSpace(result.Provider),
		"model":           strings.TrimSpace(result.Model),
		"output":          strings.TrimSpace(result.Output),
		"usage":           json.RawMessage(result.Usage),
		"usage_summary":   result.UsageSummary,
	})
}

func (r *Runner) writeImageAnalysisFailedEvent(request ImageAnalysisRequest, message string) error {
	if r == nil || r.Session == nil {
		return nil
	}
	return r.Session.Write(session.Event{
		"type":            "image_analysis_failed",
		"session_id":      r.SessionID,
		"path":            request.Path,
		"comparison_path": request.ComparisonPath,
		"temporal":        strings.TrimSpace(request.ComparisonPath) != "",
		"question":        request.Question,
		"message":         strings.TrimSpace(message),
	})
}

func (r *Runner) writePhaseWriteGateResultEvent(request PhaseWriteGateRequest, result PhaseWriteGateResult) error {
	if r == nil || r.Session == nil {
		return nil
	}
	return r.Session.Write(session.Event{
		"type":                      "phase_write_gate_result",
		"session_id":                r.SessionID,
		"tool":                      request.Tool,
		"active_phase_index":        request.ActivePhaseIndex,
		"active_phase":              strings.TrimSpace(request.ActivePhase.Name),
		"allow":                     result.Allow,
		"fits_active_phase":         result.FitsActivePhase,
		"contains_later_phase_work": result.ContainsLaterPhaseWork,
		"too_much_at_once":          result.TooMuchAtOnce,
		"reason":                    strings.TrimSpace(result.Reason),
		"suggested_smaller_slice":   strings.TrimSpace(result.SuggestedSmallerSlice),
		"provider":                  strings.TrimSpace(result.Provider),
		"model":                     strings.TrimSpace(result.Model),
		"usage":                     json.RawMessage(result.Usage),
		"usage_summary":             result.UsageSummary,
		"input_truncated":           request.ToolArgsTruncated,
	})
}

func (r *Runner) writePhaseWriteGateFailedEvent(request PhaseWriteGateRequest, err error) error {
	if r == nil || r.Session == nil {
		return nil
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	return r.Session.Write(session.Event{
		"type":               "phase_write_gate_failed",
		"session_id":         r.SessionID,
		"tool":               request.Tool,
		"active_phase_index": request.ActivePhaseIndex,
		"active_phase":       strings.TrimSpace(request.ActivePhase.Name),
		"message":            strings.TrimSpace(message),
		"fail_open":          true,
	})
}

func annotateSearchOutput(output string, headerLines ...string) string {
	var b strings.Builder
	for _, line := range headerLines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(strings.TrimSpace(output))
	b.WriteByte('\n')
	return b.String()
}

func (r *Runner) writeSearchRefineEvent(args searchArgs, originalBytes, compactBytes int) error {
	if r == nil || r.Session == nil {
		return nil
	}
	return r.Session.Write(session.Event{
		"type":                  "search_refine",
		"session_id":            r.SessionID,
		"query":                 strings.TrimSpace(args.Query),
		"intent":                strings.TrimSpace(args.Intent),
		"path":                  strings.TrimSpace(args.Path),
		"file_glob":             strings.TrimSpace(args.FileGlob),
		"original_output_bytes": originalBytes,
		"compact_output_bytes":  compactBytes,
	})
}

func (r *Runner) writeSearchRefineResultEvent(success bool, message string, result SearchRefineResult) error {
	if r == nil || r.Session == nil {
		return nil
	}
	return r.Session.Write(session.Event{
		"type":         "search_refine_result",
		"session_id":   r.SessionID,
		"success":      success,
		"message":      strings.TrimSpace(message),
		"provider":     strings.TrimSpace(result.Provider),
		"model":        strings.TrimSpace(result.Model),
		"output_bytes": len(result.Output),
	})
}

func (r *Runner) writeScoutFilesEvent(args scoutFilesArgs, packBytes int, truncated bool) error {
	if r == nil || r.Session == nil {
		return nil
	}
	return r.Session.Write(session.Event{
		"type":                "scout_files",
		"session_id":          r.SessionID,
		"question":            strings.TrimSpace(args.Question),
		"path":                strings.TrimSpace(args.Path),
		"file_glob":           strings.TrimSpace(args.FileGlob),
		"max_files":           args.MaxFiles,
		"max_lines_per_file":  args.MaxLinesPerFile,
		"file_pack_bytes":     packBytes,
		"file_pack_truncated": truncated,
		"include_hidden":      args.IncludeHidden,
	})
}

func (r *Runner) writeScoutFilesResultEvent(success bool, message string, result ScoutFilesResult) error {
	if r == nil || r.Session == nil {
		return nil
	}
	return r.Session.Write(session.Event{
		"type":         "scout_files_result",
		"session_id":   r.SessionID,
		"success":      success,
		"message":      strings.TrimSpace(message),
		"provider":     strings.TrimSpace(result.Provider),
		"model":        strings.TrimSpace(result.Model),
		"output_bytes": len(result.Output),
	})
}

type utilityModelEvent interface {
	ProviderName() string
	ModelName() string
	UsagePayload() json.RawMessage
	UsageSummaryPayload() lcrmodel.LLMUsage
}

func (r SearchRefineResult) ProviderName() string { return r.Provider }
func (r SearchRefineResult) ModelName() string    { return r.Model }
func (r SearchRefineResult) UsagePayload() json.RawMessage {
	return r.Usage
}
func (r SearchRefineResult) UsageSummaryPayload() lcrmodel.LLMUsage {
	return r.UsageSummary
}
func (r ScoutFilesResult) ProviderName() string { return r.Provider }
func (r ScoutFilesResult) ModelName() string    { return r.Model }
func (r ScoutFilesResult) UsagePayload() json.RawMessage {
	return r.Usage
}
func (r ScoutFilesResult) UsageSummaryPayload() lcrmodel.LLMUsage {
	return r.UsageSummary
}

func (r *Runner) writeUtilityModelResponseEvent(phase string, result utilityModelEvent) error {
	if r == nil || r.Session == nil {
		return nil
	}
	if strings.TrimSpace(phase) == "" {
		phase = "search_refine"
	}
	event := session.Event{
		"type":          "model_response",
		"session_id":    r.SessionID,
		"phase":         phase,
		"provider":      strings.TrimSpace(result.ProviderName()),
		"model":         strings.TrimSpace(result.ModelName()),
		"usage":         result.UsagePayload(),
		"usage_summary": result.UsageSummaryPayload(),
	}
	return r.Session.Write(event)
}

func (r *Runner) FilesTouched() []string {
	if r == nil {
		return nil
	}
	return append([]string(nil), r.filesTouched...)
}

func (r *Runner) FileTouchEvents() int {
	if r == nil {
		return 0
	}
	return r.fileTouchEvents
}

func (r *Runner) InspectionEvidenceEvents() int {
	if r == nil {
		return 0
	}
	return r.inspectionEvidence
}

func (r *Runner) VerificationDetails() []string {
	if r == nil || len(r.verificationChecks) == 0 {
		return nil
	}
	return formatVerificationChecks(r.verificationChecks, len(r.verificationChecks))
}

func (r *Runner) ChangeReviewEvidence() ChangeReviewEvidence {
	if r == nil {
		return ChangeReviewEvidence{}
	}
	evidence := ChangeReviewEvidence{
		FileTouchEvents: r.fileTouchEvents,
		FilesTouched:    r.FilesTouched(),
		PatchSummaries:  r.editSummaryCopies(),
		Verification:    append([]tools.VerificationCheck(nil), r.verificationChecks...),
		QualityPlan:     r.qualityPlanCopy(),
	}
	remainingChars := maxChangeReviewTotalChars
	for i, path := range evidence.FilesTouched {
		if i >= maxChangeReviewFiles {
			evidence.Truncated = true
			break
		}
		file := r.changeReviewFile(path, &remainingChars)
		if file.SnapshotTruncated {
			evidence.Truncated = true
		}
		evidence.Files = append(evidence.Files, file)
	}
	return evidence
}

func (r *Runner) recordEditSummary(summary *tools.PatchSummary) {
	if r == nil || summary == nil {
		return
	}
	r.editSummaries = append(r.editSummaries, copyPatchSummaryValue(*summary))
}

func (r *Runner) editSummaryCopies() []tools.PatchSummary {
	if r == nil || len(r.editSummaries) == 0 {
		return nil
	}
	out := make([]tools.PatchSummary, 0, len(r.editSummaries))
	for _, summary := range r.editSummaries {
		out = append(out, copyPatchSummaryValue(summary))
	}
	return out
}

func copyPatchSummaryValue(summary tools.PatchSummary) tools.PatchSummary {
	out := summary
	out.Files = append([]tools.FilePatchSummary(nil), summary.Files...)
	return out
}

func (r *Runner) qualityPlanCopy() *QualityPlan {
	if r == nil || r.qualityPlan == nil {
		return nil
	}
	plan := *r.qualityPlan
	plan.Phases = make([]QualityPlanPhase, len(r.qualityPlan.Phases))
	for i, phase := range r.qualityPlan.Phases {
		plan.Phases[i] = QualityPlanPhase{
			Name:       phase.Name,
			Status:     phase.Status,
			Acceptance: append([]string(nil), phase.Acceptance...),
			Evidence:   append([]string(nil), phase.Evidence...),
			Notes:      phase.Notes,
		}
	}
	return &plan
}

func (r *Runner) changeReviewFile(path string, remainingChars *int) ChangeReviewFile {
	file := ChangeReviewFile{Path: strings.TrimSpace(path)}
	if file.Path == "" {
		file.Error = "empty path"
		return file
	}
	result := r.Files.Read(file.Path, 1, maxChangeReviewLinesPerFile)
	file.Exists = result.Success
	if !result.Success {
		file.Error = strings.TrimSpace(result.Error)
		file.Binary = result.Binary
		return file
	}
	snapshot := strings.TrimSpace(result.Output)
	if remainingChars != nil {
		if *remainingChars <= 0 {
			file.SnapshotTruncated = snapshot != ""
			return file
		}
		var truncated bool
		snapshot, truncated = boundedCriticConsultText(snapshot, *remainingChars)
		file.SnapshotTruncated = truncated || result.Truncated
		*remainingChars -= len([]rune(snapshot))
	} else {
		file.SnapshotTruncated = result.Truncated
	}
	file.Snapshot = snapshot
	return file
}

func formatLoadedSkill(loaded skillcatalog.LoadedSkill) string {
	var b strings.Builder
	fmt.Fprintf(&b, "skill: %s\n", loaded.Skill.Name)
	fmt.Fprintf(&b, "source: %s\n", loaded.Skill.Source)
	fmt.Fprintf(&b, "path: %s\n", loaded.Skill.Path)
	if loaded.Skill.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", loaded.Skill.Description)
	}
	b.WriteString("\n")
	b.WriteString(loaded.Body)
	if loaded.Truncated {
		b.WriteString("\n--- skill body truncated ---\n")
	}
	return b.String()
}

func (r *Runner) Final(action Action) error {
	action.FilesChanged = cleanStringList(action.FilesChanged)
	action.Verification = cleanStringList(action.Verification)
	finalOutcome := normalizeFinalResponseOutcome(action.Outcome)
	verificationStatus, verificationMessage := finalVerificationStatus(action.FilesChanged, action.Verification, r.verificationChecks)
	if err := r.WriteFinalResponseAudit(r.FinalResponseAudit(action)); err != nil {
		return err
	}
	if err := r.Session.Write(session.Event{
		"type":                "verification_summary",
		"session_id":          r.SessionID,
		"status":              verificationStatus,
		"message":             verificationMessage,
		"files_changed":       action.FilesChanged,
		"verification_checks": action.Verification,
		"actual_checks":       append([]tools.VerificationCheck(nil), r.verificationChecks...),
	}); err != nil {
		return err
	}
	if err := r.Session.Write(session.Event{
		"type":          "assistant_message",
		"session_id":    r.SessionID,
		"message":       action.Summary,
		"final_outcome": finalOutcome,
		"files_changed": action.FilesChanged,
		"verification":  action.Verification,
	}); err != nil {
		return err
	}
	return r.Session.Write(session.Event{
		"type":                "turn_complete",
		"session_id":          r.SessionID,
		"summary":             action.Summary,
		"final_outcome":       finalOutcome,
		"files_changed":       action.FilesChanged,
		"verification":        action.Verification,
		"verification_status": verificationStatus,
		"actual_checks":       append([]tools.VerificationCheck(nil), r.verificationChecks...),
	})
}

func finalResponseAuditEvent(sessionID string, audit FinalResponseAudit) session.Event {
	outcome := strings.TrimSpace(audit.Outcome)
	if outcome == "" {
		outcome = "pass"
	}
	return session.Event{
		"type":                "final_response_audit",
		"session_id":          sessionID,
		"outcome":             outcome,
		"final_outcome":       audit.FinalOutcome,
		"verification_status": strings.TrimSpace(audit.VerificationStatus),
		"code":                strings.TrimSpace(audit.Code),
		"message":             strings.TrimSpace(audit.Message),
		"blocking":            audit.Blocking,
		"tool_failures":       audit.ToolFailures,
		"operational_actions": audit.OperationalActions,
	}
}

func verificationCheckEvent(sessionID string, check tools.VerificationCheck) session.Event {
	return session.Event{
		"type":               "verification_check",
		"session_id":         sessionID,
		"command":            check.Command,
		"argv":               check.Argv,
		"cwd":                check.CWD,
		"purpose":            check.Purpose,
		"allowed_exit_codes": check.AllowedExitCodes,
		"status":             check.Status,
		"success":            check.Success,
		"exit_code":          check.ExitCode,
		"duration":           check.Duration,
		"timed_out":          check.TimedOut,
		"denied":             check.Denied,
		"error":              check.Error,
	}
}

func verificationFeedbackEvent(sessionID string, feedback VerificationFeedback) session.Event {
	return session.Event{
		"type":       "verification_feedback",
		"session_id": sessionID,
		"status":     feedback.Status,
		"command":    feedback.Command,
		"message":    feedback.Message,
	}
}

func patchFeedbackEvent(sessionID string, feedback PatchFeedback) session.Event {
	event := session.Event{
		"type":       "patch_feedback",
		"session_id": sessionID,
		"stage":      feedback.Stage,
		"path":       feedback.Path,
		"message":    feedback.Message,
	}
	if len(feedback.SuggestedReads) > 0 {
		event["suggested_reads"] = feedback.SuggestedReads
	}
	return event
}

func finalVerificationStatus(filesChanged, verification []string, actualChecks []tools.VerificationCheck) (string, string) {
	if len(actualChecks) > 0 {
		finalChecks := latestVerificationOutcomes(relevantVerificationChecks(verification, actualChecks))
		if len(finalChecks) == 0 && len(verification) > 0 {
			finalChecks = latestVerificationOutcomes(passedVerificationChecks(actualChecks))
		}
		if len(finalChecks) == 0 {
			finalChecks = latestVerificationOutcomes(actualChecks)
		}
		failed := failedVerificationChecks(finalChecks)
		if len(failed) > 0 {
			return "failed", "Verification checks failed: " + strings.Join(formatVerificationChecks(failed, 3), "; ")
		}
		message := "Verification checks passed: " + strings.Join(formatVerificationChecks(finalChecks, 3), "; ")
		if len(verification) == 0 {
			message += ". final_response did not list verification details."
		}
		return "verified", message
	}
	if len(verification) > 0 {
		return "reported_only", "Verification was reported in final_response, but no run_command check was marked with purpose=verify."
	}
	if len(filesChanged) > 0 {
		return "missing_after_changes", "No verification check was run or reported for changed files."
	}
	return "not_run", "No verification check was run."
}

func relevantVerificationChecks(reported []string, actual []tools.VerificationCheck) []tools.VerificationCheck {
	if len(reported) == 0 {
		return actual
	}
	relevant := make([]tools.VerificationCheck, 0, len(actual))
	seen := map[int]bool{}
	for _, item := range reported {
		index := latestReportedVerificationCheckIndex(item, actual)
		if index < 0 || seen[index] {
			continue
		}
		seen[index] = true
		relevant = append(relevant, actual[index])
	}
	return relevant
}

func latestReportedVerificationCheckIndex(item string, actual []tools.VerificationCheck) int {
	for index := len(actual) - 1; index >= 0; index-- {
		if verificationReportItemMatchesCheck(item, actual[index]) {
			return index
		}
	}
	return -1
}

func verificationReportItemMatchesCheck(item string, check tools.VerificationCheck) bool {
	item = strings.ToLower(strings.TrimSpace(item))
	if item == "" {
		return false
	}
	for _, candidate := range []string{
		verificationCheckLabel(check),
		verificationCheckDisplayLabel(check),
		normalizedVerificationCommand(verificationCheckCommand(check)),
	} {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate != "" && strings.Contains(item, candidate) {
			return true
		}
	}
	return false
}

func latestVerificationOutcomes(checks []tools.VerificationCheck) []tools.VerificationCheck {
	out := make([]tools.VerificationCheck, 0, len(checks))
	indexByLabel := map[string]int{}
	for _, check := range checks {
		label := verificationCheckIdentity(check)
		if label == "" {
			label = "verification check"
		}
		if index, ok := indexByLabel[label]; ok {
			out[index] = check
			continue
		}
		indexByLabel[label] = len(out)
		out = append(out, check)
	}
	return out
}

func failedVerificationChecks(checks []tools.VerificationCheck) []tools.VerificationCheck {
	var failed []tools.VerificationCheck
	for _, check := range checks {
		if check.Status != tools.VerificationStatusPassed {
			failed = append(failed, check)
		}
	}
	return failed
}

func passedVerificationChecks(checks []tools.VerificationCheck) []tools.VerificationCheck {
	var passed []tools.VerificationCheck
	for _, check := range checks {
		if check.Status == tools.VerificationStatusPassed {
			passed = append(passed, check)
		}
	}
	return passed
}

func formatVerificationChecks(checks []tools.VerificationCheck, limit int) []string {
	if limit <= 0 || limit > len(checks) {
		limit = len(checks)
	}
	out := make([]string, 0, limit+1)
	for _, check := range checks[:limit] {
		label := firstNonEmpty(verificationCheckDisplayLabel(check), "verification check")
		if check.Status != "" && check.Status != tools.VerificationStatusPassed {
			label += " (" + check.Status + ")"
		}
		out = append(out, label)
	}
	if len(checks) > limit {
		out = append(out, fmt.Sprintf("%d more", len(checks)-limit))
	}
	return out
}

func verificationCheckLabel(check tools.VerificationCheck) string {
	return firstNonEmpty(normalizedVerificationCommand(verificationCheckCommand(check)), verificationCheckCommand(check))
}

func verificationCheckCommand(check tools.VerificationCheck) string {
	return firstNonEmpty(check.Command, strings.Join(check.Argv, " "))
}

func verificationCheckDisplayLabel(check tools.VerificationCheck) string {
	label := verificationCheckLabel(check)
	if label == "" {
		return ""
	}
	if cwd := verificationCheckEffectiveCWD(check); cwd != "" {
		label += " in " + cwd
	}
	return label
}

func verificationCheckIdentity(check tools.VerificationCheck) string {
	label := verificationCheckLabel(check)
	if label == "" {
		return ""
	}
	if cwd := verificationCheckEffectiveCWD(check); cwd != "" {
		return label + "\x00" + filepath.Clean(cwd)
	}
	return label
}

func verificationCheckEffectiveCWD(check tools.VerificationCheck) string {
	if cwd := shellLeadingCDCWD(check.Command); cwd != "" {
		return cwd
	}
	return strings.TrimSpace(check.CWD)
}

func normalizedVerificationCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	segments := strings.Split(command, "&&")
	for index := len(segments) - 1; index >= 0; index-- {
		segment := strings.TrimSpace(segments[index])
		if segment == "" || isShellBookkeepingCommand(segment) || shellLeadingCDCWD(segment) != "" {
			continue
		}
		return segment
	}
	return command
}

func isShellBookkeepingCommand(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return true
	}
	switch fields[0] {
	case "pwd":
		return len(fields) == 1
	case "ls":
		return len(fields) == 1 || (len(fields) == 2 && fields[1] == "package.json")
	default:
		return false
	}
}

func shellLeadingCDCWD(command string) string {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) < 2 || fields[0] != "cd" {
		return ""
	}
	cwd := strings.Trim(fields[1], `"'`)
	if cwd == "" || cwd == "-" {
		return ""
	}
	return filepath.Clean(cwd)
}

func cleanStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func appendCleanUniqueStrings(existing []string, values ...string) []string {
	seen := map[string]bool{}
	for _, value := range existing {
		seen[value] = true
	}
	for _, value := range cleanStringList(values) {
		if seen[value] {
			continue
		}
		existing = append(existing, value)
		seen[value] = true
	}
	return existing
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
