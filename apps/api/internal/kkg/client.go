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
	SampleCase  any      `json:"sampleCase,omitempty"`
	JudgeConfig any      `json:"judgeConfig,omitempty"`
}

type BlogSearchResult struct {
	Type  string `json:"type"`
	Items any    `json:"items"`
}

type PageRequest struct {
	Current   int64  `json:"current"`
	PageSize  int64  `json:"pageSize"`
	SortField string `json:"sortField,omitempty"`
	SortOrder string `json:"sortOrder,omitempty"`
}

type PageResult struct {
	Records any   `json:"records"`
	Total   int64 `json:"total"`
	Size    int64 `json:"size"`
	Current int64 `json:"current"`
}

type CodeRunRequest struct {
	Language   string `json:"language"`
	Code       string `json:"code"`
	QuestionID int64  `json:"questionId"`
	Input      string `json:"input,omitempty"`
}

type CodeSubmitRequest struct {
	Language   string `json:"language"`
	Code       string `json:"code"`
	QuestionID int64  `json:"questionId"`
}

type SubmissionListRequest struct {
	PageRequest
	ID         int64 `json:"id,omitempty"`
	QuestionID int64 `json:"questionId,omitempty"`
	UserID     int64 `json:"userId,omitempty"`
	Status     int32 `json:"status,omitempty"`
}

type QuestionSolutionListRequest struct {
	PageRequest
	QuestionID int64 `json:"questionId"`
}

type AuthUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url,omitempty"`
	Role      string `json:"role"`
}

type AuthSession struct {
	AccessToken string   `json:"access_token,omitempty"`
	User        AuthUser `json:"user"`
}

type APIError struct {
	StatusCode int
	Message    string
}

func (e APIError) Error() string {
	return e.Message
}

func NewClient(blogBaseURL, ojBaseURL string) *Client {
	return &Client{
		BlogBaseURL: normalizeBaseURL(blogBaseURL),
		OJBaseURL:   normalizeOJBaseURL(ojBaseURL),
		HTTP: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) Login(ctx context.Context, account, password string) (*AuthSession, []*http.Cookie, error) {
	var envelope struct {
		Code    int         `json:"code"`
		Message string      `json:"message"`
		Data    AuthSession `json:"data"`
	}
	cookies, err := c.doAuthJSON(ctx, http.MethodPost, c.BlogBaseURL+"/api/v1/auth/login", nil, map[string]string{
		"account":  account,
		"password": password,
	}, &envelope)
	if err != nil {
		return nil, nil, err
	}
	if envelope.Code != 0 {
		return nil, nil, APIError{StatusCode: http.StatusUnauthorized, Message: envelope.Message}
	}
	return &envelope.Data, cookies, nil
}

func (c *Client) Refresh(ctx context.Context, refreshToken string) (*AuthSession, []*http.Cookie, error) {
	var envelope struct {
		Code    int         `json:"code"`
		Message string      `json:"message"`
		Data    AuthSession `json:"data"`
	}
	cookies, err := c.doAuthJSON(ctx, http.MethodPost, c.BlogBaseURL+"/api/v1/auth/refresh", []*http.Cookie{
		{Name: "refresh_token", Value: refreshToken},
	}, nil, &envelope)
	if err != nil {
		return nil, nil, err
	}
	if envelope.Code != 0 {
		return nil, nil, APIError{StatusCode: http.StatusUnauthorized, Message: envelope.Message}
	}
	return &envelope.Data, cookies, nil
}

func (c *Client) Me(ctx context.Context, accessToken string) (*AuthUser, error) {
	var envelope struct {
		Code    int      `json:"code"`
		Message string   `json:"message"`
		Data    AuthUser `json:"data"`
	}
	_, err := c.doAuthJSON(ctx, http.MethodGet, c.BlogBaseURL+"/api/v1/auth/me", []*http.Cookie{
		{Name: "access_token", Value: accessToken},
	}, nil, &envelope)
	if err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		return nil, APIError{StatusCode: http.StatusUnauthorized, Message: envelope.Message}
	}
	return &envelope.Data, nil
}

func (c *Client) SearchBlog(ctx context.Context, toolCtx ToolContext, q string, limit int) (*BlogSearchResult, error) {
	return c.SearchPosts(ctx, toolCtx, q, limit)
}

func (c *Client) SearchPosts(ctx context.Context, toolCtx ToolContext, q string, limit int) (*BlogSearchResult, error) {
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

func (c *Client) ListBlogPosts(ctx context.Context, toolCtx ToolContext, limit int) (any, error) {
	values := url.Values{}
	values.Set("limit", fmt.Sprintf("%d", normalizeLimit(limit, 20, 50)))
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodGet, c.BlogBaseURL+"/api/v1/posts?"+values.Encode(), toolCtx, nil, &envelope); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("kkg blog list posts failed: %s", envelope.Message)
	}
	return envelope.Data, nil
}

func (c *Client) GetBlogPost(ctx context.Context, toolCtx ToolContext, id int64) (any, error) {
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("%s/api/v1/posts/%d", c.BlogBaseURL, id), toolCtx, nil, &envelope); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("kkg blog get post failed: %s", envelope.Message)
	}
	return envelope.Data, nil
}

func (c *Client) GetBlogPostComments(ctx context.Context, toolCtx ToolContext, postID int64, limit int) (any, error) {
	values := url.Values{}
	values.Set("limit", fmt.Sprintf("%d", normalizeLimit(limit, 20, 200)))
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("%s/api/v1/posts/%d/comments?%s", c.BlogBaseURL, postID, values.Encode()), toolCtx, nil, &envelope); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("kkg blog get comments failed: %s", envelope.Message)
	}
	return envelope.Data, nil
}

func (c *Client) GetQuestion(ctx context.Context, toolCtx ToolContext, id int64) (*Question, error) {
	values := url.Values{}
	values.Set("id", fmt.Sprintf("%d", id))
	var envelope struct {
		Code    int      `json:"code"`
		Message string   `json:"message"`
		Data    Question `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodGet, c.OJBaseURL+"/question/get/vo?"+values.Encode(), toolCtx, nil, &envelope); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("kkg oj question get failed: %s", envelope.Message)
	}
	return &envelope.Data, nil
}

func (c *Client) ListQuestions(ctx context.Context, toolCtx ToolContext, req PageRequest) (*PageResult, error) {
	req = normalizePage(req, 5, 20)
	var envelope struct {
		Code    int        `json:"code"`
		Message string     `json:"message"`
		Data    PageResult `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodPost, c.OJBaseURL+"/question/list/page/vo", toolCtx, req, &envelope); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("kkg oj list questions failed: %s", envelope.Message)
	}
	return &envelope.Data, nil
}

func (c *Client) RunCode(ctx context.Context, toolCtx ToolContext, req CodeRunRequest) (any, error) {
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodPost, c.OJBaseURL+"/question/run", toolCtx, req, &envelope); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("kkg oj run code failed: %s", envelope.Message)
	}
	return envelope.Data, nil
}

func (c *Client) SubmitSolution(ctx context.Context, toolCtx ToolContext, req CodeSubmitRequest) (any, error) {
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodPost, c.OJBaseURL+"/question/question_submit/do", toolCtx, req, &envelope); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("kkg oj submit solution failed: %s", envelope.Message)
	}
	return envelope.Data, nil
}

func (c *Client) ListSubmissions(ctx context.Context, toolCtx ToolContext, req SubmissionListRequest) (*PageResult, error) {
	req.PageRequest = normalizePage(req.PageRequest, 5, 20)
	var envelope struct {
		Code    int        `json:"code"`
		Message string     `json:"message"`
		Data    PageResult `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodPost, c.OJBaseURL+"/question/question_submit/list/page", toolCtx, req, &envelope); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("kkg oj list submissions failed: %s", envelope.Message)
	}
	return &envelope.Data, nil
}

func (c *Client) GetSubmissionResult(ctx context.Context, toolCtx ToolContext, submissionID, questionID, userID int64, maxPages int64) (map[string]any, error) {
	if submissionID <= 0 {
		return nil, fmt.Errorf("submission id is required")
	}
	if maxPages <= 0 {
		maxPages = 5
	}
	if maxPages > 20 {
		maxPages = 20
	}
	for current := int64(1); current <= maxPages; current++ {
		page, err := c.ListSubmissions(ctx, toolCtx, SubmissionListRequest{
			PageRequest: PageRequest{
				Current:   current,
				PageSize:  20,
				SortField: "createTime",
				SortOrder: "descend",
			},
			ID:         submissionID,
			QuestionID: questionID,
			UserID:     userID,
		})
		if err != nil {
			return nil, err
		}
		record, found := findSubmissionRecord(page.Records, submissionID)
		if found {
			return record, nil
		}
		if page == nil || page.Size <= 0 || current*page.Size >= page.Total {
			break
		}
	}
	return nil, APIError{StatusCode: http.StatusNotFound, Message: fmt.Sprintf("submission %d not found", submissionID)}
}

func (c *Client) ListQuestionSolutions(ctx context.Context, toolCtx ToolContext, req QuestionSolutionListRequest) (*PageResult, error) {
	req.PageRequest = normalizePage(req.PageRequest, 5, 50)
	var envelope struct {
		Code    int        `json:"code"`
		Message string     `json:"message"`
		Data    PageResult `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodPost, c.OJBaseURL+"/question/solution/list/page", toolCtx, req, &envelope); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("kkg oj list question solutions failed: %s", envelope.Message)
	}
	return &envelope.Data, nil
}

func normalizeBaseURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func normalizeOJBaseURL(value string) string {
	base := normalizeBaseURL(value)
	if base == "" {
		return base
	}
	if strings.HasSuffix(base, "/api/v1/oj") {
		return base
	}
	return base + "/api/v1/oj"
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

func normalizeLimit(limit, fallback, max int) int {
	if limit <= 0 {
		limit = fallback
	}
	if limit > max {
		return max
	}
	return limit
}

func normalizePage(req PageRequest, fallbackSize, maxSize int64) PageRequest {
	if req.Current <= 0 {
		req.Current = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = fallbackSize
	}
	if req.PageSize > maxSize {
		req.PageSize = maxSize
	}
	return req
}

func findSubmissionRecord(records any, submissionID int64) (map[string]any, bool) {
	items, ok := records.([]any)
	if !ok {
		return nil, false
	}
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if matchesSubmissionID(record["id"], submissionID) || matchesSubmissionID(record["submissionId"], submissionID) {
			return record, true
		}
	}
	return nil, false
}

func matchesSubmissionID(value any, submissionID int64) bool {
	switch v := value.(type) {
	case float64:
		return int64(v) == submissionID
	case int64:
		return v == submissionID
	case int:
		return int64(v) == submissionID
	case string:
		return strings.TrimSpace(v) == fmt.Sprintf("%d", submissionID)
	default:
		return false
	}
}

func (c *Client) doAuthJSON(ctx context.Context, method, endpoint string, cookies []*http.Cookie, body any, out any) ([]*http.Cookie, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		if strings.TrimSpace(cookie.Value) != "" {
			req.AddCookie(cookie)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.Cookies(), err
	}
	if len(rawBody) > 0 {
		if decodeErr := json.Unmarshal(rawBody, out); decodeErr != nil {
			if resp.StatusCode >= 400 {
				return resp.Cookies(), APIError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("kkg auth request failed: %s %s returned %d", method, endpoint, resp.StatusCode)}
			}
			return resp.Cookies(), decodeErr
		}
	}

	if resp.StatusCode >= 400 {
		message := fmt.Sprintf("kkg auth request failed: %s %s returned %d", method, endpoint, resp.StatusCode)
		var envelope struct {
			Message string `json:"message"`
		}
		if len(rawBody) > 0 && json.Unmarshal(rawBody, &envelope) == nil && strings.TrimSpace(envelope.Message) != "" {
			message = envelope.Message
		}
		return resp.Cookies(), APIError{StatusCode: resp.StatusCode, Message: message}
	}
	return resp.Cookies(), nil
}
