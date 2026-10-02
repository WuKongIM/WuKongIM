import { defineConfig } from 'vite';
import { fileURLToPath } from 'node:url';
export default defineConfig({
  base: '/supportdemo/',
  build: {outDir: fileURLToPath(new URL('../../internal/access/api/demoui/supportdist', import.meta.url)), emptyOutDir: true},
});
