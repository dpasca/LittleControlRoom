package policy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceResolveAllowsNestedNewPath(t *testing.T) {
	root := t.TempDir()
	w, err := NewWorkspace(root, AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.Resolve("new/dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(w.Root, "new", "dir", "file.txt")
	if got != want {
		t.Fatalf("Resolve = %q, want %q", got, want)
	}
}

func TestWorkspaceResolveDeniesParentEscape(t *testing.T) {
	root := t.TempDir()
	w, err := NewWorkspace(root, AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Resolve("../outside.txt"); err == nil {
		t.Fatal("Resolve ../outside.txt succeeded, want error")
	}
}

func TestWorkspaceResolveReadAllowsAbsolutePath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	inside := filepath.Join(root, "note.txt")
	target := filepath.Join(outside, "note.txt")
	if err := os.WriteFile(target, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := NewWorkspace(root, AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.ResolveRead(target)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Clean(target); got != want {
		t.Fatalf("ResolveRead absolute = %q, want %q", got, want)
	}
	if _, err := w.Resolve(target); err == nil {
		t.Fatal("Resolve absolute succeeded, want write-path denial")
	} else if !IsDenied(err) || !strings.Contains(DenialReason(err), "--admin-write") {
		t.Fatalf("Resolve absolute denial = %v", err)
	}
	got, err = w.Resolve(inside)
	if err != nil {
		t.Fatalf("Resolve workspace absolute denied: %v", err)
	}
	if want := filepath.Clean(inside); got != want {
		t.Fatalf("Resolve workspace absolute = %q, want %q", got, want)
	}
	w.AdminWrite = true
	got, err = w.Resolve(target)
	if err != nil {
		t.Fatalf("Resolve admin absolute denied: %v", err)
	}
	if want := filepath.Clean(target); got != want {
		t.Fatalf("Resolve admin absolute = %q, want %q", got, want)
	}
}

func TestWorkspaceResolveDeniesSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup requires elevated privileges on some Windows hosts")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	w, err := NewWorkspace(root, AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Resolve("outside/file.txt"); err == nil {
		t.Fatal("Resolve through escaping symlink succeeded, want error")
	}
}

func TestAutonomyPatchAndCommandPolicy(t *testing.T) {
	w := Workspace{Root: t.TempDir(), Auto: AutonomyOff}
	if err := w.AllowPatch(); err == nil {
		t.Fatal("AllowPatch with off succeeded, want error")
	}
	if err := w.AllowCommand("git diff"); err != nil {
		t.Fatalf("git diff denied: %v", err)
	}
	if err := w.AllowCommand("rm file.txt"); err == nil {
		t.Fatal("rm allowed with auto off, want error")
	}
	if err := w.AllowCommandSpec([]string{"go", "test", "./..."}, "", false); err == nil {
		t.Fatal("go test allowed with auto off, want low-or-medium denial")
	}
	low := Workspace{Root: t.TempDir(), Auto: AutonomyLow}
	if err := low.AllowCommandSpec([]string{"go", "test", "./..."}, "", false); err != nil {
		t.Fatalf("go test denied with auto low: %v", err)
	}
	if err := low.AllowCommandSpec([]string{"go", "test", "./internal/lcagent/...", "-run", "TestRunner", "-count=1"}, "", false); err != nil {
		t.Fatalf("scoped go test denied with auto low: %v", err)
	}
	if err := low.AllowCommandSpec([]string{"go", "test", "../..."}, "", false); err == nil {
		t.Fatal("go test outside package pattern allowed with auto low, want denial")
	}
	if err := low.AllowCommandSpec([]string{"go", "test", "./...", "-exec", "/bin/true"}, "", false); err == nil {
		t.Fatal("go test -exec allowed with auto low, want denial")
	}
	if err := low.AllowCommandSpec([]string{"go", "test", "./...", "-coverprofile=cover.out"}, "", false); err == nil {
		t.Fatal("go test -coverprofile allowed with auto low, want denial")
	}
	if err := low.AllowCommandSpec([]string{"sed", "-n", "1,20p", "README.md"}, "", false); err != nil {
		t.Fatalf("read-only sed denied with auto low: %v", err)
	}
	if err := low.AllowCommandSpec([]string{"sed", "-i", "s/old/new/", "README.md"}, "", false); err == nil {
		t.Fatal("sed -i allowed with auto low, want denial")
	}
	if err := low.AllowCommandSpec([]string{"sed", "-i.bak", "s/old/new/", "README.md"}, "", false); err == nil {
		t.Fatal("sed -i.bak allowed with auto low, want denial")
	}
	if err := low.AllowCommandSpec([]string{"find", ".", "-delete"}, "", false); err == nil {
		t.Fatal("find -delete allowed with auto low, want denial")
	}
	if err := low.AllowCommandSpec([]string{"git", "branch", "feature"}, "", false); err == nil {
		t.Fatal("git branch create allowed with auto low, want denial")
	}
	if err := low.AllowCommandSpec([]string{"git", "branch", "--show-current"}, "", false); err != nil {
		t.Fatalf("git branch --show-current denied with auto low: %v", err)
	}
	medium := Workspace{Root: t.TempDir(), Auto: AutonomyMedium}
	if err := medium.AllowCommandSpec([]string{"go", "test", "./..."}, "", false); err != nil {
		t.Fatalf("go test denied with auto medium: %v", err)
	}
	if got := ClampTimeout(5*time.Minute, time.Second, 10*time.Second); got != 10*time.Second {
		t.Fatalf("ClampTimeout = %s, want 10s", got)
	}
}

func TestLowAutonomyVerificationCommandPolicy(t *testing.T) {
	low := Workspace{Root: t.TempDir(), Auto: AutonomyLow}
	tests := []struct {
		name  string
		argv  []string
		allow bool
	}{
		{name: "go list packages", argv: []string{"go", "list", "-json", "./..."}, allow: true},
		{name: "go vet packages", argv: []string{"go", "vet", "./..."}, allow: true},
		{name: "go build all packages", argv: []string{"go", "build", "./..."}, allow: true},
		{name: "go build explicit binary output", argv: []string{"go", "build", "-o", "lcagent", "./..."}, allow: false},
		{name: "go build scoped command package", argv: []string{"go", "build", "./cmd/lcagent"}, allow: false},
		{name: "go fmt mutating formatter", argv: []string{"go", "fmt", "./..."}, allow: false},
		{name: "go vet custom vettool", argv: []string{"go", "vet", "-vettool=./tool", "./..."}, allow: false},
		{name: "make test", argv: []string{"make", "test"}, allow: true},
		{name: "make jobs test", argv: []string{"make", "-j4", "test"}, allow: true},
		{name: "make install", argv: []string{"make", "install"}, allow: false},
		{name: "make assignment", argv: []string{"make", "test", "GO=./evil"}, allow: false},
		{name: "npm test", argv: []string{"npm", "test", "--", "--runInBand"}, allow: true},
		{name: "npm typecheck script", argv: []string{"npm", "run", "typecheck", "--", "--noEmit"}, allow: true},
		{name: "npm install", argv: []string{"npm", "install"}, allow: false},
		{name: "npm write flag", argv: []string{"npm", "run", "format", "--", "--write"}, allow: false},
		{name: "npm watch flag", argv: []string{"npm", "run", "test", "--", "--watch=all"}, allow: false},
		{name: "pnpm lint script", argv: []string{"pnpm", "run", "lint"}, allow: true},
		{name: "pnpm exec tsc no emit", argv: []string{"pnpm", "exec", "tsc", "--noEmit"}, allow: true},
		{name: "pnpm exec eslint check", argv: []string{"pnpm", "exec", "eslint", "."}, allow: true},
		{name: "pnpm exec prettier check", argv: []string{"pnpm", "exec", "prettier", "--check", "."}, allow: true},
		{name: "pnpm exec biome check", argv: []string{"pnpm", "exec", "biome", "check", "."}, allow: true},
		{name: "pnpm exec separator tsc no emit", argv: []string{"pnpm", "exec", "--", "tsc", "--noEmit"}, allow: true},
		{name: "pnpm exec tsc emit", argv: []string{"pnpm", "exec", "tsc"}, allow: false},
		{name: "pnpm exec prettier write", argv: []string{"pnpm", "exec", "prettier", "--write", "."}, allow: false},
		{name: "pnpm exec arbitrary command", argv: []string{"pnpm", "exec", "vite", "build"}, allow: false},
		{name: "npm exec remains denied", argv: []string{"npm", "exec", "tsc", "--noEmit"}, allow: false},
		{name: "yarn test script", argv: []string{"yarn", "test"}, allow: true},
		{name: "bun test", argv: []string{"bun", "test"}, allow: true},
		{name: "cargo test", argv: []string{"cargo", "test", "--all"}, allow: true},
		{name: "cargo fmt check", argv: []string{"cargo", "fmt", "--check"}, allow: true},
		{name: "cargo json file reporter", argv: []string{"cargo", "test", "--reporter=json:out.json"}, allow: false},
		{name: "cargo publish", argv: []string{"cargo", "publish"}, allow: false},
		{name: "cargo fmt without check", argv: []string{"cargo", "fmt"}, allow: false},
		{name: "pytest quiet", argv: []string{"pytest", "-q"}, allow: true},
		{name: "pytest parent path", argv: []string{"pytest", "../outside"}, allow: false},
		{name: "pytest cache clear", argv: []string{"pytest", "--cache-clear"}, allow: false},
		{name: "pytest junit output", argv: []string{"pytest", "--junitxml=out.xml"}, allow: false},
		{name: "python module pytest", argv: []string{"python", "-m", "pytest", "tests"}, allow: true},
		{name: "python module unittest", argv: []string{"python", "-m", "unittest"}, allow: true},
		{name: "python compileall", argv: []string{"python", "-m", "compileall", "."}, allow: false},
		{name: "mypy install types", argv: []string{"mypy", "--install-types"}, allow: false},
		{name: "pyright create stub", argv: []string{"pyright", "--createstub", "pkg"}, allow: false},
		{name: "ruff check", argv: []string{"ruff", "check", "."}, allow: true},
		{name: "ruff fix", argv: []string{"ruff", "check", "--fix", "."}, allow: false},
		{name: "ruff format check", argv: []string{"ruff", "format", "--check", "."}, allow: true},
		{name: "prettier check", argv: []string{"prettier", "--check", "."}, allow: true},
		{name: "prettier write", argv: []string{"prettier", "--write", "."}, allow: false},
		{name: "eslint check", argv: []string{"eslint", "."}, allow: true},
		{name: "eslint fix", argv: []string{"eslint", "--fix=true", "."}, allow: false},
		{name: "typescript no emit", argv: []string{"tsc", "--noEmit"}, allow: true},
		{name: "typescript emit", argv: []string{"tsc"}, allow: false},
		{name: "gofmt list", argv: []string{"gofmt", "-l", "internal/lcagent/policy/policy.go"}, allow: true},
		{name: "gofmt write", argv: []string{"gofmt", "-w", "internal/lcagent/policy/policy.go"}, allow: false},
		{name: "biome check", argv: []string{"biome", "check", "."}, allow: true},
		{name: "biome write", argv: []string{"biome", "check", "--write", "."}, allow: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := low.AllowCommandSpec(tt.argv, "", false)
			if tt.allow && err != nil {
				t.Fatalf("AllowCommandSpec(%q) denied: %v", tt.argv, err)
			}
			if !tt.allow && err == nil {
				t.Fatalf("AllowCommandSpec(%q) allowed, want denial", tt.argv)
			}
		})
	}
}

func TestLowAutonomyCommandDenialHints(t *testing.T) {
	low := Workspace{Root: t.TempDir(), Auto: AutonomyLow}
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{
			name: "go test output file",
			argv: []string{"go", "test", "./...", "-coverprofile=cover.out"},
			want: "output-file flags are denied",
		},
		{
			name: "formatter write mode",
			argv: []string{"gofmt", "-w", "main.go"},
			want: "use gofmt -l or gofmt -d",
		},
		{
			name: "package manager install",
			argv: []string{"npm", "install"},
			want: "Dependency, publish, and package-execution commands require medium autonomy",
		},
		{
			name: "pnpm exec unsafe verifier",
			argv: []string{"pnpm", "exec", "tsc"},
			want: "approved local verifier CLIs",
		},
		{
			name: "typescript emits files",
			argv: []string{"tsc"},
			want: "Add --noEmit",
		},
		{
			name: "cargo format mutation",
			argv: []string{"cargo", "fmt"},
			want: "cargo fmt --check",
		},
		{
			name: "parent path",
			argv: []string{"pytest", "../outside"},
			want: "workspace-relative paths",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := low.AllowCommandSpec(tt.argv, "", false)
			if err == nil {
				t.Fatalf("AllowCommandSpec(%q) allowed, want denial", tt.argv)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("denial = %q, want hint %q", err.Error(), tt.want)
			}
		})
	}
}

func TestLowAutonomyShellDenialSuggestsArgvVerification(t *testing.T) {
	low := Workspace{Root: t.TempDir(), Auto: AutonomyLow}
	err := low.AllowCommandSpec(nil, "go test ./...", true)
	if err == nil {
		t.Fatal("shell verification command allowed, want denial")
	}
	for _, want := range []string{"argv-only run_command", `argv=["go","test","./..."]`, "purpose=verify"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("denial = %q, want %q", err.Error(), want)
		}
	}
}
