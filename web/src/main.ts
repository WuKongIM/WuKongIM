import './assets/main.css'

import { createApp } from 'vue'
import App from './App.vue'
import router from './router'
import './index.css'

// APIClient.shared.config.apiURL = "http://127.0.0.1:5300"  // 本地调试用，正式需要注释掉

const app = createApp(App)

app.use(router)

app.mount('#app')
