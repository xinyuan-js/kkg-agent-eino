package dashscope

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	einoembedding "github.com/cloudwego/eino/components/embedding"
)

const (
	defaultEmbeddingBaseURL    = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	defaultEmbeddingModel      = "text-embedding-v4"
	defaultEmbeddingDimensions = 1024
	maxEmbeddingBatchSize      = 10
)

type EmbeddingConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	Dimensions int
	HTTP       *http.Client
}

type Embedder struct {
	baseURL    string
	apiKey     string
	model      string
	dimensions int
	http       *http.Client
}

func NewEmbedder(cfg EmbeddingConfig) (*Embedder, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("dashscope api key is required")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultEmbeddingBaseURL
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = defaultEmbeddingModel
	}
	dimensions := cfg.Dimensions
	if dimensions <= 0 {
		dimensions = defaultEmbeddingDimensions
	}
	client := cfg.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Embedder{
		baseURL:    baseURL,
		apiKey:     apiKey,
		model:      model,
		dimensions: dimensions,
		http:       client,
	}, nil
}

func (e *Embedder) EmbedStrings(ctx context.Context, texts []string, opts ...einoembedding.Option) ([][]float64, error) {
	cleaned := make([]string, 0, len(texts))
	indexMap := make([]int, 0, len(texts))
	for i, text := range texts {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		cleaned = append(cleaned, text)
		indexMap = append(indexMap, i)
	}
	out := make([][]float64, len(texts))
	if len(cleaned) == 0 {
		return out, nil
	}

	options := einoembedding.GetCommonOptions(&einoembedding.Options{Model: &e.model}, opts...)
	model := e.model
	if options.Model != nil && strings.TrimSpace(*options.Model) != "" {
		model = strings.TrimSpace(*options.Model)
	}

	for start := 0; start < len(cleaned); start += maxEmbeddingBatchSize {
		end := start + maxEmbeddingBatchSize
		if end > len(cleaned) {
			end = len(cleaned)
		}
		vectors, err := e.embedBatch(ctx, model, cleaned[start:end])
		if err != nil {
			return nil, err
		}
		for i, vector := range vectors {
			out[indexMap[start+i]] = vector
		}
	}
	return out, nil
}

func (e *Embedder) embedBatch(ctx context.Context, model string, texts []string) ([][]float64, error) {
	reqBody := embeddingRequest{
		Model:          model,
		Input:          texts,
		Dimensions:     e.dimensions,
		EncodingFormat: "float",
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := e.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("dashscope embeddings returned %d: %s", resp.StatusCode, compactError(body))
	}

	var out embeddingResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("dashscope embeddings returned %d vectors for %d inputs", len(out.Data), len(texts))
	}
	sort.Slice(out.Data, func(i, j int) bool {
		return out.Data[i].Index < out.Data[j].Index
	})
	vectors := make([][]float64, len(out.Data))
	for i, item := range out.Data {
		vectors[i] = item.Embedding
	}
	return vectors, nil
}

type embeddingRequest struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	Dimensions     int      `json:"dimensions,omitempty"`
	EncodingFormat string   `json:"encoding_format,omitempty"`
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

func compactError(body []byte) string {
	value := strings.Join(strings.Fields(string(body)), " ")
	if len(value) > 300 {
		return value[:300] + "..."
	}
	return value
}
