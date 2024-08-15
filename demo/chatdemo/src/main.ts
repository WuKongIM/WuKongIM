import { createApp } from 'vue'
import './style.css'
import App from './App.vue'

import router from './router/index'
import { initDataSource } from './services/datasource'

import {orderMessage,CustomMessage}  from "./customessage"
import WKSDK from 'wukongimjssdk'


// 注册自定义消息
WKSDK.shared().register(orderMessage,()=>new CustomMessage());

const appVue = createApp(App)
appVue.use(router)
appVue.mount('#app')

initDataSource()


