<script name="ToolBarLeft" lang="ts" setup>
import { useGlobalStore } from '@/stores/modules/global';
import Breadcrumb from './components/Breadcrumb.vue';
const globalStore = useGlobalStore();
</script>

<template>
  <div class="tool-bar-lf">
    <Breadcrumb v-if="globalStore.breadcrumb" id="breadcrumb" />
  </div>
</template>

<style scoped lang="scss">
.tool-bar-lf {
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  white-space: nowrap;
}
</style>
