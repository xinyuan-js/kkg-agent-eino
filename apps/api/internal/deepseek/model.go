package deepseek

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const defaultModel = "deepseek-v4-pro"

const toolCallProtocolInstruction = "When tools are provided, you must use the standard tool_calls field from the chat completion API for every tool invocation. Do not emit DSML, XML, markdown tags, pseudo function syntax, or any textual representation of tool calls in content. If you need a tool, return an assistant message whose content is empty or brief and whose tool_calls field is populated."

const toolCallProtocolRepairInstruction = "Your previous response violated the tool calling protocol. Retry now. If you need a tool, respond only with a valid tool_calls field from the API. Do not place tool invocation markup, DSML, XML, or pseudo calls in content."

type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

type ChatModel struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
	tools   []*schema.ToolInfo
}

func NewChatModel(cfg Config) (*ChatModel, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("deepseek api key is required")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = defaultModel
	}
	if model == "deepseekv4pro" {
		model = defaultModel
	}
	client := cfg.HTTP
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	return &ChatModel{baseURL: baseURL, apiKey: apiKey, model: model, http: client}, nil
}

func (m *ChatModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	next := *m
	next.tools = append([]*schema.ToolInfo(nil), tools...)
	return &next, nil
}

func (m *ChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	reqBody, tools := m.buildRequest(input, opts...)
	respMsg, err := m.generateOnce(ctx, reqBody)
	if err != nil {
		return nil, err
	}
	if len(tools) > 0 && violatesToolCallProtocol(respMsg) {
		reqBody.Messages = append(reqBody.Messages, respMsg, chatMessage{
			Role:    string(schema.System),
			Content: toolCallProtocolRepairInstruction,
		})
		respMsg, err = m.generateOnce(ctx, reqBody)
		if err != nil {
			return nil, err
		}
		if violatesToolCallProtocol(respMsg) {
			return nil, fmt.Errorf("deepseek returned non-standard tool invocation content; expected structured tool_calls")
		}
	}
	return fromDeepSeekMessage(respMsg), nil
}

func (m *ChatModel) buildRequest(input []*schema.Message, opts ...einomodel.Option) (chatRequest, []*schema.ToolInfo) {
	options := einomodel.GetCommonOptions(&einomodel.Options{Model: &m.model}, opts...)
	modelName := m.model
	if options.Model != nil && strings.TrimSpace(*options.Model) != "" {
		modelName = strings.TrimSpace(*options.Model)
	}
	reqBody := chatRequest{
		Model:    modelName,
		Messages: make([]chatMessage, 0, len(input)),
	}
	if options.Temperature != nil {
		reqBody.Temperature = options.Temperature
	}
	if options.MaxTokens != nil {
		reqBody.MaxTokens = options.MaxTokens
	}
	if options.TopP != nil {
		reqBody.TopP = options.TopP
	}
	for _, msg := range input {
		reqBody.Messages = append(reqBody.Messages, toDeepSeekMessage(msg))
	}
	tools := m.tools
	if len(options.Tools) > 0 {
		tools = options.Tools
	}
	if len(tools) > 0 {
		reqBody.Messages = append(reqBody.Messages, chatMessage{
			Role:    string(schema.System),
			Content: toolCallProtocolInstruction,
		})
		reqBody.Tools = make([]chatTool, 0, len(tools))
		for _, tool := range tools {
			reqBody.Tools = append(reqBody.Tools, toDeepSeekTool(tool))
		}
	}
	return reqBody, tools
}

func (m *ChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	reqBody, _ := m.buildRequest(input, opts...)
	reqBody.Stream = true
	reader, writer := schema.Pipe[*schema.Message](1)
	go func() {
		defer writer.Close()
		if err := m.streamOnce(ctx, reqBody, writer); err != nil {
			writer.Send(nil, err)
		}
	}()
	return reader, nil
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature *float32      `json:"temperature,omitempty"`
	TopP        *float32      `json:"top_p,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Tools       []chatTool    `json:"tools,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
}

type toolCall struct {
	Index    *int         `json:"index,omitempty"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}

type functionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

type chatStreamResponse struct {
	Choices []struct {
		Delta chatMessage `json:"delta"`
	} `json:"choices"`
}

func toDeepSeekMessage(msg *schema.Message) chatMessage {
	out := chatMessage{
		Role:       string(msg.Role),
		Content:    messageTextContent(msg),
		Name:       msg.Name,
		ToolCallID: msg.ToolCallID,
	}
	if len(msg.ToolCalls) > 0 {
		out.ToolCalls = make([]toolCall, 0, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			callType := call.Type
			if callType == "" {
				callType = "function"
			}
			out.ToolCalls = append(out.ToolCalls, toolCall{
				Index: call.Index,
				ID:    call.ID,
				Type:  callType,
				Function: functionCall{
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				},
			})
		}
	}
	return out
}

func messageTextContent(msg *schema.Message) string {
	if msg == nil {
		return ""
	}
	if strings.TrimSpace(msg.Content) != "" || len(msg.UserInputMultiContent) == 0 {
		return msg.Content
	}
	var parts []string
	for _, part := range msg.UserInputMultiContent {
		if part.Type != schema.ChatMessagePartTypeText {
			continue
		}
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		parts = append(parts, part.Text)
	}
	return strings.Join(parts, "\n")
}

func fromDeepSeekMessage(msg chatMessage) *schema.Message {
	toolCalls := make([]schema.ToolCall, 0, len(msg.ToolCalls))
	for _, call := range msg.ToolCalls {
		callType := call.Type
		if callType == "" {
			callType = "function"
		}
		toolCalls = append(toolCalls, schema.ToolCall{
			Index: call.Index,
			ID:    call.ID,
			Type:  callType,
			Function: schema.FunctionCall{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return schema.AssistantMessage(strings.TrimSpace(msg.Content), toolCalls)
}

func toDeepSeekTool(info *schema.ToolInfo) chatTool {
	raw, _ := json.Marshal(info)
	var encoded struct {
		Name       string         `json:"name"`
		Desc       string         `json:"desc"`
		JSONSchema map[string]any `json:"json_schema"`
	}
	_ = json.Unmarshal(raw, &encoded)
	return chatTool{
		Type: "function",
		Function: toolFunction{
			Name:        encoded.Name,
			Description: encoded.Desc,
			Parameters:  encoded.JSONSchema,
		},
	}
}

func compactError(body []byte) string {
	value := strings.Join(strings.Fields(string(body)), " ")
	if len(value) > 300 {
		return value[:300] + "..."
	}
	return value
}

func (m *ChatModel) generateOnce(ctx context.Context, reqBody chatRequest) (chatMessage, error) {
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return chatMessage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return chatMessage{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.apiKey)

	resp, err := m.http.Do(req)
	if err != nil {
		return chatMessage{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return chatMessage{}, err
	}
	if resp.StatusCode >= 400 {
		return chatMessage{}, fmt.Errorf("deepseek chat completions returned %d: %s", resp.StatusCode, compactError(body))
	}

	var out chatResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return chatMessage{}, err
	}
	if len(out.Choices) == 0 {
		return chatMessage{}, fmt.Errorf("deepseek chat completions returned no choices")
	}
	return out.Choices[0].Message, nil
}

func (m *ChatModel) streamOnce(ctx context.Context, reqBody chatRequest, writer *schema.StreamWriter[*schema.Message]) error {
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.apiKey)

	resp, err := m.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return readErr
		}
		return fmt.Errorf("deepseek chat completions stream returned %d: %s", resp.StatusCode, compactError(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return nil
		}

		var chunk chatStreamResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("decode deepseek stream chunk: %w", err)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		msg := fromDeepSeekMessage(chunk.Choices[0].Delta)
		if msg.Role == "" {
			msg.Role = schema.Assistant
		}
		if strings.TrimSpace(msg.Content) == "" && len(msg.ToolCalls) == 0 {
			continue
		}
		writer.Send(msg, nil)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func violatesToolCallProtocol(msg chatMessage) bool {
	if len(msg.ToolCalls) > 0 {
		return false
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return false
	}
	return strings.Contains(content, "<｜｜DSML｜｜tool_calls>") ||
		strings.Contains(content, "<｜｜DSML｜｜invoke") ||
		strings.Contains(strings.ToLower(content), "tool_calls")
}
