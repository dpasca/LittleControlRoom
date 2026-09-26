package claudeartifact

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SubagentSessionID gives a delegated transcript its own identity. Claude writes
// the parent's sessionId into every child transcript; using it alone would move
// the parent session between projects and conflate all of its agents.
func SubagentSessionID(parentID, agentID string) string {
	if !validSessionIDPart(parentID) || !validSessionIDPart(agentID) {
		return ""
	}
	return parentID + "/agent-" + agentID
}

func ParseSubagentSessionID(sessionID string) (parentID, agentID string, ok bool) {
	parentID, child, found := strings.Cut(sessionID, "/agent-")
	if !found || !validSessionIDPart(parentID) || !validSessionIDPart(child) {
		return "", "", false
	}
	return parentID, child, true
}

func validSessionIDPart(value string) bool {
	return value != "" && value != "." && value != ".." &&
		strings.TrimSpace(value) == value && !strings.ContainsAny(value, "/\\\x00")
}

// FindSubagentTranscript resolves only the exact child and verifies its recorded
// cwd. The containing project directory belongs to the parent, not the child.
func FindSubagentTranscript(claudeHome, projectPath, sessionID string) (string, error) {
	parentID, agentID, ok := ParseSubagentSessionID(sessionID)
	if !ok {
		return "", fmt.Errorf("invalid Claude Code subagent identity")
	}
	projectsDir := filepath.Join(claudeHome, "projects")
	projects, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", err
	}
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		path := filepath.Join(projectsDir, project.Name(), parentID, "subagents", "agent-"+agentID+".jsonl")
		if subagentTranscriptMatches(path, projectPath, parentID, agentID) {
			return path, nil
		}
	}
	return "", fmt.Errorf("Claude Code subagent %s is unavailable for %s", sessionID, projectPath)
}

func subagentTranscriptMatches(path, projectPath, parentID, agentID string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var entry struct {
			CWD         string `json:"cwd"`
			SessionID   string `json:"sessionId"`
			AgentID     string `json:"agentId"`
			IsSidechain bool   `json:"isSidechain"`
		}
		if json.Unmarshal(sc.Bytes(), &entry) != nil || entry.CWD == "" {
			continue
		}
		return entry.IsSidechain && entry.SessionID == parentID && entry.AgentID == agentID &&
			filepath.Clean(entry.CWD) == filepath.Clean(projectPath)
	}
	return false
}
