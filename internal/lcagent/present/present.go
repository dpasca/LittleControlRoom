package present

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

type CommandOutput struct {
	Stdout       []byte
	Stderr       []byte
	ExitCode     int
	Duration     time.Duration
	TimedOut     bool
	ArtifactDir  string
	CommandLabel string
}

type Presented struct {
	Text         string
	Stdout       string
	Stderr       string
	Truncated    bool
	Binary       bool
	ArtifactPath string
}

const maxInlineBytes = 64 * 1024

func Command(result CommandOutput) Presented {
	full := combine(result.Stdout, result.Stderr)
	p := Presented{}
	if hasBinary(full) {
		p.Binary = true
		p.Text = fmt.Sprintf("[error] binary command output suppressed (%d bytes).\n%s", len(full), metadata(result))
		return p
	}

	if len(full) > maxInlineBytes {
		p.Truncated = true
		p.ArtifactPath = writeArtifact(result.ArtifactDir, full)
		inline := string(full[:maxInlineBytes])
		p.Text = strings.TrimRight(inline, "\n") + "\n\n" +
			fmt.Sprintf("--- output truncated (%d bytes) ---\n", len(full)) +
			fmt.Sprintf("Full output: %s\n", p.ArtifactPath) +
			"Explore: tail -100 " + p.ArtifactPath + "\n" +
			metadata(result)
	} else {
		p.Text = strings.TrimRight(string(full), "\n")
		if p.Text != "" {
			p.Text += "\n"
		}
		p.Text += metadata(result)
	}
	p.Stdout = textOrEmpty(result.Stdout)
	p.Stderr = textOrEmpty(result.Stderr)
	return p
}

func combine(stdout, stderr []byte) []byte {
	switch {
	case len(stdout) == 0:
		return stderr
	case len(stderr) == 0:
		return stdout
	default:
		var buf bytes.Buffer
		buf.Write(stdout)
		if !bytes.HasSuffix(stdout, []byte("\n")) {
			buf.WriteByte('\n')
		}
		buf.WriteString("[stderr]\n")
		buf.Write(stderr)
		return buf.Bytes()
	}
}

func metadata(result CommandOutput) string {
	status := fmt.Sprintf("[exit:%d | %s]", result.ExitCode, result.Duration.Round(time.Millisecond))
	if result.TimedOut {
		status = fmt.Sprintf("[exit:%d | timeout | %s]", result.ExitCode, result.Duration.Round(time.Millisecond))
	}
	return status
}

func hasBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return true
	}
	sample := data
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	return !utf8.Valid(sample)
}

func textOrEmpty(data []byte) string {
	if len(data) == 0 || hasBinary(data) {
		return ""
	}
	return string(data)
}

func writeArtifact(dir string, data []byte) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, fmt.Sprintf("command-output-%d.txt", time.Now().UnixNano()))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return ""
	}
	return path
}
