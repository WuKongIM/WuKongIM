import { defineConfig } from 'vite';
import { fileURLToPath } from 'node:url';
export default defineConfig({
  base: '/agentdemo/',
  build: {outDir: fileURLToPath(new URL('../../internal/access/api/demoui/agentdist', import.meta.url)), emptyOutDir: true},
});
