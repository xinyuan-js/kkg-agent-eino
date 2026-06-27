package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
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
	tools, err := kkgtools.New(kkgClient)
	if err != nil {
		return nil, err
	}
	ragTool, err := newRAGSearchTool(retriever)
	if err != nil {
		return nil, err
	}
	blogTools, questionOnlyTools, err := splitToolsByIntent(context.Background(), tools)
	if err != nil {
		return nil, err
	}
	questionAgent, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
		Name:        questionAgentName,
		Description: "Use this agent only for KKG OJ question explanation, solution ideas, related blogs, code run, or code submit requests.",
		Instruction: questionAgentInstruction(),
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               append(questionOnlyTools, blogTools...),
				ExecuteSequentially: true,
			},
		},
		MaxIterations: 8,
	})
	if err != nil {
		return nil, err
	}
	platformAgent, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
		Name:          platformAgentName,
		Description:   "Assistant for KKG platform capability, API, login, deployment, and project structure questions.",
		Instruction:   platformAgentInstruction(),
		Model:         chatModel,
		MaxIterations: 4,
	})
	if err != nil {
		return nil, err
	}
	blogAgent, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
		Name:        blogAgentName,
		Description: "Assistant for KKG blog article discovery, explanation, and related knowledge lookup.",
		Instruction: blogAgentInstruction(),
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               blogTools,
				ExecuteSequentially: true,
			},
		},
		MaxIterations: 6,
	})
	if err != nil {
		return nil, err
	}
	platformTool := adk.NewAgentTool(context.Background(), platformAgent, adk.WithAgentInputSchema(platformAgentInputSchema()))
	blogTool := adk.NewAgentTool(context.Background(), blogAgent, adk.WithAgentInputSchema(blogAgentInputSchema()))
	questionTool := adk.NewAgentTool(context.Background(), questionAgent, adk.WithAgentInputSchema(questionAgentInputSchema()))
	routerAgent, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
		Name:        routerAgentName,
		Description: "Top-level router agent for KKG Agent. It decides whether to answer directly or delegate to specialized sub-agents.",
		Instruction: routerAgentInstruction(),
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               []einotool.BaseTool{ragTool, platformTool, blogTool, questionTool},
				ExecuteSequentially: true,
			},
			EmitInternalEvents: true,
		},
		MaxIterations: 8,
	})
	if err != nil {
		return nil, err
	}

	s := &Service{
		retriever: retriever,
		chatModel: chatModel,
		tools:     append([]einotool.BaseTool{ragTool}, tools...),
		runner:    adk.NewRunner(context.Background(), adk.RunnerConfig{Agent: routerAgent, EnableStreaming: true, CheckPointStore: memoryStore}),
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
	if emit == nil {
		return runnable.Invoke(ctx, req, compose.WithCallbacks(observabilityCallback()))
	}
	ctx = context.WithValue(ctx, streamEmitterKey{}, emit)
	return runnable.Invoke(ctx, req, compose.WithCallbacks(observabilityCallback()))
}

func (s *Service) compileChain(ctx context.Context) (compose.Runnable[RunRequest, RunResponse], error) {
	c := compose.NewChain[RunRequest, RunResponse]()
	c.AppendLambda(compose.InvokableLambda(s.normalize), compose.WithNodeName("normalize"))
	c.AppendLambda(compose.InvokableLambda(s.prepareSession), compose.WithNodeName("prepare_session"))
	c.AppendLambda(compose.InvokableLambda(s.classifyRequest), compose.WithNodeName("classify_request"))
	c.AppendLambda(compose.InvokableLambda(s.executeRouterAgent), compose.WithNodeName("adk_chat_model_agent"))
	c.AppendLambda(compose.InvokableLambda(s.postprocessAnswer), compose.WithNodeName("postprocess_answer"))
	c.AppendLambda(compose.InvokableLambda(s.persistSession), compose.WithNodeName("persist_session"))
	c.AppendLambda(compose.InvokableLambda(s.buildResponse), compose.WithNodeName("build_response"))
	return c.Compile(ctx, compose.WithGraphName("kkg_agent_chain"))
}

func (s *Service) compileGraph(ctx context.Context) (compose.Runnable[RunRequest, RunResponse], error) {
	g := compose.NewGraph[RunRequest, RunResponse]()
	if err := g.AddLambdaNode("normalize", compose.InvokableLambda(s.normalize)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("classify_request", compose.InvokableLambda(s.classifyRequest)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("prepare_session", compose.InvokableLambda(s.prepareSession)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("direct_answer", compose.InvokableLambda(s.directAnswer)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("adk_chat_model_agent", compose.InvokableLambda(s.executeRouterAgent)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("postprocess_answer", compose.InvokableLambda(s.postprocessAnswer)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("persist_session", compose.InvokableLambda(s.persistSession)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("build_response", compose.InvokableLambda(s.buildResponse)); err != nil {
		return nil, err
	}
	edges := [][2]string{
		{compose.START, "normalize"},
		{"normalize", "prepare_session"},
		{"prepare_session", "classify_request"},
		{"direct_answer", "postprocess_answer"},
		{"adk_chat_model_agent", "postprocess_answer"},
		{"postprocess_answer", "persist_session"},
		{"persist_session", "build_response"},
		{"build_response", compose.END},
	}
	for _, edge := range edges {
		if err := g.AddEdge(edge[0], edge[1]); err != nil {
			return nil, err
		}
	}
	if err := g.AddBranch("classify_request", compose.NewGraphBranch(func(ctx context.Context, state workState) (string, error) {
		if strings.TrimSpace(state.DirectAnswer) != "" {
			return "direct_answer", nil
		}
		return "adk_chat_model_agent", nil
	}, map[string]bool{"direct_answer": true, "adk_chat_model_agent": true})); err != nil {
		return nil, err
	}
	return g.Compile(ctx, compose.WithGraphName("kkg_agent_graph"), compose.WithMaxRunSteps(20))
}

func (s *Service) normalize(ctx context.Context, req RunRequest) (workState, error) {
	start := time.Now()
	if strings.EqualFold(strings.TrimSpace(req.ApprovalAction), approvalReplyApprove) || strings.EqualFold(strings.TrimSpace(req.ApprovalAction), approvalReplyReject) {
		item, ok := s.approvalStore.consume(req.UserID, req.SessionID, strings.TrimSpace(req.ApprovalID))
		if !ok {
			return workState{}, fmt.Errorf("approval request expired or invalid")
		}
		req.QuestionID = item.QuestionID
		req.Language = item.Language
		req.Code = item.Code
		req.Submit = strings.EqualFold(strings.TrimSpace(req.ApprovalAction), approvalReplyApprove)
		if req.Submit {
			req.Query = fmt.Sprintf("确认提交题目 %d 的代码", req.QuestionID)
		} else {
			req.Query = fmt.Sprintf("取消提交题目 %d 的代码", req.QuestionID)
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
	state = resolveContextReferences(state)
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
	if containsAny(query, "上面", "上边", "刚才", "之前", "上一", "前面") {
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
		iter := s.runner.Run(ctx, state.Messages,
			adk.WithCheckPointID(state.SessionID),
			adk.WithSessionValues(map[string]any{
				kkgtools.SessionKeyAccessToken:     state.Request.AccessToken,
				kkgtools.SessionKeyUserID:          state.Request.UserID,
				kkgtools.SessionKeyRequestID:       state.Request.RequestID,
				kkgtools.SessionKeySubmitConfirmed: state.SubmitConfirmed,
			}),
		)

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
		if approval := s.approvalFromToolPayload(state, name, payload); approval != nil {
			state.PendingApproval = approval
			state.FinalAnswer = "本次代码提交需要你的确认。请使用下方的确认或取消操作继续。"
			emitStreamEvent(ctx, StreamEvent{
				Type:      "approval_required",
				SessionID: state.SessionID,
				Approval:  approval,
			})
		}
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

func (s *Service) approvalFromToolPayload(state *workState, toolName string, payload *kkgtools.ResultPayload) *ApprovalRequest {
	if state == nil || payload == nil {
		return nil
	}
	if toolName != "kkg_oj_submit_solution" || !toolPayloadNeedsApproval(payload) {
		return nil
	}
	code := strings.TrimSpace(state.Request.Code)
	questionID, language, codeChars, codeLines := approvalDetailsFromToolData(payload.Data, state.Request.QuestionID, state.Request.Language, code)
	item := storedApproval{
		ApprovalRequest: ApprovalRequest{
			ID:          newApprovalID(),
			Action:      approvalActionSubmit,
			Title:       "确认提交代码",
			Message:     fmt.Sprintf("准备提交题目 %d 的 %s 代码。确认后将正式提交到 KKG OJ。", questionID, approvalLanguage(language)),
			SessionID:   state.SessionID,
			QuestionID:  questionID,
			Language:    normalizeApprovalLanguage(language),
			CodeChars:   codeChars,
			CodeLines:   codeLines,
			RequestedAt: time.Now().Format(time.RFC3339),
		},
		UserID:    state.Request.UserID,
		Code:      code,
		CreatedAt: time.Now(),
	}
	approval := s.approvalStore.save(item)
	return &approval
}

func toolPayloadNeedsApproval(payload *kkgtools.ResultPayload) bool {
	if payload == nil {
		return false
	}
	if payload.Error != nil && payload.Error.Type == "approval_required" {
		return true
	}
	record, ok := payload.Data.(map[string]any)
	if !ok {
		return false
	}
	switch value := record["approval_required"].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func approvalDetailsFromToolData(data any, fallbackQuestionID int64, fallbackLanguage, fallbackCode string) (int64, string, int, int) {
	questionID := fallbackQuestionID
	language := normalizeApprovalLanguage(fallbackLanguage)
	codeChars := len([]rune(strings.TrimSpace(fallbackCode)))
	codeLines := countCodeLines(fallbackCode)
	record, ok := data.(map[string]any)
	if !ok {
		return questionID, language, codeChars, codeLines
	}
	if value, ok := int64FromAny(record["question_id"]); ok && value > 0 {
		questionID = value
	}
	if value := strings.TrimSpace(fmt.Sprint(record["language"])); value != "" && value != "<nil>" {
		language = value
	}
	if value, ok := intFromAny(record["code_chars"]); ok && value >= 0 {
		codeChars = value
	}
	if value, ok := intFromAny(record["code_lines"]); ok && value >= 0 {
		codeLines = value
	}
	return questionID, language, codeChars, codeLines
}

func int64FromAny(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	default:
		return 0, false
	}
}

func intFromAny(value any) (int, bool) {
	v, ok := int64FromAny(value)
	if !ok {
		return 0, false
	}
	return int(v), true
}

func compactText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "..."
}

func splitToolsByIntent(ctx context.Context, tools []einotool.BaseTool) ([]einotool.BaseTool, []einotool.BaseTool, error) {
	blogTools := make([]einotool.BaseTool, 0)
	questionTools := make([]einotool.BaseTool, 0)
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil {
			return nil, nil, err
		}
		switch {
		case strings.HasPrefix(info.Name, "kkg_blog_"):
			blogTools = append(blogTools, t)
		case strings.HasPrefix(info.Name, "kkg_oj_"):
			questionTools = append(questionTools, t)
		}
	}
	return blogTools, questionTools, nil
}

func containsAny(text string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(text, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

func normalizeApprovalLanguage(language string) string {
	language = strings.TrimSpace(language)
	if language == "" {
		return "go"
	}
	return language
}

func approvalLanguage(language string) string {
	return strings.ToUpper(normalizeApprovalLanguage(language))
}

func countCodeLines(code string) int {
	code = strings.TrimSpace(code)
	if code == "" {
		return 0
	}
	return strings.Count(code, "\n") + 1
}

func approvalSafeMessages(messages []*schema.Message, finalAnswer string) []*schema.Message {
	out := make([]*schema.Message, 0, 2)
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if msg.Role == schema.User {
			out = append(out, msg)
		}
	}
	answer := strings.TrimSpace(finalAnswer)
	if answer != "" {
		out = append(out, schema.AssistantMessage(answer, nil))
	}
	return out
}

func isSubmitConfirmation(query string) bool {
	compact := strings.ToLower(strings.TrimSpace(query))
	compact = strings.NewReplacer(" ", "", "\n", "", "\t", "", "，", ",", "。", ".", "！", "!", "？", "?").Replace(compact)
	if compact == "" {
		return false
	}
	return containsAny(compact,
		"确认提交",
		"同意提交",
		"可以提交",
		"提交吧",
		"确认submit",
		"confirm_submit",
		"yes_submit",
	)
}

func isSubmitConfirmationReply(query string, history []*schema.Message) bool {
	compact := strings.ToLower(strings.TrimSpace(query))
	compact = strings.NewReplacer(" ", "", "\n", "", "\t", "", "，", ",", "。", ".", "！", "!", "？", "?").Replace(compact)
	if !containsAny(compact, "确认", "可以", "是的", "对", "同意", "yes", "ok") {
		return false
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i] == nil || history[i].Role != schema.Assistant {
			continue
		}
		content := strings.ToLower(extractDisplayContent(history[i]))
		return containsAny(content, "确认提交", "是否确认提交", "确认后", "回复“确认提交”", "回复\"确认提交\"")
	}
	return false
}

var questionIDPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:题目|题号|oj)\s*#?\s*(\d{1,9})`),
	regexp.MustCompile(`(?i)(?:题目|题号|oj)\s*(?:id|编号)\s*[:：#]?\s*(\d{1,9})`),
	regexp.MustCompile(`(?i)(\d{1,9})\s*(?:题|题目)`),
}

var submissionIDPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:submission[_\s-]*id|submit[_\s-]*id)\s*[:=]?\s*(\d{1,9})`),
	regexp.MustCompile(`(?i)(?:提交记录|提交|判题记录)\s*(?:id|编号|#)?\s*[:：]?\s*(\d{1,9})`),
	regexp.MustCompile(`(?i)(\d{1,9})\s*(?:号)?提交(?:记录)?`),
}

var looseNumberPattern = regexp.MustCompile(`\d{1,9}`)

func extractSubmissionID(query string) int64 {
	query = strings.TrimSpace(query)
	if query == "" {
		return 0
	}
	for _, pattern := range submissionIDPatterns {
		match := pattern.FindStringSubmatch(query)
		if len(match) < 2 {
			continue
		}
		id, err := strconv.ParseInt(match[1], 10, 64)
		if err == nil && id > 0 {
			return id
		}
	}
	return 0
}

func extractQuestionID(query string) int64 {
	query = strings.TrimSpace(query)
	if query == "" {
		return 0
	}
	for _, pattern := range questionIDPatterns {
		match := pattern.FindStringSubmatch(query)
		if len(match) < 2 {
			continue
		}
		id, err := strconv.ParseInt(match[1], 10, 64)
		if err == nil && id > 0 {
			return id
		}
	}
	if containsAny(strings.ToLower(query), "题面", "题目详情", "题目内容", "查看题目", "检索题面", "题目信息") {
		raw := looseNumberPattern.FindString(query)
		id, err := strconv.ParseInt(raw, 10, 64)
		if err == nil && id > 0 {
			return id
		}
	}
	return 0
}

func submissionIDStatus(submissionID int64) string {
	if submissionID > 0 {
		return "known"
	}
	return "missing"
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func resolveContextReferences(state workState) workState {
	if !referencesPreviousContext(state.Query) || len(state.History) == 0 {
		return state
	}
	if state.Request.QuestionID <= 0 {
		state.Request.QuestionID = lastQuestionIDFromHistory(state.History)
	}
	if state.Request.SubmissionID <= 0 {
		state.Request.SubmissionID = lastSubmissionIDFromHistory(state.History)
	}
	if strings.TrimSpace(state.Request.Code) == "" {
		state.Request.Code = lastCodeFromHistory(state.History)
	}
	return state
}

func referencesPreviousContext(query string) bool {
	return containsAny(strings.ToLower(query), "上面", "上边", "刚才", "之前", "上一", "前面", "这段代码", "这个代码", "上述代码")
}

func lastQuestionIDFromHistory(history []*schema.Message) int64 {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i] == nil {
			continue
		}
		if id := extractQuestionID(extractDisplayContent(history[i])); id > 0 {
			return id
		}
	}
	return 0
}

func lastSubmissionIDFromHistory(history []*schema.Message) int64 {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i] == nil {
			continue
		}
		if id := extractSubmissionID(extractDisplayContent(history[i])); id > 0 {
			return id
		}
	}
	return 0
}

func lastCodeFromHistory(history []*schema.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i] == nil {
			continue
		}
		if code := extractCodeBlock(extractDisplayContent(history[i])); strings.TrimSpace(code) != "" {
			return code
		}
	}
	return ""
}

var (
	kkgCodePattern    = regexp.MustCompile(`(?is)<kkg-code\b[^>]*>\s*(.*?)\s*</kkg-code>`)
	fencedCodePattern = regexp.MustCompile("(?is)```(?:[a-zA-Z0-9_+.#-]+)?\\s*\\n(.*?)\\n```")
)

func extractCodeBlock(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if match := kkgCodePattern.FindStringSubmatch(text); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	if match := fencedCodePattern.FindStringSubmatch(text); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

func rebuildRuntimeMessages(state *workState) {
	if state == nil {
		return
	}
	messages := make([]*schema.Message, 0, len(state.History)+1)
	messages = append(messages, state.History...)
	messages = append(messages, schema.UserMessage(agentUserPrompt(*state)))
	state.Messages = messages
}

func replaceLastAssistantMessage(messages []*schema.Message, content string) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i] != nil && messages[i].Role == schema.Assistant {
			messages[i].Content = content
			return
		}
	}
}

func normalizeKKGCodeProtocol(answer string) string {
	out := strings.TrimSpace(answer)
	replacements := [][2]string{
		{"```go\n<kkg-code", "<kkg-code"},
		{"```golang\n<kkg-code", "<kkg-code"},
		{"```cpp\n<kkg-code", "<kkg-code"},
		{"```c++\n<kkg-code", "<kkg-code"},
		{"```java\n<kkg-code", "<kkg-code"},
		{"```python\n<kkg-code", "<kkg-code"},
		{"```py\n<kkg-code", "<kkg-code"},
		{"```\n<kkg-code", "<kkg-code"},
		{"</kkg-code>\n```", "</kkg-code>"},
	}
	for _, replacement := range replacements {
		out = strings.ReplaceAll(out, replacement[0], replacement[1])
	}
	out = strings.ReplaceAll(out, "```go<kkg-code", "<kkg-code")
	out = strings.ReplaceAll(out, "```<kkg-code", "<kkg-code")
	out = strings.ReplaceAll(out, "</kkg-code>```", "</kkg-code>")
	return strings.TrimSpace(out)
}
