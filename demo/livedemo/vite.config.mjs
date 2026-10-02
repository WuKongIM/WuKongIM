import { defineConfig } from 'vite';
import { fileURLToPath } from 'node:url';
export default defineConfig({
  base: '/livedemo/',
  build: { outDir: fileURLToPath(new URL('../../internal/access/api/demoui/livedist', import.meta.url)), emptyOutDir: true },
});
