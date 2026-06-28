package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"kkg-agent-eino/apps/api/internal/kkgtools"
)

func TestNormalizeApprovalApproveMarksSubmitIntent(t *testing.T) {
	svc := &Service{}
	got, err := svc.normalize(context.Background(), RunRequest{
		SessionID:      "sess_1",
		ApprovalID:     "interrupt_1",
		ApprovalAction: approvalReplyApprove,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Request.Submit {
		t.Fatalf("RunRequest = %+v, want submit=true", got.Request)
	}
	if got.Query != "确认提交代码" {
		t.Fatalf("Query = %q, want confirmation placeholder", got.Query)
	}
}

func TestNormalizeApprovalRejectReturnsDirectAnswer(t *testing.T) {
	svc := &Service{}
	got, err := svc.normalize(context.Background(), RunRequest{
		SessionID:      "sess_2",
		ApprovalID:     "interrupt_2",
		ApprovalAction: approvalReplyReject,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.DirectAnswer != "已取消本次代码提交。" {
		t.Fatalf("DirectAnswer = %q, want cancel message", got.DirectAnswer)
	}
}

func TestDirectRestatementAnswer(t *testing.T) {
	got := directRestatementAnswer(`复述一下： package main import "fmt"`)
	want := "```text\npackage main import \"fmt\"\n```"
	if got != want {
		t.Fatalf("directRestatementAnswer() = %q, want %q", got, want)
	}
}

func TestDirectRestatementAnswerDoesNotCatchQuestions(t *testing.T) {
	got := directRestatementAnswer(`你为啥总是 要把package main import "fmt" 删除掉?`)
	if got != "" {
		t.Fatalf("directRestatementAnswer() = %q, want empty", got)
	}
}

func TestClassifyRequestAddsHintsAndPolicy(t *testing.T) {
	svc := &Service{}
	state := workState{
		Request: RunRequest{
			Query:       "帮我运行这段代码并提交",
			QuestionID:  171,
			Code:        "package main\nfunc main(){}",
			Submit:      true,
			AccessToken: "token",
		},
		Query: "帮我运行这段代码并提交",
	}

	got, err := svc.classifyRequest(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(got.IntentHints, "explicit_question_id") || !containsString(got.IntentHints, "code_provided") {
		t.Fatalf("IntentHints = %+v, want question/code hints", got.IntentHints)
	}
	if got.ToolPolicy["question_id_status"] != "known" || got.ToolPolicy["code_status"] != "provided" {
		t.Fatalf("ToolPolicy = %+v, want known question and provided code", got.ToolPolicy)
	}
	if got.ToolPolicy["submit_intent"] != true || got.ToolPolicy["requires_submit_confirmation"] != true {
		t.Fatalf("ToolPolicy = %+v, want submit intent requiring confirmation", got.ToolPolicy)
	}
}

func TestClassifyRequestRequiresNaturalLanguageSubmitConfirmation(t *testing.T) {
	svc := &Service{}
	state := workState{
		Request: RunRequest{
			Query:       "帮我提交题目 173 的这段 Go 代码",
			QuestionID:  173,
			Code:        "package main\nfunc main(){}",
			AccessToken: "token",
		},
		Query: "帮我提交题目 173 的这段 Go 代码",
	}

	got, err := svc.classifyRequest(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(got.IntentHints, "submit_or_judge_request") {
		t.Fatalf("IntentHints = %+v, want submit_or_judge_request", got.IntentHints)
	}
	if got.ToolPolicy["submit_intent"] != true || got.ToolPolicy["submit_confirmed"] == true {
		t.Fatalf("ToolPolicy = %+v, want unconfirmed submit intent", got.ToolPolicy)
	}
	if got.ToolPolicy["requires_submit_confirmation"] != true {
		t.Fatalf("ToolPolicy = %+v, want requires_submit_confirmation true", got.ToolPolicy)
	}
}

func TestClassifyRequestRecognizesSubmitConfirmation(t *testing.T) {
	svc := &Service{}
	state := workState{
		Request: RunRequest{
			Query:       "确认提交题目 173",
			QuestionID:  173,
			Code:        "package main\nfunc main(){}",
			AccessToken: "token",
		},
		Query: "确认提交题目 173",
	}

	got, err := svc.classifyRequest(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !got.SubmitConfirmed {
		t.Fatal("SubmitConfirmed = false, want true")
	}
	if got.ToolPolicy["submit_confirmed"] != true || got.ToolPolicy["requires_submit_confirmation"] != false {
		t.Fatalf("ToolPolicy = %+v, want confirmed submit", got.ToolPolicy)
	}
}

func TestClassifyRequestSeparatesSubmissionResultQuery(t *testing.T) {
	svc := &Service{}
	state := workState{
		Request: RunRequest{
			Query:       "帮我查询题目 173 的最新提交结果是否通过",
			QuestionID:  173,
			Code:        "package main\nfunc main(){}",
			AccessToken: "token",
		},
		Query: "帮我查询题目 173 的最新提交结果是否通过",
	}

	got, err := svc.classifyRequest(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if got.ToolPolicy["judge_intent"] != true {
		t.Fatalf("ToolPolicy = %+v, want judge_intent true", got.ToolPolicy)
	}
	if got.ToolPolicy["submit_intent"] == true || got.ToolPolicy["requires_submit_confirmation"] == true {
		t.Fatalf("ToolPolicy = %+v, result query should not require submit confirmation", got.ToolPolicy)
	}
}

func TestNormalizeExtractsSubmissionIDWithoutQuestionID(t *testing.T) {
	svc := &Service{}
	got, err := svc.normalize(context.Background(), RunRequest{
		Query: "帮我查询提交 ID 22 的判题结果",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Request.SubmissionID != 22 {
		t.Fatalf("SubmissionID = %d, want 22", got.Request.SubmissionID)
	}
	if got.Request.QuestionID != 0 {
		t.Fatalf("QuestionID = %d, want 0 for submission-id query", got.Request.QuestionID)
	}
}

func TestNormalizeExtractsSubmissionAndQuestionIDs(t *testing.T) {
	svc := &Service{}
	got, err := svc.normalize(context.Background(), RunRequest{
		Query: "提交 #22 - 题目 #173",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Request.SubmissionID != 22 {
		t.Fatalf("SubmissionID = %d, want 22", got.Request.SubmissionID)
	}
	if got.Request.QuestionID != 173 {
		t.Fatalf("QuestionID = %d, want 173", got.Request.QuestionID)
	}
}

func TestClassifyRequestMarksSubmissionIDResultQuery(t *testing.T) {
	svc := &Service{}
	state := workState{
		Request: RunRequest{
			Query:        "帮我查询提交 ID 22 的判题结果",
			SubmissionID: 22,
			AccessToken:  "token",
		},
		Query: "帮我查询提交 ID 22 的判题结果",
	}

	got, err := svc.classifyRequest(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(got.IntentHints, "explicit_submission_id") {
		t.Fatalf("IntentHints = %+v, want explicit_submission_id", got.IntentHints)
	}
	if got.ToolPolicy["judge_intent"] != true || got.ToolPolicy["submission_id_status"] != "known" {
		t.Fatalf("ToolPolicy = %+v, want judge intent with known submission id", got.ToolPolicy)
	}
	if got.ToolPolicy["submit_intent"] == true || got.ToolPolicy["requires_submit_confirmation"] == true {
		t.Fatalf("ToolPolicy = %+v, submission result query should not require submit confirmation", got.ToolPolicy)
	}
}

func TestClassifyRequestInheritsCodeAndQuestionFromHistory(t *testing.T) {
	svc := &Service{}
	state := workState{
		Request: RunRequest{
			Query:       "帮我把上面的代码提交一下",
			AccessToken: "token",
		},
		Query: "帮我把上面的代码提交一下",
		History: []*schema.Message{
			schema.UserMessage("题目 173 怎么写"),
			schema.AssistantMessage("题目 173 参考代码：\n<kkg-code lang=\"go\">\npackage main\nfunc main(){}\n</kkg-code>", nil),
		},
	}

	got, err := svc.classifyRequest(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if got.Request.QuestionID != 173 {
		t.Fatalf("QuestionID = %d, want 173", got.Request.QuestionID)
	}
	if strings.TrimSpace(got.Request.Code) == "" {
		t.Fatal("Request.Code is empty, want inherited code")
	}
	if got.ToolPolicy["submit_intent"] != true || got.ToolPolicy["requires_submit_confirmation"] != true {
		t.Fatalf("ToolPolicy = %+v, want inherited submit intent requiring confirmation", got.ToolPolicy)
	}
	if !containsString(got.IntentHints, "context_reference") {
		t.Fatalf("IntentHints = %+v, want context_reference", got.IntentHints)
	}
}

func TestClassifyRequestInheritsImplicitSubmitContextFromHistory(t *testing.T) {
	svc := &Service{}
	state := workState{
		Request: RunRequest{
			Query:       "提交一下",
			AccessToken: "token",
		},
		Query: "提交一下",
		History: []*schema.Message{
			schema.UserMessage("题目 173 怎么写"),
			schema.AssistantMessage("题目 173 参考代码：\n<kkg-code lang=\"go\">\npackage main\nfunc main(){}\n</kkg-code>", nil),
		},
	}

	got, err := svc.classifyRequest(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if got.Request.QuestionID != 173 {
		t.Fatalf("QuestionID = %d, want 173", got.Request.QuestionID)
	}
	if strings.TrimSpace(got.Request.Code) == "" {
		t.Fatal("Request.Code is empty, want inherited code")
	}
	if got.ToolPolicy["submit_intent"] != true || got.ToolPolicy["requires_submit_confirmation"] != true {
		t.Fatalf("ToolPolicy = %+v, want submit intent requiring confirmation", got.ToolPolicy)
	}
	if !containsString(got.IntentHints, "context_reference") {
		t.Fatalf("IntentHints = %+v, want context_reference", got.IntentHints)
	}
}

func TestClassifyRequestInheritsStructuredSubmissionContextFromHistory(t *testing.T) {
	svc := &Service{}
	state := workState{
		Request: RunRequest{
			Query:       "查一下是否通过",
			AccessToken: "token",
		},
		Query: "查一下是否通过",
		History: []*schema.Message{
			schema.UserMessage(`{"user_query":"帮我提交题目 173","question_id":173,"submission_id":22,"code":"package main\nfunc main(){}"}`),
		},
	}

	got, err := svc.classifyRequest(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if got.Request.QuestionID != 173 {
		t.Fatalf("QuestionID = %d, want 173", got.Request.QuestionID)
	}
	if got.Request.SubmissionID != 22 {
		t.Fatalf("SubmissionID = %d, want 22", got.Request.SubmissionID)
	}
	if got.ToolPolicy["judge_intent"] != true || got.ToolPolicy["submit_intent"] == true {
		t.Fatalf("ToolPolicy = %+v, want judge intent without submit intent", got.ToolPolicy)
	}
	if !containsString(got.IntentHints, "context_reference") {
		t.Fatalf("IntentHints = %+v, want context_reference", got.IntentHints)
	}
}

func TestApprovalFromInterruptEventCreatesApprovalRequest(t *testing.T) {
	event := &adk.AgentEvent{
		Action: &adk.AgentAction{
			Interrupted: &adk.InterruptInfo{
				InterruptContexts: []*adk.InterruptCtx{{
					ID:          "interrupt_1",
					IsRootCause: true,
					Info: kkgtools.SubmitApprovalInfo{
						Action:     approvalActionSubmit,
						Title:      "确认提交代码",
						Message:    "准备提交题目 173 的 GO 代码。确认后将正式提交到 KKG OJ。",
						QuestionID: 173,
						Language:   "go",
						CodeChars:  24,
						CodeLines:  2,
					},
				}},
			},
		},
	}

	got := approvalFromInterruptEvent(event, "sess_approval")
	if got == nil {
		t.Fatal("approvalFromInterruptEvent() = nil, want approval payload")
	}
	if got.ID != "interrupt_1" || got.QuestionID != 173 || got.CodeLines != 2 {
		t.Fatalf("ApprovalRequest = %+v, want hydrated approval payload", got)
	}
}

func TestApprovalSafeMessagesDropsIntermediateToolChain(t *testing.T) {
	messages := []*schema.Message{
		schema.UserMessage("帮我提交代码"),
		{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				ID:       "call_1",
				Function: schema.FunctionCall{Name: "kkg_question_agent"},
			}},
		},
		{
			Role:       schema.Tool,
			ToolCallID: "call_1",
			ToolName:   "kkg_question_agent",
			Content:    "partial tool output",
		},
	}

	got := approvalSafeMessages(messages, "本次代码提交需要你的确认。请使用下方的确认或取消操作继续。")
	if len(got) != 2 {
		t.Fatalf("len(approvalSafeMessages) = %d, want 2", len(got))
	}
	if got[0].Role != schema.User || got[1].Role != schema.Assistant {
		t.Fatalf("approvalSafeMessages roles = %s/%s, want user/assistant", got[0].Role, got[1].Role)
	}
}

func TestNormalizeExtractsQuestionIDFromNaturalLanguage(t *testing.T) {
	svc := &Service{}
	got, err := svc.normalize(context.Background(), RunRequest{
		Query: "能否 不使用 rag 实现一下 题面检索 173?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Request.QuestionID != 173 {
		t.Fatalf("QuestionID = %d, want 173", got.Request.QuestionID)
	}
}

func TestSanitizeHistoryDropsIncompleteToolCallTail(t *testing.T) {
	history := []*schema.Message{
		schema.UserMessage("帮我提交代码"),
		{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				ID:       "call_1",
				Function: schema.FunctionCall{Name: "kkg_question_agent"},
			}},
		},
	}

	got := sanitizeHistory(history, 10)
	if len(got) != 1 {
		t.Fatalf("len(sanitizeHistory) = %d, want 1", len(got))
	}
	if got[0].Role != schema.User {
		t.Fatalf("sanitizeHistory role = %s, want user", got[0].Role)
	}
}

func TestClassifyRequestDisablesRAGForExplicitQuestionDetail(t *testing.T) {
	svc := &Service{}
	state := workState{
		Request: RunRequest{QuestionID: 173},
		Query:   "能否 不使用 rag 实现一下 题面检索 173?",
	}

	got, err := svc.classifyRequest(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(got.IntentHints, "no_rag") || !containsString(got.IntentHints, "question_detail_request") {
		t.Fatalf("IntentHints = %+v, want no_rag and question_detail_request", got.IntentHints)
	}
	if got.ToolPolicy["disable_rag"] != true || got.ToolPolicy["question_id_status"] != "known" {
		t.Fatalf("ToolPolicy = %+v, want disable_rag and known question", got.ToolPolicy)
	}
}

func TestNormalizeKKGCodeProtocolRemovesWrappingFence(t *testing.T) {
	input := "```go\n<kkg-code lang=\"go\">\npackage main\n</kkg-code>\n```"
	want := "<kkg-code lang=\"go\">\npackage main\n</kkg-code>"
	if got := normalizeKKGCodeProtocol(input); got != want {
		t.Fatalf("normalizeKKGCodeProtocol() = %q, want %q", got, want)
	}
}

func TestCollectMessageUsagePrefersStreamedUsage(t *testing.T) {
	state := workState{StartedAt: time.Now()}
	msg := schema.AssistantMessage("ok", nil)
	msg.ResponseMeta = &schema.ResponseMeta{
		Usage: &schema.TokenUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
	}

	collectMessageUsage(context.Background(), &state, msg, TokenUsage{
		PromptTokens:     3,
		CompletionTokens: 4,
		TotalTokens:      7,
	})

	if state.ModelCalls != 1 {
		t.Fatalf("ModelCalls = %d, want 1", state.ModelCalls)
	}
	if state.TokenUsage.TotalTokens != 7 {
		t.Fatalf("TotalTokens = %d, want streamed usage 7", state.TokenUsage.TotalTokens)
	}
}

func TestCollectMessageUsageFallsBackToMessageUsage(t *testing.T) {
	state := workState{StartedAt: time.Now()}
	msg := schema.AssistantMessage("ok", nil)
	msg.ResponseMeta = &schema.ResponseMeta{
		Usage: &schema.TokenUsage{
			PromptTokens:     11,
			CompletionTokens: 13,
			TotalTokens:      24,
		},
	}

	collectMessageUsage(context.Background(), &state, msg, TokenUsage{})

	if state.ModelCalls != 1 {
		t.Fatalf("ModelCalls = %d, want 1", state.ModelCalls)
	}
	if state.TokenUsage.PromptTokens != 11 || state.TokenUsage.CompletionTokens != 13 || state.TokenUsage.TotalTokens != 24 {
		t.Fatalf("TokenUsage = %+v, want message usage", state.TokenUsage)
	}
}

func TestMergeToolTracesAppendsCallbacks(t *testing.T) {
	got := mergeToolTraces(
		[]ToolTrace{{Kind: "stage", Name: "stage.normalize"}},
		[]ToolTrace{{Kind: "callback", Name: "callback.model"}},
	)
	if len(got) != 2 {
		t.Fatalf("len(merged) = %d, want 2", len(got))
	}
	if got[0].Kind != "stage" || got[1].Kind != "callback" {
		t.Fatalf("merged order = %+v, want primary then callbacks", got)
	}
}

func TestAppendTraceAddsSequenceAndTime(t *testing.T) {
	ctx := context.WithValue(context.Background(), traceSequencerKey{}, &traceSequencer{started: time.Now()})
	state := workState{}

	appendTrace(ctx, &state, ToolTrace{Kind: "stage", Name: "stage.one", Status: "ok"})
	appendTrace(ctx, &state, ToolTrace{Kind: "tool", Name: "kkg_oj_get_question", Status: "ok"})

	if len(state.ToolTrace) != 2 {
		t.Fatalf("len(ToolTrace) = %d, want 2", len(state.ToolTrace))
	}
	if state.ToolTrace[0].Seq != 1 || state.ToolTrace[1].Seq != 2 {
		t.Fatalf("trace seq = %+v, want 1 then 2", state.ToolTrace)
	}
	if state.ToolTrace[0].Timestamp == "" || state.ToolTrace[1].Timestamp == "" {
		t.Fatalf("trace timestamps = %+v, want populated timestamps", state.ToolTrace)
	}
}

func TestShouldRecordCallbackTraceFiltersInternalGraphState(t *testing.T) {
	if shouldRecordCallbackTrace("unknown", "workState") {
		t.Fatal("shouldRecordCallbackTrace unknown/workState = true, want false")
	}
	if !shouldRecordCallbackTrace("unknown", "model input: messages=3 tools=9") {
		t.Fatal("shouldRecordCallbackTrace model input = false, want true")
	}
	if got := callbackDisplayName("unknown", "model output"); got != "model" {
		t.Fatalf("callbackDisplayName = %q, want model", got)
	}
}

func TestQuestionAgentInputSchemaCarriesRouterPolicy(t *testing.T) {
	js, err := questionAgentInputSchema().ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	if js == nil || js.Properties == nil {
		t.Fatal("questionAgentInputSchema JSON schema is empty")
	}
	if _, ok := js.Properties.Get("tool_policy"); !ok {
		t.Fatal("questionAgentInputSchema missing tool_policy")
	}
	if _, ok := js.Properties.Get("intent_hints"); !ok {
		t.Fatal("questionAgentInputSchema missing intent_hints")
	}
	submit, ok := js.Properties.Get("submit")
	if !ok {
		t.Fatal("questionAgentInputSchema missing submit")
	}
	if !strings.Contains(submit.Description, "触发系统确认中断") {
		t.Fatalf("submit description = %q, want system interrupt semantics", submit.Description)
	}
}

func TestInjectQuestionAgentRuntimeContextFillsMissingRouterArguments(t *testing.T) {
	state := workState{
		Query: "提交题目 173 的代码",
		Request: RunRequest{
			QuestionID:   173,
			SubmissionID: 22,
			Language:     "go",
			Code:         "package main\nfunc main(){}",
			Input:        "1 2\n",
		},
		IntentHints: []string{"explicit_question_id", "submit_or_judge_request"},
		ToolPolicy: map[string]any{
			"submit_intent": true,
			"judge_intent":  false,
		},
	}
	gotRaw, err := injectQuestionAgentArguments(`{"request":"帮我提交"}`, agentRuntimeContext{
		UserQuery:    state.Query,
		QuestionID:   state.Request.QuestionID,
		SubmissionID: state.Request.SubmissionID,
		Language:     state.Request.Language,
		Code:         state.Request.Code,
		Input:        state.Request.Input,
		ToolPolicy:   state.ToolPolicy,
		IntentHints:  state.IntentHints,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(gotRaw), &got); err != nil {
		t.Fatal(err)
	}
	if valueAsInt64(got["question_id"]) != 173 || valueAsInt64(got["submission_id"]) != 22 {
		t.Fatalf("arguments = %s, want injected question/submission ids", gotRaw)
	}
	if strings.TrimSpace(fmt.Sprint(got["code"])) == "" || got["submit"] != true {
		t.Fatalf("arguments = %s, want injected code and forced submit", gotRaw)
	}
	if request := strings.TrimSpace(fmt.Sprint(got["request"])); !strings.Contains(request, "用户请求：帮我提交") || !strings.Contains(request, "question_id=173") {
		t.Fatalf("request = %q, want self-contained task context", request)
	}
	if _, ok := got["tool_policy"]; !ok {
		t.Fatalf("arguments = %s, want injected tool_policy", gotRaw)
	}
}

func TestInjectQuestionAgentRuntimeContextBuildsRequestForEmptyRouterArguments(t *testing.T) {
	gotRaw, err := injectQuestionAgentArguments(`{}`, agentRuntimeContext{
		UserQuery:  "提交一下",
		QuestionID: 173,
		Language:   "go",
		Code:       "package main\nfunc main(){}",
		ToolPolicy: map[string]any{
			"logged_in":                    true,
			"submit_intent":                true,
			"requires_submit_confirmation": true,
			"code_status":                  "provided",
			"question_id_status":           "known",
		},
		IntentHints: []string{"context_reference", "submit_or_judge_request"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(gotRaw), &got); err != nil {
		t.Fatal(err)
	}
	request := strings.TrimSpace(fmt.Sprint(got["request"]))
	if request == "" || !strings.Contains(request, "用户请求：提交一下") || !strings.Contains(request, "code 字段已提供") {
		t.Fatalf("request = %q, want self-contained non-empty request", request)
	}
	if valueAsInt64(got["question_id"]) != 173 || strings.TrimSpace(fmt.Sprint(got["code"])) == "" || got["submit"] != true {
		t.Fatalf("arguments = %s, want injected question id, code and submit", gotRaw)
	}
}

func TestInjectQuestionAgentRuntimeContextDoesNotForceSubmitForJudgeQuery(t *testing.T) {
	state := workState{
		Query: "查询提交结果",
		ToolPolicy: map[string]any{
			"submit_intent": true,
			"judge_intent":  true,
		},
	}
	gotRaw, err := injectQuestionAgentArguments(`{}`, agentRuntimeContext{
		UserQuery:  state.Query,
		ToolPolicy: state.ToolPolicy,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(gotRaw), &got); err != nil {
		t.Fatal(err)
	}
	if got["submit"] == true {
		t.Fatalf("arguments = %s, judge query should not force submit", gotRaw)
	}
}
