import { defineConfig } from 'vite';
import { fileURLToPath } from 'node:url';
export default defineConfig({
  base: '/mqttdemo/',
  build: { outDir: fileURLToPath(new URL('../../internal/access/api/demoui/mqttdist', import.meta.url)), emptyOutDir: true },
});
