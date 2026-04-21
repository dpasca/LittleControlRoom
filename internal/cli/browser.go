package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"lcroom/internal/browserctl"
)

var (
	managedBrowserStateReader           = browserctl.ReadManagedPlaywrightState
	managedBrowserRevealer              = browserctl.RevealManagedPlaywrightState
	browserStdout             io.Writer = os.Stdout
	browserStderr             io.Writer = os.Stderr
)

func runBrowser(args []string) int {
	if len(args) == 0 {
		printBrowserUsage()
		return 2
	}

	action := strings.TrimSpace(strings.ToLower(args[0]))
	switch action {
	case "status", "reveal":
	default:
		fmt.Fprintf(browserStderr, "browser command error: unknown action %q\n", action)
		printBrowserUsage()
		return 2
	}

	fs := flag.NewFlagSet("browser "+action, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dataDir := fs.String("data-dir", browserctl.DefaultDataDir(), "LCR data dir")
	sessionKey := fs.String("session-key", "", "managed browser session key")
	if err := fs.Parse(args[1:]); err != nil {
		fmt.Fprintf(browserStderr, "browser command error: %v\n", err)
		return 2
	}
	if strings.TrimSpace(*sessionKey) == "" {
		fmt.Fprintln(browserStderr, "browser command error: --session-key is required")
		return 2
	}

	state, err := managedBrowserStateReader(*dataDir, *sessionKey)
	if err != nil {
		fmt.Fprintf(browserStderr, "browser %s failed: %v\n", action, err)
		return 1
	}

	switch action {
	case "status":
		payload, err := json.MarshalIndent(state.Normalize(), "", "  ")
		if err != nil {
			fmt.Fprintf(browserStderr, "browser status failed: %v\n", err)
			return 1
		}
		var out bytes.Buffer
		out.Write(payload)
		out.WriteByte('\n')
		_, _ = browserStdout.Write(out.Bytes())
		return 0
	case "reveal":
		if err := managedBrowserRevealer(state); err != nil {
			fmt.Fprintf(browserStderr, "browser reveal failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(browserStdout, "revealed managed browser session %s\n", strings.TrimSpace(*sessionKey))
		return 0
	default:
		printBrowserUsage()
		return 2
	}
}

func printBrowserUsage() {
	fmt.Fprintln(browserStderr, "Usage: lcroom browser <status|reveal> --session-key <key> [--data-dir <dir>]")
}
