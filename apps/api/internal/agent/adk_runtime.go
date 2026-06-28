package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"kkg-agent-eino/apps/api/internal/kkgtools"
	"kkg-agent-eino/apps/api/internal/rag"
)

func (s *Service) recordADKMessage(ctx context.Context, state *workState, msg *schema.Message, variant *adk.MessageVariant) {
	if msg == nil {
		return
	}
	switch msg.Role {
	case schema.Assistant:
		if len(msg.ToolCalls) == 0 {
			return
		}
		names := make([]string, 0, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			names = append(names, call.Function.Name)
		}
		trace := ToolTrace{Kind: "model", Name: "eino.adk.model_tool_calls", Status: "ok", Message: strings.Join(names, ", ")}
		appendTrace(ctx, state, trace)
	case schema.Tool:
		name := msg.ToolName
		if name == "" && variant != nil {
			name = variant.ToolName
		}
		if name == "" {
			name = "unknown_tool"
		}
		if isAgentToolName(name) {
			summary := compactText(extractDisplayContent(msg), 120)
			if summary == "" {
				summary = "agent completed"
			}
			trace := ToolTrace{Kind: "tool", Name: name, Status: "ok", Message: summary}
			result := ToolResult{Name: name, Status: "ok", Summary: summary, Data: extractDisplayContent(msg)}
			state.ToolResults = append(state.ToolResults, result)
			emitStreamEvent(ctx, StreamEvent{Type: "tool_result", Trace: &trace, Result: &result})
			appendTrace(ctx, state, trace)
			return
		}
		payload, err := decodeToolPayload(msg)
		if err != nil {
			if isKKGToolName(name) {
				trace := ToolTrace{Kind: "tool", Name: name, Status: "error", Message: err.Error()}
				result := ToolResult{Name: name, Status: "error", Summary: err.Error(), Data: extractDisplayContent(msg)}
				state.ToolResults = append(state.ToolResults, result)
				emitStreamEvent(ctx, StreamEvent{Type: "tool_result", Trace: &trace, Result: &result})
				appendTrace(ctx, state, trace)
				return
			}
			summary := compactText(extractDisplayContent(msg), 120)
			if summary == "" {
				summary = err.Error()
			}
			trace := ToolTrace{Kind: "tool", Name: name, Status: "ok", Message: summary}
			result := ToolResult{Name: name, Status: "ok", Summary: summary, Data: extractDisplayContent(msg)}
			state.ToolResults = append(state.ToolResults, result)
			emitStreamEvent(ctx, StreamEvent{Type: "tool_result", Trace: &trace, Result: &result})
			appendTrace(ctx, state, trace)
			return
		}
		status := "ok"
		if payload.Error != nil || !payload.OK {
			status = "error"
		}
		summary := strings.TrimSpace(payload.Summary)
		if summary == "" {
			summary = "completed"
		}
		trace := ToolTrace{Kind: "tool", Name: name, Status: status, Message: summary}
		result := ToolResult{
			Name:    name,
			Status:  status,
			Summary: summary,
			Data:    payload.Data,
			Error:   payload.Error,
		}
		state.ToolResults = append(state.ToolResults, result)
		appendTrace(ctx, state, trace)
		if isRAGToolName(name) && status == "ok" {
			docs := decodeRAGDocuments(payload.Data)
			state.RAGDocs = docs
			emitStreamEvent(ctx, StreamEvent{
				Type:    "rag",
				RAGDocs: docs,
				Trace:   &trace,
			})
		}
		emitStreamEvent(ctx, StreamEvent{Type: "tool_result", Trace: &trace, Result: &result})
	}
}

func collectADKMessage(ctx context.Context, variant *adk.MessageVariant, emitAssistantChunks bool) (*schema.Message, bool, TokenUsage, error) {
	if variant == nil {
		return nil, false, TokenUsage{}, nil
	}
	if !variant.IsStreaming {
		msg, err := variant.GetMessage()
		return msg, false, TokenUsage{}, err
	}
	if variant.MessageStream == nil {
		return nil, false, TokenUsage{}, fmt.Errorf("streaming message output is missing stream")
	}
	defer variant.MessageStream.Close()

	var chunks []*schema.Message
	var usage TokenUsage
	streamed := false
	for {
		chunk, err := variant.MessageStream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, streamed, usage, err
		}
		if chunk == nil {
			continue
		}
		usage.add(tokenUsageFromMessage(chunk))
		chunks = append(chunks, chunk)
		if emitAssistantChunks && variant.Role == schema.Assistant && strings.TrimSpace(chunk.Content) != "" {
			emitStreamEvent(ctx, StreamEvent{Type: "message", Message: chunk.Content})
			streamed = true
		}
	}
	if len(chunks) == 0 {
		return nil, streamed, usage, nil
	}
	msg, err := schema.ConcatMessages(chunks)
	return msg, streamed, usage, err
}

func isRouterAgentEvent(event *adk.AgentEvent) bool {
	if event == nil {
		return false
	}
	if event.AgentName == routerAgentName {
		return true
	}
	if len(event.RunPath) != 1 {
		return false
	}
	return event.RunPath[0].String() == routerAgentName
}

func isAgentToolName(name string) bool {
	switch name {
	case platformAgentName, blogAgentName, questionAgentName:
		return true
	default:
		return false
	}
}

func isKKGToolName(name string) bool {
	return strings.HasPrefix(name, "kkg_blog_") || strings.HasPrefix(name, "kkg_oj_")
}

func isRAGToolName(name string) bool {
	return name == "kkg_rag_search_questions"
}

func decodeToolPayload(msg *schema.Message) (*kkgtools.ResultPayload, error) {
	raw := strings.TrimSpace(msg.Content)
	if raw == "" && len(msg.UserInputMultiContent) > 0 {
		for _, part := range msg.UserInputMultiContent {
			if part.Type != schema.ChatMessagePartTypeText {
				continue
			}
			if strings.TrimSpace(part.Text) == "" {
				continue
			}
			raw = part.Text
			break
		}
	}
	if raw == "" {
		return nil, fmt.Errorf("empty tool result message")
	}

	var payload kkgtools.ResultPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("decode tool result payload: %w", err)
	}
	return &payload, nil
}

func decodeRAGDocuments(data any) []rag.Document {
	if data == nil {
		return nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	var docs []rag.Document
	if err := json.Unmarshal(raw, &docs); err != nil {
		return nil
	}
	return docs
}

func approvalDecisionFromRequest(req RunRequest) (kkgtools.SubmitApprovalDecision, bool) {
	switch strings.ToLower(strings.TrimSpace(req.ApprovalAction)) {
	case approvalReplyApprove:
		return kkgtools.SubmitApprovalDecision{Approved: true}, true
	case approvalReplyReject:
		return kkgtools.SubmitApprovalDecision{Approved: false}, true
	default:
		return kkgtools.SubmitApprovalDecision{}, false
	}
}

func approvalFromInterruptEvent(event *adk.AgentEvent, sessionID string) *ApprovalRequest {
	if event == nil || event.Action == nil || event.Action.Interrupted == nil {
		return nil
	}
	for _, ctx := range event.Action.Interrupted.InterruptContexts {
		if ctx == nil || !ctx.IsRootCause {
			continue
		}
		info, ok := approvalInfoFromInterruptContext(ctx.Info)
		if !ok || info.Action != approvalActionSubmit {
			continue
		}
		return &ApprovalRequest{
			ID:          strings.TrimSpace(ctx.ID),
			Action:      info.Action,
			Title:       info.Title,
			Message:     info.Message,
			SessionID:   sessionID,
			QuestionID:  info.QuestionID,
			Language:    info.Language,
			CodeChars:   info.CodeChars,
			CodeLines:   info.CodeLines,
			RequestedAt: time.Now().Format(time.RFC3339),
		}
	}
	return nil
}

func approvalInfoFromInterruptContext(value any) (kkgtools.SubmitApprovalInfo, bool) {
	switch info := value.(type) {
	case kkgtools.SubmitApprovalInfo:
		return info, true
	case *kkgtools.SubmitApprovalInfo:
		if info == nil {
			return kkgtools.SubmitApprovalInfo{}, false
		}
		return *info, true
	case map[string]any:
		out := kkgtools.SubmitApprovalInfo{
			Action:     strings.TrimSpace(fmt.Sprint(info["action"])),
			Title:      strings.TrimSpace(fmt.Sprint(info["title"])),
			Message:    strings.TrimSpace(fmt.Sprint(info["message"])),
			Language:   strings.TrimSpace(fmt.Sprint(info["language"])),
			CodeChars:  valueAsInt(info["code_chars"]),
			CodeLines:  valueAsInt(info["code_lines"]),
			QuestionID: valueAsInt64(info["question_id"]),
		}
		return out, out.Action != ""
	default:
		return kkgtools.SubmitApprovalInfo{}, false
	}
}

func valueAsInt64(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return n
		}
	default:
		return 0
	}
	return 0
}

func valueAsInt(value any) int {
	return int(valueAsInt64(value))
}
