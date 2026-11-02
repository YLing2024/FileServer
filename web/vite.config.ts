import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

// 开发时把 API 与 HLS 请求转发到本地 Go 服务；生产构建仍由 Go 同源提供，
// 因此 base 固定为 '/'，构建产物直接嵌入 internal/server/web-dist。
export default defineConfig({
  base: '/',
  plugins: [react()],
  build: {
    outDir: '../internal/server/web-dist',
    emptyOutDir: true,
    // 前端挂在 /v2/：base 仍为 '/'，用 assetsDir 让资源 URL 自然落到 /v2/assets。
    assetsDir: 'v2/assets',
    sourcemap: false,
    target: 'es2022',
  },
  server: {
    host: '127.0.0.1',
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: false,
      },
      '/hls': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: false,
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    css: false,
    restoreMocks: true,
  },
});
