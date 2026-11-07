package agentquery

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// KnowledgeInstructions stays short enough for startup context. Detailed,
// versioned guidance is loaded on demand through the shared query registry.
const KnowledgeInstructions = "LCR provides built-in operating guidance through the knowledge query domain (knowledge.list and knowledge.get). Before diagnosing submodule dirtiness or changing Git worktree configuration, read the submodule-worktrees topic. LCR can prepare submodules as nested linked worktrees with shared Git configuration; a config write through a task gitdir can affect the canonical checkout and other tasks. Inspect extensions.worktreeConfig and config origins before proposing a repair. To bring a reused submodule worktree up to the parent's pinned gitlink after a merge or pull, propose the git.submodule_align control; do not run git checkout in the shared submodule yourself. When coordinating owner approvals, release gates or other directors, or after any restart, resume or /handoff of such a session, read the director-coordination topic: the executing director owns one approval location and rereads the whole saved owner answer, including conditional notes, before acting; saved answers and pending operation IDs are reconciled before resending; explicit stops and required confirmations hold, while end_turn with a requested continuation is not a stop; and a messaging or collaboration permission is not itself authorization for external writes that the provider's own approval checks gate."

//go:embed knowledge/submodule-worktrees.md
var submoduleWorktreesKnowledge string

//go:embed knowledge/director-coordination.md
var directorCoordinationKnowledge string

type knowledgeTopic struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

func knowledgeTopics() []knowledgeTopic {
	return []knowledgeTopic{{
		ID:      "submodule-worktrees",
		Title:   "LCR submodules and shared worktree configuration",
		Summary: "Understand nested submodule worktrees, diagnose false deletions, align a reused worktree to the parent's gitlink with git.submodule_align, and preserve canonical and sibling checkouts when repairing configuration.",
	}, {
		ID:      "director-coordination",
		Title:   "Directors, owner approvals and native coordination",
		Summary: "Run owner approvals from the executing director's own board: click, durable wake, reread the whole saved answer and conditions, act within authority, return a receipt; reconcile saved answers and pending operation IDs after restart or handoff without duplicates; honor stops and enforced provider permission checks.",
	}}
}

// knowledgeBody maps an exact topic id to its embedded Markdown and source.
func knowledgeBody(id string) (markdown, source string, ok bool) {
	switch id {
	case "submodule-worktrees":
		return submoduleWorktreesKnowledge, "internal/agentquery/knowledge/submodule-worktrees.md", true
	case "director-coordination":
		return directorCoordinationKnowledge, "internal/agentquery/knowledge/director-coordination.md", true
	}
	return "", "", false
}

func knowledgeCapability(name Name) Capability {
	input := objectSchema(nil, nil)
	description := "List built-in, versioned LCR operating knowledge topics. These are documentation, not live repository state."
	if name == QueryKnowledgeGet {
		input = objectSchema(map[string]any{
			"topic": stringProperty("Exact topic id returned by knowledge.list.", 1),
		}, []string{"topic"})
		description = "Read one built-in LCR operating knowledge topic before investigating or repairing managed infrastructure. No repository or transcript files are read."
	}
	c := collectionCapability(name, DomainKnowledge, ScopeProject, description, SensitivityMetadata, input)
	c.Freshness = "built_in_documentation"
	c.OutputSchema["properties"].(map[string]any)["freshness"] = map[string]any{"type": "string", "enum": []string{c.Freshness}}
	return c
}

func readKnowledge(name Name, raw json.RawMessage) (map[string]any, error) {
	if name == QueryKnowledgeList {
		var args struct{}
		if err := decodeStrict(raw, &args); err != nil {
			return nil, err
		}
		return map[string]any{"topics": knowledgeTopics()}, nil
	}
	var args struct {
		Topic string `json:"topic"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	markdown, source, ok := knowledgeBody(args.Topic)
	if !ok {
		return nil, fmt.Errorf("unknown knowledge topic %q; use knowledge.list", args.Topic)
	}
	for _, topic := range knowledgeTopics() {
		if topic.ID == args.Topic {
			return map[string]any{"topic": topic, "markdown": markdown, "source": source}, nil
		}
	}
	return nil, fmt.Errorf("unknown knowledge topic %q; use knowledge.list", args.Topic)
}
