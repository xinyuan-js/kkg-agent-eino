package kkgtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/schema"

	"kkg-agent-eino/apps/api/internal/kkg"
)

type ResultPayload struct {
	Tool    string       `json:"tool"`
	OK      bool         `json:"ok"`
	Summary string       `json:"summary,omitempty"`
	Data    any          `json:"data,omitempty"`
	Error   *ResultError `json:"error,omitempty"`
}

type ResultError struct {
	Type       string `json:"type"`
	Message    string `json:"message"`
	StatusCode int    `json:"status_code,omitempty"`
}

func executeTool[T any](ctx context.Context, toolName string, invoke func(context.Context) (T, error), summarize func(T) string) (*schema.ToolResult, error) {
	out, err := invoke(ctx)
	if err != nil {
		return newToolResult(ResultPayload{
			Tool:    toolName,
			OK:      false,
			Summary: toolErrorSummary(err),
			Error:   classifyToolError(err),
		})
	}

	summary := "completed"
	if summarize != nil {
		if value := strings.TrimSpace(summarize(out)); value != "" {
			summary = value
		}
	}

	return newToolResult(ResultPayload{
		Tool:    toolName,
		OK:      true,
		Summary: summary,
		Data:    out,
	})
}

func newToolResult(payload ResultPayload) (*schema.ToolResult, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal tool result payload: %w", err)
	}

	return &schema.ToolResult{
		Parts: []schema.ToolOutputPart{{
			Type: schema.ToolPartTypeText,
			Text: string(raw),
			Extra: map[string]any{
				"tool":    payload.Tool,
				"ok":      payload.OK,
				"summary": payload.Summary,
			},
		}},
	}, nil
}

func classifyToolError(err error) *ResultError {
	var apiErr kkg.APIError
	if errors.As(err, &apiErr) {
		errType := "upstream_error"
		switch apiErr.StatusCode {
		case http.StatusUnauthorized:
			errType = "unauthorized"
		case http.StatusNotFound:
			errType = "not_found"
		case http.StatusBadRequest:
			errType = "bad_request"
		}
		return &ResultError{
			Type:       errType,
			Message:    apiErr.Message,
			StatusCode: apiErr.StatusCode,
		}
	}

	return &ResultError{
		Type:    "tool_error",
		Message: err.Error(),
	}
}

func toolErrorSummary(err error) string {
	result := classifyToolError(err)
	if result == nil {
		return "failed"
	}
	switch result.Type {
	case "unauthorized":
		return "login required"
	case "not_found":
		return "not found"
	case "bad_request":
		return "bad request"
	default:
		return "failed"
	}
}

func summarizePageResult(page *kkg.PageResult) string {
	if page == nil {
		return "0 total"
	}
	return fmt.Sprintf("%d total", page.Total)
}
