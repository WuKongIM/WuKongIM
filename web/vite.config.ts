import { defineConfig, UserConfig } from 'vite';

import { getSrcPath, setupVitePlugins, __APP_INFO__ } from './build';

// https://vitejs.dev/config/
export default defineConfig((): UserConfig => {
  return {
    base: '/web',
    resolve: {
      alias: {
        '@': getSrcPath()
      }
    },
    define: {
      __APP_INFO__: JSON.stringify(__APP_INFO__)
    },
    plugins: setupVitePlugins(),
    css: {
      postcss: {
        plugins: [
          {
            postcssPlugin: 'internal:charset-removal',
            AtRule: {
              charset: atRule => {
                if (atRule.name === 'charset') {
                  atRule.remove();
                }
              }
            }
          }
        ]
      },
      preprocessorOptions: {
        scss: {
          api: 'modern-compiler',
          additionalData: `@use "@/styles/var.scss" as *;`
        }
      }
    },
    server: {
      host: '0.0.0.0'
    },
    build: {
      cssCodeSplit: false,
      sourcemap: false,
      emptyOutDir: true,
      chunkSizeWarningLimit: 1500,
      rollupOptions: {
        output: {
          chunkFileNames: 'static/js/[name]-[hash].js',
          entryFileNames: 'static/js/[name]-[hash].js',
          assetFileNames: 'static/[ext]/[name]-[hash].[ext]',
          manualChunks: {
            // 分包配置，配置完成自动按需加载
            vue: ['vue', 'vue-router', 'pinia', 'vue-i18n', 'element-plus'],
            echarts: ['echarts'],
          }
        }
      }
    }
  };
});
