package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	"kkg-agent-eino/apps/api/internal/memory"
)

func (s *Service) ListSessions(ctx context.Context, userID int64, archived bool) ([]ConversationSession, error) {
	items, err := s.memory.ListSessions(ctx, userID, 50, archived)
	if err != nil {
		return nil, err
	}
	out := make([]ConversationSession, 0, len(items))
	for _, item := range items {
		out = append(out, ConversationSession{
			ID:           item.ID,
			Title:        item.Title,
			LastMessage:  item.LastMessage,
			MessageCount: item.MessageCount,
			LastActiveAt: item.LastActiveAt,
			Archived:     item.Archived,
		})
	}
	return out, nil
}

func (s *Service) LoadSession(ctx context.Context, userID int64, sessionID string) (ConversationSession, error) {
	history, err := s.memory.LoadSession(ctx, userID, sessionID)
	if err != nil {
		return ConversationSession{}, err
	}
	summary := memory.SessionSummary{ID: sessionID}
	for _, archived := range []bool{false, true} {
		items, err := s.memory.ListSessions(ctx, userID, 200, archived)
		if err != nil {
			continue
		}
		for _, item := range items {
			if item.ID == sessionID {
				summary = item
				break
			}
		}
		if summary.Title != "" || summary.LastMessage != "" || summary.Archived {
			break
		}
	}
	session := ConversationSession{
		ID:           sessionID,
		Title:        summary.Title,
		LastMessage:  summary.LastMessage,
		MessageCount: len(history),
		LastActiveAt: summary.LastActiveAt,
		Archived:     summary.Archived,
		Messages:     make([]ConversationMessage, 0),
	}
	for _, msg := range history {
		if msg == nil {
			continue
		}
		switch msg.Role {
		case schema.User:
			session.Messages = append(session.Messages, ConversationMessage{Role: "user", Content: extractDisplayContent(msg)})
		case schema.Assistant:
			if strings.TrimSpace(msg.Content) == "" {
				continue
			}
			session.Messages = append(session.Messages, ConversationMessage{Role: "assistant", Content: extractDisplayContent(msg)})
		}
	}
	return session, nil
}

func (s *Service) ArchiveSession(ctx context.Context, userID int64, sessionID string, archived bool) error {
	return s.memory.ArchiveSession(ctx, userID, sessionID, archived)
}

func (s *Service) DeleteSession(ctx context.Context, userID int64, sessionID string) error {
	return s.memory.DeleteSession(ctx, userID, sessionID)
}

func extractDisplayContent(msg *schema.Message) string {
	if msg == nil {
		return ""
	}
	text := strings.TrimSpace(msg.Content)
	if text == "" && len(msg.UserInputMultiContent) > 0 {
		for _, part := range msg.UserInputMultiContent {
			if part.Type == schema.ChatMessagePartTypeText && strings.TrimSpace(part.Text) != "" {
				text = strings.TrimSpace(part.Text)
				break
			}
		}
	}
	if text == "" {
		return ""
	}
	var payload map[string]any
	if json.Unmarshal([]byte(text), &payload) == nil {
		if userQuery, ok := payload["user_query"].(string); ok && strings.TrimSpace(userQuery) != "" {
			return strings.TrimSpace(userQuery)
		}
	}
	return text
}

func directRestatementAnswer(query string) string {
	text := strings.TrimSpace(query)
	if text == "" {
		return ""
	}
	prefixes := []string{
		"复述一下", "复述下", "复述",
		"原样输出", "原样返回", "原样给我",
		"照抄一下", "照抄",
		"重复一下", "重复",
	}
	for _, prefix := range prefixes {
		if !strings.HasPrefix(text, prefix) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(text, prefix))
		rest = strings.TrimLeft(rest, ":：,，\n\t ")
		if rest == "" {
			return ""
		}
		return strings.Join([]string{
			"```text",
			rest,
			"```",
		}, "\n")
	}
	return ""
}

func sanitizeHistory(history []*schema.Message, limit int) []*schema.Message {
	if len(history) == 0 {
		return nil
	}
	start := 0
	if limit > 0 && len(history) > limit {
		start = len(history) - limit
	}
	window := cloneHistoryMessages(history[start:])
	for len(window) > 0 {
		msg := window[0]
		if msg == nil {
			window = window[1:]
			continue
		}
		if msg.Role != schema.Tool {
			break
		}
		window = window[1:]
	}

	out := make([]*schema.Message, 0, len(window))
	pendingToolCalls := make(map[string]struct{})
	for _, msg := range window {
		if msg == nil {
			continue
		}
		switch msg.Role {
		case schema.Assistant:
			if len(msg.ToolCalls) > 0 {
				for _, call := range msg.ToolCalls {
					if strings.TrimSpace(call.ID) != "" {
						pendingToolCalls[call.ID] = struct{}{}
					}
				}
			}
			out = append(out, msg)
		case schema.Tool:
			if strings.TrimSpace(msg.ToolCallID) == "" {
				continue
			}
			if _, ok := pendingToolCalls[msg.ToolCallID]; !ok {
				continue
			}
			delete(pendingToolCalls, msg.ToolCallID)
			out = append(out, msg)
		default:
			out = append(out, msg)
		}
	}
	return trimIncompleteToolCallTail(out)
}

func trimIncompleteToolCallTail(history []*schema.Message) []*schema.Message {
	if len(history) == 0 {
		return history
	}
	type pendingGroup struct {
		index int
		ids   map[string]struct{}
	}
	var groups []pendingGroup
	for i, msg := range history {
		if msg == nil {
			continue
		}
		switch msg.Role {
		case schema.Assistant:
			if len(msg.ToolCalls) == 0 {
				continue
			}
			ids := make(map[string]struct{}, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				if strings.TrimSpace(call.ID) != "" {
					ids[call.ID] = struct{}{}
				}
			}
			if len(ids) > 0 {
				groups = append(groups, pendingGroup{index: i, ids: ids})
			}
		case schema.Tool:
			if len(groups) == 0 || strings.TrimSpace(msg.ToolCallID) == "" {
				continue
			}
			last := &groups[len(groups)-1]
			if _, ok := last.ids[msg.ToolCallID]; !ok {
				continue
			}
			delete(last.ids, msg.ToolCallID)
			if len(last.ids) == 0 {
				groups = groups[:len(groups)-1]
			}
		}
	}
	if len(groups) == 0 {
		return history
	}
	cut := groups[0].index
	if cut < 0 || cut > len(history) {
		return history
	}
	return history[:cut]
}

func cloneHistoryMessages(messages []*schema.Message) []*schema.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]*schema.Message, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		raw, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		var copied schema.Message
		if err := json.Unmarshal(raw, &copied); err != nil {
			continue
		}
		out = append(out, &copied)
	}
	return out
}

func newSessionID() string {
	return fmt.Sprintf("sess_%d", time.Now().UnixNano())
}
