import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': {
        // Force IPv4 to avoid Windows resolving `localhost` -> `::1` (IPv6) and causing ECONNREFUSED
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/api/, ''),
      },
      '/static': {
        // 代理静态文件（视频/封面），与 /api 保持同一后端，避免跨域/绝对URL问题
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
    },
  },
})
