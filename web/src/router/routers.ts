import { staticRouter } from '@/router/static-router';

import { setupLayouts } from 'virtual:meta-layouts';

import generatedRoutes from 'virtual:generated-pages';

import type { RouteRecordRaw } from 'vue-router';

const routes: RouteRecordRaw[] = setupLayouts(
  generatedRoutes.filter(item => {
    return item.meta?.enabled !== false && item.meta?.constant !== true && item.meta?.layout !== false;
  })
);

export default [...staticRouter, ...routes];
