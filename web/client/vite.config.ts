import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

export default defineConfig(({ mode }) => {
  // 从 web/ 根目录 + 仓库根目录加载 .env（后者覆盖）
  const webEnv = loadEnv(mode, path.resolve(__dirname, '..'), '');
  const repoEnv = loadEnv(mode, path.resolve(__dirname, '../..'), '');
  const apiPort = repoEnv.PORT || webEnv.PORT || '3000';
  const apiTarget = `http://localhost:${apiPort}`;

  return {
    plugins: [react()],
    resolve: {
      alias: {
        '@': path.resolve(__dirname, './src'),
      },
    },
    server: {
      host: '0.0.0.0',
      port: 5173,
      proxy: {
        '/api': {
          target: apiTarget,
          changeOrigin: true,
        },
        '/uploads': {
          target: apiTarget,
          changeOrigin: true,
        },
      },
    },
  };
});
