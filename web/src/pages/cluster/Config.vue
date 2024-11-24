<script setup lang="ts">
// API 接口
import { clusterApi } from '@/api/modules/cluster-api';
import VueJsonPretty from 'vue-json-pretty';
import 'vue-json-pretty/lib/styles.css';

const config = ref<any>({});

const getConfig = async () => {
  config.value = await clusterApi.clusterConfig();
};

onMounted(() => {
  getConfig();
});
</script>

<template>
  <wk-page class="flex-col overflow-hidden">
    <div class="flex-1 card">
      <VueJsonPretty :data="config" />
    </div>
  </wk-page>
</template>

<style scoped lang="scss"></style>

<route lang="yaml">
meta:
  title: 配置
</route>
