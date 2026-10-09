// Package aligntest builds the repository layout LCR creates for reused
// submodule worktrees, for tests in this and dependent packages.
package aligntest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var isolatedEnv = map[string]string{
	"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_SYSTEM": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1",
	"GIT_AUTHOR_NAME": "LCR Test", "GIT_AUTHOR_EMAIL": "test@example.com",
	"GIT_COMMITTER_NAME": "LCR Test", "GIT_COMMITTER_EMAIL": "test@example.com",
	"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "protocol.file.allow", "GIT_CONFIG_VALUE_0": "always",
}

// SetProcessEnv isolates every Git invocation in the process, including those
// made by the code under test, from the developer's own configuration. Call it
// from TestMain when tests run in parallel.
func SetProcessEnv() {
	for k, v := range isolatedEnv {
		os.Setenv(k, v)
	}
}

// IsolateEnv does the same for one non-parallel test.
func IsolateEnv(t *testing.T) {
	t.Helper()
	for k, v := range isolatedEnv {
		t.Setenv(k, v)
	}
}

// Git runs one command and fails the test on error.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = os.Environ()
	for k, v := range isolatedEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// CommitFile writes, stages, and commits one file, returning the new commit.
func CommitFile(t testing.TB, repo, name, content, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	Git(t, repo, "add", name)
	Git(t, repo, "commit", "-q", "-m", message)
	return Git(t, repo, "rev-parse", "HEAD")
}

// Fixture is a canonical parent whose submodule is a normal checkout, plus a
// linked parent worktree whose submodule is a reused linked worktree of the same
// shared repository, left detached at C1.
type Fixture struct {
	Upstream  string
	Canonical string // canonical parent checkout
	Task      string // linked parent worktree
	CanonSub  string // canonical submodule checkout
	TaskSub   string // reused linked submodule worktree
	C1, C2    string // C2 is a child of C1
	C3        string // diverges from C2, child of C1
}

// New builds a Fixture under t.TempDir().
func New(t testing.TB) Fixture {
	t.Helper()
	root := t.TempDir()
	f := Fixture{
		Upstream:  filepath.Join(root, "asset-origin"),
		Canonical: filepath.Join(root, "main"),
		Task:      filepath.Join(root, "main--task"),
	}
	for _, dir := range []string{f.Upstream, f.Canonical} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		Git(t, dir, "init", "-q", "-b", "master")
	}
	f.C1 = CommitFile(t, f.Upstream, "a.txt", "one", "c1")
	Git(t, f.Canonical, "submodule", "add", "-q", f.Upstream, "asset")
	CommitFile(t, f.Canonical, "README", "parent", "pin c1")
	f.CanonSub = filepath.Join(f.Canonical, "asset")

	f.C2 = CommitFile(t, f.Upstream, "b.txt", "two", "c2")
	Git(t, f.Upstream, "checkout", "-q", "-b", "side", f.C1)
	f.C3 = CommitFile(t, f.Upstream, "c.txt", "three", "c3")
	Git(t, f.Upstream, "checkout", "-q", "master")
	Git(t, f.CanonSub, "fetch", "-q", "origin")

	Git(t, f.Canonical, "worktree", "add", "-q", "-b", "task", f.Task)
	f.TaskSub = filepath.Join(f.Task, "asset")
	if err := os.MkdirAll(f.TaskSub, 0o755); err != nil {
		t.Fatal(err)
	}
	Git(t, f.CanonSub, "worktree", "add", "-q", "--detach", f.TaskSub, f.C1)
	return f
}

// Pin makes the linked parent pin commit in HEAD and leaves the reused
// submodule worktree detached at `at`.
func (f Fixture) Pin(t testing.TB, commit, at string) {
	t.Helper()
	Git(t, f.TaskSub, "checkout", "-q", "--detach", commit)
	Git(t, f.Task, "add", "asset")
	if Git(t, f.Task, "status", "--porcelain") != "" {
		Git(t, f.Task, "commit", "-q", "-m", "pin "+commit[:8])
	}
	Git(t, f.TaskSub, "checkout", "-q", "--detach", at)
}

// Head returns the reused submodule worktree's HEAD.
func (f Fixture) Head(t testing.TB) string {
	t.Helper()
	return Git(t, f.TaskSub, "rev-parse", "HEAD")
}
