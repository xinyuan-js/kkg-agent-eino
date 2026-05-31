<script setup lang="ts">
import { Bot, GitBranch, Link2, Play, Search, Workflow } from '@lucide/vue'

type Mode = 'graph' | 'chain'

const config = useRuntimeConfig()
const mode = ref<Mode>('graph')
const query = ref('为 KKG OJ 的一道算法题生成题解思路，并引用已有知识库材料')
const questionId = ref<number | null>(null)
const running = ref(false)
const error = ref('')
const result = ref<any>(null)

const nodes = computed(() => [
  { label: 'Normalize', active: true },
  { label: 'RAG', active: Boolean(result.value?.rag_docs?.length) },
  { label: 'KKG Tools', active: Boolean(result.value?.tool_trace?.length) },
  { label: 'Synthesis', active: Boolean(result.value?.answer) },
])

async function runAgent() {
  running.value = true
  error.value = ''
  result.value = null
  try {
    const data = await $fetch('/api/v1/agent/run', {
      baseURL: config.public.agentApiBase,
      method: 'POST',
      credentials: 'include',
      body: {
        mode: mode.value,
        query: query.value,
        question_id: questionId.value || undefined,
      },
    })
    result.value = (data as any).data
  } catch (err: any) {
    error.value = err?.data?.message || err?.message || 'request failed'
  } finally {
    running.value = false
  }
}
</script>

<template>
  <main class="shell">
    <section class="workspace">
      <div class="topbar">
        <div class="brand">
          <Bot :size="22" />
          <span>KKG Agent</span>
        </div>
        <div class="mode-switch" aria-label="orchestration mode">
          <button :class="{ selected: mode === 'graph' }" @click="mode = 'graph'">
            <Workflow :size="16" />
            Graph
          </button>
          <button :class="{ selected: mode === 'chain' }" @click="mode = 'chain'">
            <GitBranch :size="16" />
            Chain
          </button>
        </div>
      </div>

      <div class="main-grid">
        <section class="composer">
          <div class="field">
            <label for="query">Prompt</label>
            <textarea id="query" v-model="query" rows="9" />
          </div>
          <div class="run-row">
            <label class="id-input">
              <Link2 :size="16" />
              <input v-model.number="questionId" type="number" min="1" placeholder="OJ question ID" />
            </label>
            <button class="run-button" :disabled="running" @click="runAgent">
              <Play :size="17" />
              {{ running ? 'Running' : 'Run' }}
            </button>
          </div>
          <p v-if="error" class="error">{{ error }}</p>
        </section>

        <section class="graph-panel">
          <div class="panel-title">
            <Search :size="17" />
            Execution
          </div>
          <div class="flow">
            <div v-for="node in nodes" :key="node.label" class="flow-node" :class="{ active: node.active }">
              {{ node.label }}
            </div>
          </div>
          <div class="trace">
            <div v-for="item in result?.tool_trace || []" :key="item.name + item.status" class="trace-row">
              <span>{{ item.name }}</span>
              <strong>{{ item.status }}</strong>
            </div>
          </div>
        </section>
      </div>

      <section class="answer-band">
        <pre v-if="result?.answer">{{ result.answer }}</pre>
        <div v-else class="empty">Ready</div>
      </section>

      <section v-if="result?.rag_docs?.length" class="docs-grid">
        <article v-for="doc in result.rag_docs" :key="doc.id" class="doc-card">
          <div class="doc-meta">{{ doc.source }} · {{ Number(doc.score).toFixed(2) }}</div>
          <h2>{{ doc.title }}</h2>
          <p>{{ doc.content }}</p>
        </article>
      </section>
    </section>
  </main>
</template>
