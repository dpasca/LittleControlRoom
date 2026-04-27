package boss

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

type bossSessionSearchMatch struct {
	Session   bossChatSession
	Turn      ChatMessage
	TurnIndex int
	Snippet   string
}

func (s *bossSessionStore) searchSessions(ctx context.Context, query string, limit int) ([]bossSessionSearchMatch, error) {
	if s == nil {
		return nil, nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 8
	}
	sessions, err := s.listSessions(ctx, 0)
	if err != nil {
		return nil, err
	}
	matches := make([]bossSessionSearchMatch, 0, minInt(limit, len(sessions)))
	for _, session := range sessions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		loaded, messages, err := s.loadSession(ctx, session.SessionID)
		if err != nil {
			continue
		}
		for i, message := range messages {
			if !containsFold(message.Content, query) {
				continue
			}
			matches = append(matches, bossSessionSearchMatch{
				Session:   loaded,
				Turn:      message,
				TurnIndex: i + 1,
				Snippet:   bossSessionSearchSnippet(message.Content, query, 1200),
			})
			if len(matches) >= limit {
				return matches, nil
			}
		}
	}
	return matches, nil
}

func formatBossSessionSearchXML(query string, matches []bossSessionSearchMatch, now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<boss_session_search query="%s" matches="%d" generated_at="%s">`,
		bossXMLAttr(query),
		len(matches),
		bossXMLAttr(formatBossTimestamp(now)),
	))
	if len(matches) == 0 {
		b.WriteString("\n<note>No saved boss chat sessions matched this query.</note>\n</boss_session_search>")
		return b.String()
	}
	for _, match := range matches {
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf(`<boss_session id="%s" title="%s" path="%s" updated_at="%s" age_at_query="%s">`,
			bossXMLAttr(match.Session.SessionID),
			bossXMLAttr(match.Session.Title),
			bossXMLAttr(match.Session.Path),
			bossXMLAttr(formatBossTimestamp(match.Session.UpdatedAt)),
			bossXMLAttr(ageAtTime(now, match.Session.UpdatedAt)),
		))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf(`<turn index="%d" role="%s" at="%s">`,
			match.TurnIndex,
			bossXMLAttr(normalizeChatRole(match.Turn.Role)),
			bossXMLAttr(formatBossTimestamp(match.Turn.At)),
		))
		b.WriteString("\n")
		b.WriteString(bossXMLCDATA(match.Snippet))
		b.WriteString("\n</turn>\n</boss_session>")
	}
	b.WriteString("\n</boss_session_search>")
	return b.String()
}

func bossSessionSearchSnippet(content, query string, limit int) string {
	content = strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n"))
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if !containsFold(line, query) {
			continue
		}
		start := maxInt(0, i-1)
		end := minInt(len(lines), i+2)
		return clipRawText(strings.Join(lines[start:end], "\n"), limit)
	}
	return clipRawText(content, limit)
}

func clipRawText(text string, limit int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func containsFold(text, query string) bool {
	text = strings.ToLower(text)
	query = strings.ToLower(strings.TrimSpace(query))
	return query != "" && strings.Contains(text, query)
}

func bossXMLAttr(value string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(strings.TrimSpace(value))); err != nil {
		return ""
	}
	return buf.String()
}

func bossXMLCDATA(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "]]>", "]]]]><![CDATA[>")
	return "<![CDATA[\n" + value + "\n]]>"
}
