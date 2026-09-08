package codexapp

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestOpenCodeStartupDiagnostics(t *testing.T) {
	for _, mode := range []string{"exit", "stdout", "tail", "scanner", "timeout", "ready"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestOpenCodeStartupHelperProcess$")
			cmd.Env = append(os.Environ(), "LCROOM_OPENCODE_STARTUP_HELPER="+mode)
			timeout := 5 * time.Second
			if mode == "timeout" {
				timeout = time.Second
			}
			url, exited, err := startOpenCodeServerCommand(cmd, timeout)
			if mode == "ready" {
				if err != nil {
					t.Fatal(err)
				}
				if url != "http://127.0.0.1:12345" {
					t.Fatalf("URL = %q", url)
				}
				_ = cmd.Process.Kill()
				<-exited
				return
			}
			if err == nil || !strings.Contains(err.Error(), "recognizable startup diagnosis") {
				t.Fatalf("startup error = %v, want server diagnosis", err)
			}
			if cmd.ProcessState == nil {
				t.Fatal("failed server was not reaped")
			}
			if mode == "timeout" && !strings.Contains(err.Error(), "timed out") {
				t.Fatalf("error = %v, want timeout", err)
			}
			if mode == "scanner" && !strings.Contains(err.Error(), "token too long") {
				t.Fatalf("error = %v, want scanner failure", err)
			}
			if mode == "tail" {
				if strings.Contains(err.Error(), "discard this old line") {
					t.Fatalf("retained old output: %v", err)
				}
				if len(err.Error()) > threadAdminStderrLimit+200 {
					t.Fatalf("unbounded error: %d bytes", len(err.Error()))
				}
			}
		})
	}
}

func TestOpenCodeStartupHelperProcess(t *testing.T) {
	mode := os.Getenv("LCROOM_OPENCODE_STARTUP_HELPER")
	if mode == "" {
		return
	}
	if mode == "ready" {
		fmt.Fprintln(os.Stdout, openCodeListeningPrefix+"http://127.0.0.1:12345/")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if mode == "tail" {
		fmt.Fprintln(os.Stderr, "discard this old line")
		fmt.Fprintln(os.Stderr, strings.Repeat("x", 2*threadAdminStderrLimit))
	}
	output := os.Stderr
	if mode == "stdout" {
		output = os.Stdout
	}
	fmt.Fprintln(output, "recognizable startup diagnosis")
	if mode == "scanner" {
		fmt.Fprintln(os.Stderr, strings.Repeat("x", 2*1024*1024))
		time.Sleep(time.Minute)
	}
	if mode == "timeout" {
		time.Sleep(time.Minute)
	}
	os.Exit(1)
}
