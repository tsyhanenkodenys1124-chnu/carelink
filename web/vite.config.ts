import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Під час розробки запити /api йдуть на сервер Go без переписування шляху:
// у продакшні той самий бінарник роздає і статику, і API за тим самим префіксом.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
  },
});
