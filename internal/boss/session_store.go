package boss

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/appfs"
	"lcroom/internal/service"
)

const bossSessionsDirName = "boss-sessions"

type bossChatSession struct {
	SessionID    string
	Title        string
	Path         string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int
}

type bossSessionStore struct {
	dir string
}

type bossSessionRecord struct {
	Type      string    `json:"type"`
	SessionID string    `json:"session_id,omitempty"`
	Title     string    `json:"title,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	Role      string    `json:"role,omitempty"`
	Content   string    `json:"content,omitempty"`
	At        time.Time `json:"at,omitempty"`
}

func newBossSessionStore(dataDir string) *bossSessionStore {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		dataDir = appfs.DefaultDataDir()
	}
	return &bossSessionStore{dir: filepath.Join(filepath.Clean(dataDir), bossSessionsDirName)}
}

func newBossSessionStoreForService(svc *service.Service) *bossSessionStore {
	if svc == nil {
		return nil
	}
	return newBossSessionStore(svc.Config().DataDir)
}

func (s *bossSessionStore) loadLatestOrCreate(ctx context.Context, now time.Time) (bossChatSession, []ChatMessage, bool, error) {
	if s == nil {
		return bossChatSession{}, nil, false, errors.New("boss chat session store is not available")
	}
	if now.IsZero() {
		now = time.Now()
	}
	sessions, err := s.listSessions(ctx, 1)
	if err != nil {
		return bossChatSession{}, nil, false, err
	}
	if len(sessions) > 0 {
		session, messages, err := s.loadSession(ctx, sessions[0].SessionID)
		return session, messages, false, err
	}
	session, err := s.createSession(ctx, now)
	if err != nil {
		return bossChatSession{}, nil, false, err
	}
	return session, nil, true, nil
}

func (s *bossSessionStore) createSession(ctx context.Context, now time.Time) (bossChatSession, error) {
	if s == nil {
		return bossChatSession{}, errors.New("boss chat session store is not available")
	}
	if err := ctx.Err(); err != nil {
		return bossChatSession{}, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return bossChatSession{}, fmt.Errorf("create boss sessions dir: %w", err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		sessionID, err := newBossSessionID(now)
		if err != nil {
			return bossChatSession{}, err
		}
		path, err := s.sessionPath(sessionID)
		if err != nil {
			return bossChatSession{}, err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return bossChatSession{}, fmt.Errorf("create boss session file: %w", err)
		}
		session := bossChatSession{
			SessionID: sessionID,
			Path:      path,
			CreatedAt: now,
			UpdatedAt: now,
		}
		writeErr := writeBossSessionRecord(file, bossSessionRecord{
			Type:      "session",
			SessionID: sessionID,
			CreatedAt: now,
			UpdatedAt: now,
		})
		closeErr := file.Close()
		if writeErr != nil {
			_ = os.Remove(path)
			return bossChatSession{}, writeErr
		}
		if closeErr != nil {
			_ = os.Remove(path)
			return bossChatSession{}, fmt.Errorf("close boss session file: %w", closeErr)
		}
		return session, nil
	}
	return bossChatSession{}, errors.New("could not allocate a unique boss chat session id")
}

func (s *bossSessionStore) loadSession(ctx context.Context, sessionID string) (bossChatSession, []ChatMessage, error) {
	if s == nil {
		return bossChatSession{}, nil, errors.New("boss chat session store is not available")
	}
	if err := ctx.Err(); err != nil {
		return bossChatSession{}, nil, err
	}
	path, err := s.sessionPath(sessionID)
	if err != nil {
		return bossChatSession{}, nil, err
	}
	session, messages, err := readBossSessionFile(path)
	if err != nil {
		return bossChatSession{}, nil, err
	}
	session.Path = path
	return session, messages, nil
}

func (s *bossSessionStore) appendMessage(ctx context.Context, sessionID string, message ChatMessage) error {
	if s == nil {
		return errors.New("boss chat session store is not available")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	content := strings.TrimSpace(message.Content)
	if content == "" {
		return nil
	}
	role := normalizeChatRole(message.Role)
	at := message.At
	if at.IsZero() {
		at = time.Now()
	}
	path, err := s.sessionPath(sessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create boss sessions dir: %w", err)
	}

	session, _, err := readBossSessionFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		session = bossChatSession{SessionID: sessionID, CreatedAt: at}
	}
	title := strings.TrimSpace(session.Title)
	if title == "" && role == "user" {
		title = bossSessionTitleFromMessage(content)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open boss session file: %w", err)
	}
	defer file.Close()

	if session.CreatedAt.IsZero() {
		if err := writeBossSessionRecord(file, bossSessionRecord{
			Type:      "session",
			SessionID: sessionID,
			Title:     title,
			CreatedAt: at,
			UpdatedAt: at,
		}); err != nil {
			return err
		}
	}
	if err := writeBossSessionRecord(file, bossSessionRecord{
		Type:      "message",
		SessionID: sessionID,
		Role:      role,
		Content:   content,
		At:        at,
	}); err != nil {
		return err
	}
	return writeBossSessionRecord(file, bossSessionRecord{
		Type:      "session",
		SessionID: sessionID,
		Title:     title,
		CreatedAt: firstNonZeroTime(session.CreatedAt, at),
		UpdatedAt: at,
	})
}

func (s *bossSessionStore) listSessions(ctx context.Context, limit int) ([]bossChatSession, error) {
	if s == nil {
		return nil, errors.New("boss chat session store is not available")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list boss sessions dir: %w", err)
	}
	sessions := make([]bossChatSession, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		session, _, err := readBossSessionFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			continue
		}
		sessions = append(sessions, session)
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if !sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
		}
		return sessions[i].SessionID > sessions[j].SessionID
	})
	if limit > 0 && len(sessions) > limit {
		sessions = sessions[:limit]
	}
	return sessions, nil
}

func (s *bossSessionStore) sessionPath(sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if !validBossSessionID(sessionID) {
		return "", fmt.Errorf("invalid boss chat session id: %s", sessionID)
	}
	return filepath.Join(s.dir, sessionID+".jsonl"), nil
}

func readBossSessionFile(path string) (bossChatSession, []ChatMessage, error) {
	file, err := os.Open(path)
	if err != nil {
		return bossChatSession{}, nil, err
	}
	defer file.Close()

	session := bossChatSession{Path: path}
	var messages []ChatMessage
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record bossSessionRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return bossChatSession{}, nil, fmt.Errorf("decode boss session record %s: %w", filepath.Base(path), err)
		}
		switch record.Type {
		case "session":
			if strings.TrimSpace(record.SessionID) != "" {
				session.SessionID = strings.TrimSpace(record.SessionID)
			}
			if strings.TrimSpace(record.Title) != "" {
				session.Title = strings.TrimSpace(record.Title)
			}
			if !record.CreatedAt.IsZero() && (session.CreatedAt.IsZero() || record.CreatedAt.Before(session.CreatedAt)) {
				session.CreatedAt = record.CreatedAt
			}
			if record.UpdatedAt.After(session.UpdatedAt) {
				session.UpdatedAt = record.UpdatedAt
			}
		case "message":
			msg := ChatMessage{
				Role:    normalizeChatRole(record.Role),
				Content: strings.TrimSpace(record.Content),
				At:      record.At,
			}
			if msg.Content == "" {
				continue
			}
			messages = append(messages, msg)
			session.MessageCount++
			if msg.At.After(session.UpdatedAt) {
				session.UpdatedAt = msg.At
			}
			if session.CreatedAt.IsZero() || (!msg.At.IsZero() && msg.At.Before(session.CreatedAt)) {
				session.CreatedAt = msg.At
			}
			if session.Title == "" && msg.Role == "user" {
				session.Title = bossSessionTitleFromMessage(msg.Content)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return bossChatSession{}, nil, fmt.Errorf("read boss session file %s: %w", filepath.Base(path), err)
	}
	if session.SessionID == "" {
		session.SessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if session.UpdatedAt.IsZero() {
		if info, err := file.Stat(); err == nil {
			session.UpdatedAt = info.ModTime()
		}
	}
	return session, messages, nil
}

func writeBossSessionRecord(w io.Writer, record bossSessionRecord) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode boss session record: %w", err)
	}
	if _, err := w.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("write boss session record: %w", err)
	}
	return nil
}

func newBossSessionID(now time.Time) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate boss chat session id: %w", err)
	}
	return "boss_" + now.UTC().Format("20060102_150405") + "_" + hex.EncodeToString(b[:]), nil
}

func validBossSessionID(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	for _, r := range sessionID {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func bossSessionTitleFromMessage(content string) string {
	title := strings.Join(strings.Fields(content), " ")
	if title == "" {
		return ""
	}
	runes := []rune(title)
	if len(runes) <= 72 {
		return title
	}
	return string(runes[:69]) + "..."
}

func firstNonZeroTime(first, second time.Time) time.Time {
	if !first.IsZero() {
		return first
	}
	return second
}
