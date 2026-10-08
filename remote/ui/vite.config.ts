import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 产物给 remote/ui/embed.go 嵌进 relay 二进制。
// 相对路径是必须的：relay 把页面挂在 "/" 上，但反代可能挂在子路径下。
export default defineConfig({
  base: './',
  plugins: [vue()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    host: '127.0.0.1',
    port: 5199,
    strictPort: true,
    // 开发时把 API/WS 转发给本地的 relay，省得每次改前端都重新打包
    proxy: {
      '/api': 'http://127.0.0.1:8080',
      '/ws': { target: 'ws://127.0.0.1:8080', ws: true },
    },
  },
})
