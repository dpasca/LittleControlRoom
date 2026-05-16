package sessionmetrics

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"lcroom/internal/lcagent/modeladapter"
	lcrmodel "lcroom/internal/model"
)

const largestToolOutputLimit = 5

type Summary struct {
	Files                     []string          `json:"files,omitempty"`
	Sessions                  int               `json:"sessions"`
	SessionIDs                []string          `json:"session_ids,omitempty"`
	ToolProfiles              map[string]int    `json:"tool_profiles,omitempty"`
	ContextProfiles           map[string]int    `json:"context_profiles,omitempty"`
	ModelResponses            int               `json:"model_responses"`
	ToolCalls                 map[string]int    `json:"tool_calls"`
	ToolResults               map[string]int    `json:"tool_results"`
	ToolSuccesses             map[string]int    `json:"tool_successes,omitempty"`
	ToolFailures              map[string]int    `json:"tool_failures,omitempty"`
	ResumeContexts            int               `json:"resume_contexts"`
	PermissionDenials         int               `json:"permission_denials"`
	PatchDiffSummaries        int               `json:"patch_diff_summaries"`
	PatchFeedback             int               `json:"patch_feedback"`
	RepairFeedbackSuppressed  int               `json:"repair_feedback_suppressed"`
	VerificationChecks        int               `json:"verification_checks"`
	VerificationFeedback      int               `json:"verification_feedback"`
	VerificationCheckStatuses map[string]int    `json:"verification_check_statuses,omitempty"`
	VerificationStatuses      map[string]int    `json:"verification_statuses,omitempty"`
	ReadFileCalls             int               `json:"read_file_calls"`
	ReadFileLines             int               `json:"read_file_lines"`
	ReadFileOutputBytes       int               `json:"read_file_output_bytes"`
	ReadFileOverlappingCalls  int               `json:"read_file_overlapping_calls"`
	ReadFileOverlappingLines  int               `json:"read_file_overlapping_lines"`
	RepeatedReadRanges        []ReadRangeCount  `json:"repeated_read_ranges,omitempty"`
	LargestToolOutputs        []ToolOutputSize  `json:"largest_tool_outputs,omitempty"`
	TokenUsage                lcrmodel.LLMUsage `json:"token_usage"`
	MaxInputTokens            int64             `json:"max_input_tokens"`
	MaxTotalTokens            int64             `json:"max_total_tokens"`
	TraceQuality              TraceQuality      `json:"trace_quality"`
	rangesByFile              map[string][]lineRange
	readRangeCounts           map[string]rangeSeen
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

type TraceQuality struct {
	Score                int                   `json:"score"`
	Grade                string                `json:"grade"`
	Findings             []TraceQualityFinding `json:"findings,omitempty"`
	ToolFailures         int                   `json:"tool_failures"`
	ToolFailureRate      float64               `json:"tool_failure_rate"`
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
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return err
		}
		summary.addEvent(source, event)
	}
	if err := scanner.Err(); err != nil {
		return err
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
	if s.ToolProfiles == nil {
		s.ToolProfiles = map[string]int{}
	}
	if s.ContextProfiles == nil {
		s.ContextProfiles = map[string]int{}
	}
	if s.VerificationStatuses == nil {
		s.VerificationStatuses = map[string]int{}
	}
	if s.VerificationCheckStatuses == nil {
		s.VerificationCheckStatuses = map[string]int{}
	}
	if s.rangesByFile == nil {
		s.rangesByFile = map[string][]lineRange{}
	}
	if s.readRangeCounts == nil {
		s.readRangeCounts = map[string]rangeSeen{}
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
	s.TraceQuality = s.computeTraceQuality()
}

func (s *Summary) addEvent(source string, event map[string]json.RawMessage) {
	switch rawString(event["type"]) {
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
	case "model_response":
		s.ModelResponses++
		usage := usageFromEvent(event)
		s.addUsage(usage)
	case "permission_denied":
		s.PermissionDenials++
	case "resume_context":
		s.ResumeContexts++
	case "patch_diff_summary":
		s.PatchDiffSummaries++
	case "patch_feedback":
		s.PatchFeedback++
	case "repair_feedback_suppressed":
		s.RepairFeedbackSuppressed++
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
	case "tool_call":
		tool := rawString(event["tool"])
		if tool == "" {
			tool = "unknown"
		}
		s.ToolCalls[tool]++
	case "tool_result":
		tool := rawString(event["tool"])
		if tool == "" {
			tool = "unknown"
		}
		s.ToolResults[tool]++
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
		RepairEvents:         s.PermissionDenials + s.PatchFeedback + s.VerificationFeedback + s.RepairFeedbackSuppressed,
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
