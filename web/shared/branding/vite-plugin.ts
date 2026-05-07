import { copyFileSync, existsSync, mkdirSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import type { Plugin } from 'vite';

const HERE = dirname(fileURLToPath(import.meta.url));

const BRANDING_FILES = ['theme.css', 'branding.json', 'logo.svg', 'favicon.svg'] as const;

const CONTENT_TYPES: Record<string, string> = {
  css: 'text/css; charset=utf-8',
  json: 'application/json; charset=utf-8',
  svg: 'image/svg+xml',
};

export function brandingPlugin(): Plugin {
  return {
    name: 'swarm-branding',
    config() {
      return {
        resolve: {
          alias: { '@branding': HERE },
        },
      };
    },
    configureServer(server) {
      server.middlewares.use('/branding/', (req, res, next) => {
        const url = (req.url || '/').split('?')[0].replace(/^\//, '');
        if (!BRANDING_FILES.includes(url as typeof BRANDING_FILES[number])) {
          return next();
        }
        const file = resolve(HERE, url);
        if (!existsSync(file)) return next();
        const ext = url.split('.').pop() || '';
        if (CONTENT_TYPES[ext]) res.setHeader('Content-Type', CONTENT_TYPES[ext]);
        res.setHeader('Cache-Control', 'no-cache');
        res.end(readFileSync(file));
      });
    },
    closeBundle() {
      const outDir = resolve(process.cwd(), 'dist', 'branding');
      mkdirSync(outDir, { recursive: true });
      for (const f of BRANDING_FILES) {
        copyFileSync(resolve(HERE, f), resolve(outDir, f));
      }
    },
  };
}
