import App from '@/App.vue';
// router
import router from './router';
// pinia store
import pinia from '@/stores';
// vue i18n
import I18n from '@/i18n';

import 'uno.css';
import '@/styles/index.scss';

// element icons
import * as Icons from '@element-plus/icons-vue';
// icon-park
import { install } from '@icon-park/vue-next/es/all';

import 'vue-global-api';

import { setupUi } from '@/ui';

import { createApp } from 'vue';

const app = createApp(App);

install(app, 'i-wk');
// register the element Icons component
Object.keys(Icons).forEach(key => {
  app.component(key, Icons[key as keyof typeof Icons]);
});

app.use(router);
app.use(setupUi);
app.use(I18n);
app.use(pinia);
app.mount('#app');
