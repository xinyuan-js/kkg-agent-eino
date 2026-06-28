export type AuthUser = {
  id: number
  username: string
  email: string
  avatar_url?: string
  role: string
}

export type ChatMessage = {
  id: string
  role: 'user' | 'assistant'
  content: string
}

export type ChatSession = {
  id: string
  title: string
  last_message?: string
  message_count: number
  last_active_at?: string
  archived?: boolean
}

export type ToolTraceItem = {
  kind?: 'stage' | 'callback' | 'model' | 'tool' | string
  seq?: number
  timestamp?: string
  elapsed_ms?: number
  name: string
  status: string
  message?: string
  duration_ms?: number
  metadata?: Record<string, any>
}

export type RAGDoc = {
  id: string
  source: string
  title: string
}

export type TokenUsage = {
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  cached_tokens?: number
  reasoning_tokens?: number
}

export type RunMetrics = {
  token_usage: TokenUsage
  model_calls: number
  latency_ms: number
}

export type ApprovalRequest = {
  id: string
  action: string
  title: string
  message: string
  session_id?: string
  question_id?: number
  language?: string
  code_chars?: number
  code_lines?: number
  requested_at?: string
}

export type StreamEvent = {
  type: string
  session_id?: string
  message?: string
  trace?: ToolTraceItem
  result?: any
  rag_docs?: RAGDoc[]
  metrics?: RunMetrics
  approval?: ApprovalRequest
  done?: any
}
