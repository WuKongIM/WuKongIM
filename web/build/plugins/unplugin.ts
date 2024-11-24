import AutoImport from 'unplugin-auto-import/vite';
import Components from 'unplugin-vue-components/rollup';
import setupExtend from 'unplugin-vue-setup-extend-plus/vite';
import { VxeResolver, lazyImport } from 'vite-plugin-lazy-import';

export default function unplugin() {
  return [
    AutoImport({
      include: [/\.[tj]sx?$/, /\.vue\?vue/, /\.md$/],
      imports: ['vue', 'vue-router', 'pinia'],
      resolvers: [],
      dts: 'src/types/auto-imports.d.ts'
    }),
    Components({
      include: [/\.vue$/, /\.vue\?vue/, /\.md$/],
      resolvers: [],
      dts: 'src/types/components.d.ts'
    }),
    lazyImport({
      resolvers: [
        VxeResolver({
          libraryName: 'vxe-table'
        }),
        VxeResolver({
          libraryName: 'vxe-pc-ui'
        })
      ]
    }),
    setupExtend({})
  ];
}
