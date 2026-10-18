package claudecli

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"lcroom/internal/appfs"
)

const subscriptionTokenService = "Little Control Room Claude subscription"

type subscriptionTokenStore struct {
	platform string
	dir      string
	account  string
	run      func(context.Context, string, ...string) ([]byte, error)
}

func NewSubscriptionTokenStore(dataDir string) SubscriptionTokenStore {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = appfs.DefaultDataDir()
	}
	abs, err := filepath.Abs(dataDir)
	if err == nil {
		dataDir = abs
	}
	digest := sha256.Sum256([]byte(filepath.Clean(dataDir)))
	return &subscriptionTokenStore{
		platform: runtime.GOOS,
		dir:      filepath.Join(dataDir, "credentials"),
		account:  fmt.Sprintf("claude-oauth-%x", digest[:16]),
		run: func(ctx context.Context, input string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, "/usr/bin/security", args...)
			cmd.Stdin = strings.NewReader(input)
			return cmd.Output()
		},
	}
}

func (s *subscriptionTokenStore) Description() string {
	if s.platform == "darwin" {
		return "macOS Keychain"
	}
	return "private token file (0600): " + filepath.Join(s.dir, "claude-oauth-token")
}

func (s *subscriptionTokenStore) Read(ctx context.Context) (string, error) {
	if s.platform == "darwin" {
		output, err := s.run(ctx, "", "find-generic-password", "-s", subscriptionTokenService, "-a", s.account, "-w")
		if keychainItemMissing(err) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("read Claude token from macOS Keychain: %w", err)
		}
		token := strings.TrimSpace(string(output))
		if token == "" {
			return "", errors.New("saved Claude token in macOS Keychain is empty; replace or remove it in /claude-auth")
		}
		return token, nil
	}
	if err := s.checkDirectory(false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	path := filepath.Join(s.dir, "claude-oauth-token")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect Claude token file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("Claude token file must be a regular owner-only file (0600)")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open Claude token file: %w", err)
	}
	defer file.Close()
	// Allow the trailing newline written by Write, plus one byte to detect
	// oversized files without reading an unbounded credential into memory.
	data, err := io.ReadAll(io.LimitReader(file, 16*1024+2))
	if err != nil {
		return "", fmt.Errorf("read Claude token file: %w", err)
	}
	if len(data) > 16*1024+1 {
		return "", errors.New("saved Claude token file is too large; replace or remove it in /claude-auth")
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", errors.New("saved Claude token file is empty; replace or remove it in /claude-auth")
	}
	return token, nil
}

func (s *subscriptionTokenStore) Write(ctx context.Context, token string) error {
	if err := ValidateSubscriptionToken(token); err != nil {
		return err
	}
	if s.platform == "darwin" {
		// security's interactive mode accepts the command on stdin. The token
		// never appears in process arguments, a shell, or diagnostic output.
		quote := func(value string) string {
			return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
		}
		input := "add-generic-password -U -s " + quote(subscriptionTokenService) + " -a " + quote(s.account) + " -w " + quote(token) + "\n"
		_, err := s.run(ctx, input, "-i")
		if err != nil {
			return fmt.Errorf("save Claude token in macOS Keychain: %w", err)
		}
		return nil
	}
	if err := s.checkDirectory(true); err != nil {
		return err
	}
	file, err := os.CreateTemp(s.dir, ".claude-token-*")
	if err != nil {
		return fmt.Errorf("create private Claude token file: %w", err)
	}
	defer os.Remove(file.Name())
	if _, err = io.WriteString(file, token+"\n"); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), filepath.Join(s.dir, "claude-oauth-token"))
	}
	if err != nil {
		return fmt.Errorf("save Claude token file: %w", err)
	}
	return nil
}

func (s *subscriptionTokenStore) Delete(ctx context.Context) error {
	if s.platform == "darwin" {
		_, err := s.run(ctx, "", "delete-generic-password", "-s", subscriptionTokenService, "-a", s.account)
		if keychainItemMissing(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("remove Claude token from macOS Keychain: %w", err)
		}
		return nil
	}
	if err := s.checkDirectory(false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	err := os.Remove(filepath.Join(s.dir, "claude-oauth-token"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Claude token file: %w", err)
	}
	return nil
}

func (s *subscriptionTokenStore) checkDirectory(create bool) error {
	if create {
		if err := os.MkdirAll(s.dir, 0o700); err != nil {
			return fmt.Errorf("create Claude credentials directory: %w", err)
		}
	}
	info, err := os.Lstat(s.dir)
	if err != nil {
		return fmt.Errorf("inspect Claude credentials directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("Claude credentials directory must be a private directory (0700), not a symlink")
	}
	return nil
}

func keychainItemMissing(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 44
}
