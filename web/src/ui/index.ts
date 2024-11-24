import { setupElementPlus } from './element-plus';
import { setupVxeTable, setupVxeUI } from './vxe-table';

import type { App } from 'vue';

export function setupUi(app: App) {
  app.use(setupElementPlus);
  app.use(setupVxeUI);
  app.use(setupVxeTable);
}
