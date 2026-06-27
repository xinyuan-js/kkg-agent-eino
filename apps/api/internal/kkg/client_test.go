package kkg

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestNewClientNormalizesOJBaseURL(t *testing.T) {
	client := NewClient("http://kkg.local", "http://kkg.local")
	if got, want := client.OJBaseURL, "http://kkg.local/api/v1/oj"; got != want {
		t.Fatalf("OJBaseURL = %q, want %q", got, want)
	}

	client = NewClient("http://kkg.local", "http://kkg.local/api/v1/oj/")
	if got, want := client.OJBaseURL, "http://kkg.local/api/v1/oj"; got != want {
		t.Fatalf("OJBaseURL = %q, want %q", got, want)
	}
}

func TestOJClientUsesMergedBackendPrefix(t *testing.T) {
	requests := make([]string, 0)
	client := NewClient("http://kkg.local", "http://kkg.local")
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())

		var body any
		switch r.URL.Path {
		case "/api/v1/oj/question/list/page/vo":
			body = map[string]any{
				"code":    0,
				"message": "ok",
				"data": map[string]any{
					"records": []map[string]any{{"id": 1, "title": "Two Sum"}},
					"total":   1,
					"size":    1,
					"current": 1,
				},
			}
		case "/api/v1/oj/question/get/vo":
			body = map[string]any{
				"code":    0,
				"message": "ok",
				"data": map[string]any{
					"id":      1,
					"title":   "Two Sum",
					"content": "Find two numbers.",
				},
			}
		case "/api/v1/oj/question/question_submit/list/page":
			body = map[string]any{
				"code":    0,
				"message": "ok",
				"data": map[string]any{
					"records": []map[string]any{{"id": 9, "questionId": 1}},
					"total":   1,
					"size":    20,
					"current": 1,
				},
			}
		default:
			body = map[string]any{"code": 404, "message": "not found"}
			return jsonResponse(t, http.StatusNotFound, body), nil
		}
		return jsonResponse(t, http.StatusOK, body), nil
	})}

	if _, err := client.ListQuestions(context.Background(), ToolContext{}, PageRequest{Current: 1, PageSize: 1}); err != nil {
		t.Fatalf("ListQuestions: %v", err)
	}
	if _, err := client.GetQuestion(context.Background(), ToolContext{}, 1); err != nil {
		t.Fatalf("GetQuestion: %v", err)
	}
	if _, err := client.ListSubmissions(context.Background(), ToolContext{}, SubmissionListRequest{}); err != nil {
		t.Fatalf("ListSubmissions: %v", err)
	}

	want := []string{
		"POST /api/v1/oj/question/list/page/vo",
		"GET /api/v1/oj/question/get/vo?id=1",
		"POST /api/v1/oj/question/question_submit/list/page",
	}
	if len(requests) != len(want) {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
	for i := range want {
		if requests[i] != want[i] {
			t.Fatalf("request[%d] = %q, want %q; all requests: %#v", i, requests[i], want[i], requests)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(t *testing.T, status int, value any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(&buf),
	}
}
