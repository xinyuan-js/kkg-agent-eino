package kkg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestOJBusinessCodeBecomesAPIError(t *testing.T) {
	client := NewClient("http://kkg.local", "http://kkg.local")
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(t, http.StatusOK, map[string]any{
			"code":    40400,
			"message": "question not found",
			"data":    nil,
		}), nil
	})}

	_, err := client.GetQuestion(context.Background(), ToolContext{}, 999)
	if err == nil {
		t.Fatal("GetQuestion err = nil, want APIError")
	}
	var apiErr APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %T %[1]v, want APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound || apiErr.Message != "question not found" {
		t.Fatalf("APIError = %+v, want 404 question not found", apiErr)
	}
}

func TestHTTPErrorPreservesEnvelopeMessage(t *testing.T) {
	client := NewClient("http://kkg.local", "http://kkg.local")
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(t, http.StatusUnauthorized, map[string]any{
			"code":    40100,
			"message": "not login",
		}), nil
	})}

	_, err := client.ListSubmissions(context.Background(), ToolContext{}, SubmissionListRequest{})
	if err == nil {
		t.Fatal("ListSubmissions err = nil, want APIError")
	}
	var apiErr APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %T %[1]v, want APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized || apiErr.Message != "not login" {
		t.Fatalf("APIError = %+v, want 401 not login", apiErr)
	}
}

func TestListQuestionsAllowsPageSizeFifty(t *testing.T) {
	var requestBody PageRequest
	client := NewClient("http://kkg.local", "http://kkg.local")
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return jsonResponse(t, http.StatusOK, map[string]any{
			"code":    0,
			"message": "ok",
			"data": map[string]any{
				"records": []map[string]any{},
				"total":   0,
				"size":    50,
				"current": 1,
			},
		}), nil
	})}

	if _, err := client.ListQuestions(context.Background(), ToolContext{}, PageRequest{Current: 1, PageSize: 50}); err != nil {
		t.Fatalf("ListQuestions: %v", err)
	}
	if requestBody.PageSize != 50 {
		t.Fatalf("request pageSize = %d, want 50", requestBody.PageSize)
	}
}

func TestSubmitSolutionNormalizesSubmissionID(t *testing.T) {
	client := NewClient("http://kkg.local", "http://kkg.local")
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(t, http.StatusOK, map[string]any{
			"code":    0,
			"message": "ok",
			"data":    123,
		}), nil
	})}

	got, err := client.SubmitSolution(context.Background(), ToolContext{}, CodeSubmitRequest{
		Language: "go", Code: "package main\nfunc main(){}", QuestionID: 171,
	})
	if err != nil {
		t.Fatalf("SubmitSolution: %v", err)
	}
	record, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("SubmitSolution result = %T, want map", got)
	}
	if record["submission_id"] != int64(123) || record["question_id"] != int64(171) || record["status_label"] != "pending" {
		t.Fatalf("SubmitSolution result = %+v, want normalized pending submission", record)
	}
}

func TestGetSubmissionResultAddsStatusLabelAndPassed(t *testing.T) {
	client := NewClient("http://kkg.local", "http://kkg.local")
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(t, http.StatusOK, map[string]any{
			"code":    0,
			"message": "ok",
			"data": map[string]any{
				"records": []map[string]any{{
					"id":         123,
					"questionId": 171,
					"status":     2,
					"judgeInfo":  map[string]any{"message": "Accepted", "time": 1, "memory": 1024},
				}},
				"total":   1,
				"size":    20,
				"current": 1,
			},
		}), nil
	})}

	got, err := client.GetSubmissionResult(context.Background(), ToolContext{}, 123, 171, 0, 1)
	if err != nil {
		t.Fatalf("GetSubmissionResult: %v", err)
	}
	if got["status_label"] != "accepted" || got["passed"] != true || got["judge_message"] != "Accepted" {
		t.Fatalf("GetSubmissionResult = %+v, want accepted passed result", got)
	}
}

func TestGetSubmissionResultWithoutIDReturnsLatest(t *testing.T) {
	client := NewClient("http://kkg.local", "http://kkg.local")
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(t, http.StatusOK, map[string]any{
			"code":    0,
			"message": "ok",
			"data": map[string]any{
				"records": []map[string]any{{
					"id":         456,
					"questionId": 171,
					"status":     2,
					"judgeInfo":  map[string]any{"message": "Accepted"},
				}},
				"total":   1,
				"size":    20,
				"current": 1,
			},
		}), nil
	})}

	got, err := client.GetSubmissionResult(context.Background(), ToolContext{}, 0, 171, 0, 1)
	if err != nil {
		t.Fatalf("GetSubmissionResult latest: %v", err)
	}
	if got["id"] != float64(456) || got["status_label"] != "accepted" || got["passed"] != true {
		t.Fatalf("GetSubmissionResult latest = %+v, want latest accepted submission", got)
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
