package rag

import (
	"context"
	"strings"
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

type StaticRetriever struct{}

func (StaticRetriever) Retrieve(_ context.Context, query Query) ([]Document, error) {
	text := strings.TrimSpace(query.Text)
	if text == "" {
		return nil, nil
	}
	topK := query.TopK
	if topK <= 0 {
		topK = 5
	}
	docs := []Document{
		{
			ID:      "architecture:agent",
			Source:  "local-doc",
			Title:   "KKG Agent 架构约束",
			Content: "后端核心采用 CloudWeGo Eino，通过 chain 承载确定性线性流程，通过 graph 承载含工具调用、RAG 检索和条件分支的智能体编排。",
			Score:   0.82,
			Metadata: map[string]string{
				"stage": "bootstrap",
			},
		},
		{
			ID:      "kkg:auth",
			Source:  "kkg-api",
			Title:   "KKG 共享鉴权",
			Content: "博客服务签发 access_token 与 refresh_token，access_token 可通过 HttpOnly cookie 或 Authorization Bearer 传给 OJ 与智能体服务。",
			Score:   0.76,
			Metadata: map[string]string{
				"stage": "tool-prototype",
			},
		},
	}
	if len(docs) > topK {
		return docs[:topK], nil
	}
	return docs, nil
}
