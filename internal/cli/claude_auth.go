package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"lcroom/internal/claudecli"
	"lcroom/internal/config"

	"github.com/charmbracelet/x/term"
)

// The token is accepted only on stdin, never as a flag or positional argument.
// setup is also used by the TUI while Bubble Tea temporarily releases the PTY.
func runClaudeAuth(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: lcroom claude-auth <setup|set|status|remove> [--config path] [--db path]")
		return 2
	}
	action := args[0]
	if action != "setup" && action != "set" && action != "status" && action != "remove" {
		fmt.Fprintln(os.Stderr, "expected claude-auth setup, set, status, or remove")
		return 2
	}
	// Avoid accidentally accepting a pasted token as an ignored positional
	// argument. Error messages never echo the supplied arguments.
	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--config" || args[i] == "--db":
			i++
			if i < len(args) && args[i] != "" {
				continue
			}
		case strings.HasPrefix(args[i], "--config=") || strings.HasPrefix(args[i], "--db="):
			continue
		}
		fmt.Fprintln(os.Stderr, "claude-auth accepts only --config and --db options; paste tokens on stdin")
		return 2
	}
	cfg, err := config.Parse("claude-auth", args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 2
	}
	manager := claudecli.NewSubscriptionTokenManager(claudecli.NewSubscriptionTokenStore(cfg.DataDir))
	if action == "setup" {
		if !term.IsTerminal(os.Stdin.Fd()) {
			fmt.Fprintln(os.Stderr, "setup needs a terminal; generate a token with `claude setup-token` on another computer, then use claude-auth set on this host")
			return 2
		}
		cmd := exec.Command("claude", "setup-token")
		// Claude may print the generated token on either stream. Keep both on
		// the terminal, away from LCR's stderr/crash-log capture.
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "Claude token generation did not complete; the previous LCR token was kept")
			return 1
		}
	}

	var token string
	if action == "set" || action == "setup" {
		var data []byte
		if term.IsTerminal(os.Stdin.Fd()) {
			fmt.Fprint(os.Stderr, "Paste the token from claude setup-token (hidden), then press Enter: ")
			data, err = term.ReadPassword(os.Stdin.Fd())
			fmt.Fprintln(os.Stderr)
		} else {
			data, err = io.ReadAll(io.LimitReader(os.Stdin, 16*1024+1))
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "could not read the subscription token")
			return 1
		}
		token = strings.TrimSpace(string(data))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	switch action {
	case "setup", "set":
		err = manager.Save(ctx, token)
	case "remove":
		err = manager.Remove(ctx)
	case "status":
		err = manager.Refresh(ctx)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	info := manager.Info()
	if action == "remove" {
		fmt.Fprintln(os.Stdout, "Saved Claude subscription token removed. Open /claude-auth in a running LCR to reload credentials, then reconnect Claude sessions.")
	} else if info.Saved {
		fmt.Fprintln(os.Stdout, "Claude subscription token saved in "+info.Storage+". Open /claude-auth in a running LCR to reload credentials, then reconnect Claude sessions.")
	} else {
		fmt.Fprintln(os.Stdout, "No LCR-saved Claude subscription token. Claude uses its normal login or environment credentials.")
	}
	if override := claudecli.SubscriptionTokenEnvironmentOverride(); override != "" {
		fmt.Fprintln(os.Stdout, override+" is set in the environment and takes precedence over the LCR-saved token.")
	}
	return 0
}
