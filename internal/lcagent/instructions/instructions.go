package instructions

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const maxInstructionsBytes = 128 * 1024

type ProjectInstructions struct {
	Path      string `json:"path"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
}

func LoadWorkspace(root string) (ProjectInstructions, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return ProjectInstructions{}, nil
	}
	path := filepath.Join(root, "AGENTS.md")
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ProjectInstructions{}, nil
		}
		return ProjectInstructions{}, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxInstructionsBytes+1))
	if err != nil {
		return ProjectInstructions{}, err
	}
	truncated := len(data) > maxInstructionsBytes
	if truncated {
		data = data[:maxInstructionsBytes]
	}
	if !utf8.Valid(data) {
		return ProjectInstructions{}, fmt.Errorf("AGENTS.md is not valid utf-8: %s", path)
	}
	body := strings.TrimSpace(string(data))
	if body == "" {
		return ProjectInstructions{}, nil
	}
	return ProjectInstructions{Path: path, Body: body, Truncated: truncated}, nil
}

func (p ProjectInstructions) PromptSection() string {
	if strings.TrimSpace(p.Body) == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Project instructions from %s:\n", p.Path)
	b.WriteString(strings.TrimSpace(p.Body))
	if p.Truncated {
		b.WriteString("\n\n--- AGENTS.md truncated ---")
	}
	return b.String()
}
