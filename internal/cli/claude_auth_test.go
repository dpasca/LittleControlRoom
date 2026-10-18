package cli

import (
	"os"
	"testing"
)

func TestClaudeAuthRejectsTokenArgumentsAndNoninteractiveSetup(t *testing.T) {
	for _, args := range [][]string{
		{"claude-auth"},
		{"claude-auth", "unknown"},
		{"claude-auth", "set", "sk-ant-oat01-test"},
		{"claude-auth", "set", "--token", "sk-ant-oat01-test"},
		{"claude-auth", "status", "--db"},
	} {
		if code, _ := captureRunStdout(t, args); code != 2 {
			t.Fatalf("unexpected exit code for invalid auth command: %d", code)
		}
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	previous := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = previous; _ = reader.Close() })
	if code, _ := captureRunStdout(t, []string{"claude-auth", "setup"}); code != 2 {
		t.Fatalf("noninteractive setup exit code = %d", code)
	}
}
