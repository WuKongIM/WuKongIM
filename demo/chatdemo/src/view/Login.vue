<script setup lang="ts">
import { ref } from 'vue'
import APIClient from '../services/APIClient'
import { useRouter } from "vue-router";
import {WKSDK} from 'wukongimjssdk/lib/sdk';
const router = useRouter();
// defineProps<{ msg: string }>()

const count = ref(0)
const apiAddr = ref('http://127.0.0.1:5001')
const username = ref('')
const password = ref('')

const login = () => {
  APIClient.shared.config.apiURL = apiAddr.value
  // 注意：这里的登录接口是悟空IM的演示接口，仅供演示使用，这些接口不应该暴露给前端，应该由后端封装后提供给前端
  APIClient.shared.post('/user/token', {
    uid: username.value, // 第三方服务端的用户唯一uid
    token: password.value||"default111111", // 第三方服务端的用户的token
    device_flag: 1, // 设备标识  0.app 1.web （相同用户相同设备标记的主设备登录会互相踢，从设备将共存）
    device_level: 0,  // 设备等级 0.为从设备 1.为主设备
  }).then((res) => {
    console.log(res)
    router.push({ path: '/chat',query:{uid:username.value,token:password.value} })
  }).catch((err) => {
     alert(err.msg)
  })
}

</script>
<template>
  <div class="hello">
    <div>
      <a href="https://githubim.com" target="_blank">
        <img src="/logo.png" class="logo" alt="Vite logo" />
      </a>
    </div>
    <p>
      悟空IM演示程序，当前SDK版本：[v{{ WKSDK.shared().config.sdkVersion}}]
    </p>
    <div class="form">
      <div class="item">
        <div class="label">
          <label>API基地址</label>
        </div>
        <div class="field">
          <input type="text" placeholder="请输入API基地址" v-model="apiAddr" />
        </div>
      </div>
      <div class="item">
        <div class="label">
          <label>登录账号</label>
        </div>
        <div class="field">
          <input type="text" placeholder="演示下，随便输，唯一即可" v-model="username" />
        </div>
      </div>
      <div class="item">
        <div class="label">
          <label>登录密码</label>
        </div>
        <div class="field">
          <input type="text" placeholder="演示下，随便输" v-model="password" />
        </div>
      </div>
      <button class="submit" v-on:click="login">登录</button>
    </div>
  </div>
</template>

<style scoped>
.form {
  width: 100%;
  margin-top: 40px;
}

.item {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  margin-top: 20px;
}

.item label {
  font-size: 17px;
}

.field input {
  width: 200px;
  height: 30px;
  border: none;
  margin-left: 20px;
  font-size: 17px;
}

.form .submit {
  margin-top: 40px;
  height: 60px;
  min-width: 300px;
  max-width: 600px;
  width: 80%;
  border: none;
  border-radius: 4px;
  color: white;
  background-color: rgb(228, 98, 64);
  font-size: 20px;
  cursor: pointer;
}

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
