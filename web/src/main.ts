import './assets/main.css'

import { createApp } from 'vue'
import App from './App.vue'
import router from './router'
import './index.css'
import APIClient from './services/APIClient'

// APIClient.shared.config.apiURL = "http://175.27.245.108:5300" 

const app = createApp(App)

app.use(router)

app.mount('#app')
