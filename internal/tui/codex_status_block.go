package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

type codexStatusBlockData struct {
	ThreadID           string
	ProjectPath        string
	CWD                string
	Model              string
	ModelProvider      string
	ReasoningEffort    string
	Agent              string
	ServiceTier        string
	Approval           string
	Sandbox            string
	Network            string
	WritableRoots      string
	ContextTokens      int64
	TotalTokens        int64
	ModelContextWindow int64
	ContextUsedPercent int
	HasContextPercent  bool
	LastTurnTokens     int64
	UsageWindows       []codexStatusUsageWindow
}

type codexStatusUsageWindow struct {
	Limit       string
	Plan        string
	Window      string
	LeftPercent int
	ResetsAt    time.Time
}

type codexStatusUsageGroup struct {
	Limit   string
	Plan    string
	Windows []codexStatusUsageWindow
}

func renderCodexStatusBlock(body string, width int) string {
	status, ok := parseCodexStatusBlock(body)
	if !ok {
		return renderCodexMessageBlock("Status", body, lipgloss.Color("81"), lipgloss.Color("252"), width)
	}
	contentWidth := max(20, width-2)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	sectionStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	groupStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("153"))

	lines := []string{titleStyle.Render("Status")}
	lines = append(lines, renderCodexStatusSummaryRows(status, contentWidth)...)

	groups := groupCodexStatusUsageWindows(status.UsageWindows)
	if len(groups) > 0 {
		lines = append(lines, "")
		lines = append(lines, sectionStyle.Render("Usage left"))
		for index, group := range groups {
			if index > 0 {
				lines = append(lines, "")
			}
			title := group.Limit
			if strings.TrimSpace(group.Plan) != "" {
				title += " (" + group.Plan + ")"
			}
			lines = append(lines, groupStyle.Render(title))
			for _, window := range group.Windows {
				lines = append(lines, renderCodexStatusUsageRow(window, contentWidth))
			}
		}
	}

	footerRows := renderCodexStatusFooterRows(status)
	if len(footerRows) > 0 {
		lines = append(lines, "")
		lines = append(lines, footerRows...)
	}

	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(lipgloss.Color("81")).
		PaddingLeft(0).
		Render(strings.Join(lines, "\n"))
}

func parseCodexStatusBlock(body string) (codexStatusBlockData, bool) {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 {
		return codexStatusBlockData{}, false
	}
	switch strings.TrimSpace(lines[0]) {
	case "Embedded Codex status", "Embedded OpenCode status":
	default:
		return codexStatusBlockData{}, false
	}
	status := codexStatusBlockData{}
	for _, rawLine := range lines[1:] {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "usage window:") {
			window, ok := parseCodexStatusUsageWindow(strings.TrimSpace(strings.TrimPrefix(line, "usage window:")))
			if ok {
				status.UsageWindows = append(status.UsageWindows, window)
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "thread":
			status.ThreadID = value
		case "project":
			status.ProjectPath = value
		case "cwd":
			status.CWD = value
		case "model":
			status.Model = value
		case "model provider":
			status.ModelProvider = value
		case "reasoning effort":
			status.ReasoningEffort = value
		case "agent":
			status.Agent = value
		case "service tier":
			status.ServiceTier = value
		case "approval":
			status.Approval = value
		case "sandbox":
			status.Sandbox = value
		case "network":
			status.Network = value
		case "writable roots":
			status.WritableRoots = value
		case "total tokens":
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				status.TotalTokens = parsed
			}
		case "context tokens":
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				status.ContextTokens = parsed
			}
		case "model context window":
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				status.ModelContextWindow = parsed
			}
		case "context used percent":
			if parsed, err := strconv.Atoi(value); err == nil {
				status.ContextUsedPercent = clampCodexStatusPercent(parsed)
				status.HasContextPercent = true
			}
		case "last turn tokens":
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				status.LastTurnTokens = parsed
			}
		}
	}
	return status, true
}

func parseCodexStatusUsageWindow(spec string) (codexStatusUsageWindow, bool) {
	window := codexStatusUsageWindow{}
	hasLimit := false
	hasWindow := false
	hasLeft := false
	for _, rawPart := range strings.Split(spec, ";") {
		part := strings.TrimSpace(rawPart)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "limit":
			window.Limit = value
			hasLimit = value != ""
		case "plan":
			window.Plan = value
		case "window":
			window.Window = value
			hasWindow = value != ""
		case "left":
			if parsed, err := strconv.Atoi(value); err == nil {
				window.LeftPercent = clampCodexStatusPercent(parsed)
				hasLeft = true
			}
		case "resetsat":
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed > 0 {
				window.ResetsAt = time.Unix(parsed, 0).Local()
			}
		}
	}
	if !hasLimit || !hasWindow || !hasLeft {
		return codexStatusUsageWindow{}, false
	}
	return window, true
}

func renderCodexStatusSummaryRows(status codexStatusBlockData, width int) []string {
	labelWidth := 11
	rows := make([]string, 0, 6)
	modelStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	reasoningStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	valueStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	if status.Model != "" {
		value := modelStyle.Render(status.Model)
		extras := make([]string, 0, 2)
		if status.ModelProvider != "" {
			extras = append(extras, status.ModelProvider)
		}
		if status.ServiceTier != "" && status.Agent == "" {
			if strings.HasPrefix(status.ServiceTier, "agent ") {
				extras = append(extras, status.ServiceTier)
			} else {
				extras = append(extras, "tier "+status.ServiceTier)
			}
		}
		if len(extras) > 0 {
			value += " " + mutedStyle.Render("("+strings.Join(extras, ", ")+")")
		}
		rows = append(rows, renderCodexStatusField("Model", value, labelWidth))
	}
	if status.ReasoningEffort != "" {
		rows = append(rows, renderCodexStatusField("Reasoning", reasoningStyle.Render(status.ReasoningEffort), labelWidth))
	}
	if status.Agent != "" {
		rows = append(rows, renderCodexStatusField("Agent", valueStyle.Render(status.Agent), labelWidth))
	}
	directory := status.CWD
	if strings.TrimSpace(directory) == "" {
		directory = status.ProjectPath
	}
	if directory != "" {
		rows = append(rows, renderCodexStatusField("Directory", valueStyle.Render(directory), labelWidth))
	}
	contextTokens := status.ContextTokens
	if contextTokens <= 0 {
		contextTokens = status.TotalTokens
	}
	if contextTokens > 0 {
		contextValue := valueStyle.Render(fmt.Sprintf("%s tokens", formatInt64(contextTokens)))
		if status.ModelContextWindow > 0 {
			details := fmt.Sprintf("of %s", formatInt64(status.ModelContextWindow))
			if status.HasContextPercent {
				details += fmt.Sprintf(" (%d%% used)", status.ContextUsedPercent)
			}
			contextValue += " " + mutedStyle.Render(details)
		}
		rows = append(rows, renderCodexStatusField("Context", contextValue, labelWidth))
	}
	if status.LastTurnTokens > 0 {
		rows = append(rows, renderCodexStatusField("Last turn", valueStyle.Render(fmt.Sprintf("%s tokens", formatInt64(status.LastTurnTokens))), labelWidth))
	}
	return rows
}

func renderCodexStatusFooterRows(status codexStatusBlockData) []string {
	labelWidth := 11
	mutedValueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
	rows := make([]string, 0, 2)

	accessParts := make([]string, 0, 3)
	if status.Approval != "" {
		accessParts = append(accessParts, status.Approval)
	}
	if status.Sandbox != "" {
		accessParts = append(accessParts, status.Sandbox)
	}
	if status.Network != "" {
		accessParts = append(accessParts, "network "+status.Network)
	}
	if len(accessParts) > 0 {
		rows = append(rows, renderCodexStatusField("Access", mutedValueStyle.Render(strings.Join(accessParts, " | ")), labelWidth))
	}
	if status.WritableRoots != "" {
		rows = append(rows, renderCodexStatusField("Writable", mutedValueStyle.Render(status.WritableRoots), labelWidth))
	}
	if status.ThreadID != "" {
		rows = append(rows, renderCodexStatusField("Session", mutedValueStyle.Render(status.ThreadID), labelWidth))
	}
	return rows
}

func groupCodexStatusUsageWindows(windows []codexStatusUsageWindow) []codexStatusUsageGroup {
	groups := make([]codexStatusUsageGroup, 0, len(windows))
	indexByKey := make(map[string]int)
	for _, window := range windows {
		key := strings.ToLower(window.Limit) + "|" + strings.ToLower(window.Plan)
		index, ok := indexByKey[key]
		if !ok {
			index = len(groups)
			indexByKey[key] = index
			groups = append(groups, codexStatusUsageGroup{
				Limit: window.Limit,
				Plan:  window.Plan,
			})
		}
		groups[index].Windows = append(groups[index].Windows, window)
	}
	return groups
}

func renderCodexStatusUsageRow(window codexStatusUsageWindow, width int) string {
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Width(12)
	resetStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	percentStyle := codexStatusUsagePercentStyle(window.LeftPercent)
	label := codexStatusWindowTitle(window.Window)
	bar := renderCodexStatusProgressBar(window.LeftPercent, codexStatusProgressBarWidth(width))
	leftText := percentStyle.Render(fmt.Sprintf("%3d%% left", window.LeftPercent))
	base := labelStyle.Render(label) + " " + bar + " " + leftText
	resetText := formatCodexStatusReset(window.ResetsAt)
	if resetText == "" {
		return base
	}
	resetRendered := resetStyle.Render(resetText)
	if lipgloss.Width(base+" "+resetRendered) <= width {
		return base + " " + resetRendered
	}
	return base + "\n" + lipgloss.NewStyle().MarginLeft(13).Foreground(lipgloss.Color("244")).Render(resetText)
}

func renderCodexStatusProgressBar(leftPercent, width int) string {
	if width <= 0 {
		width = 10
	}
	filled := (clampCodexStatusPercent(leftPercent)*width + 50) / 100
	if filled > width {
		filled = width
	}
	empty := width - filled
	fillStyle := codexStatusUsagePercentStyle(leftPercent)
	frameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	emptyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	return frameStyle.Render("[") +
		fillStyle.Render(strings.Repeat("=", filled)) +
		emptyStyle.Render(strings.Repeat("-", empty)) +
		frameStyle.Render("]")
}

func renderCodexStatusField(label, value string, labelWidth int) string {
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Width(labelWidth)
	return labelStyle.Render(label+":") + " " + value
}

func codexStatusProgressBarWidth(width int) int {
	switch {
	case width >= 84:
		return 22
	case width >= 66:
		return 18
	case width >= 52:
		return 14
	default:
		return 10
	}
}

func codexStatusUsagePercentStyle(leftPercent int) lipgloss.Style {
	switch {
	case leftPercent >= 75:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	case leftPercent >= 40:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	default:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
	}
}

func codexStatusWindowTitle(window string) string {
	window = strings.TrimSpace(window)
	switch strings.ToLower(window) {
	case "":
		return "Limit"
	case "weekly":
		return "Weekly limit"
	}
	return window + " limit"
}

func formatCodexStatusReset(reset time.Time) string {
	if reset.IsZero() {
		return ""
	}
	now := time.Now().In(reset.Location())
	if sameCodexStatusDay(now, reset) {
		return "resets " + reset.Format("15:04")
	}
	if now.Year() == reset.Year() {
		return "resets " + reset.Format("15:04 on 2 Jan")
	}
	return "resets " + reset.Format("15:04 on 2 Jan 2006")
}

func sameCodexStatusDay(left, right time.Time) bool {
	left = left.In(right.Location())
	return left.Year() == right.Year() && left.YearDay() == right.YearDay()
}

func clampCodexStatusPercent(percent int) int {
	switch {
	case percent < 0:
		return 0
	case percent > 100:
		return 100
	default:
		return percent
	}
}

func formatInt64(value int64) string {
	if value == 0 {
		return "0"
	}
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	digits := strconv.FormatInt(value, 10)
	if len(digits) <= 3 {
		return sign + digits
	}
	var out strings.Builder
	out.Grow(len(digits) + len(digits)/3)
	for index, r := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(r)
	}
	return sign + out.String()
}
