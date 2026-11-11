package codexapp

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Claude may acknowledge a queued prompt without saving its body to its own
// transcript. Keep a host-owned copy until that transcript contains the prompt.
// These records are recovery evidence, never an automatic replay queue.
type claudeSavedInput struct {
	ID          string     `json:"id"`
	ProjectPath string     `json:"project_path"`
	SessionID   string     `json:"session_id"`
	SubmittedAt time.Time  `json:"submitted_at"`
	Input       Submission `json:"input"`
}

func claudeInputRecoveryDir(dataDir, projectPath, sessionID string) string {
	key := sha256.Sum256([]byte(projectPath + "\x00" + sessionID))
	return filepath.Join(dataDir, "claude-input-recovery", fmt.Sprintf("%x", key))
}

func saveClaudeInput(dataDir string, record claudeSavedInput) error {
	if dataDir == "" {
		return nil
	}
	dir := claudeInputRecoveryDir(dataDir, record.ProjectPath, record.SessionID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".input-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(file.Name(), filepath.Join(dir, record.ID+".json")); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func loadClaudeSavedInputs(dataDir, projectPath, sessionID string) ([]claudeSavedInput, error) {
	if dataDir == "" || sessionID == "" {
		return nil, nil
	}
	dir := claudeInputRecoveryDir(dataDir, projectPath, sessionID)
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []claudeSavedInput
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			return nil, err
		}
		var record claudeSavedInput
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("read saved Claude input %s: %w", file.Name(), err)
		}
		if record.ID+".json" != file.Name() || record.ProjectPath != projectPath || record.SessionID != sessionID {
			return nil, fmt.Errorf("saved Claude input %s has mismatched ownership", file.Name())
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].SubmittedAt.Equal(records[j].SubmittedAt) {
			return records[i].ID < records[j].ID
		}
		return records[i].SubmittedAt.Before(records[j].SubmittedAt)
	})
	return records, nil
}

// Called only on transcript reloads, outside the UI thread. Matching is by the
// submitted UUID, including queued_command.source_uuid, never by prompt text.
func (s *claudeCodeSession) restoreClaudeSavedInputsLocked() {
	persisted := make(map[string]bool)
	for _, entry := range s.entries {
		if entry.Kind == TranscriptUser {
			persisted[entry.ItemID] = true
		}
	}
	kept := s.savedInputs[:0]
	for _, record := range s.savedInputs {
		if persisted[record.ID] {
			// Once Claude has saved the full prompt, it owns that history. Do
			// not resurrect it later if the operator compacts the conversation.
			if s.dataDir != "" {
				path := filepath.Join(claudeInputRecoveryDir(s.dataDir, record.ProjectPath, record.SessionID), record.ID+".json")
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					kept = append(kept, record)
				}
			}
			continue
		}
		kept = append(kept, record)
		text := strings.TrimSpace(record.Input.TranscriptDisplayText())
		s.entries = append(s.entries, TranscriptEntry{ItemID: record.ID, Kind: TranscriptUser, Text: text})
		if s.cmd != nil {
			// A live queue still owns the submission. Preserve its echo, but
			// do not invite a duplicate resend while Claude may process it.
			continue
		}
		s.entries = append(s.entries, TranscriptEntry{
			ItemID: "saved-input:" + record.ID, Kind: TranscriptSystem,
			Text: "Saved follow-up missing from Claude's transcript; processing is unconfirmed. Copy the message above to resend it. It was not automatically resent.",
		})
		if s.dataDir != "" {
			// The full submission stays accessible even when the UI bounds an
			// exceptionally large paste or hides model-only context in the echo.
			path := filepath.Join(claudeInputRecoveryDir(s.dataDir, record.ProjectPath, record.SessionID), record.ID+".json")
			s.entries[len(s.entries)-1].Text += " Full submission backup: " + path
		}
	}
	s.savedInputs = kept
}
