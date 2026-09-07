export default defineNuxtConfig({
  compatibilityDate: '2026-09-07',
  devtools: { enabled: true },
  modules: ['@vite-pwa/nuxt'],
  css: ['~/assets/css/main.css'],
  runtimeConfig: {
    public: {
      apiBase: process.env.NUXT_PUBLIC_API_BASE ?? 'http://localhost:4000',
    },
  },
  app: {
    head: {
      title: 'NOVA',
      meta: [
        { name: 'description', content: 'NOVA personal AI operating system' },
        { name: 'theme-color', content: '#020607' },
      ],
    },
  },
  pwa: {
    registerType: 'autoUpdate',
    manifest: {
      name: 'NOVA Personal AI',
      short_name: 'NOVA',
      description: 'Your personal AI operating system',
      theme_color: '#020607',
      background_color: '#020607',
      display: 'standalone',
    },
  },
});
