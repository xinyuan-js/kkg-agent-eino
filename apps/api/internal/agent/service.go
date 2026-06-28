package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"kkg-agent-eino/apps/api/internal/kkg"
	"kkg-agent-eino/apps/api/internal/kkgtools"
	"kkg-agent-eino/apps/api/internal/memory"
	"kkg-agent-eino/apps/api/internal/rag"
)

func NewService(retriever rag.Retriever, kkgClient *kkg.Client, chatModel einomodel.BaseChatModel, memoryStore memory.Store) (*Service, error) {
	if chatModel == nil {
		return nil, fmt.Errorf("eino adk chat model is required")
	}
	if retriever == nil {
		return nil, fmt.Errorf("rag retriever is required")
	}
	if kkgClient == nil {
		return nil, fmt.Errorf("kkg client is required")
	}
	if memoryStore == nil {
		memoryStore = memory.NewInMemoryStore()
	}
	topology, err := buildAgentTopology(context.Background(), retriever, kkgClient, chatModel, memoryStore)
	if err != nil {
		return nil, err
	}

	s := &Service{
		retriever: retriever,
		chatModel: chatModel,
		tools:     topology.tools,
		runner:    topology.runner,
		memory:    memoryStore,
	}

	chain, err := s.compileChain(context.Background())
	if err != nil {
		return nil, err
	}
	graph, err := s.compileGraph(context.Background())
	if err != nil {
		return nil, err
	}
	s.chain = chain
	s.graph = graph
	return s, nil
}

func (s *Service) ToolInfos(ctx context.Context) ([]*schema.ToolInfo, error) {
	infos := make([]*schema.ToolInfo, 0, len(s.tools))
	for _, t := range s.tools {
		info, err := t.Info(ctx)
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func newRAGSearchTool(retriever rag.Retriever) (einotool.BaseTool, error) {
	if retriever == nil {
		return nil, fmt.Errorf("rag retriever is required")
	}
	return utils.InferEnhancedTool("kkg_rag_search_questions", "检索 KKG 题库向量索引。适合题目推荐、相似题、专项练习、按知识点找题和需要题库候选材料的请求。", func(ctx context.Context, input RAGSearchInput) (*schema.ToolResult, error) {
		query := strings.TrimSpace(input.Query)
		if query == "" {
			return nil, fmt.Errorf("query is required")
		}
		topK := input.TopK
		if topK <= 0 {
			topK = 8
		}
		if topK > 12 {
			topK = 12
		}
		docs, err := retriever.Retrieve(ctx, rag.Query{
			Text:      query,
			TopK:      topK,
			RequestID: requestIDFromContext(ctx),
		})
		if err != nil {
			return newAgentToolResult(kkgtools.ResultPayload{
				Tool:    "kkg_rag_search_questions",
				OK:      false,
				Summary: "retrieval failed",
				Error: &kkgtools.ResultError{
					Type:    "tool_error",
					Message: err.Error(),
				},
			})
		}
		return newAgentToolResult(kkgtools.ResultPayload{
			Tool:    "kkg_rag_search_questions",
			OK:      true,
			Summary: fmt.Sprintf("%d documents", len(docs)),
			Data:    docs,
		})
	})
}

func newAgentToolResult(payload kkgtools.ResultPayload) (*schema.ToolResult, error) {
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

func requestIDFromContext(ctx context.Context) string {
	value, ok := adk.GetSessionValue(ctx, kkgtools.SessionKeyRequestID)
	if !ok {
		return ""
	}
	requestID, _ := value.(string)
	return strings.TrimSpace(requestID)
}

func (s *Service) Run(ctx context.Context, req RunRequest) (RunResponse, error) {
	return s.run(ctx, req, nil)
}

func (s *Service) Stream(ctx context.Context, req RunRequest, emit func(StreamEvent) error) (RunResponse, error) {
	return s.run(ctx, req, emit)
}

func (s *Service) run(ctx context.Context, req RunRequest, emit func(StreamEvent) error) (RunResponse, error) {
	if req.Mode == "" {
		req.Mode = ModeGraph
	}
	switch req.Mode {
	case ModeChain:
		return s.invokeWithEmitter(ctx, s.chain, req, emit)
	case ModeGraph:
		return s.invokeWithEmitter(ctx, s.graph, req, emit)
	default:
		return RunResponse{}, fmt.Errorf("unsupported mode %q", req.Mode)
	}
}

func (s *Service) invokeWithEmitter(ctx context.Context, runnable compose.Runnable[RunRequest, RunResponse], req RunRequest, emit func(StreamEvent) error) (RunResponse, error) {
	ctx = context.WithValue(ctx, callbackTraceRecorderKey{}, &callbackTraceRecorder{})
	ctx = context.WithValue(ctx, traceSequencerKey{}, &traceSequencer{started: time.Now()})
	if emit == nil {
		return runnable.Invoke(ctx, req, compose.WithCallbacks(observabilityCallback()))
	}
	ctx = context.WithValue(ctx, streamEmitterKey{}, emit)
	return runnable.Invoke(ctx, req, compose.WithCallbacks(observabilityCallback()))
}

func (s *Service) normalize(ctx context.Context, req RunRequest) (workState, error) {
	start := time.Now()
	if strings.EqualFold(strings.TrimSpace(req.ApprovalAction), approvalReplyApprove) {
		req.Submit = true
		if strings.TrimSpace(req.Query) == "" {
			req.Query = "确认提交代码"
		}
	} else if strings.EqualFold(strings.TrimSpace(req.ApprovalAction), approvalReplyReject) {
		if strings.TrimSpace(req.Query) == "" {
			req.Query = "取消提交代码"
		}
	}
	query := strings.TrimSpace(req.Query)
	if req.SubmissionID <= 0 {
		req.SubmissionID = extractSubmissionID(query)
	}
	if req.QuestionID <= 0 {
		req.QuestionID = extractQuestionID(query)
	}
	if query == "" && req.QuestionID > 0 {
		query = fmt.Sprintf("为 KKG OJ 题目 %d 生成题解与讲解", req.QuestionID)
	}
	if query == "" {
		return workState{}, fmt.Errorf("query or question_id is required")
	}
	directAnswer := directRestatementAnswer(query)
	if strings.EqualFold(strings.TrimSpace(req.ApprovalAction), approvalReplyReject) {
		directAnswer = "已取消本次代码提交。"
	}
	state := workState{
		Request:      req,
		Query:        query,
		DirectAnswer: directAnswer,
		StartedAt:    time.Now(),
		Normalized:   true,
	}
	appendTrace(ctx, &state, ToolTrace{Kind: "stage", Name: "stage.normalize", Status: "ok", Message: "input normalized", DurationMS: time.Since(start).Milliseconds()})
	return state, nil
}

func (s *Service) classifyRequest(ctx context.Context, state workState) (workState, error) {
	start := time.Now()
	var inheritedContext bool
	state, inheritedContext = resolveContextReferences(state)
	query := strings.ToLower(state.Query)
	compactQuery := strings.ReplaceAll(query, " ", "")
	hints := make([]string, 0, 6)
	addHint := func(hint string) {
		for _, existing := range hints {
			if existing == hint {
				return
			}
		}
		hints = append(hints, hint)
	}

	if state.Request.QuestionID > 0 {
		addHint("explicit_question_id")
	}
	if state.Request.SubmissionID > 0 {
		addHint("explicit_submission_id")
	}
	if containsAny(compactQuery, "不使用rag", "不用rag", "不要rag", "禁用rag", "withoutrag", "norag") {
		addHint("no_rag")
	}
	if containsAny(query, "题面", "题目详情", "题目内容", "查看题目", "检索题面", "题目信息") {
		addHint("question_detail_request")
	}
	if strings.TrimSpace(state.Request.Code) != "" {
		addHint("code_provided")
	}
	if inheritedContext || referencesPreviousContext(query) {
		addHint("context_reference")
	}
	submitConfirmation := isSubmitConfirmation(state.Query) ||
		isSubmitConfirmationReply(state.Query, state.History) ||
		strings.EqualFold(strings.TrimSpace(state.Request.ApprovalAction), approvalReplyApprove)
	judgeIntent := containsAny(query,
		"提交结果",
		"提交记录",
		"查看结果",
		"查询结果",
		"最新提交",
		"刚才提交",
		"上次提交",
		"最近提交",
		"提交#",
		"判题",
		"是否通过",
		"通过了吗",
		"ac",
	)
	submitIntent := state.Request.Submit || (!judgeIntent && containsAny(query, "提交", "submit")) || submitConfirmation
	submitConfirmed := submitIntent && submitConfirmation
	if submitIntent || judgeIntent {
		addHint("submit_or_judge_request")
	}
	if containsAny(query, "运行", "run", "执行", "测试") && strings.TrimSpace(state.Request.Code) != "" {
		addHint("code_run_request")
	}
	if containsAny(query, "推荐", "找题", "相似题", "专项", "练习", "知识点", "题单") {
		addHint("question_recommendation")
	}
	if containsAny(query, "博客", "文章", "题解", "评论", "blog") {
		addHint("blog_or_solution_material")
	}
	if containsAny(query, "登录", "鉴权", "接口", "部署", "容器", "项目结构", "api", "docker") {
		addHint("platform_or_project_question")
	}
	if strings.TrimSpace(state.DirectAnswer) != "" {
		addHint("direct_restatement")
	}

	loggedIn := strings.TrimSpace(state.Request.AccessToken) != ""
	state.IntentHints = hints
	state.SubmitConfirmed = submitConfirmed
	questionIDStatus := "missing"
	if state.Request.QuestionID > 0 {
		questionIDStatus = "known"
	}
	codeStatus := "missing"
	if strings.TrimSpace(state.Request.Code) != "" {
		codeStatus = "provided"
	}
	submitMissing := make([]string, 0, 3)
	if !loggedIn {
		submitMissing = append(submitMissing, "login")
	}
	if state.Request.QuestionID <= 0 {
		submitMissing = append(submitMissing, "question_id")
	}
	if strings.TrimSpace(state.Request.Code) == "" {
		submitMissing = append(submitMissing, "code")
	}
	submitReady := len(submitMissing) == 0
	state.ToolPolicy = map[string]any{
		"logged_in":                    loggedIn,
		"question_id_status":           questionIDStatus,
		"submission_id_status":         submissionIDStatus(state.Request.SubmissionID),
		"code_status":                  codeStatus,
		"submit_intent":                submitIntent,
		"judge_intent":                 judgeIntent,
		"submit_ready":                 submitReady,
		"submit_missing":               submitMissing,
		"submit_confirmed":             submitConfirmed,
		"requires_submit_confirmation": submitIntent && submitReady && !submitConfirmed,
		"disable_rag":                  containsString(hints, "no_rag"),
		"prefer_rag_for_listing":       containsString(hints, "question_recommendation"),
		"guidance":                     "题目查询、题解、运行和提交结果查询可在题号/上下文确定时执行；只有正式提交代码需要用户明确确认。",
	}
	appendTrace(ctx, &state, ToolTrace{
		Kind:       "stage",
		Name:       "stage.classify_request",
		Status:     "ok",
		Message:    strings.Join(hints, ", "),
		DurationMS: time.Since(start).Milliseconds(),
		Metadata: map[string]any{
			"intent_hints": hints,
			"tool_policy":  state.ToolPolicy,
		},
	})
	rebuildRuntimeMessages(&state)
	return state, nil
}

func (s *Service) prepareSession(ctx context.Context, state workState) (workState, error) {
	start := time.Now()
	sessionID := strings.TrimSpace(state.Request.SessionID)
	if sessionID == "" {
		sessionID = newSessionID()
	}
	state.SessionID = sessionID
	emitStreamEvent(ctx, StreamEvent{Type: "session", SessionID: sessionID})

	if err := s.memory.EnsureSession(ctx, state.Request.UserID, sessionID); err != nil {
		return state, err
	}

	history, err := s.memory.LoadMessages(ctx, state.Request.UserID, sessionID, 20)
	if err != nil {
		return state, err
	}
	history = sanitizeHistory(history, 12)
	messages := make([]*schema.Message, 0, len(history)+1)
	messages = append(messages, history...)
	persistedUserMessage := schema.UserMessage(state.Query)
	state.History = history
	state.Messages = messages
	state.TurnMessages = []*schema.Message{persistedUserMessage}
	appendTrace(ctx, &state, ToolTrace{
		Kind:       "stage",
		Name:       "stage.prepare_session",
		Status:     "ok",
		Message:    fmt.Sprintf("history=%d", len(history)),
		DurationMS: time.Since(start).Milliseconds(),
		Metadata: map[string]any{
			"session_id": sessionID,
			"history":    len(history),
		},
	})
	return state, nil
}

func (s *Service) directAnswer(ctx context.Context, state workState) (workState, error) {
	start := time.Now()
	state.FinalAnswer = strings.TrimSpace(state.DirectAnswer)
	if state.FinalAnswer == "" {
		return state, fmt.Errorf("direct answer is empty")
	}
	state.TurnMessages = append(state.TurnMessages, schema.AssistantMessage(state.FinalAnswer, nil))
	appendTrace(ctx, &state, ToolTrace{
		Kind:       "stage",
		Name:       "stage.direct_answer",
		Status:     "ok",
		Message:    "answered without model for exact restatement request",
		DurationMS: time.Since(start).Milliseconds(),
	})
	return state, nil
}

func (s *Service) executeRouterAgent(ctx context.Context, state workState) (workState, error) {
	start := time.Now()
	if state.PendingApproval != nil {
		return state, nil
	}
	if strings.TrimSpace(state.DirectAnswer) != "" {
		state.FinalAnswer = state.DirectAnswer
		state.TurnMessages = append(state.TurnMessages, schema.AssistantMessage(state.FinalAnswer, nil))
		appendTrace(ctx, &state, ToolTrace{
			Kind:       "stage",
			Name:       "stage.direct_restatement",
			Status:     "ok",
			Message:    "answered without model for exact restatement request",
			DurationMS: time.Since(start).Milliseconds(),
		})
		return state, nil
	}

	runOnce := func() (*schema.Message, error) {
		sessionValues := map[string]any{
			kkgtools.SessionKeyAccessToken: state.Request.AccessToken,
			kkgtools.SessionKeyUserID:      state.Request.UserID,
			kkgtools.SessionKeyRequestID:   state.Request.RequestID,
			sessionKeyAgentRuntimeContext:  runtimeContextSessionValue(state),
		}
		var (
			iter *adk.AsyncIterator[*adk.AgentEvent]
			err  error
		)
		if decision, ok := approvalDecisionFromRequest(state.Request); ok {
			iter, err = s.runner.ResumeWithParams(ctx, state.SessionID, &adk.ResumeParams{
				Targets: map[string]any{
					strings.TrimSpace(state.Request.ApprovalID): decision,
				},
			}, adk.WithSessionValues(sessionValues))
			if err != nil {
				appendTrace(ctx, &state, ToolTrace{Kind: "stage", Name: "eino.adk.resume", Status: "error", Message: err.Error(), DurationMS: time.Since(start).Milliseconds()})
				return nil, err
			}
		} else {
			iter = s.runner.Run(ctx, state.Messages,
				adk.WithCheckPointID(state.SessionID),
				adk.WithSessionValues(sessionValues),
			)
		}

		var final *schema.Message
		for {
			event, ok := iter.Next()
			if !ok {
				break
			}
			if event == nil {
				continue
			}
			if event.Err != nil {
				appendTrace(ctx, &state, ToolTrace{Kind: "stage", Name: "eino.adk.runner", Status: "error", Message: event.Err.Error(), DurationMS: time.Since(start).Milliseconds()})
				return nil, event.Err
			}
			if event.Action != nil && event.Action.Interrupted != nil {
				if approval := approvalFromInterruptEvent(event, state.SessionID); approval != nil {
					state.PendingApproval = approval
					state.FinalAnswer = "本次代码提交需要你的确认。请使用下方的确认或取消操作继续。"
					emitStreamEvent(ctx, StreamEvent{
						Type:      "approval_required",
						SessionID: state.SessionID,
						Approval:  approval,
					})
					appendTrace(ctx, &state, ToolTrace{
						Kind:    "tool",
						Name:    "kkg_oj_submit_solution",
						Status:  "interrupt",
						Message: "awaiting user confirmation",
						Metadata: map[string]any{
							"interrupt_id": approval.ID,
						},
					})
					approvalMessage := schema.AssistantMessage(state.FinalAnswer, nil)
					state.TurnMessages = append(state.TurnMessages, approvalMessage)
					return approvalMessage, nil
				}
			}
			if event.Output == nil || event.Output.MessageOutput == nil {
				continue
			}
			isRootEvent := isRouterAgentEvent(event)
			msg, streamed, usage, err := collectADKMessage(ctx, event.Output.MessageOutput, false)
			if err != nil {
				appendTrace(ctx, &state, ToolTrace{Kind: "stage", Name: "eino.adk.message", Status: "error", Message: err.Error()})
				continue
			}
			collectMessageUsage(ctx, &state, msg, usage)
			if isRootEvent && streamed {
				state.StreamedAnswer = true
			}
			if msg != nil && isRootEvent {
				state.TurnMessages = append(state.TurnMessages, msg)
			}
			s.recordADKMessage(ctx, &state, msg, event.Output.MessageOutput)
			if state.PendingApproval != nil {
				approvalMessage := schema.AssistantMessage(state.FinalAnswer, nil)
				state.TurnMessages = append(state.TurnMessages, approvalMessage)
				return approvalMessage, nil
			}
			if isRootEvent && msg != nil && msg.Role == schema.Assistant && strings.TrimSpace(msg.Content) != "" {
				final = msg
			}
		}
		return final, nil
	}

	final, err := runOnce()
	if err != nil {
		return state, err
	}
	if final == nil || strings.TrimSpace(final.Content) == "" {
		if strings.EqualFold(strings.TrimSpace(state.Request.ApprovalAction), approvalReplyReject) {
			state.FinalAnswer = "已取消本次代码提交。"
			state.TurnMessages = append(state.TurnMessages, schema.AssistantMessage(state.FinalAnswer, nil))
			appendTrace(ctx, &state, ToolTrace{Kind: "stage", Name: "eino.adk.resume", Status: "ok", Message: "submission canceled by user", DurationMS: time.Since(start).Milliseconds()})
			return state, nil
		}
		return state, fmt.Errorf("eino adk agent returned no final answer")
	}
	state.FinalAnswer = strings.TrimSpace(final.Content)
	appendTrace(ctx, &state, ToolTrace{Kind: "stage", Name: "eino.adk.router_agent", Status: "ok", Message: "ReAct loop completed", DurationMS: time.Since(start).Milliseconds()})
	return state, nil
}

func (s *Service) postprocessAnswer(ctx context.Context, state workState) (workState, error) {
	start := time.Now()
	original := state.FinalAnswer
	state.FinalAnswer = normalizeKKGCodeProtocol(state.FinalAnswer)
	if state.FinalAnswer != original {
		replaceLastAssistantMessage(state.TurnMessages, state.FinalAnswer)
		appendTrace(ctx, &state, ToolTrace{
			Kind:       "stage",
			Name:       "stage.postprocess_answer",
			Status:     "ok",
			Message:    "normalized code rendering protocol",
			DurationMS: time.Since(start).Milliseconds(),
		})
		return state, nil
	}
	appendTrace(ctx, &state, ToolTrace{
		Kind:       "stage",
		Name:       "stage.postprocess_answer",
		Status:     "ok",
		Message:    "no changes",
		DurationMS: time.Since(start).Milliseconds(),
	})
	return state, nil
}

func (s *Service) persistSession(ctx context.Context, state workState) (workState, error) {
	start := time.Now()
	messages := state.TurnMessages
	if state.PendingApproval != nil {
		messages = approvalSafeMessages(state.TurnMessages, state.FinalAnswer)
	}
	if err := s.memory.AppendMessages(ctx, state.Request.UserID, state.SessionID, messages...); err != nil {
		appendTrace(ctx, &state, ToolTrace{Kind: "stage", Name: "stage.persist_session", Status: "error", Message: err.Error(), DurationMS: time.Since(start).Milliseconds()})
		return state, err
	}
	appendTrace(ctx, &state, ToolTrace{Kind: "stage", Name: "stage.persist_session", Status: "ok", Message: fmt.Sprintf("messages=%d", len(messages)), DurationMS: time.Since(start).Milliseconds()})
	return state, nil
}

func (s *Service) buildResponse(ctx context.Context, state workState) (RunResponse, error) {
	mode := state.Request.Mode
	if mode == "" {
		mode = ModeGraph
	}
	toolTrace := mergeToolTraces(state.ToolTrace, callbackTracesFromContext(ctx))
	out := RunResponse{
		Mode:        mode,
		SessionID:   state.SessionID,
		Answer:      state.FinalAnswer,
		RAGDocs:     state.RAGDocs,
		ToolTrace:   toolTrace,
		ToolResults: state.ToolResults,
		TokenUsage:  state.TokenUsage,
		ModelCalls:  state.ModelCalls,
		LatencyMS:   time.Since(state.StartedAt).Milliseconds(),
		RequestID:   state.Request.RequestID,
	}
	if !state.StreamedAnswer {
		emitAnswerDeltas(ctx, state.FinalAnswer)
	}
	emitStreamEvent(ctx, StreamEvent{
		Type: "metrics",
		Metrics: &RunMetrics{
			TokenUsage: state.TokenUsage,
			ModelCalls: state.ModelCalls,
			LatencyMS:  out.LatencyMS,
		},
	})
	emitStreamEvent(ctx, StreamEvent{Type: "done", SessionID: state.SessionID, Done: &out})
	return out, nil
}
