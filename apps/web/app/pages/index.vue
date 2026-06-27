<script setup lang="ts">
import {
  Activity,
  Archive,
  BookOpen,
  Braces,
  CheckCircle2,
  CircleSlash,
  PanelLeftClose,
  PanelLeftOpen,
  ChevronDown,
  ChevronRight,
  LogIn,
  LogOut,
  Play,
  RotateCcw,
  ShieldCheck,
  SquarePen,
  Trash2,
  UserCircle,
} from '@lucide/vue'
import type { ApprovalRequest, AuthUser, ChatMessage, ChatSession, RAGDoc, RunMetrics, StreamEvent, ToolTraceItem } from '../types/agent'
import { parseStreamEvents } from '../utils/agent-stream'
import { renderMarkdown } from '../utils/markdown'

const agentApiBase = '/agent-api'
const query = ref('')
const queryPlaceholder = '输入你的问题，例如：171 题怎么做，讲一下思路和知识点'
const running = ref(false)
const error = ref('')
const result = ref<any>(null)
const sessionId = ref('')
const account = ref('')
const password = ref('')
const authLoading = ref(false)
const authError = ref('')
const currentUser = ref<AuthUser | null>(null)
const sidebarCollapsed = ref(false)
const archivedCollapsed = ref(true)
const messages = ref<ChatMessage[]>([])
const sessions = ref<ChatSession[]>([])
const archivedSessions = ref<ChatSession[]>([])
const traceItems = ref<ToolTraceItem[]>([])
const ragDocs = ref<RAGDoc[]>([])
const liveMetrics = ref<RunMetrics | null>(null)
const pendingApproval = ref<ApprovalRequest | null>(null)
const approvalLoading = ref(false)
const conversationRef = ref<HTMLElement | null>(null)
const activeAssistantMessageId = ref('')

const nodes = computed(() => [
  { key: 'normalize', label: '输入规范化', detail: '清洗用户意图、补齐题目上下文', active: Boolean(result.value) || running.value },
  { key: 'rag', label: '知识检索', detail: '召回本地文档和 KKG 知识片段', active: Boolean(ragDocs.value.length) },
  { key: 'tools', label: 'KKG 工具', detail: '按需调用 OJ、博客、题解接口', active: Boolean(toolTraceCount.value) },
  { key: 'synthesis', label: '答案合成', detail: '汇总证据、工具结果与输出', active: Boolean(result.value?.answer) },
])

const currentMetrics = computed<RunMetrics | null>(() => {
  if (result.value) {
    return {
      token_usage: result.value.token_usage || { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 },
      model_calls: result.value.model_calls || 0,
      latency_ms: result.value.latency_ms || 0,
    }
  }
  return liveMetrics.value
})

function traceKind(item: ToolTraceItem) {
  if (item.kind) {
    return item.kind
  }
  if (item.name.startsWith('callback.')) {
    return 'callback'
  }
  if (item.name === 'eino.adk.model_tool_calls') {
    return 'model'
  }
  if (item.name.startsWith('stage.') || item.name.startsWith('eino.adk.')) {
    return 'stage'
  }
  return 'tool'
}

function isVisibleTrace(item: ToolTraceItem) {
  const name = (item.name || '').replace(/^callback\./, '').trim()
  const message = (item.message || '').trim()
  return !(name === 'unknown' && message === 'workState')
}

const visibleTraceItems = computed(() => traceItems.value.filter(isVisibleTrace))
const toolTraceCount = computed(() => visibleTraceItems.value.filter((item) => traceKind(item) === 'tool').length)
const callbackTraceCount = computed(() => visibleTraceItems.value.filter((item) => traceKind(item) === 'callback').length)
const traceGroups = computed(() => {
  const groups = [
    { key: 'stage', title: '流程阶段', items: [] as ToolTraceItem[] },
    { key: 'model', title: '模型决策', items: [] as ToolTraceItem[] },
    { key: 'tool', title: '工具调用', items: [] as ToolTraceItem[] },
    { key: 'callback', title: '底层回调', items: [] as ToolTraceItem[] },
  ]
  const byKind = new Map(groups.map((group) => [group.key, group]))
  for (const item of visibleTraceItems.value) {
    const kind = traceKind(item)
    const group = byKind.get(kind) || byKind.get('stage')
    group?.items.push(item)
  }
  return groups.filter((group) => group.items.length)
})

const summary = computed(() => {
  const metrics = currentMetrics.value
  const usage = metrics?.token_usage
  return [
    { label: 'RAG 引用', value: String(ragDocs.value.length || 0) },
    { label: '工具调用', value: String(toolTraceCount.value || 0) },
    { label: '回调', value: String(callbackTraceCount.value || 0) },
    { label: '模型调用', value: metrics ? String(metrics.model_calls || 0) : '-' },
    { label: '总 token', value: usage ? String(usage.total_tokens || 0) : '-' },
    { label: '输入/输出', value: usage ? `${usage.prompt_tokens || 0}/${usage.completion_tokens || 0}` : '-' },
    { label: '延迟', value: metrics ? `${metrics.latency_ms || 0} ms` : '-' },
  ]
})

const signedIn = computed(() => Boolean(currentUser.value))

function traceTitle(item: ToolTraceItem) {
  const name = item.name.replace(/^callback\./, '')
  const labels: Record<string, string> = {
    'stage.normalize': '输入规范化',
    'stage.prepare_session': '加载会话上下文',
    'eino.adk.model_tool_calls': '模型选择工具',
    'eino.adk.router_agent': '路由智能体完成',
    'stage.persist_session': '保存会话',
    'stage.direct_restatement': '原样复述',
    deepseek_chat_model: 'DeepSeek 模型请求',
    kkg_rag_search_questions: '题库语义检索',
    kkg_question_agent: 'OJ 题目智能体',
    kkg_blog_agent: '博客知识智能体',
    kkg_platform_agent: '平台说明智能体',
  }
  return labels[name] || name
}

function traceDetail(item: ToolTraceItem) {
  const message = item.message || ''
  return message
    .replace(/^model input: /, '输入：')
    .replace(/^model output: /, '输出：')
    .replace(/^history=/, '历史消息：')
    .replace(/^messages=/, '写入消息：')
}

function traceStatus(item: ToolTraceItem) {
  if (item.duration_ms !== undefined) {
    return `${item.duration_ms} ms`
  }
  if (item.status === 'ok') {
    return '完成'
  }
  if (item.status === 'start') {
    return '开始'
  }
  if (item.status === 'error') {
    return '失败'
  }
  return item.status
}

function isUnauthorized(err: any) {
  return err?.statusCode === 401 || err?.response?.status === 401
}

async function refreshSessionSilently() {
  try {
    const data = await $fetch('/auth/refresh', {
      baseURL: agentApiBase,
      method: 'POST',
      credentials: 'include',
    })
    currentUser.value = (data as any).data?.user || null
    return true
  } catch {
    currentUser.value = null
    return false
  }
}

async function apiFetch(path: string, options: Record<string, any> = {}) {
  const { authRetry = true, ...fetchOptions } = options
  try {
    return await $fetch(path, {
      baseURL: agentApiBase,
      credentials: 'include',
      ...fetchOptions,
    })
  } catch (err: any) {
    if (!authRetry || !isUnauthorized(err)) {
      throw err
    }
    const refreshed = await refreshSessionSilently()
    if (!refreshed) {
      throw err
    }
    return await $fetch(path, {
      baseURL: agentApiBase,
      credentials: 'include',
      ...fetchOptions,
    })
  }
}

async function loadMe() {
  try {
    const data = await apiFetch('/auth/me')
    currentUser.value = (data as any).data
    await loadSessions()
  } catch {
    currentUser.value = null
    sessions.value = []
    archivedSessions.value = []
  }
}

async function login() {
  authLoading.value = true
  authError.value = ''
  try {
    const data = await apiFetch('/auth/login', {
      method: 'POST',
      authRetry: false,
      body: {
        account: account.value,
        password: password.value,
      },
    })
    currentUser.value = (data as any).data?.user || null
    password.value = ''
    await loadSessions()
  } catch (err: any) {
    authError.value = err?.data?.message || err?.message || 'login failed'
  } finally {
    authLoading.value = false
  }
}

async function logout() {
  authLoading.value = true
  authError.value = ''
  try {
    await apiFetch('/auth/logout', {
      method: 'POST',
      authRetry: false,
    })
    currentUser.value = null
    sessions.value = []
    archivedSessions.value = []
    startNewChat()
  } catch (err: any) {
    authError.value = err?.data?.message || err?.message || 'logout failed'
  } finally {
    authLoading.value = false
  }
}

function startNewChat() {
  query.value = ''
  error.value = ''
  result.value = null
  sessionId.value = ''
  messages.value = []
  traceItems.value = []
  ragDocs.value = []
  liveMetrics.value = null
  pendingApproval.value = null
  activeAssistantMessageId.value = ''
}

async function loadSessions() {
  if (!signedIn.value) {
    sessions.value = []
    archivedSessions.value = []
    return
  }
  const [activeData, archivedData] = await Promise.all([
    apiFetch('/agent/sessions'),
    apiFetch('/agent/sessions?archived=true'),
  ])
  sessions.value = (activeData as any).data || []
  archivedSessions.value = (archivedData as any).data || []
}

async function openSession(id: string) {
  if (!id) {
    return
  }
  const data = await apiFetch(`/agent/sessions/${id}`)
  const session = (data as any).data
  sessionId.value = session.id
  messages.value = (session.messages || []).map((item: any, index: number) => ({
    id: `${session.id}-${index}`,
    role: item.role,
    content: item.content,
  }))
  traceItems.value = []
  ragDocs.value = []
  liveMetrics.value = null
  pendingApproval.value = null
  result.value = null
  await nextTick()
  scrollConversationToBottom()
}

async function archiveSession(item: ChatSession, archived: boolean) {
  await apiFetch(`/agent/sessions/${item.id}/archive`, {
    method: 'POST',
    body: { archived },
  })
  if (item.id === sessionId.value && archived) {
    startNewChat()
  }
  await loadSessions()
}

async function deleteSession(item: ChatSession) {
  await apiFetch(`/agent/sessions/${item.id}`, {
    method: 'DELETE',
  })
  if (item.id === sessionId.value) {
    startNewChat()
  }
  await loadSessions()
}

async function runAgent() {
  if (!signedIn.value) {
    error.value = '请先登录后再开始对话'
    return
  }
  running.value = true
  error.value = ''
  const prompt = query.value.trim()
  if (!prompt) {
    error.value = '请输入问题'
    running.value = false
    return
  }
  const userMessage: ChatMessage = {
    id: `${Date.now()}-user`,
    role: 'user',
    content: prompt,
  }
  messages.value.push(userMessage)
  const assistantMessageId = `${Date.now()}-assistant`
  activeAssistantMessageId.value = assistantMessageId
  messages.value.push({
    id: assistantMessageId,
    role: 'assistant',
    content: '',
  })
  query.value = ''
  traceItems.value = []
  ragDocs.value = []
  liveMetrics.value = null
  pendingApproval.value = null
  result.value = null
  await nextTick()
  scrollConversationToBottom()
  try {
    await runAgentStream({
      mode: 'graph',
      query: prompt,
      session_id: sessionId.value || undefined,
    }, assistantMessageId)
    await loadSessions()
  } catch (err: any) {
    error.value = err?.data?.message || err?.message || 'request failed'
    updateAssistantMessage(assistantMessageId, `请求失败：${error.value}`)
    await nextTick()
    scrollConversationToBottom()
  } finally {
    running.value = false
  }
}

async function runApprovalAction(action: 'approve' | 'reject') {
  if (!pendingApproval.value || approvalLoading.value || running.value) {
    return
  }
  approvalLoading.value = true
  running.value = true
  error.value = ''
  const approval = pendingApproval.value
  const userText = action === 'approve' ? '确认提交' : '取消提交'
  const userMessage: ChatMessage = {
    id: `${Date.now()}-user`,
    role: 'user',
    content: userText,
  }
  messages.value.push(userMessage)
  const assistantMessageId = `${Date.now()}-assistant`
  activeAssistantMessageId.value = assistantMessageId
  messages.value.push({
    id: assistantMessageId,
    role: 'assistant',
    content: '',
  })
  traceItems.value = []
  ragDocs.value = []
  liveMetrics.value = null
  result.value = null
  pendingApproval.value = null
  await nextTick()
  scrollConversationToBottom()
  try {
    await runAgentStream({
      mode: 'graph',
      session_id: approval.session_id || sessionId.value || undefined,
      approval_id: approval.id,
      approval_action: action,
    }, assistantMessageId)
    await loadSessions()
  } catch (err: any) {
    error.value = err?.data?.message || err?.message || 'request failed'
    updateAssistantMessage(assistantMessageId, `请求失败：${error.value}`)
    pendingApproval.value = approval
    await nextTick()
    scrollConversationToBottom()
  } finally {
    approvalLoading.value = false
    running.value = false
  }
}

async function runAgentStream(requestBodyValue: Record<string, any>, assistantMessageId: string) {
  const controller = new AbortController()
  let idleTimer: ReturnType<typeof setTimeout> | null = null
  let timedOut = false
  const resetIdleTimer = () => {
    if (idleTimer) {
      clearTimeout(idleTimer)
    }
    idleTimer = setTimeout(() => {
      timedOut = true
      controller.abort()
    }, 90000)
  }

  try {
    resetIdleTimer()
    const requestBody = JSON.stringify(requestBodyValue)
    const requestStream = () => fetch(`${agentApiBase}/agent/stream`, {
      method: 'POST',
      credentials: 'include',
      signal: controller.signal,
      headers: {
        'Content-Type': 'application/json',
      },
      body: requestBody,
    })

    let response = await requestStream()
    if (response.status === 401) {
      const refreshed = await refreshSessionSilently()
      if (refreshed) {
        response = await requestStream()
      }
    }
    if (!response.ok || !response.body) {
      let message = `request failed: ${response.status}`
      try {
        const data = await response.json()
        message = data?.message || message
      } catch {}
      throw new Error(message)
    }

    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    while (true) {
      const { value, done } = await reader.read()
      if (done) {
        break
      }
      resetIdleTimer()
      buffer += decoder.decode(value, { stream: true })
      const parsed = parseStreamEvents(buffer)
      buffer = parsed.rest
      for (const event of parsed.events) {
        await applyStreamEvent(event, assistantMessageId)
      }
    }
    buffer += decoder.decode()
    const parsed = parseStreamEvents(buffer, true)
    for (const event of parsed.events) {
      await applyStreamEvent(event, assistantMessageId)
    }
    if (parsed.rest.trim()) {
      throw new Error(`invalid stream payload: ${parsed.rest.trim().slice(0, 120)}`)
    }
  } catch (err: any) {
    if (timedOut) {
      throw new Error('请求超时：90 秒内没有收到新的流式响应')
    }
    throw err
  } finally {
    if (idleTimer) {
      clearTimeout(idleTimer)
    }
  }
}

async function applyStreamEvent(event: StreamEvent, assistantMessageId: string) {
  if (event.session_id) {
    sessionId.value = event.session_id
  }
  if (event.type === 'trace' && event.trace) {
    traceItems.value = [...traceItems.value, event.trace]
  }
  if (event.rag_docs?.length) {
    ragDocs.value = event.rag_docs
  }
  if (event.metrics) {
    liveMetrics.value = event.metrics
  }
  if (event.type === 'approval_required' && event.approval) {
    pendingApproval.value = event.approval
  }
  if (event.type === 'error') {
    updateAssistantMessage(assistantMessageId, `请求失败：${event.message || 'unknown error'}`)
  }
  if (event.type === 'message' && event.message) {
    appendAssistantMessage(assistantMessageId, event.message)
    await nextTick()
    scrollConversationToBottom()
  }
  if (event.done) {
    result.value = event.done
    liveMetrics.value = {
      token_usage: event.done.token_usage || { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 },
      model_calls: event.done.model_calls || 0,
      latency_ms: event.done.latency_ms || 0,
    }
    reconcileAssistantMessage(assistantMessageId, String(event.done.answer || ''))
    await nextTick()
    scrollConversationToBottom()
  }
}

function updateAssistantMessage(id: string, content: string) {
  const item = messages.value.find((message) => message.id === id)
  if (!item) {
    return
  }
  item.content = content
}

function appendAssistantMessage(id: string, delta: string) {
  const item = messages.value.find((message) => message.id === id)
  if (!item || !delta) {
    return
  }
  item.content += delta
}

function reconcileAssistantMessage(id: string, finalContent: string) {
  const item = messages.value.find((message) => message.id === id)
  if (!item || !finalContent) {
    return
  }
  if (!item.content || item.content === finalContent) {
    item.content = finalContent
    return
  }
  if (finalContent.startsWith(item.content)) {
    item.content += finalContent.slice(item.content.length)
    return
  }
  item.content = finalContent
}

function scrollConversationToBottom() {
  const el = conversationRef.value
  if (!el) {
    return
  }
  el.scrollTop = el.scrollHeight
}

watch(sidebarCollapsed, async () => {
  await nextTick()
  scrollConversationToBottom()
})

onMounted(async () => {
  await loadMe()
  await nextTick()
  scrollConversationToBottom()
})
</script>

<template>
  <main class="shell">
    <section class="app-frame" :class="{ 'sidebar-collapsed': sidebarCollapsed }" aria-label="KKG Agent Console">
      <aside class="app-sidebar">
        <div class="sidebar-top">
          <strong class="sidebar-logo">KKG Agent</strong>
          <button class="sidebar-toggle" type="button" :aria-expanded="!sidebarCollapsed" @click="sidebarCollapsed = !sidebarCollapsed">
            <PanelLeftOpen v-if="sidebarCollapsed" :size="18" />
            <PanelLeftClose v-else :size="18" />
          </button>
        </div>

        <div class="sidebar-content">
          <nav class="sidebar-nav" aria-label="primary navigation">
            <button class="nav-item selected" type="button" @click="startNewChat">
              <SquarePen :size="19" />
              <span>新建问题</span>
            </button>
          </nav>

          <section class="sidebar-section rag-list">
            <h2>历史会话</h2>
            <div v-if="sessions.length" class="session-list">
              <article v-for="item in sessions" :key="item.id" class="session-item" :class="{ active: item.id === sessionId }">
                <button class="session-main" type="button" @click="openSession(item.id)">
                  <strong>{{ item.title }}</strong>
                  <small>{{ item.last_message || '暂无摘要' }}</small>
                </button>
                <div class="session-actions">
                  <button type="button" class="session-action" title="归档" @click.stop="archiveSession(item, true)">
                    <Archive :size="14" />
                  </button>
                  <button type="button" class="session-action danger" title="删除" @click.stop="deleteSession(item)">
                    <Trash2 :size="14" />
                  </button>
                </div>
              </article>
            </div>
            <div v-else class="sidebar-empty">暂无历史会话</div>
          </section>

          <section class="sidebar-section rag-list">
            <button class="sidebar-section-toggle" type="button" :aria-expanded="!archivedCollapsed" @click="archivedCollapsed = !archivedCollapsed">
              <span>已归档</span>
              <component :is="archivedCollapsed ? ChevronRight : ChevronDown" :size="14" />
            </button>
            <div v-if="!archivedCollapsed && archivedSessions.length" class="session-list">
              <article v-for="item in archivedSessions" :key="item.id" class="session-item archived">
                <button class="session-main" type="button" @click="openSession(item.id)">
                  <strong>{{ item.title }}</strong>
                  <small>{{ item.last_message || '暂无摘要' }}</small>
                </button>
                <div class="session-actions">
                  <button type="button" class="session-action" title="恢复" @click.stop="archiveSession(item, false)">
                    <RotateCcw :size="14" />
                  </button>
                  <button type="button" class="session-action danger" title="删除" @click.stop="deleteSession(item)">
                    <Trash2 :size="14" />
                  </button>
                </div>
              </article>
            </div>
            <div v-else-if="!archivedCollapsed" class="sidebar-empty">暂无归档会话</div>
          </section>

          <section class="sidebar-section" aria-label="run status">
            <h2>运行状态</h2>
            <div class="sidebar-metrics">
              <span v-for="item in summary" :key="item.label">
                <small>{{ item.label }}</small>
                <strong>{{ item.value }}</strong>
              </span>
            </div>
          </section>

          <section class="sidebar-section rag-list">
            <h2>RAG 引用</h2>
            <div v-if="ragDocs.length" class="rag-compact-list">
              <article v-for="doc in ragDocs" :key="doc.id" class="rag-compact-item">
                <span><BookOpen :size="14" /> {{ doc.source }}</span>
                <strong>{{ doc.title }}</strong>
              </article>
            </div>
            <div v-else class="sidebar-empty">暂无引用</div>
          </section>
        </div>

        <section class="sidebar-account" aria-label="KKG login">
          <form v-if="!signedIn" class="auth-form" @submit.prevent="login">
            <label>
              <UserCircle :size="16" />
              <input v-model="account" autocomplete="username" placeholder="用户名或邮箱" />
            </label>
            <label>
              <ShieldCheck :size="16" />
              <input v-model="password" autocomplete="current-password" type="password" placeholder="密码" />
            </label>
            <button class="auth-button" :disabled="authLoading">
              <LogIn :size="16" />
              {{ authLoading ? '登录中' : '登录' }}
            </button>
            <p v-if="authError" class="auth-error">{{ authError }}</p>
          </form>

          <div v-else class="auth-session">
            <div class="account-avatar">{{ currentUser?.username?.slice(0, 1) || 'K' }}</div>
            <div>
              <strong>{{ currentUser?.username }}</strong>
              <span>{{ currentUser?.email }}</span>
            </div>
            <button class="ghost-button" :disabled="authLoading" @click="logout">
              <LogOut :size="16" />
            </button>
          </div>
        </section>
      </aside>

      <section class="workspace">
        <div class="chat-layout">
          <aside class="chat-sidebar">
            <section class="panel trace-panel">
              <div class="panel-head compact">
                <div>
                  <span class="section-kicker">Trace</span>
                  <h2>运行轨迹</h2>
                </div>
                <Braces :size="20" />
              </div>
              <div v-if="traceGroups.length" class="trace">
                <section v-for="group in traceGroups" :key="group.key" class="trace-group">
                  <header>
                    <strong>{{ group.title }}</strong>
                    <small>{{ group.items.length }}</small>
                  </header>
                  <div v-for="(item, index) in group.items" :key="group.key + item.name + item.status + index" class="trace-row">
                    <span>{{ traceTitle(item) }}</span>
                    <strong :class="item.status">{{ traceStatus(item) }}</strong>
                    <small v-if="traceDetail(item)">{{ traceDetail(item) }}</small>
                  </div>
                </section>
              </div>
              <div v-else class="empty-state small">暂无运行轨迹</div>
            </section>
          </aside>

          <section class="panel chat-main" aria-label="agent conversation">
            <section class="orchestration-bar" aria-label="runtime progress">
              <div class="linear-flow">
                <div v-for="(node, index) in nodes" :key="node.key" class="linear-step" :class="{ active: node.active }">
                  <span class="step-dot">
                    <CheckCircle2 v-if="node.active" :size="13" />
                    <template v-else>{{ index + 1 }}</template>
                  </span>
                  <span>{{ node.label }}</span>
                </div>
              </div>
            </section>

            <div ref="conversationRef" class="conversation">
              <template v-if="messages.length">
                <div v-for="message in messages" :key="message.id" class="message-row" :class="message.role === 'user' ? 'user-message' : 'assistant-message'">
                  <div class="message-avatar">{{ message.role === 'user' ? 'U' : 'A' }}</div>
                  <div class="message-body">
                    <div class="message-meta">{{ message.role === 'user' ? currentUser?.username || '用户' : 'KKG Agent' }}</div>
                    <p v-if="message.role === 'user'">{{ message.content }}</p>
                    <div v-else-if="message.content" class="markdown-answer" v-html="renderMarkdown(message.content)" />
                    <div v-else class="empty-state inline-loading">
                      <Activity :size="18" />
                      <span>正在生成答案</span>
                    </div>
                  </div>
                </div>
              </template>
              <div v-else class="empty-state conversation-empty">
                <Activity :size="20" />
                <span>{{ signedIn ? '开始新的对话' : '请先登录后开始对话' }}</span>
              </div>
            </div>

            <section class="chat-composer">
              <div v-if="pendingApproval" class="approval-card" aria-live="polite">
                <div class="approval-copy">
                  <span class="section-kicker">授权确认</span>
                  <strong>{{ pendingApproval.title }}</strong>
                  <p>{{ pendingApproval.message }}</p>
                  <small>
                    题目 {{ pendingApproval.question_id || '-' }} · {{ pendingApproval.language || 'go' }} · {{ pendingApproval.code_lines || 0 }} 行 / {{ pendingApproval.code_chars || 0 }} 字符
                  </small>
                </div>
                <div class="approval-actions">
                  <button class="ghost-button" type="button" :disabled="approvalLoading || running" @click="runApprovalAction('reject')">
                    <CircleSlash :size="16" />
                    取消
                  </button>
                  <button class="auth-button approval-confirm" type="button" :disabled="approvalLoading || running" @click="runApprovalAction('approve')">
                    <ShieldCheck :size="16" />
                    {{ approvalLoading || running ? '处理中' : '确认提交' }}
                  </button>
                </div>
              </div>

              <div class="prompt-box">
                <textarea id="query" v-model="query" rows="4" :placeholder="signedIn ? queryPlaceholder : '请先登录后开始对话'" :disabled="!signedIn || running" />
                <button class="run-button send-button" :disabled="running || !signedIn" @click="runAgent">
                  <Play :size="17" />
                  {{ running ? '运行中' : '发送' }}
                </button>
              </div>

              <p v-if="error" class="form-error">{{ error }}</p>
            </section>
          </section>
        </div>
      </section>
    </section>
  </main>
</template>
