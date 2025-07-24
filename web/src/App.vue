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
      <div class="flex-col w-80 z-50 overflow-y-auto h-[calc(100vh-4rem)]">
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
        <div class="w-full  flex-col justify-start text-left pt-5">
          <div class="w-full flex-col">
            <div class="w-full flex   pl-6">
              <label class="text-[1.1rem] font-semibold">分布式</label>
            </div>

            <ul className="menu  w-56 rounded-box">
              <li>
                <RouterLink to="/cluster/nodes">
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                    stroke-width="1" stroke-linecap="round" stroke-linejoin="round"
                    class="lucide lucide-server h-5 w-5">
                    <rect width="20" height="8" x="2" y="2" rx="2" ry="2" />
                    <rect width="20" height="8" x="2" y="14" rx="2" ry="2" />
                    <line x1="6" x2="6.01" y1="6" y2="6" />
                    <line x1="6" x2="6.01" y1="18" y2="18" />
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
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                    stroke-width="1" stroke-linecap="round" stroke-linejoin="round"
                    class="lucide lucide-settings-2 h-5 w-5">
                    <path d="M20 7h-9" />
                    <path d="M14 17H5" />
                    <circle cx="17" cy="17" r="3" />
                    <circle cx="7" cy="7" r="3" />
                  </svg>
                  <label class="ml-2">配置</label>
                </RouterLink>
              </li>
            </ul>

          </div>
        </div>

        <!-- <div class="w-full bg-gray-100 mt-0" style="height: 1px;"></div> -->
        <div class="w-full  flex-col justify-start text-left pt-5">
          <div class="w-full flex-col">
            <div class="w-full flex   pl-6">
              <label class="text-[1.1rem] font-bold">数据</label>
            </div>

            <ul className="menu  w-56 rounded-box">
              <li>
                <RouterLink to="/data/connection">
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                    stroke-width="1" stroke-linecap="round" stroke-linejoin="round"
                    class="lucide lucide-activity h-5 w-5">
                    <path
                      d="M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2" />
                  </svg>
                  <label class="ml-2">连接（Top100）</label>
                </RouterLink>
              </li>
              <li>
                <RouterLink to="/data/user">
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                    stroke-width="1" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-user h-5 w-5">
                    <path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2" />
                    <circle cx="12" cy="7" r="4" />
                  </svg>
                  <label class="ml-2">用户</label>
                </RouterLink>
              </li>
              <li>
                <RouterLink to="/data/device">
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                    stroke-width="1" stroke-linecap="round" stroke-linejoin="round"
                    class="lucide lucide-smartphone h-5 w-5">
                    <rect width="14" height="20" x="5" y="2" rx="2" ry="2" />
                    <path d="M12 18h.01" />
                  </svg>
                  <label class="ml-2">设备</label>
                </RouterLink>
              </li>
              <li>
                <RouterLink to="/data/message">
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                    stroke-width="1" stroke-linecap="round" stroke-linejoin="round"
                    class="lucide lucide-message-square-more h-5 w-5">
                    <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
                    <path d="M8 10h.01" />
                    <path d="M12 10h.01" />
                    <path d="M16 10h.01" />
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
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                    stroke-width="1" stroke-linecap="round" stroke-linejoin="round"
                    class="lucide lucide-message-circle-dashed h-5 w-5">
                    <path d="M13.5 3.1c-.5 0-1-.1-1.5-.1s-1 .1-1.5.1" />
                    <path d="M19.3 6.8a10.45 10.45 0 0 0-2.1-2.1" />
                    <path d="M20.9 13.5c.1-.5.1-1 .1-1.5s-.1-1-.1-1.5" />
                    <path d="M17.2 19.3a10.45 10.45 0 0 0 2.1-2.1" />
                    <path d="M10.5 20.9c.5.1 1 .1 1.5.1s1-.1 1.5-.1" />
                    <path d="M3.5 17.5 2 22l4.5-1.5" />
                    <path d="M3.1 10.5c0 .5-.1 1-.1 1.5s.1 1 .1 1.5" />
                    <path d="M6.8 4.7a10.45 10.45 0 0 0-2.1 2.1" />
                  </svg>
                  <label class="ml-2">会话</label>
                </RouterLink>
              </li>

              <li>
                <RouterLink to="/data/tag">
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                    stroke-width="1" stroke-linecap="round" stroke-linejoin="round"
                    class="lucide lucide-bookmark h-5 w-5">
                    <path d="m19 21-7-4-7 4V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2v16z" />
                  </svg>
                  <label class="ml-2">标签</label>
                </RouterLink>
              </li>

              <li>
                <RouterLink to="/data/plugin">
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                    stroke-width="1" stroke-linecap="round" stroke-linejoin="round"
                    class="lucide lucide-toy-brick h-5 w-5">
                    <rect width="18" height="12" x="3" y="8" rx="1" />
                    <path d="M10 8V5c0-.6-.4-1-1-1H6a1 1 0 0 0-1 1v3" />
                    <path d="M19 8V5c0-.6-.4-1-1-1h-3a1 1 0 0 0-1 1v3" />
                  </svg>
                  <label class="ml-2">插件</label>
                </RouterLink>
              </li>

              <li>
                <RouterLink to="/data/ai">
                  <svg xmlns="http://www.w3.org/2000/svg"  viewBox="0 0 24 24" fill="none"
                    stroke="currentColor" stroke-width="1" stroke-linecap="round" stroke-linejoin="round"
                    class="lucide lucide-bot h-5 w-5">
                    <path d="M12 8V4H8" />
                    <rect width="16" height="12" x="4" y="8" rx="2" />
                    <path d="M2 14h2" />
                    <path d="M20 14h2" />
                    <path d="M15 13v2" />
                    <path d="M9 13v2" />
                  </svg>
                  <label class="ml-2">AI</label>
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
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                    stroke-width="1" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-dock h-5 w-5">
                    <path d="M2 8h20" />
                    <rect width="20" height="16" x="2" y="4" rx="2" />
                    <path d="M6 16h12" />
                  </svg>
                  <label class="ml-2">应用</label>
                </RouterLink>
              </li>
              <li>
                <RouterLink to="/monitor/system">
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                    stroke-width="1" stroke-linecap="round" stroke-linejoin="round"
                    class="lucide lucide-monitor-cog h-5 w-5">
                    <path d="M12 17v4" />
                    <path d="m15.2 4.9-.9-.4" />
                    <path d="m15.2 7.1-.9.4" />
                    <path d="m16.9 3.2-.4-.9" />
                    <path d="m16.9 8.8-.4.9" />
                    <path d="m19.5 2.3-.4.9" />
                    <path d="m19.5 9.7-.4-.9" />
                    <path d="m21.7 4.5-.9.4" />
                    <path d="m21.7 7.5-.9-.4" />
                    <path d="M22 13v2a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h7" />
                    <path d="M8 21h8" />
                    <circle cx="18" cy="6" r="3" />
                  </svg>
                  <label class="ml-2">系统</label>
                </RouterLink>
              </li>
              <li>
                <RouterLink to="/monitor/cluster">
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                    stroke-width="1" stroke-linecap="round" stroke-linejoin="round"
                    class="lucide lucide-chart-network h-5 w-5">
                    <path d="m13.11 7.664 1.78 2.672" />
                    <path d="m14.162 12.788-3.324 1.424" />
                    <path d="m20 4-6.06 1.515" />
                    <path d="M3 3v16a2 2 0 0 0 2 2h16" />
                    <circle cx="12" cy="6" r="2" />
                    <circle cx="16" cy="12" r="2" />
                    <circle cx="9" cy="15" r="2" />
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
