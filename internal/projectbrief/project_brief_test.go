package projectbrief

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestBriefPrefersOwnCheckoutAndUsesAuthorFreshness(t *testing.T) {
	f := newFixture(t)
	f.record(f.root, "Root account", nil)
	if fallback := f.read(f.linked)[f.linked]; fallback == nil || fallback.CheckoutPath != f.root {
		t.Fatalf("checkout without a brief did not use the family fallback: %#v", fallback)
	}
	f.record(f.linked, "First linked account", nil)
	f.record(f.linked, "Latest linked account", nil)

	briefs := f.read(f.root, f.linked)
	root, brief := briefs[f.root], briefs[f.linked]
	if root == nil || root.CheckoutPath != f.root || root.Now != "Root account" {
		t.Fatalf("newer sibling brief masked root brief: %#v", root)
	}
	if brief == nil || brief.CheckoutPath != f.linked || brief.Now != "Latest linked account" ||
		brief.GitChanged == nil || *brief.GitChanged || brief.NewerSession {
		t.Fatalf("initial brief = %#v", brief)
	}

	rows, err := f.db.ListProjectBriefs(t.Context(), []string{f.linked})
	if err != nil || len(rows) != 1 {
		t.Fatalf("checkout rows = %#v, error %v", rows, err)
	}
	old := rows[0]
	old.RecordedAt = old.RecordedAt.Add(-time.Second)
	if saved, err := f.db.UpsertProjectBrief(t.Context(), old); err != nil || saved {
		t.Fatalf("older concurrent report was applied: saved %v, error %v", saved, err)
	}

	f.setSessions(f.linked, []model.SessionEvidence{{
		SessionID: "thread", Source: model.SessionSourceCodex, LastEventAt: brief.RecordedAt.Add(2 * time.Second),
	}})
	if f.read(f.linked)[f.linked].NewerSession {
		t.Fatal("author's final events counted as a newer session")
	}
	f.setSessions(f.linked, []model.SessionEvidence{{
		SessionID: "thread", Source: model.SessionSourceCodex, LastEventAt: brief.RecordedAt.Add(4 * time.Second),
	}, {
		SessionID: "other", Source: model.SessionSourceOpenCode, LastEventAt: brief.RecordedAt.Add(3 * time.Second),
	}})
	if err := os.WriteFile(filepath.Join(f.linked, "local-change"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	briefs = f.read(f.root, f.linked)
	if root = briefs[f.root]; root.NewerSession || root.GitChanged == nil || *root.GitChanged {
		t.Fatalf("sibling activity or Git changes affected root brief: %#v", root)
	}
	brief = briefs[f.linked]
	if !brief.NewerSession || brief.GitChanged == nil || !*brief.GitChanged {
		t.Fatalf("updated freshness = %#v", brief)
	}
}

func TestGitDecisionChecksPinSourceAndDestination(t *testing.T) {
	t.Run("push", func(t *testing.T) {
		f := newFixture(t)
		head := f.git(f.linked, "rev-parse", "HEAD")
		f.git(f.root, "update-ref", "refs/remotes/origin/old-name", head)
		f.record(f.linked, "Awaiting publication", &model.ProjectBriefCheck{Kind: "pushed", RemoteBranch: "origin/feature"})
		f.expectOpen()
		f.git(f.root, "update-ref", "refs/remotes/origin/feature", head)
		f.expectAnswered("refs/remotes/origin/feature contains " + head)
	})
	t.Run("merge", func(t *testing.T) {
		f := newFixture(t)
		head := f.git(f.linked, "rev-parse", "HEAD")
		f.record(f.linked, "Awaiting integration", &model.ProjectBriefCheck{Kind: "merged", TargetBranch: "base"})
		f.expectOpen()
		f.git(f.root, "update-ref", "refs/heads/base", head)
		f.expectAnswered("refs/heads/base contains " + head)
	})
	t.Run("branch gone with checkout missing", func(t *testing.T) {
		f := newFixture(t)
		f.record(f.linked, "Awaiting branch removal", &model.ProjectBriefCheck{Kind: "branch_gone"})
		f.expectOpen()
		f.git(f.root, "worktree", "remove", f.linked)
		f.git(f.root, "update-ref", "-d", "refs/heads/feature")
		f.expectAnswered("refs/heads/feature is absent")
	})
}

type fixture struct {
	t            *testing.T
	root, linked string
	db           *store.Store
}

func newFixture(t *testing.T) *fixture {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "briefs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &fixture{t: t, root: filepath.Join(dir, "root"), linked: filepath.Join(dir, "linked"), db: db}
	f.git(dir, "init", "-b", "base", f.root)
	tree := f.git(f.root, "mktree")
	base := f.git(f.root, "commit-tree", tree, "-m", "fixture base")
	feature := f.git(f.root, "commit-tree", tree, "-p", base, "-m", "fixture feature")
	f.git(f.root, "update-ref", "refs/heads/base", base)
	f.git(f.root, "update-ref", "refs/heads/feature", feature)
	f.git(f.root, "worktree", "add", f.linked, "feature")
	for _, path := range []string{f.root, f.linked} {
		f.setSessions(path, nil)
		if err := db.BindEngineerSession(t.Context(), path, model.SessionSourceCodex, "key", "thread"); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fixture) setSessions(path string, sessions []model.SessionEvidence) {
	f.t.Helper()
	state := model.ProjectState{Path: path, Name: filepath.Base(path), InScope: true, PresentOnDisk: true, UpdatedAt: time.Now(), Sessions: sessions}
	if path == f.linked {
		state.WorktreeRootPath, state.WorktreeKind = f.root, model.WorktreeKindLinked
	}
	if err := f.db.UpsertProjectState(f.t.Context(), state); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) git(path string, args ...string) string {
	f.t.Helper()
	cmd := exec.CommandContext(f.t.Context(), "git", append([]string{"-C", path, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) record(path, now string, check *model.ProjectBriefCheck) {
	f.t.Helper()
	writer := Writer{Store: f.db, CheckoutPath: path, Provider: model.SessionSourceCodex, SessionKey: "key"}
	input := model.ProjectBriefText{Goal: "Ship the feature", Now: now, Next: "Review the feature"}
	if check != nil {
		input.Needs = &model.ProjectBriefNeeds{
			Question: "Proceed?", Options: []string{"Proceed", "Wait"}, Recommendation: "Proceed", Default: "Wait", Check: check,
		}
	}
	if report, err := writer.Record(f.t.Context(), input); err != nil || report["saved"] != true {
		f.t.Fatalf("record: %v, %v", report, err)
	}
}

func (f *fixture) read(paths ...string) map[string]*model.ProjectBrief {
	f.t.Helper()
	projects, err := f.db.ListProjects(context.Background(), true)
	if err != nil {
		f.t.Fatal(err)
	}
	return Resolve(f.t.Context(), f.db, projects, paths)
}

func (f *fixture) expectOpen() {
	f.t.Helper()
	brief := f.read(f.root)[f.root]
	if brief == nil || brief.Needs == nil || brief.NeedsStatus != "open" || brief.Answer != "" {
		f.t.Fatalf("expected open decision, got %#v", brief)
	}
}

func (f *fixture) expectAnswered(evidence string) {
	f.t.Helper()
	brief := f.read(f.root)[f.root]
	if brief == nil || brief.Needs != nil || brief.NeedsStatus != "answered" || !strings.Contains(brief.Answer, evidence) ||
		brief.GitChanged == nil || !*brief.GitChanged {
		f.t.Fatalf("expected answered decision with %q, got %#v", evidence, brief)
	}
}
