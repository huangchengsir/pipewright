/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 前端构建为静态资源 → web/dist → 由 Go go:embed 嵌入单二进制(无前后端分离部署)。
// base 用绝对根 '/':平台始终服务于根,SPA 深路由(如 /runs/128)回退 index.html 时,
// 资源以 /assets/... 绝对解析,避免相对 './' 在二级深链下解析错位。dev 时代理 API 到 Go。
export default defineConfig({
  plugins: [vue()],
  base: '/',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/healthz': 'http://localhost:8080',
    },
  },
  // Vitest 单元/组件测试配置(内联到 vite.config,复用同一份插件与解析规则)。
  // jsdom 提供 DOM/localStorage/document.cookie;setup 注册全局 stub。
  // e2e/ 由 Playwright 拥有,排除在 Vitest 之外避免被当成单测收集。
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.{test,spec}.ts'],
    exclude: ['node_modules', 'dist', 'e2e'],
    css: false,
    clearMocks: true,
  },
})
