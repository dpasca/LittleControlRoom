package helpmeta

import (
	"strings"
	"testing"

	"lcroom/internal/codexslash"
	"lcroom/internal/commands"
	"lcroom/internal/control"
	"lcroom/internal/slashcmd"
)

func TestTopicsIncludePublicSlashCommands(t *testing.T) {
	topics := topicsByID(Topics())
	assertPublicCommandTopics(t, topics, SurfaceMainTUI, commands.Specs())
	assertPublicCommandTopics(t, topics, SurfaceEmbeddedEngineer, codexslash.Specs())
}

func TestTopicsIncludeControlCapabilities(t *testing.T) {
	topics := topicsByID(Topics())
	for _, name := range control.CapabilityNameValues() {
		capability, ok := control.CapabilityByName(name)
		if !ok {
			t.Fatalf("control capability %q is listed in CapabilityNameValues but missing from CapabilityByName", name)
		}
		id := CapabilityTopicID(name)
		topic, ok := topics[id]
		if !ok {
			t.Fatalf("missing help topic %q for control capability %q", id, name)
		}
		if topic.Kind != TopicKindCapability || topic.Surface != SurfaceControl {
			t.Fatalf("topic %q kind/surface = %q/%q, want capability/control", id, topic.Kind, topic.Surface)
		}
		if strings.TrimSpace(topic.Summary) == "" {
			t.Fatalf("topic %q has empty summary", id)
		}
		if len(topic.CanDoVia) != 1 || topic.CanDoVia[0] != capability.Name {
			t.Fatalf("topic %q CanDoVia = %#v, want [%q]", id, topic.CanDoVia, capability.Name)
		}
	}
}

func TestTopicsIncludeCuratedWorkflowsAndKeybindings(t *testing.T) {
	topics := topicsByID(Topics())
	for _, tc := range []struct {
		id      string
		kind    TopicKind
		surface Surface
	}{
		{TopicID(SurfaceMainTUI, TopicKindKeybinding, "help-chat"), TopicKindKeybinding, SurfaceMainTUI},
		{TopicID(SurfaceMainTUI, TopicKindKeybinding, "project-todos"), TopicKindKeybinding, SurfaceMainTUI},
		{TopicID(SurfaceMainTUI, TopicKindWorkflow, "start-todo-work"), TopicKindWorkflow, SurfaceMainTUI},
		{TopicID(SurfaceMainTUI, TopicKindWorkflow, "worktree-lanes"), TopicKindWorkflow, SurfaceMainTUI},
		{TopicID(SurfaceMainTUI, TopicKindWorkflow, "worktree-merge-back"), TopicKindWorkflow, SurfaceMainTUI},
		{TopicID(SurfaceMainTUI, TopicKindWorkflow, "merge-conflict-recovery"), TopicKindWorkflow, SurfaceMainTUI},
	} {
		topic, ok := topics[tc.id]
		if !ok {
			t.Fatalf("missing curated help topic %q", tc.id)
		}
		if topic.Kind != tc.kind || topic.Surface != tc.surface {
			t.Fatalf("topic %q kind/surface = %q/%q, want %q/%q", tc.id, topic.Kind, topic.Surface, tc.kind, tc.surface)
		}
		if strings.TrimSpace(topic.Summary) == "" {
			t.Fatalf("topic %q has empty summary", tc.id)
		}
		if len(topic.ManualSteps) == 0 {
			t.Fatalf("topic %q should include manual steps", tc.id)
		}
		if len(topic.SourceRefs) == 0 {
			t.Fatalf("topic %q has no source refs", tc.id)
		}
	}
}

func TestTopicsHaveUniqueIDs(t *testing.T) {
	seen := map[string]struct{}{}
	for _, topic := range Topics() {
		if strings.TrimSpace(topic.ID) == "" {
			t.Fatalf("topic has empty ID: %#v", topic)
		}
		if _, ok := seen[topic.ID]; ok {
			t.Fatalf("duplicate help topic ID %q", topic.ID)
		}
		seen[topic.ID] = struct{}{}
	}
}

func TestTopicByIDReturnsDefensiveCopy(t *testing.T) {
	id := CommandTopicID(SurfaceMainTUI, "help")
	topic, ok := TopicByID(id)
	if !ok {
		t.Fatalf("TopicByID(%q) not found", id)
	}
	topic.Usage[0] = "/changed"

	again, ok := TopicByID(id)
	if !ok {
		t.Fatalf("TopicByID(%q) missing after mutation", id)
	}
	if again.Usage[0] == "/changed" {
		t.Fatalf("TopicByID(%q) returned shared mutable topic slices", id)
	}
}

func assertPublicCommandTopics(t *testing.T, topics map[string]Topic, surface Surface, specs []slashcmd.Spec) {
	t.Helper()
	for _, spec := range specs {
		if spec.Hidden {
			continue
		}
		id := CommandTopicID(surface, spec.Name)
		topic, ok := topics[id]
		if !ok {
			t.Fatalf("missing help topic %q for public %s slash command /%s", id, surface, spec.Name)
		}
		if topic.Kind != TopicKindCommand || topic.Surface != surface {
			t.Fatalf("topic %q kind/surface = %q/%q, want command/%s", id, topic.Kind, topic.Surface, surface)
		}
		if strings.TrimSpace(topic.Title) != strings.TrimSpace(spec.Usage) {
			t.Fatalf("topic %q title = %q, want usage %q", id, topic.Title, spec.Usage)
		}
		if strings.TrimSpace(topic.Summary) != strings.TrimSpace(spec.Summary) {
			t.Fatalf("topic %q summary = %q, want %q", id, topic.Summary, spec.Summary)
		}
		if len(topic.Usage) == 0 || strings.TrimSpace(topic.Usage[0]) == "" {
			t.Fatalf("topic %q has no usage", id)
		}
		if len(topic.SourceRefs) == 0 {
			t.Fatalf("topic %q has no source refs", id)
		}
	}
}

func topicsByID(topics []Topic) map[string]Topic {
	out := make(map[string]Topic, len(topics))
	for _, topic := range topics {
		out[topic.ID] = topic
	}
	return out
}
