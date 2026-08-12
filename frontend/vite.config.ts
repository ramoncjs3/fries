import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
// 从 vitest/config 引入而不是 vite —— vite 的 defineConfig 不认识 test 字段
import { defineConfig } from 'vitest/config'

// 后端地址。默认 8080；本机 8080 被别的服务占了就 `VITE_API_TARGET=http://localhost:8081 pnpm dev`。
const apiTarget = process.env.VITE_API_TARGET ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    // jsdom 而不是浏览器：这批测试盯的是「表单初始值有没有灌进去」「拦截会不会
    // 把自己拦住」这类逻辑，不是像素。真需要看渲染效果用 make dev 开浏览器看。
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    // 只跑 src 下的测试，别把 node_modules 里的一起扫进来
    include: ['src/**/*.test.{ts,tsx}'],
  },
  server: {
    port: 5173,
    // 前端开发时把接口打到本地后端。**必须走代理而不是跨域直连**：
    // 会话在 httpOnly cookie 里，同源才带得上，CSRF 的 SameSite 也才生效。
    proxy: {
      '/api': {
        target: apiTarget,
        changeOrigin: false,
      },
    },
  },
})
