package codexapp

import (
	"encoding/json"
	"sort"
	"strings"
)

type mcpUsageStats struct {
	ServerName string
	ToolCalls  int
	LastTool   string
	Tools      map[string]int
}

func recordMCPToolUsage(usage map[string]*mcpUsageStats, serverName, toolName string) map[string]*mcpUsageStats {
	serverName = strings.TrimSpace(serverName)
	toolName = strings.TrimSpace(toolName)
	if serverName == "" {
		return usage
	}
	if usage == nil {
		usage = make(map[string]*mcpUsageStats)
	}
	stats := usage[serverName]
	if stats == nil {
		stats = &mcpUsageStats{
			ServerName: serverName,
			Tools:      make(map[string]int),
		}
		usage[serverName] = stats
	}
	stats.ToolCalls++
	if toolName != "" {
		stats.LastTool = toolName
		stats.Tools[toolName]++
	}
	return usage
}

func exportedMCPUsageSnapshot(usage map[string]*mcpUsageStats) []MCPUsageSnapshot {
	if len(usage) == 0 {
		return nil
	}
	snapshot := make([]MCPUsageSnapshot, 0, len(usage))
	for _, stats := range usage {
		if stats == nil || strings.TrimSpace(stats.ServerName) == "" || stats.ToolCalls <= 0 {
			continue
		}
		item := MCPUsageSnapshot{
			ServerName: strings.TrimSpace(stats.ServerName),
			ToolCalls:  stats.ToolCalls,
			LastTool:   strings.TrimSpace(stats.LastTool),
			Tools:      exportedMCPToolUsageSnapshot(stats.Tools),
		}
		snapshot = append(snapshot, item)
	}
	sort.SliceStable(snapshot, func(i, j int) bool {
		if snapshot[i].ToolCalls != snapshot[j].ToolCalls {
			return snapshot[i].ToolCalls > snapshot[j].ToolCalls
		}
		return snapshot[i].ServerName < snapshot[j].ServerName
	})
	return snapshot
}

func exportedMCPToolUsageSnapshot(tools map[string]int) []MCPToolUsageSnapshot {
	if len(tools) == 0 {
		return nil
	}
	snapshot := make([]MCPToolUsageSnapshot, 0, len(tools))
	for name, calls := range tools {
		name = strings.TrimSpace(name)
		if name == "" || calls <= 0 {
			continue
		}
		snapshot = append(snapshot, MCPToolUsageSnapshot{Name: name, Calls: calls})
	}
	sort.SliceStable(snapshot, func(i, j int) bool {
		if snapshot[i].Calls != snapshot[j].Calls {
			return snapshot[i].Calls > snapshot[j].Calls
		}
		return snapshot[i].Name < snapshot[j].Name
	})
	return snapshot
}

func codexMCPToolCallInfo(item map[string]json.RawMessage) (serverName, toolName string) {
	if strings.TrimSpace(decodeRawString(item["type"])) != "mcpToolCall" {
		return "", ""
	}
	return strings.TrimSpace(decodeRawString(item["server"])), strings.TrimSpace(decodeRawString(item["tool"]))
}

func (s *appServerSession) recordCodexMCPToolUsageLocked(itemID string, item map[string]json.RawMessage) {
	serverName, toolName := codexMCPToolCallInfo(item)
	if serverName == "" {
		return
	}
	itemID = strings.TrimSpace(itemID)
	if itemID != "" {
		if s.mcpUsageItemIDs == nil {
			s.mcpUsageItemIDs = make(map[string]struct{})
		}
		if _, ok := s.mcpUsageItemIDs[itemID]; ok {
			return
		}
		s.mcpUsageItemIDs[itemID] = struct{}{}
	}
	s.mcpUsage = recordMCPToolUsage(s.mcpUsage, serverName, toolName)
}

func openCodeMCPToolCallInfo(rawTool string) (serverName, toolName string) {
	rawTool = strings.TrimSpace(rawTool)
	if rawTool == "" {
		return "", ""
	}
	if normalized := normalizeOpenCodePlaywrightToolName(rawTool); normalized != "" {
		return "playwright", normalized
	}
	lower := strings.ToLower(rawTool)
	const runtimePrefix = "lcr_runtime_"
	if strings.HasPrefix(lower, runtimePrefix) {
		tool := strings.TrimSpace(rawTool[len(runtimePrefix):])
		if tool == "" {
			tool = rawTool
		}
		return "lcr_runtime", tool
	}
	return "", ""
}

func (s *openCodeSession) recordOpenCodeMCPToolUsageLocked(partID, rawTool string) {
	serverName, toolName := openCodeMCPToolCallInfo(rawTool)
	if serverName == "" {
		return
	}
	partID = strings.TrimSpace(partID)
	if partID != "" {
		if s.mcpUsageItemIDs == nil {
			s.mcpUsageItemIDs = make(map[string]struct{})
		}
		if _, ok := s.mcpUsageItemIDs[partID]; ok {
			return
		}
		s.mcpUsageItemIDs[partID] = struct{}{}
	}
	s.mcpUsage = recordMCPToolUsage(s.mcpUsage, serverName, toolName)
}
