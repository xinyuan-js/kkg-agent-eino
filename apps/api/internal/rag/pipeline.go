package rag

import (
	"context"
	"time"
)

type Document struct {
	ID        string            `json:"id"`
	Source    string            `json:"source"`
	Title     string            `json:"title"`
	Content   string            `json:"content"`
	Score     float64           `json:"score"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
}

type Query struct {
	Text      string   `json:"text"`
	TopK      int      `json:"top_k"`
	Tags      []string `json:"tags,omitempty"`
	RequestID string   `json:"request_id,omitempty"`
}

type Retriever interface {
	Retrieve(ctx context.Context, query Query) ([]Document, error)
}

type NoopRetriever struct{}

func (NoopRetriever) Retrieve(_ context.Context, _ Query) ([]Document, error) {
	return nil, nil
}
