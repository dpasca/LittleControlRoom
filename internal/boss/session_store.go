package boss

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
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

const (
	bossSessionsDirName       = "boss-sessions"
	bossSessionFileExt        = ".md"
	bossSessionHeadingPrefix  = "## "
	bossSessionHeadingDivider = " @ "
)

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
		writeErr := writeBossSessionMarkdownHeader(file, sessionID, now)
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

	needsHeader := false
	session, _, err := readBossSessionFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		session = bossChatSession{SessionID: sessionID}
		needsHeader = true
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = at
		needsHeader = true
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open boss session file: %w", err)
	}
	defer file.Close()

	if needsHeader {
		if err := writeBossSessionMarkdownHeader(file, sessionID, session.CreatedAt); err != nil {
			return err
		}
	}
	if err := writeBossSessionMarkdownMessage(file, ChatMessage{
		Role:    role,
		Content: content,
		At:      at,
	}); err != nil {
		return err
	}
	return nil
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
		if entry.IsDir() || filepath.Ext(entry.Name()) != bossSessionFileExt {
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
	return filepath.Join(s.dir, sessionID+bossSessionFileExt), nil
}

func readBossSessionFile(path string) (bossChatSession, []ChatMessage, error) {
	return readBossSessionMarkdownFile(path)
}

func readBossSessionMarkdownFile(path string) (bossChatSession, []ChatMessage, error) {
	file, err := os.Open(path)
	if err != nil {
		return bossChatSession{}, nil, err
	}
	defer file.Close()

	session := bossChatSession{Path: path}
	var messages []ChatMessage
	var role string
	var at time.Time
	var contentLines []string
	flushMessage := func() {
		if role == "" {
			return
		}
		content := strings.TrimSpace(strings.Join(contentLines, "\n"))
		if content == "" {
			role = ""
			at = time.Time{}
			contentLines = nil
			return
		}
		msg := ChatMessage{Role: role, Content: content, At: at}
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
		role = ""
		at = time.Time{}
		contentLines = nil
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if nextRole, nextAt, ok := parseBossSessionMarkdownHeading(line); ok {
			flushMessage()
			role = nextRole
			at = nextAt
			contentLines = nil
			continue
		}
		if role != "" {
			contentLines = append(contentLines, line)
			continue
		}
		parseBossSessionMarkdownMetadata(line, &session)
	}
	flushMessage()
	if err := scanner.Err(); err != nil {
		return bossChatSession{}, nil, fmt.Errorf("read boss session file %s: %w", filepath.Base(path), err)
	}
	if session.SessionID == "" {
		session.SessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = session.CreatedAt
	}
	if session.UpdatedAt.IsZero() {
		if info, err := file.Stat(); err == nil {
			session.UpdatedAt = info.ModTime()
		}
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = session.UpdatedAt
	}
	return session, messages, nil
}

func parseBossSessionMarkdownMetadata(line string, session *bossChatSession) {
	key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
	if !ok {
		return
	}
	value = strings.TrimSpace(value)
	switch strings.TrimSpace(strings.ToLower(key)) {
	case "session":
		if value != "" {
			session.SessionID = value
		}
	case "created":
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			session.CreatedAt = parsed
		}
	case "title":
		if value != "" {
			session.Title = value
		}
	}
}

func parseBossSessionMarkdownHeading(line string) (string, time.Time, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, bossSessionHeadingPrefix) {
		return "", time.Time{}, false
	}
	body := strings.TrimSpace(strings.TrimPrefix(line, bossSessionHeadingPrefix))
	roleText, timeText, ok := strings.Cut(body, bossSessionHeadingDivider)
	if !ok {
		return "", time.Time{}, false
	}
	role, ok := parseBossSessionMarkdownRole(roleText)
	if !ok {
		return "", time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(timeText))
	if err != nil {
		return "", time.Time{}, false
	}
	return role, at, true
}

func parseBossSessionMarkdownRole(role string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "user":
		return "user", true
	case "assistant":
		return "assistant", true
	default:
		return "", false
	}
}

func writeBossSessionMarkdownHeader(w io.Writer, sessionID string, createdAt time.Time) error {
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	_, err := fmt.Fprintf(w, "# Boss Chat Session\n\nSession: %s\nCreated: %s\n\n---\n", sessionID, createdAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("write boss session header: %w", err)
	}
	return nil
}

func writeBossSessionMarkdownMessage(w io.Writer, message ChatMessage) error {
	content := strings.TrimSpace(message.Content)
	if content == "" {
		return nil
	}
	at := message.At
	if at.IsZero() {
		at = time.Now()
	}
	_, err := fmt.Fprintf(w, "\n%s%s%s%s\n\n%s\n",
		bossSessionHeadingPrefix,
		bossSessionMarkdownRole(message.Role),
		bossSessionHeadingDivider,
		at.UTC().Format(time.RFC3339Nano),
		content,
	)
	if err != nil {
		return fmt.Errorf("write boss session message: %w", err)
	}
	return nil
}

func bossSessionMarkdownRole(role string) string {
	if normalizeChatRole(role) == "assistant" {
		return "Assistant"
	}
	return "User"
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
