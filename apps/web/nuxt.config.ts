export default defineNuxtConfig({
  compatibilityDate: '2026-05-31',
  devtools: { enabled: false },
  css: ['~/assets/css/main.css'],
  nitro: {
    routeRules: {
      '/agent-api/**': {
        proxy: `${process.env.NUXT_AGENT_API_TARGET || 'http://api:8088'}/api/v1/**`,
      },
    },
  },
  runtimeConfig: {
    public: {
      agentApiBase: process.env.NUXT_PUBLIC_AGENT_API_BASE || '/agent-api',
    },
  },
})
