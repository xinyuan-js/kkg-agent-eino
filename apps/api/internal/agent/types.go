package agent

import (
	"time"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

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
	Query          string `json:"query"`
	Mode           Mode   `json:"mode"`
	QuestionID     int64  `json:"question_id,omitempty"`
	SubmissionID   int64  `json:"submission_id,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	Language       string `json:"language,omitempty"`
	Code           string `json:"code,omitempty"`
	Input          string `json:"input,omitempty"`
	Submit         bool   `json:"submit,omitempty"`
	ApprovalID     string `json:"approval_id,omitempty"`
	ApprovalAction string `json:"approval_action,omitempty"`
	UserID         int64  `json:"-"`
	AccessToken    string `json:"-"`
	RequestID      string `json:"request_id,omitempty"`
}

type RunResponse struct {
	Mode        Mode           `json:"mode"`
	SessionID   string         `json:"session_id,omitempty"`
	Answer      string         `json:"answer"`
	RAGDocs     []rag.Document `json:"rag_docs"`
	ToolTrace   []ToolTrace    `json:"tool_trace"`
	ToolResults []ToolResult   `json:"tool_results,omitempty"`
	TokenUsage  TokenUsage     `json:"token_usage"`
	ModelCalls  int            `json:"model_calls"`
	LatencyMS   int64          `json:"latency_ms"`
	RequestID   string         `json:"request_id,omitempty"`
}

type StreamEvent struct {
	Type      string           `json:"type"`
	SessionID string           `json:"session_id,omitempty"`
	Message   string           `json:"message,omitempty"`
	Trace     *ToolTrace       `json:"trace,omitempty"`
	Result    *ToolResult      `json:"result,omitempty"`
	RAGDocs   []rag.Document   `json:"rag_docs,omitempty"`
	Metrics   *RunMetrics      `json:"metrics,omitempty"`
	Approval  *ApprovalRequest `json:"approval,omitempty"`
	Done      *RunResponse     `json:"done,omitempty"`
}

type ApprovalRequest struct {
	ID          string `json:"id"`
	Action      string `json:"action"`
	Title       string `json:"title"`
	Message     string `json:"message"`
	SessionID   string `json:"session_id,omitempty"`
	QuestionID  int64  `json:"question_id,omitempty"`
	Language    string `json:"language,omitempty"`
	CodeChars   int    `json:"code_chars,omitempty"`
	CodeLines   int    `json:"code_lines,omitempty"`
	RequestedAt string `json:"requested_at,omitempty"`
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
	Kind       string         `json:"kind,omitempty"`
	Name       string         `json:"name"`
	Status     string         `json:"status"`
	Message    string         `json:"message,omitempty"`
	DurationMS int64          `json:"duration_ms,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type ToolResult struct {
	Name    string                `json:"name"`
	Status  string                `json:"status"`
	Summary string                `json:"summary,omitempty"`
	Data    any                   `json:"data,omitempty"`
	Error   *kkgtools.ResultError `json:"error,omitempty"`
}

type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"cached_tokens,omitempty"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
}

type RunMetrics struct {
	TokenUsage TokenUsage `json:"token_usage"`
	ModelCalls int        `json:"model_calls"`
	LatencyMS  int64      `json:"latency_ms"`
}

type RAGSearchInput struct {
	Query string `json:"query" jsonschema:"required" jsonschema_description:"检索题库知识、相似题或练习题的查询文本"`
	TopK  int    `json:"top_k,omitempty" jsonschema_description:"最大返回数量，默认 8，最大 12"`
}

type Service struct {
	retriever     rag.Retriever
	chatModel     einomodel.BaseChatModel
	tools         []einotool.BaseTool
	runner        *adk.Runner
	memory        memory.Store
	chain         compose.Runnable[RunRequest, RunResponse]
	graph         compose.Runnable[RunRequest, RunResponse]
	approvalStore approvalStore
}

type workState struct {
	Request         RunRequest
	Query           string
	SessionID       string
	IntentHints     []string
	ToolPolicy      map[string]any
	SubmitConfirmed bool
	PendingApproval *ApprovalRequest
	RAGDocs         []rag.Document
	History         []*schema.Message
	Messages        []*schema.Message
	TurnMessages    []*schema.Message
	FinalAnswer     string
	DirectAnswer    string
	StreamedAnswer  bool
	ToolTrace       []ToolTrace
	ToolResults     []ToolResult
	TokenUsage      TokenUsage
	ModelCalls      int
	StartedAt       time.Time
	Normalized      bool
}
