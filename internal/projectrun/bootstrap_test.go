package projectrun

import "testing"

func TestPackageManagerBootstrapCommandForPnpm(t *testing.T) {
	t.Parallel()

	command, ok := packageManagerBootstrapCommand("pnpm dev")
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if command != "if [ ! -d node_modules ] || [ ! -f pnpm-lock.yaml ]; then pnpm install; fi" {
		t.Fatalf("command = %q, want %q", command, "if [ ! -d node_modules ] || [ ! -f pnpm-lock.yaml ]; then pnpm install; fi")
	}
}

func TestPackageManagerBootstrapCommandForNestedPnpm(t *testing.T) {
	t.Parallel()

	command, ok := packageManagerBootstrapCommand("cd src && pnpm dev")
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if command != "cd src && if [ ! -d node_modules ] || [ ! -f pnpm-lock.yaml ]; then pnpm install; fi" {
		t.Fatalf("command = %q, want %q", command, "cd src && if [ ! -d node_modules ] || [ ! -f pnpm-lock.yaml ]; then pnpm install; fi")
	}
}

func TestPackageManagerBootstrapCommandForCorepackPnpm(t *testing.T) {
	t.Parallel()

	command, ok := packageManagerBootstrapCommand("corepack pnpm dev")
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if command != "if [ ! -d node_modules ] || [ ! -f pnpm-lock.yaml ]; then corepack pnpm install; fi" {
		t.Fatalf("command = %q, want %q", command, "if [ ! -d node_modules ] || [ ! -f pnpm-lock.yaml ]; then corepack pnpm install; fi")
	}
}

func TestPackageManagerBootstrapCommandSkipsExistingInstallCommand(t *testing.T) {
	t.Parallel()

	if command, ok := packageManagerBootstrapCommand("pnpm install"); ok {
		t.Fatalf("ok = true, want false with command %q", command)
	}
	if command, ok := packageManagerBootstrapCommand("corepack pnpm install"); ok {
		t.Fatalf("ok = true, want false with command %q", command)
	}
}

func TestPackageManagerBootstrapCommandSkipsNonPackageManager(t *testing.T) {
	t.Parallel()

	if command, ok := packageManagerBootstrapCommand("go run ."); ok {
		t.Fatalf("ok = true, want false with command %q", command)
	}
}
