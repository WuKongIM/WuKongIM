import { createPinia } from 'pinia';

import { createPersistedState } from 'pinia-plugin-persistedstate';

import { NAME_SPACE } from '@/config';

const pinia = createPinia();
pinia.use(
  createPersistedState({
    key: storeKey => `${NAME_SPACE}-${storeKey}`,
    storage: localStorage
  })
);

export default pinia;
