package kkg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BlogBaseURL string
	OJBaseURL   string
	HTTP        *http.Client
}

type ToolContext struct {
	AccessToken string
}

type Question struct {
	ID          int64    `json:"id"`
	Title       string   `json:"title"`
	Content     string   `json:"content"`
	Tags        []string `json:"tags,omitempty"`
	SampleCase  string   `json:"sampleCase,omitempty"`
	JudgeConfig string   `json:"judgeConfig,omitempty"`
}

type BlogSearchResult struct {
	Type  string `json:"type"`
	Items any    `json:"items"`
}

func NewClient(blogBaseURL, ojBaseURL string) *Client {
	return &Client{
		BlogBaseURL: strings.TrimRight(blogBaseURL, "/"),
		OJBaseURL:   strings.TrimRight(ojBaseURL, "/"),
		HTTP: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) SearchBlog(ctx context.Context, toolCtx ToolContext, q string, limit int) (*BlogSearchResult, error) {
	values := url.Values{}
	values.Set("type", "post")
	values.Set("q", q)
	values.Set("limit", fmt.Sprintf("%d", limit))
	var envelope struct {
		Code    int              `json:"code"`
		Message string           `json:"message"`
		Data    BlogSearchResult `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodGet, c.BlogBaseURL+"/api/v1/search?"+values.Encode(), toolCtx, nil, &envelope); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("kkg blog search failed: %s", envelope.Message)
	}
	return &envelope.Data, nil
}

func (c *Client) GetQuestion(ctx context.Context, toolCtx ToolContext, id int64) (*Question, error) {
	values := url.Values{}
	values.Set("id", fmt.Sprintf("%d", id))
	var envelope struct {
		Code    int      `json:"code"`
		Message string   `json:"message"`
		Data    Question `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodGet, c.OJBaseURL+"/api/question/get/vo?"+values.Encode(), toolCtx, nil, &envelope); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("kkg oj question get failed: %s", envelope.Message)
	}
	return &envelope.Data, nil
}

func (c *Client) doJSON(ctx context.Context, method, endpoint string, toolCtx ToolContext, body any, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := strings.TrimSpace(toolCtx.AccessToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.AddCookie(&http.Cookie{Name: "access_token", Value: token})
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("kkg request failed: %s %s returned %d", method, endpoint, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
