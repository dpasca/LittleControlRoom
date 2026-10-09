package submodulealign

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/submodulealign/aligntest"
)

func request(f aligntest.Fixture) Request {
	return Request{ParentPath: f.Task, SubmodulePath: "asset"}
}

func TestMain(m *testing.M) {
	aligntest.SetProcessEnv()
	os.Exit(m.Run())
}

func mustRefuse(t *testing.T, err error, code string) *Refusal {
	t.Helper()
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != code {
		t.Fatalf("error = %v, want refusal %q", err, code)
	}
	return refusal
}

func TestInspectReportsCleanFastForwardAsStandingEligible(t *testing.T) {
	t.Parallel()
	f := aligntest.New(t)
	f.Pin(t, f.C2, f.C1)

	plan, err := Inspect(context.Background(), request(f))
	if err != nil {
		t.Fatal(err)
	}
	if plan.CurrentHead != f.C1 || plan.TargetCommit != f.C2 || plan.TargetSource != TargetHeadGitlink {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Ancestry != AncestryFastForward || plan.TargetAhead != 1 || plan.TargetBehind != 0 || !plan.StandingEligible || plan.NeedsFetch {
		t.Fatalf("plan = %+v", plan)
	}
	if !strings.HasSuffix(filepath.ToSlash(plan.AdminDir), "/modules/asset/worktrees/"+filepath.Base(plan.AdminDir)) {
		t.Fatalf("admin dir = %s", plan.AdminDir)
	}
	if preview := plan.Preview(); !strings.Contains(preview, "fast-forward (+1 commits)") || !strings.Contains(preview, "eligible") {
		t.Fatalf("preview = %s", preview)
	}
}

func TestAlignMovesOnlyTheLinkedWorktreeAndVerifies(t *testing.T) {
	t.Parallel()
	f := aligntest.New(t)
	f.Pin(t, f.C2, f.C1)
	canonHead := aligntest.Git(t, f.CanonSub, "rev-parse", "HEAD")
	configBefore := aligntest.Git(t, f.TaskSub, "config", "--show-origin", "--list")

	result, err := Align(context.Background(), request(f), Options{RequireStandingEligibility: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Fetched || result.PreviousHead != f.C1 || result.Head != f.C2 || result.Ancestry != AncestryFastForward {
		t.Fatalf("result = %+v", result)
	}
	if v := result.Verification; !v.HeadMatchesTarget || !v.Detached || v.ParentGitlinkDrift != "none" || !v.ConfigUnchanged {
		t.Fatalf("verification = %+v", v)
	}
	if got := f.Head(t); got != f.C2 {
		t.Fatalf("worktree HEAD = %s, want %s", got, f.C2)
	}
	if got := aligntest.Git(t, f.CanonSub, "rev-parse", "HEAD"); got != canonHead {
		t.Fatalf("canonical submodule HEAD moved: %s -> %s", canonHead, got)
	}
	if got := aligntest.Git(t, f.Task, "status", "--porcelain"); got != "" {
		t.Fatalf("parent status = %q", got)
	}
	if got := aligntest.Git(t, f.TaskSub, "config", "--show-origin", "--list"); got != configBefore {
		t.Fatalf("configuration changed:\n%s\n---\n%s", configBefore, got)
	}
}

func TestAlignAlreadyAlignedIsANoOp(t *testing.T) {
	t.Parallel()
	f := aligntest.New(t)
	result, err := Align(context.Background(), request(f), Options{RequireStandingEligibility: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || result.Ancestry != AncestryIdentical || result.Head != f.C1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestAlignUsesStagedGitlinkOverHead(t *testing.T) {
	t.Parallel()
	f := aligntest.New(t)
	aligntest.Git(t, f.TaskSub, "checkout", "-q", "--detach", f.C2)
	aligntest.Git(t, f.Task, "add", "asset")
	aligntest.Git(t, f.TaskSub, "checkout", "-q", "--detach", f.C1)

	plan, err := Inspect(context.Background(), request(f))
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetCommit != f.C2 || plan.TargetSource != TargetIndexGitlink || plan.PinnedCommit != f.C2 {
		t.Fatalf("plan = %+v", plan)
	}
	if _, err := Align(context.Background(), request(f), Options{}); err != nil {
		t.Fatal(err)
	}
	if got := f.Head(t); got != f.C2 {
		t.Fatalf("HEAD = %s", got)
	}
}

func TestBackwardAndDivergedAreReportedAndNotStandingEligible(t *testing.T) {
	t.Parallel()
	f := aligntest.New(t)
	f.Pin(t, f.C1, f.C2)
	plan, err := Inspect(context.Background(), request(f))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ancestry != AncestryBackward || plan.TargetBehind != 1 || plan.StandingEligible || len(plan.Warnings) == 0 {
		t.Fatalf("backward plan = %+v", plan)
	}
	_, err = Align(context.Background(), request(f), Options{RequireStandingEligibility: true})
	mustRefuse(t, err, RefusalNotStandingEligible)
	if got := f.Head(t); got != f.C2 {
		t.Fatalf("standing refusal moved HEAD to %s", got)
	}

	f.Pin(t, f.C2, f.C3)
	plan, err = Inspect(context.Background(), request(f))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ancestry != AncestryDiverged || plan.TargetAhead != 1 || plan.TargetBehind != 1 || plan.StandingEligible {
		t.Fatalf("diverged plan = %+v", plan)
	}
	// An operator who reviewed the plan can still confirm the move.
	if _, err := Align(context.Background(), request(f), Options{}); err != nil {
		t.Fatal(err)
	}
	if got := f.Head(t); got != f.C2 {
		t.Fatalf("HEAD = %s, want %s", got, f.C2)
	}
}

func TestExplicitTargetDifferentFromPinIsExpectedDrift(t *testing.T) {
	t.Parallel()
	f := aligntest.New(t)
	f.Pin(t, f.C2, f.C2)
	req := request(f)
	req.TargetCommit = f.C1

	plan, err := Inspect(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetSource != TargetExplicit || plan.TargetIsPinned || plan.StandingEligible {
		t.Fatalf("plan = %+v", plan)
	}
	result, err := Align(context.Background(), req, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verification.ParentGitlinkDrift != "expected_commit_change" {
		t.Fatalf("verification = %+v", result.Verification)
	}
	if _, err := Align(context.Background(), req, Options{RequireStandingEligibility: true}); err == nil {
		t.Fatal("standing permission applied to an explicit non-pinned target")
	}
}

func TestDirtyCheckoutsAreRefused(t *testing.T) {
	t.Parallel()
	for name, dirty := range map[string]func(t *testing.T, f aligntest.Fixture){
		"untracked": func(t *testing.T, f aligntest.Fixture) {
			os.WriteFile(filepath.Join(f.TaskSub, "scratch.txt"), []byte("x"), 0o644)
		},
		"unstaged": func(t *testing.T, f aligntest.Fixture) {
			os.WriteFile(filepath.Join(f.TaskSub, "a.txt"), []byte("changed"), 0o644)
		},
		"staged": func(t *testing.T, f aligntest.Fixture) {
			os.WriteFile(filepath.Join(f.TaskSub, "a.txt"), []byte("changed"), 0o644)
			aligntest.Git(t, f.TaskSub, "add", "a.txt")
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := aligntest.New(t)
			f.Pin(t, f.C2, f.C1)
			dirty(t, f)
			_, err := Align(context.Background(), request(f), Options{})
			refusal := mustRefuse(t, err, RefusalDirty)
			if !strings.Contains(refusal.Reason, name) {
				t.Fatalf("reason %q does not name %s", refusal.Reason, name)
			}
			if got := f.Head(t); got != f.C1 {
				t.Fatalf("dirty checkout was moved to %s", got)
			}
		})
	}
}

func TestStructuralPreconditions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("canonical submodule", func(t *testing.T) {
		t.Parallel()
		f := aligntest.New(t)
		_, err := Inspect(ctx, Request{ParentPath: f.Canonical, SubmodulePath: "asset"})
		mustRefuse(t, err, RefusalNotLinkedWorktree)
	})
	t.Run("standalone clone", func(t *testing.T) {
		t.Parallel()
		f := aligntest.New(t)
		aligntest.Git(t, f.CanonSub, "worktree", "remove", "--force", f.TaskSub)
		aligntest.Git(t, f.Task, "-c", "protocol.file.allow=always", "clone", "-q", f.Upstream, f.TaskSub)
		_, err := Inspect(ctx, request(f))
		mustRefuse(t, err, RefusalNotLinkedWorktree)
	})
	t.Run("uninitialized", func(t *testing.T) {
		t.Parallel()
		f := aligntest.New(t)
		aligntest.Git(t, f.CanonSub, "worktree", "remove", "--force", f.TaskSub)
		os.MkdirAll(f.TaskSub, 0o755)
		_, err := Inspect(ctx, request(f))
		mustRefuse(t, err, RefusalNotInitialized)
	})
	t.Run("not a gitlink", func(t *testing.T) {
		t.Parallel()
		f := aligntest.New(t)
		_, err := Inspect(ctx, Request{ParentPath: f.Task, SubmodulePath: "README"})
		mustRefuse(t, err, RefusalNotGitlink)
	})
	t.Run("parent is not a checkout root", func(t *testing.T) {
		t.Parallel()
		f := aligntest.New(t)
		_, err := Inspect(ctx, Request{ParentPath: f.TaskSub, SubmodulePath: "x"})
		mustRefuse(t, err, RefusalNotGitlink)
		_, err = Inspect(ctx, Request{ParentPath: filepath.Join(f.Task, "nowhere"), SubmodulePath: "asset"})
		mustRefuse(t, err, RefusalParentNotCheckout)
	})
	t.Run("branch checked out", func(t *testing.T) {
		t.Parallel()
		f := aligntest.New(t)
		aligntest.Git(t, f.TaskSub, "checkout", "-q", "-b", "feature")
		_, err := Inspect(ctx, request(f))
		mustRefuse(t, err, RefusalBranchCheckedOut)
	})
	t.Run("operation in progress", func(t *testing.T) {
		t.Parallel()
		f := aligntest.New(t)
		plan, err := Inspect(ctx, request(f))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(plan.AdminDir, "MERGE_HEAD"), []byte(f.C2+"\n"), 0o644)
		_, err = Inspect(ctx, request(f))
		mustRefuse(t, err, RefusalOperationInProgress)
	})
	t.Run("index locked", func(t *testing.T) {
		t.Parallel()
		f := aligntest.New(t)
		plan, err := Inspect(ctx, request(f))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(plan.AdminDir, "index.lock"), nil, 0o644)
		_, err = Inspect(ctx, request(f))
		mustRefuse(t, err, RefusalIndexLocked)
	})
	t.Run("pointers not reciprocal", func(t *testing.T) {
		t.Parallel()
		f := aligntest.New(t)
		plan, err := Inspect(ctx, request(f))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(plan.AdminDir, "gitdir"), []byte(filepath.Join(f.Task, "elsewhere", ".git")+"\n"), 0o644)
		_, err = Inspect(ctx, request(f))
		mustRefuse(t, err, RefusalPointersMismatch)
	})
}

func TestMissingTargetNeedsExplicitFetchPermission(t *testing.T) {
	t.Parallel()
	f := aligntest.New(t)
	c4 := aligntest.CommitFile(t, f.Upstream, "d.txt", "four", "c4")
	aligntest.Git(t, f.Task, "update-index", "--add", "--cacheinfo", "160000,"+c4+",asset")
	aligntest.Git(t, f.Task, "commit", "-q", "-m", "pin c4")
	if exists := exec.Command("git", "-C", f.TaskSub, "cat-file", "-e", c4+"^{commit}").Run(); exists == nil {
		t.Fatal("fixture error: c4 should not be present yet")
	}

	_, err := Inspect(context.Background(), request(f))
	mustRefuse(t, err, RefusalTargetMissing)

	req := request(f)
	req.FetchIfMissing = true
	plan, err := Inspect(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.NeedsFetch || plan.TargetPresent || plan.Ancestry != AncestryUnknown || plan.StandingEligible {
		t.Fatalf("plan = %+v", plan)
	}
	if _, err := Align(context.Background(), req, Options{RequireStandingEligibility: true}); err == nil {
		t.Fatal("standing permission authorized a fetch")
	}
	if got := f.Head(t); got != f.C1 {
		t.Fatalf("HEAD moved to %s before fetch was authorized", got)
	}

	result, err := Align(context.Background(), req, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Fetched || !result.Changed || result.Head != c4 || result.Ancestry != AncestryFastForward {
		t.Fatalf("result = %+v", result)
	}
}

func TestFetchedTargetThatIsNotFastForwardRequiresReview(t *testing.T) {
	t.Parallel()
	f := aligntest.New(t)
	aligntest.Git(t, f.Upstream, "checkout", "-q", "-b", "other", f.C1)
	c5 := aligntest.CommitFile(t, f.Upstream, "e.txt", "five", "c5")
	aligntest.Git(t, f.Upstream, "checkout", "-q", "master")
	aligntest.Git(t, f.Task, "update-index", "--add", "--cacheinfo", "160000,"+c5+",asset")
	aligntest.Git(t, f.Task, "commit", "-q", "-m", "pin c5")
	aligntest.Git(t, f.TaskSub, "checkout", "-q", "--detach", f.C2)

	req := request(f)
	req.FetchIfMissing = true
	_, err := Align(context.Background(), req, Options{})
	mustRefuse(t, err, RefusalRequiresReview)
	if got := f.Head(t); got != f.C2 {
		t.Fatalf("HEAD moved to %s for an unreviewed diverged target", got)
	}
}

func TestNestedGitlinkChangesAreRefused(t *testing.T) {
	t.Parallel()
	f := aligntest.New(t)
	aligntest.Git(t, f.Upstream, "update-index", "--add", "--cacheinfo", "160000,"+f.C1+",vendor/dep")
	aligntest.Git(t, f.Upstream, "commit", "-q", "-m", "c4 adds nested gitlink")
	c4 := aligntest.Git(t, f.Upstream, "rev-parse", "HEAD")
	aligntest.Git(t, f.CanonSub, "fetch", "-q", "origin")
	aligntest.Git(t, f.Task, "update-index", "--add", "--cacheinfo", "160000,"+c4+",asset")
	aligntest.Git(t, f.Task, "commit", "-q", "-m", "pin c4")

	_, err := Inspect(context.Background(), request(f))
	refusal := mustRefuse(t, err, RefusalNestedGitlinks)
	if !strings.Contains(refusal.Reason, "vendor/dep") {
		t.Fatalf("reason = %q", refusal.Reason)
	}
}

func TestNormalizeRequestRejectsUnsafeInput(t *testing.T) {
	t.Parallel()
	good := Request{ParentPath: "/repo", SubmodulePath: "Apps/asset"}
	for _, mutate := range []func(*Request){
		func(r *Request) { r.ParentPath = "relative/path" },
		func(r *Request) { r.ParentPath = "/" },
		func(r *Request) { r.SubmodulePath = "" },
		func(r *Request) { r.SubmodulePath = "." },
		func(r *Request) { r.SubmodulePath = "/etc" },
		func(r *Request) { r.SubmodulePath = "../outside" },
		func(r *Request) { r.SubmodulePath = "a/../../outside" },
		func(r *Request) { r.SubmodulePath = "a/.git/hooks" },
		func(r *Request) { r.TargetCommit = "main" },
		func(r *Request) { r.TargetCommit = "abc123" },
		func(r *Request) { r.TargetCommit = strings.Repeat("g", 40) },
	} {
		req := good
		mutate(&req)
		if _, err := NormalizeRequest(req); err == nil {
			t.Fatalf("accepted %+v", req)
		}
	}
	req := good
	req.SubmodulePath = " Apps//asset/ "
	req.TargetCommit = strings.ToUpper(strings.Repeat("ab", 20))
	got, err := NormalizeRequest(req)
	if err != nil || got.SubmodulePath != "Apps/asset" || got.TargetCommit != strings.Repeat("ab", 20) {
		t.Fatalf("normalized = %+v, %v", got, err)
	}
}

func TestDescribeDirtyAndClassifyDrift(t *testing.T) {
	t.Parallel()
	if got := describeDirty(""); got != "" {
		t.Fatalf("clean = %q", got)
	}
	porcelain := "1 M. N... 100644 100644 100644 aaa bbb staged.txt\n1 .M N... 100644 100644 100644 aaa aaa edited.txt\n? new.txt\n"
	got := describeDirty(porcelain)
	for _, want := range []string{"1 staged", "1 unstaged", "1 untracked", "staged.txt", "edited.txt", "new.txt"} {
		if !strings.Contains(got, want) {
			t.Fatalf("describeDirty = %q, missing %q", got, want)
		}
	}
	line := func(xy, sub string) string {
		return "1 " + xy + " " + sub + " 160000 160000 160000 aaa bbb asset\n"
	}
	for _, tc := range []struct {
		name, porcelain string
		pinned          bool
		want            string
	}{
		{"clean", "", true, "none"},
		{"staged only", line("M.", "S..."), true, "none"},
		{"worktree commit change when pinned", line(".M", "SC.."), true, "worktree M, submodule SC.."},
		{"expected for explicit target", line(".M", "SC.."), false, "expected_commit_change"},
		{"dirty submodule", line(".M", "S.M."), false, "worktree M, submodule S.M."},
		{"unmerged", "u UU S... 160000 160000 160000 160000 a b c asset\n", true, "unmerged entry"},
	} {
		if got := classifyDrift(tc.porcelain, tc.pinned); got != tc.want {
			t.Errorf("%s: classifyDrift = %q, want %q", tc.name, got, tc.want)
		}
	}
}
