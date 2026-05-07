import { defineConfig } from 'vite';
import { brandingPlugin } from '../shared/branding/vite-plugin';

export default defineConfig({
  plugins: [brandingPlugin()],
  server: {
    port: 3004,
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
  build: {
    outDir: 'dist',
    // Unique assetsDir so /assets/<hash>.js doesn't collide with chat
    // when both apps share the kai.emai.dev host.
    assetsDir: 'workspace-assets',
  },
});
