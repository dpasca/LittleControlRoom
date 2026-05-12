package boss

import (
	"fmt"
	"strings"
)

func (m *Model) syncAttentionHotkeys() {
	candidates := m.attentionHotkeyCandidates()
	valid := make(map[string]AttentionItem, len(candidates))
	for _, item := range candidates {
		key := attentionItemKey(item)
		if key == "" {
			continue
		}
		if _, exists := valid[key]; !exists {
			valid[key] = item
		}
	}

	slots := make([]AttentionItem, hotProjectLimit)
	used := make(map[string]bool, hotProjectLimit)
	for index, item := range m.attentionHotkeys {
		if index >= len(slots) {
			break
		}
		key := attentionItemKey(item)
		if key == "" || used[key] {
			continue
		}
		if current, ok := valid[key]; ok {
			slots[index] = current
			used[key] = true
		}
	}
	for _, item := range candidates {
		key := attentionItemKey(item)
		if key == "" || used[key] {
			continue
		}
		index := firstEmptyAttentionHotkeySlot(slots)
		if index < 0 {
			break
		}
		slots[index] = item
		used[key] = true
	}
	m.attentionHotkeys = slots
}

func (m Model) attentionHotkeySlots() []AttentionItem {
	if len(m.attentionHotkeys) > 0 {
		return append([]AttentionItem(nil), m.attentionHotkeys...)
	}
	slots := make([]AttentionItem, hotProjectLimit)
	for index, item := range m.attentionHotkeyCandidates() {
		if index >= len(slots) {
			break
		}
		slots[index] = item
	}
	return slots
}

func (m Model) attentionHotkeyCandidates() []AttentionItem {
	items := make([]AttentionItem, 0, len(m.snapshot.OpenAgentTasks)+len(m.snapshot.HotProjects))
	seen := map[string]bool{}
	for _, task := range m.snapshot.OpenAgentTasks {
		item := attentionItemForAgentTask(task)
		if appendAttentionCandidate(seen, item) {
			items = append(items, item)
		}
	}
	for _, project := range m.snapshot.HotProjects {
		item := attentionItemForProject(project)
		if appendAttentionCandidate(seen, item) {
			items = append(items, item)
		}
	}
	return items
}

func appendAttentionCandidate(seen map[string]bool, item AttentionItem) bool {
	key := attentionItemKey(item)
	if key == "" || seen[key] {
		return false
	}
	seen[key] = true
	return true
}

func attentionItemForAgentTask(task AgentTaskBrief) AttentionItem {
	return AttentionItem{
		Kind:   AttentionItemAgentTask,
		TaskID: strings.TrimSpace(task.ID),
	}
}

func attentionItemForProject(project ProjectBrief) AttentionItem {
	return AttentionItem{
		Kind:        AttentionItemProject,
		ProjectPath: strings.TrimSpace(project.Path),
	}
}

func attentionItemForEngineerActivity(activity ViewEngineerActivity) AttentionItem {
	if strings.TrimSpace(activity.Kind) == string(AttentionItemAgentTask) && strings.TrimSpace(activity.TaskID) != "" {
		return AttentionItem{
			Kind:   AttentionItemAgentTask,
			TaskID: strings.TrimSpace(activity.TaskID),
		}
	}
	if strings.TrimSpace(activity.ProjectPath) != "" {
		return AttentionItem{
			Kind:        AttentionItemProject,
			ProjectPath: strings.TrimSpace(activity.ProjectPath),
		}
	}
	return AttentionItem{}
}

func (m Model) attentionHotkeyLabel(item AttentionItem) string {
	index, ok := m.attentionHotkeyIndex(item)
	if !ok {
		return ""
	}
	return fmt.Sprintf("Alt+%d", index+1)
}

func (m Model) attentionHotkeyIndex(item AttentionItem) (int, bool) {
	key := attentionItemKey(item)
	if key == "" {
		return 0, false
	}
	for index, slot := range m.attentionHotkeySlots() {
		if attentionItemKey(slot) == key {
			return index, true
		}
	}
	return 0, false
}

func firstEmptyAttentionHotkeySlot(slots []AttentionItem) int {
	for index, item := range slots {
		if attentionItemKey(item) == "" {
			return index
		}
	}
	return -1
}

func attentionItemKey(item AttentionItem) string {
	switch item.Kind {
	case AttentionItemAgentTask:
		taskID := strings.TrimSpace(item.TaskID)
		if taskID == "" {
			return ""
		}
		return string(item.Kind) + ":" + taskID
	case AttentionItemProject:
		projectPath := strings.TrimSpace(item.ProjectPath)
		if projectPath == "" {
			return ""
		}
		return string(item.Kind) + ":" + projectPath
	default:
		return ""
	}
}
