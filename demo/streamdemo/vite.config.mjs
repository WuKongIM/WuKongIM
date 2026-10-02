import { defineConfig } from 'vite';
import { fileURLToPath } from 'node:url';
import { createModelProxy } from './model-proxy.mjs';

const proxy = createModelProxy();

export default defineConfig({
  base: '/streamdemo/',
  plugins: [{
    name: 'local-model-proxy',
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        if (req.url?.split('?')[0] === '/streamdemo/api/chat') void proxy(req, res);
        else next();
      });
      server.httpServer?.once('close', () => proxy.abortAll());
    },
  }],
  build: {
    outDir: fileURLToPath(new URL('../../internal/access/api/demoui/streamdist', import.meta.url)),
    emptyOutDir: true,
  },
});
