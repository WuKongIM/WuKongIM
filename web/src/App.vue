<script  lang="ts" setup>

import { RouterLink, RouterView } from 'vue-router'
import { ref } from 'vue';
import APIClient from './services/APIClient';
import { ConnectStatus, WKSDK } from 'wukongimjssdk';
import { useCookies } from "vue3-cookies";
import Login from './components/Login.vue'
import { onMounted, onUnmounted } from 'vue';


const { cookies } = useCookies()
const logined = ref<Boolean>(false)
const varz = ref<any>()

// token获取回调
APIClient.shared.config.tokenCallback = () => {

  return getToken()
}

const getToken = () => {
  return cookies.get("token")
}

// 是否登录
const isLogin = () => {
  const token = getToken()
  if (token && token.length > 0) {
    return true
  }
  return false
}


const login = (managerToken: string) => {
  cookies.set("token", managerToken)
  toLogin()
}


const connectIM = (uid: string, wsAddr: string) => {
  console.log("connectIM", uid, wsAddr)
  const config = WKSDK.shared().config
  config.uid = uid
  config.token = getToken()
  config.addr = wsAddr
  WKSDK.shared().config = config
  WKSDK.shared().connect()
}

APIClient.shared.get("/api/varz").then((res) => {
  varz.value = res
  if (res.manager_token_on === 1) {
    if (isLogin()) {
      toLogin()
      return
    }
    return
  } else {
    toLogin()
  }

}).catch((err) => {
  console.log(err)
  alert(err.msg)
})

const toLogin = () => {
  let wsAddr = varz.value.wss_addr
  if (!wsAddr || wsAddr === '') {
    wsAddr = varz.value.ws_addr
  }
  connectIM(varz.value.manager_uid, wsAddr)
}

const connectStatusListener = (status: ConnectStatus) => {
  if (status === ConnectStatus.Connected) {
    logined.value = true
  }else if(status === ConnectStatus.ConnectFail) {
    logined.value = false
    alert("连接失败")
  }
}

onUnmounted(() => {
  WKSDK.shared().connectManager.removeConnectStatusListener(connectStatusListener)
  cookies.remove("token")
  WKSDK.shared().disconnect()
})



onMounted(() => {
  APIClient.shared.logoutCallback = () => {
    cookies.remove("token")
    window.location.reload()
  }
  WKSDK.shared().connectManager.addConnectStatusListener(connectStatusListener)
})

</script>

<template>
  <div class="flex-col min-h-screen w-screen bg-gray-50">
    <!-- nav -->
    <div class="h-[4rem] w-full flex items-center fixed bg-gray-50" v-if="logined">
      <img src="@/assets/logo.png" class="w-10 h-10 ml-[3rem]">
      <div class="font-bold text-xl ml-2">悟空IM</div>
    </div>
    <main class="flex pt-[4rem] w-full min-h-screen" v-if="logined">
      <!-- left -->
      <div class="flex w-40 h-full fixed z-50">
        <!-- menus -->
        <div class="w-full h-full flex-col justify-end text-right pr-10 pt-5  bg-gray-50">
          <RouterLink to="/">
            <div class="p-2 font-bold text-[1rem]  text-gray-500 cursor-pointer">
              首页
            </div>
          </RouterLink>
          <RouterLink to="channel">
            <div class="p-2 font-bold text-[1rem] text-gray-500 cursor-pointer">
              频道
            </div>
          </RouterLink>
          <RouterLink to="message">
            <div class="p-2 font-bold text-[1rem] text-gray-500 cursor-pointer">
              消息
            </div>
          </RouterLink>
          <RouterLink to="conn">
            <div class="p-2 font-bold text-[1rem] text-gray-500 cursor-pointer">
              连接
            </div>
          </RouterLink>
          <RouterLink to="conversation">
            <div class="p-2 font-bold text-[1rem] text-gray-500 cursor-pointer">
              会话
            </div>
          </RouterLink>
        </div>
      </div>
      <!-- right -->
      <div class="flex  w-full  pr-20 pl-40 relative">
        <div class="bg-white rounded content min-w-full h-full max-w-[60rem]">
          <RouterView />
        </div>
      </div>
    </main>
    <Login :login="login" v-else></Login>
  </div>
</template>
