import { defineConfig } from 'astro/config';

// Static-only build — the marketing site never needs server-side rendering;
// every page is generated at build time and served from a CDN.
// `site` and `base` are overridden in the deployment overlay (swarm-cloud)
// when the real production domain lands.
export default defineConfig({
  site: 'https://kai.example.org',
  output: 'static',
  trailingSlash: 'never',
  build: {
    format: 'directory',
  },
  // Astro's compress integration would normally land here; deferring until
  // the production overlay picks it up so the public swarm repo stays
  // toolchain-light.
});
