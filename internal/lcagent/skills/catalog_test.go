package skills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverIndexesMetadataAndLoadsBody(t *testing.T) {
	root := t.TempDir()
	codexHome := t.TempDir()
	agentsHome := t.TempDir()
	writeSkill(t, filepath.Join(root, ".agents", "skills", "project-skill", "SKILL.md"), "project-skill", "Project workflow", "Project body")
	writeSkill(t, filepath.Join(codexHome, "skills", ".system", "system-skill", "SKILL.md"), "system-skill", "System workflow", "System body")
	writeSkill(t, filepath.Join(agentsHome, "skills", "global-skill", "SKILL.md"), "global-skill", "Global workflow", "Global body")

	catalog, err := Discover(context.Background(), Options{
		WorkspaceRoot: root,
		CodexHome:     codexHome,
		AgentsHome:    agentsHome,
	})
	if err != nil {
		t.Fatalf("discover skills: %v", err)
	}
	if len(catalog.Skills) != 3 {
		t.Fatalf("skill count = %d, want 3: %#v", len(catalog.Skills), catalog.Skills)
	}
	index := catalog.PromptIndex(10)
	for _, want := range []string{"project-skill [project]: Project workflow", "system-skill [codex_system]: System workflow", "global-skill [agents]: Global workflow"} {
		if !strings.Contains(index, want) {
			t.Fatalf("prompt index missing %q:\n%s", want, index)
		}
	}

	loaded, err := catalog.Load("project-skill")
	if err != nil {
		t.Fatalf("load skill: %v", err)
	}
	if loaded.Skill.Source != SourceProjectAgents || !strings.Contains(loaded.Body, "Project body") || loaded.Truncated {
		t.Fatalf("loaded skill = %#v", loaded)
	}
}

func TestDiscoverPrefersProjectSkillDuplicate(t *testing.T) {
	root := t.TempDir()
	codexHome := t.TempDir()
	writeSkill(t, filepath.Join(root, ".agents", "skills", "demo", "SKILL.md"), "demo", "Project copy", "Project body")
	writeSkill(t, filepath.Join(codexHome, "skills", "demo", "SKILL.md"), "demo", "Codex copy", "Codex body")

	catalog, err := Discover(context.Background(), Options{
		WorkspaceRoot: root,
		CodexHome:     codexHome,
	})
	if err != nil {
		t.Fatalf("discover skills: %v", err)
	}
	loaded, err := catalog.Load("demo")
	if err != nil {
		t.Fatalf("load skill: %v", err)
	}
	if loaded.Skill.Source != SourceProjectAgents || !strings.Contains(loaded.Body, "Project body") {
		t.Fatalf("loaded duplicate = %#v", loaded)
	}
}

func TestProjectCodexSkillsRequireOptIn(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, ".codex", "skills", "codex-local", "SKILL.md"), "codex-local", "Local Codex", "Local body")

	catalog, err := Discover(context.Background(), Options{WorkspaceRoot: root})
	if err != nil {
		t.Fatalf("discover without opt-in: %v", err)
	}
	if _, err := catalog.Load("codex-local"); err == nil {
		t.Fatalf("project .codex skill loaded without opt-in")
	}

	catalog, err = Discover(context.Background(), Options{WorkspaceRoot: root, IncludeProjectCodex: true})
	if err != nil {
		t.Fatalf("discover with opt-in: %v", err)
	}
	if _, err := catalog.Load("codex-local"); err != nil {
		t.Fatalf("project .codex skill did not load with opt-in: %v", err)
	}
}

func writeSkill(t *testing.T, path, name, description, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	text := "---\nname: " + name + "\ndescription: " + description + "\n---\n# " + name + "\n\n" + body + "\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
