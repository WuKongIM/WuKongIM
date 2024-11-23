<script name="WkSpanNode" lang="ts" setup>
import { inject, onMounted, ref } from 'vue';
const getNode = inject<() => any>('getNode');
const data = ref({ name: '', time: '', icon: '', duration: 0, description: '' });
onMounted(() => {
  const node = getNode!();
  data.value = node.data;
});
</script>

<template>
  <div class="w-full h-full flex bg-white p-4px">
    <div v-if="data.icon" v-html="data.icon" class="pr-4px pt-8px"></div>
    <div class="flex-1">
      <div class="font-semibold flex items-center text-16px justify-between">
        <div>{{ data.name }}</div>
      </div>
      <div class="text text-sm text-gray-400 flex items-center">{{ data.description }}</div>
      <div class="text text-sm text-gray-400 flex items-center">{{ data.time }}</div>
    </div>
  </div>
</template>
