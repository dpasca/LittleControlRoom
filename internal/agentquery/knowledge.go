package agentquery

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// KnowledgeInstructions stays short enough for startup context. Detailed,
// versioned guidance is loaded on demand through the shared query registry.
const KnowledgeInstructions = "LCR provides built-in operating guidance through the knowledge query domain (knowledge.list and knowledge.get). Before diagnosing submodule dirtiness or changing Git worktree configuration, read the submodule-worktrees topic. LCR can prepare submodules as nested linked worktrees with shared Git configuration; a config write through a task gitdir can affect the canonical checkout and other tasks. Inspect extensions.worktreeConfig and config origins before proposing a repair."

//go:embed knowledge/submodule-worktrees.md
var submoduleWorktreesKnowledge string

type knowledgeTopic struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

func knowledgeTopics() []knowledgeTopic {
	return []knowledgeTopic{{
		ID:      "submodule-worktrees",
		Title:   "LCR submodules and shared worktree configuration",
		Summary: "Understand nested submodule worktrees, diagnose false deletions, and preserve canonical and sibling checkouts when repairing configuration.",
	}}
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
	if args.Topic != "submodule-worktrees" {
		return nil, fmt.Errorf("unknown knowledge topic %q; use knowledge.list", args.Topic)
	}
	return map[string]any{
		"topic": knowledgeTopics()[0], "markdown": submoduleWorktreesKnowledge,
		"source": "internal/agentquery/knowledge/submodule-worktrees.md",
	}, nil
}
