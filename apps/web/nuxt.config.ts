export default defineNuxtConfig({
  compatibilityDate: '2026-05-31',
  devtools: { enabled: true },
  css: ['~/assets/css/main.css'],
  runtimeConfig: {
    public: {
      agentApiBase: process.env.NUXT_PUBLIC_AGENT_API_BASE || 'http://localhost:8088',
    },
  },
})
