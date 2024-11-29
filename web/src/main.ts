import { createApp } from 'vue'
import './style.css'
import App from './App.vue'

import router from './router'
import APIClient from './services/APIClient'


if (process.env.NODE_ENV === "development") {
    APIClient.shared.config.apiURL = "http://localhost:5300" // 本地调试用，正式环境请注释掉
}



const app = createApp(App)

app.use(router)

app.mount('#app')
