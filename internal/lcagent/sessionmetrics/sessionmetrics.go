package sessionmetrics

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/lcagent/modeladapter"
	lcrmodel "lcroom/internal/model"
)

const largestToolOutputLimit = 5
const largestTimingLimit = 5

type Summary struct {
	Files                       []string           `json:"files,omitempty"`
	Sessions                    int                `json:"sessions"`
	SessionIDs                  []string           `json:"session_ids,omitempty"`
	ToolProfiles                map[string]int     `json:"tool_profiles,omitempty"`
	ContextProfiles             map[string]int     `json:"context_profiles,omitempty"`
	ModelResponses              int                `json:"model_responses"`
	CriticModelResponses        int                `json:"critic_model_responses,omitempty"`
	CriticInvalidModelResponses int                `json:"critic_invalid_model_responses,omitempty"`
	ToolCalls                   map[string]int     `json:"tool_calls"`
	ToolResults                 map[string]int     `json:"tool_results"`
	ToolSuccesses               map[string]int     `json:"tool_successes,omitempty"`
	ToolFailures                map[string]int     `json:"tool_failures,omitempty"`
	ProviderFailures            map[string]int     `json:"provider_failures,omitempty"`
	ProviderRetries             int                `json:"provider_retries"`
	ProviderRetrySuccesses      int                `json:"provider_retry_successes"`
	SearchRefinements           int                `json:"search_refinements,omitempty"`
	SearchRefinementFailures    int                `json:"search_refinement_failures,omitempty"`
	CriticReviewsStarted        int                `json:"critic_reviews_started,omitempty"`
	CriticReviewResults         int                `json:"critic_review_results,omitempty"`
	CriticReviewFailures        int                `json:"critic_review_failures,omitempty"`
	CriticReviewStatuses        map[string]int     `json:"critic_review_statuses,omitempty"`
	CriticReviewModes           map[string]int     `json:"critic_review_modes,omitempty"`
	CriticConsultsStarted       int                `json:"critic_consults_started,omitempty"`
	CriticConsultResults        int                `json:"critic_consult_results,omitempty"`
	CriticConsultFailures       int                `json:"critic_consult_failures,omitempty"`
	CriticConsultStatuses       map[string]int     `json:"critic_consult_statuses,omitempty"`
	CriticLeadFeedback          int                `json:"critic_lead_feedback,omitempty"`
	CriticHumanPrompts          int                `json:"critic_human_prompts,omitempty"`
	Continuations               int                `json:"continuations"`
	ResumeContexts              int                `json:"resume_contexts"`
	PermissionDenials           int                `json:"permission_denials"`
	PatchDiffSummaries          int                `json:"patch_diff_summaries"`
	PatchFeedback               int                `json:"patch_feedback"`
	RepairFeedbackSuppressed    int                `json:"repair_feedback_suppressed"`
	RepairGuidance              int                `json:"repair_guidance"`
	VerificationChecks          int                `json:"verification_checks"`
	VerificationFeedback        int                `json:"verification_feedback"`
	VerificationCheckStatuses   map[string]int     `json:"verification_check_statuses,omitempty"`
	VerificationStatuses        map[string]int     `json:"verification_statuses,omitempty"`
	FinalResponseAudits         int                `json:"final_response_audits,omitempty"`
	FinalResponseAuditOutcomes  map[string]int     `json:"final_response_audit_outcomes,omitempty"`
	OperationalActions          int                `json:"operational_actions,omitempty"`
	OperationalActionStatuses   map[string]int     `json:"operational_action_statuses,omitempty"`
	ContextCompactions          int                `json:"context_compactions,omitempty"`
	ReadFileCalls               int                `json:"read_file_calls"`
	ReadFileLines               int                `json:"read_file_lines"`
	ReadFileOutputBytes         int                `json:"read_file_output_bytes"`
	ReadFileOverlappingCalls    int                `json:"read_file_overlapping_calls"`
	ReadFileOverlappingLines    int                `json:"read_file_overlapping_lines"`
	RepeatedReadRanges          []ReadRangeCount   `json:"repeated_read_ranges,omitempty"`
	LargestToolOutputs          []ToolOutputSize   `json:"largest_tool_outputs,omitempty"`
	TokenUsage                  lcrmodel.LLMUsage  `json:"token_usage"`
	UncachedInputTokens         int64              `json:"uncached_input_tokens,omitempty"`
	MaxInputTokens              int64              `json:"max_input_tokens"`
	MaxTotalTokens              int64              `json:"max_total_tokens"`
	ObservedElapsedSeconds      float64            `json:"observed_elapsed_seconds,omitempty"`
	ModelResponseWaitSeconds    float64            `json:"model_response_wait_seconds,omitempty"`
	MaxModelResponseWaitSeconds float64            `json:"max_model_response_wait_seconds,omitempty"`
	ToolSeconds                 map[string]float64 `json:"tool_seconds,omitempty"`
	SlowestEventGaps            []EventGap         `json:"slowest_event_gaps,omitempty"`
	SlowestToolRuns             []ToolRunTiming    `json:"slowest_tool_runs,omitempty"`
	WasteReport                 WasteReport        `json:"waste_report"`
	TraceQuality                TraceQuality       `json:"trace_quality"`
	rangesByFile                map[string][]lineRange
	readRangeCounts             map[string]rangeSeen
	sourceBounds                map[string]timeBounds
	lastEventBySource           map[string]timedEvent
	pendingToolStarts           map[string][]timedEvent
}

type ReadRangeCount struct {
	File  string `json:"file"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Count int    `json:"count"`
}

type ToolOutputSize struct {
	Source    string `json:"source,omitempty"`
	Tool      string `json:"tool"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated,omitempty"`
}

type WasteReport struct {
	RepeatedReadRanges       []ReadRangeCount `json:"repeated_read_ranges,omitempty"`
	LargestToolOutputs       []ToolOutputSize `json:"largest_tool_outputs,omitempty"`
	ContextCompactions       int              `json:"context_compactions,omitempty"`
	UncachedInputTokens      int64            `json:"uncached_input_tokens,omitempty"`
	ReadFileOutputBytes      int              `json:"read_file_output_bytes,omitempty"`
	ReadFileOverlappingCalls int              `json:"read_file_overlapping_calls,omitempty"`
	ReadFileOverlappingLines int              `json:"read_file_overlapping_lines,omitempty"`
	ReadOverlapRate          float64          `json:"read_overlap_rate,omitempty"`
	CachedInputTokenRate     float64          `json:"cached_input_token_rate,omitempty"`
}

type EventGap struct {
	Source  string  `json:"source,omitempty"`
	From    string  `json:"from"`
	To      string  `json:"to"`
	Seconds float64 `json:"seconds"`
}

type ToolRunTiming struct {
	Source  string  `json:"source,omitempty"`
	Tool    string  `json:"tool"`
	Seconds float64 `json:"seconds"`
}

type TraceQuality struct {
	Score                int                   `json:"score"`
	Grade                string                `json:"grade"`
	Findings             []TraceQualityFinding `json:"findings,omitempty"`
	ToolFailures         int                   `json:"tool_failures"`
	ToolFailureRate      float64               `json:"tool_failure_rate"`
	ProviderFailures     int                   `json:"provider_failures"`
	ProviderRetries      int                   `json:"provider_retries"`
	CriticReviews        int                   `json:"critic_reviews,omitempty"`
	CriticConsultations  int                   `json:"critic_consultations,omitempty"`
	CriticLeadFeedback   int                   `json:"critic_lead_feedback,omitempty"`
	CriticHumanPrompts   int                   `json:"critic_human_prompts,omitempty"`
	RepairEvents         int                   `json:"repair_events"`
	VerifiedSessions     int                   `json:"verified_sessions"`
	VerificationRate     float64               `json:"verification_rate"`
	ReadOverlapRate      float64               `json:"read_overlap_rate"`
	CachedInputTokenRate float64               `json:"cached_input_token_rate"`
	EstimatedCostUSD     float64               `json:"estimated_cost_usd"`
}

type TraceQualityFinding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type lineRange struct {
	File  string
	Start int
	End   int
}

type rangeSeen struct {
	Range lineRange
	Count int
}

type timeBounds struct {
	First time.Time
	Last  time.Time
}

type timedEvent struct {
	Type string
	At   time.Time
}

func AnalyzeFiles(paths []string) (Summary, error) {
	var summary Summary
	summary.init()
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return Summary{}, err
		}
		if err := Analyze(file, path, &summary); err != nil {
			_ = file.Close()
			return Summary{}, err
		}
		if err := file.Close(); err != nil {
			return Summary{}, err
		}
		summary.Files = append(summary.Files, path)
	}
	summary.finish()
	return summary, nil
}

func Analyze(reader io.Reader, source string, summary *Summary) error {
	if summary == nil {
		return fmt.Errorf("summary is nil")
	}
	summary.init()
	buffered := bufio.NewReader(reader)
	for {
		line, readErr := buffered.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return readErr
			}
			continue
		}
		var event map[string]json.RawMessage
		if err := json.Unmarshal(line, &event); err != nil {
			return err
		}
		summary.addEvent(source, event)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return nil
}

func (s *Summary) init() {
	if s.ToolCalls == nil {
		s.ToolCalls = map[string]int{}
	}
	if s.ToolResults == nil {
		s.ToolResults = map[string]int{}
	}
	if s.ToolSuccesses == nil {
		s.ToolSuccesses = map[string]int{}
	}
	if s.ToolFailures == nil {
		s.ToolFailures = map[string]int{}
	}
	if s.ProviderFailures == nil {
		s.ProviderFailures = map[string]int{}
	}
	if s.ToolProfiles == nil {
		s.ToolProfiles = map[string]int{}
	}
	if s.ContextProfiles == nil {
		s.ContextProfiles = map[string]int{}
	}
	if s.CriticReviewStatuses == nil {
		s.CriticReviewStatuses = map[string]int{}
	}
	if s.CriticReviewModes == nil {
		s.CriticReviewModes = map[string]int{}
	}
	if s.CriticConsultStatuses == nil {
		s.CriticConsultStatuses = map[string]int{}
	}
	if s.VerificationStatuses == nil {
		s.VerificationStatuses = map[string]int{}
	}
	if s.VerificationCheckStatuses == nil {
		s.VerificationCheckStatuses = map[string]int{}
	}
	if s.FinalResponseAuditOutcomes == nil {
		s.FinalResponseAuditOutcomes = map[string]int{}
	}
	if s.OperationalActionStatuses == nil {
		s.OperationalActionStatuses = map[string]int{}
	}
	if s.rangesByFile == nil {
		s.rangesByFile = map[string][]lineRange{}
	}
	if s.readRangeCounts == nil {
		s.readRangeCounts = map[string]rangeSeen{}
	}
	if s.ToolSeconds == nil {
		s.ToolSeconds = map[string]float64{}
	}
	if s.sourceBounds == nil {
		s.sourceBounds = map[string]timeBounds{}
	}
	if s.lastEventBySource == nil {
		s.lastEventBySource = map[string]timedEvent{}
	}
	if s.pendingToolStarts == nil {
		s.pendingToolStarts = map[string][]timedEvent{}
	}
}

func (s *Summary) finish() {
	repeated := make([]ReadRangeCount, 0)
	for _, seen := range s.readRangeCounts {
		if seen.Count <= 1 {
			continue
		}
		repeated = append(repeated, ReadRangeCount{
			File:  seen.Range.File,
			Start: seen.Range.Start,
			End:   seen.Range.End,
			Count: seen.Count,
		})
	}
	sort.Slice(repeated, func(i, j int) bool {
		if repeated[i].Count != repeated[j].Count {
			return repeated[i].Count > repeated[j].Count
		}
		if repeated[i].File != repeated[j].File {
			return repeated[i].File < repeated[j].File
		}
		return repeated[i].Start < repeated[j].Start
	})
	s.RepeatedReadRanges = repeated
	s.ObservedElapsedSeconds = 0
	for _, bounds := range s.sourceBounds {
		if bounds.First.IsZero() || bounds.Last.IsZero() || !bounds.Last.After(bounds.First) {
			continue
		}
		s.ObservedElapsedSeconds += roundedSeconds(bounds.Last.Sub(bounds.First).Seconds())
	}
	s.ObservedElapsedSeconds = roundedSeconds(s.ObservedElapsedSeconds)
	if s.TokenUsage.InputTokens > s.TokenUsage.CachedInputTokens {
		s.UncachedInputTokens = s.TokenUsage.InputTokens - s.TokenUsage.CachedInputTokens
	}
	s.TraceQuality = s.computeTraceQuality()
	s.WasteReport = WasteReport{
		RepeatedReadRanges:       append([]ReadRangeCount(nil), s.RepeatedReadRanges...),
		LargestToolOutputs:       append([]ToolOutputSize(nil), s.LargestToolOutputs...),
		ContextCompactions:       s.ContextCompactions,
		UncachedInputTokens:      s.UncachedInputTokens,
		ReadFileOutputBytes:      s.ReadFileOutputBytes,
		ReadFileOverlappingCalls: s.ReadFileOverlappingCalls,
		ReadFileOverlappingLines: s.ReadFileOverlappingLines,
		ReadOverlapRate:          s.TraceQuality.ReadOverlapRate,
		CachedInputTokenRate:     s.TraceQuality.CachedInputTokenRate,
	}
}

func (s *Summary) addEvent(source string, event map[string]json.RawMessage) {
	eventType := rawString(event["type"])
	eventAt, hasEventAt := rawTime(event["timestamp"])
	s.observeTiming(source, eventType, eventAt, hasEventAt)

	switch eventType {
	case "session_meta":
		s.Sessions++
		if id := rawString(event["id"]); id != "" {
			s.SessionIDs = append(s.SessionIDs, id)
		}
	case "tool_profile":
		profile := rawString(event["profile"])
		if profile == "" {
			profile = "unknown"
		}
		s.ToolProfiles[profile]++
	case "context_profile":
		profile := rawString(event["profile"])
		if profile == "" {
			profile = "unknown"
		}
		s.ContextProfiles[profile]++
	case "context_compacted":
		s.ContextCompactions++
	case "model_response":
		s.ModelResponses++
		usage := usageFromEvent(event)
		s.addUsage(usage)
	case "critic_model_response":
		s.CriticModelResponses++
		usage := usageFromEvent(event)
		s.addUsage(usage)
	case "critic_model_response_invalid":
		s.CriticInvalidModelResponses++
		usage := usageFromEvent(event)
		s.addUsage(usage)
	case "permission_denied":
		s.PermissionDenials++
	case "provider_failure":
		kind := rawString(event["kind"])
		if kind == "" {
			kind = "unknown"
		}
		s.ProviderFailures[kind]++
	case "provider_retry":
		s.ProviderRetries++
	case "provider_retry_succeeded":
		s.ProviderRetrySuccesses++
	case "search_refine_result":
		if rawBool(event["success"]) {
			s.SearchRefinements++
		} else {
			s.SearchRefinementFailures++
		}
	case "critic_review_started":
		s.CriticReviewsStarted++
	case "critic_review_result":
		s.CriticReviewResults++
		s.addCriticReviewMode(rawString(event["mode"]))
		status := rawString(event["status"])
		if status == "" {
			status = "unknown"
		}
		s.CriticReviewStatuses[status]++
		if strings.TrimSpace(firstNonEmpty(rawString(event["human_prompt"]), rawString(event["proposed_user_message"]))) != "" {
			s.CriticHumanPrompts++
		}
	case "critic_review_failed":
		s.CriticReviewFailures++
		s.addCriticReviewMode(rawString(event["mode"]))
	case "critic_consult_started":
		s.CriticConsultsStarted++
	case "critic_consult_result":
		s.CriticConsultResults++
		status := rawString(event["status"])
		if status == "" {
			status = "unknown"
		}
		s.CriticConsultStatuses[status]++
		usage := usageFromEvent(event)
		s.addUsage(usage)
	case "critic_consult_failed":
		s.CriticConsultFailures++
	case "critic_lead_feedback":
		s.CriticLeadFeedback++
	case "continuation":
		s.Continuations++
	case "resume_context":
		s.ResumeContexts++
	case "patch_diff_summary":
		s.PatchDiffSummaries++
	case "patch_feedback":
		s.PatchFeedback++
	case "repair_feedback_suppressed":
		s.RepairFeedbackSuppressed++
	case "repair_guidance":
		s.RepairGuidance++
	case "verification_check":
		s.VerificationChecks++
		status := rawString(event["status"])
		if status == "" {
			status = "unknown"
		}
		s.VerificationCheckStatuses[status]++
	case "verification_feedback":
		s.VerificationFeedback++
	case "verification_summary":
		status := rawString(event["status"])
		if status == "" {
			status = "unknown"
		}
		s.VerificationStatuses[status]++
	case "final_response_audit":
		s.FinalResponseAudits++
		outcome := rawString(event["outcome"])
		if outcome == "" {
			outcome = "unknown"
		}
		s.FinalResponseAuditOutcomes[outcome]++
	case "operational_action":
		s.OperationalActions++
		action := rawString(event["action"])
		if action == "" {
			action = "unknown"
		}
		status := "failed"
		if rawBool(event["denied"]) {
			status = "denied"
		} else if rawBool(event["success"]) {
			status = "succeeded"
		}
		s.OperationalActionStatuses[action+"."+status]++
	case "tool_call":
		tool := rawString(event["tool"])
		if tool == "" {
			tool = "unknown"
		}
		s.ToolCalls[tool]++
		if hasEventAt {
			s.addPendingToolStart(source, tool, eventAt)
		}
	case "tool_result":
		tool := rawString(event["tool"])
		if tool == "" {
			tool = "unknown"
		}
		s.ToolResults[tool]++
		if hasEventAt {
			s.observeToolRun(source, tool, eventAt)
		}
		result := toolResultFromRaw(event["result"])
		if result.Success != nil {
			if *result.Success {
				s.ToolSuccesses[tool]++
			} else {
				s.ToolFailures[tool]++
			}
		}
		outputBytes := len(result.Output) + len(result.Error)
		s.addLargestToolOutput(ToolOutputSize{
			Source:    source,
			Tool:      tool,
			Bytes:     outputBytes,
			Truncated: result.Truncated,
		})
		if tool == "read_file" {
			s.ReadFileCalls++
			s.ReadFileOutputBytes += len(result.Output)
			if readRange, ok := parseReadRange(result.Output); ok {
				s.ReadFileLines += readRange.End - readRange.Start + 1
				s.addReadRange(readRange)
			}
		}
	}
}

func (s *Summary) observeTiming(source, eventType string, eventAt time.Time, ok bool) {
	if !ok || eventType == "" {
		return
	}
	bounds := s.sourceBounds[source]
	if bounds.First.IsZero() || eventAt.Before(bounds.First) {
		bounds.First = eventAt
	}
	if bounds.Last.IsZero() || eventAt.After(bounds.Last) {
		bounds.Last = eventAt
	}
	s.sourceBounds[source] = bounds

	if prior, exists := s.lastEventBySource[source]; exists && eventAt.After(prior.At) {
		seconds := roundedSeconds(eventAt.Sub(prior.At).Seconds())
		s.addSlowestEventGap(EventGap{
			Source:  source,
			From:    firstNonEmpty(prior.Type, "unknown"),
			To:      eventType,
			Seconds: seconds,
		})
		if eventType == "model_response" {
			s.ModelResponseWaitSeconds = roundedSeconds(s.ModelResponseWaitSeconds + seconds)
			if seconds > s.MaxModelResponseWaitSeconds {
				s.MaxModelResponseWaitSeconds = seconds
			}
		}
	}
	s.lastEventBySource[source] = timedEvent{Type: eventType, At: eventAt}
}

func (s *Summary) addPendingToolStart(source, tool string, eventAt time.Time) {
	key := toolTimingKey(source, tool)
	s.pendingToolStarts[key] = append(s.pendingToolStarts[key], timedEvent{Type: tool, At: eventAt})
}

func (s *Summary) observeToolRun(source, tool string, resultAt time.Time) {
	key := toolTimingKey(source, tool)
	starts := s.pendingToolStarts[key]
	if len(starts) == 0 {
		return
	}
	start := starts[0]
	if len(starts) == 1 {
		delete(s.pendingToolStarts, key)
	} else {
		s.pendingToolStarts[key] = starts[1:]
	}
	if !resultAt.After(start.At) {
		return
	}
	seconds := roundedSeconds(resultAt.Sub(start.At).Seconds())
	s.ToolSeconds[tool] = roundedSeconds(s.ToolSeconds[tool] + seconds)
	s.addSlowestToolRun(ToolRunTiming{
		Source:  source,
		Tool:    tool,
		Seconds: seconds,
	})
}

func (s *Summary) addSlowestEventGap(gap EventGap) {
	if gap.Seconds <= 0 {
		return
	}
	s.SlowestEventGaps = append(s.SlowestEventGaps, gap)
	sort.Slice(s.SlowestEventGaps, func(i, j int) bool {
		return s.SlowestEventGaps[i].Seconds > s.SlowestEventGaps[j].Seconds
	})
	if len(s.SlowestEventGaps) > largestTimingLimit {
		s.SlowestEventGaps = s.SlowestEventGaps[:largestTimingLimit]
	}
}

func (s *Summary) addSlowestToolRun(run ToolRunTiming) {
	if run.Seconds <= 0 {
		return
	}
	s.SlowestToolRuns = append(s.SlowestToolRuns, run)
	sort.Slice(s.SlowestToolRuns, func(i, j int) bool {
		return s.SlowestToolRuns[i].Seconds > s.SlowestToolRuns[j].Seconds
	})
	if len(s.SlowestToolRuns) > largestTimingLimit {
		s.SlowestToolRuns = s.SlowestToolRuns[:largestTimingLimit]
	}
}

func toolTimingKey(source, tool string) string {
	return source + "\x00" + tool
}

func (s *Summary) addCriticReviewMode(mode string) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "unknown"
	}
	s.CriticReviewModes[mode]++
}

func usageFromEvent(event map[string]json.RawMessage) lcrmodel.LLMUsage {
	var usage lcrmodel.LLMUsage
	if raw := event["usage_summary"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &usage); err == nil {
			return usage
		}
	}
	return modeladapter.UsageFromRaw(event["usage"], rawString(event["model"]))
}

func (s *Summary) addUsage(usage lcrmodel.LLMUsage) {
	s.TokenUsage.InputTokens += usage.InputTokens
	s.TokenUsage.OutputTokens += usage.OutputTokens
	s.TokenUsage.TotalTokens += usage.TotalTokens
	s.TokenUsage.CachedInputTokens += usage.CachedInputTokens
	s.TokenUsage.ReasoningTokens += usage.ReasoningTokens
	s.TokenUsage.EstimatedCostUSD += usage.EstimatedCostUSD
	if usage.InputTokens > s.MaxInputTokens {
		s.MaxInputTokens = usage.InputTokens
	}
	if usage.TotalTokens > s.MaxTotalTokens {
		s.MaxTotalTokens = usage.TotalTokens
	}
}

func (s *Summary) addReadRange(readRange lineRange) {
	covered := coveredLineCount(readRange, s.rangesByFile[readRange.File])
	if covered > 0 {
		s.ReadFileOverlappingCalls++
		s.ReadFileOverlappingLines += covered
	}
	s.rangesByFile[readRange.File] = append(s.rangesByFile[readRange.File], readRange)
	key := fmt.Sprintf("%s:%d-%d", readRange.File, readRange.Start, readRange.End)
	seen := s.readRangeCounts[key]
	seen.Range = readRange
	seen.Count++
	s.readRangeCounts[key] = seen
}

func coveredLineCount(readRange lineRange, existing []lineRange) int {
	if readRange.End < readRange.Start {
		return 0
	}
	covered := make(map[int]bool)
	for _, prior := range existing {
		if prior.File != readRange.File {
			continue
		}
		start := maxInt(readRange.Start, prior.Start)
		end := minInt(readRange.End, prior.End)
		for line := start; line <= end; line++ {
			covered[line] = true
		}
	}
	return len(covered)
}

func (s *Summary) addLargestToolOutput(output ToolOutputSize) {
	if output.Bytes <= 0 {
		return
	}
	s.LargestToolOutputs = append(s.LargestToolOutputs, output)
	sort.Slice(s.LargestToolOutputs, func(i, j int) bool {
		return s.LargestToolOutputs[i].Bytes > s.LargestToolOutputs[j].Bytes
	})
	if len(s.LargestToolOutputs) > largestToolOutputLimit {
		s.LargestToolOutputs = s.LargestToolOutputs[:largestToolOutputLimit]
	}
}

type rawToolResult struct {
	Output    string `json:"output"`
	Error     string `json:"error"`
	Truncated bool   `json:"truncated"`
	Success   *bool  `json:"success"`
}

func toolResultFromRaw(raw json.RawMessage) rawToolResult {
	var result rawToolResult
	_ = json.Unmarshal(raw, &result)
	return result
}

func parseReadRange(output string) (lineRange, bool) {
	var readRange lineRange
	for _, line := range strings.Split(output, "\n") {
		if readRange.File == "" {
			if value, ok := strings.CutPrefix(line, "file: "); ok {
				readRange.File = strings.TrimSpace(value)
				continue
			}
		}
		if value, ok := strings.CutPrefix(line, "lines: "); ok {
			value = strings.TrimSpace(value)
			if strings.HasPrefix(value, "none") {
				return lineRange{}, false
			}
			startText, endText, ok := strings.Cut(value, "-")
			if !ok {
				return lineRange{}, false
			}
			start, err := strconv.Atoi(strings.TrimSpace(startText))
			if err != nil {
				return lineRange{}, false
			}
			end, err := strconv.Atoi(strings.TrimSpace(endText))
			if err != nil {
				return lineRange{}, false
			}
			readRange.Start = start
			readRange.End = end
			if readRange.File != "" && start > 0 && end >= start {
				return readRange, true
			}
			return lineRange{}, false
		}
	}
	return lineRange{}, false
}

func rawString(raw json.RawMessage) string {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func rawBool(raw json.RawMessage) bool {
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return value
}

func rawTime(raw json.RawMessage) (time.Time, bool) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return time.Time{}, false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func roundedSeconds(seconds float64) float64 {
	return math.Round(seconds*1000) / 1000
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s Summary) computeTraceQuality() TraceQuality {
	quality := TraceQuality{
		Score:                100,
		VerifiedSessions:     s.VerificationStatuses["verified"],
		RepairEvents:         s.PermissionDenials + s.PatchFeedback + s.VerificationFeedback + s.RepairFeedbackSuppressed + s.RepairGuidance,
		ProviderRetries:      s.ProviderRetries,
		CriticReviews:        s.CriticReviewResults,
		CriticConsultations:  s.CriticConsultResults,
		CriticLeadFeedback:   s.CriticLeadFeedback,
		CriticHumanPrompts:   s.CriticHumanPrompts,
		ReadOverlapRate:      ratio(s.ReadFileOverlappingLines, s.ReadFileLines),
		CachedInputTokenRate: ratio64(s.TokenUsage.CachedInputTokens, s.TokenUsage.InputTokens),
		EstimatedCostUSD:     s.TokenUsage.EstimatedCostUSD,
	}
	totalKnownToolResults := 0
	for _, count := range s.ToolSuccesses {
		totalKnownToolResults += count
	}
	for _, count := range s.ToolFailures {
		quality.ToolFailures += count
		totalKnownToolResults += count
	}
	for _, count := range s.ProviderFailures {
		quality.ProviderFailures += count
	}
	quality.ToolFailureRate = ratio(quality.ToolFailures, totalKnownToolResults)
	if s.Sessions > 0 {
		quality.VerificationRate = ratio(quality.VerifiedSessions, s.Sessions)
	}
	if s.Sessions == 0 {
		quality.addFinding("warn", "no_sessions", "No session metadata was found in the analyzed trace.")
		quality.Score -= 20
	}
	if s.Sessions > 0 && quality.VerifiedSessions == 0 {
		quality.addFinding("warn", "no_verified_sessions", "No session recorded a verified verification summary.")
		quality.Score -= 30
	} else if s.Sessions > 0 && quality.VerifiedSessions < s.Sessions {
		quality.addFinding("info", "partial_verification", "Some sessions did not record a verified verification summary.")
		quality.Score -= minInt(15, (s.Sessions-quality.VerifiedSessions)*5)
	}
	if quality.ToolFailures > 0 {
		quality.addFinding("warn", "tool_failures", fmt.Sprintf("%d tool result(s) failed.", quality.ToolFailures))
		quality.Score -= minInt(25, quality.ToolFailures*5)
	}
	if quality.ProviderFailures > 0 {
		quality.addFinding("warn", "provider_failures", fmt.Sprintf("%d provider failure event(s) were recorded.", quality.ProviderFailures))
		quality.Score -= minInt(20, quality.ProviderFailures*5)
	}
	if s.PermissionDenials > 0 {
		quality.addFinding("info", "permission_denials", fmt.Sprintf("%d permission denial(s) were recorded.", s.PermissionDenials))
		quality.Score -= minInt(10, s.PermissionDenials*2)
	}
	if s.PatchFeedback > 0 {
		quality.addFinding("warn", "patch_repair", fmt.Sprintf("%d patch repair feedback event(s) were needed.", s.PatchFeedback))
		quality.Score -= minInt(15, s.PatchFeedback*5)
	}
	if s.VerificationFeedback > 0 {
		quality.addFinding("warn", "verification_repair", fmt.Sprintf("%d verification feedback event(s) were needed.", s.VerificationFeedback))
		quality.Score -= minInt(15, s.VerificationFeedback*5)
	}
	if s.RepairFeedbackSuppressed > 0 {
		quality.addFinding("warn", "repeated_repair_feedback", fmt.Sprintf("%d duplicate repair feedback event(s) were suppressed.", s.RepairFeedbackSuppressed))
		quality.Score -= minInt(10, s.RepairFeedbackSuppressed*5)
	}
	if s.RepairGuidance > 0 {
		quality.addFinding("warn", "repair_guidance", fmt.Sprintf("%d repair guidance escalation event(s) were needed.", s.RepairGuidance))
		quality.Score -= minInt(10, s.RepairGuidance*5)
	}
	if s.CriticLeadFeedback > 0 {
		quality.addFinding("info", "critic_lead_feedback", fmt.Sprintf("%d private critic lead revision event(s) were recorded.", s.CriticLeadFeedback))
	}
	if s.CriticConsultResults > 0 {
		quality.addFinding("info", "critic_consultations", fmt.Sprintf("%d lead-initiated critic consultation(s) were recorded.", s.CriticConsultResults))
	}
	if s.CriticHumanPrompts > 0 {
		quality.addFinding("info", "critic_human_prompts", fmt.Sprintf("%d critic review result(s) drafted human-facing follow-up.", s.CriticHumanPrompts))
	}
	if quality.ReadOverlapRate >= 0.25 && s.ReadFileLines >= 100 {
		quality.addFinding("info", "read_overlap", fmt.Sprintf("%.0f%% of read_file lines overlapped earlier reads.", quality.ReadOverlapRate*100))
		quality.Score -= minInt(10, int(quality.ReadOverlapRate*20))
	}
	if quality.Score < 0 {
		quality.Score = 0
	}
	quality.Grade = traceQualityGrade(quality.Score)
	return quality
}

func (q *TraceQuality) addFinding(severity, code, message string) {
	q.Findings = append(q.Findings, TraceQualityFinding{
		Severity: severity,
		Code:     code,
		Message:  message,
	})
}

func traceQualityGrade(score int) string {
	switch {
	case score >= 90:
		return "excellent"
	case score >= 80:
		return "good"
	case score >= 60:
		return "mixed"
	default:
		return "needs_attention"
	}
}

func ratio(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func ratio64(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
