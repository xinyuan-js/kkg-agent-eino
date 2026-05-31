package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"

	"kkg-agent-eino/apps/api/internal/kkg"
	"kkg-agent-eino/apps/api/internal/rag"
)

type Mode string

const (
	ModeChain Mode = "chain"
	ModeGraph Mode = "graph"
)

type RunRequest struct {
	Query       string `json:"query"`
	Mode        Mode   `json:"mode"`
	QuestionID  int64  `json:"question_id,omitempty"`
	AccessToken string `json:"-"`
	RequestID   string `json:"request_id,omitempty"`
}

type RunResponse struct {
	Mode      Mode           `json:"mode"`
	Answer    string         `json:"answer"`
	RAGDocs   []rag.Document `json:"rag_docs"`
	ToolTrace []ToolTrace    `json:"tool_trace"`
	LatencyMS int64          `json:"latency_ms"`
	RequestID string         `json:"request_id,omitempty"`
}

type ToolTrace struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type Service struct {
	retriever rag.Retriever
	kkg       *kkg.Client
	chain     compose.Runnable[RunRequest, RunResponse]
	graph     compose.Runnable[RunRequest, RunResponse]
}

type workState struct {
	Request    RunRequest
	Query      string
	RAGDocs    []rag.Document
	Question   *kkg.Question
	ToolTrace  []ToolTrace
	StartedAt  time.Time
	Normalized bool
}

func NewService(retriever rag.Retriever, kkgClient *kkg.Client) (*Service, error) {
	s := &Service{retriever: retriever, kkg: kkgClient}

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

func (s *Service) Run(ctx context.Context, req RunRequest) (RunResponse, error) {
	if req.Mode == "" {
		req.Mode = ModeGraph
	}
	switch req.Mode {
	case ModeChain:
		return s.chain.Invoke(ctx, req)
	case ModeGraph:
		return s.graph.Invoke(ctx, req)
	default:
		return RunResponse{}, fmt.Errorf("unsupported mode %q", req.Mode)
	}
}

func (s *Service) compileChain(ctx context.Context) (compose.Runnable[RunRequest, RunResponse], error) {
	c := compose.NewChain[RunRequest, RunResponse]()
	c.AppendLambda(compose.InvokableLambda(s.normalize), compose.WithNodeName("normalize"))
	c.AppendLambda(compose.InvokableLambda(s.retrieve), compose.WithNodeName("rag_retrieve"))
	c.AppendLambda(compose.InvokableLambda(s.callKKGTools), compose.WithNodeName("kkg_tools"))
	c.AppendLambda(compose.InvokableLambda(s.synthesize), compose.WithNodeName("synthesize"))
	return c.Compile(ctx, compose.WithGraphName("kkg_agent_chain"))
}

func (s *Service) compileGraph(ctx context.Context) (compose.Runnable[RunRequest, RunResponse], error) {
	g := compose.NewGraph[RunRequest, RunResponse]()
	if err := g.AddLambdaNode("normalize", compose.InvokableLambda(s.normalize)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("rag_retrieve", compose.InvokableLambda(s.retrieve)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("kkg_tools", compose.InvokableLambda(s.callKKGTools)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("synthesize", compose.InvokableLambda(s.synthesize)); err != nil {
		return nil, err
	}
	edges := [][2]string{
		{compose.START, "normalize"},
		{"normalize", "rag_retrieve"},
		{"rag_retrieve", "kkg_tools"},
		{"kkg_tools", "synthesize"},
		{"synthesize", compose.END},
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

func (s *Service) retrieve(ctx context.Context, state workState) (workState, error) {
	docs, err := s.retriever.Retrieve(ctx, rag.Query{
		Text:      state.Query,
		TopK:      5,
		RequestID: state.Request.RequestID,
	})
	if err != nil {
		return state, err
	}
	state.RAGDocs = docs
	state.ToolTrace = append(state.ToolTrace, ToolTrace{Name: "rag.retrieve", Status: "ok", Message: fmt.Sprintf("%d documents", len(docs))})
	return state, nil
}

func (s *Service) callKKGTools(ctx context.Context, state workState) (workState, error) {
	if state.Request.QuestionID <= 0 {
		state.ToolTrace = append(state.ToolTrace, ToolTrace{Name: "kkg.oj.question.get", Status: "skipped", Message: "question_id is empty"})
		return state, nil
	}
	question, err := s.kkg.GetQuestion(ctx, kkg.ToolContext{AccessToken: state.Request.AccessToken}, state.Request.QuestionID)
	if err != nil {
		state.ToolTrace = append(state.ToolTrace, ToolTrace{Name: "kkg.oj.question.get", Status: "error", Message: err.Error()})
		return state, nil
	}
	state.Question = question
	state.ToolTrace = append(state.ToolTrace, ToolTrace{Name: "kkg.oj.question.get", Status: "ok", Message: question.Title})
	return state, nil
}

func (s *Service) synthesize(_ context.Context, state workState) (RunResponse, error) {
	var b strings.Builder
	b.WriteString("已完成 KKG Agent 编排演示。\n\n")
	if state.Question != nil {
		b.WriteString("题目：")
		b.WriteString(state.Question.Title)
		b.WriteString("\n\n")
	}
	b.WriteString("问题：")
	b.WriteString(state.Query)
	b.WriteString("\n\n")
	b.WriteString("下一步接入真实模型后，这个节点将把 RAG 文档、KKG 工具结果和提示词模板合成为最终答案。")

	mode := state.Request.Mode
	if mode == "" {
		mode = ModeGraph
	}
	return RunResponse{
		Mode:      mode,
		Answer:    b.String(),
		RAGDocs:   state.RAGDocs,
		ToolTrace: state.ToolTrace,
		LatencyMS: time.Since(state.StartedAt).Milliseconds(),
		RequestID: state.Request.RequestID,
	}, nil
}
