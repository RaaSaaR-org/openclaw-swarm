import { defineConfig } from 'vite';
import { brandingPlugin } from '../shared/branding/vite-plugin';

export default defineConfig({
  plugins: [brandingPlugin()],
  server: {
    port: 3000,
    // SPA fallback — all routes serve index.html
    historyApiFallback: true,
  },
  build: {
    outDir: 'dist',
  },
});
