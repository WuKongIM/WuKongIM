

<script setup lang="ts">

import {ref } from 'vue';

const props = defineProps({
    login: {
        type: Function,
        required: true
    }
})

const managerToken = ref<string>();

const login = () => {
    if(!managerToken.value || managerToken.value === '') {
        alert("Please enter Manager Token")
        return
    }
    props.login(managerToken.value)
}


</script>

<template>
    <div class="flex flex-col min-h-screen w-screen bg-gray-50 justify-center items-center">
        <div class="w-[40rem] mt-[-10rem] h-80 flex flex-col items-center">
            <div class="mt-4">
                <img src="../assets/logo.svg" class="w-[6rem] rounded-2xl"/>
            </div>
            <div class="mt-8">
                <div class="mt-4">
                    <input v-model="managerToken" type="text" placeholder="Manager Token" class="w-[20rem] h-10 rounded-2xl px-4 py-2 bg-gray-100"/>
                </div>
                <div class="mt-4">
                    <button class="w-[20rem] h-10 rounded-2xl px-4 py-2 bg-[#F66948] text-white" v-on:click="login">Login</button>
                </div>
            </div>
        </div>
    </div>
</template> 