<script setup lang="ts">
import {
  Activity,
  Archive,
  BookOpen,
  Braces,
  CheckCircle2,
  PanelLeftClose,
  PanelLeftOpen,
  ChevronDown,
  ChevronRight,
  GitBranch,
  LogIn,
  LogOut,
  Link2,
  Play,
  RotateCcw,
  ShieldCheck,
  SquarePen,
  Trash2,
  UserCircle,
  Workflow,
} from '@lucide/vue'

type Mode = 'graph' | 'chain'
type AuthUser = {
  id: number
  username: string
  email: string
  avatar_url?: string
  role: string
}

type ChatMessage = {
  id: string
  role: 'user' | 'assistant'
  content: string
  questionId?: number | null
}

type ChatSession = {
  id: string
  title: string
  last_message?: string
  message_count: number
  last_active_at?: string
  archived?: boolean
}

type ToolTraceItem = {
  name: string
  status: string
  message?: string
}

type RAGDoc = {
  id: string
  source: string
  title: string
}

type StreamEvent = {
  type: string
  session_id?: string
  message?: string
  trace?: ToolTraceItem
  result?: any
  rag_docs?: RAGDoc[]
  done?: any
}

const agentApiBase = '/agent-api'
const mode = ref<Mode>('graph')
const query = ref('')
const queryPlaceholder = '输入题目 ID 或问题，例如：这道题怎么做，讲一下思路和知识点'
const questionId = ref<number | null>(null)
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
const conversationRef = ref<HTMLElement | null>(null)
const activeAssistantMessageId = ref('')

const nodes = computed(() => [
  { key: 'normalize', label: '输入规范化', detail: '清洗用户意图、补齐题目上下文', active: Boolean(result.value) || running.value },
  { key: 'rag', label: '知识检索', detail: '召回本地文档和 KKG 知识片段', active: Boolean(ragDocs.value.length) },
  { key: 'tools', label: 'KKG 工具', detail: '按需调用 OJ、博客、题解接口', active: Boolean(traceItems.value.length) },
  { key: 'synthesis', label: '答案合成', detail: '汇总证据、工具结果与输出', active: Boolean(result.value?.answer) },
])

const modeInfo = computed(() => {
  if (mode.value === 'chain') {
    return { title: 'Chain', detail: '线性流程', icon: GitBranch }
  }
  return { title: 'Graph', detail: '节点编排', icon: Workflow }
})

const summary = computed(() => [
  { label: '编排模式', value: modeInfo.value.title },
  { label: 'RAG 引用', value: String(ragDocs.value.length || 0) },
  { label: '工具调用', value: String(traceItems.value.length || 0) },
  { label: '延迟', value: result.value?.latency_ms !== undefined ? `${result.value.latency_ms} ms` : '-' },
])

const signedIn = computed(() => Boolean(currentUser.value))
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
  questionId.value = null
  error.value = ''
  result.value = null
  sessionId.value = ''
  messages.value = []
  traceItems.value = []
  ragDocs.value = []
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
    questionId: questionId.value,
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
  result.value = null
  await nextTick()
  scrollConversationToBottom()
  try {
    await runAgentStream(prompt, assistantMessageId)
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

async function runAgentStream(prompt: string, assistantMessageId: string) {
  const response = await fetch(`${agentApiBase}/agent/stream`, {
    method: 'POST',
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      mode: mode.value,
      query: prompt,
      question_id: questionId.value || undefined,
      session_id: sessionId.value || undefined,
    }),
  })
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
    buffer += decoder.decode(value, { stream: true })
    const lines = buffer.split('\n')
    buffer = lines.pop() || ''
    for (const line of lines) {
      const trimmed = line.trim()
      if (!trimmed) {
        continue
      }
      const event = JSON.parse(trimmed) as StreamEvent
      applyStreamEvent(event, assistantMessageId)
    }
  }
}

function applyStreamEvent(event: StreamEvent, assistantMessageId: string) {
  if (event.session_id) {
    sessionId.value = event.session_id
  }
  if (event.trace) {
    traceItems.value = [...traceItems.value, event.trace]
  }
  if (event.rag_docs?.length) {
    ragDocs.value = event.rag_docs
  }
  if (event.type === 'error') {
    updateAssistantMessage(assistantMessageId, `请求失败：${event.message || 'unknown error'}`)
  }
  if (event.done) {
    result.value = event.done
    updateAssistantMessage(assistantMessageId, String(event.done.answer || ''))
  }
}

function updateAssistantMessage(id: string, content: string) {
  const item = messages.value.find((message) => message.id === id)
  if (!item) {
    return
  }
  item.content = content
}

function scrollConversationToBottom() {
  const el = conversationRef.value
  if (!el) {
    return
  }
  el.scrollTop = el.scrollHeight
}

function escapeHtml(value: string) {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;')
}

function safeHref(value: string) {
  const href = value.replaceAll('&amp;', '&').trim()
  if (href.startsWith('/') || href.startsWith('http://') || href.startsWith('https://')) {
    return escapeHtml(href)
  }
  return ''
}

function renderInline(value: string) {
  return escapeHtml(value)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/\[([^\]]+)]\(([^)\s]+)\)/g, (_match, label, href) => {
      const safe = safeHref(String(href))
      if (!safe) {
        return label
      }
      return `<a href="${safe}" target="_blank" rel="noreferrer">${label}</a>`
    })
}

function renderMarkdown(markdown: string) {
  const lines = markdown.replace(/\r\n/g, '\n').split('\n')
  const html: string[] = []
  let paragraph: string[] = []
  let listOpen = false
  let codeOpen = false
  let codeLines: string[] = []

  const closeParagraph = () => {
    if (!paragraph.length) {
      return
    }
    html.push(`<p>${paragraph.map(renderInline).join('<br>')}</p>`)
    paragraph = []
  }

  const closeList = () => {
    if (!listOpen) {
      return
    }
    html.push('</ul>')
    listOpen = false
  }

  const closeCode = () => {
    html.push(`<pre><code>${escapeHtml(codeLines.join('\n'))}</code></pre>`)
    codeLines = []
    codeOpen = false
  }

  for (const rawLine of lines) {
    const line = rawLine.trimEnd()
    const trimmed = line.trim()

    if (trimmed.startsWith('```')) {
      if (codeOpen) {
        closeCode()
      } else {
        closeParagraph()
        closeList()
        codeOpen = true
        codeLines = []
      }
      continue
    }

    if (codeOpen) {
      codeLines.push(line)
      continue
    }

    if (!trimmed) {
      closeParagraph()
      closeList()
      continue
    }

    const heading = trimmed.match(/^(#{1,4})\s+(.+)$/)
    if (heading) {
      closeParagraph()
      closeList()
      const level = heading[1].length
      html.push(`<h${level}>${renderInline(heading[2])}</h${level}>`)
      continue
    }

    const listItem = trimmed.match(/^[-*]\s+(.+)$/)
    if (listItem) {
      closeParagraph()
      if (!listOpen) {
        html.push('<ul>')
        listOpen = true
      }
      html.push(`<li>${renderInline(listItem[1])}</li>`)
      continue
    }

    paragraph.push(trimmed)
  }

  if (codeOpen) {
    closeCode()
  }
  closeParagraph()
  closeList()
  return html.join('')
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
              <h2>工具轨迹</h2>
            </div>
            <Braces :size="20" />
          </div>
          <div v-if="traceItems.length" class="trace">
            <div v-for="(item, index) in traceItems" :key="item.name + item.status + index" class="trace-row">
              <span>{{ item.name }}</span>
              <strong :class="item.status">{{ item.status }}</strong>
              <small>{{ item.message || '-' }}</small>
            </div>
          </div>
          <div v-else class="empty-state small">暂无工具调用</div>
        </section>
        </aside>

        <section class="panel chat-main" aria-label="agent conversation">
          <section class="orchestration-bar" aria-label="runtime progress">
            <div class="mode-pill">
              <component :is="modeInfo.icon" :size="16" />
              <strong>{{ modeInfo.title }}</strong>
              <span>{{ modeInfo.detail }}</span>
            </div>
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
                  <small v-if="message.role === 'user' && message.questionId">OJ ID: {{ message.questionId }}</small>
                </div>
              </div>
            </template>
            <div v-else class="empty-state conversation-empty">
              <Activity :size="20" />
              <span>{{ signedIn ? '开始新的对话' : '请先登录后开始对话' }}</span>
            </div>
          </div>

          <section class="chat-composer">
            <div class="composer-toolbar">
              <div class="mode-switch" aria-label="orchestration mode">
                <button :class="{ selected: mode === 'graph' }" @click="mode = 'graph'">
                  <Workflow :size="15" />
                  Graph
                </button>
                <button :class="{ selected: mode === 'chain' }" @click="mode = 'chain'">
                  <GitBranch :size="15" />
                  Chain
                </button>
              </div>

              <label class="id-input compact-id">
                <Link2 :size="16" />
                <span>OJ ID</span>
                <input v-model.number="questionId" type="number" min="1" placeholder="可选" />
              </label>
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
