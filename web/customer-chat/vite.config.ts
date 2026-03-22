import { defineConfig } from 'vite';

export default defineConfig({
  server: {
    port: 3000,
    // SPA fallback — all routes serve index.html
    historyApiFallback: true,
  },
  build: {
    outDir: 'dist',
  },
});
