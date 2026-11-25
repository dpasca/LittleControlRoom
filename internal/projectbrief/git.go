package projectbrief

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/scanner"
)

const gitCheckTimeout = 700 * time.Millisecond

type gitState struct {
	Head, Branch, Upstream string
	Ahead, Behind          int
	Dirty, HasRemote       bool
}

func readGitState(ctx context.Context, path, family string) *gitState {
	if !inGitFamily(ctx, path, family) {
		return nil
	}
	status, err := scanner.ReadGitCheckoutStatus(ctx, path)
	if err != nil {
		return nil
	}
	return &gitState{Head: status.Head, Branch: status.Branch, Upstream: status.Upstream,
		Ahead: status.Ahead, Behind: status.Behind, Dirty: status.Dirty, HasRemote: status.HasRemote}
}

func inGitFamily(ctx context.Context, path, family string) bool {
	info, err := scanner.ReadGitWorktreeInfo(ctx, path)
	return err == nil && sameDiskPath(info.TopLevelPath, path) && sameDiskPath(info.RootPath, family)
}

func sameDiskPath(left, right string) bool {
	if filepath.Clean(left) == filepath.Clean(right) {
		return true
	}
	resolvedLeft, leftErr := filepath.EvalSymlinks(left)
	resolvedRight, rightErr := filepath.EvalSymlinks(right)
	return leftErr == nil && rightErr == nil && resolvedLeft == resolvedRight
}

func gitOutput(ctx context.Context, path string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", path}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func decisionRef(baseline gitState, check model.ProjectBriefCheck) string {
	switch check.Kind {
	case "pushed":
		return "refs/remotes/" + check.RemoteBranch
	case "merged":
		return "refs/heads/" + check.TargetBranch
	case "branch_gone":
		return "refs/heads/" + baseline.Branch
	}
	return ""
}

func checkDecision(ctx context.Context, path string, baseline gitState, check model.ProjectBriefCheck) (string, error) {
	ref := decisionRef(baseline, check)
	// A recreated, unrelated repository cannot prove anything about the old
	// checkout. Verify its recorded commit before confirming a missing ref.
	if _, err := gitOutput(ctx, path, "cat-file", "-e", baseline.Head+"^{commit}"); err != nil {
		return "", err
	}
	// A symbolic remote ref such as origin/HEAD is not a push destination.
	out, err := gitOutput(ctx, path, "for-each-ref", "--format=%(refname) %(symref)", ref)
	if err != nil {
		return "", err
	}
	exists := false
	for _, line := range strings.Split(out, "\n") {
		name, symbolicTarget, _ := strings.Cut(line, " ")
		if name == ref && (check.Kind != "pushed" || symbolicTarget == "") {
			exists = true
			break
		}
	}
	if check.Kind == "branch_gone" {
		if exists {
			return "", nil
		}
		return fmt.Sprintf("%s is absent from the local repository.", ref), nil
	}
	if !exists {
		return "", nil
	}
	_, err = gitOutput(ctx, path, "merge-base", "--is-ancestor", baseline.Head, ref)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	if check.Kind == "pushed" {
		return fmt.Sprintf("%s contains %s (local remote-tracking ref).", ref, baseline.Head), nil
	}
	return fmt.Sprintf("%s contains %s.", ref, baseline.Head), nil
}
