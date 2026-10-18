package claudecli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func linuxTokenStore(t *testing.T) *subscriptionTokenStore {
	t.Helper()
	store := NewSubscriptionTokenStore(t.TempDir()).(*subscriptionTokenStore)
	store.platform = "linux"
	return store
}

func TestSubscriptionTokenFileLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := linuxTokenStore(t)
	manager := NewSubscriptionTokenManager(store)
	if err := manager.Refresh(ctx); err != nil || manager.Info().Saved {
		t.Fatalf("missing token: info=%+v, err=%v", manager.Info(), err)
	}
	for _, token := range []string{"sk-ant-oat01-test-one", "sk-ant-oat01-test-two"} {
		if err := manager.Save(ctx, token); err != nil {
			t.Fatal(err)
		}
		if err := manager.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
		if !manager.Info().Saved || strings.Contains(fmt.Sprint(manager.Info()), token) {
			t.Fatal("token missing or exposed in public metadata")
		}
		cmd := exec.Command("unused")
		cmd.Env = []string{"CLAUDE_CODE_OAUTH_TOKEN=", "OTHER=value"}
		if err := manager.Apply(cmd); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cmd.Env, []string{"OTHER=value", SubscriptionTokenEnv + "=" + token}) {
			t.Fatal("saved token was not installed in the command environment")
		}
	}
	for path, want := range map[string]os.FileMode{store.dir: 0o700, filepath.Join(store.dir, "claude-oauth-token"): 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("permissions for %s: info=%v, err=%v", path, info, err)
		}
	}
	entries, err := os.ReadDir(store.dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("credential replacement left temporary files")
	}
	if err := manager.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(ctx); err != nil || manager.Info().Saved {
		t.Fatalf("removed token: info=%+v, err=%v", manager.Info(), err)
	}
	if err := manager.Remove(ctx); err != nil {
		t.Fatalf("repeated removal: %v", err)
	}
}

func TestSubscriptionTokenFileRejectsUnsafePermissionsAndSymlinks(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"public file", "public directory", "file symlink", "directory symlink", "oversized", "oversized padding", "empty"} {
		t.Run(kind, func(t *testing.T) {
			store := linuxTokenStore(t)
			ctx := context.Background()
			if err := store.Write(ctx, "sk-ant-oat01-test"); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.dir, "claude-oauth-token")
			var err error
			switch kind {
			case "public file":
				err = os.Chmod(path, 0o644)
			case "public directory":
				err = os.Chmod(store.dir, 0o755)
			case "file symlink":
				target := filepath.Join(store.dir, "target")
				if err = os.Rename(path, target); err == nil {
					err = os.Symlink(target, path)
				}
			case "directory symlink":
				target := store.dir + "-target"
				if err = os.Rename(store.dir, target); err == nil {
					err = os.Symlink(target, store.dir)
				}
			case "oversized":
				err = os.WriteFile(path, []byte(strings.Repeat("x", 16*1024+2)), 0o600)
			case "oversized padding":
				err = os.WriteFile(path, []byte("short"+strings.Repeat(" ", 16*1024+2)), 0o600)
			case "empty":
				err = os.WriteFile(path, nil, 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			manager := NewSubscriptionTokenManager(store)
			if err := manager.Refresh(ctx); err == nil {
				t.Fatal("unsafe credential storage was accepted")
			}
			if manager.Info().Saved || manager.Info().Error == "" {
				t.Fatal("failed credential read was hidden")
			}
			cmd := exec.Command("unused")
			cmd.Env = []string{"OTHER=value"}
			if err := manager.Apply(cmd); err == nil {
				t.Fatal("credential read failure silently fell back to another login")
			}
			for _, override := range []string{
				SubscriptionTokenEnv + "=explicit-token",
				"ANTHROPIC_API_KEY=explicit-key",
				"ANTHROPIC_AUTH_TOKEN=explicit-bearer",
				"ANTHROPIC_PROFILE=work",
				"CLAUDE_CODE_USE_BEDROCK=1",
				"CLAUDE_CODE_USE_VERTEX=true",
				"CLAUDE_CODE_USE_FOUNDRY=1",
			} {
				cmd.Env = []string{override}
				if err := manager.Apply(cmd); err != nil {
					t.Fatalf("explicit environment cannot override a broken store: %v", err)
				}
			}
		})
	}
}

func TestSubscriptionTokenDoesNotChangeParentOrOtherCommandEnvironment(t *testing.T) {
	t.Setenv(SubscriptionTokenEnv, "")
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_PROFILE", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
		t.Setenv(key, "")
	}
	manager := NewSubscriptionTokenManager(linuxTokenStore(t))
	if err := manager.Save(context.Background(), "sk-ant-oat01-test"); err != nil {
		t.Fatal(err)
	}
	base := make([]string, 1, 8)
	base[0] = "OTHER=value"
	cmd := exec.Command("unused")
	cmd.Env = base
	if err := manager.Apply(cmd); err != nil {
		t.Fatal(err)
	}
	if os.Getenv(SubscriptionTokenEnv) != "" || base[:cap(base)][1] != "" {
		t.Fatal("token leaked into the parent or a shared environment slice")
	}
	cmd.Env = []string{SubscriptionTokenEnv + "=explicit-token"}
	if err := manager.Apply(cmd); err != nil || cmd.Env[0] != SubscriptionTokenEnv+"=explicit-token" {
		t.Fatal("saved token overrode the explicit environment")
	}
	cmd = exec.Command("unused")
	cmd.Dir = t.TempDir()
	if err := manager.Apply(cmd); err != nil {
		t.Fatal(err)
	}
	for _, entry := range cmd.Env {
		if value, ok := strings.CutPrefix(entry, "PWD="); ok && value != cmd.Dir {
			t.Fatal("credential injection changed the child process working directory environment")
		}
	}
}

func TestInvalidSubscriptionTokenDoesNotReplaceSavedCredential(t *testing.T) {
	t.Parallel()
	store := linuxTokenStore(t)
	manager := NewSubscriptionTokenManager(store)
	const token = "sk-ant-oat01-existing-test"
	if err := manager.Save(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", "two words", "embedded\nnewline", "control\x1b", strings.Repeat("x", 16*1024+1)} {
		if err := manager.Save(context.Background(), invalid); err == nil {
			t.Fatal("invalid credential replaced an existing token")
		}
	}
	got, err := store.Read(context.Background())
	if err != nil || got != token {
		t.Fatal("invalid save changed the credential on disk")
	}
}

func TestSubscriptionTokenKeychainUsesStdinAndDoesNotExposeFailureOutput(t *testing.T) {
	t.Parallel()
	store := NewSubscriptionTokenStore(t.TempDir()).(*subscriptionTokenStore)
	store.platform = "darwin"
	token := `sk-ant-oat01-test-with-"quotes"-and-\slashes`
	var input string
	store.run = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		input = stdin
		if !reflect.DeepEqual(args, []string{"-i"}) || strings.Contains(strings.Join(args, " "), token) {
			t.Fatal("Keychain password appeared in command arguments")
		}
		return []byte(token), errors.New("keychain locked")
	}
	err := store.Write(context.Background(), token)
	if err == nil || strings.Contains(err.Error(), token) || !strings.Contains(input, `-w "sk-ant-oat01-`) {
		t.Fatal("Keychain failure was hidden or exposed a secret")
	}
	if !strings.Contains(input, `\"quotes\"`) || !strings.Contains(input, `\\slashes`) {
		t.Fatal("Keychain interactive command did not escape quoted input")
	}
}

type blockedTokenStore struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockedTokenStore) Read(context.Context) (string, error) {
	close(s.started)
	<-s.release
	return "", nil
}
func (*blockedTokenStore) Write(context.Context, string) error { return nil }
func (*blockedTokenStore) Delete(context.Context) error        { return nil }
func (*blockedTokenStore) Description() string                 { return "test storage" }

func TestSubscriptionTokenCachedAccessDoesNotWaitForCredentialIO(t *testing.T) {
	t.Parallel()
	store := &blockedTokenStore{started: make(chan struct{}), release: make(chan struct{})}
	manager := NewSubscriptionTokenManager(store)
	if err := manager.Save(context.Background(), "sk-ant-oat01-test"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = manager.Refresh(context.Background())
	}()
	<-store.started
	defer func() { close(store.release); <-done }()
	read := make(chan error, 1)
	go func() {
		if !manager.Info().Saved {
			read <- errors.New("cached credential disappeared during refresh")
			return
		}
		cmd := exec.Command("unused")
		cmd.Env = []string{"OTHER=value"}
		read <- manager.Apply(cmd)
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cached credentials blocked on storage I/O")
	}
}

func TestAuthenticationStatusCommandReceivesSavedToken(t *testing.T) {
	manager := NewSubscriptionTokenManager(linuxTokenStore(t))
	if err := manager.Save(context.Background(), "sk-ant-oat01-test"); err != nil {
		t.Fatal(err)
	}
	previous := subscriptionTokens.Swap(manager)
	t.Cleanup(func() { subscriptionTokens.Store(previous) })
	for _, key := range []string{SubscriptionTokenEnv, "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_PROFILE", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
		t.Setenv(key, "")
	}
	binDir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$CLAUDE_CODE_OAUTH_TOKEN\" != 'sk-ant-oat01-test' ]; then exit 1; fi\nprintf '%s\\n' '{\"loggedIn\":true,\"authMethod\":\"oauth_token\"}'\n"
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	status, err := ReadAuthenticationStatus(ctx)
	if err != nil || !status.LoggedIn || status.AuthMethod != "oauth_token" {
		t.Fatalf("saved-token auth probe: status=%+v, err=%v", status, err)
	}
}
