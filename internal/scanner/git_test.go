package scanner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGitRepoStatusOutputAhead(t *testing.T) {
	status := parseGitRepoStatusOutput(`# branch.oid abc123
# branch.head master
# branch.upstream origin/master
# branch.ab +2 -0
1 .M N... 100644 100644 100644 abc123 abc123 README.md
`)

	if !status.Dirty {
		t.Fatalf("expected dirty status")
	}
	if !status.HasRemote || !status.HasUpstream {
		t.Fatalf("expected upstream tracking status, got %#v", status)
	}
	if status.Ahead != 2 || status.Behind != 0 {
		t.Fatalf("unexpected ahead/behind counts: %#v", status)
	}
	if status.Branch != "master" {
		t.Fatalf("branch = %q, want master", status.Branch)
	}
	if got := len(status.Changes); got != 1 {
		t.Fatalf("changes len = %d, want 1", got)
	}
	if got := status.Changes[0].Code; got != "M" {
		t.Fatalf("change code = %q, want M", got)
	}
	if !status.Changes[0].Unstaged || status.Changes[0].Staged {
		t.Fatalf("expected unstaged-only change, got %#v", status.Changes[0])
	}
}

func TestParseGitRepoStatusOutputDivergedClean(t *testing.T) {
	status := parseGitRepoStatusOutput(`# branch.oid abc123
# branch.head master
# branch.upstream origin/master
# branch.ab +3 -1
`)

	if status.Dirty {
		t.Fatalf("expected clean status")
	}
	if !status.HasRemote || !status.HasUpstream {
		t.Fatalf("expected upstream tracking status, got %#v", status)
	}
	if status.Ahead != 3 || status.Behind != 1 {
		t.Fatalf("unexpected ahead/behind counts: %#v", status)
	}
}

func TestParseGitRepoStatusOutputSeparatesStagedAndUntracked(t *testing.T) {
	status := parseGitRepoStatusOutput(`# branch.oid abc123
# branch.head master
# branch.upstream origin/master
# branch.ab +0 -0
1 M. N... 100644 100644 100644 abc123 abc123 README.md
? notes.txt
`)

	staged := status.StagedChanges()
	if len(staged) != 1 || staged[0].Path != "README.md" {
		t.Fatalf("staged changes = %#v, want README.md", staged)
	}

	untracked := status.UntrackedChanges()
	if len(untracked) != 1 || untracked[0].Path != "notes.txt" {
		t.Fatalf("untracked changes = %#v, want notes.txt", untracked)
	}
	if !untracked[0].Unstaged {
		t.Fatalf("expected untracked file to count as unstaged: %#v", untracked[0])
	}
}

func TestParseGitRepoStatusOutputRenamedPath(t *testing.T) {
	status := parseGitRepoStatusOutput(`# branch.oid abc123
# branch.head master
# branch.upstream origin/master
# branch.ab +0 -0
2 R. N... 100644 100644 100644 abc123 abc123 R100 old.txt	new.txt
`)

	if len(status.Changes) != 1 {
		t.Fatalf("changes len = %d, want 1", len(status.Changes))
	}
	change := status.Changes[0]
	if change.Path != "new.txt" || change.OriginalPath != "old.txt" {
		t.Fatalf("rename paths = %#v, want old.txt -> new.txt", change)
	}
	if change.Kind != GitChangeRenamed {
		t.Fatalf("rename kind = %s, want %s", change.Kind, GitChangeRenamed)
	}
	if !change.Staged || change.Unstaged {
		t.Fatalf("expected staged-only rename, got %#v", change)
	}
}

func TestParseGitRepoStatusOutputDirtySubmoduleOnlyNeedsLocalAttention(t *testing.T) {
	status := parseGitRepoStatusOutput(`# branch.oid abc123
# branch.head master
1 .M S.M. 160000 160000 160000 abc123 abc123 assets_src
`)

	if len(status.Changes) != 1 {
		t.Fatalf("changes len = %d, want 1", len(status.Changes))
	}
	change := status.Changes[0]
	if !change.IsSubmodule {
		t.Fatalf("expected submodule change, got %#v", change)
	}
	if !change.SubmoduleModified || change.SubmoduleCommitChanged || change.SubmoduleUntracked {
		t.Fatalf("unexpected submodule state flags: %#v", change)
	}
	if change.ParentCommitEligible() {
		t.Fatalf("dirty-only submodule worktree should not be parent-commit eligible: %#v", change)
	}
}

func TestParseGitRepoStatusOutputSubmoduleCommitChangeIsParentCommitEligible(t *testing.T) {
	status := parseGitRepoStatusOutput(`# branch.oid abc123
# branch.head master
1 .M SC.. 160000 160000 160000 abc123 def456 assets_src
`)

	if len(status.Changes) != 1 {
		t.Fatalf("changes len = %d, want 1", len(status.Changes))
	}
	change := status.Changes[0]
	if !change.IsSubmodule || !change.SubmoduleCommitChanged {
		t.Fatalf("expected submodule commit change, got %#v", change)
	}
	if !change.ParentCommitEligible() {
		t.Fatalf("submodule commit change should be parent-commit eligible: %#v", change)
	}
}

func TestReadGitWorktreeInfoFallsBackToGitFileForStaleLinkedWorktree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	worktreePath := filepath.Join(root, "repo--feature")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir worktree path: %v", err)
	}

	gitDir := filepath.Join(repoPath, ".git", "worktrees", "repo--feature")
	if err := os.WriteFile(filepath.Join(worktreePath, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatalf("write gitfile: %v", err)
	}

	info, err := ReadGitWorktreeInfo(context.Background(), worktreePath)
	if err != nil {
		t.Fatalf("ReadGitWorktreeInfo() error = %v", err)
	}
	if info.RootPath != repoPath {
		t.Fatalf("RootPath = %q, want %q", info.RootPath, repoPath)
	}
	if info.TopLevelPath != worktreePath {
		t.Fatalf("TopLevelPath = %q, want %q", info.TopLevelPath, worktreePath)
	}
	if info.Kind != GitWorktreeKindLinked {
		t.Fatalf("Kind = %q, want %q", info.Kind, GitWorktreeKindLinked)
	}
}

func TestReadGitRepoStatusReportsSubmoduleDirtyAndAhead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	originPath := filepath.Join(root, "assets-origin")
	parentPath := filepath.Join(root, "parent")
	scannerInitGitRepo(t, originPath)
	scannerInitGitRepo(t, parentPath)
	scannerRunGit(t, parentPath, "-c", "protocol.file.allow=always", "submodule", "add", originPath, "assets")
	scannerRunGit(t, parentPath, "commit", "-m", "add submodule")

	submodulePath := filepath.Join(parentPath, "assets")
	scannerRunGit(t, submodulePath, "branch", "--set-upstream-to=origin/master", "master")
	if err := os.WriteFile(filepath.Join(submodulePath, "README.md"), []byte("hello\npushed later\n"), 0o644); err != nil {
		t.Fatalf("write submodule README: %v", err)
	}
	scannerRunGit(t, submodulePath, "add", "README.md")
	scannerRunGit(t, submodulePath, "commit", "-m", "local submodule commit")
	if err := os.WriteFile(filepath.Join(submodulePath, "local.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty submodule file: %v", err)
	}

	status, err := ReadGitRepoStatus(ctx, parentPath)
	if err != nil {
		t.Fatalf("ReadGitRepoStatus() error = %v", err)
	}
	if status.SubmoduleDirtyCount() != 1 {
		t.Fatalf("submodule dirty count = %d, want 1; status = %#v", status.SubmoduleDirtyCount(), status.Submodules)
	}
	if status.SubmoduleUnpushedCount() != 1 {
		t.Fatalf("submodule unpushed count = %d, want 1; status = %#v", status.SubmoduleUnpushedCount(), status.Submodules)
	}
	if len(status.Submodules) != 1 || status.Submodules[0].Path != "assets" || status.Submodules[0].Ahead != 1 {
		t.Fatalf("submodules = %#v, want assets ahead by 1", status.Submodules)
	}
}

func scannerInitGitRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	scannerRunGit(t, path, "init")
	scannerRunGit(t, path, "config", "user.email", "test@example.com")
	scannerRunGit(t, path, "config", "user.name", "Little Control Room Test")
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	scannerRunGit(t, path, "add", "README.md")
	scannerRunGit(t, path, "commit", "-m", "initial")
}

func scannerRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git -C %s %s failed: %v\n%s", dir, strings.Join(args, " "), err, string(out))
	}
}
