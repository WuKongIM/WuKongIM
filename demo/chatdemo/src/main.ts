import { createApp } from 'vue'
import './style.css'
import App from './App.vue'

import router from './router/index'

const appVue = createApp(App)
appVue.use(router)
appVue.mount('#app')

