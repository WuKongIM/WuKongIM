<script setup lang="ts">
import App from '../../services/App.ts';
import API from '../../services/API';
import { ref } from 'vue';

const username = ref<string>('guest')
const password = ref<string>('guest')

const onLogin = () => {
    console.log('login')

    if(!username.value|| username.value.trim()=='') {
        alert('请输入用户名')
        return
    }

    if(!password.value || password.value.trim()=='') {
        alert('请输入密码')
        return
    }

    API.shared.login(username.value, password.value).then((res) => {
        console.log(res)
        const loginInfo = App.shard().loginInfo
        loginInfo.username = res.username
        loginInfo.token = res.token
        loginInfo.setPermissionFromStr(res.permissions)
        loginInfo.save()
        window.location.href = '/web'
    }).catch((err) => {
        console.log(err)
        alert(err.msg)
    })
}

</script>

<template>
    <div class="flex flex-col items-center justify-center h-full mb-20">
        <div>
            <img src="/logo_big.png" class="h-[80px] w-[80px]" />
        </div>
        <div class="flex flex-col items-center justify-center pt-10">
            <h1 class="text-[1.5rem] font-semibold">WuKongIM分布式管理系统</h1>
            <div class="mt-4 text-gray-500">演示账号: guest 密码：guest</div>
            <div class="mt-10">
                <input type="text" class="input border-gray-200 w-[20rem]" placeholder="用户名" v-model="username" >
            </div>
            <div class="mt-5">
                <input type="password" class="input border-gray-200 w-[20rem]" placeholder="密码" v-model="password"  />
            </div>
            <div class="mt-5">
                <button class="btn btn-primary w-[10rem] mt-10" v-on:click="() => onLogin()">登录</button>
            </div>
        </div>
    </div>
</template>