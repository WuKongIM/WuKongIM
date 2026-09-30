import { createApp } from 'vue'
import './style.css'
import App from './App.vue'

import router from './router/index'
import { locale, t } from './i18n'
import { initDataSource } from './services/datasource'

import {orderMessage,CustomMessage}  from "./customessage"
import WKSDK from 'wukongimjssdk'


// 注册自定义消息
WKSDK.shared().register(orderMessage,()=>new CustomMessage());

document.documentElement.lang = locale === 'zh' ? 'zh-CN' : 'en'
document.title = t('appTitle')

const appVue = createApp(App)
appVue.use(router)
appVue.mount('#app')

initDataSource()

