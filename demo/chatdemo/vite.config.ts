import path from 'node:path'

import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [vue()],
  base: '/demo/',
  build: {
    outDir: path.resolve(__dirname, '../../internal/access/api/demoui/dist'),
    emptyOutDir: true,
  },
  server: {
    fs: {
      strict: false
    }
  }
})
