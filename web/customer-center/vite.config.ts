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
    // Unique assetsDir so /assets/<hash>.js doesn't collide with customer-chat
    // when both apps share the kai.emai.dev host.
    assetsDir: 'center-assets',
  },
});
