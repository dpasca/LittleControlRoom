package submodulealign

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type gitResult struct {
	stdout string
	stderr string
	code   int
}

func (r gitResult) ok() bool { return r.code == 0 }

func (r gitResult) line() string { return strings.TrimSpace(r.stdout) }

func (r gitResult) failure() string {
	text := strings.TrimSpace(r.stderr)
	if text == "" {
		text = strings.TrimSpace(r.stdout)
	}
	return text
}

// hijackingEnv are variables that would redirect `git -C <dir>` to another
// repository. They are dropped so inspection always targets the named path.
var hijackingEnv = map[string]struct{}{
	"GIT_DIR": {}, "GIT_WORK_TREE": {}, "GIT_INDEX_FILE": {}, "GIT_COMMON_DIR": {},
	"GIT_OBJECT_DIRECTORY": {}, "GIT_ALTERNATE_OBJECT_DIRECTORIES": {}, "GIT_NAMESPACE": {},
	"GIT_PREFIX": {}, "GIT_CEILING_DIRECTORIES": {},
}

func gitEnv(readOnly bool) []string {
	var env []string
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); name != "" {
			if _, drop := hijackingEnv[name]; drop {
				continue
			}
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	if readOnly {
		// Inspection must not refresh the index; that would take index.lock.
		env = append(env, "GIT_OPTIONAL_LOCKS=0")
	}
	return env
}

// git runs one command in dir. A non-zero exit is reported through the result;
// the error is reserved for failures to run Git at all and for cancellation.
func git(ctx context.Context, dir string, readOnly bool, args ...string) (gitResult, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv(readOnly)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := gitResult{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && ctx.Err() == nil {
		result.code = exitErr.ExitCode()
		return result, nil
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, fmt.Errorf("run git %s: %w", strings.Join(args, " "), err)
}

// gitOK runs a command that must succeed and returns its trimmed stdout.
func gitOK(ctx context.Context, dir string, readOnly bool, args ...string) (string, error) {
	result, err := git(ctx, dir, readOnly, args...)
	if err != nil {
		return "", err
	}
	if !result.ok() {
		return "", fmt.Errorf("git %s failed in %s: %s", strings.Join(args, " "), dir, result.failure())
	}
	return result.line(), nil
}

func samePath(a, b string) bool {
	return resolvePath(a) == resolvePath(b)
}

func resolvePath(p string) string {
	p = filepath.Clean(strings.TrimSpace(p))
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	return p
}

func pathInside(root, child string) bool {
	rel, err := filepath.Rel(resolvePath(root), resolvePath(child))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
