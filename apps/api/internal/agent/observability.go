package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/callbacks"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type streamEmitterKey struct{}
type callbackTraceRecorderKey struct{}

type callbackTraceRecorder struct {
	mu     sync.Mutex
	traces []ToolTrace
}

func mergeToolTraces(primary []ToolTrace, callbackTraces []ToolTrace) []ToolTrace {
	if len(callbackTraces) == 0 {
		return primary
	}
	out := make([]ToolTrace, 0, len(primary)+len(callbackTraces))
	out = append(out, primary...)
	out = append(out, callbackTraces...)
	return out
}

func emitStreamEvent(ctx context.Context, event StreamEvent) {
	if ctx == nil {
		return
	}
	emit, ok := ctx.Value(streamEmitterKey{}).(func(StreamEvent) error)
	if !ok || emit == nil {
		return
	}
	_ = emit(event)
}

func recordCallbackTrace(ctx context.Context, trace ToolTrace) {
	if ctx == nil {
		return
	}
	recorder, _ := ctx.Value(callbackTraceRecorderKey{}).(*callbackTraceRecorder)
	if recorder != nil {
		recorder.mu.Lock()
		recorder.traces = append(recorder.traces, trace)
		recorder.mu.Unlock()
	}
	emitStreamEvent(ctx, StreamEvent{Type: "trace", Trace: &trace})
}

func callbackTracesFromContext(ctx context.Context) []ToolTrace {
	recorder, _ := ctx.Value(callbackTraceRecorderKey{}).(*callbackTraceRecorder)
	if recorder == nil {
		return nil
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	out := make([]ToolTrace, len(recorder.traces))
	copy(out, recorder.traces)
	return out
}

func appendTrace(ctx context.Context, state *workState, trace ToolTrace) {
	if state != nil {
		state.ToolTrace = append(state.ToolTrace, trace)
	}
	emitStreamEvent(ctx, StreamEvent{Type: "trace", Trace: &trace})
}

func collectMessageUsage(ctx context.Context, state *workState, msg *schema.Message, streamedUsage TokenUsage) {
	if state == nil {
		return
	}
	delta := streamedUsage
	if delta.empty() {
		delta = tokenUsageFromMessage(msg)
	}
	if delta.TotalTokens == 0 && (delta.PromptTokens > 0 || delta.CompletionTokens > 0) {
		delta.TotalTokens = delta.PromptTokens + delta.CompletionTokens
	}
	if delta.empty() {
		return
	}
	state.TokenUsage.add(delta)
	state.ModelCalls++
	emitStreamEvent(ctx, StreamEvent{
		Type: "metrics",
		Metrics: &RunMetrics{
			TokenUsage: state.TokenUsage,
			ModelCalls: state.ModelCalls,
			LatencyMS:  time.Since(state.StartedAt).Milliseconds(),
		},
	})
}

func tokenUsageFromMessage(msg *schema.Message) TokenUsage {
	if msg == nil || msg.ResponseMeta == nil || msg.ResponseMeta.Usage == nil {
		return TokenUsage{}
	}
	usage := msg.ResponseMeta.Usage
	return TokenUsage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		CachedTokens:     usage.PromptTokenDetails.CachedTokens,
		ReasoningTokens:  usage.CompletionTokensDetails.ReasoningTokens,
	}
}

func (u TokenUsage) empty() bool {
	return u.PromptTokens == 0 &&
		u.CompletionTokens == 0 &&
		u.TotalTokens == 0 &&
		u.CachedTokens == 0 &&
		u.ReasoningTokens == 0
}

func (u *TokenUsage) add(delta TokenUsage) {
	u.PromptTokens += delta.PromptTokens
	u.CompletionTokens += delta.CompletionTokens
	u.TotalTokens += delta.TotalTokens
	u.CachedTokens += delta.CachedTokens
	u.ReasoningTokens += delta.ReasoningTokens
}

func observabilityCallback() callbacks.Handler {
	type startKey struct{}
	return callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
			name := callbackName(info)
			message := callbackInputSummary(input)
			if shouldRecordCallbackTrace(name, message) {
				trace := ToolTrace{Kind: "callback", Name: "callback." + callbackDisplayName(name, message), Status: "start", Message: message}
				recordCallbackTrace(ctx, trace)
			}
			return context.WithValue(ctx, startKey{}, time.Now())
		}).
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			start, _ := ctx.Value(startKey{}).(time.Time)
			name := callbackName(info)
			message := callbackOutputSummary(output)
			if !shouldRecordCallbackTrace(name, message) {
				return ctx
			}
			trace := ToolTrace{Kind: "callback", Name: "callback." + callbackDisplayName(name, message), Status: "ok", Message: message}
			if !start.IsZero() {
				trace.DurationMS = time.Since(start).Milliseconds()
			}
			recordCallbackTrace(ctx, trace)
			return ctx
		}).
		OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			start, _ := ctx.Value(startKey{}).(time.Time)
			name := callbackName(info)
			message := err.Error()
			if !shouldRecordCallbackTrace(name, message) {
				return ctx
			}
			trace := ToolTrace{Kind: "callback", Name: "callback." + callbackDisplayName(name, message), Status: "error", Message: message}
			if !start.IsZero() {
				trace.DurationMS = time.Since(start).Milliseconds()
			}
			recordCallbackTrace(ctx, trace)
			return ctx
		}).
		Build()
}

func callbackName(info *callbacks.RunInfo) string {
	if info == nil {
		return "unknown"
	}
	if strings.TrimSpace(info.Name) != "" {
		return strings.TrimSpace(info.Name)
	}
	if strings.TrimSpace(info.Type) != "" {
		return strings.TrimSpace(info.Type)
	}
	return "unknown"
}

func callbackDisplayName(name, message string) string {
	if name != "unknown" {
		return name
	}
	if strings.HasPrefix(message, "model ") {
		return "model"
	}
	return name
}

func shouldRecordCallbackTrace(name, message string) bool {
	message = strings.TrimSpace(message)
	if message == "" || message == "workState" {
		return false
	}
	if strings.HasPrefix(message, "model input:") || strings.HasPrefix(message, "model output") {
		return true
	}
	if strings.Contains(strings.ToLower(name), "model") {
		return true
	}
	return false
}

func callbackInputSummary(input callbacks.CallbackInput) string {
	if modelInput := einomodel.ConvCallbackInput(input); modelInput != nil {
		return fmt.Sprintf("model input: messages=%d tools=%d", len(modelInput.Messages), len(modelInput.Tools))
	}
	return compactTypeName(input)
}

func callbackOutputSummary(output callbacks.CallbackOutput) string {
	if modelOutput := einomodel.ConvCallbackOutput(output); modelOutput != nil {
		if modelOutput.TokenUsage != nil {
			return fmt.Sprintf("model output: tokens=%d input=%d output=%d", modelOutput.TokenUsage.TotalTokens, modelOutput.TokenUsage.PromptTokens, modelOutput.TokenUsage.CompletionTokens)
		}
		if modelOutput.Message != nil && modelOutput.Message.ResponseMeta != nil && modelOutput.Message.ResponseMeta.Usage != nil {
			usage := modelOutput.Message.ResponseMeta.Usage
			return fmt.Sprintf("model output: tokens=%d input=%d output=%d", usage.TotalTokens, usage.PromptTokens, usage.CompletionTokens)
		}
		return "model output"
	}
	return compactTypeName(output)
}

func compactTypeName(value any) string {
	if value == nil {
		return "nil"
	}
	text := fmt.Sprintf("%T", value)
	if idx := strings.LastIndex(text, "."); idx >= 0 && idx < len(text)-1 {
		return text[idx+1:]
	}
	return text
}

func emitAnswerDeltas(ctx context.Context, answer string) {
	text := strings.TrimSpace(answer)
	if text == "" {
		return
	}
	runes := []rune(text)
	const chunkSize = 10
	for start := 0; start < len(runes); start += chunkSize {
		end := start + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		emitStreamEvent(ctx, StreamEvent{Type: "message", Message: string(runes[start:end])})
		if end < len(runes) {
			time.Sleep(18 * time.Millisecond)
		}
	}
}
