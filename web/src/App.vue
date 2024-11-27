<script setup lang="ts">

import { onMounted } from 'vue';
import Login from './pages/login/Login.vue';
import App from './services/App';
import APIClient from './services/APIClient';

onMounted(() => {


  if (App.shard().loginInfo.isLogin()) {
    App.shard().loadSystemSettingIfNeed()
  }

  APIClient.shared.config.tokenCallback = () => {
    return App.shard().loginInfo.token
  }

  APIClient.shared.logoutCallback = () => {
    App.shard().loginInfo.clear();
    window.location.href = '/web/login'
  }

});

const onLogout = () => {
  console.log('logout')
  App.shard().loginInfo.clear();

  window.location.href = '/web/login'
}

</script>

<template>
  <div class="flex-col min-h-screen w-screen" v-if="App.shard().loginInfo.isLogin()">
    <!-- nav -->
    <div class="h-[4rem] w-full flex items-center fixed justify-end">
      <div class="h-[4rem] w-full flex items-center">
        <img src="./assets/logo.png" class="w-10 h-10 ml-[2rem] mt-1">
        <div class="font-bold text-xl ml-2">悟空IM</div>
      </div>
      <div class="mr-20 flex">
        <label class="mt-[0.4rem] mr-2 text-sm">{{ App.shard().loginInfo.username }}</label>
        <button class="btn btn-sm" v-on:click="() => onLogout()">
          <svg width="18" height="18" viewBox="0 0 48 48" fill="none">
            <path d="M23.9917 6H6V42H24" stroke="#333" stroke-width="4" stroke-linecap="round"
              stroke-linejoin="round" />
            <path d="M33 33L42 24L33 15" stroke="#333" stroke-width="4" stroke-linecap="round"
              stroke-linejoin="round" />
            <path d="M16 23.9917H42" stroke="#333" stroke-width="4" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </button>
      </div>
    </div>
    <main class="flex pt-[4rem] w-full min-h-screen">
      <!-- left -->
      <div class="flex-col w-80 h-full  z-50">
        <!-- menus -->

        <!-- <div class="w-full h-full flex-col justify-start text-left pt-5">
          <ul class="menu  w-56 rounded-box">
            <li>
              <RouterLink to="/cluster/nodes">
                <svg class="h-5 w-5" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
                  <path d="M9 18V42H39V18L24 6L9 18Z" fill="none" stroke="#333" stroke-width="2" stroke-linecap="round"
                    stroke-linejoin="round" />
                  <path d="M19 29V42H29V29H19Z" fill="none" stroke="#333" stroke-width="2" stroke-linejoin="round" />
                  <path d="M9 42H39" stroke="#333" stroke-width="2" stroke-linecap="round" />
                </svg>
                <label class="ml-2">首页</label>
              </RouterLink>
            </li>
          </ul>
        </div> -->

        <!-- cluster -->
        <div class="w-full h-full flex-col justify-start text-left pt-5">
          <div class="w-full flex-col">
            <div class="w-full flex   pl-6">
              <label class="text-[1.1rem] font-semibold">分布式</label>
            </div>

            <ul className="menu  w-56 rounded-box">
              <li>
                <RouterLink to="/cluster/nodes">
                  <svg class="h-5 w-5" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <rect x="4" y="34" width="8" height="8" fill="none" stroke="#333" stroke-width="2"
                      stroke-linecap="round" stroke-linejoin="round" />
                    <rect x="8" y="6" width="32" height="12" fill="none" stroke="#333" stroke-width="2"
                      stroke-linecap="round" stroke-linejoin="round" />
                    <path d="M24 34V18" stroke="#333" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
                    <path d="M8 34V26H40V34" stroke="#333" stroke-width="2" stroke-linecap="round"
                      stroke-linejoin="round" />
                    <rect x="36" y="34" width="8" height="8" fill="none" stroke="#333" stroke-width="2"
                      stroke-linecap="round" stroke-linejoin="round" />
                    <rect x="20" y="34" width="8" height="8" fill="none" stroke="#333" stroke-width="2"
                      stroke-linecap="round" stroke-linejoin="round" />
                    <path d="M14 12H16" stroke="#333" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
                  </svg>
                  <label class="ml-2">节点</label>
                </RouterLink>
              </li>
              <li>
                <RouterLink to="/cluster/slots">
                  <svg xmlns="http://www.w3.org/2000/svg" class="h-5 w-5" fill="none" viewBox="0 0 24 24"
                    stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2"
                      d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                  <label class="ml-2">分区（槽）</label>
                </RouterLink>
              </li>
              <li>
                <RouterLink to="/cluster/channels">
                  <svg xmlns="http://www.w3.org/2000/svg" class="h-5 w-5" fill="none" viewBox="0 0 24 24"
                    stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2"
                      d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
                  </svg>
                  <label class="ml-2">频道</label>
                </RouterLink>
              </li>

              <li>
                <RouterLink to="/cluster/config">
                  <svg class="h-5 w-5" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <path d="M41.5 10H35.5" stroke="#333" stroke-width="4" stroke-linecap="round"
                      stroke-linejoin="round" />
                    <path d="M27.5 6V14" stroke="#333" stroke-width="4" stroke-linecap="round"
                      stroke-linejoin="round" />
                    <path d="M27.5 10L5.5 10" stroke="#333" stroke-width="2" stroke-linecap="round"
                      stroke-linejoin="round" />
                    <path d="M13.5 24H5.5" stroke="#333" stroke-width="2" stroke-linecap="round"
                      stroke-linejoin="round" />
                    <path d="M21.5 20V28" stroke="#333" stroke-width="2" stroke-linecap="round"
                      stroke-linejoin="round" />
                    <path d="M43.5 24H21.5" stroke="#333" stroke-width="2" stroke-linecap="round"
                      stroke-linejoin="round" />
                    <path d="M41.5 38H35.5" stroke="#333" stroke-width="2" stroke-linecap="round"
                      stroke-linejoin="round" />
                    <path d="M27.5 34V42" stroke="#333" stroke-width="2" stroke-linecap="round"
                      stroke-linejoin="round" />
                    <path d="M27.5 38H5.5" stroke="#333" stroke-width="2" stroke-linecap="round"
                      stroke-linejoin="round" />
                  </svg>
                  <label class="ml-2">配置</label>
                </RouterLink>
              </li>
            </ul>

          </div>
        </div>

        <!-- <div class="w-full bg-gray-100 mt-0" style="height: 1px;"></div> -->
        <div class="w-full h-full flex-col justify-start text-left pt-5">
          <div class="w-full flex-col">
            <div class="w-full flex   pl-6">
              <label class="text-[1.1rem] font-bold">数据</label>
            </div>

            <ul className="menu  w-56 rounded-box">
              <li>
                <RouterLink to="/data/connection">
                  <svg class="h-5 w-5" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <path
                      d="M37 22.0001L34 25.0001L23 14.0001L26 11.0001C27.5 9.50002 33 7.00005 37 11.0001C41 15.0001 38.5 20.5 37 22.0001Z"
                      fill="none" stroke="#333" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
                    <path d="M42 6L37 11" stroke="#333" stroke-width="2" stroke-linecap="round"
                      stroke-linejoin="round" />
                    <path
                      d="M11 25.9999L14 22.9999L25 33.9999L22 36.9999C20.5 38.5 15 41 11 36.9999C7 32.9999 9.5 27.5 11 25.9999Z"
                      fill="none" stroke="#333" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
                    <path d="M23 32L27 28" stroke="#333" stroke-width="2" stroke-linecap="round"
                      stroke-linejoin="round" />
                    <path d="M6 42L11 37" stroke="#333" stroke-width="2" stroke-linecap="round"
                      stroke-linejoin="round" />
                    <path d="M16 25L20 21" stroke="#333" stroke-width="2" stroke-linecap="round"
                      stroke-linejoin="round" />
                  </svg>
                  <label class="ml-2">连接（Top100）</label>
                </RouterLink>
              </li>
              <li>
                <RouterLink to="/data/user">
                  <svg class="h-5 w-5" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <circle cx="24" cy="12" r="8" fill="none" stroke="#333" stroke-width="2" stroke-linecap="round"
                      stroke-linejoin="round" />
                    <path d="M42 44C42 34.0589 33.9411 26 24 26C14.0589 26 6 34.0589 6 44" stroke="#333"
                      stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
                  </svg>
                  <label class="ml-2">用户</label>
                </RouterLink>
              </li>
              <li>
                <RouterLink to="/data/device">
                  <svg class="h-5 w-5" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <path d="M23 43H43V5H14V15" stroke="#333" stroke-width="2" stroke-linejoin="round" />
                    <path d="M5 15H23V43H5L5 15Z" fill="none" stroke="#333" stroke-width="2" stroke-linejoin="round" />
                    <path d="M13 37H15" stroke="#333" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
                    <path d="M28 37H30" stroke="#333" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
                  </svg>
                  <label class="ml-2">设备</label>
                </RouterLink>
              </li>
              <li>
                <RouterLink to="/data/message">
                  <svg class="h-5 w-5" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <path
                      d="M44 24C44 35.0457 35.0457 44 24 44C18.0265 44 4 44 4 44C4 44 4 29.0722 4 24C4 12.9543 12.9543 4 24 4C35.0457 4 44 12.9543 44 24Z"
                      fill="none" stroke="#333" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
                    <path d="M13.9999 26L20 32L33 19" stroke="#333" stroke-width="2" stroke-linecap="round"
                      stroke-linejoin="round" />
                  </svg>
                  <label class="ml-2">消息</label>
                </RouterLink>
              </li>
              <li>
                <RouterLink to="/data/channel">
                  <svg xmlns="http://www.w3.org/2000/svg" class="h-5 w-5" fill="none" viewBox="0 0 24 24"
                    stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2"
                      d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
                  </svg>
                  <label class="ml-2">频道</label>
                </RouterLink>
              </li>

              <li>
                <RouterLink to="/data/conversation">
                  <svg width="20" height="20" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <path d="M44 6H4V36H13V41L23 36H44V6Z" fill="none" stroke="#333" stroke-width="2"
                      stroke-linecap="round" stroke-linejoin="round" />
                    <path d="M14 21H34" stroke="#333" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
                  </svg>
                  <label class="ml-2">会话</label>
                </RouterLink>
              </li>
            </ul>

          </div>
        </div>

        <div class="w-full h-full flex-col justify-start text-left pt-5">
          <div class="w-full flex-col">
            <div class="w-full flex   pl-6">
              <label class="text-[1.1rem] font-bold">监控</label>
            </div>

            <ul className="menu  w-56 rounded-box">
              <li>
                <RouterLink to="/monitor/app">
                  <svg class="h-5 w-5" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <path d="M44 5H3.99998V17H44V5Z" fill="none" stroke="#333" stroke-width="2"
                      stroke-linejoin="round" />
                    <path d="M3.99998 41.0301L16.1756 28.7293L22.7549 35.0301L30.7982 27L35.2786 31.368" stroke="#333"
                      stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
                    <path d="M44 16.1719V42.1719" stroke="#333" stroke-width="2" stroke-linecap="round" />
                    <path d="M3.99998 16.1719V30.1719" stroke="#333" stroke-width="2" stroke-linecap="round" />
                    <path d="M13.0155 43H44" stroke="#333" stroke-width="2" stroke-linecap="round" />
                    <path d="M17 11H38" stroke="#333" stroke-width="2" stroke-linecap="round" />
                    <path d="M9.99998 10.9966H11" stroke="#333" stroke-width="2" stroke-linecap="round" />
                  </svg>
                  <label class="ml-2">应用</label>
                </RouterLink>
              </li>
              <li>
                <RouterLink to="/monitor/system">
                  <svg class="h-5 w-5" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <path
                      d="M24 44C35.0457 44 44 35.0457 44 24C44 12.9543 35.0457 4 24 4C12.9543 4 4 12.9543 4 24C4 35.0457 12.9543 44 24 44Z"
                      fill="none" stroke="#333" stroke-width="2" stroke-linejoin="round" />
                    <path d="M11 28.1321H16.6845L21.2234 13L24.8953 35L29.4483 24.6175L32.9127 28.1321H37" stroke="#333"
                      stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
                  </svg>
                  <label class="ml-2">系统</label>
                </RouterLink>
              </li>
              <li>
                <RouterLink to="/monitor/cluster">
                  <svg class="h-5 w-5" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <path
                      d="M43 4H5C4.44772 4 4 4.48842 4 5.09091V14.9091C4 15.5116 4.44772 16 5 16H43C43.5523 16 44 15.5116 44 14.9091V5.09091C44 4.48842 43.5523 4 43 4Z"
                      fill="none" stroke="#333" stroke-width="2" stroke-linejoin="round" />
                    <path
                      d="M43 32H5C4.44772 32 4 32.4884 4 33.0909V42.9091C4 43.5116 4.44772 44 5 44H43C43.5523 44 44 43.5116 44 42.9091V33.0909C44 32.4884 43.5523 32 43 32Z"
                      fill="none" stroke="#333" stroke-width="2" stroke-linejoin="round" />
                    <path d="M14 16V24.0083L34 24.0172V32" stroke="#333" stroke-width="2" stroke-linecap="round"
                      stroke-linejoin="round" />
                    <path d="M18 38H30" stroke="#333" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
                    <path d="M18 10H30" stroke="#333" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
                  </svg>
                  <label class="ml-2">分布式</label>
                </RouterLink>
              </li>
              <li>
                <RouterLink to="/monitor/trace">
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                    stroke-width="1" stroke-linecap="round" stroke-linejoin="round"
                    class="lucide lucide-spline h-5 w-5">
                    <circle cx="19" cy="5" r="2" />
                    <circle cx="5" cy="19" r="2" />
                    <path d="M5 17A12 12 0 0 1 17 5" />
                  </svg>
                  <label class="ml-2">消息追踪</label>
                </RouterLink>
              </li>

              <li>
                <RouterLink to="/monitor/tester">
                  <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none"
                    stroke="currentColor" stroke-width="1" stroke-linecap="round" stroke-linejoin="round"
                    class="lucide lucide-biceps-flexed">
                    <path
                      d="M12.409 13.017A5 5 0 0 1 22 15c0 3.866-4 7-9 7-4.077 0-8.153-.82-10.371-2.462-.426-.316-.631-.832-.62-1.362C2.118 12.723 2.627 2 10 2a3 3 0 0 1 3 3 2 2 0 0 1-2 2c-1.105 0-1.64-.444-2-1" />
                    <path d="M15 14a5 5 0 0 0-7.584 2" />
                    <path d="M9.964 6.825C8.019 7.977 9.5 13 8 15" />
                  </svg>
                  <label class="ml-2">压力测试</label>
                </RouterLink>
              </li>
            </ul>

          </div>
        </div>

      </div>
      <!-- right -->
      <div class="flex  w-full  pr-20 pt-2  relative">
        <div class="rounded content min-w-full max-w-[60rem] overflow-hidden h-[calc(100vh-4rem)]">
          <RouterView class="h-[100%]" />
        </div>
      </div>
    </main>
  </div>
  <div class="flex-col min-h-screen w-screen table" v-else>
    <div class="table-cell align-middle">
      <Login class="h-[100%]" />
    </div>
  </div>
</template>


<style scoped>
.logo {
  height: 6em;
  padding: 1.5em;
  will-change: filter;
  transition: filter 300ms;
}

.logo:hover {
  filter: drop-shadow(0 0 2em #646cffaa);
}

.logo.vue:hover {
  filter: drop-shadow(0 0 2em #42b883aa);
}
</style>
