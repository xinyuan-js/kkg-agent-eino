package rag

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const defaultQuestionVectorTable = "rag_question_documents"

type PGVectorStore struct {
	db         *sql.DB
	tableName  string
	dimensions int
}

func OpenPGVectorStore(ctx context.Context, dsn string, dimensions int) (*PGVectorStore, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("postgres dsn is required")
	}
	if dimensions <= 0 {
		return nil, fmt.Errorf("embedding dimensions must be positive")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	store := &PGVectorStore{
		db:         db,
		tableName:  defaultQuestionVectorTable,
		dimensions: dimensions,
	}
	if err := store.ensureSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *PGVectorStore) Upsert(ctx context.Context, docs ...VectorDocument) error {
	for _, doc := range docs {
		if strings.TrimSpace(doc.Document.ID) == "" || len(doc.Vector) == 0 {
			continue
		}
		if len(doc.Vector) != s.dimensions {
			return fmt.Errorf("embedding dimension mismatch for %s: got %d want %d", doc.Document.ID, len(doc.Vector), s.dimensions)
		}
		metadata, err := json.Marshal(doc.Document.Metadata)
		if err != nil {
			return fmt.Errorf("marshal rag metadata: %w", err)
		}
		_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO %s (id, source, title, content, metadata, embedding, updated_at)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6::vector, now())
			ON CONFLICT (id)
			DO UPDATE SET
				source = EXCLUDED.source,
				title = EXCLUDED.title,
				content = EXCLUDED.content,
				metadata = EXCLUDED.metadata,
				embedding = EXCLUDED.embedding,
				updated_at = now()
		`, s.tableName),
			doc.Document.ID,
			doc.Document.Source,
			doc.Document.Title,
			doc.Document.Content,
			string(metadata),
			vectorLiteral(doc.Vector),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *PGVectorStore) Search(ctx context.Context, vector []float64, topK int, filter map[string]string) ([]Document, error) {
	if len(vector) != s.dimensions {
		return nil, fmt.Errorf("query embedding dimension mismatch: got %d want %d", len(vector), s.dimensions)
	}
	if topK <= 0 {
		topK = 5
	}
	args := []any{vectorLiteral(vector)}
	where := ""
	for _, value := range filter {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		args = append(args, "%"+strings.ToLower(value)+"%")
		where += fmt.Sprintf(" AND lower(coalesce(metadata->>'tags', '')) LIKE $%d", len(args))
	}
	args = append(args, topK)
	limitParam := len(args)

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, source, title, content, metadata, 1 - (embedding <=> $1::vector) AS score, updated_at
		FROM %s
		WHERE embedding IS NOT NULL%s
		ORDER BY embedding <=> $1::vector
		LIMIT $%d
	`, s.tableName, where, limitParam), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	docs := make([]Document, 0, topK)
	for rows.Next() {
		var (
			doc      Document
			metadata []byte
			updated  time.Time
		)
		if err := rows.Scan(&doc.ID, &doc.Source, &doc.Title, &doc.Content, &metadata, &doc.Score, &updated); err != nil {
			return nil, err
		}
		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &doc.Metadata); err != nil {
				return nil, fmt.Errorf("decode rag metadata: %w", err)
			}
		}
		doc.UpdatedAt = updated
		docs = append(docs, doc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return docs, nil
}

func (s *PGVectorStore) ensureSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL,
			metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
			embedding vector(%d) NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`, s.tableName, s.dimensions)); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_source ON %s (source)`, s.tableName, s.tableName)); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_metadata ON %s USING GIN (metadata)`, s.tableName, s.tableName)); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_embedding ON %s USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)`, s.tableName, s.tableName)); err != nil {
		return err
	}
	return nil
}

func vectorLiteral(vector []float64) string {
	parts := make([]string, len(vector))
	for i, value := range vector {
		parts[i] = strconv.FormatFloat(value, 'g', -1, 64)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
