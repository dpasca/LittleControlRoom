package codexskills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadInventoryFlagsLocalSkillDuplicateOfSystemSkill(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	userPath := writeSkill(t, root, filepath.Join("skills", "imagegen", "SKILL.md"), "imagegen", "Old image workflow")
	systemPath := writeSkill(t, root, filepath.Join("skills", ".system", "imagegen", "SKILL.md"), "imagegen", "New image workflow")
	_ = writeSkill(t, root, filepath.Join("plugins", "cache", "openai-curated", "gmail", "abc123", "skills", "gmail", "SKILL.md"), "gmail", "Gmail triage")

	oldTime := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	newTime := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(userPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes user skill: %v", err)
	}
	if err := os.Chtimes(systemPath, newTime, newTime); err != nil {
		t.Fatalf("chtimes system skill: %v", err)
	}

	inv, err := LoadInventory(context.Background(), root, newTime)
	if err != nil {
		t.Fatalf("LoadInventory() error = %v", err)
	}
	if len(inv.Skills) != 3 {
		t.Fatalf("skill count = %d, want 3: %#v", len(inv.Skills), inv.Skills)
	}

	var user Skill
	var plugin Skill
	for _, skill := range inv.Skills {
		switch {
		case skill.InvocationName == "imagegen" && skill.Source == SourceUser:
			user = skill
		case skill.InvocationName == "gmail:gmail":
			plugin = skill
		}
	}
	if user.Path == "" {
		t.Fatalf("missing user imagegen skill: %#v", inv.Skills)
	}
	for _, want := range []string{
		"possible stale local duplicate of a system skill",
		"local file is older than the system skill",
		"content differs from the system skill",
	} {
		if !strings.Contains(strings.Join(user.Attention, "\n"), want) {
			t.Fatalf("user attention missing %q: %#v", want, user.Attention)
		}
	}
	if plugin.Source != SourcePlugin || plugin.SourceLabel != "plugin gmail" {
		t.Fatalf("plugin skill = %#v, want plugin gmail", plugin)
	}
}

func TestFormatInventoryReportIncludesReviewAndInstallLists(t *testing.T) {
	t.Parallel()

	inv := Inventory{
		CodexHome: "/tmp/codex",
		Skills: []Skill{
			{
				Name:           "openai-docs",
				InvocationName: "openai-docs",
				Description:    "Official docs",
				Path:           "/tmp/codex/skills/openai-docs/SKILL.md",
				Source:         SourceUser,
				SourceLabel:    "user",
				Attention:      []string{"possible stale local duplicate of a system skill"},
			},
			{
				Name:           "pdf",
				InvocationName: "pdf",
				Description:    "PDF workflows",
				Path:           "/tmp/codex/skills/pdf/SKILL.md",
				Source:         SourceUser,
				SourceLabel:    "user",
			},
		},
	}

	report := FormatInventoryReport(inv, 10)
	for _, want := range []string{
		"Codex skills inventory: 2 skill(s)",
		"Review suggested for 1 skill(s)",
		"openai-docs [user]: possible stale",
		"Installed skills:",
		"pdf [user] PDF workflows",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func writeSkill(t *testing.T, root, rel, name, description string) string {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	content := "---\nname: \"" + name + "\"\ndescription: \"" + description + "\"\n---\n\n# " + name + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}
