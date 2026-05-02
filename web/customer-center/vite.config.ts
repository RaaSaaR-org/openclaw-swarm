import { defineConfig } from 'vite';

export default defineConfig({
  server: {
    port: 3004,
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
  build: {
    outDir: 'dist',
  },
});
