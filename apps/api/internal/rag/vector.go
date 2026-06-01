package rag

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	einoembedding "github.com/cloudwego/eino/components/embedding"
)

type VectorDocument struct {
	Document Document
	Text     string
	Vector   []float64
}

type VectorStore interface {
	Upsert(ctx context.Context, docs ...VectorDocument) error
	Search(ctx context.Context, vector []float64, topK int, filter map[string]string) ([]Document, error)
}

type SemanticRetriever struct {
	embedder einoembedding.Embedder
	store    VectorStore
}

func NewSemanticRetriever(embedder einoembedding.Embedder, store VectorStore) (*SemanticRetriever, error) {
	if embedder == nil {
		return nil, fmt.Errorf("embedding component is required")
	}
	if store == nil {
		return nil, fmt.Errorf("vector store is required")
	}
	return &SemanticRetriever{embedder: embedder, store: store}, nil
}

func (r *SemanticRetriever) Retrieve(ctx context.Context, query Query) ([]Document, error) {
	text := strings.TrimSpace(query.Text)
	if text == "" {
		return nil, nil
	}
	topK := query.TopK
	if topK <= 0 {
		topK = 5
	}
	vectors, err := r.embedder.EmbedStrings(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return nil, nil
	}
	return r.store.Search(ctx, vectors[0], topK, queryFilter(query))
}

func (r *SemanticRetriever) IndexDocuments(ctx context.Context, docs ...Document) error {
	if len(docs) == 0 {
		return nil
	}
	texts := make([]string, 0, len(docs))
	sourceDocs := make([]Document, 0, len(docs))
	for _, doc := range docs {
		text := documentEmbeddingText(doc)
		if text == "" {
			continue
		}
		texts = append(texts, text)
		sourceDocs = append(sourceDocs, doc)
	}
	if len(texts) == 0 {
		return nil
	}
	vectors, err := r.embedder.EmbedStrings(ctx, texts)
	if err != nil {
		return err
	}
	items := make([]VectorDocument, 0, len(vectors))
	for i, vector := range vectors {
		if len(vector) == 0 {
			continue
		}
		items = append(items, VectorDocument{
			Document: sourceDocs[i],
			Text:     texts[i],
			Vector:   vector,
		})
	}
	return r.store.Upsert(ctx, items...)
}

func documentEmbeddingText(doc Document) string {
	var parts []string
	if title := strings.TrimSpace(doc.Title); title != "" {
		parts = append(parts, "标题："+title)
	}
	if content := strings.TrimSpace(doc.Content); content != "" {
		parts = append(parts, content)
	}
	for key, value := range doc.Metadata {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		parts = append(parts, key+"："+value)
	}
	return strings.Join(parts, "\n")
}

func queryFilter(query Query) map[string]string {
	if len(query.Tags) == 0 {
		return nil
	}
	filter := make(map[string]string, len(query.Tags))
	for _, tag := range query.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		filter["tag:"+tag] = tag
	}
	return filter
}

type InMemoryVectorStore struct {
	mu   sync.RWMutex
	docs []VectorDocument
}

func NewInMemoryVectorStore() *InMemoryVectorStore {
	return &InMemoryVectorStore{}
}

func (s *InMemoryVectorStore) Upsert(_ context.Context, docs ...VectorDocument) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, doc := range docs {
		if strings.TrimSpace(doc.Document.ID) == "" || len(doc.Vector) == 0 {
			continue
		}
		replaced := false
		for i := range s.docs {
			if s.docs[i].Document.ID == doc.Document.ID {
				s.docs[i] = doc
				replaced = true
				break
			}
		}
		if !replaced {
			s.docs = append(s.docs, doc)
		}
	}
	return nil
}

func (s *InMemoryVectorStore) Search(_ context.Context, vector []float64, topK int, filter map[string]string) ([]Document, error) {
	if topK <= 0 {
		topK = 5
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	scored := make([]Document, 0, len(s.docs))
	for _, item := range s.docs {
		if !matchesFilter(item.Document, filter) {
			continue
		}
		score := cosine(vector, item.Vector)
		doc := item.Document
		doc.Score = score
		scored = append(scored, doc)
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	if len(scored) > topK {
		return scored[:topK], nil
	}
	return scored, nil
}

func matchesFilter(doc Document, filter map[string]string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, value := range filter {
		found := false
		for _, metaValue := range doc.Metadata {
			for _, part := range strings.Split(metaValue, ",") {
				if strings.EqualFold(strings.TrimSpace(part), value) {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func cosine(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
