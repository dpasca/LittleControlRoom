package helpmeta

import (
	"sort"
	"strings"

	"lcroom/internal/bossslash"
	"lcroom/internal/codexslash"
	"lcroom/internal/commands"
	"lcroom/internal/control"
	"lcroom/internal/slashcmd"
)

type TopicKind string

const (
	TopicKindCommand    TopicKind = "command"
	TopicKindCapability TopicKind = "capability"
	TopicKindWorkflow   TopicKind = "workflow"
	TopicKindKeybinding TopicKind = "keybinding"
	TopicKindSetting    TopicKind = "setting"
	TopicKindState      TopicKind = "state_signal"
)

type Surface string

const (
	SurfaceMainTUI          Surface = "main_tui"
	SurfaceEmbeddedEngineer Surface = "embedded_engineer"
	SurfaceBossChat         Surface = "boss_chat"
	SurfaceControl          Surface = "control"
)

type Topic struct {
	ID          string                   `json:"id"`
	Kind        TopicKind                `json:"kind"`
	Surface     Surface                  `json:"surface"`
	Title       string                   `json:"title"`
	Summary     string                   `json:"summary"`
	Usage       []string                 `json:"usage,omitempty"`
	ManualSteps []string                 `json:"manual_steps,omitempty"`
	CanDoVia    []control.CapabilityName `json:"can_do_via,omitempty"`
	Related     []string                 `json:"related,omitempty"`
	SourceRefs  []string                 `json:"source_refs,omitempty"`
}

func Topics() []Topic {
	topics := make([]Topic, 0, len(commands.Specs())+len(codexslash.Specs())+len(bossslash.Specs())+len(control.Capabilities()))
	topics = append(topics, MainCommandTopics()...)
	topics = append(topics, EmbeddedCommandTopics()...)
	topics = append(topics, BossCommandTopics()...)
	topics = append(topics, ControlCapabilityTopics()...)
	sortTopics(topics)
	return cloneTopics(topics)
}

func TopicByID(id string) (Topic, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Topic{}, false
	}
	for _, topic := range Topics() {
		if topic.ID == id {
			return topic, true
		}
	}
	return Topic{}, false
}

func TopicsByKind(kind TopicKind) []Topic {
	kind = TopicKind(strings.TrimSpace(string(kind)))
	if kind == "" {
		return nil
	}
	var out []Topic
	for _, topic := range Topics() {
		if topic.Kind == kind {
			out = append(out, topic)
		}
	}
	return out
}

func MainCommandTopics() []Topic {
	return commandTopics(SurfaceMainTUI, "commands.Specs", commands.Specs())
}

func EmbeddedCommandTopics() []Topic {
	return commandTopics(SurfaceEmbeddedEngineer, "codexslash.Specs", codexslash.Specs())
}

func BossCommandTopics() []Topic {
	return commandTopics(SurfaceBossChat, "bossslash.Specs", bossslash.Specs())
}

func ControlCapabilityTopics() []Topic {
	capabilities := control.Capabilities()
	topics := make([]Topic, 0, len(capabilities))
	for _, capability := range capabilities {
		name := strings.TrimSpace(string(capability.Name))
		if name == "" {
			continue
		}
		topic := Topic{
			ID:          CapabilityTopicID(capability.Name),
			Kind:        TopicKindCapability,
			Surface:     SurfaceControl,
			Title:       name,
			Summary:     strings.TrimSpace(capability.Description),
			Usage:       []string{name},
			ManualSteps: capabilityManualSteps(capability),
			CanDoVia:    []control.CapabilityName{capability.Name},
			Related:     capabilityRelatedTopics(capability),
			SourceRefs:  []string{"control.Capabilities"},
		}
		topics = append(topics, topic)
	}
	sortTopics(topics)
	return cloneTopics(topics)
}

func CommandTopicID(surface Surface, name string) string {
	return strings.TrimSpace(string(surface)) + ".command." + normalizeTopicIDPart(name)
}

func CapabilityTopicID(name control.CapabilityName) string {
	return string(SurfaceControl) + ".capability." + normalizeTopicIDPart(string(name))
}

func commandTopics(surface Surface, sourceRef string, specs []slashcmd.Spec) []Topic {
	topics := make([]Topic, 0, len(specs))
	for _, spec := range specs {
		if spec.Hidden {
			continue
		}
		name := strings.TrimSpace(spec.Name)
		usage := strings.TrimSpace(spec.Usage)
		if name == "" || usage == "" {
			continue
		}
		topics = append(topics, Topic{
			ID:          CommandTopicID(surface, name),
			Kind:        TopicKindCommand,
			Surface:     surface,
			Title:       usage,
			Summary:     strings.TrimSpace(spec.Summary),
			Usage:       []string{usage},
			ManualSteps: []string{"Run " + usage + "."},
			Related:     commandRelatedTopics(surface, name),
			SourceRefs:  []string{strings.TrimSpace(sourceRef)},
		})
	}
	sortTopics(topics)
	return cloneTopics(topics)
}

func commandRelatedTopics(surface Surface, name string) []string {
	switch surface {
	case SurfaceMainTUI:
		switch strings.TrimSpace(name) {
		case "boss":
			return []string{CommandTopicID(SurfaceBossChat, "help")}
		case "codex", "codex-new", "opencode", "opencode-new", "claude", "claude-new", "lcagent", "lcagent-new":
			return []string{CommandTopicID(SurfaceEmbeddedEngineer, "new"), CommandTopicID(SurfaceEmbeddedEngineer, "sessions")}
		}
	case SurfaceEmbeddedEngineer:
		switch strings.TrimSpace(name) {
		case "boss":
			return []string{CommandTopicID(SurfaceMainTUI, "boss")}
		}
	}
	return nil
}

func capabilityManualSteps(capability control.Capability) []string {
	if capability.Confirmation == control.ConfirmationRequired {
		return []string{"Confirm the proposal before Little Control Room changes state or contacts an engineer session."}
	}
	return nil
}

func capabilityRelatedTopics(capability control.Capability) []string {
	switch capability.Name {
	case control.CapabilityEngineerSendPrompt:
		return []string{
			CommandTopicID(SurfaceMainTUI, "codex"),
			CommandTopicID(SurfaceMainTUI, "opencode"),
			CommandTopicID(SurfaceMainTUI, "claude"),
			CommandTopicID(SurfaceMainTUI, "lcagent"),
		}
	case control.CapabilityTodoAdd, control.CapabilityTodoComplete:
		return []string{CommandTopicID(SurfaceMainTUI, "todo")}
	case control.CapabilityGitPrepareCommit:
		return []string{CommandTopicID(SurfaceMainTUI, "commit")}
	case control.CapabilitySettingsUpdate:
		return []string{CommandTopicID(SurfaceMainTUI, "settings"), CommandTopicID(SurfaceMainTUI, "setup")}
	case control.CapabilityProjectArchive, control.CapabilityScratchTaskArchive:
		return []string{CommandTopicID(SurfaceMainTUI, "archive"), CommandTopicID(SurfaceMainTUI, "unarchive"), CommandTopicID(SurfaceMainTUI, "remove")}
	default:
		return nil
	}
}

func normalizeTopicIDPart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "_", "-")
	return value
}

func sortTopics(topics []Topic) {
	sort.SliceStable(topics, func(i, j int) bool {
		if topics[i].Surface != topics[j].Surface {
			return topics[i].Surface < topics[j].Surface
		}
		if topics[i].Kind != topics[j].Kind {
			return topics[i].Kind < topics[j].Kind
		}
		return topics[i].ID < topics[j].ID
	})
}

func cloneTopics(topics []Topic) []Topic {
	out := make([]Topic, len(topics))
	for i, topic := range topics {
		out[i] = cloneTopic(topic)
	}
	return out
}

func cloneTopic(topic Topic) Topic {
	topic.Usage = append([]string(nil), topic.Usage...)
	topic.ManualSteps = append([]string(nil), topic.ManualSteps...)
	topic.CanDoVia = append([]control.CapabilityName(nil), topic.CanDoVia...)
	topic.Related = append([]string(nil), topic.Related...)
	topic.SourceRefs = append([]string(nil), topic.SourceRefs...)
	return topic
}
