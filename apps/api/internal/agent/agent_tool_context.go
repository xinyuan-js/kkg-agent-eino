package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const sessionKeyAgentRuntimeContext = "kkg_agent_runtime_context"

type agentRuntimeContext struct {
	UserQuery    string         `json:"user_query,omitempty"`
	QuestionID   int64          `json:"question_id,omitempty"`
	SubmissionID int64          `json:"submission_id,omitempty"`
	Language     string         `json:"language,omitempty"`
	Code         string         `json:"code,omitempty"`
	Input        string         `json:"input,omitempty"`
	ToolPolicy   map[string]any `json:"tool_policy,omitempty"`
	IntentHints  []string       `json:"intent_hints,omitempty"`
}

type agentToolArgumentInjector struct {
	tool   einotool.InvokableTool
	inject func(context.Context, string) (string, error)
}

func (t agentToolArgumentInjector) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.tool.Info(ctx)
}

func (t agentToolArgumentInjector) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einotool.Option) (string, error) {
	arguments, err := t.inject(ctx, argumentsInJSON)
	if err != nil {
		return "", err
	}
	return t.tool.InvokableRun(ctx, arguments, opts...)
}

func withQuestionAgentRuntimeContext(tool einotool.BaseTool) (einotool.BaseTool, error) {
	invokable, ok := tool.(einotool.InvokableTool)
	if !ok {
		return nil, fmt.Errorf("question agent tool must be invokable")
	}
	return agentToolArgumentInjector{
		tool:   invokable,
		inject: injectQuestionAgentRuntimeContext,
	}, nil
}

func runtimeContextSessionValue(state workState) string {
	payload := agentRuntimeContext{
		UserQuery:    state.Query,
		QuestionID:   state.Request.QuestionID,
		SubmissionID: state.Request.SubmissionID,
		Language:     state.Request.Language,
		Code:         state.Request.Code,
		Input:        state.Request.Input,
		ToolPolicy:   state.ToolPolicy,
		IntentHints:  state.IntentHints,
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}

func runtimeContextFromSession(ctx context.Context) (agentRuntimeContext, bool) {
	value, ok := adk.GetSessionValue(ctx, sessionKeyAgentRuntimeContext)
	if !ok {
		return agentRuntimeContext{}, false
	}
	raw, ok := value.(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return agentRuntimeContext{}, false
	}
	var out agentRuntimeContext
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return agentRuntimeContext{}, false
	}
	return out, true
}

func injectQuestionAgentRuntimeContext(ctx context.Context, argumentsInJSON string) (string, error) {
	runtimeCtx, ok := runtimeContextFromSession(ctx)
	if !ok {
		return argumentsInJSON, nil
	}
	return injectQuestionAgentArguments(argumentsInJSON, runtimeCtx)
}

func injectQuestionAgentArguments(argumentsInJSON string, runtimeCtx agentRuntimeContext) (string, error) {
	args := map[string]any{}
	if strings.TrimSpace(argumentsInJSON) != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return "", fmt.Errorf("decode question agent arguments: %w", err)
		}
	}

	putInt64IfMissing(args, "question_id", runtimeCtx.QuestionID)
	putInt64IfMissing(args, "submission_id", runtimeCtx.SubmissionID)
	putStringIfMissing(args, "language", runtimeCtx.Language)
	putStringIfMissing(args, "code", runtimeCtx.Code)
	putStringIfMissing(args, "input", runtimeCtx.Input)
	if len(runtimeCtx.ToolPolicy) > 0 {
		if _, ok := args["tool_policy"]; !ok {
			args["tool_policy"] = runtimeCtx.ToolPolicy
		}
	}
	if len(runtimeCtx.IntentHints) > 0 {
		if _, ok := args["intent_hints"]; !ok {
			args["intent_hints"] = runtimeCtx.IntentHints
		}
	}
	if shouldForceSubmit(args, runtimeCtx.ToolPolicy) {
		args["submit"] = true
	}
	if request := buildQuestionAgentRequest(args, runtimeCtx); request != "" {
		args["request"] = request
	}

	raw, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("encode question agent arguments: %w", err)
	}
	return string(raw), nil
}

func putStringIfMissing(args map[string]any, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	if current, ok := args[key]; ok && strings.TrimSpace(fmt.Sprint(current)) != "" {
		return
	}
	args[key] = value
}

func putInt64IfMissing(args map[string]any, key string, value int64) {
	if value <= 0 {
		return
	}
	if current, ok := args[key]; ok && valueAsInt64(current) > 0 {
		return
	}
	args[key] = value
}

func shouldForceSubmit(args map[string]any, toolPolicy map[string]any) bool {
	if len(toolPolicy) == 0 {
		return false
	}
	submitIntent, _ := toolPolicy["submit_intent"].(bool)
	judgeIntent, _ := toolPolicy["judge_intent"].(bool)
	if !submitIntent || judgeIntent {
		return false
	}
	if current, ok := args["submit"].(bool); ok && current {
		return false
	}
	return true
}

func buildQuestionAgentRequest(args map[string]any, runtimeCtx agentRuntimeContext) string {
	current := strings.TrimSpace(fmt.Sprint(args["request"]))
	if current == "" || current == "<nil>" {
		current = strings.TrimSpace(runtimeCtx.UserQuery)
	}
	if current == "" {
		return ""
	}

	contextParts := make([]string, 0, 6)
	if id := valueAsInt64(args["question_id"]); id > 0 {
		contextParts = append(contextParts, fmt.Sprintf("question_id=%d", id))
	}
	if id := valueAsInt64(args["submission_id"]); id > 0 {
		contextParts = append(contextParts, fmt.Sprintf("submission_id=%d", id))
	}
	if language := strings.TrimSpace(fmt.Sprint(args["language"])); language != "" && language != "<nil>" {
		contextParts = append(contextParts, "language="+language)
	}
	if strings.TrimSpace(fmt.Sprint(args["code"])) != "" {
		contextParts = append(contextParts, "code 字段已提供，请直接使用 code 字段，不要再要求用户重复提供代码")
	}
	if strings.TrimSpace(fmt.Sprint(args["input"])) != "" {
		contextParts = append(contextParts, "input 字段已提供")
	}
	if submit, _ := args["submit"].(bool); submit {
		contextParts = append(contextParts, "submit=true")
	}

	policyParts := make([]string, 0, len(runtimeCtx.ToolPolicy))
	for _, key := range []string{"logged_in", "disable_rag", "submit_intent", "judge_intent", "requires_submit_confirmation", "question_id_status", "submission_id_status", "code_status"} {
		value, ok := runtimeCtx.ToolPolicy[key]
		if !ok {
			continue
		}
		policyParts = append(policyParts, fmt.Sprintf("%s=%v", key, value))
	}

	sections := []string{"用户请求：" + current}
	if len(contextParts) > 0 {
		sections = append(sections, "可用上下文："+strings.Join(contextParts, "；"))
	}
	if len(policyParts) > 0 {
		sections = append(sections, "工具策略："+strings.Join(policyParts, "；"))
	}
	if len(runtimeCtx.IntentHints) > 0 {
		sections = append(sections, "意图提示："+strings.Join(runtimeCtx.IntentHints, "；"))
	}
	return strings.Join(sections, "\n")
}
