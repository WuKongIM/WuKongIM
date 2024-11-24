<script lang="ts" name="Main" setup>
import { storeToRefs } from 'pinia';
import { useGlobalStore } from '@/stores/modules/global';

const globalStore = useGlobalStore();
const { layout } = storeToRefs(globalStore);
// 监听布局变化，在 body 上添加相对应的 layout class
watch(
  () => layout.value,
  () => {
    const body = document.body as HTMLElement;
    body.setAttribute('class', layout.value);
  },
  { immediate: true }
);
</script>

<template>
  <el-main>
    <router-view v-slot="{ Component, route }">
      <transition appear name="fade-transform" mode="out-in">
        <component :is="Component" :key="route.fullPath" />
      </transition>
    </router-view>
  </el-main>
</template>

<style lang="scss" scoped>
.el-main {
  box-sizing: border-box;
  padding: 0;
  overflow-x: hidden;
  background-color: var(--el-bg-color-page);
}
</style>
