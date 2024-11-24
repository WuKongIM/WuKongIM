<script lang="ts" setup>
import { getBrowserLang } from '@/utils';
import { useGlobalStore } from '@/stores/modules/global';

import { LanguageType } from '@/stores/interface';
import { useTheme } from '@/hooks/useTheme';

import { useI18n } from 'vue-i18n';

import zhCn from 'element-plus/es/locale/lang/zh-cn';
import en from 'element-plus/es/locale/lang/en';

// init theme
const { initTheme } = useTheme();
initTheme();

const globalStore = useGlobalStore();

const locale = computed(() => {
  if (globalStore.language == 'zh') return zhCn;
  if (globalStore.language == 'en') return en;
  return getBrowserLang() == 'zh' ? zhCn : en;
});

const assemblySize = computed(() => globalStore.assemblySize);

const buttonConfig = reactive({ autoInsertSpace: false });
const i18n = useI18n();

onMounted(() => {
  const language = globalStore.language ?? getBrowserLang();
  i18n.locale.value = language;
  globalStore.setGlobalState('language', language as LanguageType);
});
</script>

<template>
  <el-config-provider :locale="locale" :size="assemblySize" :button="buttonConfig">
    <router-view></router-view>
  </el-config-provider>
</template>
