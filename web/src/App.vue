<script  lang="ts" setup>

import { onMounted } from 'vue';
import { RouterLink, RouterView } from 'vue-router'
import APIClient from './services/APIClient';
import { WKSDK } from 'wukongimjssdk/lib/sdk';


const requestConnect = (uid: string, apiURL: string) => {
  // 获取IM的长连接地址
  APIClient.shared.get(`${apiURL}/route`, {
    param: { uid: uid }
  }).then((res) => {
    console.log(res)
    connectIM(uid, res.ws_addr)

  }).catch((err) => {
    console.log(err)
    alert(err.msg)
  })
}

const connectIM = (uid: string, wsAddr: string) => {
  const config = WKSDK.shared().config
  config.uid = uid
  config.token = "xxx"
  config.addr = wsAddr
  WKSDK.shared().config = config
  console.log("connectIM........")
  WKSDK.shared().connect()
}

APIClient.shared.get("/api/varz").then((res) => {
  console.log(res)
  requestConnect("____manager", res.api_url)
}).catch((err) => {
  console.log(err)
  alert(err.msg)
})


</script>

<template>
  <div class="flex-col min-h-screen w-screen bg-gray-50">
    <!-- nav -->
    <div class="h-[4rem] w-full flex items-center fixed bg-gray-50">
      <img src="@/assets/logo.png" class="w-10 h-10 ml-[3rem]">
      <div class="font-bold text-xl ml-2">悟空IM</div>
    </div>
    <main class="flex pt-[4rem] w-full min-h-screen">
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
  </div>
</template>
