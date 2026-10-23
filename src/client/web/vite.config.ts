import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 管理服务地址（开发期通过 Vite 代理访问，避免浏览器跨域）
const MANAGER_TARGET = 'http://127.0.0.1:8080'

// Web 控制台的 URL 前缀：系统不再占用根路径，所有页面都在 /bastion/* 下。
// 与 router/index.ts 的 WEB_PREFIX、manager 侧静态托管的 BASTION_WEB_PREFIX 保持一致。
const WEB_PREFIX = '/bastion/'

export default defineConfig({
  // base 决定构建产物里静态资源的引用前缀，以及路由的基准路径
  base: WEB_PREFIX,
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    strictPort: false,
    proxy: {
      '/api': {
        target: MANAGER_TARGET,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    chunkSizeWarningLimit: 1200,
  },
})
