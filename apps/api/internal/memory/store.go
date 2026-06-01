package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/cloudwego/eino/schema"
)

var ErrSessionForbidden = errors.New("session does not belong to current user")

type Store interface {
	EnsureSession(ctx context.Context, userID int64, sessionID string) error
	LoadMessages(ctx context.Context, userID int64, sessionID string, limit int) ([]*schema.Message, error)
	AppendMessages(ctx context.Context, userID int64, sessionID string, messages ...*schema.Message) error
	ListSessions(ctx context.Context, userID int64, limit int, archived bool) ([]SessionSummary, error)
	LoadSession(ctx context.Context, userID int64, sessionID string) ([]*schema.Message, error)
	ArchiveSession(ctx context.Context, userID int64, sessionID string, archived bool) error
	DeleteSession(ctx context.Context, userID int64, sessionID string) error
	Get(ctx context.Context, id string) ([]byte, bool, error)
	Set(ctx context.Context, id string, value []byte) error
}

type SessionSummary struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	LastMessage  string `json:"last_message,omitempty"`
	MessageCount int    `json:"message_count"`
	LastActiveAt string `json:"last_active_at"`
	Archived     bool   `json:"archived"`
}

func Open(ctx context.Context, dsn string) (Store, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return NewInMemoryStore(), nil
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	store := &PostgresStore{db: db}
	if err := store.ensureSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

type InMemoryStore struct {
	mu           sync.RWMutex
	sessions     map[string][]*schema.Message
	sessionUsers map[string]int64
	sessionFlags map[string]bool
	checkpoints  map[string][]byte
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		sessions:     make(map[string][]*schema.Message),
		sessionUsers: make(map[string]int64),
		sessionFlags: make(map[string]bool),
		checkpoints:  make(map[string][]byte),
	}
}

func (s *InMemoryStore) EnsureSession(_ context.Context, userID int64, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner, ok := s.sessionUsers[sessionID]; ok && owner != 0 && owner != userID {
		return ErrSessionForbidden
	}
	if _, ok := s.sessions[sessionID]; !ok {
		s.sessions[sessionID] = nil
	}
	if s.sessionUsers[sessionID] == 0 {
		s.sessionUsers[sessionID] = userID
	}
	return nil
}

func (s *InMemoryStore) LoadMessages(_ context.Context, userID int64, sessionID string, limit int) ([]*schema.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if owner, ok := s.sessionUsers[sessionID]; ok && owner != 0 && owner != userID {
		return nil, ErrSessionForbidden
	}
	history := cloneMessages(s.sessions[sessionID])
	if limit > 0 && len(history) > limit {
		history = history[len(history)-limit:]
	}
	return history, nil
}

func (s *InMemoryStore) AppendMessages(_ context.Context, userID int64, sessionID string, messages ...*schema.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner, ok := s.sessionUsers[sessionID]; ok && owner != 0 && owner != userID {
		return ErrSessionForbidden
	}
	s.sessionUsers[sessionID] = userID
	next := append(cloneMessages(s.sessions[sessionID]), cloneMessages(messages)...)
	s.sessions[sessionID] = next
	return nil
}

func (s *InMemoryStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.checkpoints[id]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), value...), true, nil
}

func (s *InMemoryStore) ListSessions(_ context.Context, userID int64, limit int, archived bool) ([]SessionSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SessionSummary, 0, len(s.sessions))
	for sessionID, messages := range s.sessions {
		if owner, ok := s.sessionUsers[sessionID]; ok && owner != 0 && owner != userID {
			continue
		}
		if s.sessionFlags[sessionID] != archived {
			continue
		}
		item := buildSessionSummary(sessionID, messages, "")
		item.Archived = archived
		out = append(out, item)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) LoadSession(_ context.Context, userID int64, sessionID string) ([]*schema.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if owner, ok := s.sessionUsers[sessionID]; ok && owner != 0 && owner != userID {
		return nil, ErrSessionForbidden
	}
	return cloneMessages(s.sessions[sessionID]), nil
}

func (s *InMemoryStore) ArchiveSession(_ context.Context, userID int64, sessionID string, archived bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner, ok := s.sessionUsers[sessionID]; ok && owner != 0 && owner != userID {
		return ErrSessionForbidden
	}
	if _, ok := s.sessions[sessionID]; !ok {
		return sql.ErrNoRows
	}
	s.sessionFlags[sessionID] = archived
	return nil
}

func (s *InMemoryStore) DeleteSession(_ context.Context, userID int64, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner, ok := s.sessionUsers[sessionID]; ok && owner != 0 && owner != userID {
		return ErrSessionForbidden
	}
	delete(s.sessions, sessionID)
	delete(s.sessionUsers, sessionID)
	delete(s.sessionFlags, sessionID)
	delete(s.checkpoints, sessionID)
	return nil
}

func (s *InMemoryStore) Set(_ context.Context, id string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkpoints[id] = append([]byte(nil), value...)
	return nil
}

type PostgresStore struct {
	db *sql.DB
}

func (s *PostgresStore) ensureSchema(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS agent_sessions (
			id TEXT PRIMARY KEY,
			user_id BIGINT NOT NULL DEFAULT 0,
			archived BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`ALTER TABLE agent_sessions ADD COLUMN IF NOT EXISTS user_id BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE agent_sessions ADD COLUMN IF NOT EXISTS archived BOOLEAN NOT NULL DEFAULT FALSE`,
		`CREATE TABLE IF NOT EXISTS agent_messages (
			id BIGSERIAL PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
			role TEXT NOT NULL,
			payload JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_messages_session_id_id ON agent_messages (session_id, id DESC)`,
		`CREATE TABLE IF NOT EXISTS agent_checkpoints (
			session_id TEXT PRIMARY KEY REFERENCES agent_sessions(id) ON DELETE CASCADE,
			checkpoint BYTEA NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
	}
	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("ensure memory schema: %w", err)
		}
	}
	return nil
}

func (s *PostgresStore) EnsureSession(ctx context.Context, userID int64, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_sessions (id, user_id)
		VALUES ($1, $2)
		ON CONFLICT (id)
		DO UPDATE SET
			user_id = CASE
				WHEN agent_sessions.user_id = 0 THEN EXCLUDED.user_id
				ELSE agent_sessions.user_id
			END,
			updated_at = now()
	`, sessionID, userID)
	if err != nil {
		return err
	}
	return s.ensureSessionOwnership(ctx, userID, sessionID)
}

func (s *PostgresStore) LoadMessages(ctx context.Context, userID int64, sessionID string, limit int) ([]*schema.Message, error) {
	if err := s.ensureSessionOwnership(ctx, userID, sessionID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT payload
		FROM agent_messages
		WHERE session_id = $1
		ORDER BY id DESC
		LIMIT $2
	`, sessionID, max(limit, 1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reversed []*schema.Message
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		msg := new(schema.Message)
		if err := json.Unmarshal(raw, msg); err != nil {
			return nil, fmt.Errorf("decode memory message: %w", err)
		}
		reversed = append(reversed, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]*schema.Message, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		out = append(out, reversed[i])
	}
	return out, nil
}

func (s *PostgresStore) AppendMessages(ctx context.Context, userID int64, sessionID string, messages ...*schema.Message) error {
	if len(messages) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_sessions (id, user_id)
		VALUES ($1, $2)
		ON CONFLICT (id)
		DO UPDATE SET
			user_id = CASE
				WHEN agent_sessions.user_id = 0 THEN EXCLUDED.user_id
				ELSE agent_sessions.user_id
			END,
			updated_at = now()
	`, sessionID, userID); err != nil {
		return err
	}
	var owner int64
	if err := tx.QueryRowContext(ctx, `SELECT user_id FROM agent_sessions WHERE id = $1`, sessionID).Scan(&owner); err != nil {
		return err
	}
	if owner != 0 && owner != userID {
		return ErrSessionForbidden
	}

	for _, msg := range messages {
		if msg == nil {
			continue
		}
		raw, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("encode memory message: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agent_messages (session_id, role, payload)
			VALUES ($1, $2, $3::jsonb)
		`, sessionID, string(msg.Role), string(raw)); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM agent_messages
		WHERE session_id = $1
		  AND id NOT IN (
			SELECT id
			FROM agent_messages
			WHERE session_id = $1
			ORDER BY id DESC
			LIMIT 20
		  )
	`, sessionID); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *PostgresStore) ensureSessionOwnership(ctx context.Context, userID int64, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	var owner int64
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM agent_sessions WHERE id = $1`, sessionID).Scan(&owner)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if owner == 0 {
		_, err = s.db.ExecContext(ctx, `UPDATE agent_sessions SET user_id = $2, updated_at = now() WHERE id = $1 AND user_id = 0`, sessionID, userID)
		return err
	}
	if owner != userID {
		return ErrSessionForbidden
	}
	return nil
}

func (s *PostgresStore) Get(ctx context.Context, id string) ([]byte, bool, error) {
	var value []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT checkpoint
		FROM agent_checkpoints
		WHERE session_id = $1
	`, id).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (s *PostgresStore) ListSessions(ctx context.Context, userID int64, limit int, archived bool) ([]SessionSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.updated_at, s.archived, COALESCE(m.message_count, 0)
		FROM agent_sessions s
		LEFT JOIN (
			SELECT session_id, COUNT(*) AS message_count
			FROM agent_messages
			GROUP BY session_id
		) m ON m.session_id = s.id
		WHERE s.user_id = $1 AND s.archived = $3
		ORDER BY s.updated_at DESC
		LIMIT $2
	`, userID, max(limit, 1), archived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summaries := make([]SessionSummary, 0)
	for rows.Next() {
		var (
			sessionID    string
			updatedAt    time.Time
			isArchived   bool
			messageCount int
		)
		if err := rows.Scan(&sessionID, &updatedAt, &isArchived, &messageCount); err != nil {
			return nil, err
		}
		messages, err := s.LoadMessages(ctx, userID, sessionID, 20)
		if err != nil {
			return nil, err
		}
		item := buildSessionSummary(sessionID, messages, updatedAt.Format(time.RFC3339))
		item.MessageCount = messageCount
		item.Archived = isArchived
		summaries = append(summaries, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return summaries, nil
}

func (s *PostgresStore) LoadSession(ctx context.Context, userID int64, sessionID string) ([]*schema.Message, error) {
	if err := s.ensureSessionOwnership(ctx, userID, sessionID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT payload
		FROM agent_messages
		WHERE session_id = $1
		ORDER BY id ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*schema.Message
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		msg := new(schema.Message)
		if err := json.Unmarshal(raw, msg); err != nil {
			return nil, fmt.Errorf("decode memory message: %w", err)
		}
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *PostgresStore) ArchiveSession(ctx context.Context, userID int64, sessionID string, archived bool) error {
	if err := s.ensureSessionOwnership(ctx, userID, sessionID); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE agent_sessions
		SET archived = $3, updated_at = now()
		WHERE id = $1 AND user_id = $2
	`, sessionID, userID, archived)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *PostgresStore) DeleteSession(ctx context.Context, userID int64, sessionID string) error {
	if err := s.ensureSessionOwnership(ctx, userID, sessionID); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM agent_sessions
		WHERE id = $1 AND user_id = $2
	`, sessionID, userID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *PostgresStore) Set(ctx context.Context, id string, value []byte) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_sessions (id, user_id)
		VALUES ($1, 0)
		ON CONFLICT (id) DO NOTHING
	`, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_checkpoints (session_id, checkpoint)
		VALUES ($1, $2)
		ON CONFLICT (session_id)
		DO UPDATE SET checkpoint = EXCLUDED.checkpoint, updated_at = now()
	`, id, value)
	return err
}

func cloneMessages(messages []*schema.Message) []*schema.Message {
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func buildSessionSummary(sessionID string, messages []*schema.Message, updatedAt string) SessionSummary {
	summary := SessionSummary{
		ID:           sessionID,
		Title:        "未命名会话",
		MessageCount: len(messages),
		LastActiveAt: updatedAt,
	}
	for _, msg := range messages {
		if msg == nil || msg.Role != schema.User {
			continue
		}
		if title := summarizeMessage(msg); title != "" {
			summary.Title = title
			break
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg == nil {
			continue
		}
		if text := summarizeMessage(msg); text != "" {
			summary.LastMessage = text
			break
		}
	}
	return summary
}

func summarizeMessage(msg *schema.Message) string {
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
			text = strings.TrimSpace(userQuery)
		}
	}
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > 48 {
		return string(runes[:48]) + "..."
	}
	return text
}
