import { defineConfig } from 'vite';
import { brandingPlugin } from '../shared/branding/vite-plugin';

export default defineConfig({
  plugins: [brandingPlugin()],
  server: {
    port: 3002,
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
  build: {
    outDir: 'dist',
  },
});
