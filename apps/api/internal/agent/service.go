package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type Mode string

const (
	ModeChain Mode = "chain"
	ModeGraph Mode = "graph"
)

const (
	routerAgentName   = "kkg_router_agent"
	platformAgentName = "kkg_platform_agent"
	blogAgentName     = "kkg_blog_agent"
	questionAgentName = "kkg_question_agent"
)

type RunRequest struct {
	Query       string `json:"query"`
	Mode        Mode   `json:"mode"`
	QuestionID  int64  `json:"question_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	Language    string `json:"language,omitempty"`
	Code        string `json:"code,omitempty"`
	Input       string `json:"input,omitempty"`
	Submit      bool   `json:"submit,omitempty"`
	UserID      int64  `json:"-"`
	AccessToken string `json:"-"`
	RequestID   string `json:"request_id,omitempty"`
}

type RunResponse struct {
	Mode        Mode           `json:"mode"`
	SessionID   string         `json:"session_id,omitempty"`
	Answer      string         `json:"answer"`
	RAGDocs     []rag.Document `json:"rag_docs"`
	ToolTrace   []ToolTrace    `json:"tool_trace"`
	ToolResults []ToolResult   `json:"tool_results,omitempty"`
	LatencyMS   int64          `json:"latency_ms"`
	RequestID   string         `json:"request_id,omitempty"`
}

type StreamEvent struct {
	Type      string         `json:"type"`
	SessionID string         `json:"session_id,omitempty"`
	Message   string         `json:"message,omitempty"`
	Trace     *ToolTrace     `json:"trace,omitempty"`
	Result    *ToolResult    `json:"result,omitempty"`
	RAGDocs   []rag.Document `json:"rag_docs,omitempty"`
	Done      *RunResponse   `json:"done,omitempty"`
}

type ConversationMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ConversationSession struct {
	ID           string                `json:"id"`
	Title        string                `json:"title"`
	LastMessage  string                `json:"last_message,omitempty"`
	MessageCount int                   `json:"message_count"`
	LastActiveAt string                `json:"last_active_at,omitempty"`
	Archived     bool                  `json:"archived"`
	Messages     []ConversationMessage `json:"messages,omitempty"`
}

type ToolTrace struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type ToolResult struct {
	Name    string                `json:"name"`
	Status  string                `json:"status"`
	Summary string                `json:"summary,omitempty"`
	Data    any                   `json:"data,omitempty"`
	Error   *kkgtools.ResultError `json:"error,omitempty"`
}

type RAGSearchInput struct {
	Query string `json:"query" jsonschema:"required" jsonschema_description:"检索题库知识、相似题或练习题的查询文本"`
	TopK  int    `json:"top_k,omitempty" jsonschema_description:"最大返回数量，默认 8，最大 12"`
}

type Service struct {
	retriever rag.Retriever
	chatModel einomodel.BaseChatModel
	tools     []einotool.BaseTool
	runner    *adk.Runner
	memory    memory.Store
	chain     compose.Runnable[RunRequest, RunResponse]
	graph     compose.Runnable[RunRequest, RunResponse]
}

type workState struct {
	Request        RunRequest
	Query          string
	SessionID      string
	RAGDocs        []rag.Document
	History        []*schema.Message
	Messages       []*schema.Message
	TurnMessages   []*schema.Message
	FinalAnswer    string
	StreamedAnswer bool
	ToolTrace      []ToolTrace
	ToolResults    []ToolResult
	StartedAt      time.Time
	Normalized     bool
}

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
	if emit == nil {
		return runnable.Invoke(ctx, req)
	}
	ctx = context.WithValue(ctx, streamEmitterKey{}, emit)
	return runnable.Invoke(ctx, req)
}

func (s *Service) compileChain(ctx context.Context) (compose.Runnable[RunRequest, RunResponse], error) {
	c := compose.NewChain[RunRequest, RunResponse]()
	c.AppendLambda(compose.InvokableLambda(s.normalize), compose.WithNodeName("normalize"))
	c.AppendLambda(compose.InvokableLambda(s.prepareSession), compose.WithNodeName("prepare_session"))
	c.AppendLambda(compose.InvokableLambda(s.executeRouterAgent), compose.WithNodeName("adk_chat_model_agent"))
	c.AppendLambda(compose.InvokableLambda(s.persistSession), compose.WithNodeName("persist_session"))
	c.AppendLambda(compose.InvokableLambda(s.buildResponse), compose.WithNodeName("build_response"))
	return c.Compile(ctx, compose.WithGraphName("kkg_agent_chain"))
}

func (s *Service) compileGraph(ctx context.Context) (compose.Runnable[RunRequest, RunResponse], error) {
	g := compose.NewGraph[RunRequest, RunResponse]()
	if err := g.AddLambdaNode("normalize", compose.InvokableLambda(s.normalize)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("prepare_session", compose.InvokableLambda(s.prepareSession)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("adk_chat_model_agent", compose.InvokableLambda(s.executeRouterAgent)); err != nil {
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
		{"prepare_session", "adk_chat_model_agent"},
		{"adk_chat_model_agent", "persist_session"},
		{"persist_session", "build_response"},
		{"build_response", compose.END},
	}
	for _, edge := range edges {
		if err := g.AddEdge(edge[0], edge[1]); err != nil {
			return nil, err
		}
	}
	return g.Compile(ctx, compose.WithGraphName("kkg_agent_graph"), compose.WithMaxRunSteps(20))
}

func (s *Service) normalize(_ context.Context, req RunRequest) (workState, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" && req.QuestionID > 0 {
		query = fmt.Sprintf("为 KKG OJ 题目 %d 生成题解与讲解", req.QuestionID)
	}
	if query == "" {
		return workState{}, fmt.Errorf("query or question_id is required")
	}
	return workState{
		Request:    req,
		Query:      query,
		StartedAt:  time.Now(),
		Normalized: true,
	}, nil
}

func (s *Service) prepareSession(ctx context.Context, state workState) (workState, error) {
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
	userPrompt := agentUserPrompt(state)
	messages := make([]*schema.Message, 0, len(history)+1)
	messages = append(messages, history...)
	persistedUserMessage := schema.UserMessage(state.Query)
	runtimeUserMessage := schema.UserMessage(userPrompt)
	messages = append(messages, runtimeUserMessage)
	state.History = history
	state.Messages = messages
	state.TurnMessages = []*schema.Message{persistedUserMessage}
	return state, nil
}

func (s *Service) executeRouterAgent(ctx context.Context, state workState) (workState, error) {
	iter := s.runner.Run(ctx, state.Messages,
		adk.WithCheckPointID(state.SessionID),
		adk.WithSessionValues(map[string]any{
			kkgtools.SessionKeyAccessToken: state.Request.AccessToken,
			kkgtools.SessionKeyUserID:      state.Request.UserID,
			kkgtools.SessionKeyRequestID:   state.Request.RequestID,
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
			state.ToolTrace = append(state.ToolTrace, ToolTrace{Name: "eino.adk.runner", Status: "error", Message: event.Err.Error()})
			return state, event.Err
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		isRootEvent := isRouterAgentEvent(event)
		msg, streamed, err := collectADKMessage(ctx, event.Output.MessageOutput, isRootEvent)
		if err != nil {
			state.ToolTrace = append(state.ToolTrace, ToolTrace{Name: "eino.adk.message", Status: "error", Message: err.Error()})
			continue
		}
		if isRootEvent && streamed {
			state.StreamedAnswer = true
		}
		if msg != nil && isRootEvent {
			state.TurnMessages = append(state.TurnMessages, msg)
		}
		s.recordADKMessage(ctx, &state, msg, event.Output.MessageOutput)
		if isRootEvent && msg != nil && msg.Role == schema.Assistant && strings.TrimSpace(msg.Content) != "" {
			final = msg
		}
	}

	if final == nil || strings.TrimSpace(final.Content) == "" {
		return state, fmt.Errorf("eino adk agent returned no final answer")
	}
	state.FinalAnswer = strings.TrimSpace(final.Content)
	state.ToolTrace = append(state.ToolTrace, ToolTrace{Name: "eino.adk.router_agent", Status: "ok", Message: "ReAct loop completed"})
	return state, nil
}

func (s *Service) persistSession(ctx context.Context, state workState) (workState, error) {
	if err := s.memory.AppendMessages(ctx, state.Request.UserID, state.SessionID, state.TurnMessages...); err != nil {
		return state, err
	}
	return state, nil
}

func (s *Service) buildResponse(ctx context.Context, state workState) (RunResponse, error) {
	mode := state.Request.Mode
	if mode == "" {
		mode = ModeGraph
	}
	out := RunResponse{
		Mode:        mode,
		SessionID:   state.SessionID,
		Answer:      state.FinalAnswer,
		RAGDocs:     state.RAGDocs,
		ToolTrace:   state.ToolTrace,
		ToolResults: state.ToolResults,
		LatencyMS:   time.Since(state.StartedAt).Milliseconds(),
		RequestID:   state.Request.RequestID,
	}
	if !state.StreamedAnswer {
		emitAnswerDeltas(ctx, state.FinalAnswer)
	}
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
		trace := ToolTrace{Name: "eino.adk.model_tool_calls", Status: "ok", Message: strings.Join(names, ", ")}
		state.ToolTrace = append(state.ToolTrace, trace)
		emitStreamEvent(ctx, StreamEvent{Type: "trace", Trace: &trace})
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
			trace := ToolTrace{Name: name, Status: "ok", Message: summary}
			result := ToolResult{Name: name, Status: "ok", Summary: summary, Data: extractDisplayContent(msg)}
			state.ToolTrace = append(state.ToolTrace, trace)
			state.ToolResults = append(state.ToolResults, result)
			emitStreamEvent(ctx, StreamEvent{Type: "tool_result", Trace: &trace, Result: &result})
			return
		}
		payload, err := decodeToolPayload(msg)
		if err != nil {
			if isKKGToolName(name) {
				trace := ToolTrace{Name: name, Status: "error", Message: err.Error()}
				result := ToolResult{Name: name, Status: "error", Summary: err.Error(), Data: extractDisplayContent(msg)}
				state.ToolTrace = append(state.ToolTrace, trace)
				state.ToolResults = append(state.ToolResults, result)
				emitStreamEvent(ctx, StreamEvent{Type: "tool_result", Trace: &trace, Result: &result})
				return
			}
			summary := compactText(extractDisplayContent(msg), 120)
			if summary == "" {
				summary = err.Error()
			}
			trace := ToolTrace{Name: name, Status: "ok", Message: summary}
			result := ToolResult{Name: name, Status: "ok", Summary: summary, Data: extractDisplayContent(msg)}
			state.ToolTrace = append(state.ToolTrace, trace)
			state.ToolResults = append(state.ToolResults, result)
			emitStreamEvent(ctx, StreamEvent{Type: "tool_result", Trace: &trace, Result: &result})
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
		trace := ToolTrace{Name: name, Status: status, Message: summary}
		result := ToolResult{
			Name:    name,
			Status:  status,
			Summary: summary,
			Data:    payload.Data,
			Error:   payload.Error,
		}
		state.ToolTrace = append(state.ToolTrace, trace)
		state.ToolResults = append(state.ToolResults, result)
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

func collectADKMessage(ctx context.Context, variant *adk.MessageVariant, emitAssistantChunks bool) (*schema.Message, bool, error) {
	if variant == nil {
		return nil, false, nil
	}
	if !variant.IsStreaming {
		msg, err := variant.GetMessage()
		return msg, false, err
	}
	if variant.MessageStream == nil {
		return nil, false, fmt.Errorf("streaming message output is missing stream")
	}
	defer variant.MessageStream.Close()

	var chunks []*schema.Message
	streamed := false
	for {
		chunk, err := variant.MessageStream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, streamed, err
		}
		if chunk == nil {
			continue
		}
		chunks = append(chunks, chunk)
		if emitAssistantChunks && variant.Role == schema.Assistant && strings.TrimSpace(chunk.Content) != "" {
			emitStreamEvent(ctx, StreamEvent{Type: "message", Message: chunk.Content})
			streamed = true
		}
	}
	if len(chunks) == 0 {
		return nil, streamed, nil
	}
	msg, err := schema.ConcatMessages(chunks)
	return msg, streamed, err
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

func (s *Service) ListSessions(ctx context.Context, userID int64, archived bool) ([]ConversationSession, error) {
	items, err := s.memory.ListSessions(ctx, userID, 50, archived)
	if err != nil {
		return nil, err
	}
	out := make([]ConversationSession, 0, len(items))
	for _, item := range items {
		out = append(out, ConversationSession{
			ID:           item.ID,
			Title:        item.Title,
			LastMessage:  item.LastMessage,
			MessageCount: item.MessageCount,
			LastActiveAt: item.LastActiveAt,
			Archived:     item.Archived,
		})
	}
	return out, nil
}

func (s *Service) LoadSession(ctx context.Context, userID int64, sessionID string) (ConversationSession, error) {
	history, err := s.memory.LoadSession(ctx, userID, sessionID)
	if err != nil {
		return ConversationSession{}, err
	}
	summary := memory.SessionSummary{ID: sessionID}
	for _, archived := range []bool{false, true} {
		items, err := s.memory.ListSessions(ctx, userID, 200, archived)
		if err != nil {
			continue
		}
		for _, item := range items {
			if item.ID == sessionID {
				summary = item
				break
			}
		}
		if summary.Title != "" || summary.LastMessage != "" || summary.Archived {
			break
		}
	}
	session := ConversationSession{
		ID:           sessionID,
		Title:        summary.Title,
		LastMessage:  summary.LastMessage,
		MessageCount: len(history),
		LastActiveAt: summary.LastActiveAt,
		Archived:     summary.Archived,
		Messages:     make([]ConversationMessage, 0),
	}
	for _, msg := range history {
		if msg == nil {
			continue
		}
		switch msg.Role {
		case schema.User:
			session.Messages = append(session.Messages, ConversationMessage{Role: "user", Content: extractDisplayContent(msg)})
		case schema.Assistant:
			if strings.TrimSpace(msg.Content) == "" {
				continue
			}
			session.Messages = append(session.Messages, ConversationMessage{Role: "assistant", Content: extractDisplayContent(msg)})
		}
	}
	return session, nil
}

func (s *Service) ArchiveSession(ctx context.Context, userID int64, sessionID string, archived bool) error {
	return s.memory.ArchiveSession(ctx, userID, sessionID, archived)
}

func (s *Service) DeleteSession(ctx context.Context, userID int64, sessionID string) error {
	return s.memory.DeleteSession(ctx, userID, sessionID)
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

type streamEmitterKey struct{}

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

func extractDisplayContent(msg *schema.Message) string {
	if msg == nil {
		return ""
	}
	text := strings.TrimSpace(msg.Content)
	if text == "" && len(msg.UserInputMultiContent) > 0 {
		for _, part := range msg.UserInputMultiContent {
			if part.Type == schema.ChatMessagePartTypeText && strings.TrimSpace(part.Text) != "" {
				text = strings.TrimSpace(part.Text)
				break
			}
		}
	}
	if text == "" {
		return ""
	}
	var payload map[string]any
	if json.Unmarshal([]byte(text), &payload) == nil {
		if userQuery, ok := payload["user_query"].(string); ok && strings.TrimSpace(userQuery) != "" {
			return strings.TrimSpace(userQuery)
		}
	}
	return text
}

func sanitizeHistory(history []*schema.Message, limit int) []*schema.Message {
	if len(history) == 0 {
		return nil
	}
	start := 0
	if limit > 0 && len(history) > limit {
		start = len(history) - limit
	}
	window := cloneHistoryMessages(history[start:])
	for len(window) > 0 {
		msg := window[0]
		if msg == nil {
			window = window[1:]
			continue
		}
		if msg.Role != schema.Tool {
			break
		}
		window = window[1:]
	}

	out := make([]*schema.Message, 0, len(window))
	pendingToolCalls := make(map[string]struct{})
	for _, msg := range window {
		if msg == nil {
			continue
		}
		switch msg.Role {
		case schema.Assistant:
			if len(msg.ToolCalls) > 0 {
				for _, call := range msg.ToolCalls {
					if strings.TrimSpace(call.ID) != "" {
						pendingToolCalls[call.ID] = struct{}{}
					}
				}
			}
			out = append(out, msg)
		case schema.Tool:
			if strings.TrimSpace(msg.ToolCallID) == "" {
				continue
			}
			if _, ok := pendingToolCalls[msg.ToolCallID]; !ok {
				continue
			}
			delete(pendingToolCalls, msg.ToolCallID)
			out = append(out, msg)
		default:
			out = append(out, msg)
		}
	}
	return out
}

func cloneHistoryMessages(messages []*schema.Message) []*schema.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]*schema.Message, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		raw, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		var copied schema.Message
		if err := json.Unmarshal(raw, &copied); err != nil {
			continue
		}
		out = append(out, &copied)
	}
	return out
}

func generalAgentInstruction() string {
	return strings.Join([]string{
		"你是 KKG Agent 的通用智能体。",
		"你处理普通问答、平台说明、工程讨论、系统能力说明和非 OJ 题目请求。",
		"你没有工具可用，因此只能基于当前消息和已知上下文直接回答。",
		"回答必须中文、简洁、明确；不知道的内容直接说明，不要虚构题目、博客、提交记录或平台数据。",
	}, "\n")
}

func routerAgentInstruction() string {
	return strings.Join([]string{
		"你是 KKG Agent 的顶层路由智能体。",
		"你必须先判断当前请求是否需要委派给专业子智能体；只有在需要专业领域能力时才调用子智能体工具。",
		"可用能力包括：题库 RAG 检索工具、KKG 平台说明子智能体、KKG 博客知识子智能体、KKG OJ 题目讲解与提交分析子智能体。",
		"当用户请求题目推荐、找题、专项练习、相似题、按知识点寻找练习材料，或需要题库候选材料时，由你调用 kkg_rag_search_questions；不要依赖业务层预先提供 rag_docs。",
		"调用 RAG 工具后，直接基于返回的文档给出推荐或候选说明；只有用户选定具体题目并要求题解、题面详情、代码运行或提交分析时，才委派 KKG OJ 题目子智能体。",
		"调用子智能体工具时，必须严格按工具参数 schema 传参，确保 request 自包含，必要时带上 question_id、code、language、input、submit 等字段。",
		"如果用户只是普通聊天、一般工程问题、泛化解释，直接回答，不要强行调用子智能体。",
		"如果用户的问题同时涉及多个子领域，可以按需要顺序调用多个子智能体，再综合回答。",
		"回答必须中文、简洁、明确；不要编造平台数据、博客内容、题目条件或提交结果。",
	}, "\n")
}

func platformAgentInstruction() string {
	return strings.Join([]string{
		"你是 KKG 平台与项目说明智能体。",
		"你的输入来自父智能体调用工具时传入的结构化参数，其中 request 字段是你必须直接处理的自包含请求。",
		"你负责回答 KKG 平台能力、登录鉴权、接口边界、项目结构、开发容器、部署方式和系统说明相关问题。",
		"你没有实时工具，不要假装读取了线上状态或不存在的数据。",
		"回答必须中文、清晰、工程化，优先说明边界、依赖、前提条件和限制。",
	}, "\n")
}

func blogAgentInstruction() string {
	return strings.Join([]string{
		"你是 KKG 博客与知识材料智能体。",
		"你的输入来自父智能体调用工具时传入的结构化参数，其中 request 字段是你必须直接处理的自包含请求。",
		"你负责查找博客文章、相关文章、评论和知识材料摘要。",
		"优先使用博客工具获取文章、搜索结果和评论，不要编造文章标题、正文或评论。",
		"回答必须中文 Markdown，简洁说明：相关材料、摘要、可继续阅读的文章或缺失信息。",
	}, "\n")
}

func questionAgentInstruction() string {
	return strings.Join([]string{
		"你是 KKG OJ 的题目讲解子智能体，只处理题目讲解、题解、相关博客、代码运行和提交验证。",
		"你的输入来自父智能体调用工具时传入的结构化参数，request 字段描述任务本身；question_id、code、language、input、submit 等字段会按需提供。",
		"题目推荐、找题、专项练习或相似题通常应由父智能体先使用 RAG 工具处理；你不要逐个调用 kkg_oj_get_question 批量拉取详情。",
		"只有用户明确指定某个 question_id 并要求讲解、题面详情、运行或提交时，才调用 kkg_oj_get_question。",
		"如果确实需要浏览题库，kkg_oj_list_questions 最多调用一次用于发现候选题；不要循环翻页或连续查多个题目详情。",
		"用户询问题目做法时，优先根据题目 ID 调用 KKG OJ 工具获取题面；需要相关资料时调用博客或题解工具。",
		"不要编造不存在的题目条件、博客、提交结果或工具返回。",
		"如果用户提供代码并要求运行或提交，只有在已登录且题目 ID 明确时才调用运行或提交工具。",
		"输出中文 Markdown，结构包含：题目理解、知识点、做法讲解、复杂度、相关博客、下一步建议。",
	}, "\n")
}

func agentUserPrompt(state workState) string {
	payload := map[string]any{
		"user_query":  state.Query,
		"question_id": state.Request.QuestionID,
		"language":    state.Request.Language,
		"code":        state.Request.Code,
		"input":       state.Request.Input,
		"submit":      state.Request.Submit,
	}
	raw, _ := json.MarshalIndent(payload, "", "  ")
	return strings.Join([]string{
		"以下是当前会话请求的结构化上下文。",
		"请根据当前智能体的职责回答，不要越权假装具备其他能力。",
		"",
		"```json",
		string(raw),
		"```",
	}, "\n")
}

func platformAgentInputSchema() *schema.ParamsOneOf {
	return schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"request": {
			Desc:     "需要 KKG 平台说明子智能体处理的自包含请求，应该包含用户真正关心的平台/接口/登录/容器/部署问题。",
			Required: true,
			Type:     schema.String,
		},
	})
}

func blogAgentInputSchema() *schema.ParamsOneOf {
	return schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"request": {
			Desc:     "需要 KKG 博客子智能体处理的自包含请求，应该包含要查找的博客主题、文章方向或评论上下文。",
			Required: true,
			Type:     schema.String,
		},
	})
}

func questionAgentInputSchema() *schema.ParamsOneOf {
	return schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"request": {
			Desc:     "需要 KKG OJ 题目子智能体处理的自包含请求，应描述题目讲解、提交结果分析、运行代码或题解查找任务。",
			Required: true,
			Type:     schema.String,
		},
		"question_id": {
			Desc: "可选，OJ 题目 ID。",
			Type: schema.Integer,
		},
		"language": {
			Desc: "可选，代码语言。",
			Type: schema.String,
		},
		"code": {
			Desc: "可选，待运行或待提交的代码。",
			Type: schema.String,
		},
		"input": {
			Desc: "可选，运行代码时的标准输入。",
			Type: schema.String,
		},
		"submit": {
			Desc: "可选，是否偏向正式提交与提交结果分析。",
			Type: schema.Boolean,
		},
	})
}

func compactText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "..."
}

func newSessionID() string {
	return fmt.Sprintf("sess_%d", time.Now().UnixNano())
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
