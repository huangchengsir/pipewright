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
})
