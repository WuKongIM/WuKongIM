<template>
    <div class="w-full h-full border border-blue-500 rounded-md shadow-md grid grid-rows-3 grid-cols-[auto,1fr] bg-white">
        <div class="row-span-3 p-2" v-html="data.icon"></div>
        <div class="font-semibold flex items-center text-[0.9rem] justify-between">
            <div>{{data.name}}</div>
        </div>
        <div class="text text-sm text-gray-400 flex items-center">{{data.description}}</div>
        <div class="text text-sm text-gray-400 flex items-center">{{data.time}}</div>
    </div>
</template>

<script setup lang="ts">
import { inject, onMounted,ref } from 'vue';
const getNode = inject<() => any>('getNode');
const data = ref({name: '', time: '',icon:'',duration:0,description:''});
onMounted(() => {
  const node = getNode!();
  data.value = node.data;
});



</script>