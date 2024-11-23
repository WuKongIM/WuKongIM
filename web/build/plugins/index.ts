import type { PluginOption } from 'vite';
import vue from '@vitejs/plugin-vue';
import vueJsx from '@vitejs/plugin-vue-jsx';
import VueDevtools from 'vite-plugin-vue-devtools';
import unocss from '@unocss/vite';
import unplugin from './unplugin';

import createHtml from './html';
import createLayouts from './layouts';
import createPage from './pages';
/**
 * vite插件
 * @param viteEnv - 环境变量配置
 */
export function setupVitePlugins(): (PluginOption | PluginOption[])[] {
  const plugins = [
    vue(),
    vueJsx(),
    VueDevtools(),
    ...unplugin(),
    unocss()
  ];

  plugins.push(createHtml());
  plugins.push(createLayouts());
  plugins.push(createPage());
  return plugins;
}